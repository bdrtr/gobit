package workflow_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// --- test helpers -----------------------------------------------------------

// testLogger produces a log that does not pollute the tests.
//
// The panic tests log a stack trace; in the test output that is noise.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// recorder records the step calls IN ARRIVAL ORDER.
//
// That is what proves the compensation order is reversed: the order is kept in a
// slice rather than a set, and the tests assert exact equality.
type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, s)
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

// testStep is the configurable fake step.
type testStep struct {
	name         string
	rec          *recorder
	onInvoke     func(ctx context.Context, sc *workflow.StepContext) (any, error)
	onCompensate func(ctx context.Context, sc *workflow.StepContext) error
}

func (s *testStep) Name() string { return s.name }

func (s *testStep) Invoke(ctx context.Context, sc *workflow.StepContext) (any, error) {
	s.rec.add("invoke:" + s.name)
	if s.onInvoke != nil {
		return s.onInvoke(ctx, sc)
	}
	return s.name + "-out", nil
}

func (s *testStep) Compensate(ctx context.Context, sc *workflow.StepContext) error {
	s.rec.add("compensate:" + s.name)
	if s.onCompensate != nil {
		return s.onCompensate(ctx, sc)
	}
	return nil
}

// rawOutput returns Run's output as raw JSON text.
//
// The output type is a json.RawMessage on the happy path and on the repeat
// alike; the helper re-verifies that contract at every call site.
func rawOutput(t *testing.T, out any) string {
	t.Helper()

	raw, ok := out.(json.RawMessage)
	require.True(t, ok, "the Run output has to be a json.RawMessage, %T arrived", out)
	return string(raw)
}

// step produces a step with the default behavior (always succeeds).
func step(rec *recorder, name string) *testStep {
	return &testStep{name: name, rec: rec}
}

// steps converts a testStep slice into a workflow.Step slice.
func steps(list ...*testStep) []workflow.Step {
	out := make([]workflow.Step, len(list))
	for i, s := range list {
		out[i] = s
	}
	return out
}

// captureExecutionID carries the execution id the step saw to the outside.
func captureExecutionID(dst *string) func(context.Context, *workflow.StepContext) (any, error) {
	return func(_ context.Context, sc *workflow.StepContext) (any, error) {
		*dst = sc.ExecutionID
		return sc.StepName + "-out", nil
	}
}

// --- the compensation (saga) core -------------------------------------------

// TestCompensationReverseOrderSkipsFailedStep is the literal counterpart of
// Phase 3's DoD.
func TestCompensationReverseOrderSkipsFailedStep(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	boom := errors.New("step 2 blew up")

	var execID string
	first := step(rec, "first")
	first.onInvoke = captureExecutionID(&execID)

	second := step(rec, "second")
	second.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Wrap(boom, errors.KindUnavailable, "second_failed", "the second step")
	}

	third := step(rec, "third")

	wf := workflow.Workflow{Name: "three_steps", Steps: steps(first, second, third)}

	out, err := eng.Run(t.Context(), wf, map[string]any{"x": 1})

	require.Error(t, err)
	assert.Nil(t, out)
	assert.ErrorIs(t, err, boom, "the returned error has to carry the error of the step that blew up")

	// DoD: compensation runs in REVERSE ORDER and ONLY for the steps that
	// succeeded.
	assert.Equal(t, []string{
		"invoke:first",
		"invoke:second",
		"compensate:first",
	}, rec.snapshot())

	calls := rec.snapshot()
	assert.NotContains(t, calls, "invoke:third", "the step after the one that blew up must never have run")
	assert.NotContains(t, calls, "compensate:second", "the step that blew up must not be compensated ITSELF")

	// Because compensation completed in full the status is failed (not
	// compensation_failed).
	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusFailed, exec.Status)
	assert.NotEmpty(t, exec.Failure)

	require.Len(t, exec.Steps, 2, "a step that never ran must not enter the record")
	assert.Equal(t, workflow.StepCompensated, exec.Steps[0].Status)
	assert.Equal(t, 0, exec.Steps[0].Index)
	assert.Equal(t, workflow.StepFailed, exec.Steps[1].Status)
	assert.Equal(t, 1, exec.Steps[1].Index)
	assert.NotEmpty(t, exec.Steps[1].Failure)
}

// TestCompensationOrderWithFiveSteps verifies that when step 4 blows up the
// order is 3, 2, 1.
func TestCompensationOrderWithFiveSteps(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	s1, s2, s3 := step(rec, "1"), step(rec, "2"), step(rec, "3")
	s4 := step(rec, "4")
	s4.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Invalid("fourth", "the fourth step blew up")
	}
	s5 := step(rec, "5")

	wf := workflow.Workflow{Name: "five_steps", Steps: steps(s1, s2, s3, s4, s5)}

	_, err := eng.Run(t.Context(), wf, nil)
	require.Error(t, err)

	assert.Equal(t, []string{
		"invoke:1", "invoke:2", "invoke:3", "invoke:4",
		"compensate:3", "compensate:2", "compensate:1",
	}, rec.snapshot())
}

// TestSuccessfulRunPersistsCompletedState verifies the durable trace of a
// successful run.
func TestSuccessfulRunPersistsCompletedState(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	var execID string
	a := step(rec, "a")
	a.onInvoke = captureExecutionID(&execID)
	b, c := step(rec, "b"), step(rec, "c")

	wf := workflow.Workflow{Name: "successful", Steps: steps(a, b, c)}

	out, err := eng.Run(t.Context(), wf, map[string]any{"input": "value"})
	require.NoError(t, err)
	assert.JSONEq(t, `"c-out"`, rawOutput(t, out), "the workflow output is the LAST step's output")

	assert.Equal(t, []string{"invoke:a", "invoke:b", "invoke:c"}, rec.snapshot())

	exec, err := store.Get(t.Context(), execID)
	require.NoError(t, err)

	assert.Equal(t, workflow.StatusCompleted, exec.Status)
	assert.Equal(t, "successful", exec.Workflow)
	assert.Empty(t, exec.Failure)
	assert.JSONEq(t, `{"input":"value"}`, string(exec.Input))
	assert.JSONEq(t, `"c-out"`, string(exec.Output))

	require.Len(t, exec.Steps, 3)
	for i, want := range []string{"a", "b", "c"} {
		assert.Equal(t, i, exec.Steps[i].Index, "the step records have to be IN ORDER")
		assert.Equal(t, want, exec.Steps[i].Name)
		assert.Equal(t, workflow.StepInvoked, exec.Steps[i].Status)
		assert.Equal(t, 1, exec.Steps[i].Attempts)
		assert.JSONEq(t, `"`+want+`-out"`, string(exec.Steps[i].Output))
		assert.False(t, exec.Steps[i].StartedAt.IsZero())
		assert.False(t, exec.Steps[i].EndedAt.IsZero())
	}
}

// TestCompensationFailureContinuesChain verifies that a compensation failure
// does not stop the chain.
func TestCompensationFailureContinuesChain(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	invokeBoom := errors.New("the third step blew up")
	compBoom := errors.New("the compensation of the second step blew up")

	var execID string
	a := step(rec, "a")
	a.onInvoke = captureExecutionID(&execID)

	b := step(rec, "b")
	b.onCompensate = func(context.Context, *workflow.StepContext) error {
		return errors.Wrap(compBoom, errors.KindInternal, "b_comp", "the compensation of b")
	}

	c := step(rec, "c")
	c.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Wrap(invokeBoom, errors.KindInvalid, "c_invoke", "step c")
	}

	wf := workflow.Workflow{Name: "compensation_fails", Steps: steps(a, b, c)}

	_, err := eng.Run(t.Context(), wf, nil)
	require.Error(t, err)

	// The chain blew up at b but a's compensation STILL ran.
	assert.Equal(t, []string{
		"invoke:a", "invoke:b", "invoke:c",
		"compensate:b", "compensate:a",
	}, rec.snapshot())

	assert.ErrorIs(t, err, invokeBoom, "the error has to contain the step error")
	assert.ErrorIs(t, err, compBoom, "the error has to contain the compensation error too")
	assert.Equal(t, errors.KindInternal, errors.KindOf(err), "a state that needs a human has to be internal")
	assert.Equal(t, workflow.CodeCompensationFailed, errors.CodeOf(err))

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusCompensationFailed, exec.Status)

	require.Len(t, exec.Steps, 3)
	assert.Equal(t, workflow.StepCompensated, exec.Steps[0].Status, "a has to have been compensated")
	assert.Equal(t, workflow.StepCompensationFailed, exec.Steps[1].Status)
	assert.Equal(t, workflow.StepFailed, exec.Steps[2].Status)
}

