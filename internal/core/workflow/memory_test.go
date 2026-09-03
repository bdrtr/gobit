package workflow_test

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// newExecution produces a valid execution record for the tests.
func newExecution(id, wf, key string) *workflow.Execution {
	return &workflow.Execution{
		ID:             id,
		Workflow:       wf,
		IdempotencyKey: key,
		Status:         workflow.StatusRunning,
		Input:          json.RawMessage(`{"n":1}`),
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
}

func TestMemoryStoreCreateAndGet(t *testing.T) {
	s := workflow.NewMemoryStore()
	require.NoError(t, s.Create(t.Context(), newExecution("wfx_1", "wf", "k1")))

	got, err := s.Get(t.Context(), "wfx_1")
	require.NoError(t, err)
	assert.Equal(t, "wfx_1", got.ID)
	assert.Equal(t, "wf", got.Workflow)
	assert.Equal(t, workflow.StatusRunning, got.Status)
	assert.JSONEq(t, `{"n":1}`, string(got.Input))
}

func TestMemoryStoreGetNotFound(t *testing.T) {
	s := workflow.NewMemoryStore()
	_, err := s.Get(t.Context(), "missing")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Equal(t, workflow.CodeExecutionNotFound, errors.CodeOf(err))
}

func TestMemoryStoreCreateRejectsInvalid(t *testing.T) {
	s := workflow.NewMemoryStore()

	require.Error(t, s.Create(t.Context(), nil))
	assert.Equal(t, errors.KindInvalid, errors.KindOf(s.Create(t.Context(), &workflow.Execution{Workflow: "wf"})))
	assert.Equal(t, errors.KindInvalid, errors.KindOf(s.Create(t.Context(), &workflow.Execution{ID: "wfx_1"})))
}

func TestMemoryStoreDuplicateIdempotencyKeyConflicts(t *testing.T) {
	s := workflow.NewMemoryStore()
	require.NoError(t, s.Create(t.Context(), newExecution("wfx_1", "wf", "k1")))

	err := s.Create(t.Context(), newExecution("wfx_2", "wf", "k1"))
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))

	// The same key is free for a DIFFERENT workflow.
	require.NoError(t, s.Create(t.Context(), newExecution("wfx_3", "other_wf", "k1")))
}

func TestMemoryStoreDuplicateIDConflicts(t *testing.T) {
	s := workflow.NewMemoryStore()
	require.NoError(t, s.Create(t.Context(), newExecution("wfx_1", "wf", "")))

	err := s.Create(t.Context(), newExecution("wfx_1", "wf", ""))
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}

