package workflow

import (
	"context"
	"log/slog"
	"maps"
	"reflect"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bdrtr/gobit/core/errors"
)

// ParallelStep is a composite step that runs its branches CONCURRENTLY.
//
// To the engine it is a single Step: it appears as one row in the durable
// record, the retry policy applies to the COMPOSITE rather than to the branches
// one by one, and if a branch blows up the compensation chain continues from the
// steps BEFORE the composite rather than from the composite itself.
//
// # What happens when a branch blows up
//
// Invoke rolls the successful sibling branches back WITHIN ITSELF and then
// returns the error. That keeps the engine's "a step that blew up is not
// compensated" rule intact: if the composite step returned an error, it left no
// work behind. The inner rollback goes through the same concurrent path as
// [ParallelStep.Compensate], so each branch gets its own budget.
//
// If the inner rollback BLOWS UP that claim collapses: a sibling branch's side
// effect (a stock reservation, say) is left hanging. In that case the returned
// error wraps ErrUncompensated and the engine writes the execution as
// StatusCompensationFailed rather than StatusFailed — otherwise the record would
// lie by saying "rolled back, the system is consistent" and the monitoring rule
// counting compensation_failed would never see this execution.
//
// # Shared data
//
// Because the branches run concurrently, writing to a common map would be a data
// race. Each branch therefore sees ITS OWN COPY of the parent context's Shared.
// The merge follows two rules:
//
//   - Writes are applied ONLY when every branch succeeded. If even one branch
//     blows up, no branch's writes reach the parent context; the successful ones
//     have been rolled back, and leaking the id of a rolled-back reservation to
//     the following steps (or to the previous step's Compensate) would have the
//     wrong record canceled.
//   - What is applied is not the WHOLE of the branch's copy but the keys it
//     CHANGED. Were the whole copy written back, the stale copy of a branch that
//     never touched a key would OVERWRITE the write of a sibling that updated it.
//     Between two branches that really did change the same key, the later one
//     wins.
//
// The copy is shallow: if a branch changes a map or slice value inside Shared in
// place the race is back — branches must not mutate shared values. Branches
// cannot DELETE keys from Shared; a deletion is not reflected in the parent map.
//
// # Compensation
//
// Compensate calls every branch. If a branch's compensation blows up the rest
// are still attempted; the errors are joined.
type ParallelStep struct {
	name     string
	branches []Step
	// rollbackTimeout is the time budget for Invoke's inner rollback.
	rollbackTimeout time.Duration
	log             *slog.Logger
}

var _ Step = (*ParallelStep)(nil)

// NewParallel produces a composite step that runs the given branches
// concurrently.
//
// name is the composite's name as it appears in the records. At least one branch
// has to be given; otherwise Invoke returns errors.Invalid.
func NewParallel(name string, branches ...Step) *ParallelStep {
	return &ParallelStep{
		name:            name,
		branches:        branches,
		rollbackTimeout: DefaultCompensationTimeout,
	}
}

// WithRollbackTimeout changes the branch compensations' time budget and returns
// the step.
//
// The budget is PER BRANCH and applies both to the inner rollback and to the
// compensation the engine triggers. Because branch compensations run
// CONCURRENTLY every branch gets its budget at the same time; a slow branch does
// not starve its siblings. In the engine's compensation the whole composite is
// additionally bounded by the engine's step budget (see WithCompensationTimeout)
// and this budget does not EXTEND it: if the branch budget is larger than the
// engine's remaining budget, the engine's is what actually applies. A
// non-positive value is ignored.
func (p *ParallelStep) WithRollbackTimeout(d time.Duration) *ParallelStep {
	if d > 0 {
		p.rollbackTimeout = d
	}

	return p
}

// WithLogger sets the logger the composite uses and returns the step.
//
// Without it slog.Default is used. Since the Step interface carries no logger,
// the composite has to take its own; panic stack traces are written here.
func (p *ParallelStep) WithLogger(log *slog.Logger) *ParallelStep {
	if log != nil {
		p.log = log
	}

	return p
}

// Name returns the composite's name.
func (p *ParallelStep) Name() string { return p.name }

// branchResult is a single branch's result.
type branchResult struct {
	out    any
	shared map[string]any
	err    error
}

