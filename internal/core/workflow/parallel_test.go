package workflow_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// TestParallelBranchesRunConcurrently verifies that the branches really do run
// concurrently: every branch waits for the other to start, so they would
// deadlock if they ran in sequence.
func TestParallelBranchesRunConcurrently(t *testing.T) {
	rec := &recorder{}

	startedA := make(chan struct{})
	startedB := make(chan struct{})

	a := step(rec, "a")
	a.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		close(startedA)
		select {
		case <-startedB:
			return "a-out", nil
		case <-time.After(2 * time.Second):
			return nil, errors.Internal("timeout", "branch b did not start: the branches run in sequence")
		}
	}

	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		close(startedB)
		select {
		case <-startedA:
			return "b-out", nil
		case <-time.After(2 * time.Second):
			return nil, errors.Internal("timeout", "branch a did not start: the branches run in sequence")
		}
	}

	par := workflow.NewParallel("pair", a, b).WithLogger(testLogger())
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	out, err := eng.Run(t.Context(), workflow.Workflow{Name: "concurrent", Steps: []workflow.Step{par}}, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `["a-out","b-out"]`, rawOutput(t, out), "the output has to be in BRANCH ORDER")
}

// TestParallelMergesSharedData verifies that the branch copies are merged into
// the parent context.
func TestParallelMergesSharedData(t *testing.T) {
	rec := &recorder{}

	a := step(rec, "a")
	a.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		assert.Equal(t, "root", sc.Shared["earlier"], "a branch has to see the parent context's data")
		sc.Shared["a"] = 1
		return nil, nil
	}

	b := step(rec, "b")
	b.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		sc.Shared["b"] = 2
		return nil, nil
	}

	earlier := step(rec, "earlier")
	earlier.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		sc.Shared["earlier"] = "root"
		return nil, nil
	}

	var afterwards map[string]any
	last := step(rec, "last")
	last.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		afterwards = map[string]any{"a": sc.Shared["a"], "b": sc.Shared["b"]}
		return nil, nil
	}

	par := workflow.NewParallel("pair", a, b).WithLogger(testLogger())
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	_, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "sharing", Steps: []workflow.Step{earlier, par, last}}, nil)
	require.NoError(t, err)

	assert.Equal(t, map[string]any{"a": 1, "b": 2}, afterwards,
		"what the branches wrote has to be visible in the next step")
}

// TestParallelBranchFailureCompensatesSiblings verifies that when one branch
// blows up the siblings that succeeded are rolled back.
func TestParallelBranchFailureCompensatesSiblings(t *testing.T) {
	rec := &recorder{}

	boom := errors.New("branch b blew up")

	a := step(rec, "a")
	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Wrap(boom, errors.KindInternal, "b_branch", "branch b")
	}
	c := step(rec, "c")

	par := workflow.NewParallel("triple", a, b, c).WithLogger(testLogger())
	before := step(rec, "before")

	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	_, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "branch_fails", Steps: []workflow.Step{before, par}}, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, boom)

	calls := rec.snapshot()
	// The siblings that succeeded are rolled back; the branch that blew up is
	// NOT compensated.
	assert.Contains(t, calls, "compensate:a")
	assert.Contains(t, calls, "compensate:c")
	assert.NotContains(t, calls, "compensate:b", "the branch that blew up must not be compensated itself")
	// Because the composite step returned an error the engine does not
	// compensate it separately, but the step BEFORE it is compensated.
	assert.Contains(t, calls, "compensate:before")

	// The sibling compensations run CONCURRENTLY; there is no order between them
	// to reverse (the branches ran concurrently too). The saga rule that has to
	// hold is that the composite's compensation finishes BEFORE the compensation
	// of the step that came before it.
	assert.Less(t, slices.Index(calls, "compensate:a"), slices.Index(calls, "compensate:before"))
	assert.Less(t, slices.Index(calls, "compensate:c"), slices.Index(calls, "compensate:before"))
}

