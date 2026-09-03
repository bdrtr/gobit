package pgstore_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
	"github.com/bdrtr/gobit/internal/core/workflow/pgstore"
)

// nulEscape is the escape sequence that writes a NUL character in JSON:
// backslash + u0000. It is written escaped in the source; unescaped, the
// compiler would turn it into a real NUL character and the case under test would
// be gone.
const nulEscape = "\\u0000"

// validExecution is an execution used in the tests that passes validation.
func validExecution() *workflow.Execution {
	return &workflow.Execution{
		Workflow: "complete_order",
		Status:   workflow.StatusRunning,
		Input:    json.RawMessage(`{"cart_id":"cart_1"}`),
	}
}

// validStep is a step record used in the tests that passes validation.
func validStep() workflow.StepRecord {
	return workflow.StepRecord{
		Name:     "reserve_stock",
		Index:    0,
		Status:   workflow.StepInvoked,
		Attempts: 1,
	}
}

// TestNewSatisfiesTheContract verifies that the returned value is a
// workflow.Store. Because the engine does not import this package, the fit can
// only be checked here.
func TestNewSatisfiesTheContract(t *testing.T) {
	t.Parallel()

	var returned any = pgstore.New(nil, nil)

	_, fits := returned.(workflow.Store)
	assert.True(t, fits, "the type New returns has to satisfy workflow.Store")
}

// TestStoreWithoutAPoolIsUnavailable verifies that with no pool built every
// method returns a typed Unavailable error; there is no panic and no nil pointer
// accident.
func TestStoreWithoutAPoolIsUnavailable(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := pgstore.New(nil, nil)

	tests := map[string]func() error{
		"Create": func() error {
			return store.Create(ctx, validExecution())
		},
		"FindByIdempotencyKey": func() error {
			_, err := store.FindByIdempotencyKey(ctx, "complete_order", "ord_1")
			return err
		},
		"AppendStep": func() error {
			return store.AppendStep(ctx, "wfx_1", validStep())
		},
		"UpdateStatus": func() error {
			return store.UpdateStatus(ctx, "wfx_1", workflow.StatusCompleted, nil, "")
		},
		"Get": func() error {
			_, err := store.Get(ctx, "wfx_1")
			return err
		},
	}

	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := call()

			require.Error(t, err)
			assert.Equal(t, errors.KindUnavailable, errors.KindOf(err),
				"with no pool Unavailable is expected: %v", err)
			assert.Equal(t, "workflow_store_unavailable", errors.CodeOf(err))
		})
	}
}

