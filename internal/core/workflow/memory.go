package workflow

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/bdrtr/gobit/core/errors"
)

// memoryStore is the in-process, non-durable implementation of Store.
type memoryStore struct {
	// mu guards every field. The write path (Create/AppendStep/UpdateStatus) is
	// as frequent as the read path, so a plain Mutex is enough.
	mu sync.Mutex
	// byID holds the executions by id.
	byID map[string]*Execution
	// byKey maps the (workflow, idempotency key) pair to an id. Executions with
	// an empty key do NOT go in here; uniqueness is enforced only when a key was
	// given.
	byKey map[idempotencyKey]string
}

// idempotencyKey is the composite key of the byKey map.
//
// A struct is used rather than concatenating the strings: concatenation could
// collapse two different pairs onto the same key when a workflow name or a key
// containing the separator arrived.
type idempotencyKey struct {
	workflow string
	key      string
}

var (
	_ Store         = (*memoryStore)(nil)
	_ ClaimingStore = (*memoryStore)(nil)
)

// NewMemoryStore produces an in-process, non-durable Store.
//
// It is for tests and development: if the process dies the whole execution
// history is lost and a half-done execution cannot be recovered. Production has
// to use a durable Store. It is safe for concurrent use and every Execution it
// hands out is a DEEP COPY: changing the value the caller holds cannot corrupt
// the Store's state.
//
// It IGNORES the context: in-process map access has no wait for a cancellation
// to cut short. The consequence for tests is this — tests exercising the
// engine's context behavior (writing under a canceled context, say) PASS
// misleadingly against this Store; such a check needs a fake Store that really
// uses the context.
func NewMemoryStore() Store {
	return &memoryStore{
		byID:  make(map[string]*Execution),
		byKey: make(map[idempotencyKey]string),
	}
}

// Create opens a new execution record.
//
// If the same (Workflow, IdempotencyKey) pair already exists it returns
// errors.Conflict; with an empty key uniqueness is not checked. Recording the
// same id a second time returns errors.Conflict as well.
func (s *memoryStore) Create(_ context.Context, exec *Execution) error {
	if exec == nil {
		return errors.Invalid(CodeInvalidWorkflow, "the execution cannot be nil")
	}
	if exec.ID == "" {
		return errors.Invalid(CodeInvalidWorkflow, "the execution id cannot be empty")
	}
	if exec.Workflow == "" {
		return errors.Invalid(CodeInvalidWorkflow, "the execution's workflow name cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.byID[exec.ID]; ok {
		return errors.Conflict(CodeExecutionExists, "an execution with the id %q already exists", exec.ID)
	}

	k := idempotencyKey{workflow: exec.Workflow, key: exec.IdempotencyKey}
	if exec.IdempotencyKey != "" {
		if existing, ok := s.byKey[k]; ok {
			return errors.Conflict(CodeExecutionExists,
				"the %q workflow already has an execution with the key %q: %s",
				exec.Workflow, exec.IdempotencyKey, existing)
		}
		s.byKey[k] = exec.ID
	}

	s.byID[exec.ID] = cloneExecution(exec)

	return nil
}

// ClaimAbandoned claims the execution for recovery if it is still running and
// its UpdatedAt is unchanged.
//
// The whole check-and-write happens under the same lock, which is what makes it
// atomic; in a durable Store the same thing is one conditional UPDATE.
func (s *memoryStore) ClaimAbandoned(_ context.Context, executionID string, seen time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	exec, ok := s.byID[executionID]
	if !ok {
		return false, errors.NotFound(CodeExecutionNotFound, "no execution with the id %q was found", executionID)
	}
	if exec.Status != StatusRunning || !exec.UpdatedAt.Equal(seen) {
		return false, nil
	}

	exec.UpdatedAt = time.Now().UTC()

	return true, nil
}

// FindByIdempotencyKey returns the execution the key belongs to, or
// errors.NotFound.
func (s *memoryStore) FindByIdempotencyKey(_ context.Context, workflow, key string) (*Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.byKey[idempotencyKey{workflow: workflow, key: key}]
	if !ok {
		return nil, errors.NotFound(CodeExecutionNotFound,
			"the %q workflow has no execution with the key %q", workflow, key)
	}

	exec, ok := s.byID[id]
	if !ok {
		return nil, errors.Internal(CodeStoreFailed,
			"the key %q points at the id %q but there is no such record", key, id)
	}

	return cloneExecution(exec), nil
}

// AppendStep inserts a step record or updates the one with the same Index.
func (s *memoryStore) AppendStep(_ context.Context, executionID string, rec StepRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	exec, ok := s.byID[executionID]
	if !ok {
		return errors.NotFound(CodeExecutionNotFound, "no execution with the id %q was found", executionID)
	}

	stored := cloneStep(rec)
	if i := slices.IndexFunc(exec.Steps, func(r StepRecord) bool { return r.Index == rec.Index }); i >= 0 {
		exec.Steps[i] = stored
	} else {
		exec.Steps = append(exec.Steps, stored)
	}

	exec.UpdatedAt = time.Now().UTC()

	return nil
}

// UpdateStatus writes the execution's final status.
func (s *memoryStore) UpdateStatus(_ context.Context, executionID string, status Status, output json.RawMessage, failure string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	exec, ok := s.byID[executionID]
	if !ok {
		return errors.NotFound(CodeExecutionNotFound, "no execution with the id %q was found", executionID)
	}

	exec.Status = status
	exec.Output = slices.Clone(output)
	exec.Failure = failure
	exec.UpdatedAt = time.Now().UTC()

	// If compensation completed in full the key is RELEASED; the reasoning is
	// in the [StatusFailed] godoc. The record stays and only its key drops —
	// the same behavior as pgstore setting that column to NULL.
	if status == StatusFailed && exec.IdempotencyKey != "" {
		delete(s.byKey, idempotencyKey{workflow: exec.Workflow, key: exec.IdempotencyKey})
		exec.IdempotencyKey = ""
	}

	return nil
}

// Get reads the execution together with its steps, or errors.NotFound.
func (s *memoryStore) Get(_ context.Context, executionID string) (*Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	exec, ok := s.byID[executionID]
	if !ok {
		return nil, errors.NotFound(CodeExecutionNotFound, "no execution with the id %q was found", executionID)
	}

	return cloneExecution(exec), nil
}

// cloneExecution produces a deep copy of the execution.
//
// The copy keeps the Store's state from being changed from outside (or the
// outside from being changed by the Store); a durable Store behaves that way
// too, and having the in-memory implementation imitate it keeps tests from
// passing misleadingly.
func cloneExecution(exec *Execution) *Execution {
	out := *exec
	out.Input = slices.Clone(exec.Input)
	out.Output = slices.Clone(exec.Output)

	if exec.Steps != nil {
		out.Steps = make([]StepRecord, len(exec.Steps))
		for i := range exec.Steps {
			out.Steps[i] = cloneStep(exec.Steps[i])
		}
	}

	return &out
}

// cloneStep produces a deep copy of the step record.
func cloneStep(rec StepRecord) StepRecord {
	rec.Output = slices.Clone(rec.Output)

	return rec
}
