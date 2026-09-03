package workflow_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// --- compensating a step the engine retried ---------------------------------

// TestRetriedStepSideEffectIsCompensated verifies that a step which blew up
// after a retry the engine ITSELF triggered is compensated.
func TestRetriedStepSideEffectIsCompensated(t *testing.T) {
	rec := &recorder{}
	world := map[string]bool{}

	var execID string
	s := step(rec, "reserve")
	s.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		execID = sc.ExecutionID
		if sc.Attempt == 1 {
			// Attempt 1 applied the side effect TO THE WORLD, the answer was lost.
			sc.Shared["res"] = "res_1"
			world["res_1"] = true
			return nil, errors.Unavailable("transient", "the answer was lost")
		}
		return nil, errors.Unavailable("transient", "the service is down")
	}
	s.onCompensate = func(_ context.Context, sc *workflow.StepContext) error {
		if id, ok := sc.Shared["res"].(string); ok {
			delete(world, id)
		}
		return nil
	}

	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	_, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "retried_compensation", Steps: steps(s)}, nil,
		workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 2}))

	require.Error(t, err)
	assert.Contains(t, rec.snapshot(), "compensate:reserve",
		"if the engine retried the step, the step that blew up has to be compensated too")
	assert.Empty(t, world, "the reservation attempt 1 opened must not be left hanging")

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusFailed, exec.Status,
		"if the compensation succeeded, failed is the right record")
}

// TestSingleAttemptFailureIsNotCompensated verifies that a step which blew up on
// its ONLY attempt is NOT compensated: the rule is pierced only for a repeat the
// engine triggered.
func TestSingleAttemptFailureIsNotCompensated(t *testing.T) {
	rec := &recorder{}

	a := step(rec, "a")
	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Invalid("invalid", "b blew up")
	}

	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "tek_deneme", Steps: steps(a, b)}, nil)

	require.Error(t, err)
	calls := rec.snapshot()
	assert.NotContains(t, calls, "compensate:b", "a step that blew up on its only attempt must not be compensated")
	assert.Contains(t, calls, "compensate:a")
}

// TestRetriedStepCompensationFailureMarksExecution verifies that when the
// compensation of a retried step blows up too, the execution is written as
// compensation_failed.
func TestRetriedStepCompensationFailureMarksExecution(t *testing.T) {
	rec := &recorder{}

	var execID string
	s := step(rec, "reserve")
	s.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		execID = sc.ExecutionID
		return nil, errors.Unavailable("transient", "attempt %d blew up", sc.Attempt)
	}
	s.onCompensate = func(context.Context, *workflow.StepContext) error {
		return errors.Unavailable("transient", "the compensation blew up too")
	}

	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	_, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "telafi_de_patlar", Steps: steps(s)}, nil,
		workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 2}))

	require.Error(t, err)

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusCompensationFailed, exec.Status)
}

// --- the compensation record must not overwrite the Invoke trace ------------

// TestCompensationRecordKeepsInvokeOutput verifies that the compensation record
// preserves Invoke's output and attempt count; a human intervention rests on
// exactly that data.
func TestCompensationRecordKeepsInvokeOutput(t *testing.T) {
	rec := &recorder{}

	var execID string
	kere := 0
	a := step(rec, "reserve")
	a.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		execID = sc.ExecutionID
		kere++
		if kere == 1 {
			return nil, errors.Unavailable("transient", "the first attempt blew up")
		}
		return map[string]string{"reservation_id": "res_42"}, nil
	}

	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		// A non-retryable class: b blows up on its only attempt.
		return nil, errors.Invalid("invalid", "b blew up")
	}

	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "iz_korunur", Steps: steps(a, b)}, nil,
		workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 3}))
	require.Error(t, err)

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	require.NotEmpty(t, exec.Steps)

	got := exec.Steps[0]
	assert.Equal(t, workflow.StepCompensated, got.Status)
	assert.JSONEq(t, `{"reservation_id":"res_42"}`, string(got.Output),
		"the compensation record must not erase Invoke's output")
	assert.Equal(t, 2, got.Attempts, "Invoke's attempt count has to be preserved")
	assert.False(t, got.StartedAt.IsZero())
}

// --- the compensation budget is per step ------------------------------------

// TestCompensationBudgetIsPerStep verifies that a slow compensation does not
// leave the compensation of the steps BEFORE it with a dead context.
func TestCompensationBudgetIsPerStep(t *testing.T) {
	rec := &recorder{}

	var firstCompensationErr error
	a := step(rec, "fast")
	a.onCompensate = func(ctx context.Context, _ *workflow.StepContext) error {
		firstCompensationErr = ctx.Err()
		return nil
	}

	b := step(rec, "slow")
	b.onCompensate = func(context.Context, *workflow.StepContext) error {
		// A slow compensation that ignores the context: it overruns its budget.
		time.Sleep(150 * time.Millisecond)
		return nil
	}

	c := step(rec, "c")
	c.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Invalid("invalid", "c blew up")
	}

	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "budget", Steps: steps(a, b, c)}, nil,
		workflow.WithCompensationTimeout(50*time.Millisecond))

	require.Error(t, err)
	assert.NoError(t, firstCompensationErr,
		"every compensation gets its own budget; the earlier step must not be called with a dead context")
}

