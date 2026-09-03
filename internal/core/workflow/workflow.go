// Package workflow is the saga engine that runs multi-step operations across
// modules (plan Section 5.5, Phase 3).
//
// A workflow is made of steps that run in order. Every step has an Invoke and a
// Compensate that undoes it. The engine runs the steps in order; if one blows up
// it calls the Compensate of the steps that SUCCEEDED UP TO THAT POINT, in
// REVERSE ORDER. That is what stands in for a distributed transaction (2PC):
// because the modules own separate tables — and one day separate services — they
// cannot be wrapped in a single database transaction (plan Sections 2.2, 2.3).
//
// # A step that blows up is NOT compensated — the one exception is the engine's own retry
//
// As a rule the compensation chain is limited to the steps whose Invoke returned
// SUCCESSFULLY. The step that blew up is not compensated: if Invoke returned an
// error there is no work to undo, and trying to undo work that was never done
// makes the half-finished state worse (for instance "canceling" a reservation
// that was never created can cancel a different, real one). For a step that
// blows up on its only attempt the cost falls to the step's author: its Invoke
// has to either succeed completely or leave things clean BY ITSELF.
//
// The rule's ONE EXCEPTION is a retry the engine ITSELF triggered. If the step
// was attempted more than once (Attempts > 1 in the record) the step that blew up
// is compensated too, on a BEST-EFFORT basis, and is placed at the HEAD of the
// compensation chain. The reasoning: the whole reason retrying exists is the
// "the request went, the answer was lost" case, and in that case attempt 1 DID
// APPLY the side effect to the world. An engine that does not undo it lies when
// it writes the execution as StatusFailed (= "the work was done and UNDONE"); in
// a real order that is an orphaned reservation nobody can see. Since the ENGINE
// rather than the step started the repeat, the engine also takes on its cost.
// The call is safe by contract: Compensate already has to be IDEMPOTENT and
// callable twice (see Step). The single requirement this puts on a step author
// is explicit — Compensate has to behave correctly even when the side effect may
// NEVER have been applied, that is, it has to no-op and return nil when it finds
// nothing to undo.
//
// Why the other options were not chosen: writing the execution into a separate
// "dirty" state instead of failed does NOT UNDO the side effect, it only reports
// it — and besides, if the best-effort compensation blows up the engine already
// writes StatusCompensationFailed, so the monitoring signal that option would
// give is contained in this one. And "let the step author leave things clean" is
// not enough: even if a step knew which of the engine's attempts it was on, it
// was not the one that asked for the repeat.
//
// # Steps that report a hanging side effect
//
// Composite steps that roll back internally (see ParallelStep) leave
// UNCOMPENSATED work behind when their rollback blows up. Such a step has to
// wrap its error with ErrUncompensated: if the engine sees that sentinel in the
// error chain it writes the execution as StatusCompensationFailed rather than
// StatusFailed, even when the compensation chain itself completed in full.
//
// # A compensation error does NOT STOP the chain
//
// If a Compensate blows up during compensation the remaining ones are STILL
// attempted. The reason is simple: step 3's compensation failing is no argument
// for step 1's compensation not running; cutting the chain there would leave
// work hanging that could have been undone. The errors are joined with
// errors.Join and the execution becomes StatusCompensationFailed — that state
// NEEDS A HUMAN and should be the first thing monitoring counts.
//
// # Retrying
//
// Retrying is per step and is OFF BY DEFAULT (see NoRetry); WithRetry turns it
// on. For which errors are retryable see DefaultRetryable. Compensation is
// retried too: because a compensation failure costs a human's time, insisting
// through a transient failure is worth more there than insisting on Invoke. If
// no compensation policy is given separately it inherits the step's policy (see
// WithCompensationRetry).
//
// Panics and context errors are not retried even when a CUSTOM predicate was
// given through RetryPolicy.Retryable; the exclusion is applied before the
// predicate and unconditionally (see RetryPolicy.Retryable).
//
// # Idempotency key
//
// An execution given WithIdempotencyKey is unique in the Store by the
// (workflow name, key) pair. A second call's behavior depends on the first
// execution's state and is documented one by one in Executor.Run. Uniqueness is
// established not by "read first, then write" but directly by Store.Create
// returning Conflict; a check open to the race between the read and the write
// could have run both of two concurrent requests.
//
// If the context is already dead AT THE MOMENT of the call (the client hung up)
// the engine does NOT OPEN the record at all and returns an error immediately.
// The reason: the whole point of the key is that the client can safely retry
// with the SAME key after a timeout; opening the record and writing it to a
// terminal state while no step has run burns that key permanently and the client
// gets Conflict forever. The check does not close the race entirely — the
// context can die right after the record is opened — but it definitively covers
// the common case, a context that is dead on arrival.
//
// # Persistence policy (what happens on Store errors)
//
// Store errors are NOT handled uniformly; the measure is whether the error
// carries the risk of applying the side effect twice:
//
//   - Create and FindByIdempotencyKey errors DROP THE EXECUTION. Both are the
//     gate of the repeat protection: running the steps without being able to
//     open the record, or without being able to read an existing execution's
//     outcome, would mean accepting the risk of doing the same work a second
//     time. The error is returned with no step having run.
//   - AppendStep and UpdateStatus errors are LOGGED and the execution CONTINUES.
//     At that point the step's side effect has ALREADY BEEN APPLIED to the
//     world; the record is not the thing itself, it is its trace. Rolling back a
//     successful flow because the ledger could not be kept turns a bookkeeping
//     failure into one the customer can see — and the rollback records would go
//     to the same broken Store anyway. The "which steps succeeded" information
//     the compensation needs is held in the engine's MEMORY, not read from the
//     Store; that is why compensation works correctly even when the Store is
//     down.
//
// The accepted cost is a hole in the trace: if UpdateStatus cannot be written
// the execution goes on looking running in the Store and the next call with the
// same key gets Conflict. That direction was chosen deliberately — falling the
// other way (presenting the output as though nothing had run) would have the
// work done a second time. Every failed Store write is logged at ERROR with the
// execution id.
//
// # Context cancellation
//
// If ctx is already dead AT THE MOMENT of the call the execution never starts
// (see Executor.Run). If it is canceled after the execution began, the run stops
// and the steps up to that point are STILL compensated. Because compensation
// cannot run with a canceled context, the engine uses a separate context derived
// with context.WithoutCancel that has its own time budget (see
// WithCompensationTimeout). The budget is PER STEP: a single shared budget,
// exhausted by a slow compensation at the end of the chain, would call the
// remaining — and typically EARLIEST, heaviest-resource-holding — steps with a
// dead context. Store writes are unaffected by cancellation for the same reason
// (see WithStoreTimeout).
//
// # Panics
//
// A panic in a step's Invoke or Compensate does not bring the engine down: it is
// caught, logged with its stack trace, and turned into a typed error wrapping
// ErrPanic. The flow after a panic is the same as the normal error flow — if
// Invoke panicked compensation begins, if Compensate panicked the chain
// continues with the remaining steps. A panic is NOT RETRIED (see
// DefaultRetryable).
//
// Panics coming from the workflow definition itself do not bring the engine down
// either: a typed-nil step (an interface value carrying a nil pointer but not
// itself nil) is caught in Workflow.Validate before Name() is called and turned
// into errors.Invalid.
//
// # Serialization
//
// The input, the output and the step outputs are written to the Store as JSON.
// If the input cannot be turned into JSON the execution NEVER STARTS
// (errors.Invalid) — raising the error early, while there is no side effect yet,
// is free. If a step's output cannot be converted the step counts as successful,
// the event is logged, and the record keeps an empty Output with a filled-in
// Failure description: at that point the side effect has been applied and cannot
// be undone over a serialization detail.
//
// Executor.Run's output is a json.RawMessage ON BOTH PATHS: on the happy path
// where the steps ran and on an idempotency repeat. That type stability is so
// the caller's type assertion does not depend on a race — on the repeat path the
// output is read from the Store, where the Go type has already been lost. For
// typed reading see RunInto.
package workflow

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"runtime/debug"
	"slices"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// The error codes. The caller can branch on them.
