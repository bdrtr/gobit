// recover.go holds ABANDONED EXECUTIONS: a record left running because the
// process that owned it died. This path never calls Invoke — it rebuilds the
// state the compensation chain needs from the steps' own persisted output
// ([Recoverable]) and claims the record before touching it so that only one
// process undoes the work ([ClaimingStore]). It is its own file because it is
// reached only through a declared lease and is the engine's only consumer of the
// claim capability, [StepStatus.Held] and [RecoveryBlocker].

package workflow

import (
	"context"
	"slices"
	"time"

	"github.com/bdrtr/gobit/core/errors"
)

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

	// The claim comes AFTER the read and before the first write. Reading has no
	// side effect, while a won claim STAMPS UpdatedAt and so renews the lease
	// (see [ClaimingStore]); claiming first would mean a caller that then cannot
	// read the steps has silently pushed the record's lease out by a full
	// period, hiding a genuinely stuck saga from the next caller and from
	// "gobit stuck" for exactly that long.
	switch e.claimAbandoned(ctx, prev, o) {
	case claimLost:
		// Somebody else is recovering it; the caller tries to open again.
		return true, nil
	case claimUndecided:
		return false, nil
	case claimWon:
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
			wf.Name, exec.IdempotencyKey, exec.ID)
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

// claimAbandoned takes the abandoned record for THIS process and reports
// whether the claim was won.
//
// If the Store has no claim capability the answer is "won" without a write: the
// capability is optional ([ClaimingStore]) and its absence must not stop the
// recovery, only leave it non-exclusive.
//
// A lost claim means ANOTHER process is recovering the same record right now.
// The caller is then told "abandoned, try opening again" rather than "still
// going": the winner may release the key at any instant, and a second turn
// answers both endings correctly — if the key is free the caller opens a new
// execution, and if the winner is still working the record it finds is FRESH
// (the claim stamps UpdatedAt), so it gets an honest "still going".
//
// A Store error is the undecidable case and falls to the SAFE side, exactly as
// unreadable steps do: the record is left as it stands. Claiming without being
// able to write would let two processes compensate the same saga.
func (e *executor) claimAbandoned(ctx context.Context, prev *Execution, o *runOptions) claimResult {
	claimer, ok := e.store.(ClaimingStore)
	if !ok {
		return claimWon
	}

	sctx, cancel := o.storeContext(ctx)
	won, err := claimer.ClaimAbandoned(sctx, prev.ID, prev.UpdatedAt)
	cancel()

	switch {
	case err != nil:
		e.log.ErrorContext(ctx, "workflow: an abandoned execution could not be claimed for recovery",
			attrWorkflow, prev.Workflow, attrExecutionID, prev.ID, attrError, err)

		return claimUndecided
	case !won:
		e.log.WarnContext(ctx, "workflow: the abandoned execution is being recovered by another process",
			attrWorkflow, prev.Workflow, attrExecutionID, prev.ID)

		return claimLost
	default:
		return claimWon
	}
}

// claimResult is the outcome of a claim on an abandoned record.
type claimResult int

const (
	// claimWon means this process recovers the record.
	claimWon claimResult = iota
	// claimLost means another process is already recovering it.
	claimLost
	// claimUndecided means the Store could not answer; the record is left as it
	// stands and the caller is told the execution is still going.
	claimUndecided
)

// Recoverer is an OPTIONAL capability of the engine: running an abandoned
// execution's compensation ON DEMAND, addressed by ID.
//
// The engine's own recovery path is ARRIVED AT, not triggered: it runs when a
// caller comes back with the same idempotency key (see [WithLease]). That covers
// the customer who retries, and nothing else. If nobody comes back — an
// abandoned cart, a storefront that gave up — the record stays running and the
// stock it reserved stays reserved, listed by "gobit stuck" and released by
// nobody.
//
// This interface is that missing hand. It is deliberately NOT a sweeper: a
// scheduled job that compensates on its own would decide, unwatched, to undo
// work whose side effects are real. Here a HUMAN decides and names the
// execution.
//
// The engine returned by [New] implements it; the capability is reached with a
// type assertion, the same shape as [ClaimingStore] on stores and [Recoverable]
// on steps.
type Recoverer interface {
	// Recover runs the compensation chain of the abandoned execution with this
	// id and writes it into a terminal state.
	//
	// It NEVER calls Invoke: recovery undoes, it does not continue. The workflow
	// definition is needed for its Compensate functions and its step names, and
	// the state the compensations read is rebuilt from the steps' own persisted
	// output ([Recoverable]).
	Recover(ctx context.Context, wf Workflow, executionID string, opts ...RunOption) error
}