// TestInputValidation verifies that invalid input comes back as Invalid WITHOUT
// GOING to the database. Because the pool is nil a call that reached the query
// would return Unavailable; seeing Invalid is the proof that validation ran
// first.
func TestInputValidation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := pgstore.New(nil, nil)
	longName := strings.Repeat("a", 200)

	tests := map[string]func() error{
		"nil execution": func() error {
			return store.Create(ctx, nil)
		},
		"empty workflow name": func() error {
			exec := validExecution()
			exec.Workflow = "   "
			return store.Create(ctx, exec)
		},
		"overlong workflow name": func() error {
			exec := validExecution()
			exec.Workflow = longName
			return store.Create(ctx, exec)
		},
		"overlong id": func() error {
			exec := validExecution()
			exec.ID = strings.Repeat("x", 200)
			return store.Create(ctx, exec)
		},
		"overlong idempotency key": func() error {
			exec := validExecution()
			exec.IdempotencyKey = strings.Repeat("k", 300)
			return store.Create(ctx, exec)
		},
		"invalid input JSON": func() error {
			exec := validExecution()
			exec.Input = json.RawMessage(`{bozuk`)
			return store.Create(ctx, exec)
		},
		"invalid output JSON": func() error {
			exec := validExecution()
			exec.Output = json.RawMessage(`{bozuk`)
			return store.Create(ctx, exec)
		},
		"idempotency key made of whitespace only": func() error {
			exec := validExecution()
			exec.IdempotencyKey = "   "
			return store.Create(ctx, exec)
		},
		"input JSONB cannot convert": func() error {
			exec := validExecution()
			exec.Input = json.RawMessage(`{"not":"a` + nulEscape + `b"}`)
			return store.Create(ctx, exec)
		},
		"workflow name with a NUL byte": func() error {
			exec := validExecution()
			exec.Workflow = "complete\x00order"
			return store.Create(ctx, exec)
		},
		"step name carrying invalid UTF-8": func() error {
			rec := validStep()
			rec.Name = "stock\xff"
			return store.AppendStep(ctx, "wfx_1", rec)
		},
		"lookup with an empty key": func() error {
			_, err := store.FindByIdempotencyKey(ctx, "complete_order", "  ")
			return err
		},
		"lookup with an empty workflow name": func() error {
			_, err := store.FindByIdempotencyKey(ctx, "", "ord_1")
			return err
		},
		"read with an empty id": func() error {
			_, err := store.Get(ctx, "")
			return err
		},
		"step write with an empty id": func() error {
			return store.AppendStep(ctx, "", validStep())
		},
		"step with an empty name": func() error {
			rec := validStep()
			rec.Name = ""
			return store.AppendStep(ctx, "wfx_1", rec)
		},
		"step with an empty status": func() error {
			rec := validStep()
			rec.Status = ""
			return store.AppendStep(ctx, "wfx_1", rec)
		},
		"negative step index": func() error {
			rec := validStep()
			rec.Index = -1
			return store.AppendStep(ctx, "wfx_1", rec)
		},
		"negative attempt count": func() error {
			rec := validStep()
			rec.Attempts = -3
			return store.AppendStep(ctx, "wfx_1", rec)
		},
		"invalid step output": func() error {
			rec := validStep()
			rec.Output = json.RawMessage(`[1,`)
			return store.AppendStep(ctx, "wfx_1", rec)
		},
		"update with an empty status": func() error {
			return store.UpdateStatus(ctx, "wfx_1", "", nil, "")
		},
		"update with an empty id": func() error {
			return store.UpdateStatus(ctx, " ", workflow.StatusCompleted, nil, "")
		},
		"update with an invalid output": func() error {
			return store.UpdateStatus(ctx, "wfx_1", workflow.StatusCompleted, json.RawMessage(`}`), "")
		},
	}

	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := call()

			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err),
				"a validation failure has to be Invalid: %v", err)
			assert.Equal(t, "workflow_store_invalid", errors.CodeOf(err))
		})
	}
}

// TestValidInputPassesValidation verifies that the boundary values DO PASS
// validation: a zero index, zero attempts, an empty key, nil JSON and zero times
// are valid inputs; the error has to come from Unavailable (no pool).
func TestValidInputPassesValidation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := pgstore.New(nil, nil)

	tests := map[string]func() error{
		"execution with no key and no status": func() error {
			return store.Create(ctx, &workflow.Execution{Workflow: "complete_order"})
		},
		"nil JSON fields": func() error {
			return store.Create(ctx, &workflow.Execution{
				Workflow: "complete_order",
				Status:   workflow.StatusRunning,
				Input:    nil,
				Output:   nil,
			})
		},
		"a JSON null value": func() error {
			return store.Create(ctx, &workflow.Execution{
				Workflow: "complete_order",
				Input:    json.RawMessage(`null`),
			})
		},
		"step with zero times": func() error {
			rec := validStep()
			rec.Attempts = 0
			rec.StartedAt = time.Time{}
			rec.EndedAt = time.Time{}
			return store.AppendStep(ctx, "wfx_1", rec)
		},
		// A failure description is diagnostic text: unwritable bytes are NOT
		// REFUSED, they are cleaned. Refusing them would mean the execution's
		// terminal state could never be written and the record would stay
		// "running" forever.
		"failure description carrying broken bytes": func() error {
			exec := validExecution()
			exec.Failure = "the stock\x00 service \xff did not answer"
			return store.Create(ctx, exec)
		},
		"step description carrying broken bytes": func() error {
			rec := validStep()
			rec.Failure = "the stock\x00 service \xff did not answer"
			return store.AppendStep(ctx, "wfx_1", rec)
		},
		"terminal state description carrying broken bytes": func() error {
			return store.UpdateStatus(ctx, "wfx_1", workflow.StatusFailed, nil,
				"the stock\x00 service \xff did not answer")
		},
	}

	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := call()

			require.Error(t, err)
			assert.Equal(t, errors.KindUnavailable, errors.KindOf(err),
				"valid input has to pass validation, the error may only come from the pool: %v", err)
		})
	}
}

// TestMigrationsReturnTheSameRootPerCall verifies that Migrations returns the
// same files on every call; the core may call it more than once.
func TestMigrationsReturnTheSameRootPerCall(t *testing.T) {
	t.Parallel()

	first := pgstore.Migrations()
	second := pgstore.Migrations()

	require.NotNil(t, first)
	assert.Equal(t, first, second)
}