const (
	// CodeInvalidWorkflow reports that the workflow definition is invalid.
	CodeInvalidWorkflow = "workflow_invalid"
	// CodeInvalidOption reports that a RunOption is invalid.
	CodeInvalidOption = "workflow_invalid_option"
	// CodeInvalidOutput reports that the execution output could not be
	// converted to the requested type.
	CodeInvalidOutput = "workflow_invalid_output"
	// CodeStepFailed reports that a step blew up and compensation completed.
	//
	// It is a FALLBACK code: if the step's error carries its own code THAT one
	// is preserved and this one never appears (see [stepFailureCode]). For a
	// step error with no code — an untyped stdlib error — this is the only name
	// left.
	CodeStepFailed = "workflow_step_failed"
	// CodeStepPanicked reports that a step panicked.
	CodeStepPanicked = "workflow_step_panicked"
	// CodeParallelBranchFailed reports that a ParallelStep branch blew up.
	CodeParallelBranchFailed = "workflow_parallel_branch_failed"
	// CodeCompensationFailed reports that compensation could not be completed;
	// a human is needed.
	CodeCompensationFailed = "workflow_compensation_failed"
	// CodeCanceled reports that the execution stopped because the context was
	// canceled.
	CodeCanceled = "workflow_canceled"
	// CodeStoreFailed reports that the persistence layer returned an error.
	CodeStoreFailed = "workflow_store_failed"
	// CodeExecutionRunning reports that an execution with the same key is still
	// going.
	CodeExecutionRunning = "workflow_execution_running"
	// CodeExecutionFailed reports that an execution with the same key blew up
	// earlier.
	CodeExecutionFailed = "workflow_execution_failed"
	// CodeExecutionNotFound reports that the requested execution was not found.
	CodeExecutionNotFound = "workflow_execution_not_found"
	// CodeExecutionExists reports that an execution with the same id or key
	// already exists.
	CodeExecutionExists = "workflow_execution_exists"
	// CodeRecoveryFailed reports that an abandoned execution's compensation
	// could not be rebuilt FROM THE RECORD (see [Recoverable]).
	CodeRecoveryFailed = "workflow_recovery_failed"
)

// ErrPanic is the sentinel error reporting that a step panicked.
//
// The panic error the engine produces wraps it; with errors.Is(err, ErrPanic)
// the caller can tell a programming error from a transient failure.
var ErrPanic = errors.New("the step panicked")

// ErrUncompensated is the sentinel error reporting that a step left a side
// effect that COULD NOT BE UNDONE.
//
// Steps that roll back internally (see ParallelStep) wrap their error with it
// when the rollback blows up. If the engine sees the sentinel in a step error's
// chain it writes the execution as StatusCompensationFailed even when the
// compensation chain completed in full: StatusFailed means "the work was done
// and UNDONE", and with a side effect still hanging that record would be a lie.
var ErrUncompensated = errors.New("the step has an uncompensated side effect")

// StepContext is the context a step sees while it runs.
type StepContext struct {
	// Input is the input given to the workflow; every step sees the same value.
	Input any

	// Shared is the map that carries data between steps. Steps write to it and
	// later steps read from it. The same map is passed DURING COMPENSATION too;
	// a Compensate finds the value its own Invoke wrote here (for instance
	// "which reservation am I canceling").
	//
	// The map is used without a lock between consecutive steps because the
	// engine calls the steps in order on a single goroutine. For concurrent
	// branches see ParallelStep.
	Shared map[string]any

	// ExecutionID is the execution's id; steps can use it as their own
	// idempotency key.
	ExecutionID string
	// Workflow is the name of the workflow that is running.
	Workflow string
	// StepName is the name of the step currently running; the engine writes it
	// before every call.
	StepName string
	// StepIndex is the order of the step currently running; the engine writes it
	// before every call.
	StepIndex int
	// Attempt is the number of the attempt currently running (starting at 1);
	// the engine writes it before every call. A step can tell a first attempt
	// from a retry by it.
	Attempt int
}

// Step is a single step of a workflow.
//
// Implementations make two promises: Invoke either succeeds completely or leaves
// no work behind; and Compensate undoes Invoke's side effect and runs
// IDEMPOTENTLY (because it can be retried and called twice).
type Step interface {
	// Name is the step's name as it appears in the records and logs; it cannot
	// be empty.
	Name() string
	// Invoke does the step's work and returns its output. The output is written
	// to the Store as JSON; the last step's output is the workflow's output.
	Invoke(ctx context.Context, sc *StepContext) (output any, err error)
	// Compensate undoes Invoke's side effect. It is called only for steps whose
	// Invoke returned SUCCESSFULLY.
	Compensate(ctx context.Context, sc *StepContext) error
}

// Recoverable reports that a step can rebuild the state its compensation needs
// from ITS OWN persisted output.
//
// The engine calls it ONLY while recovering an abandoned execution: when the
// process dies in the middle of a saga [StepContext.Shared] goes with it and the
// compensation chain loses the answer to "which reservation am I canceling".
// The answer is NOT lost — the step's Invoke output stands in the record and the
// compensation record does not erase it (see [StepRecord.Output]) — but only the
// step itself knows how to turn it back into the typed value in Shared.
//
// Implementing it is OPTIONAL and the cost of not doing so is plain: a workflow
// with one unrecoverable step gets today's behavior when it is abandoned — the
// record becomes compensation_failed and waits for a human. The interface adds a
// capability; it does not break a contract.
//
// output is Invoke's persisted output and it can be EMPTY (the step succeeded
// but its output could not be turned into JSON; see [StepRecord.Output]).
// Restore has to return an error in that case: a compensation running on missing
// state says "done" without finding the work it was supposed to undo.
type Recoverable interface {
	// Restore reads the step's persisted output and puts back the values Invoke
	// wrote into [StepContext.Shared].
	Restore(sc *StepContext, output json.RawMessage) error
}

