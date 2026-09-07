// executor.go holds a FRESH RUN: building the engine, opening the record,
// running the steps in order and — when one blows up — unwinding the chain that
// succeeded up to that point. Everything here is driven by a live caller with a
// live context and a workflow definition in hand. The other way into the same
// compensation chain, an execution whose process died, is reached only through a
// lease and lives in recover.go.

package workflow

import (
	"context"
	"encoding/json"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/bdrtr/gobit/core/errors"
)

// executor is Executor's only implementation.
type executor struct {
	store Store
	log   *slog.Logger
}

var (
	_ Executor  = (*executor)(nil)
	_ Recoverer = (*executor)(nil)
)

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
	for range 2 {
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
		if !again {
			return out, rerr
		}
	}

	// "Try again" was said on the second turn too and the bound stops the loop
	// here. The return value MUST be an error: replay's own return value is
	// (nil, nil) on this path, and handing it back would report SUCCESS with no
	// step having run and no output — the worst possible lie a saga engine can
	// tell, because the caller reads a nil error as "the order was placed"
	// (measured; the engine used to return exactly that).
	//
	// The class is KindUnavailable and not KindInternal: nothing is broken, the
	// key is contended, and the caller CAN safely repeat the request with the
	// same key.
	return nil, errors.Unavailable(CodeExecutionContended,
		"no execution could be opened for the %q workflow with the key %q: the key was held by an execution that looked abandoned on both turns; nothing was run, the request can be repeated",
		wf.Name, o.idempotencyKey)
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
		if errors.IsNotFound(err) {
			// Our own Create had just said the key was taken, and now it is
			// gone: between the two calls someone RELEASED the key — a
			// compensated execution drops its key (see [Store.UpdateStatus]).
			// This is an ordinary interleaving, not a Store failure; the caller
			// tries to open again. Turning it into an error would answer a race
			// that resolves ITSELF with a 500 (measured: two callers arriving
			// on the same abandoned record produce exactly this).
			e.log.WarnContext(ctx, "workflow: the idempotency key was released while it was being read, opening again",
				attrWorkflow, wf.Name, "idempotency_key", o.idempotencyKey)

			return nil, true, nil
		}

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
// core/http.WriteError). A code is fixed and machine-readable by
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