// TestCompensationErrorDoesNotMaskStepError verifies that the compensation error
// does not take the place of the step error; both stay in the returned error.
func TestCompensationErrorDoesNotMaskStepError(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	a := step(rec, "a")
	a.onCompensate = func(context.Context, *workflow.StepContext) error {
		return errors.Internal("a_comp", "the compensation of a blew up")
	}
	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Conflict("b_invoke", "step b clashed")
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "masking", Steps: steps(a, b)}, nil)
	require.Error(t, err)

	msg := err.Error()
	assert.Contains(t, msg, "step b clashed")
	assert.Contains(t, msg, "the compensation of a blew up")
}

// --- retrying ---------------------------------------------------------------

// TestRetrySucceedsAfterTransientFailures verifies that a transient failure
// passes after N attempts.
func TestRetrySucceedsAfterTransientFailures(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	var attempts []int
	var execID string
	flaky := step(rec, "flaky")
	flaky.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		execID = sc.ExecutionID
		attempts = append(attempts, sc.Attempt)
		if sc.Attempt < 3 {
			return nil, errors.Unavailable("transient", "the subsystem is down on attempt %d", sc.Attempt)
		}
		return "at last", nil
	}

	out, err := eng.Run(t.Context(), workflow.Workflow{Name: "retry", Steps: steps(flaky)}, nil,
		workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 5, Backoff: time.Millisecond}))

	require.NoError(t, err)
	assert.JSONEq(t, `"at last"`, rawOutput(t, out))
	assert.Equal(t, []int{1, 2, 3}, attempts, "StepContext.Attempt has to start at 1 and grow")
	assert.Len(t, rec.snapshot(), 3, "the step has to be called three times")

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	require.Len(t, exec.Steps, 1)
	assert.Equal(t, 3, exec.Steps[0].Attempts, "the attempt count has to enter the record")
	assert.Equal(t, workflow.StepInvoked, exec.Steps[0].Status)
}

// TestRetrySkippedForNonRetryableKind verifies that the Invalid class fails
// immediately.
func TestRetrySkippedForNonRetryableKind(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	var execID string
	bad := step(rec, "bad")
	bad.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		execID = sc.ExecutionID
		return nil, errors.Invalid("invalid_input", "the input is invalid")
	}

	start := time.Now()
	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "no_retry", Steps: steps(bad)}, nil,
		workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 5, Backoff: 2 * time.Second}))
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Len(t, rec.snapshot(), 1, "Invalid MUST NOT be retried")
	assert.Less(t, elapsed, time.Second, "the backoff must never have been waited out")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "the error class has to be preserved")

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	require.Len(t, exec.Steps, 1)
	assert.Equal(t, 1, exec.Steps[0].Attempts)
}

// TestDefaultPolicyDoesNotRetry verifies that the default is a single attempt.
func TestDefaultPolicyDoesNotRetry(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	flaky := step(rec, "flaky")
	flaky.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		return nil, errors.Unavailable("transient", "attempt %d", sc.Attempt)
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "default", Steps: steps(flaky)}, nil)
	require.Error(t, err)
	assert.Len(t, rec.snapshot(), 1, "without WithRetry there must be no retry")
}

// TestCompensationIsRetried verifies that the compensation is retried too.
func TestCompensationIsRetried(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	var execID string
	a := step(rec, "a")
	a.onInvoke = captureExecutionID(&execID)
	compCalls := 0
	a.onCompensate = func(_ context.Context, sc *workflow.StepContext) error {
		compCalls++
		if sc.Attempt < 2 {
			return errors.Unavailable("transient_compensation", "the compensation failed transiently")
		}
		return nil
	}

	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Internal("b_failed", "b blew up")
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "compensation_retry", Steps: steps(a, b)}, nil,
		workflow.WithCompensationRetry(workflow.RetryPolicy{MaxAttempts: 3, Backoff: time.Millisecond}))
	require.Error(t, err)

	assert.Equal(t, 2, compCalls, "the compensation has to succeed on the second attempt")

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusFailed, exec.Status, "the status is failed because the compensation eventually succeeded")
	require.Len(t, exec.Steps, 2)
	assert.Equal(t, workflow.StepCompensated, exec.Steps[0].Status)
	assert.Equal(t, 1, exec.Steps[0].Attempts,
		"Attempts is the INVOKE attempt count; the compensation record does not overwrite it, compensation attempts are logged")
	assert.JSONEq(t, `"a-out"`, string(exec.Steps[0].Output),
		"the compensation record has to preserve Invoke's output too")
}

// TestDefaultRetryable exercises the classification decision directly.
func TestDefaultRetryable(t *testing.T) {
	assert.False(t, workflow.DefaultRetryable(nil))
	assert.True(t, workflow.DefaultRetryable(errors.Unavailable("x", "transient")))
	assert.True(t, workflow.DefaultRetryable(errors.Internal("x", "unexpected")))
	assert.True(t, workflow.DefaultRetryable(errors.New("unclassified")))
	assert.False(t, workflow.DefaultRetryable(errors.Invalid("x", "input")))
	assert.False(t, workflow.DefaultRetryable(errors.Conflict("x", "clash")))
	assert.False(t, workflow.DefaultRetryable(errors.NotFound("x", "missing")))
	assert.False(t, workflow.DefaultRetryable(errors.Unauthorized("x", "unauthorized")))
	assert.False(t, workflow.DefaultRetryable(errors.Forbidden("x", "forbidden")))
	assert.False(t, workflow.DefaultRetryable(context.Canceled))
	assert.False(t, workflow.DefaultRetryable(context.DeadlineExceeded))
	assert.False(t, workflow.DefaultRetryable(workflow.ErrPanic), "a panic must not be retried")
}

// TestCustomRetryablePredicate verifies that the custom classification is used.
func TestCustomRetryablePredicate(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	s := step(rec, "s")
	s.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		if sc.Attempt < 2 {
			// A class the default WOULD NOT retry.
			return nil, errors.Invalid("invalid", "invalid")
		}
		return "done", nil
	}

	out, err := eng.Run(t.Context(), workflow.Workflow{Name: "custom", Steps: steps(s)}, nil,
		workflow.WithRetry(workflow.RetryPolicy{
			MaxAttempts: 3,
			Retryable:   func(error) bool { return true },
		}))

	require.NoError(t, err)
	assert.JSONEq(t, `"done"`, rawOutput(t, out))
	assert.Len(t, rec.snapshot(), 2)
}

// --- idempotency ------------------------------------------------------------