// RecoveryBlocker marks a step that cannot be assumed "did not run" WHILE IT HAS
// NO RECORD.
//
// The engine writes a step's record AFTER Invoke RETURNS, so a process dying in
// the middle of Invoke leaves NO TRACE of that step. Recovery (see
// [Recoverable]) looks at the records, so it takes such a step as "never ran" —
// and that is falling on the wrong side when the step's side effect cannot be
// undone.
//
// Its concrete case is checkout's capture step: if the card was charged but the
// process died before the record was written, recovery releases the stock,
// cancels the order and frees the key; the customer pays again and is charged
// TWICE. A human prevents that because a person can look at the payment
// provider.
//
// A step implementing this interface also blocks the recovery of the steps
// BEFORE it — but only while it has NO RECORD, that is, in the case where it
// really might have been in flight. If it has a record its outcome is known and
// the chain is compensated normally.
type RecoveryBlocker interface {
	// BlocksRecovery is the mark itself; it has no body and is never called.
	BlocksRecovery()
}

// Workflow is a flow made of steps.
type Workflow struct {
	// Name is the workflow's name; together with the idempotency key it defines
	// uniqueness in the Store. It cannot be empty.
	Name string
	// Steps are the steps to run in order; there has to be at least one. Steps
	// with the same name are allowed: in the records the identity is the Index,
	// not the name.
	Steps []Step
}

// Validate checks whether the workflow definition can be run.
//
// The name lengths are checked here too (see MaxNameLen): the limit is part of
// the Store contract, and applying it in the engine means an execution that
// would blow up in a durable Store NEVER starts — there is no workflow that
// passes on the in-memory Store and fails on Postgres.
//
// The nil check covers a TYPED NIL too (see isNilStep): the interface value may
// not be nil while the pointer inside it is, and calling Name() on such a value
// would bring the engine down.
func (w Workflow) Validate() error {
	if w.Name == "" {
		return errors.Invalid(CodeInvalidWorkflow, "the workflow name cannot be empty")
	}
	if len(w.Name) > MaxNameLen {
		return errors.Invalid(CodeInvalidWorkflow,
			"the workflow name can be at most %d bytes, %d bytes were given", MaxNameLen, len(w.Name))
	}
	if len(w.Steps) == 0 {
		return errors.Invalid(CodeInvalidWorkflow, "the %q workflow has no steps", w.Name)
	}

	for i, s := range w.Steps {
		if isNilStep(s) {
			return errors.Invalid(CodeInvalidWorkflow, "step %d of the %q workflow is nil", i, w.Name)
		}

		name := s.Name()
		if name == "" {
			return errors.Invalid(CodeInvalidWorkflow, "the name of step %d of the %q workflow is empty", i, w.Name)
		}
		if len(name) > MaxNameLen {
			return errors.Invalid(CodeInvalidWorkflow,
				"the name of step %d of the %q workflow can be at most %d bytes, %d bytes were given",
				i, w.Name, MaxNameLen, len(name))
		}
	}
	return nil
}

// isNilStep reports whether a step is nil or a TYPED NIL.
//
// An interface value is NOT nil even while it carries a nil pointer inside: if a
// plugin's step constructor returns (*myStep)(nil) on an error, the s == nil
// check passes and s.Name() panics on a nil pointer dereference. The panic is
// caught with reflect before it can bring the engine down; the price is a single
// reflection call per definition.
func isNilStep(s Step) bool {
	if s == nil {
		return true
	}

	v := reflect.ValueOf(s)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice,
		reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return v.IsNil()
	default:
		return false
	}
}

// Executor is the engine that runs a workflow.
type Executor interface {
	// Run runs the steps in order; if a step blows up it runs the Compensate of
	// the steps that succeeded up to that point, in reverse order, and persists
	// the state. The output is a json.RawMessage on every path; for typed
	// reading see RunInto.
	Run(ctx context.Context, wf Workflow, input any, opts ...RunOption) (any, error)
}

// RunInto runs the workflow and decodes its output into T.
//
// Executor.Run's output is a json.RawMessage, but the any in its signature hides
// that from the compiler; this helper binds the output's type to the contract at
// the call site. If the output is empty (the last step returned nil, or the
// output could not be turned into JSON) it returns T's zero value and a nil
// error: the execution succeeded, there is nothing to read. If decoding blows up
// the error is errors.Invalid — the execution IS COMPLETE AT THAT POINT and only
// the read failed.
func RunInto[T any](ctx context.Context, e Executor, wf Workflow, input any, opts ...RunOption) (T, error) {
	var out T

	raw, err := e.Run(ctx, wf, input, opts...)
	if err != nil {
		return out, err
	}

	payload, ok := raw.(json.RawMessage)
	if !ok {
		return out, errors.Internal(CodeInvalidOutput,
			"the output of the %q workflow is not a json.RawMessage: %T", wf.Name, raw)
	}
	if len(payload) == 0 {
		return out, nil
	}
	if uerr := json.Unmarshal(payload, &out); uerr != nil {
		return out, errors.Wrap(uerr, errors.KindInvalid, CodeInvalidOutput,
			"the output of the %q workflow could not be converted to %T", wf.Name, out)
	}
	return out, nil
}

// executor is Executor's only implementation.
type executor struct {
	store Store
	log   *slog.Logger
}

var _ Executor = (*executor)(nil)

// The log field names; the same keys are used in every record.
const (
	attrWorkflow    = "workflow"
	attrExecutionID = "execution_id"
	attrStep        = "step"
	attrStepIndex   = "step_index"
	attrAttempt     = "attempt"
	attrError       = "error"
)

// New builds a saga engine on the given Store.
//
// With a nil log, slog.Default is used. With a nil store the engine is built but
// DOES NOT RUN: the setup is logged at ERROR and every Run is rejected with
// errors.Invalid. Falling back silently to an in-process store on a missing
// Store would pull the idempotency protection down to the process boundary — two
// misconfigured replicas would charge the same payment with the same key, and
// the only trace of it would be one warning line. An in-process store is
// therefore the CALLER'S explicit decision (see NewInMemory).
//
// The rejection surfaces as an error on the first Run rather than at
// construction because of the signature: a constructor returning an Executor has
// no error channel, and panicking over a missing dependency turns a wiring
// mistake into a crash rather than a failure signal.
func New(store Store, log *slog.Logger) Executor {
	if log == nil {
		log = slog.Default()
	}
	if store == nil {
		log.Error("workflow: no Store was given; the engine is built but every Run will be rejected — use NewInMemory for an in-process store")
	}
	return &executor{store: store, log: log}
}