// --- a custom Retryable predicate inherits the panic/cancel exclusion -------

// TestCustomRetryableStillSkipsPanic verifies that a panic is NOT retried even
// when the custom predicate says everything is retryable.
func TestCustomRetryableStillSkipsPanic(t *testing.T) {
	rec := &recorder{}
	calls := 0

	s := step(rec, "panicking")
	s.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		calls++
		panic("the same crash on every attempt")
	}

	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "ozel_yuklem_panik", Steps: steps(s)}, nil,
		workflow.WithRetry(workflow.RetryPolicy{
			MaxAttempts: 3,
			Retryable:   func(error) bool { return true },
		}))

	require.Error(t, err)
	assert.ErrorIs(t, err, workflow.ErrPanic)
	assert.Equal(t, 1, calls, "a panic must not be retried with a custom predicate either")
}

// TestCustomRetryableStillSkipsCanceledContext verifies that the custom
// predicate inherits the cancellation exclusion as well.
func TestCustomRetryableStillSkipsCanceledContext(t *testing.T) {
	rec := &recorder{}
	calls := 0

	s := step(rec, "iptal")
	s.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		calls++
		// The dependency returns with a canceled sub-context; the engine's own
		// context is ALIVE, so the only thing stopping the repeat is the
		// exclusion rule.
		return nil, errors.Wrap(context.Canceled, errors.KindUnavailable, "canceled", "the call was canceled")
	}

	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "ozel_yuklem_iptal", Steps: steps(s)}, nil,
		workflow.WithRetry(workflow.RetryPolicy{
			MaxAttempts: 3,
			Retryable:   func(error) bool { return true },
		}))

	require.Error(t, err)
	assert.Equal(t, 1, calls, "a canceled context must not be retried")
}

// --- a call arriving with a dead context must not burn the key --------------

// TestCanceledContextDoesNotBurnIdempotencyKey verifies that a call canceled
// before any step ran DOES NOT make the same key unusable.
func TestCanceledContextDoesNotBurnIdempotencyKey(t *testing.T) {
	rec := &recorder{}
	s := step(rec, "a")

	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())
	wf := workflow.Workflow{Name: "dead_context", Steps: steps(s)}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := eng.Run(ctx, wf, nil, workflow.WithIdempotencyKey("order-1"))
	require.Error(t, err)
	assert.Empty(t, rec.snapshot(), "no step may have run")

	out, err := eng.Run(t.Context(), wf, nil, workflow.WithIdempotencyKey("order-1"))
	require.NoError(t, err, "with no work done the key has to be reusable")
	assert.NotNil(t, out)
}

// --- a typed nil step -------------------------------------------------------

// TestTypedNilStepIsRejected verifies that a typed-nil step does not bring the
// engine down.
func TestTypedNilStepIsRejected(t *testing.T) {
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	var empty *testStep // a typed nil: the interface value is NOT nil
	wf := workflow.Workflow{Name: "typed_nil", Steps: []workflow.Step{empty}}

	_, err := eng.Run(t.Context(), wf, nil)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a typed nil step is an invalid workflow, not a panic")
}

// --- name and key length limits ---------------------------------------------

// TestWorkflowNameLengthIsValidated verifies that a name over the limit is
// rejected in the engine: better known here than by hitting the durable Store's
// own limit down there.
func TestWorkflowNameLengthIsValidated(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	long := ""
	for range workflow.MaxNameLen + 1 {
		long += "a"
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: long, Steps: steps(step(rec, "a"))}, nil)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))

	_, err = eng.Run(t.Context(),
		workflow.Workflow{Name: "kisa", Steps: steps(step(rec, long))}, nil)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the step name is bounded too")
}

// --- the concurrent composite's Shared hygiene ------------------------------

// TestParallelFailureDiscardsBranchWrites verifies that after a composite blows
// up NO branch's write is left in the parent context.
func TestParallelFailureDiscardsBranchWrites(t *testing.T) {
	rec := &recorder{}

	var seenRes any
	var ghostFound bool

	first := step(rec, "order")
	first.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		sc.Shared["res"] = "original"
		return "order-out", nil
	}
	first.onCompensate = func(_ context.Context, sc *workflow.StepContext) error {
		seenRes = sc.Shared["res"]
		_, ghostFound = sc.Shared["ghost"]
		return nil
	}

	a := step(rec, "stock")
	a.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		sc.Shared["res"] = "branch_reservation"
		return "a-out", nil
	}

	b := step(rec, "shipping")
	b.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		sc.Shared["ghost"] = "present"
		return nil, errors.Invalid("invalid", "the shipping branch blew up")
	}

	par := workflow.NewParallel("pair", a, b).WithLogger(testLogger())
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	_, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "branch_contamination", Steps: []workflow.Step{first, par}}, nil)

	require.Error(t, err)
	assert.Equal(t, "original", seenRes,
		"a rolled-back branch's write must not leak into the earlier step's compensation")
	assert.False(t, ghostFound, "the write of the branch that blew up must not be merged into the parent context")
}