// TestIdempotencyReplayDoesNotRerunSteps verifies that a second call with the
// same key does not run the steps again.
func TestIdempotencyReplayDoesNotRerunSteps(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	wf := workflow.Workflow{Name: "idem", Steps: steps(step(rec, "a"), step(rec, "b"))}

	first, err := eng.Run(t.Context(), wf, map[string]any{"n": 1}, workflow.WithIdempotencyKey("key-1"))
	require.NoError(t, err)
	assert.JSONEq(t, `"b-out"`, rawOutput(t, first))
	assert.Equal(t, []string{"invoke:a", "invoke:b"}, rec.snapshot())

	second, err := eng.Run(t.Context(), wf, map[string]any{"n": 1}, workflow.WithIdempotencyKey("key-1"))
	require.NoError(t, err)

	assert.Equal(t, []string{"invoke:a", "invoke:b"}, rec.snapshot(),
		"the second call must not run any step AGAIN")

	assert.JSONEq(t, `"b-out"`, rawOutput(t, second),
		"the repeat call returns the SAME type as the happy path")
}

// TestCompensatedExecutionCanBeRetriedWithTheSameKey is the contract that closes
// this engine's most expensive failure.
//
// [workflow.StatusFailed] means "a step blew up and compensation completed IN
// FULL": there is no half-finished work. So the attempt's idempotency key must
// not be held either — a key is a trace too.
//
// What happened while it was held was measured: a customer whose card was
// declined in the storefront COULD NEVER PAY FOR THAT CART AGAIN. Because the
// key is derived from the cart id, the engine's advice to "use a NEW key to try
// again" had no counterpart on the HTTP surface either; the cart returned 409
// forever.
func TestCompensatedExecutionCanBeRetriedWithTheSameKey(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	shouldFail := true
	second := &testStep{name: "b", rec: rec, onInvoke: func(context.Context, *workflow.StepContext) (any, error) {
		if shouldFail {
			return nil, errors.Internal("declined", "the payment was declined")
		}

		return "b-out", nil
	}}
	wf := workflow.Workflow{Name: "idem", Steps: steps(step(rec, "a"), second)}

	_, err := eng.Run(t.Context(), wf, nil, workflow.WithIdempotencyKey("cart-1"))
	require.Error(t, err, "the first attempt has to fail")
	require.Equal(t, []string{"invoke:a", "invoke:b", "compensate:a"}, rec.snapshot(),
		"the compensation has to run; that is what the contract rests on")

	shouldFail = false

	out, err := eng.Run(t.Context(), wf, nil, workflow.WithIdempotencyKey("cart-1"))
	require.NoError(t, err,
		"a compensated attempt MUST NOT hold its key; the customer has to be able to pay for the same cart again")
	assert.JSONEq(t, `"b-out"`, rawOutput(t, out))
	assert.Equal(t, []string{"invoke:a", "invoke:b", "compensate:a", "invoke:a", "invoke:b"},
		rec.snapshot(), "the second attempt has to run the steps FROM THE START")
}

// TestCompletedExecutionDoesNotReleaseItsKey verifies that the release rule
// belongs to the compensated attempt alone.
//
// If a completed execution released its key the same cart would be charged
// TWICE — that is, the whole reason the idempotency key exists would be gone.
func TestCompletedExecutionDoesNotReleaseItsKey(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())
	wf := workflow.Workflow{Name: "idem", Steps: steps(step(rec, "a"))}

	_, err := eng.Run(t.Context(), wf, nil, workflow.WithIdempotencyKey("cart-2"))
	require.NoError(t, err)

	_, err = eng.Run(t.Context(), wf, nil, workflow.WithIdempotencyKey("cart-2"))
	require.NoError(t, err)

	assert.Equal(t, []string{"invoke:a"}, rec.snapshot(),
		"the second call must not run the step AGAIN")
}

// TestUncompensatedExecutionDoesNotReleaseItsKey draws the second boundary.
//
// If the compensation could not be completed there is HALF-FINISHED work waiting
// for a human, and releasing the key means a new attempt landing on top of it.
// So in that case the record holds its key and the caller gets a 409.
func TestUncompensatedExecutionDoesNotReleaseItsKey(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	first := &testStep{name: "a", rec: rec, onCompensate: func(context.Context, *workflow.StepContext) error {
		return errors.Internal("compensation_failed", "it could not be rolled back")
	}}
	failing := &testStep{name: "b", rec: rec, onInvoke: func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Internal("declined", "the payment was declined")
	}}
	wf := workflow.Workflow{Name: "idem", Steps: steps(first, failing)}

	_, err := eng.Run(t.Context(), wf, nil, workflow.WithIdempotencyKey("cart-3"))
	require.Error(t, err)

	_, err = eng.Run(t.Context(), wf, nil, workflow.WithIdempotencyKey("cart-3"))
	require.Error(t, err, "an execution that could not be compensated MUST hold its key")
	assert.True(t, errors.IsConflict(err), "the caller has to get a 409; error: %v", err)
}

// --- abandoned executions ---------------------------------------------------

// abandonedRecord opens an execution record of the given age in the "running"
// state.
//
// A process CRASH cannot be imitated any other way: crashing means the code that
// would write the terminal state NEVER runs, so driving the engine and cutting
// it short does not produce the same state — which is why the record is written
// straight into the store.
func abandonedRecord(t *testing.T, store workflow.Store, key string, age time.Duration, records ...workflow.StepRecord) string {
	t.Helper()

	instant := time.Now().UTC().Add(-age)
	exec := &workflow.Execution{
		ID: "wfx_ABANDONED_" + key, Workflow: "idem", IdempotencyKey: key,
		Status: workflow.StatusRunning, CreatedAt: instant, UpdatedAt: instant,
	}
	require.NoError(t, store.Create(t.Context(), exec))
	for i := range records {
		require.NoError(t, store.AppendStep(t.Context(), exec.ID, records[i]))
	}

	return exec.ID
}

// TestExpiredLeaseWithNoWorkCanBeRetried closes the most common shape of a
// crash: the process died before doing any work.
//
// There is nothing to compensate, so the record is closed, releases its key and
// the customer can pay for the cart. Had it not been closed — and it was not —
// that cart would have returned 409 forever.
func TestExpiredLeaseWithNoWorkCanBeRetried(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())
	wf := workflow.Workflow{Name: "idem", Steps: steps(step(rec, "a"))}

	id := abandonedRecord(t, store, "cart-abandoned-1", time.Hour)

	out, err := eng.Run(t.Context(), wf, nil,
		workflow.WithIdempotencyKey("cart-abandoned-1"), workflow.WithLease(time.Minute))

	require.NoError(t, err, "an abandoned record MUST NOT block a retry")
	assert.JSONEq(t, `"a-out"`, rawOutput(t, out))
	assert.Equal(t, []string{"invoke:a"}, rec.snapshot(), "the steps really have to run")

	old, err := store.Get(t.Context(), id)
	require.NoError(t, err, "the abandoned record is NOT DELETED, it is an audit trail")
	assert.Equal(t, workflow.StatusFailed, old.Status)
	assert.Contains(t, old.Failure, "lease expired")
}

// The exam for an abandoned execution that DID work is not here but in
// pgstore's integration test
// ([TestAbandonedExecutionThatDidWorkNeedsAHuman]). The reason is not a gap
// in this package but a behavior BOTH stores share: AppendStep REFRESHES the
// execution's updated_at — which is right, a saga that is making progress has to
// keep its lease alive — so the state "has a step AND is stale" cannot be built
// through the store surface. Production reaches it by crashing; a test has to
// reach it by winding time back, and that is only possible against the real
// store.