// NewInMemory builds an engine on an in-process, non-durable store.
//
// It is for tests and development and, as its name says, an EXPLICIT decision:
// if the process dies the execution history is lost, and across several replicas
// idempotency holds only within a single process. Production has to use New.
func NewInMemory(log *slog.Logger) Executor {
	if log == nil {
		log = slog.Default()
	}
	return &executor{store: NewMemoryStore(), log: log}
}

// doneStep is a step that enters the compensation chain.
//
// rec is the step's Invoke record: because the compensation record UPDATES the
// same Index (see Store.AppendStep), the output and attempt information survives
// only if it is carried here.
type doneStep struct {
	step Step
	rec  StepRecord
	// bestEffort reports that the step's Invoke BLEW UP but that it will be
	// compensated anyway, because the engine attempted it more than once.
	bestEffort bool
}

// Run runs the steps in order and persists the execution state.
//
// The return value is the json.RawMessage form of the last step's output (for
// typed reading see RunInto). If a step blows up, the steps that succeeded up to
// that point are compensated in REVERSE ORDER; the step that blew up is
// compensated on a best-effort basis only if the ENGINE attempted it more than
// once (see the package comment). If compensation completes in full the
// execution is written as StatusFailed; if the compensation itself blows up, or
// the step error carries ErrUncompensated, it is written as
// StatusCompensationFailed — and in that second case the returned error carries
// both the step error and the compensation errors through errors.Join and is
// KindInternal.
//
// If the context was already canceled at the moment of the call NO record is
// OPENED and errors.Unavailable is returned: burning the idempotency key with no
// work done would invert the very reason the key exists. If the engine was built
// without a Store no step runs and errors.Invalid is returned (see New).
//
// The Kind of the returned error preserves the class of the STEP THAT BLEW UP
// when the execution was written [StatusFailed]; that way the HTTP layer can go
// on mapping invalid input to 422 and a conflict to 409. In the
// [StatusCompensationFailed] case, and when a hanging side effect is reported
// ([ErrUncompensated]), the class is raised to KindInternal: telling the caller
// "your input was invalid" while uncleaned work is left behind would mislead.
//
// If WithIdempotencyKey was given, a second call with the same key
// DOES NOT RUN THE STEPS AGAIN and behaves according to the first execution's state:
//
//   - completed → the first execution's OUTPUT is returned. The output is read
//     from the Store, so it is a json.RawMessage because the Go type was lost in
//     persistence. The happy path returns the same type, so the caller's type
//     assertion DOES NOT DEPEND on which path it landed on.
//   - running → errors.Conflict. The same work is still in flight; starting a
//     second copy of it is exactly what the key exists to prevent.
//   - failed → errors.Conflict. Even though the execution was rolled back the
//     engine does NOT REPEAT it on its own: a key names the OUTCOME of an
//     attempt, not a right to endless repeats. Running it again in silence with
//     the same key would rest on the ASSUMPTION that the first attempt's side
//     effects really were undone, and compensation is best-effort. Retrying is
//     the caller's explicit decision and needs a NEW key — which keeps "how many
//     times was this work attempted" answerable from the Store.
//   - compensation_failed → errors.Conflict. The system is inconsistent; putting
//     a new execution on top of it without a human first makes the damage
//     bigger.
func (e *executor) Run(ctx context.Context, wf Workflow, input any, opts ...RunOption) (any, error) {
	if e.store == nil {
		return nil, errors.Invalid(CodeInvalidOption,
			"the workflow engine was built without a Store; give it a durable Store, or use NewInMemory for an in-process one")
	}

	o, err := newRunOptions(opts)
	if err != nil {
		return nil, err
	}
	if verr := wf.Validate(); verr != nil {
		return nil, verr
	}
	if cerr := ctx.Err(); cerr != nil {
		// The context arrived at the call dead: the record is NOT OPENED. Were it
		// opened it would be written to a terminal state with no step having run
		// and the idempotency key would be burned permanently (see the package
		// comment, "Idempotency key").
		return nil, errors.Wrap(cerr, errors.KindUnavailable, CodeCanceled,
			"the %q workflow was not started: the context was already canceled at the moment of the call", wf.Name)
	}

	payload, merr := json.Marshal(input)
	if merr != nil {
		return nil, errors.Wrap(merr, errors.KindInvalid, CodeInvalidWorkflow,
			"the input of the %q workflow could not be turned into JSON", wf.Name)
	}

	// The loop turns AT MOST twice, and the second turn happens for exactly one
	// reason: an abandoned record was closed and released its key, so there is
	// now room to open one (see [WithLease]). The bound is deliberate — an
	// unbounded loop could keep turning while two processes closed the same
	// abandoned record one after the other.
	for turn := range 2 {
		exec, err := e.open(ctx, wf, payload, o)
		if err != nil {
			return nil, err
		}
		if exec != nil {
			return e.execute(ctx, wf, input, exec, o)
		}

		// An execution opened with the same key was found; replay gives its
		// outcome.
		out, again, rerr := e.replay(ctx, wf, o)
		if !again || turn == 1 {
			return out, rerr
		}
	}

	// Landing here would mean "try again" was said on the second turn too, and
	// the loop bound prevents that; it is here for the compiler.
	return nil, errors.Internal(CodeStoreFailed,
		"no execution could be opened for the %q workflow: the abandoned record did not close on the second turn either", wf.Name)
}