// TestParallelCompensatedByEngine verifies that when a later step blows up all
// of the composite's branches are compensated.
func TestParallelCompensatedByEngine(t *testing.T) {
	rec := &recorder{}

	a, b := step(rec, "a"), step(rec, "b")
	par := workflow.NewParallel("pair", a, b).WithLogger(testLogger())

	last := step(rec, "last")
	last.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Internal("last", "the last step blew up")
	}

	eng := workflow.New(workflow.NewMemoryStore(), testLogger())
	_, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "composite_compensation", Steps: []workflow.Step{par, last}}, nil)
	require.Error(t, err)

	calls := rec.snapshot()
	// The engine's reverse-order rule BETWEEN STEPS is not applied INSIDE the
	// composite: because the branches ran concurrently there is no order between
	// them to reverse and their compensations run concurrently too. What is
	// asserted is that EVERY branch was compensated.
	assert.Contains(t, calls, "compensate:a")
	assert.Contains(t, calls, "compensate:b")

	// The composite's compensation has to come after the steps BEFORE it; that
	// is the real saga rule and it is the one that has to hold.
	assert.NotContains(t, calls, "compensate:last", "the step that blew up is not compensated")
}

// TestParallelCompensationFailureContinues verifies that when one branch's
// compensation blows up the remaining ones are still attempted.
func TestParallelCompensationFailureContinues(t *testing.T) {
	rec := &recorder{}

	compBoom := errors.New("the compensation of branch b blew up")

	a := step(rec, "a")
	b := step(rec, "b")
	b.onCompensate = func(context.Context, *workflow.StepContext) error {
		return errors.Wrap(compBoom, errors.KindInternal, "b_compensation", "the compensation of b")
	}

	par := workflow.NewParallel("pair", a, b).WithLogger(testLogger())
	last := step(rec, "last")
	last.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Internal("last", "the last step blew up")
	}

	eng := workflow.New(workflow.NewMemoryStore(), testLogger())
	_, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "branch_compensation_fails", Steps: []workflow.Step{par, last}}, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, compBoom)
	assert.Contains(t, rec.snapshot(), "compensate:a", "one branch's compensation failure must not stop the rest")
}

// TestParallelBranchPanicIsRecovered verifies that a branch panic does not bring
// the engine down.
func TestParallelBranchPanicIsRecovered(t *testing.T) {
	rec := &recorder{}

	a := step(rec, "a")
	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		panic("the branch blew up")
	}

	par := workflow.NewParallel("pair", a, b).WithLogger(testLogger())
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "branch_panic", Steps: []workflow.Step{par}}, nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, workflow.ErrPanic)
	assert.Contains(t, rec.snapshot(), "compensate:a", "the sibling of the branch that panicked has to be rolled back")
}

// TestParallelNonRetryableBranchStopsRetry verifies that when one branch fails
// with a non-retryable class the composite is not attempted again.
func TestParallelNonRetryableBranchStopsRetry(t *testing.T) {
	rec := &recorder{}

	a := step(rec, "a")
	b := step(rec, "b")
	b.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Invalid("invalid", "the branch input is invalid")
	}

	par := workflow.NewParallel("pair", a, b).WithLogger(testLogger())
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "no_retry_branch", Steps: []workflow.Step{par}}, nil,
		workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 4, Backoff: time.Millisecond}))

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "the composite's class has to be picked from the branch that is not retried")

	calls := rec.snapshot()
	invokes := 0
	for _, c := range calls {
		if c == "invoke:b" {
			invokes++
		}
	}
	assert.Equal(t, 1, invokes, "a branch that is not retried has to make the composite not retried either")
}

// TestParallelRetriesWholeComposite verifies that on a transient failure the
// composite is retried AS A WHOLE.
func TestParallelRetriesWholeComposite(t *testing.T) {
	rec := &recorder{}

	a := step(rec, "a")
	b := step(rec, "b")
	b.onInvoke = func(_ context.Context, sc *workflow.StepContext) (any, error) {
		if sc.Attempt < 2 {
			return nil, errors.Unavailable("transient", "the branch is temporarily down")
		}
		return "b-out", nil
	}

	par := workflow.NewParallel("pair", a, b).WithLogger(testLogger())
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	out, err := eng.Run(t.Context(), workflow.Workflow{Name: "composite_retry", Steps: []workflow.Step{par}}, nil,
		workflow.WithRetry(workflow.RetryPolicy{MaxAttempts: 3, Backoff: time.Millisecond}))

	require.NoError(t, err)
	assert.JSONEq(t, `["a-out","b-out"]`, rawOutput(t, out))

	calls := rec.snapshot()
	assert.Equal(t, 2, countCalls(calls, "invoke:a"), "the composite is retried as a whole")
	assert.Equal(t, 1, countCalls(calls, "compensate:a"), "the branch that succeeded on the first attempt has to be rolled back")
}