// TestMemoryStoreEmptyKeysDoNotConflict verifies that keyless executions do not
// clash with each other; uniqueness is enforced only when a key was given.
func TestMemoryStoreEmptyKeysDoNotConflict(t *testing.T) {
	s := workflow.NewMemoryStore()
	require.NoError(t, s.Create(t.Context(), newExecution("wfx_1", "wf", "")))
	require.NoError(t, s.Create(t.Context(), newExecution("wfx_2", "wf", "")))
	require.NoError(t, s.Create(t.Context(), newExecution("wfx_3", "wf", "")))

	_, err := s.FindByIdempotencyKey(t.Context(), "wf", "")
	require.Error(t, err, "an empty key must not match any execution")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

func TestMemoryStoreFindByIdempotencyKey(t *testing.T) {
	s := workflow.NewMemoryStore()
	require.NoError(t, s.Create(t.Context(), newExecution("wfx_1", "wf", "k1")))

	got, err := s.FindByIdempotencyKey(t.Context(), "wf", "k1")
	require.NoError(t, err)
	assert.Equal(t, "wfx_1", got.ID)

	_, err = s.FindByIdempotencyKey(t.Context(), "wf", "missing")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	_, err = s.FindByIdempotencyKey(t.Context(), "other", "k1")
	require.Error(t, err, "a key is unique TOGETHER WITH the workflow name")
}

func TestMemoryStoreAppendStepAddsThenUpdates(t *testing.T) {
	s := workflow.NewMemoryStore()
	require.NoError(t, s.Create(t.Context(), newExecution("wfx_1", "wf", "")))

	require.NoError(t, s.AppendStep(t.Context(), "wfx_1", workflow.StepRecord{
		Name: "a", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
	}))
	require.NoError(t, s.AppendStep(t.Context(), "wfx_1", workflow.StepRecord{
		Name: "b", Index: 1, Status: workflow.StepInvoked, Attempts: 1,
	}))

	got, err := s.Get(t.Context(), "wfx_1")
	require.NoError(t, err)
	require.Len(t, got.Steps, 2)

	// The same Index DOES NOT open a new row, it updates the existing one.
	require.NoError(t, s.AppendStep(t.Context(), "wfx_1", workflow.StepRecord{
		Name: "a", Index: 0, Status: workflow.StepCompensated, Attempts: 2,
	}))

	got, err = s.Get(t.Context(), "wfx_1")
	require.NoError(t, err)
	require.Len(t, got.Steps, 2, "the same Index must not have opened a second row")
	assert.Equal(t, workflow.StepCompensated, got.Steps[0].Status)
	assert.Equal(t, 2, got.Steps[0].Attempts)
	assert.Equal(t, workflow.StepInvoked, got.Steps[1].Status)
}

func TestMemoryStoreAppendStepUnknownExecution(t *testing.T) {
	s := workflow.NewMemoryStore()
	err := s.AppendStep(t.Context(), "missing", workflow.StepRecord{Name: "a"})
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

func TestMemoryStoreUpdateStatus(t *testing.T) {
	s := workflow.NewMemoryStore()
	require.NoError(t, s.Create(t.Context(), newExecution("wfx_1", "wf", "")))

	require.NoError(t, s.UpdateStatus(t.Context(), "wfx_1", workflow.StatusCompleted, json.RawMessage(`{"ok":true}`), ""))

	got, err := s.Get(t.Context(), "wfx_1")
	require.NoError(t, err)
	assert.Equal(t, workflow.StatusCompleted, got.Status)
	assert.JSONEq(t, `{"ok":true}`, string(got.Output))
	assert.True(t, got.UpdatedAt.After(got.CreatedAt) || got.UpdatedAt.Equal(got.CreatedAt))

	err = s.UpdateStatus(t.Context(), "missing", workflow.StatusFailed, nil, "failure")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestMemoryStoreReturnsDeepCopies verifies that a value handed out cannot
// corrupt the Store's state; a durable Store behaves the same way.
func TestMemoryStoreReturnsDeepCopies(t *testing.T) {
	s := workflow.NewMemoryStore()

	original := newExecution("wfx_1", "wf", "k1")
	require.NoError(t, s.Create(t.Context(), original))

	// Changing the value the caller holds must not affect the Store.
	original.Status = workflow.StatusCompleted
	original.Input[2] = 'X'

	require.NoError(t, s.AppendStep(t.Context(), "wfx_1", workflow.StepRecord{
		Name: "a", Index: 0, Status: workflow.StepInvoked, Output: json.RawMessage(`"ilk"`),
	}))

	got, err := s.Get(t.Context(), "wfx_1")
	require.NoError(t, err)
	assert.Equal(t, workflow.StatusRunning, got.Status, "the Store has to keep its own copy")
	assert.JSONEq(t, `{"n":1}`, string(got.Input))

	// Changing the value taken FROM the Store must not affect it either.
	got.Status = workflow.StatusFailed
	got.Steps[0].Status = workflow.StepFailed
	got.Steps[0].Output[1] = 'X'

	again, err := s.Get(t.Context(), "wfx_1")
	require.NoError(t, err)
	assert.Equal(t, workflow.StatusRunning, again.Status)
	assert.Equal(t, workflow.StepInvoked, again.Steps[0].Status)
	assert.JSONEq(t, `"ilk"`, string(again.Steps[0].Output))
}

// TestMemoryStoreClaimAbandonedHasOneWinner verifies that the claim is
// EXCLUSIVE.
//
// The claim is recovery's gate: of two processes reaching the same abandoned
// record at once, only one may run the compensation chain. The winner stamps
// updated_at, so the second claim can no longer rest on the instant it saw and
// LOSES.
func TestMemoryStoreClaimAbandonedHasOneWinner(t *testing.T) {
	s := workflow.NewMemoryStore()
	claimer, ok := s.(workflow.ClaimingStore)
	require.True(t, ok, "the in-process store MUST offer the claim capability")

	old := time.Now().UTC().Add(-time.Hour)
	exec := newExecution("wfx_claim", "idem", "k")
	exec.CreatedAt, exec.UpdatedAt = old, old
	require.NoError(t, s.Create(t.Context(), exec))

	won, err := claimer.ClaimAbandoned(t.Context(), "wfx_claim", old)
	require.NoError(t, err)
	assert.True(t, won, "the first claim has to win")

	again, err := claimer.ClaimAbandoned(t.Context(), "wfx_claim", old)
	require.NoError(t, err)
	assert.False(t, again, "a second claim resting on the same instant MUST LOSE")

	current, err := s.Get(t.Context(), "wfx_claim")
	require.NoError(t, err)
	assert.True(t, current.UpdatedAt.After(old),
		"a won claim renews the lease too; otherwise the record would look abandoned again while the recovery runs")
}

// TestMemoryStoreClaimAbandonedEdgeCases pins the cases the claim refuses.
func TestMemoryStoreClaimAbandonedEdgeCases(t *testing.T) {
	s := workflow.NewMemoryStore()
	claimer := s.(workflow.ClaimingStore) //nolint:errcheck // the capability is verified in the test above

	old := time.Now().UTC().Add(-time.Hour)
	exec := newExecution("wfx_edge", "idem", "k2")
	exec.CreatedAt, exec.UpdatedAt = old, old
	require.NoError(t, s.Create(t.Context(), exec))

	won, err := claimer.ClaimAbandoned(t.Context(), "wfx_edge", time.Now().UTC())
	require.NoError(t, err)
	assert.False(t, won, "a claim resting on another instant loses: the record changed since then")

	require.NoError(t, s.UpdateStatus(t.Context(), "wfx_edge", workflow.StatusCompleted, nil, ""))
	current, err := s.Get(t.Context(), "wfx_edge")
	require.NoError(t, err)

	won, err = claimer.ClaimAbandoned(t.Context(), "wfx_edge", current.UpdatedAt)
	require.NoError(t, err)
	assert.False(t, won, "an execution that is not running is not recovered")

	_, err = claimer.ClaimAbandoned(t.Context(), "wfx_missing", old)
	require.Error(t, err, "a claim on a record that is not there returns an ERROR; a silent \"you lost\" would hide the failure")
	assert.True(t, errors.IsNotFound(err))
}

// TestMemoryStoreConcurrentUse verifies that concurrent use is safe (it is
// meaningful with the race detector).
func TestMemoryStoreConcurrentUse(t *testing.T) {
	s := workflow.NewMemoryStore()

	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)

	for i := range n {
		go func() {
			defer wg.Done()

			id := "wfx_" + string(rune('A'+i%26)) + string(rune('a'+i/26))
			if err := s.Create(t.Context(), newExecution(id, "wf", id)); err != nil {
				return
			}
			_ = s.AppendStep(t.Context(), id, workflow.StepRecord{Name: "a", Index: 0, Status: workflow.StepInvoked})
			_ = s.UpdateStatus(t.Context(), id, workflow.StatusCompleted, json.RawMessage(`1`), "")
			_, _ = s.Get(t.Context(), id)
			_, _ = s.FindByIdempotencyKey(t.Context(), "wf", id)
		}()
	}
	wg.Wait()
}