// TestAbandonedExecutionWithUnreadableStepsFallsToTheSafeSide verifies the fall
// to the safe side.
//
// What a record with an expired lease did can only be told from its step
// records. If the store cannot hand them over the question CANNOT BE DECIDED,
// and the two wrong answers do not cost the same: saying "still going" keeps the
// customer waiting, while saying "abandoned, try again" starts a second saga on
// top of work that may be half done — that is, it reserves the stock a second
// time. So an unreadable record is left exactly as it stands.
func TestAbandonedExecutionWithUnreadableStepsFallsToTheSafeSide(t *testing.T) {
	rec := &recorder{}
	store := &brokenStore{
		Store:  workflow.NewMemoryStore(),
		getErr: errors.Unavailable("store_down", "the steps could not be read"),
	}
	eng := workflow.New(store, testLogger())
	wf := workflow.Workflow{Name: "idem", Steps: steps(step(rec, "a"))}

	abandonedRecord(t, store, "cart-abandoned-5", time.Hour)

	_, err := eng.Run(t.Context(), wf, nil,
		workflow.WithIdempotencyKey("cart-abandoned-5"), workflow.WithLease(time.Minute))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "is still going",
		"if the steps cannot be read, being abandoned CANNOT BE DECIDED")
	assert.Empty(t, rec.snapshot(), "no step may be run")
}

// TestExecutionWithLiveLeaseSaysStillGoing draws the other side of the boundary.
//
// Taking an execution that is really running for an abandoned one means starting
// a SECOND saga for the same cart — that is, reserving the stock twice. So the
// decision rests not on age but on the LEASE the caller declared.
func TestExecutionWithLiveLeaseSaysStillGoing(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())
	wf := workflow.Workflow{Name: "idem", Steps: steps(step(rec, "a"))}

	abandonedRecord(t, store, "cart-abandoned-3", time.Second)

	_, err := eng.Run(t.Context(), wf, nil,
		workflow.WithIdempotencyKey("cart-abandoned-3"), workflow.WithLease(time.Hour))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "is still going")
	assert.Empty(t, rec.snapshot())
}

// TestWithoutALeaseTheBehaviorDoesNotChange verifies that the option is
// OPTIONAL.
//
// A caller that declares no lease has not told the engine how long its flow may
// take; the engine does not guess on its behalf.
func TestWithoutALeaseTheBehaviorDoesNotChange(t *testing.T) {
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())
	wf := workflow.Workflow{Name: "idem", Steps: steps(step(&recorder{}, "a"))}

	abandonedRecord(t, store, "cart-abandoned-4", 365*24*time.Hour)

	_, err := eng.Run(t.Context(), wf, nil, workflow.WithIdempotencyKey("cart-abandoned-4"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "is still going")
}

// alwaysAgain shows the key as taken on EVERY Create and always returns the
// record it finds as STALE: that is, it makes the engine say "abandoned, try
// again" on both turns.
//
// In production this is two abandoned executions in a row (or a lease declared
// shorter than the real saga takes): the record is closed, the key is released
// and another process in between opens a NEW execution with the same key — and
// that one looks stale too.
type alwaysAgain struct {
	workflow.Store

	mu      sync.Mutex
	updates int
}

func (s *alwaysAgain) Create(context.Context, *workflow.Execution) error {
	return errors.Conflict(workflow.CodeExecutionExists, "the key belongs to somebody else")
}

func (s *alwaysAgain) stale(id, wfName, key string) *workflow.Execution {
	old := time.Now().UTC().Add(-time.Hour)

	return &workflow.Execution{
		ID: id, Workflow: wfName, IdempotencyKey: key,
		Status: workflow.StatusRunning, CreatedAt: old, UpdatedAt: old,
	}
}

func (s *alwaysAgain) FindByIdempotencyKey(_ context.Context, wfName, key string) (*workflow.Execution, error) {
	return s.stale("wfx_SOMEBODY_ELSE", wfName, key), nil
}

func (s *alwaysAgain) Get(_ context.Context, id string) (*workflow.Execution, error) {
	return s.stale(id, "idem", "cart-contended"), nil
}

func (s *alwaysAgain) UpdateStatus(context.Context, string, workflow.Status, json.RawMessage, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates++

	return nil
}

// TestKeyAbandonedOnBothTurnsDoesNotReturnSilentSuccess closes the WORST LIE the
// engine COULD TELL.
//
// The engine tries to open the key for at most two turns. If the second turn
// also answers "abandoned, try again" the loop ends — and at that point replay's
// return value is (nil, nil). That value used to be handed to the caller as it
// stood: nil error, nil output, NO STEP RUN. The caller reads a nil error as
// "the order was placed"; in the cart flow that means a success response with no
// order opened at all (measured: out=<nil> err=<nil> invokes=0).
//
// The right answer is an error, and its class is KindUnavailable: nothing is
// broken, the key is contended; because no step ran the caller can repeat with
// the SAME key.
func TestKeyAbandonedOnBothTurnsDoesNotReturnSilentSuccess(t *testing.T) {
	rec := &recorder{}
	store := &alwaysAgain{Store: workflow.NewMemoryStore()}
	eng := workflow.New(store, testLogger())
	wf := workflow.Workflow{Name: "idem", Steps: steps(step(rec, "a"))}

	out, err := eng.Run(t.Context(), wf, nil,
		workflow.WithIdempotencyKey("cart-contended"), workflow.WithLease(time.Minute))

	require.Error(t, err, "returning SUCCESS with no step run is the worst lie")
	assert.Nil(t, out)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err),
		"nothing is broken, the key is contended: a repeat is safe")
	assert.Equal(t, workflow.CodeExecutionContended, errors.CodeOf(err))
	assert.Empty(t, rec.snapshot(), "no step may be run")
}

// keyReleasedInBetween shows the key as TAKEN on the first Create and as GONE on
// the read.
//
// Its counterpart in production is two callers reaching the same abandoned
// record at once: one closes the record and releases the key (a compensated
// execution NULLs its key), while the other's Create has already taken the
// conflict and by the time its read comes around the key is no longer there.
type keyReleasedInBetween struct {
	workflow.Store

	mu      sync.Mutex
	creates int
	lookups int
}

func (s *keyReleasedInBetween) Create(ctx context.Context, exec *workflow.Execution) error {
	s.mu.Lock()
	s.creates++
	first := s.creates == 1
	s.mu.Unlock()

	if first {
		return errors.Conflict(workflow.CodeExecutionExists, "the key was taken at that moment")
	}

	return s.Store.Create(ctx, exec)
}

func (s *keyReleasedInBetween) FindByIdempotencyKey(_ context.Context, wfName, key string) (*workflow.Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lookups++

	return nil, errors.NotFound(workflow.CodeExecutionNotFound,
		"the %q workflow has no execution with the key %q", wfName, key)
}

// TestKeyReleasedBeforeTheReadOpensTheExecutionAgain closes the case of a race
// that resolves ITSELF being turned into a 500.
//
// If Create says "the key is taken" and the read then says "there is no such
// execution", the key was released BETWEEN the two calls. That is an ordinary
// interleaving: a compensated execution releases its key and every caller that
// closes an abandoned record does exactly that. This read error used to be
// wrapped into workflow_store_failed, so the customer got a 500 because of a
// race that resolves itself (measured: with four callers reaching the same
// abandoned record, one got precisely this error).
//
// The right answer is to try to OPEN again; the key is free now.
func TestKeyReleasedBeforeTheReadOpensTheExecutionAgain(t *testing.T) {
	rec := &recorder{}
	store := &keyReleasedInBetween{Store: workflow.NewMemoryStore()}
	eng := workflow.New(store, testLogger())
	wf := workflow.Workflow{Name: "idem", Steps: steps(step(rec, "a"))}

	out, err := eng.Run(t.Context(), wf, nil,
		workflow.WithIdempotencyKey("cart-released"), workflow.WithLease(time.Minute))

	require.NoError(t, err, "a key that was released MUST NOT block the execution")
	assert.JSONEq(t, `"a-out"`, rawOutput(t, out))
	assert.Equal(t, []string{"invoke:a"}, rec.snapshot(), "the step really has to run")
	assert.Equal(t, 2, store.creates, "the second turn has to open the execution")
	assert.Equal(t, 1, store.lookups)
}