// TestParallelValidation verifies that invalid composite definitions are
// rejected.
func TestParallelValidation(t *testing.T) {
	rec := &recorder{}
	eng := workflow.New(workflow.NewMemoryStore(), testLogger())

	tests := []struct {
		name string
		par  *workflow.ParallelStep
	}{
		{"no branches", workflow.NewParallel("empty")},
		{"nil branch", workflow.NewParallel("nil", nil)},
		{"unnamed branch", workflow.NewParallel("unnamed", step(rec, ""))},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := eng.Run(t.Context(),
				workflow.Workflow{Name: "invalid", Steps: []workflow.Step{tc.par}}, nil)
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}
}

// TestParallelNameIsUsedInRecords verifies that the composite appears as a
// SINGLE step in the record.
func TestParallelNameIsUsedInRecords(t *testing.T) {
	rec := &recorder{}

	var execID string
	a := step(rec, "a")
	a.onInvoke = captureExecutionID(&execID)
	b := step(rec, "b")

	par := workflow.NewParallel("pair", a, b).WithLogger(testLogger())
	store := workflow.NewMemoryStore()
	eng := workflow.New(store, testLogger())

	_, err := eng.Run(t.Context(), workflow.Workflow{Name: "single_record", Steps: []workflow.Step{par}}, nil)
	require.NoError(t, err)

	exec, err := store.Get(t.Context(), execID)
	require.NoError(t, err)
	require.Len(t, exec.Steps, 1, "the composite has to be a single row in the record")
	assert.Equal(t, "pair", exec.Steps[0].Name)
	assert.Equal(t, workflow.StepInvoked, exec.Steps[0].Status)
}

// countCalls counts how many times a call was recorded.
func countCalls(calls []string, want string) int {
	n := 0
	for _, c := range calls {
		if c == want {
			n++
		}
	}
	return n
}

// TestParallelCompensationDoesNotStarveSiblings verifies that a slow branch does
// not leave its siblings with a dead context.
//
// Regression: branch compensations ran IN SEQUENCE and each was derived from the
// shared parent context. Once a slow branch that came earlier in the order
// consumed the engine's compensation budget, the branches after it were called
// with a context carrying context.DeadlineExceeded — that is, the compensation
// of the branches recorded EARLIEST (and typically holding the heaviest
// resource) failed silently. Compensations now run concurrently, so every branch
// gets its budget AT THE SAME TIME.
func TestParallelCompensationDoesNotStarveSiblings(t *testing.T) {
	rec := &recorder{}

	var (
		mu      sync.Mutex
		ctxErrs = map[string]error{}
	)
	record := func(name string) func(context.Context, *workflow.StepContext) error {
		return func(ctx context.Context, _ *workflow.StepContext) error {
			mu.Lock()
			ctxErrs[name] = ctx.Err()
			mu.Unlock()
			return nil
		}
	}

	early := step(rec, "early")
	early.onCompensate = record("early")

	slow := step(rec, "slow")
	slow.onCompensate = func(ctx context.Context, sc *workflow.StepContext) error {
		// Takes longer than the engine's compensation budget.
		select {
		case <-time.After(400 * time.Millisecond):
		case <-ctx.Done():
		}
		return record("slow")(ctx, sc)
	}

	par := workflow.NewParallel("pair", early, slow).WithLogger(testLogger())

	last := step(rec, "last")
	last.onInvoke = func(context.Context, *workflow.StepContext) (any, error) {
		return nil, errors.Internal("last", "the last step blew up")
	}

	eng := workflow.New(workflow.NewMemoryStore(), testLogger())
	_, err := eng.Run(t.Context(),
		workflow.Workflow{Name: "starvation", Steps: []workflow.Step{par, last}}, nil,
		workflow.WithCompensationTimeout(150*time.Millisecond))
	require.Error(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.Contains(t, ctxErrs, "early", "the early branch's compensation was never called")
	assert.NoError(t, ctxErrs["early"],
		"the early branch must not be called with a dead context because of its slow sibling")
}