// open opens the execution record.
//
// If a record with the same key already exists it returns (nil, nil) and the
// caller goes to replay. Other Store errors drop the execution: this is the gate
// of the repeat protection.
func (e *executor) open(ctx context.Context, wf Workflow, payload json.RawMessage, o *runOptions) (*Execution, error) {
	now := time.Now().UTC()
	exec := &Execution{
		ID:             newExecutionID(now),
		Workflow:       wf.Name,
		IdempotencyKey: o.idempotencyKey,
		Status:         StatusRunning,
		Input:          payload,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	sctx, cancel := o.storeContext(ctx)
	err := e.store.Create(sctx, exec)
	cancel()

	switch {
	case err == nil:
		return exec, nil
	case o.idempotencyKey != "" && errors.IsConflict(err):
		return nil, nil
	default:
		return nil, errors.Wrap(err, errors.KindOf(err), CodeStoreFailed,
			"the execution record for the %q workflow could not be opened", wf.Name)
	}
}

// replay returns the outcome of the execution opened with the same idempotency
// key.
//
// If the second result ("again") is true the record was found ABANDONED, was
// closed and released its key; the caller should try to open a new execution. In
// that case the first two results are meaningless.
func (e *executor) replay(ctx context.Context, wf Workflow, o *runOptions) (out any, again bool, err error) {
	sctx, cancel := o.storeContext(ctx)
	prev, err := e.store.FindByIdempotencyKey(sctx, wf.Name, o.idempotencyKey)
	cancel()
	if err != nil {
		return nil, false, errors.Wrap(err, errors.KindOf(err), CodeStoreFailed,
			"the execution of the %q workflow with the key %q could not be read", wf.Name, o.idempotencyKey)
	}
	if prev == nil {
		// A contract violation: with no error the record has to be filled in.
		// Because the Store is written in a separate package the engine meets
		// this with a typed error rather than a nil dereference.
		return nil, false, errors.Internal(CodeStoreFailed,
			"the Store returned a nil record with no error for the %q key of the %q workflow", o.idempotencyKey, wf.Name)
	}

	switch prev.Status {
	case StatusCompleted:
		e.log.Info("workflow: the idempotency key matched, the steps were not run again",
			attrWorkflow, wf.Name, attrExecutionID, prev.ID)
		return prev.Output, false, nil
	case StatusRunning:
		// A record whose lease has expired is not "running"; what it did is
		// determined from the step records (see [WithLease]).
		abandoned, aerr := e.judgeAbandoned(ctx, wf, prev, o)
		switch {
		case aerr != nil:
			return nil, false, aerr
		case abandoned:
			return nil, true, nil
		}

		return nil, false, errors.Conflict(CodeExecutionRunning,
			"the execution of the %q workflow with the key %q (%s) is still going", wf.Name, o.idempotencyKey, prev.ID)
	case StatusFailed:
		return nil, false, errors.Conflict(CodeExecutionFailed,
			"the execution of the %q workflow with the key %q (%s) failed earlier and was compensated; use a NEW key to try again: %s",
			wf.Name, o.idempotencyKey, prev.ID, prev.Failure)
	case StatusCompensationFailed:
		return nil, false, errors.Conflict(CodeExecutionFailed,
			"the execution of the %q workflow with the key %q (%s) could not be compensated; a human is needed: %s",
			wf.Name, o.idempotencyKey, prev.ID, prev.Failure)
	default:
		return nil, false, errors.Internal(CodeStoreFailed,
			"execution %q is in an unknown state: %q", prev.ID, prev.Status)
	}
}

// judgeAbandoned moves a "running" record whose lease has expired into a
// terminal state.
//
// The first result reports that the caller can open a NEW execution (the record
// became [StatusFailed] and released its key). If the second result is set the
// caller should return it; the record became [StatusCompensationFailed] and is
// waiting for a human. If both are empty the record really is still running.
//
// The reasoning and the decision table are in the [WithLease] godoc.
func (e *executor) judgeAbandoned(ctx context.Context, wf Workflow, prev *Execution, o *runOptions) (bool, error) {
	if o.lease <= 0 || time.Since(prev.UpdatedAt) <= o.lease {
		return false, nil
	}

	// The steps are read separately: the contract does not say
	// FindByIdempotencyKey brings them, and this path is exceptional anyway.
	sctx, cancel := o.storeContext(ctx)
	full, err := e.store.Get(sctx, prev.ID)
	cancel()
	if err != nil {
		// If the steps cannot be read, whether it was abandoned CANNOT BE
		// DECIDED; the record is left as it stands and the caller gets "still
		// going". Falling the other way (retrying while work was done) would
		// double the reserved stock.
		e.log.ErrorContext(ctx, "workflow: the steps of an execution whose lease expired could not be read",
			attrWorkflow, wf.Name, attrExecutionID, prev.ID, attrError, err)

		return false, nil
	}

	if hasHeldWork(full.Steps) {
		return e.recoverExecution(ctx, wf, full, o)
	}

	// No step did any work: there is nothing to compensate, so the key is
	// released.
	e.persistStatus(ctx, prev.ID, o, StatusFailed, nil,
		"the execution's lease expired and no step had done any work; it was taken as abandoned")
	e.log.WarnContext(ctx, "workflow: an abandoned execution was closed and can be retried",
		attrWorkflow, wf.Name, attrExecutionID, prev.ID)

	return true, nil
}

// recoverExecution runs an abandoned execution's compensation chain again FROM
// THE RECORD.
//
// The record that arrives here is this: the process was cut off after doing work
// and before compensation ever ran. The stock reserved up to that point is
// standing in the world and nobody is releasing it. The engine HAS the
// compensation functions (the caller came with the same workflow definition);
// the only thing lost was the state shared between steps, and rebuilding that
// from the step's own persisted output is [Recoverable]'s job.
//
// # The four cases where recovery is REFUSED
//
// In all four today's behavior is kept — compensation_failed and a human —
// because a compensation running on the wrong state says it undid work it did
// not:
//
//   - The step in the record does not carry the SAME NAME as the one in the
//     definition. The index is the record's identity, but the workflow
//     definition may have changed between two deploys; without the name check,
//     step 2's compensation would be called with an entirely different step's
//     output.
//   - A step that did work does not implement [Recoverable]. If one link of the
//     chain cannot rebuild its state, the whole chain is untrustworthy.
//   - Restore returned an error (the output is empty or its shape changed).
//   - The FIRST step with no record is a [RecoveryBlocker]. The process may have
//     died inside it and the records cannot tell; see [checkRecoveryBoundary].
//
// # sc.Input is NOT TYPED on the recovery path
//
// On the normal path [StepContext.Input] is the Go value the caller gave; here
// that value went with the process and what remains is the record's JSON, so
// Input is a json.RawMessage. No compensation reads Input today. One that starts
// to has to know the same field carries two different types on the two paths.
//
// # Compensation can run a second time
//
// If the process dies again during recovery the same compensations are called
// once more. Compensate being idempotent is ALREADY the engine's contract (when
// a compensation blows up the chain does not stop, and on the next attempt the
// same steps are compensated again); recovery brings no new requirement, it uses
// the existing one.
func (e *executor) recoverExecution(ctx context.Context, wf Workflow, exec *Execution, o *runOptions) (bool, error) {
	sc := &StepContext{
		Input:       exec.Input,
		Shared:      make(map[string]any),
		ExecutionID: exec.ID,
		Workflow:    wf.Name,
	}

	done, err := e.rebuildChain(sc, wf, exec)
	if err != nil {
		final := errors.Wrap(err, errors.KindConflict, CodeExecutionFailed,
			"the execution of the %q workflow with the key %q (%s) was left unfinished with an "+
				"expired lease and COULD NOT BE RECOVERED; A HUMAN IS NEEDED",
			wf.Name, o.idempotencyKey, exec.ID)
		e.persistStatus(ctx, exec.ID, o, StatusCompensationFailed, nil, final.Error())
		e.log.ErrorContext(ctx, "workflow: an abandoned execution could not be recovered; a human is needed",
			attrWorkflow, wf.Name, attrExecutionID, exec.ID, attrError, err)

		return true, final
	}

	e.log.WarnContext(ctx, "workflow: running an abandoned execution's compensation from the record",
		attrWorkflow, wf.Name, attrExecutionID, exec.ID, "steps", len(done))

	if compErr := e.compensate(ctx, sc, exec.ID, done, o); compErr != nil {
		final := errors.Wrap(compErr, errors.KindInternal, CodeCompensationFailed,
			"the compensation of the abandoned execution (%s) of the %q workflow could not be "+
				"completed; A HUMAN IS NEEDED", exec.ID, wf.Name)
		e.persistStatus(ctx, exec.ID, o, StatusCompensationFailed, nil, final.Error())
		e.log.ErrorContext(ctx, "workflow: the recovery compensation blew up; a human is needed",
			attrWorkflow, wf.Name, attrExecutionID, exec.ID, attrError, final)

		return true, final
	}

	// Compensation completed in full: that is exactly what StatusFailed means in
	// this engine, and it releases the key, so the customer can pay for the same
	// cart again.
	e.persistStatus(ctx, exec.ID, o, StatusFailed, nil,
		"the execution's lease expired; the compensation chain was run from the record and completed")
	e.log.WarnContext(ctx, "workflow: an abandoned execution was compensated and can be retried",
		attrWorkflow, wf.Name, attrExecutionID, exec.ID)

	return true, nil
}

// rebuildChain collects the steps to compensate from the records and rebuilds
// the shared state.
//
// The records are walked in ASCENDING order, because what builds Shared is the
// order of the steps: a later step's write can overwrite an earlier one's, and
// walking in reverse would invert that overwrite. The compensation chain itself
// runs in REVERSE order ([executor.compensate]), but that answers a different
// question.
//
// The returned slice carries only the steps that DID WORK ([StepStatus.Held]);
// Restore, on the other hand, is called for EVERY step that has an output,
// because the value a step being compensated needs may have been written by a
// successful step before it.
func (e *executor) rebuildChain(sc *StepContext, wf Workflow, exec *Execution) ([]doneStep, error) {
	records := make([]StepRecord, len(exec.Steps))
	copy(records, exec.Steps)
	slices.SortFunc(records, func(a, b StepRecord) int { return a.Index - b.Index })

	done := make([]doneStep, 0, len(records))
	for i := range records {
		rec := &records[i]
		if rec.Index < 0 || rec.Index >= len(wf.Steps) {
			return nil, errors.Internal(CodeRecoveryFailed,
				"step %d has a record but the workflow definition holds %d steps; the definition has changed",
				rec.Index, len(wf.Steps))
		}

		step := wf.Steps[rec.Index]
		if step.Name() != rec.Name {
			return nil, errors.Internal(CodeRecoveryFailed,
				"step %d is %q in the record and %q in the definition; the workflow definition changed after the execution",
				rec.Index, rec.Name, step.Name())
		}

		restorer, ok := step.(Recoverable)
		if !ok {
			if !rec.Status.Held() {
				continue
			}

			return nil, errors.Internal(CodeRecoveryFailed,
				"the %q step (%d) did work but cannot rebuild its state (it is not Recoverable)",
				rec.Name, rec.Index)
		}

		if err := restorer.Restore(sc, rec.Output); err != nil {
			return nil, errors.Wrap(err, errors.KindInternal, CodeRecoveryFailed,
				"the state of the %q step (%d) could not be rebuilt from the record", rec.Name, rec.Index)
		}

		if rec.Status.Held() {
			done = append(done, doneStep{step: step, rec: *rec})
		}
	}

	if len(done) == 0 {
		return nil, errors.Internal(CodeRecoveryFailed,
			"no step to compensate was found; the record looked as though work had been done")
	}

	if err := checkRecoveryBoundary(wf, records); err != nil {
		return nil, err
	}

	return done, nil
}

// checkRecoveryBoundary refuses the case where the process may have died INSIDE
// a step that has no record, and that step cannot bear the assumption.
//
// If the highest recorded index is k, the process died either inside step k+1's
// Invoke or without ever entering it; the records CANNOT TELL the two apart. If
// k+1 is a [RecoveryBlocker] the price of not telling them apart is a side effect
// that cannot be undone, and the decision is left to a human.
//
// A blocking step anywhere else in the chain does NOT matter: those have records,
// so what they did is known.
func checkRecoveryBoundary(wf Workflow, records []StepRecord) error {
	if len(records) == 0 {
		return nil
	}

	next := records[len(records)-1].Index + 1
	if next >= len(wf.Steps) {
		return nil
	}

	if _, blocks := wf.Steps[next].(RecoveryBlocker); blocks {
		return errors.Internal(CodeRecoveryFailed,
			"the %q step (%d) has no record: the process may have died INSIDE it, and that "+
				"step cannot be assumed not to have run without one; no recovery is done",
			wf.Steps[next].Name(), next)
	}

	return nil
}

// hasHeldWork reports whether the step records hold work that was NOT UNDONE.
//
// The decision lives in a single predicate ([StepStatus.Held]) and is NOT
// REPEATED here: the listing surface uses the same distinction, and the day the
// two copies diverged the engine would count a record as "work done" while the
// listing skipped it.
func hasHeldWork(steps []StepRecord) bool {
	for i := range steps {
		if steps[i].Status.Held() {
			return true
		}
	}

	return false
}

// execute runs the steps in order and persists the result.
func (e *executor) execute(ctx context.Context, wf Workflow, input any, exec *Execution, o *runOptions) (any, error) {
	sc := &StepContext{
		Input:       input,
		Shared:      make(map[string]any),
		ExecutionID: exec.ID,
		Workflow:    wf.Name,
	}

	done := make([]doneStep, 0, len(wf.Steps))
	var last any

	for i, s := range wf.Steps {
		if cerr := ctx.Err(); cerr != nil {
			// The cancellation was caught BETWEEN steps: no new step is started
			// and the steps up to this point are compensated.
			cause := errors.Wrap(cerr, errors.KindUnavailable, CodeCanceled,
				"the %q workflow was canceled before step %d", wf.Name, i)
			return e.unwind(ctx, sc, exec, done, o, cause)
		}

		name := s.Name()
		sc.StepName, sc.StepIndex, sc.Attempt = name, i, 1

		started := time.Now().UTC()
		out, attempts, serr := e.invokeStep(ctx, s, sc, o)
		ended := time.Now().UTC()

		if serr != nil {
			rec := StepRecord{
				Name:      name,
				Index:     i,
				Status:    StepFailed,
				Failure:   serr.Error(),
				Attempts:  attempts,
				StartedAt: started,
				EndedAt:   ended,
			}
			e.persistStep(ctx, exec.ID, o, rec)
			e.log.ErrorContext(ctx, "workflow: a step failed, compensation is starting",
				attrWorkflow, wf.Name, attrExecutionID, exec.ID,
				attrStep, name, attrStepIndex, i, attrAttempt, attempts, attrError, serr)

			if attempts > 1 {
				// The engine attempted the step more than once on ITS OWN
				// decision: the first attempt's side effect may have been
				// applied to the world. The step is added to the head of the
				// compensation chain on a best-effort basis (see the package
				// comment).
				done = append(done, doneStep{step: s, rec: rec, bestEffort: true})
			}

			cause := errors.Wrap(serr, errors.KindOf(serr), stepFailureCode(serr),
				"the %q step (%d) of the %q workflow failed", name, i, wf.Name)
			return e.unwind(ctx, sc, exec, done, o, cause)
		}

		rec := StepRecord{
			Name:      name,
			Index:     i,
			Status:    StepInvoked,
			Attempts:  attempts,
			StartedAt: started,
			EndedAt:   ended,
		}
		rec.Output, rec.Failure = e.encode(ctx, out, exec.ID, name)
		e.persistStep(ctx, exec.ID, o, rec)

		done = append(done, doneStep{step: s, rec: rec})
		last = out
	}

	output, note := e.encode(ctx, last, exec.ID, "")
	e.persistStatus(ctx, exec.ID, o, StatusCompleted, output, note)
	e.log.InfoContext(ctx, "workflow: completed",
		attrWorkflow, wf.Name, attrExecutionID, exec.ID, "steps", len(wf.Steps))

	return output, nil
}

// stepFailureCode picks the code a step error carries outward.
//
// If the underlying error carries its own code THAT one is preserved; if it does
// not, [CodeStepFailed] is used.
//
// # Why the engine's own code does not overwrite it
//
// The engine's wrapping already inherited the error's CLASS (Kind) from the
// underlying error but overwrote its CODE with its own constant. The result was
// losing half of the class: the transport layer writes a single
// machine-readable field into the body (Code) and every step error flattened
// there into one value — "workflow_step_failed". The cost is concrete: a
// purchase exceeding a B2B spending limit gets a 409, but its body could not be
// told apart from a transient conflict. And 409 is exactly the class A REPEAT
// DOES NOT SOLVE: the storefront has to tell the customer "your limit was not
// enough", not "try again". The data that makes the distinction was being
// produced; it just was not reaching the consumer.
//
// # Only the CODE is carried
//
// The message and the Details stay in the wrapped chain; the engine goes on
// writing its own sentence (which workflow, which step, which position) on the
// outside. On KindInternal errors the transport layer masks that sentence and
// the chain anyway and publishes only the code (see
// internal/core/http.WriteError). A code is fixed and machine-readable by
// definition; it leaks no server detail.
//
// If the step error has no code — an untyped stdlib error — the engine's own
// constant is what is left: a body with no code is a body that tells the client
// nothing.
func stepFailureCode(err error) string {
	if code := errors.CodeOf(err); code != "" {
		return code
	}
	return CodeStepFailed
}

// unwind runs the compensation chain and writes the execution into a terminal
// state.
//
// cause is the error that triggered the compensation (a step error or a context
// cancellation). StatusFailed is written only when compensation completed in
// full AND the step error does not carry ErrUncompensated: a step that rolls
// back internally uses that sentinel to say "I left hanging work behind", and
// even if the engine's compensation chain finished cleanly the record cannot say
// "rolled back".
func (e *executor) unwind(ctx context.Context, sc *StepContext, exec *Execution, done []doneStep, o *runOptions, cause error) (any, error) {
	compErr := e.compensate(ctx, sc, exec.ID, done, o)

	if compErr == nil && !errors.Is(cause, ErrUncompensated) {
		e.persistStatus(ctx, exec.ID, o, StatusFailed, nil, cause.Error())
		return nil, cause
	}

	final := errors.Wrap(errors.Join(cause, compErr), errors.KindInternal, CodeCompensationFailed,
		"the compensation of the %q workflow could not be completed (%s); A HUMAN IS NEEDED", sc.Workflow, exec.ID)
	e.persistStatus(ctx, exec.ID, o, StatusCompensationFailed, nil, final.Error())
	e.log.ErrorContext(ctx, "workflow: compensation could not be completed, a human is needed",
		attrWorkflow, sc.Workflow, attrExecutionID, exec.ID, attrError, final)

	return nil, final
}

// compensate calls the compensation chain in REVERSE ORDER.
//
// Every step gets ITS OWN time budget and that budget is NOT AFFECTED by the
// caller's context being canceled (context.WithoutCancel): a compensation cannot
// run with a canceled context, yet cancellation is one of the cases where
// compensation is needed most. The budget being per step is deliberate — were a
// slow compensation at the end of the chain to consume a shared budget, the
// remaining and typically heaviest-resource-holding (a payment capture, say)
// EARLIEST steps would be called with a dead context and every context-respecting
// Compensate would fail instantly. If a Compensate blows up the chain DOES NOT
// STOP; the error is collected and the remaining steps are still attempted. The
// return value is the errors.Join of the collected errors.
//
// The compensation record is written OVER the step's Invoke record
// (Store.AppendStep updates the same Index) and the Output, Attempts and
// StartedAt are PRESERVED: in compensation_failed — the one state that needs a
// human — the only data the operator needs is which reservation or payment the
// step produced.
func (e *executor) compensate(ctx context.Context, sc *StepContext, execID string, done []doneStep, o *runOptions) error {
	if len(done) == 0 {
		return nil
	}

	base := context.WithoutCancel(ctx)

	var failures []error
	for i := len(done) - 1; i >= 0; i-- {
		d := done[i]
		sc.StepName, sc.StepIndex, sc.Attempt = d.rec.Name, d.rec.Index, 1

		cctx, cancel := context.WithTimeout(base, o.compensationTimeout)
		attempts, err := e.compensateStep(cctx, d.step, sc, o)
		ended := time.Now().UTC()
		cancel()

		// The Invoke record is carried over; only the status, the failure and the
		// end instant change.
		rec := d.rec
		rec.Status = StepCompensated
		rec.EndedAt = ended

		if err != nil {
			wrapped := errors.Wrap(err, errors.KindOf(err), CodeCompensationFailed,
				"the compensation of the %q step (%d) failed", rec.Name, rec.Index)
			failures = append(failures, wrapped)

			rec.Status = StepCompensationFailed
			rec.Failure = joinFailure(rec.Failure, wrapped.Error())

			e.log.ErrorContext(ctx, "workflow: a compensation failed, the chain is continuing",
				attrWorkflow, sc.Workflow, attrExecutionID, execID,
				attrStep, rec.Name, attrStepIndex, rec.Index, attrAttempt, attempts,
				"best_effort", d.bestEffort, attrError, err)
		}

		e.persistStep(ctx, execID, o, rec)
	}

	return errors.Join(failures...)
}

// joinFailure joins two error texts for the record.
//
// In the record of a step compensated on a best-effort basis the Invoke error is
// already written; if the compensation blows up too both are needed — one says
// what was attempted, the other says what was left hanging.
func joinFailure(existing, added string) string {
	if existing == "" {
		return added
	}
	return existing + "; " + added
}

// invokeStep retries a step's Invoke as far as the policy allows.
//
// The number in the return value is the total count of attempts made (at least
// 1).
func (e *executor) invokeStep(ctx context.Context, s Step, sc *StepContext, o *runOptions) (output any, attempts int, err error) {
	p := o.retry
	for attempt := 1; ; attempt++ {
		sc.Attempt = attempt

		out, serr := e.safeInvoke(ctx, s, sc)
		if serr == nil {
			return out, attempt, nil
		}
		if attempt >= p.MaxAttempts || !p.allow(serr) {
			return nil, attempt, serr
		}

		e.log.WarnContext(ctx, "workflow: a step failed, it will be retried",
			attrWorkflow, sc.Workflow, attrExecutionID, sc.ExecutionID,
			attrStep, sc.StepName, attrStepIndex, sc.StepIndex, attrAttempt, attempt, attrError, serr)

		if werr := wait(ctx, p.backoffFor(attempt)); werr != nil {
			// The context died during the wait; starting a new attempt is
			// pointless.
			return nil, attempt, serr
		}
	}
}

// compensateStep retries a step's Compensate as far as the policy allows.
func (e *executor) compensateStep(ctx context.Context, s Step, sc *StepContext, o *runOptions) (attempts int, err error) {
	p := o.compensationRetry
	for attempt := 1; ; attempt++ {
		sc.Attempt = attempt

		cerr := e.safeCompensate(ctx, s, sc)
		if cerr == nil {
			return attempt, nil
		}
		if attempt >= p.MaxAttempts || !p.allow(cerr) {
			return attempt, cerr
		}

		e.log.WarnContext(ctx, "workflow: a compensation failed, it will be retried",
			attrWorkflow, sc.Workflow, attrExecutionID, sc.ExecutionID,
			attrStep, sc.StepName, attrStepIndex, sc.StepIndex, attrAttempt, attempt, attrError, cerr)

		if werr := wait(ctx, p.backoffFor(attempt)); werr != nil {
			return attempt, cerr
		}
	}
}

// safeInvoke calls the step's Invoke while catching panics.
func (e *executor) safeInvoke(ctx context.Context, s Step, sc *StepContext) (out any, err error) {
	defer func() {
		if r := recover(); r != nil {
			out = nil
			err = e.recovered(ctx, sc, "Invoke", r)
		}
	}()

	return s.Invoke(ctx, sc)
}

// safeCompensate calls the step's Compensate while catching panics.
func (e *executor) safeCompensate(ctx context.Context, s Step, sc *StepContext) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = e.recovered(ctx, sc, "Compensate", r)
		}
	}()

	return s.Compensate(ctx, sc)
}