// closeCounter counts the writes into a terminal state and FORWARDS the claim
// capability BY HAND.
//
// The second part is not an accident but Go's rule: an embedded INTERFACE
// carries only its own methods, so every type wrapping Store hides ClaimingStore
// silently. Writing it out deliberately keeps it here as an example for whoever
// writes the next wrapper.
type closeCounter struct {
	workflow.Store

	mu     sync.Mutex
	closes map[string]int
}

func (s *closeCounter) UpdateStatus(
	ctx context.Context, execID string, status workflow.Status, out json.RawMessage, failure string,
) error {
	if status == workflow.StatusFailed {
		s.mu.Lock()
		s.closes[execID]++
		s.mu.Unlock()
	}

	return s.Store.UpdateStatus(ctx, execID, status, out, failure)
}

func (s *closeCounter) ClaimAbandoned(ctx context.Context, execID string, seen time.Time) (bool, error) {
	claimer, ok := s.Store.(workflow.ClaimingStore)
	if !ok {
		return true, nil
	}

	return claimer.ClaimAbandoned(ctx, execID, seen)
}

func (s *closeCounter) count(execID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.closes[execID]
}

// TestOnlyOneCallerCLOSESTheAbandonedRecord verifies that recovery is EXCLUSIVE.
//
// An abandoned record belongs to nobody: every caller that comes back with the
// same key finds it, and without the claim ALL of them recover it. On a record
// where no work was done the cost is only a duplicated write, but the same path
// on a record where work WAS done runs the compensation chain several times, at
// once (measured with four concurrent callers: the chain ran four times). The
// gate is the same for both, which is why what is counted here is the number of
// closes.
//
// The invariant under test: exactly one caller CLOSES the record and the saga
// runs only ONCE.
func TestOnlyOneCallerCLOSESTheAbandonedRecord(t *testing.T) {
	rec := &recorder{}
	store := &closeCounter{Store: workflow.NewMemoryStore(), closes: map[string]int{}}
	eng := workflow.New(store, testLogger())
	wf := workflow.Workflow{Name: "idem", Steps: steps(step(rec, "a"))}

	id := abandonedRecord(t, store, "cart-race", time.Hour)

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// What is under test is not the returned error but how many times
			// the record was closed.
			_, _ = eng.Run(t.Context(), wf, nil,
				workflow.WithIdempotencyKey("cart-race"), workflow.WithLease(time.Minute))
		}()
	}
	wg.Wait()

	assert.Equal(t, 1, store.count(id),
		"only the caller that WINS THE CLAIM may close the abandoned record")
	assert.Equal(t, []string{"invoke:a"}, rec.snapshot(),
		"only one saga may run for the same key")
}

// unreadableStore refuses to hand over the steps but FORWARDS the claim
// capability by hand.
type unreadableStore struct {
	workflow.Store
}

func (s *unreadableStore) Get(context.Context, string) (*workflow.Execution, error) {
	return nil, errors.Unavailable("store_down", "the steps could not be read")
}

func (s *unreadableStore) ClaimAbandoned(ctx context.Context, execID string, seen time.Time) (bool, error) {
	claimer, ok := s.Store.(workflow.ClaimingStore)
	if !ok {
		return true, nil
	}

	return claimer.ClaimAbandoned(ctx, execID, seen)
}

// TestAnUndecidableRecordIsNOTClaimed pins the ORDER of the claim.
//
// A claim is a WRITE: the winner stamps updated_at and pushes the lease out by
// another period. On a record whose steps cannot be read nothing can be decided
// and the engine does nothing — but had the claim come BEFORE the read, that
// caller which "does nothing" would silently have pushed the record's lease
// forward. The result is that a genuinely half-finished saga is HIDDEN from the
// next caller and from `gobit stuck` for a whole lease period.
//
// Hence the order: read first (no side effect), then claim (the first write).
func TestAnUndecidableRecordIsNOTClaimed(t *testing.T) {
	rec := &recorder{}
	base := workflow.NewMemoryStore()
	store := &unreadableStore{Store: base}
	eng := workflow.New(store, testLogger())
	wf := workflow.Workflow{Name: "idem", Steps: steps(step(rec, "a"))}

	id := abandonedRecord(t, base, "cart-unreadable", time.Hour)
	before, err := base.Get(t.Context(), id)
	require.NoError(t, err)

	_, err = eng.Run(t.Context(), wf, nil,
		workflow.WithIdempotencyKey("cart-unreadable"), workflow.WithLease(time.Minute))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "is still going")

	after, err := base.Get(t.Context(), id)
	require.NoError(t, err)
	assert.True(t, after.UpdatedAt.Equal(before.UpdatedAt),
		"an undecidable record MUST NOT BE CLAIMED: extending its lease hides a half-finished saga")
	assert.Empty(t, rec.snapshot())
}

// TestIdempotencyDifferentKeyRunsAgain verifies that a different key opens a new
// execution.
func TestIdempotencyDifferentKeyRunsAgain(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())
	wf := workflow.Workflow{Name: "idem", Steps: steps(step(rec, "a"))}

	_, err := eng.Run(t.Context(), wf, nil, workflow.WithIdempotencyKey("k1"))
	require.NoError(t, err)
	_, err = eng.Run(t.Context(), wf, nil, workflow.WithIdempotencyKey("k2"))
	require.NoError(t, err)

	assert.Equal(t, []string{"invoke:a", "invoke:a"}, rec.snapshot())
}

// TestIdempotencyWithoutKeyAlwaysRuns verifies that keyless calls do not clash.
func TestIdempotencyWithoutKeyAlwaysRuns(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())
	wf := workflow.Workflow{Name: "keyless", Steps: steps(step(rec, "a"))}

	for range 3 {
		_, err := eng.Run(t.Context(), wf, nil)
		require.NoError(t, err)
	}
	assert.Len(t, rec.snapshot(), 3)
}

// TestIdempotencyNonCompletedStates verifies what a repeat call returns for
// executions that did not complete.
func TestIdempotencyNonCompletedStates(t *testing.T) {
	tests := []struct {
		name     string
		status   workflow.Status
		wantCode string
	}{
		{"running", workflow.StatusRunning, workflow.CodeExecutionRunning},
		{"failed", workflow.StatusFailed, workflow.CodeExecutionFailed},
		{"compensation_failed", workflow.StatusCompensationFailed, workflow.CodeExecutionFailed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{}
			store := workflow.NewMemoryStore()
			eng := workflow.New(store, testLogger())

			prev := &workflow.Execution{
				ID:             "wfx_PREVIOUS",
				Workflow:       "state",
				IdempotencyKey: "k",
				Status:         tc.status,
				Failure:        "the earlier failure",
			}
			require.NoError(t, store.Create(t.Context(), prev))

			wf := workflow.Workflow{Name: "state", Steps: steps(step(rec, "a"))}
			_, err := eng.Run(t.Context(), wf, nil, workflow.WithIdempotencyKey("k"))

			require.Error(t, err)
			assert.Equal(t, errors.KindConflict, errors.KindOf(err))
			assert.Equal(t, tc.wantCode, errors.CodeOf(err))
			assert.Empty(t, rec.snapshot(), "no step may be run")
		})
	}
}

// --- context cancellation ---------------------------------------------------