// Invoke runs every branch concurrently.
//
// If all of them succeed the output is an []any slice of the outputs in branch
// order. If at least one blows up the successful branches are rolled back and
// the branch errors (plus any rollback errors) are returned joined together.
func (p *ParallelStep) Invoke(ctx context.Context, sc *StepContext) (any, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	if sc.Shared == nil {
		sc.Shared = make(map[string]any)
	}

	// The snapshot from before the branches started: it is where the answer to
	// "did this branch touch this key" comes from during the merge.
	snapshot := maps.Clone(sc.Shared)

	results := make([]branchResult, len(p.branches))
	var wg sync.WaitGroup
	wg.Add(len(p.branches))

	for i, b := range p.branches {
		go func() {
			defer wg.Done()

			shared := maps.Clone(sc.Shared)
			if shared == nil {
				shared = make(map[string]any)
			}
			bsc := branchContext(sc, shared, b.Name(), i)
			out, err := p.safeCall(ctx, bsc, "Invoke", func() (any, error) {
				return b.Invoke(ctx, bsc)
			})
			results[i] = branchResult{out: out, shared: shared, err: err}
		}()
	}
	wg.Wait()

	var succeeded []int
	var failures []error
	for i, r := range results {
		if r.err != nil {
			failures = append(failures, errors.Wrap(r.err, errors.KindOf(r.err), CodeParallelBranchFailed,
				"the %q branch of the %q composite failed", p.branches[i].Name(), p.name))

			continue
		}
		succeeded = append(succeeded, i)
	}

	if len(failures) > 0 {
		// No branch's writes are applied: the successful ones are about to be
		// rolled back, and the data the failing one wrote has no owner (see the
		// type comment, "Shared data").
		if rerr := p.rollback(ctx, sc, succeeded); rerr != nil {
			failures = append(failures, rerr)
		}

		return nil, combineBranchErrors(p.name, failures)
	}

	outputs := make([]any, len(p.branches))
	for i, r := range results {
		outputs[i] = r.out
		mergeShared(sc.Shared, snapshot, r.shared)
	}

	return outputs, nil
}

// mergeShared applies the keys a branch CHANGED to the parent map.
//
// snapshot is the parent map from before the branches started. If a key is not
// there, or its value in the branch's copy differs, the branch wrote to it;
// unchanged keys are left alone so that a branch's stale copy cannot overwrite a
// sibling's write.
//
// The comparison uses reflect.DeepEqual: the == operator would PANIC on a map or
// slice value placed in Shared. The aim is not to measure equality but to detect
// "untouched".
func mergeShared(dst, snapshot, branch map[string]any) {
	for k, v := range branch {
		if old, ok := snapshot[k]; ok && reflect.DeepEqual(old, v) {
			continue
		}
		dst[k] = v
	}
}

// Compensate compensates every branch.
//
// Normally it is called only for a composite whose Invoke returned successfully;
// in that case every branch succeeded, so every one is compensated. It can also
// be called in a BEST-EFFORT compensation when the engine retried the composite
// and it blew up again (see the package comment); in that call, branches that
// never ran or were already rolled back are compensated too. A branch author's
// Compensate therefore has to be idempotent and must return nil when it finds
// nothing to undo.
func (p *ParallelStep) Compensate(ctx context.Context, sc *StepContext) error {
	if err := p.validate(); err != nil {
		return err
	}

	all := make([]int, len(p.branches))
	for i := range p.branches {
		all[i] = i
	}

	return p.compensateBranches(ctx, sc, all)
}

// rollback undoes the branches that succeeded inside Invoke.
//
// The rollback runs on a context that is NOT AFFECTED by cancellation: if the
// branch error came from the context being canceled, rolling back with the
// caller's dead context would be impossible.
//
// If the rollback blows up the returned error wraps ErrUncompensated: the
// composite has now left uncompensated work behind, and the engine cannot write
// the execution as "rolled back" without seeing that.
func (p *ParallelStep) rollback(ctx context.Context, sc *StepContext, succeeded []int) error {
	if len(succeeded) == 0 {
		return nil
	}

	err := p.compensateBranches(context.WithoutCancel(ctx), sc, succeeded)
	if err == nil {
		return nil
	}

	return errors.Wrap(errors.Join(ErrUncompensated, err), errors.KindInternal, CodeCompensationFailed,
		"the inner rollback of the %q concurrent composite could not be completed; A HUMAN IS NEEDED", p.name)
}