// Recover runs the compensation chain of the abandoned execution with this id.
//
// # What it REFUSES, and why every refusal is about money
//
//   - No lease was declared ([WithLease]). Without one, a saga that is STILL
//     RUNNING cannot be told from an abandoned record, and compensating a live
//     saga releases stock a paying customer is about to own.
//   - The record is not [StatusRunning]. A terminal record has nothing left to
//     undo; running the chain over it would call every Compensate a second time
//     for no reason.
//   - The lease has NOT expired. The saga may be in flight in another process.
//   - The claim was lost. Another process is already recovering it
//     ([ClaimingStore]).
//   - The definition given does not carry the record's workflow name. A
//     compensation chain built from another workflow would undo the wrong work.
//
// The boundary at an unrecorded [RecoveryBlocker] holds here too, and it is not
// overridable: whether a capture went through cannot be answered from the
// records, and an operator cannot answer it either without asking the payment
// provider. The command's job is to say so, not to guess.
//
// A record whose lease expired while NO step held work is closed rather than
// compensated: there is nothing to undo, and closing it releases the key.
func (e *executor) Recover(ctx context.Context, wf Workflow, executionID string, opts ...RunOption) error {
	if e.store == nil {
		return errors.Invalid(CodeInvalidOption,
			"the workflow engine was built without a Store; give it a durable Store, or use NewInMemory for an in-process one")
	}

	o, err := newRunOptions(opts)
	if err != nil {
		return err
	}
	if verr := wf.Validate(); verr != nil {
		return verr
	}
	if o.lease <= 0 {
		return errors.Invalid(CodeInvalidOption,
			"recovering %q needs a declared lease (WithLease): without one a saga that is still running cannot be told from an abandoned record",
			wf.Name)
	}

	sctx, cancel := o.storeContext(ctx)
	exec, gerr := e.store.Get(sctx, executionID)
	cancel()

	switch {
	case gerr != nil:
		return errors.Wrap(gerr, errors.KindOf(gerr), CodeStoreFailed,
			"the execution %q could not be read", executionID)
	case exec == nil:
		return errors.Internal(CodeStoreFailed,
			"the Store returned a nil record with no error for the execution %q", executionID)
	case exec.Workflow != wf.Name:
		return errors.Invalid(CodeInvalidWorkflow,
			"the execution %q belongs to the %q workflow, the definition given is %q",
			executionID, exec.Workflow, wf.Name)
	case exec.Status != StatusRunning:
		return errors.Conflict(CodeExecutionFailed,
			"the execution %q is in the %q state; only a running execution can be recovered",
			executionID, exec.Status)
	case time.Since(exec.UpdatedAt) <= o.lease:
		return errors.Conflict(CodeExecutionRunning,
			"the lease of the execution %q has not expired yet (last write %s); it may still be going",
			executionID, exec.UpdatedAt.Format(time.RFC3339))
	}

	switch e.claimAbandoned(ctx, exec, o) {
	case claimLost:
		return errors.Conflict(CodeExecutionRunning,
			"the execution %q is being recovered by another process right now", executionID)
	case claimUndecided:
		return errors.Unavailable(CodeStoreFailed,
			"the execution %q could not be claimed for recovery; the Store did not answer", executionID)
	case claimWon:
	}

	if !hasHeldWork(exec.Steps) {
		e.persistStatus(ctx, exec.ID, o, StatusFailed, nil,
			"the execution was closed on request: its lease had expired and no step was holding work")
		e.log.WarnContext(ctx, "workflow: an abandoned execution was closed on request",
			attrWorkflow, wf.Name, attrExecutionID, exec.ID)

		return nil
	}

	_, rerr := e.recoverExecution(ctx, wf, exec, o)

	return rerr
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