// recovered logs the caught panic and turns it into a typed error.
//
// The stack trace is written to the log and NOT to the error: the error text can
// go back to the client at the HTTP layer, and a stack trace leaks file paths and
// internal structure (plan Section 8).
func (e *executor) recovered(ctx context.Context, sc *StepContext, phase string, r any) error {
	e.log.ErrorContext(ctx, "workflow: a step panicked",
		attrWorkflow, sc.Workflow, attrExecutionID, sc.ExecutionID,
		attrStep, sc.StepName, attrStepIndex, sc.StepIndex, "phase", phase,
		"panic", r, "stack", string(debug.Stack()))

	return panicError(sc.StepName, phase, r)
}

// panicError turns the panic value into a typed error wrapping ErrPanic.
func panicError(step, phase string, r any) error {
	if err, ok := r.(error); ok {
		return errors.Wrap(errors.Join(ErrPanic, err), errors.KindInternal, CodeStepPanicked,
			"the %s of the %q step panicked", phase, step)
	}
	return errors.Wrap(ErrPanic, errors.KindInternal, CodeStepPanicked,
		"the %s of the %q step panicked: %v", phase, step, r)
}

// wait waits for the given duration; if the context dies it returns early.
func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}

	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// encode turns a value into JSON; if it cannot be turned it returns (nil, note).
