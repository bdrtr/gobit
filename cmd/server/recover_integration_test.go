//go:build integration

package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
	"github.com/bdrtr/gobit/internal/core/workflow/pgstore"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// recoverEnv points the binary at a database of its own and gives it the
// smallest configuration that boots.
//
// The command reads its configuration exactly the way the server does
// ([config.Load]), so the test has to speak to it the same way: through the
// environment. That is the point of the command's design — run inside the
// running container it is configured already — and a test that reached past it
// would be exercising a wiring nobody uses.
func recoverEnv(t *testing.T, dsn string) {
	t.Helper()

	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("JWT_SECRET", "recover-integration-test-secret-32-bytes-long")
	t.Setenv("LOG_LEVEL", "warn")
}

// stuckRecord writes a half-done execution straight into the workflow tables.
//
// A crash cannot be imitated any other way: crashing means the code that would
// write the terminal state NEVER runs. Winding updated_at back is what makes the
// record "abandoned" — the engine's own judgement, and the one the command
// depends on.
func stuckRecord(t *testing.T, dsn, id, wfName, key string, age time.Duration, input string) {
	t.Helper()

	ctx := context.Background()

	pool, err := db.New(ctx, db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	defer pool.Close()

	require.NoError(t, db.Migrate(ctx, dsn, pgstore.Migrations(), pgstore.MigrationOwner))

	store := pgstore.New(pool, nil)
	require.NoError(t, store.Create(ctx, &workflow.Execution{
		ID: id, Workflow: wfName, IdempotencyKey: key,
		Status: workflow.StatusRunning, Input: []byte(input),
	}))

	_, err = pool.Pool().Exec(ctx,
		`UPDATE workflow_executions SET updated_at = now() - $2::interval WHERE id = $1`,
		id, age.String())
	require.NoError(t, err)
}

// TestRecoverBootsTheApplicationAndReportsAMissingExecution is the wiring proof.
//
// The command has to bring up every module before it can compensate anything:
// the compensations are the checkout flow's own functions and they call the
// inventory, order and payment services. If that wiring broke, no unit test
// would see it — they all build the pieces by hand. Here the whole composition
// root runs and the only thing missing is the record.
func TestRecoverBootsTheApplicationAndReportsAMissingExecution(t *testing.T) {
	dsn := migrateDSN(t)
	recoverEnv(t, dsn)

	var out bytes.Buffer
	err := run([]string{recoverCommand, "wfx_NOTTHERE00001", "-" + flagConfirm, "wfx_NOTTHERE00001"}, &out)

	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "error: %v", err)
}

// TestRecoverRefusesAWorkflowItCannotBuild verifies the refusal by NAME.
//
// This binary can rebuild one workflow's definition from a record
// (complete_cart). A record belonging to anything else is refused rather than
// guessed at: a compensation chain built from the wrong definition undoes the
// wrong work.
func TestRecoverRefusesAWorkflowItCannotBuild(t *testing.T) {
	dsn := migrateDSN(t)
	recoverEnv(t, dsn)
	stuckRecord(t, dsn, "wfx_OTHERWORKFLOW", "some_other_flow", "k_other", time.Hour, `{"cart_id":"cart_1"}`)

	var out bytes.Buffer
	err := run([]string{recoverCommand, "wfx_OTHERWORKFLOW", "-" + flagConfirm, "wfx_OTHERWORKFLOW"}, &out)

	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "error: %v", err)
	assert.Contains(t, err.Error(), checkoutwf.WorkflowName)
}

// TestRecoverRefusesAnExecutionWhoseLeaseIsAlive verifies that the engine's own
// gate is reached THROUGH the command.
//
// A saga whose lease has not expired may be in flight in another process, and
// compensating it releases stock a paying customer is about to own. The
// confirmation flag does not override this: the operator can only decide WHICH
// execution, never whether the engine's refusals apply.
func TestRecoverRefusesAnExecutionWhoseLeaseIsAlive(t *testing.T) {
	dsn := migrateDSN(t)
	recoverEnv(t, dsn)
	stuckRecord(t, dsn, "wfx_STILLRUNNING1", checkoutwf.WorkflowName, "k_live", time.Second, `{"cart_id":"cart_1"}`)

	var out bytes.Buffer
	err := run([]string{recoverCommand, "wfx_STILLRUNNING1", "-" + flagConfirm, "wfx_STILLRUNNING1"}, &out)

	require.Error(t, err)
	assert.True(t, coreerrors.IsConflict(err), "error: %v", err)
}

// TestRecoverClosesAnAbandonedExecutionThatHeldNothing walks the whole path
// end to end, on the one ending that needs no half-done cart.
//
// The record's lease expired before any step wrote a record, so there is nothing
// to compensate: the command closes it and the idempotency key is released —
// which is exactly what lets the customer pay for that cart again.
func TestRecoverClosesAnAbandonedExecutionThatHeldNothing(t *testing.T) {
	ctx := context.Background()
	dsn := migrateDSN(t)
	recoverEnv(t, dsn)
	stuckRecord(t, dsn, "wfx_NOWORKDONE001", checkoutwf.WorkflowName, "k_nowork", time.Hour, `{"cart_id":"cart_1"}`)

	var out bytes.Buffer
	err := run([]string{recoverCommand, "wfx_NOWORKDONE001", "-" + flagConfirm, "wfx_NOWORKDONE001"}, &out)
	require.NoError(t, err)

	assert.Contains(t, out.String(), "wfx_NOWORKDONE001")
	assert.Contains(t, out.String(), string(workflow.StatusFailed))

	pool, err := db.New(ctx, db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	defer pool.Close()

	stored, err := pgstore.New(pool, nil).Get(ctx, "wfx_NOWORKDONE001")
	require.NoError(t, err)
	assert.Equal(t, workflow.StatusFailed, stored.Status)
	assert.Empty(t, stored.IdempotencyKey,
		"closing releases the key; without that the customer could never pay for this cart again")
}