// TestContextCancellationStillCompensates verifies that the compensation runs
// after a cancellation.
func TestContextCancellationStillCompensates(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var execID string
	var compCtxErr error
	var compHasDeadline bool

	a := step(rec, "a")
	a.onInvoke = captureExecutionID(&execID)
	a.onCompensate = func(cctx context.Context, _ *workflow.StepContext) error {
		compCtxErr = cctx.Err()
		_, compHasDeadline = cctx.Deadline()
		return nil
	}

	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		// The step succeeds but kills the context: the engine has to see the
		// cancellation before moving on to the NEXT step.
		cancel()
		return "b-out", nil
	}

	c := step(rec, "c")

	wf := workflow.Workflow{Name: "cancellation", Steps: steps(a, b, c)}
	_, err := eng.Run(ctx, wf, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, workflow.CodeCanceled, errors.CodeOf(err))

	assert.Equal(t, []string{
		"invoke:a", "invoke:b",
		"compensate:b", "compensate:a",
	}, rec.snapshot(), "in a canceled execution the compensation STILL has to run in reverse order")

	assert.NoError(t, compCtxErr, "the compensation has to run with a LIVE context (context.WithoutCancel)")
	assert.True(t, compHasDeadline, "the compensation context has to have its own time budget")

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	// Note: because MemoryStore ignores the context, the persistence claim here
	// is weak; the exercise against a Store that really uses the context is in
	// TestCancelledRunStillPersistsState.
	assert.Equal(t, workflow.StatusFailed, exec.Status)
	require.Len(t, exec.Steps, 2)
	assert.Equal(t, workflow.StepCompensated, exec.Steps[0].Status)
	assert.Equal(t, workflow.StepCompensated, exec.Steps[1].Status)
}

// ctxAwareStore imitates a Store that really uses the context.
//
// MemoryStore is an in-process map and ignores the context, so the property
// "writing under a canceled context" CANNOT BE EXERCISED with it (the test
// passes through a mutation because the cancellation breaks nothing). A real
// database driver returns an error with a dead context — this wrapper gives that
// behavior.
type ctxAwareStore struct {
	workflow.Store
}

func (s *ctxAwareStore) AppendStep(ctx context.Context, id string, rec workflow.StepRecord) error {
	if err := ctx.Err(); err != nil {
		return errors.Wrap(err, errors.KindUnavailable, "ctx_dead", "the context is dead")
	}
	return s.Store.AppendStep(ctx, id, rec)
}

func (s *ctxAwareStore) UpdateStatus(ctx context.Context, id string, st workflow.Status, out json.RawMessage, failure string) error {
	if err := ctx.Err(); err != nil {
		return errors.Wrap(err, errors.KindUnavailable, "ctx_dead", "the context is dead")
	}
	return s.Store.UpdateStatus(ctx, id, st, out, failure)
}

// TestCancelledRunStillPersistsState verifies that the trace of a canceled
// execution is STILL written.
//
// Had the Store writes been tied to the caller's context, the trace could not be
// written at exactly the moment it is needed most (a canceled, compensated
// execution).
func TestCancelledRunStillPersistsState(t *testing.T) {
	rec := &recorder{}
	store := &ctxAwareStore{Store: workflow.NewMemoryStore()}
	eng := workflow.New(store, testLogger())

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var execID string
	a := step(rec, "a")
	a.onInvoke = captureExecutionID(&execID)

	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		cancel()
		return "b-out", nil
	}

	_, err := eng.Run(ctx, workflow.Workflow{Name: "cancellation_trace", Steps: steps(a, b, step(rec, "c"))}, nil)
	require.Error(t, err)

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusFailed, exec.Status,
		"the terminal state has to be written even under a canceled context (context.WithoutCancel)")
	require.Len(t, exec.Steps, 2)
	assert.Equal(t, workflow.StepCompensated, exec.Steps[0].Status,
		"the compensation records must not be affected by the cancellation either")
	assert.Equal(t, workflow.StepCompensated, exec.Steps[1].Status)
}

// TestContextCancelledBeforeFirstStep verifies a cancellation with no step
// having run.
//
// Because the context is dead at the moment of the call the engine never opens
// the record; that the key is not burned is verified separately by
// TestCanceledContextDoesNotBurnIdempotencyKey.
func TestContextCancelledBeforeFirstStep(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	wf := workflow.Workflow{Name: "early_cancellation", Steps: steps(step(rec, "a"))}
	_, err := eng.Run(ctx, wf, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, rec.snapshot())
}

// --- panics -----------------------------------------------------------------

// TestStepPanicIsRecoveredAndCompensated verifies that a step panic does not
// bring the engine down.
func TestStepPanicIsRecoveredAndCompensated(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	var execID string
	a := step(rec, "a")
	a.onInvoke = captureExecutionID(&execID)

	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		panic("step b blew up")
	}

	c := step(rec, "c")

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "panic", Steps: steps(a, b, c)}, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, workflow.ErrPanic, "a panic has to be turned into a typed error")
	assert.Equal(t, errors.KindInternal, errors.KindOf(err))
	assert.Contains(t, err.Error(), "step b blew up", "the panic value has to be carried into the error text")

	assert.Equal(t, []string{"invoke:a", "invoke:b", "compensate:a"}, rec.snapshot())

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusFailed, exec.Status)
}

// TestPanicIsNotRetried verifies that a panic is not retried.
func TestPanicIsNotRetried(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	boom := step(rec, "boom")
	boom.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		panic(errors.New("the same panic every time"))
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "panic_no_retry", Steps: steps(boom)}, nil,
		workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 4, Backoff: time.Millisecond}))

	require.Error(t, err)
	assert.ErrorIs(t, err, workflow.ErrPanic)
	assert.Len(t, rec.snapshot(), 1, "a panic MUST NOT be retried")
}

// TestCompensatePanicIsRecovered verifies that a compensation panic keeps the
// chain going.
func TestCompensatePanicIsRecovered(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	var execID string
	a := step(rec, "a")
	a.onInvoke = captureExecutionID(&execID)

	b := step(rec, "b")
	b.onCompensate = func(context.Context, *workflow.StepContext) error {
		panic("the compensation blew up")
	}

	c := step(rec, "c")
	c.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Internal("c_failed", "c blew up")
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "compensation_panic", Steps: steps(a, b, c)}, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, workflow.ErrPanic)
	assert.Equal(t, []string{
		"invoke:a", "invoke:b", "invoke:c",
		"compensate:b", "compensate:a",
	}, rec.snapshot(), "a compensation panic must not stop the remaining compensations")

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusCompensationFailed, exec.Status)
}

// --- Shared data ------------------------------------------------------------

// TestSharedDataFlowsBetweenStepsAndCompensation verifies that Shared is
// reachable both between the steps and in the compensation.
func TestSharedDataFlowsBetweenStepsAndCompensation(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	var seenByB any
	var seenInCompensateA any
	var seenInCompensateB any
	var inputInStep any

	a := step(rec, "a")
	a.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		inputInStep = sc.Input
		sc.Shared["reservation"] = "res_1"
		return nil, nil
	}
	a.onCompensate = func(_ context.Context, sc *workflow.StepContext) error {
		seenInCompensateA = sc.Shared["reservation"]
		return nil
	}

	b := step(rec, "b")
	b.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		seenByB = sc.Shared["reservation"]
		sc.Shared["payment"] = "pay_1"
		return nil, nil
	}
	b.onCompensate = func(_ context.Context, sc *workflow.StepContext) error {
		seenInCompensateB = sc.Shared["payment"]
		return nil
	}

	c := step(rec, "c")
	c.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Internal("c", "c blew up")
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "sharing", Steps: steps(a, b, c)},
		map[string]any{"cart": "cart_1"})
	require.Error(t, err)

	assert.Equal(t, "res_1", seenByB, "the next step has to see what the earlier step wrote")
	assert.Equal(t, "res_1", seenInCompensateA, "a compensation has to see what its own Invoke wrote")
	assert.Equal(t, "pay_1", seenInCompensateB)
	assert.Equal(t, map[string]any{"cart": "cart_1"}, inputInStep, "the input has to reach the steps")
}

// --- validation -------------------------------------------------------------