// compensateBranches compensates the given branches; an error does not stop the
// rest.
//
// Every branch gets ITS OWN time budget (see WithRollbackTimeout): a slow branch
// consuming a shared budget and leaving the remaining branches to be called with
// a dead context would leave work hanging that could have been undone.
func (p *ParallelStep) compensateBranches(ctx context.Context, sc *StepContext, idx []int) error {
	// Branch compensations run CONCURRENTLY. Since the branches themselves run
	// without an ordering dependency (concurrently), there is no ordering
	// dependency between their compensations either; the engine's reverse-order
	// rule BETWEEN STEPS is not applied INSIDE the composite.
	//
	// This also removes the STARVATION problem sequential execution created:
	// done sequentially, every branch derived its context from a common parent
	// budget, so a slow branch consuming it left the branches LATER in the order
	// to be called with a dead context. Running concurrently, every branch gets
	// its own budget at the same time.
	//
	// Every branch sees ITS OWN COPY of the parent map: because the
	// compensations are concurrent, writing to a common map would be a data
	// race. A compensation reaches for Shared to READ what it has to undo
	// anyway, not to write to it.
	var (
		mu       sync.Mutex
		failures []error
		wg       sync.WaitGroup
	)

	for _, i := range idx {
		wg.Add(1)
		go func() {
			defer wg.Done()

			b := p.branches[i]
			bsc := branchContext(sc, maps.Clone(sc.Shared), b.Name(), i)

			bctx, cancel := context.WithTimeout(ctx, p.rollbackTimeout)
			defer cancel()

			_, err := p.safeCall(bctx, bsc, "Compensate", func() (any, error) {
				return nil, b.Compensate(bctx, bsc)
			})
			if err == nil {
				return
			}

			mu.Lock()
			failures = append(failures, errors.Wrap(err, errors.KindOf(err), CodeCompensationFailed,
				"the compensation of the %q branch of the %q composite failed", b.Name(), p.name))
			mu.Unlock()
		}()
	}
	wg.Wait()

	// The failures are collected in branch order so the error order is
	// deterministic.
	slices.SortFunc(failures, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })

	return errors.Join(failures...)
}

// validate checks whether the composite can be run.
func (p *ParallelStep) validate() error {
	if p.name == "" {
		return errors.Invalid(CodeInvalidWorkflow, "the concurrent composite step's name cannot be empty")
	}
	if len(p.branches) == 0 {
		return errors.Invalid(CodeInvalidWorkflow, "the %q concurrent composite has no branches", p.name)
	}
	for i, b := range p.branches {
		if isNilStep(b) {
			return errors.Invalid(CodeInvalidWorkflow,
				"branch %d of the %q concurrent composite is nil", i, p.name)
		}
		if b.Name() == "" {
			return errors.Invalid(CodeInvalidWorkflow,
				"the name of branch %d of the %q concurrent composite is empty", i, p.name)
		}
	}

	return nil
}

// safeCall runs a branch call while catching panics.
func (p *ParallelStep) safeCall(ctx context.Context, sc *StepContext, phase string, fn func() (any, error)) (out any, err error) {
	defer func() {
		if r := recover(); r != nil {
			out = nil
			err = panicError(sc.StepName, phase, r)

			p.logger().ErrorContext(ctx, "workflow: a concurrent branch panicked",
				attrWorkflow, sc.Workflow, attrExecutionID, sc.ExecutionID,
				attrStep, p.name, "branch", sc.StepName, "phase", phase,
				"panic", r, "stack", string(debug.Stack()))
		}
	}()

	return fn()
}

// logger returns the composite's logger; without one, slog.Default is used.
func (p *ParallelStep) logger() *slog.Logger {
	if p.log != nil {
		return p.log
	}

	return slog.Default()
}

// branchContext derives a StepContext for a branch.
func branchContext(parent *StepContext, shared map[string]any, name string, index int) *StepContext {
	return &StepContext{
		Input:       parent.Input,
		Shared:      shared,
		ExecutionID: parent.ExecutionID,
		Workflow:    parent.Workflow,
		StepName:    name,
		StepIndex:   index,
		Attempt:     parent.Attempt,
	}
}

// combineBranchErrors joins the branch errors into a single typed error.
//
// The composite's class is chosen so that retryability falls on the RIGHT side:
// if even one branch is of a non-retryable class (invalid input, say) the whole
// composite is not retried — that branch would give the same error on every
// attempt, so a repeat would only reapply the other branches' side effects for
// nothing.
func combineBranchErrors(name string, failures []error) error {
	joined := errors.Join(failures...)

	kind := errors.KindOf(joined)
	for _, f := range failures {
		if !DefaultRetryable(f) {
			kind = errors.KindOf(f)

			break
		}
	}

	return errors.Wrap(joined, kind, CodeParallelBranchFailed,
		"the %q concurrent composite step failed", name)
}