//
// Failing to turn it DOES NOT DROP the execution: at this point the step's side
// effect has already been applied and cannot be undone over a serialization
// detail. The event is logged and the note is written into the record. If step is
// empty, what is being turned is the workflow output.
func (e *executor) encode(ctx context.Context, v any, execID, step string) (payload json.RawMessage, note string) {
	payload, err := json.Marshal(v)
	if err == nil {
		return payload, ""
	}

	e.log.ErrorContext(ctx, "workflow: the output could not be turned into JSON, the record is being written without one",
		attrExecutionID, execID, attrStep, step, attrError, err)

	return nil, "the output could not be turned into JSON: " + err.Error()
}

// persistStep writes the step record; an error is logged and the execution goes
// on.
func (e *executor) persistStep(ctx context.Context, execID string, o *runOptions, rec StepRecord) {
	sctx, cancel := o.storeContext(ctx)
	defer cancel()

	if err := e.store.AppendStep(sctx, execID, rec); err != nil {
		e.log.ErrorContext(ctx, "workflow: the step record could not be written, the execution is going on",
			attrExecutionID, execID, attrStep, rec.Name, attrStepIndex, rec.Index,
			"step_status", string(rec.Status), attrError, err)
	}
}

// persistStatus writes the execution's terminal state; an error is logged and
// the outcome does not change.
func (e *executor) persistStatus(ctx context.Context, execID string, o *runOptions, status Status, output json.RawMessage, failure string) {
	sctx, cancel := o.storeContext(ctx)
	defer cancel()

	if err := e.store.UpdateStatus(sctx, execID, status, output, failure); err != nil {
		e.log.ErrorContext(ctx, "workflow: the execution status could not be written; the record may have stayed running in the Store",
			attrExecutionID, execID, "status", string(status), attrError, err)
	}
}