// TestRunValidatesWorkflow verifies that invalid workflow definitions are
// rejected.
func TestRunValidatesWorkflow(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	tests := []struct {
		name string
		wf   workflow.Workflow
	}{
		{"unnamed", workflow.Workflow{Steps: steps(step(rec, "a"))}},
		{"no steps", workflow.Workflow{Name: "empty"}},
		{"nil step", workflow.Workflow{Name: "nil", Steps: []workflow.Step{nil}}},
		{"unnamed step", workflow.Workflow{Name: "unnamed_step", Steps: steps(step(rec, ""))}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := eng.Run(t.Context(), tc.wf, nil)
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
			assert.Equal(t, workflow.CodeInvalidWorkflow, errors.CodeOf(err))
		})
	}
	assert.Empty(t, rec.snapshot())
}

// TestRunValidatesOptions verifies that invalid options are rejected.
func TestRunValidatesOptions(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())
	wf := workflow.Workflow{Name: "option", Steps: steps(step(rec, "a"))}

	tests := []struct {
		name string
		opt  workflow.RunOption
	}{
		{"empty key", workflow.WithIdempotencyKey("")},
		{"zero attempts", workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 0})},
		{"negative backoff", workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 2, Backoff: -time.Second})},
		{"negative ceiling", workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 2, MaxBackoff: -time.Second})},
		{"negative multiplier", workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 2, Multiplier: -1})},
		{"invalid compensation policy", workflow.WithCompensationRetry(workflow.RetryPolicy{MaxAttempts: -1})},
		{"zero compensation timeout", workflow.WithCompensationTimeout(0)},
		{"negative store timeout", workflow.WithStoreTimeout(-time.Second)},
		{"nil option", nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := eng.Run(t.Context(), wf, nil, tc.opt)
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
			assert.Equal(t, workflow.CodeInvalidOption, errors.CodeOf(err))
		})
	}
	assert.Empty(t, rec.snapshot(), "with an invalid option no step may have run")
}

// TestRunRejectsUnserializableInput verifies that an input which cannot be
// turned into JSON NEVER starts the execution.
func TestRunRejectsUnserializableInput(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())
	wf := workflow.Workflow{Name: "unserializable", Steps: steps(step(rec, "a"))}

	_, err := eng.Run(t.Context(), wf, make(chan int))

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Empty(t, rec.snapshot(), "if the input cannot be persisted no step may have run")
}

// TestUnserializableStepOutputDoesNotFailRun verifies that a step whose output
// does not serialize does not drop the execution.
func TestUnserializableStepOutputDoesNotFailRun(t *testing.T) {
	rec := &recorder{}
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	var execID string
	a := step(rec, "a")
	a.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		execID = sc.ExecutionID
		return make(chan int), nil
	}

	out, err := eng.Run(t.Context(), workflow.Workflow{Name: "unserializable_output", Steps: steps(a)}, nil)
	require.NoError(t, err, "after the side effect was applied a serialization error must not drop the execution")
	assert.Empty(t, rawOutput(t, out), "an output that cannot be converted comes back as an empty RawMessage")

	exec, gerr := store.Get(t.Context(), execID)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusCompleted, exec.Status)
	require.Len(t, exec.Steps, 1)
	assert.Equal(t, workflow.StepInvoked, exec.Steps[0].Status)
	assert.Empty(t, exec.Steps[0].Output)
	assert.Contains(t, exec.Steps[0].Failure, "JSON")
}

// --- Store failures ---------------------------------------------------------

// brokenStore is the wrapping Store that returns an error on chosen calls.
type brokenStore struct {
	workflow.Store
	createErr error
	findErr   error
	appendErr error
	updateErr error
	getErr    error

	mu          sync.Mutex
	appendCalls int
	updateCalls int
}

func (s *brokenStore) Create(ctx context.Context, exec *workflow.Execution) error {
	if s.createErr != nil {
		return s.createErr
	}
	return s.Store.Create(ctx, exec)
}

func (s *brokenStore) Get(ctx context.Context, executionID string) (*workflow.Execution, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}

	return s.Store.Get(ctx, executionID)
}

func (s *brokenStore) FindByIdempotencyKey(ctx context.Context, wf, key string) (*workflow.Execution, error) {
	if s.findErr != nil {
		return nil, s.findErr
	}
	return s.Store.FindByIdempotencyKey(ctx, wf, key)
}

func (s *brokenStore) AppendStep(ctx context.Context, id string, rec workflow.StepRecord) error {
	s.mu.Lock()
	s.appendCalls++
	s.mu.Unlock()
	if s.appendErr != nil {
		return s.appendErr
	}
	return s.Store.AppendStep(ctx, id, rec)
}

func (s *brokenStore) UpdateStatus(ctx context.Context, id string, st workflow.Status, out json.RawMessage, failure string) error {
	s.mu.Lock()
	s.updateCalls++
	s.mu.Unlock()
	if s.updateErr != nil {
		return s.updateErr
	}
	return s.Store.UpdateStatus(ctx, id, st, out, failure)
}

// TestCreateFailureAbortsRun verifies that if the record cannot be opened no
// step runs.
func TestCreateFailureAbortsRun(t *testing.T) {
	rec := &recorder{}
	store := &brokenStore{Store: workflow.NewMemoryStore(), createErr: errors.Unavailable("db", "the database is down")}
	eng := workflow.New(store, testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "create_fails", Steps: steps(step(rec, "a"))}, nil)

	require.Error(t, err)
	assert.Equal(t, workflow.CodeStoreFailed, errors.CodeOf(err))
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err), "the class of the underlying error has to be preserved")
	assert.Empty(t, rec.snapshot(), "no step may run before the repeat protection is in place")
}

// TestFindFailureAbortsRun verifies that if the repeat protection cannot be read
// the steps do not run.
func TestFindFailureAbortsRun(t *testing.T) {
	rec := &recorder{}
	inner := workflow.NewMemoryStore()
	require.NoError(t, inner.Create(t.Context(), &workflow.Execution{
		ID: "wfx_EXISTING", Workflow: "find_fails", IdempotencyKey: "k", Status: workflow.StatusCompleted,
	}))

	store := &brokenStore{Store: inner, findErr: errors.Unavailable("db", "the database is down")}
	eng := workflow.New(store, testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "find_fails", Steps: steps(step(rec, "a"))}, nil,
		workflow.WithIdempotencyKey("k"))

	require.Error(t, err)
	assert.Equal(t, workflow.CodeStoreFailed, errors.CodeOf(err))
	assert.Empty(t, rec.snapshot(), "no step may run again before the earlier execution's outcome can be read")
}

// nilFindStore is a Store that violates the contract and returns (nil, nil).
type nilFindStore struct {
	workflow.Store
}

func (s *nilFindStore) Create(context.Context, *workflow.Execution) error {
	return errors.Conflict("exists", "it already exists")
}

func (s *nilFindStore) FindByIdempotencyKey(context.Context, string, string) (*workflow.Execution, error) {
	return nil, nil
}

// TestReplayHandlesNilExecution verifies that a Store violating the contract
// does not bring the engine down; because the Store is written in a separate
// package that defense is needed.
func TestReplayHandlesNilExecution(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(&nilFindStore{Store: workflow.NewMemoryStore()}, testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "nil_record", Steps: steps(step(rec, "a"))}, nil,
		workflow.WithIdempotencyKey("k"))

	require.Error(t, err)
	assert.Equal(t, workflow.CodeStoreFailed, errors.CodeOf(err))
	assert.Empty(t, rec.snapshot())
}