// TestParallelMergeKeepsWritingBranchValue verifies that the stale copy of a
// read-only branch does not overwrite the value its writing sibling produced.
func TestParallelMergeKeepsWritingBranchValue(t *testing.T) {
	rec := &recorder{}

	first := step(rec, "first")
	first.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		sc.Shared["res"] = "old"
		return "first-out", nil
	}

	writer := step(rec, "writer")
	writer.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		sc.Shared["res"] = "new"
		return "writer-out", nil
	}

	reader := step(rec, "reader")
	reader.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		_ = sc.Shared["res"] // reads only, never writes
		return "reader-out", nil
	}

	var afterwards any
	later := step(rec, "later")
	later.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		afterwards = sc.Shared["res"]
		return "later-out", nil
	}

	par := workflow.NewParallel("pair", writer, reader).WithLogger(testLogger())
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	_, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "stale_copy", Steps: []workflow.Step{first, par, later}}, nil)

	require.NoError(t, err)
	assert.Equal(t, "new", afterwards,
		"the stale copy of the branch that did not touch it must not overwrite the writing branch's value")
}

// TestParallelRollbackFailureMarksCompensationFailed verifies that when the
// composite's INTERNAL rollback blows up the execution is written as
// compensation_failed rather than failed.
func TestParallelRollbackFailureMarksCompensationFailed(t *testing.T) {
	rec := &recorder{}

	var execID string
	a := step(rec, "a")
	a.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		execID = sc.ExecutionID
		return "a-out", nil
	}
	a.onCompensate = func(context.Context, *workflow.StepContext) error {
		return errors.Unavailable("transient", "branch a could not be rolled back")
	}

	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Invalid("invalid", "branch b blew up")
	}

	par := workflow.NewParallel("pair", a, b).WithLogger(testLogger())
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	_, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "internal_rollback", Steps: []workflow.Step{par}}, nil)
	require.Error(t, err)

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusCompensationFailed, exec.Status,
		"a branch side effect left hanging cannot be written as failed")
}

// --- Run's output type ------------------------------------------------------

// TestRunOutputTypeIsStable verifies that the happy path and the repeat return
// the SAME Go type: the caller's type assertion cannot depend on a race.
func TestRunOutputTypeIsStable(t *testing.T) {
	rec := &recorder{}
	wf := workflow.Workflow{Name: "stable_type", Steps: steps(step(rec, "a"))}

	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	first, err := eng.Run(t.Context(), wf, nil, workflow.WithIdempotencyKey("k"))
	require.NoError(t, err)
	repeat, err := eng.Run(t.Context(), wf, nil, workflow.WithIdempotencyKey("k"))
	require.NoError(t, err)

	firstRaw, ok := first.(json.RawMessage)
	require.True(t, ok, "the happy path has to return a json.RawMessage too")
	repeatRaw, ok := repeat.(json.RawMessage)
	require.True(t, ok)

	assert.JSONEq(t, string(firstRaw), string(repeatRaw))
}

// TestParallelRetryStartsFromCleanShared verifies that attempt 2 of a retried
// composite DOES NOT SEE the data of the rolled-back attempt 1.
func TestParallelRetryStartsFromCleanShared(t *testing.T) {
	rec := &recorder{}

	var seen []any
	a := step(rec, "a")
	a.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		seen = append(seen, sc.Shared["res"])
		sc.Shared["res"] = fmt.Sprintf("res_%d", sc.Attempt)
		return "a-out", nil
	}

	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Unavailable("transient", "branch b blew up")
	}

	par := workflow.NewParallel("pair", a, b).WithLogger(testLogger())
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	_, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "composite_retry", Steps: []workflow.Step{par}}, nil,
		workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 2}))

	require.Error(t, err)
	require.Len(t, seen, 2)
	assert.Equal(t, []any{nil, nil}, seen,
		"the write of a rolled-back attempt must not leak into the next attempt")
}

// TestUncompensatedSentinelForcesCompensationFailed verifies that a step error
// carrying ErrUncompensated makes the status compensation_failed even when the
// compensation chain finished cleanly.
func TestUncompensatedSentinelForcesCompensationFailed(t *testing.T) {
	rec := &recorder{}

	var execID string
	a := step(rec, "a")
	a.onInvoke = captureExecutionID(&execID)

	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Wrap(workflow.ErrUncompensated, errors.KindInternal,
			"hanging", "b left work behind")
	}

	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "hanging_side_effect", Steps: steps(a, b)}, nil)
	require.Error(t, err)

	assert.Contains(t, rec.snapshot(), "compensate:a", "the earlier step still has to be compensated")

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusCompensationFailed, exec.Status,
		"an execution reporting a hanging side effect cannot be written as failed")
}
