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
	"reflect"

	"github.com/bdrtr/gobit/core/errors"
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
	// CodeExecutionContended reports that no execution could be opened for the
	// key although every turn found the previous one abandoned and closed it.
	//
	// It names CONTENTION, not a broken state: another process opened a new
	// execution with the same key in the meantime and that one also looked
	// abandoned. Nothing was run, so a repeat is safe — the code is
	// KindUnavailable exactly for that reason.
	CodeExecutionContended = "workflow_execution_contended"
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
//
// # Compensate can be called CONCURRENTLY
//
// "Called twice" also means twice AT THE SAME TIME. On the recovery path
// ([Recoverable]) the abandoned execution is not owned by anybody: every caller
// that comes back with the same idempotency key finds it, and each one runs the
// compensation chain (measured: four concurrent callers ran the same chain four
// times). Compensation that is idempotent only in sequence is therefore not
// enough — a Compensate that reads a quantity, adds to it and writes it back
// releases the stock more than once when two copies interleave. Undoing by
// IDENTITY (delete the reservation with this id, cancel the order with this id)
// is safe; read-modify-write is not.
//
// A Store that implements [ClaimingStore] closes that window: exactly one
// process recovers and the others are told the execution is still going. Both
// Stores shipped with the engine do (see NewMemoryStore and the pgstore
// package), so this requirement is left over for a Store written elsewhere —
// and a step cannot see which Store is wired underneath it.
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