// TestStoreWriteFailuresDoNotAbortRun verifies that step/status write failures
// do not drop the execution.
func TestStoreWriteFailuresDoNotAbortRun(t *testing.T) {
	rec := &recorder{}
	store := &brokenStore{
		Store:     workflow.NewMemoryStore(),
		appendErr: errors.Unavailable("db", "it could not be written"),
		updateErr: errors.Unavailable("db", "it could not be written"),
	}
	eng := workflow.New(store, testLogger())

	out, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "write_fails", Steps: steps(step(rec, "a"), step(rec, "b"))}, nil)

	require.NoError(t, err, "a successful flow must not be dropped because the ledger could not be kept")
	assert.JSONEq(t, `"b-out"`, rawOutput(t, out))
	assert.Equal(t, []string{"invoke:a", "invoke:b"}, rec.snapshot())

	store.mu.Lock()
	defer store.mu.Unlock()
	assert.Equal(t, 2, store.appendCalls)
	assert.Equal(t, 1, store.updateCalls)
}

// TestStoreWriteFailureStillCompensates verifies that with a write failure the
// compensation still runs correctly from the in-memory state.
func TestStoreWriteFailureStillCompensates(t *testing.T) {
	rec := &recorder{}
	store := &brokenStore{
		Store:     workflow.NewMemoryStore(),
		appendErr: errors.Unavailable("db", "it could not be written"),
	}
	eng := workflow.New(store, testLogger())

	a := step(rec, "a")
	b := step(rec, "b")
	c := step(rec, "c")
	c.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Internal("c", "c blew up")
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "compensation_without_writes", Steps: steps(a, b, c)}, nil)
	require.Error(t, err)

	assert.Equal(t, []string{
		"invoke:a", "invoke:b", "invoke:c",
		"compensate:b", "compensate:a",
	}, rec.snapshot(), "the compensation has to rest on the engine's memory, not on the Store")
}

// --- configuration ----------------------------------------------------------

// TestNilStoreEngineRefusesToRun verifies that an engine built without a Store
// runs no step: falling back silently to an in-process store would pull the
// idempotency protection down to the process boundary.
func TestNilStoreEngineRefusesToRun(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(nil, testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "no_store", Steps: steps(step(rec, "a"))}, nil)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Empty(t, rec.snapshot(), "with no Store no step may be run")
}

// TestNewInMemoryRunsWithoutStore verifies that the in-process store can be used
// by an EXPLICIT choice and that a nil log does not bring the engine down.
func TestNewInMemoryRunsWithoutStore(t *testing.T) {
	rec := &recorder{}
	eng := workflow.NewInMemory(testLogger())

	out, err := eng.Run(t.Context(), workflow.Workflow{Name: "nil_dependency", Steps: steps(step(rec, "a"))}, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `"a-out"`, rawOutput(t, out))
}

// TestStepContextCarriesEngineFields verifies that the engine fills in the
// StepContext fields.
func TestStepContextCarriesEngineFields(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	var seen []workflow.StepContext
	capture := func(_ context.Context, sc *workflow.StepContext) (any, error) {
		seen = append(seen, *sc)
		return nil, nil
	}

	a, b := step(rec, "a"), step(rec, "b")
	a.onInvoke, b.onInvoke = capture, capture

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "context", Steps: steps(a, b)}, nil)
	require.NoError(t, err)

	require.Len(t, seen, 2)
	assert.True(t, strings.HasPrefix(seen[0].ExecutionID, "wfx_"), "the execution id has to carry the prefix")
	assert.Equal(t, seen[0].ExecutionID, seen[1].ExecutionID)
	assert.Equal(t, "context", seen[0].Workflow)
	assert.Equal(t, "a", seen[0].StepName)
	assert.Equal(t, 0, seen[0].StepIndex)
	assert.Equal(t, "b", seen[1].StepName)
	assert.Equal(t, 1, seen[1].StepIndex)
	assert.Equal(t, 1, seen[1].Attempt)
}

// TestStepFailureKeepsUnderlyingCode verifies that the step error's CODE is
// preserved through the engine's wrapping.
//
// # Why this claim is about money
//
// The transport layer writes a single machine-readable field into the error
// body: Code. When the engine overwrote it with its own constant EVERY step
// error flattened, for the client, into one value — "workflow_step_failed". The
// concrete cost was the B2B spending limit: a purchase over the limit gets a 409
// but the storefront COULD NOT TELL "your limit was not enough" from "a transient
// conflict, try again". A 409 is exactly the class a repeat does not solve; the
// data that makes the distinction (the underlying error's code) was being
// produced and just was not reaching the consumer.
//
// The class (Kind) was already inherited; the test pins that it is preserved too
// — a class drifting while the code is carried would break the transport layer,
// which reads both in one place.
func TestStepFailureKeepsUnderlyingCode(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	a := step(rec, "a")
	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Conflict("order_spending_limit_exceeded",
			"the customer's spending limit for the period was exceeded")
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "limit", Steps: steps(a, b)}, nil)
	require.Error(t, err)

	assert.Equal(t, "order_spending_limit_exceeded", errors.CodeOf(err),
		"the CODE of the refusal has to be carried outward; if the engine's own code overwrites it "+
			"the client cannot tell a limit overrun from a transient failure")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err),
		"the class has to come from the underlying error too: 409 is the class a repeat does not solve")
	assert.Contains(t, err.Error(), "the \"b\" step",
		"the engine's own sentence (which workflow, which step) MUST NOT BE LOST")

	// The compensation chain is unaffected by the code change: a has to have been
	// rolled back.
	assert.Equal(t, []string{"invoke:a", "invoke:b", "compensate:a"}, rec.snapshot())
}

// TestStepFailureFallsBackToEngineCode verifies that a step error WITH NO CODE
// takes the engine's own code.
//
// For an untyped stdlib error there is no code to carry, and a body with no code
// tells the client nothing; that is why the fallback code exists.
func TestStepFailureFallsBackToEngineCode(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	a := step(rec, "a")
	a.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.New("a failure with no code")
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "codeless", Steps: steps(a)}, nil)
	require.Error(t, err)

	assert.Equal(t, workflow.CodeStepFailed, errors.CodeOf(err),
		"a step error with no code has to take the engine's own code")
	assert.Equal(t, errors.KindInternal, errors.KindOf(err),
		"an unclassified error has to fall to the safe side, to internal")
}

// TestStepPanicKeepsPanicCode verifies that the panic path keeps its own code
// too.
//
// A panic is a PROGRAMMING error and an operator has to be able to tell it from
// a transient failure; had the code not been carried outward a panic would look
// like an ordinary step failure. The sentinel error (ErrPanic) stays in the
// chain as well.
func TestStepPanicKeepsPanicCode(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	a := step(rec, "a")
	a.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		panic("a step panic")
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "panic_code", Steps: steps(a)}, nil)
	require.Error(t, err)

	assert.Equal(t, workflow.CodeStepPanicked, errors.CodeOf(err))
	assert.ErrorIs(t, err, workflow.ErrPanic)
}

// TestCompensationFailureCodeUnchanged verifies that the code leaving the engine
// DOES NOT CHANGE when the compensation blows up.
//
// Carrying the step code affects every saga in the engine; this test draws that
// change's boundary. When compensation blows up what the client and the operator
// need to see is not why the step failed but that THE SYSTEM IS LEFT
// INCONSISTENT — so the outer code has to stay [workflow.CodeCompensationFailed]
// and the step code has to stay inside the chain.
func TestCompensationFailureCodeUnchanged(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	a := step(rec, "a")
	a.onCompensate = func(context.Context, *workflow.StepContext) error {
		return errors.Internal("a_comp", "the compensation of a blew up")
	}
	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Conflict("order_spending_limit_exceeded", "the limit was exceeded")
	}

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "compensation_code", Steps: steps(a, b)}, nil)
	require.Error(t, err)

	assert.Equal(t, workflow.CodeCompensationFailed, errors.CodeOf(err),
		"a state that needs a human must not be masked by the step code")
	assert.Equal(t, errors.KindInternal, errors.KindOf(err))
	assert.Contains(t, err.Error(), "order_spending_limit_exceeded",
		"the step code has to stay readable in the chain")
}
