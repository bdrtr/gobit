//go:build integration

// The tests in this file need a real PostgreSQL instance (and therefore
// Docker); they are separated behind the `integration` tag so that `make test`
// stays fast. To run them: make test-integration
//
// Most of the claims here can be proven ONLY against a real database: that the
// partial unique index lets exactly one of two concurrent Creates through, that
// ON CONFLICT updates the same step, that JSONB tells SQL NULL from JSON null
// and that the migration can be rolled back cannot be shown in a unit test.
//
// The file is INSIDE the pgstore package: the constraint names the error mapping
// rests on (idempotencyIndex, executionsPKConstraint) are unexported, and that
// they really match the schema can only be verified by reading the catalog from
// here.
package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/db"
	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

const postgresImage = "postgres:16-alpine"

var (
	// testPool is the pool every test shares.
	testPool *db.Pool
	// testDSN is the connection string of the same database; it is kept
	// separately because the migration path uses its own connection rather than
	// the pool.
	testDSN string
	// adminDSN is the administrative address used to open new databases.
	adminDSN string
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres brings up a single Postgres container, applies the schema and
// runs every test against it. It is a separate function because os.Exit skips
// the defers.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_test"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	defer func() {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			fmt.Fprintf(os.Stderr, "the postgres container could not be stopped: %v\n", termErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "the postgres container could not be started: %v\n", err)
		return 1
	}

	testDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection string could not be read: %v\n", err)
		return 1
	}
	adminDSN = testDSN

	if err = db.Migrate(ctx, testDSN, Migrations(), MigrationOwner); err != nil {
		fmt.Fprintf(os.Stderr, "the workflow schema could not be applied: %v\n", err)
		return 1
	}

	testPool, err = db.New(ctx, db.DefaultConfig(testDSN), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection pool could not be opened: %v\n", err)
		return 1
	}
	defer testPool.Close()

	return m.Run()
}

// newStore returns the store the test will use.
func newStore() workflow.Store {
	return New(testPool, nil)
}

// wfName produces a workflow name specific to the test; because the tests share
// the same tables they must not see each other's records.
func wfName(t *testing.T) string {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	if len(name) > maxNameLen {
		name = name[:maxNameLen]
	}
	return name
}

// openedExecution opens and returns an execution record for the test to work on.
func openedExecution(ctx context.Context, t *testing.T, store workflow.Store) *workflow.Execution {
	t.Helper()
	exec := &workflow.Execution{
		Workflow: wfName(t),
		Status:   workflow.StatusRunning,
		Input:    json.RawMessage(`{"cart_id":"cart_1"}`),
	}
	require.NoError(t, store.Create(ctx, exec))
	return exec
}

// TestMigrationUpDown verifies that the schema can be applied and ROLLED BACK
// (plan Section 8). It runs in its own database: dropping the other tests'
// schema would affect them.
func TestMigrationUpDown(t *testing.T) {
	ctx := context.Background()
	dsn := newDatabase(ctx, t)

	version, dirty, err := db.Version(ctx, dsn, MigrationOwner)
	require.NoError(t, err)
	assert.Zero(t, version, "on an empty database the version has to be 0")
	assert.False(t, dirty)

	require.NoError(t, db.Migrate(ctx, dsn, Migrations(), MigrationOwner))

	assert.True(t, relationExists(ctx, t, dsn, "workflow_executions"), "the execution table has to be created")
	assert.True(t, relationExists(ctx, t, dsn, "workflow_execution_steps"), "the step table has to be created")
	assert.True(t, relationExists(ctx, t, dsn, idempotencyIndex), "the partial unique index has to be created")

	version, dirty, err = db.Version(ctx, dsn, MigrationOwner)
	require.NoError(t, err)
	assert.Equal(t, uint(1), version)
	assert.False(t, dirty)

	require.NoError(t, db.MigrateDown(ctx, dsn, Migrations(), MigrationOwner, 0))

	assert.False(t, relationExists(ctx, t, dsn, "workflow_executions"), "the rollback has to drop the table")
	assert.False(t, relationExists(ctx, t, dsn, "workflow_execution_steps"), "the rollback has to drop the table")

	version, _, err = db.Version(ctx, dsn, MigrationOwner)
	require.NoError(t, err)
	assert.Zero(t, version, "after the rollback the version has to be 0")
}

// TestConstraintNamesMatchTheSchema verifies that the constraint names the error
// mapping rests on REALLY exist in the schema under those names.
//
// If a name did not hold, createError would fall silently to the generic branch
// and the engine could not recognize an idempotent repeat by CodeDuplicateKey;
// that is why the names are read from the catalog.
func TestConstraintNamesMatchTheSchema(t *testing.T) {
	ctx := context.Background()

	assert.True(t, relationExists(ctx, t, testDSN, idempotencyIndex),
		"an index named %s has to exist in the schema", idempotencyIndex)
	assert.True(t, relationExists(ctx, t, testDSN, executionsPKConstraint),
		"a primary key named %s has to exist in the schema", executionsPKConstraint)
}

// TestCreateAndGetEndToEnd verifies that the record is opened and read back
// unchanged.
func TestCreateAndGetEndToEnd(t *testing.T) {
	ctx := context.Background()
	store := newStore()

	exec := &workflow.Execution{
		Workflow:       wfName(t),
		IdempotencyKey: "ord_1",
		Status:         workflow.StatusRunning,
		Input:          json.RawMessage(`{"cart_id":"cart_1","quantity":2}`),
	}
	require.NoError(t, store.Create(ctx, exec))

	assert.True(t, strings.HasPrefix(exec.ID, "wfx_"), "the id has to be produced with the prefix: %s", exec.ID)
	assert.False(t, exec.CreatedAt.IsZero(), "CreatedAt has to be written back")
	assert.False(t, exec.UpdatedAt.IsZero(), "UpdatedAt has to be written back")
	assert.Equal(t, time.UTC, exec.CreatedAt.Location(), "the times have to be UTC")

	read, err := store.Get(ctx, exec.ID)
	require.NoError(t, err)

	assert.Equal(t, exec.ID, read.ID)
	assert.Equal(t, exec.Workflow, read.Workflow)
	assert.Equal(t, "ord_1", read.IdempotencyKey)
	assert.Equal(t, workflow.StatusRunning, read.Status)
	assert.JSONEq(t, `{"cart_id":"cart_1","quantity":2}`, string(read.Input))
	assert.Nil(t, read.Output, "an output that was not written has to stay NULL")
	assert.Empty(t, read.Failure)
	assert.Empty(t, read.Steps, "with no step written Steps has to be empty")
	assert.True(t, read.CreatedAt.Equal(exec.CreatedAt))
	assert.Equal(t, time.UTC, read.CreatedAt.Location())
}

// TestCreateKeepsTheGivenID verifies that the id the caller produced is
// preserved; the engine produces the id itself and hands it to Create.
func TestCreateKeepsTheGivenID(t *testing.T) {
	ctx := context.Background()
	store := newStore()

	exec := &workflow.Execution{
		ID:       "wfx_ENGINEPRODUCED001",
		Workflow: wfName(t),
		Status:   workflow.StatusRunning,
	}
	require.NoError(t, store.Create(ctx, exec))

	assert.Equal(t, "wfx_ENGINEPRODUCED001", exec.ID)

	read, err := store.Get(ctx, "wfx_ENGINEPRODUCED001")
	require.NoError(t, err)
	assert.Equal(t, "wfx_ENGINEPRODUCED001", read.ID)
}

// TestCreateWithTheSameIDClashes verifies that the same id cannot be opened a
// second time.
//
// The error is Invalid, NOT Conflict: the engine reads Conflict as "this request
// was made before" and goes down the replay path, while on an id clash there is
// no idempotency key to look for (see createError).
func TestCreateWithTheSameIDClashes(t *testing.T) {
	ctx := context.Background()
	store := newStore()

	first := &workflow.Execution{ID: "wfx_SAMEID00000001", Workflow: wfName(t)}
	require.NoError(t, store.Create(ctx, first))

	second := &workflow.Execution{ID: "wfx_SAMEID00000001", Workflow: wfName(t)}
	err := store.Create(ctx, second)

	require.Error(t, err)
	assert.Equal(t, CodeDuplicateID, coreerrors.CodeOf(err))
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err),
		"the same id is an input error: %v", err)
	assert.False(t, coreerrors.IsConflict(err),
		"Conflict is reserved for the idempotency clash alone: %v", err)
}

// TestCreateRefusesAWhitespaceKey verifies that an idempotency key made of
// whitespace only is not turned into "no key" SILENTLY.
//
// Were the key pulled to NULL the partial unique index would never engage and a
// second and third record would open with the same key: the repeat protection
// the caller asked for would vanish without a single warning.
func TestCreateRefusesAWhitespaceKey(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	name := wfName(t)

	for _, key := range []string{" ", "   ", "\t"} {
		err := store.Create(ctx, &workflow.Execution{
			Workflow: name, IdempotencyKey: key, Status: workflow.StatusRunning,
		})

		require.Errorf(t, err, "the key %q has to be refused", key)
		assert.True(t, coreerrors.IsInvalid(err), "the error has to be Invalid: %v", err)
	}

	var count int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM workflow_executions WHERE workflow = $1`, name).Scan(&count))
	assert.Zero(t, count, "no record may be OPENED with a refused key")

	// The read path refuses the same key too; the two paths accept the same set.
	_, err := store.FindByIdempotencyKey(ctx, name, " ")
	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "the lookup has to return Invalid as well: %v", err)
}

// TestCreateIdempotencyClash verifies that the same (workflow, key) pair cannot
// be opened a second time.
func TestCreateIdempotencyClash(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	name := wfName(t)

	require.NoError(t, store.Create(ctx, &workflow.Execution{
		Workflow: name, IdempotencyKey: "ord_1", Status: workflow.StatusRunning,
	}))

	err := store.Create(ctx, &workflow.Execution{
		Workflow: name, IdempotencyKey: "ord_1", Status: workflow.StatusRunning,
	})

	require.Error(t, err)
	assert.True(t, coreerrors.IsConflict(err), "an idempotency clash has to be Conflict: %v", err)
	assert.Equal(t, CodeDuplicateKey, coreerrors.CodeOf(err))
}

// TestCreateSameKeyInADifferentWorkflow verifies that uniqueness is PER
// workflow: the same key is free in another workflow.
func TestCreateSameKeyInADifferentWorkflow(t *testing.T) {
	ctx := context.Background()
	store := newStore()

	require.NoError(t, store.Create(ctx, &workflow.Execution{
		Workflow: wfName(t) + "_a", IdempotencyKey: "ord_1",
	}))
	require.NoError(t, store.Create(ctx, &workflow.Execution{
		Workflow: wfName(t) + "_b", IdempotencyKey: "ord_1",
	}))
}

// TestCreateKeylessExecutionsDoNotClash verifies that keyless executions do not
// clash with each other. If the index were not partial the second call would
// fail.
func TestCreateKeylessExecutionsDoNotClash(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	name := wfName(t)

	for i := range 3 {
		exec := &workflow.Execution{Workflow: name, Status: workflow.StatusRunning}
		require.NoErrorf(t, store.Create(ctx, exec), "keyless execution %d could not be opened", i)

		read, err := store.Get(ctx, exec.ID)
		require.NoError(t, err)
		assert.Empty(t, read.IdempotencyKey, "a keyless record has to come back with an empty key")
	}
}

// TestCreateConcurrentRace verifies that in a race where two (or more) processes
// open a record with the same key at once EXACTLY ONE succeeds.
//
// What the test proves is that "SELECT first, then INSERT" is not enough: the
// goroutines are released at once through a single gate and the decision is left
// to the unique index in the database.
func TestCreateConcurrentRace(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	name := wfName(t)

	const racers = 8
	gate := make(chan struct{})
	results := make([]error, racers)
	ids := make([]string, racers)

	var wg sync.WaitGroup
	wg.Add(racers)
	for i := range racers {
		go func() {
			defer wg.Done()
			exec := &workflow.Execution{
				Workflow:       name,
				IdempotencyKey: "ord_race",
				Status:         workflow.StatusRunning,
				Input:          json.RawMessage(fmt.Sprintf(`{"racer":%d}`, i)),
			}
			<-gate // let them all start at once
			results[i] = store.Create(ctx, exec)
			ids[i] = exec.ID
		}()
	}
	close(gate)
	wg.Wait()

	var succeeded, clashed int
	var winnerID string
	for i, err := range results {
		switch {
		case err == nil:
			succeeded++
			winnerID = ids[i]
		case coreerrors.IsConflict(err):
			clashed++
			assert.Equal(t, CodeDuplicateKey, coreerrors.CodeOf(err))
		default:
			t.Errorf("racer %d got an unexpected error: %v", i, err)
		}
	}

	assert.Equal(t, 1, succeeded, "the race has to produce exactly one winner")
	assert.Equal(t, racers-1, clashed, "everybody else has to get Conflict")

	// There has to be a single record in the database too: the count verifies
	// that the winner really is alone, independently of the error classes.
	var count int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM workflow_executions WHERE workflow = $1 AND idempotency_key = $2`,
		name, "ord_race").Scan(&count))
	assert.Equal(t, 1, count, "there has to be a single row for the same key")

	found, err := store.FindByIdempotencyKey(ctx, name, "ord_race")
	require.NoError(t, err)
	assert.Equal(t, winnerID, found.ID, "the record that persisted has to be the winner's")
}

// TestFindByIdempotencyKey verifies that reading by key returns the record along
// with its steps.
func TestFindByIdempotencyKey(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	name := wfName(t)

	exec := &workflow.Execution{
		Workflow: name, IdempotencyKey: "ord_find", Status: workflow.StatusRunning,
	}
	require.NoError(t, store.Create(ctx, exec))
	require.NoError(t, store.AppendStep(ctx, exec.ID, workflow.StepRecord{
		Name: "reserve_stock", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
	}))

	found, err := store.FindByIdempotencyKey(ctx, name, "ord_find")

	require.NoError(t, err)
	assert.Equal(t, exec.ID, found.ID)
	require.Len(t, found.Steps, 1, "the record found has to carry its steps too")
	assert.Equal(t, "reserve_stock", found.Steps[0].Name)
}

// TestFindByIdempotencyKeyNotFound verifies that a key that is not there returns
// NotFound.
func TestFindByIdempotencyKeyNotFound(t *testing.T) {
	ctx := context.Background()
	store := newStore()

	_, err := store.FindByIdempotencyKey(ctx, wfName(t), "never_written")

	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "a record that is not found has to be NotFound: %v", err)
	assert.Equal(t, CodeNotFound, coreerrors.CodeOf(err))
}

// TestGetNotFound verifies that an id that is not there returns NotFound.
func TestGetNotFound(t *testing.T) {
	ctx := context.Background()
	store := newStore()

	_, err := store.Get(ctx, "wfx_NEVERWRITTEN001")

	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "a record that is not found has to be NotFound: %v", err)
	assert.Equal(t, CodeNotFound, coreerrors.CodeOf(err))
}

// TestAppendStepUpdatesTheSameIndex verifies that a second write to the same
// Index DOES NOT OPEN A NEW ROW but updates the existing one. During a retry a
// step is written first as invoked and then as compensated; if two rows were
// left the execution's trace would be read wrong.
func TestAppendStepUpdatesTheSameIndex(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	exec := openedExecution(ctx, t, store)

	started := time.Now().UTC().Truncate(time.Microsecond)
	require.NoError(t, store.AppendStep(ctx, exec.ID, workflow.StepRecord{
		Name:      "reserve_stock",
		Index:     0,
		Status:    workflow.StepInvoked,
		Output:    json.RawMessage(`{"reservation":"res_1"}`),
		Attempts:  1,
		StartedAt: started,
		EndedAt:   started.Add(time.Second),
	}))

	ended := started.Add(2 * time.Second)
	require.NoError(t, store.AppendStep(ctx, exec.ID, workflow.StepRecord{
		Name:      "reserve_stock",
		Index:     0,
		Status:    workflow.StepCompensated,
		Output:    nil,
		Failure:   "not enough stock",
		Attempts:  3,
		StartedAt: started,
		EndedAt:   ended,
	}))

	read, err := store.Get(ctx, exec.ID)
	require.NoError(t, err)

	require.Len(t, read.Steps, 1, "the second write must not add a new row")
	step := read.Steps[0]
	assert.Equal(t, workflow.StepCompensated, step.Status, "the status has to be updated")
	assert.Equal(t, 3, step.Attempts, "the attempt count has to be updated")
	assert.Equal(t, "not enough stock", step.Failure)
	assert.Nil(t, step.Output, "a nil output has to be pulled to NULL")
	assert.True(t, step.EndedAt.Equal(ended), "the end time has to be updated")
	assert.Equal(t, time.UTC, step.EndedAt.Location())
}

// TestAppendStepPreservesTheTimes verifies that the step times are read back in
// UTC at microsecond precision and that zero times stay zero.
func TestAppendStepPreservesTheTimes(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	exec := openedExecution(ctx, t, store)

	zone := time.FixedZone("UTC+3", 3*60*60)
	started := time.Date(2026, 8, 23, 15, 4, 5, 123456000, zone)

	require.NoError(t, store.AppendStep(ctx, exec.ID, workflow.StepRecord{
		Name: "timed", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
		StartedAt: started,
	}))

	read, err := store.Get(ctx, exec.ID)
	require.NoError(t, err)
	require.Len(t, read.Steps, 1)

	step := read.Steps[0]
	assert.True(t, step.StartedAt.Equal(started), "the same instant has to be read back")
	assert.Equal(t, time.UTC, step.StartedAt.Location(), "the time has to be moved to UTC")
	assert.True(t, step.EndedAt.IsZero(), "a time that was not written has to stay zero")
}

// TestAppendStepReturnsInOrder verifies that the steps come back in Index order;
// the records are deliberately written in a SHUFFLED order.
func TestAppendStepReturnsInOrder(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	exec := openedExecution(ctx, t, store)

	for _, index := range []int{4, 0, 3, 1, 2} {
		require.NoError(t, store.AppendStep(ctx, exec.ID, workflow.StepRecord{
			Name:     fmt.Sprintf("step_%d", index),
			Index:    index,
			Status:   workflow.StepInvoked,
			Attempts: 1,
		}))
	}

	read, err := store.Get(ctx, exec.ID)
	require.NoError(t, err)

	require.Len(t, read.Steps, 5)
	for i, step := range read.Steps {
		assert.Equal(t, i, step.Index, "at position %d Index %d is expected", i, i)
		assert.Equal(t, fmt.Sprintf("step_%d", i), step.Name)
	}
}

// TestAppendStepRefreshesTheExecution verifies that writing a step advances the
// execution's UpdatedAt while leaving CreatedAt alone.
func TestAppendStepRefreshesTheExecution(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	exec := openedExecution(ctx, t, store)

	require.NoError(t, store.AppendStep(ctx, exec.ID, workflow.StepRecord{
		Name: "step", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
	}))

	read, err := store.Get(ctx, exec.ID)
	require.NoError(t, err)
	assert.True(t, read.UpdatedAt.After(exec.UpdatedAt),
		"writing a step has to advance UpdatedAt (before %s, after %s)", exec.UpdatedAt, read.UpdatedAt)
	assert.True(t, read.CreatedAt.Equal(exec.CreatedAt), "CreatedAt must not change")
}

// TestAppendStepForAMissingExecution verifies that writing an orphan step
// returns NotFound; the foreign key blocks it at the database level.
func TestAppendStepForAMissingExecution(t *testing.T) {
	ctx := context.Background()
	store := newStore()

	err := store.AppendStep(ctx, "wfx_MISSINGEXEC0001", workflow.StepRecord{
		Name: "step", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
	})

	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "a missing execution has to be NotFound: %v", err)

	var count int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM workflow_execution_steps WHERE execution_id = $1`,
		"wfx_MISSINGEXEC0001").Scan(&count))
	assert.Zero(t, count, "an orphan step must not be written")
}

// TestUpdateStatus verifies that the final status and the output are written.
func TestUpdateStatus(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	exec := openedExecution(ctx, t, store)

	require.NoError(t, store.UpdateStatus(ctx, exec.ID,
		workflow.StatusCompleted, json.RawMessage(`{"order_id":"ord_9"}`), ""))

	read, err := store.Get(ctx, exec.ID)
	require.NoError(t, err)
	assert.Equal(t, workflow.StatusCompleted, read.Status)
	assert.JSONEq(t, `{"order_id":"ord_9"}`, string(read.Output))
	assert.Empty(t, read.Failure)
	assert.True(t, read.UpdatedAt.After(exec.UpdatedAt), "UpdatedAt has to advance")
	assert.True(t, read.CreatedAt.Equal(exec.CreatedAt), "CreatedAt must not change")
}

// TestUpdateStatusCompensationFailure verifies that the state needing a human
// and its failure description are persisted.
func TestUpdateStatusCompensationFailure(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	exec := openedExecution(ctx, t, store)

	require.NoError(t, store.UpdateStatus(ctx, exec.ID,
		workflow.StatusCompensationFailed, nil, "the compensation blew up: the refund failed"))

	read, err := store.Get(ctx, exec.ID)
	require.NoError(t, err)
	assert.Equal(t, workflow.StatusCompensationFailed, read.Status)
	assert.Equal(t, "the compensation blew up: the refund failed", read.Failure)
	assert.Nil(t, read.Output, "a nil output has to stay NULL")
}

// TestUpdateStatusForAMissingExecution verifies that a record that is not there
// cannot be updated.
func TestUpdateStatusForAMissingExecution(t *testing.T) {
	ctx := context.Background()
	store := newStore()

	err := store.UpdateStatus(ctx, "wfx_MISSINGEXEC0002", workflow.StatusCompleted, nil, "")

	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "a missing execution has to be NotFound: %v", err)
	assert.Equal(t, CodeNotFound, coreerrors.CodeOf(err))
}

// TestSQLNullAndJSONNullAreToldApart verifies that "no value" (SQL NULL) and
// "the value is null" (JSON null) are told apart on the write and the read path
// alike.
func TestSQLNullAndJSONNullAreToldApart(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	name := wfName(t)

	nilExec := &workflow.Execution{Workflow: name, Input: nil}
	require.NoError(t, store.Create(ctx, nilExec))

	nullExec := &workflow.Execution{Workflow: name, Input: json.RawMessage(`null`)}
	require.NoError(t, store.Create(ctx, nullExec))

	emptyExec := &workflow.Execution{Workflow: name, Input: json.RawMessage{}}
	require.NoError(t, store.Create(ctx, emptyExec))

	readNil, err := store.Get(ctx, nilExec.ID)
	require.NoError(t, err)
	assert.Nil(t, readNil.Input, "a nil input has to come back as NULL")

	readNull, err := store.Get(ctx, nullExec.ID)
	require.NoError(t, err)
	assert.Equal(t, json.RawMessage(`null`), readNull.Input,
		"a JSON null value must not be turned into NULL")

	readEmpty, err := store.Get(ctx, emptyExec.ID)
	require.NoError(t, err)
	assert.Nil(t, readEmpty.Input, "an empty slice has to be written as NULL")

	// Verify that the column really is NULL (and not the JSON text "null") from
	// the query rather than the catalog.
	var nilIsNull, nullIsNull bool
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT input IS NULL FROM workflow_executions WHERE id = $1`, nilExec.ID).Scan(&nilIsNull))
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT input IS NULL FROM workflow_executions WHERE id = $1`, nullExec.ID).Scan(&nullIsNull))
	assert.True(t, nilIsNull, "a nil input has to be NULL in the column")
	assert.False(t, nullIsNull, "a JSON null MUST NOT be NULL in the column")
}

// TestStepJSONNullIsToldApart verifies that the same distinction holds for the
// step output.
func TestStepJSONNullIsToldApart(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	exec := openedExecution(ctx, t, store)

	require.NoError(t, store.AppendStep(ctx, exec.ID, workflow.StepRecord{
		Name: "nil_output", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
	}))
	require.NoError(t, store.AppendStep(ctx, exec.ID, workflow.StepRecord{
		Name: "null_output", Index: 1, Status: workflow.StepInvoked, Attempts: 1,
		Output: json.RawMessage(`null`),
	}))

	read, err := store.Get(ctx, exec.ID)
	require.NoError(t, err)
	require.Len(t, read.Steps, 2)
	assert.Nil(t, read.Steps[0].Output, "a nil step output has to be NULL")
	assert.Equal(t, json.RawMessage(`null`), read.Steps[1].Output,
		"a JSON null step output has to be preserved")
}

// TestFieldsAreStoredAsJSONB verifies that the fields are stored as JSONB rather
// than text: JSONB can be queried, text cannot.
func TestFieldsAreStoredAsJSONB(t *testing.T) {
	ctx := context.Background()
	store := newStore()

	exec := &workflow.Execution{
		Workflow: wfName(t),
		Input:    json.RawMessage(`{"cart_id":"cart_7","lines":[{"quantity":2}]}`),
	}
	require.NoError(t, store.Create(ctx, exec))

	var cartID string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT input->>'cart_id' FROM workflow_executions WHERE id = $1`, exec.ID).Scan(&cartID))
	assert.Equal(t, "cart_7", cartID, "the JSONB operators have to work")

	var columnType string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT pg_typeof(input)::text FROM workflow_executions WHERE id = $1`, exec.ID).Scan(&columnType))
	assert.Equal(t, "jsonb", columnType)
}

// TestInputJSONBRefusesIsInvalid verifies that an input JSONB cannot store is
// turned into a typed Invalid error and that the record is never opened.
//
// json.Valid counts this body as valid; PostgreSQL does not. Without the check
// the error would come back from the driver unclassified (KindInternal): an HTTP
// 500 produced by the caller's own data.
func TestInputJSONBRefusesIsInvalid(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	name := wfName(t)

	// nulEscape is backslash + u0000; it cannot be written into the source
	// directly.
	input := json.RawMessage(`{"not":"a` + nulEscape + `b"}`)
	require.True(t, json.Valid(input), "the body has to PASS json.Valid; the case rests on it")

	err := store.Create(ctx, &workflow.Execution{Workflow: name, Input: input})

	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "an error from the caller's data has to be Invalid: %v", err)
	assert.Equal(t, CodeInvalid, coreerrors.CodeOf(err))

	var count int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM workflow_executions WHERE workflow = $1`, name).Scan(&count))
	assert.Zero(t, count, "no record may be opened with a refused input")
}

// TestSQLSTATECodesMatchTheServer verifies the SQLSTATE codes wrapDB maps by
// asking the LIVE SERVER.
//
// The codes are written as constants; had one been written wrong the mapping
// would fall silently to the generic branch and an error born of the caller's
// data would come back as KindInternal. Here the same failure is produced on the
// real server and its classification is checked.
func TestSQLSTATECodesMatchTheServer(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name  string
		query string
		value any
		code  string
	}{
		{"an escape JSONB cannot convert", `SELECT $1::jsonb`, `{"x":"` + nulEscape + `"}`, untranslatableCharacter},
		{"a NUL byte in text", `SELECT $1::text`, "a\x00b", notInRepertoire},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := testPool.Pool().Exec(ctx, tc.query, tc.value)
			require.Error(t, err, "the server has to refuse this value")

			var pgErr *pgconn.PgError
			require.ErrorAs(t, err, &pgErr)
			assert.Equal(t, tc.code, pgErr.Code, "the SQLSTATE written as a constant has to match the server's")

			wrapped := wrapDB(err, CodeQueryFailed, "the execution could not be written")
			assert.True(t, coreerrors.IsInvalid(wrapped),
				"an error born of the caller's data has to be Invalid: %v", wrapped)

			var typed *coreerrors.Error
			require.ErrorAs(t, wrapped, &typed)
			assert.NotContains(t, typed.Message, pgErr.Message,
				"the driver message can carry the caller's data; it must not enter the message handed outward")
		})
	}
}

// TestBrokenFailureTextDoesNotBlockTheTerminalState verifies that unwritable
// bytes in the diagnostic text DO NOT LEAVE the execution hanging in "running".
//
// Had the text been refused, the terminal state could never be written, the
// record would stay running forever and that idempotency key could never be used
// again (every repeat would say "still going"). So the description is not
// refused, it is CLEANED.
func TestBrokenFailureTextDoesNotBlockTheTerminalState(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	exec := openedExecution(ctx, t, store)

	broken := "the stock\x00 service \xff did not answer"

	require.NoError(t, store.AppendStep(ctx, exec.ID, workflow.StepRecord{
		Name: "reserve_stock", Index: 0, Status: workflow.StepFailed,
		Failure: broken, Attempts: 1,
	}), "the step trace must not stay unwritable because of its diagnostic text")

	require.NoError(t, store.UpdateStatus(ctx, exec.ID, workflow.StatusFailed, nil, broken),
		"the terminal state must not stay unwritable because of its diagnostic text")

	read, err := store.Get(ctx, exec.ID)
	require.NoError(t, err)

	assert.Equal(t, workflow.StatusFailed, read.Status, "the record must not hang in running")
	assert.NotContains(t, read.Failure, "\x00", "the NUL byte has to be cleaned")
	assert.Contains(t, read.Failure, "stock", "the readable part has to be preserved")
	assert.Contains(t, read.Failure, "did not answer")
	require.Len(t, read.Steps, 1)
	assert.NotContains(t, read.Steps[0].Failure, "\x00")
	assert.Contains(t, read.Steps[0].Failure, "stock")
}

// TestGetReadsAMultiStepExecutionIntact verifies that scanning the execution
// columns on the FIRST row only does not corrupt the record that is read.
//
// The LEFT JOIN carries the execution row again for every step; if the scan
// boundary drifted (see skipExecColumns) either the execution fields would come
// back empty or steps would go missing. The input is deliberately large: it is
// the real weight that would be allocated over and over.
func TestGetReadsAMultiStepExecutionIntact(t *testing.T) {
	ctx := context.Background()
	store := newStore()

	largeInput := json.RawMessage(`{"filler":"` + strings.Repeat("g", 64*1024) + `","cart_id":"cart_9"}`)
	exec := &workflow.Execution{
		Workflow:       wfName(t),
		IdempotencyKey: "ord_many_steps",
		Status:         workflow.StatusRunning,
		Input:          largeInput,
	}
	require.NoError(t, store.Create(ctx, exec))

	const stepCount = 6
	for i := range stepCount {
		require.NoError(t, store.AppendStep(ctx, exec.ID, workflow.StepRecord{
			Name:     fmt.Sprintf("step_%d", i),
			Index:    i,
			Status:   workflow.StepInvoked,
			Output:   json.RawMessage(fmt.Sprintf(`{"position":%d}`, i)),
			Attempts: i + 1,
		}))
	}

	for name, read := range map[string]func() (*workflow.Execution, error){
		"Get": func() (*workflow.Execution, error) {
			return store.Get(ctx, exec.ID)
		},
		"FindByIdempotencyKey": func() (*workflow.Execution, error) {
			return store.FindByIdempotencyKey(ctx, exec.Workflow, "ord_many_steps")
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := read()
			require.NoError(t, err)

			assert.Equal(t, exec.ID, got.ID)
			assert.Equal(t, exec.Workflow, got.Workflow)
			assert.Equal(t, "ord_many_steps", got.IdempotencyKey)
			assert.Equal(t, workflow.StatusRunning, got.Status)
			assert.JSONEq(t, string(largeInput), string(got.Input),
				"the execution input has to come back in full regardless of the step count")
			assert.True(t, got.CreatedAt.Equal(exec.CreatedAt))

			require.Len(t, got.Steps, stepCount)
			for i, step := range got.Steps {
				assert.Equal(t, i, step.Index)
				assert.Equal(t, fmt.Sprintf("step_%d", i), step.Name)
				assert.JSONEq(t, fmt.Sprintf(`{"position":%d}`, i), string(step.Output),
					"every step has to come back with its own output")
				assert.Equal(t, i+1, step.Attempts)
			}
		})
	}
}

// TestValuesTravelAsParameters verifies that an SQL injection attempt stays
// DATA: values carrying quotes and semicolons are stored as they are and no
// statement runs.
func TestValuesTravelAsParameters(t *testing.T) {
	ctx := context.Background()
	store := newStore()

	hostile := `x'; DROP TABLE workflow_execution_steps; --`
	exec := &workflow.Execution{
		Workflow:       wfName(t),
		IdempotencyKey: hostile,
		Status:         workflow.StatusRunning,
		Failure:        hostile,
	}
	require.NoError(t, store.Create(ctx, exec))

	require.NoError(t, store.AppendStep(ctx, exec.ID, workflow.StepRecord{
		Name: hostile, Index: 0, Status: workflow.StepInvoked, Attempts: 1,
	}))

	read, err := store.FindByIdempotencyKey(ctx, exec.Workflow, hostile)
	require.NoError(t, err)
	assert.Equal(t, hostile, read.IdempotencyKey, "the value has to be stored as it is")
	assert.Equal(t, hostile, read.Failure)
	require.Len(t, read.Steps, 1)
	assert.Equal(t, hostile, read.Steps[0].Name)

	assert.True(t, relationExists(ctx, t, testDSN, "workflow_execution_steps"),
		"the table has to still be there: values must not be interpreted as statements")
}

// TestGetWithACanceledContext verifies that a canceled context is turned into a
// typed Unavailable error.
func TestGetWithACanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := newStore()
	cancel()

	_, err := store.Get(ctx, "wfx_ANYTHING0000001")

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindUnavailable, coreerrors.KindOf(err),
		"a cancellation has to be Unavailable: %v", err)
	assert.Equal(t, CodeCanceled, coreerrors.CodeOf(err))
}

// TestDeletingAnExecutionDropsItsSteps verifies that ON DELETE CASCADE works;
// when the execution record is cleaned up its steps must not be left orphaned.
func TestDeletingAnExecutionDropsItsSteps(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	exec := openedExecution(ctx, t, store)

	require.NoError(t, store.AppendStep(ctx, exec.ID, workflow.StepRecord{
		Name: "step", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
	}))

	_, err := testPool.Pool().Exec(ctx, `DELETE FROM workflow_executions WHERE id = $1`, exec.ID)
	require.NoError(t, err)

	var count int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM workflow_execution_steps WHERE execution_id = $1`, exec.ID).Scan(&count))
	assert.Zero(t, count, "deleting the execution has to drop its steps too")
}

// fakeStep is a simple workflow step used in the tests.
//
// When its compensation is called it writes its name into a shared slice; that
// makes the compensation ORDER measurable.
type fakeStep struct {
	name string
	// output is what Invoke returns when it succeeds.
	output any
	// failure, when set, is the error Invoke returns.
	failure       error
	compensations *[]string
	// executionID, when set, receives the id of the execution the step ran in.
	//
	// A compensated execution RELEASES its idempotency key (see
	// [workflow.StatusFailed]), so its record cannot be reached through the key.
	// Taking the id from the step is the only way to prove the record is still
	// there.
	executionID *string
	// ran, when set, is set to true when the step is invoked.
	ran *bool
}

func (a *fakeStep) Name() string { return a.name }

func (a *fakeStep) Invoke(_ context.Context, sc *workflow.StepContext) (any, error) {
	if a.executionID != nil {
		*a.executionID = sc.ExecutionID
	}
	if a.ran != nil {
		*a.ran = true
	}
	if a.failure != nil {
		return nil, a.failure
	}
	return a.output, nil
}

func (a *fakeStep) Compensate(_ context.Context, _ *workflow.StepContext) error {
	*a.compensations = append(*a.compensations, a.name)
	return nil
}

// recoverableStep is a step that can rebuild its state from ITS OWN persisted
// output.
//
// Its compensation READS the rebuilt shared state and records the value it saw:
// it is not enough for the compensation to run, it also has to be seen running
// with the RIGHT data. A compensation running with a value that did not come
// from the record's output claims to have undone work it never undid.
type recoverableStep struct {
	name string
	// output is the value the step writes into Shared and returns.
	output        any
	compensations *[]string
	// seen is the value Compensate found in the shared map.
	seen *string
	// restoreFailure, when set, is the error Restore fails with.
	restoreFailure error
}

func (a *recoverableStep) Name() string { return a.name }

func (a *recoverableStep) Invoke(_ context.Context, sc *workflow.StepContext) (any, error) {
	sc.Shared[a.name] = a.output

	return a.output, nil
}

func (a *recoverableStep) Compensate(_ context.Context, sc *workflow.StepContext) error {
	*a.compensations = append(*a.compensations, a.name)
	if a.seen != nil {
		value, _ := sc.Shared[a.name].(string)
		*a.seen = value
	}

	return nil
}

func (a *recoverableStep) Restore(sc *workflow.StepContext, output json.RawMessage) error {
	if a.restoreFailure != nil {
		return a.restoreFailure
	}

	var value string
	if err := json.Unmarshal(output, &value); err != nil {
		return err
	}
	sc.Shared[a.name] = value

	return nil
}

// blockingStep is a step that CANNOT BE TAKEN as not having run while it has no
// record (a payment capture, say).
type blockingStep struct {
	recoverableStep
}

func (a *blockingStep) BlocksRecovery() {}

// TestSuccessfulRunWithTheEnginePersists verifies that the real engine works
// against this store and that a successful run is persisted as completed
// (Phase 3 DoD).
//
// The engine and the store are SEPARATE packages and do not import each other;
// that the contract really fits can only be seen by running the two together.
func TestSuccessfulRunWithTheEnginePersists(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	engine := workflow.New(store, nil)

	var compensations []string
	wf := workflow.Workflow{
		Name: wfName(t),
		Steps: []workflow.Step{
			&fakeStep{name: "reserve_stock", output: map[string]any{"reservation": "res_1"}, compensations: &compensations},
			&fakeStep{name: "take_payment", output: map[string]any{"payment": "pay_1"}, compensations: &compensations},
			&fakeStep{name: "create_order", output: map[string]any{"order_id": "ord_9"}, compensations: &compensations},
		},
	}

	output, err := engine.Run(ctx, wf, map[string]any{"cart_id": "cart_1"},
		workflow.WithIdempotencyKey("ord_e2e"))

	require.NoError(t, err)
	raw, isJSON := output.(json.RawMessage)
	require.Truef(t, isJSON, "Run returns the output as a json.RawMessage, the type returned: %T", output)
	assert.JSONEq(t, `{"order_id":"ord_9"}`, string(raw))
	assert.Empty(t, compensations, "no compensation runs on a successful run")

	stored, err := store.FindByIdempotencyKey(ctx, wf.Name, "ord_e2e")
	require.NoError(t, err)

	assert.Equal(t, workflow.StatusCompleted, stored.Status, "a successful run has to be completed")
	assert.JSONEq(t, `{"cart_id":"cart_1"}`, string(stored.Input))
	assert.JSONEq(t, `{"order_id":"ord_9"}`, string(stored.Output))
	assert.Empty(t, stored.Failure)

	require.Len(t, stored.Steps, 3, "the trace of all three steps has to remain")
	for i, step := range stored.Steps {
		assert.Equal(t, i, step.Index, "the steps have to come back in Index order")
		assert.Equal(t, workflow.StepInvoked, step.Status)
		assert.GreaterOrEqual(t, step.Attempts, 1, "the attempt count has to be at least 1")
		assert.False(t, step.StartedAt.IsZero(), "the start time has to be written")
		assert.False(t, step.EndedAt.IsZero(), "the end time has to be written")
	}
	assert.Equal(t, "create_order", stored.Steps[2].Name)
}

// TestCompensationWithTheEnginePersists verifies that when a step blows up the
// compensation runs in REVERSE ORDER and that its trace is persisted correctly.
//
// AppendStep's update path is exercised here in its real use: steps 0 and 1 are
// written first as invoked and then as compensated; the record is still three
// rows, not six.
func TestCompensationWithTheEnginePersists(t *testing.T) {
	var executionID string
	ctx := context.Background()
	store := newStore()
	engine := workflow.New(store, nil)

	var compensations []string
	wf := workflow.Workflow{
		Name: wfName(t),
		Steps: []workflow.Step{
			&fakeStep{name: "reserve_stock", output: "res_1", compensations: &compensations},
			&fakeStep{name: "take_payment", output: "pay_1", compensations: &compensations},
			&fakeStep{name: "create_order", failure: coreerrors.Internal("blew_up", "the order could not be written"), compensations: &compensations, executionID: &executionID},
		},
	}

	_, err := engine.Run(ctx, wf, map[string]any{"cart_id": "cart_2"},
		workflow.WithIdempotencyKey("ord_e2e_compensation"))

	require.Error(t, err)
	assert.Equal(t, []string{"take_payment", "reserve_stock"}, compensations,
		"the compensation has to run in REVERSE order")

	// If the compensation completed the execution left no trace in the world and
	// released its key as well: the next call with the same key has to get a NEW
	// execution rather than a 409 (see [workflow.StatusFailed]).
	_, keyErr := store.FindByIdempotencyKey(ctx, wf.Name, "ord_e2e_compensation")
	require.Error(t, keyErr, "a compensated execution MUST NOT hold its key")
	assert.True(t, coreerrors.IsNotFound(keyErr))

	// The record is NOT DELETED; only its key drops. The id is reached through
	// the step.
	require.NotEmpty(t, executionID, "the step has to write the execution id")
	stored, err := store.Get(ctx, executionID)
	require.NoError(t, err, "a failed attempt HAS TO REMAIN as an audit record")

	assert.Equal(t, workflow.StatusFailed, stored.Status, "if the compensation completed the status has to be failed")
	assert.NotEmpty(t, stored.Failure, "the failure description has to be persisted")

	require.Len(t, stored.Steps, 3, "every step has to be a single row; an update does not open a new one")
	assert.Equal(t, workflow.StepCompensated, stored.Steps[0].Status)
	assert.Equal(t, workflow.StepCompensated, stored.Steps[1].Status)
	assert.Equal(t, workflow.StepFailed, stored.Steps[2].Status,
		"the step that blew up is not compensated, it stays failed")
	assert.Contains(t, stored.Steps[2].Failure, "the order could not be written")
}

// relationExists reports whether a relation (a table or an index) with the given
// name exists.
func relationExists(ctx context.Context, t *testing.T, dsn, name string) bool {
	t.Helper()

	conn, err := pgx.Connect(ctx, dsn)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()

	var exists bool
	require.NoError(t, conn.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM pg_class
			WHERE relname = $1 AND relnamespace = current_schema()::regnamespace
		)`, name).Scan(&exists))
	return exists
}

// newDatabase opens an empty database specific to the test and returns its
// address. The database is dropped when the test ends.
func newDatabase(ctx context.Context, t *testing.T) string {
	t.Helper()

	name := fmt.Sprintf("gobit_wf_%d", time.Now().UnixNano())

	conn, err := pgx.Connect(ctx, adminDSN)
	require.NoError(t, err)
	defer func() { _ = conn.Close(ctx) }()

	// A database name cannot be parameterized; name is in the fixed shape the
	// test produces (letters, underscores and digits only) and takes no data
	// from outside.
	_, err = conn.Exec(ctx, `CREATE DATABASE `+name)
	require.NoError(t, err)

	t.Cleanup(func() {
		cleanup := context.Background()
		c, cErr := pgx.Connect(cleanup, adminDSN)
		if cErr != nil {
			return
		}
		defer func() { _ = c.Close(cleanup) }()
		_, _ = c.Exec(cleanup, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`)
	})

	u, err := url.Parse(adminDSN)
	require.NoError(t, err)
	u.Path = "/" + name
	return u.String()
}

// buildAbandonedExecution builds an execution that did work but is stale, and
// returns its id.
//
// Winding time back is REQUIRED: AppendStep refreshes the execution's
// updated_at, so the state "has a step AND is stale" cannot be built through the
// store surface. Production reaches the same state by crashing.
func buildAbandonedExecution(
	ctx context.Context, t *testing.T, store workflow.Store, wf workflow.Workflow,
	key, id string, records []workflow.StepRecord,
) {
	t.Helper()

	exec := &workflow.Execution{ID: id, Workflow: wf.Name, IdempotencyKey: key, Status: workflow.StatusRunning}
	require.NoError(t, store.Create(ctx, exec))
	for _, record := range records {
		require.NoError(t, store.AppendStep(ctx, id, record))
	}

	_, err := testPool.Pool().Exec(ctx,
		`UPDATE workflow_executions SET updated_at = now() - interval '1 hour' WHERE id = $1`, id)
	require.NoError(t, err)
}

// countingStep keeps the compensation and call counts in a way that survives a
// CONCURRENT run.
//
// It is a separate type because recoverableStep writes its compensations into an
// unlocked slice: right in sequential tests, a data race in a concurrent one,
// and -race rightly fails it.
type countingStep struct {
	name        string
	output      string
	compensated atomic.Int64
	invoked     atomic.Int64
}

func (a *countingStep) Name() string { return a.name }

func (a *countingStep) Invoke(_ context.Context, sc *workflow.StepContext) (any, error) {
	a.invoked.Add(1)
	sc.Shared[a.name] = a.output

	return a.output, nil
}

func (a *countingStep) Compensate(context.Context, *workflow.StepContext) error {
	a.compensated.Add(1)
	time.Sleep(20 * time.Millisecond) // in the real world a compensation is a call

	return nil
}

func (a *countingStep) Restore(sc *workflow.StepContext, output json.RawMessage) error {
	var value string
	if err := json.Unmarshal(output, &value); err != nil {
		return err
	}
	sc.Shared[a.name] = value

	return nil
}

// TestRecoveryRunsONCEWithConcurrentCallers proves against a real database that
// recovery is EXCLUSIVE.
//
// An abandoned record belongs to nobody: every caller that comes back with the
// same key finds it. Without the claim all of them would run the compensation
// chain — measured with four concurrent callers, the chain had run FOUR times.
// Compensation being idempotent does not save it: the contract's "can be called
// twice" meant IN SEQUENCE, while here two copies interleave.
//
// What makes it exclusive is a single conditional UPDATE (claimAbandonedSQL) and
// that can only be exercised against real Postgres: the second process waits on
// the row lock, then re-evaluates the WHERE against the committed value and
// matches nothing.
func TestRecoveryRunsONCEWithConcurrentCallers(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	engine := workflow.New(store, nil)

	step := &countingStep{name: "reserve_stock", output: "res_1"}
	wf := workflow.Workflow{
		Name:  "TestRecoveryRunsONCEWithConcurrentCallers",
		Steps: []workflow.Step{step},
	}

	const key = "abandoned_concurrent"
	const id = "wfx_ABANDONED_RACE"
	buildAbandonedExecution(ctx, t, store, wf, key, id, []workflow.StepRecord{{
		Name: "reserve_stock", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
		Output: []byte(`"res_1"`),
	}})

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// What is under test is not the returned error but how many times
			// the compensation ran.
			_, _ = engine.Run(ctx, wf, nil,
				workflow.WithIdempotencyKey(key), workflow.WithLease(time.Minute))
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(1), step.compensated.Load(),
		"the compensation chain has to run ONLY ONCE for an abandoned record")

	stored, err := store.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, workflow.StatusFailed, stored.Status,
		"if the recovery completed the record is written into a terminal state")
	assert.Empty(t, stored.IdempotencyKey,
		"if the compensation is complete the key is released; the customer can pay for the cart again")
}

// TestRecoverOnDemandReleasesAStuckSaga proves the operator's hand against a
// real database.
//
// The engine's own recovery path is arrived at: it runs when a caller comes back
// with the same idempotency key. An abandoned cart has no such caller, so
// without this entry point the record stays running forever, `gobit stuck` keeps
// listing it and the stock it reserved is released by nobody.
//
// The claim is exercised here for real (claimAbandonedSQL against a live row),
// which the unit tests cannot do: they have to invent a stale timestamp, and a
// compare-and-set against an invented value could never win.
func TestRecoverOnDemandReleasesAStuckSaga(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	engine := workflow.New(store, nil)

	recoverer, ok := engine.(workflow.Recoverer)
	require.True(t, ok, "the engine has to offer the recovery capability")

	compensations := []string{}
	var seen string
	step := &recoverableStep{name: "reserve_stock", output: "res_1", compensations: &compensations, seen: &seen}
	wf := workflow.Workflow{Name: "TestRecoverOnDemandReleasesAStuckSaga", Steps: []workflow.Step{step}}

	const key = "stuck_nobody_returns"
	const id = "wfx_STUCK_ONDEMAND"
	buildAbandonedExecution(ctx, t, store, wf, key, id, []workflow.StepRecord{{
		Name: "reserve_stock", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
		Output: []byte(`"res_1"`),
	}})

	err := recoverer.Recover(ctx, wf, id, workflow.WithLease(time.Minute))
	require.NoError(t, err)

	assert.Equal(t, []string{"reserve_stock"}, compensations,
		"the compensation chain has to run on the operator's request")
	assert.Equal(t, "res_1", seen,
		"the compensation has to see the value rebuilt from the record's output")

	stored, err := store.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, workflow.StatusFailed, stored.Status)
	assert.Empty(t, stored.IdempotencyKey,
		"the key has to be released; the customer can pay for the same cart again")

	// A second request finds nothing to recover: the record is terminal now.
	again := recoverer.Recover(ctx, wf, id, workflow.WithLease(time.Minute))
	require.Error(t, again, "a terminal record must not be compensated a second time")
	assert.True(t, coreerrors.IsConflict(again), "error: %v", again)
	assert.Len(t, compensations, 1, "the chain must not run twice")
}

// TestRecoverOnDemandSTOPSAtABlockingStep verifies that the boundary the engine
// draws for itself holds for the operator too, and that it is not overridable.
//
// Whether a capture went through cannot be answered from the records — the
// engine writes a step's record after Invoke returns — and an operator cannot
// answer it either without asking the payment provider. The command's job is to
// say so, not to guess.
func TestRecoverOnDemandSTOPSAtABlockingStep(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	engine := workflow.New(store, nil)
	recoverer, ok := engine.(workflow.Recoverer)
	require.True(t, ok)

	compensations := []string{}
	reserve := &recoverableStep{name: "reserve_stock", output: "res_1", compensations: &compensations}
	capture := &blockingStep{recoverableStep: recoverableStep{
		name: "capture", output: "pay_1", compensations: &compensations}}
	wf := workflow.Workflow{
		Name:  "TestRecoverOnDemandSTOPSAtABlockingStep",
		Steps: []workflow.Step{reserve, capture},
	}

	const key = "stuck_blocking"
	const id = "wfx_STUCK_BLOCKING"
	// ONLY the first step has a record: the process MAY have died inside the
	// capture.
	buildAbandonedExecution(ctx, t, store, wf, key, id, []workflow.StepRecord{{
		Name: "reserve_stock", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
		Output: []byte(`"res_1"`),
	}})

	err := recoverer.Recover(ctx, wf, id, workflow.WithLease(time.Minute))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "A HUMAN IS NEEDED")
	assert.Empty(t, compensations, "the capture may have been in flight; no compensation may run")

	stored, gerr := store.Get(ctx, id)
	require.NoError(t, gerr)
	assert.Equal(t, workflow.StatusCompensationFailed, stored.Status)
	assert.Equal(t, key, stored.IdempotencyKey, "the key has to be held")
}

// TestAbandonedExecutionIsCompensatedFromTheRecord proves the recovery itself.
//
// If the process died after doing work the reserved stock stands in the world
// and the only thing that will release it is the compensation chain. The engine
// has the compensation functions; the only thing lost was the state shared
// between the steps, and that is rebuilt from the step's OWN persisted output
// (workflow.Recoverable).
//
// Two claims are exercised at once and the second is the real one: the
// compensation RAN, and it ran with the RIGHT data. A test that only said "it
// ran" would also pass a recovery that leaves Shared empty — that compensation
// would say "I released nothing" too.
func TestAbandonedExecutionIsCompensatedFromTheRecord(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	engine := workflow.New(store, nil)

	compensations := []string{}
	var seen string
	step := &recoverableStep{name: "reserve_stock", output: "res_1", compensations: &compensations, seen: &seen}
	wf := workflow.Workflow{Name: "TestAbandonedExecutionIsCompensatedFromTheRecord", Steps: []workflow.Step{step}}

	const key = "abandoned_recovered"
	const id = "wfx_ABANDONED_RECOVER"
	buildAbandonedExecution(ctx, t, store, wf, key, id, []workflow.StepRecord{{
		Name: "reserve_stock", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
		Output: []byte(`"res_1"`),
	}})

	_, err := engine.Run(ctx, wf, nil,
		workflow.WithIdempotencyKey(key), workflow.WithLease(time.Minute))
	require.NoError(t, err, "a recoverable execution MUST NOT block a retry")

	assert.Equal(t, []string{"reserve_stock"}, compensations,
		"the compensation of an abandoned execution has to be run from the records")
	assert.Equal(t, "res_1", seen,
		"the compensation has to see the value rebuilt from the record's output; a compensation "+
			"running with an empty Shared cannot find the reservation it is supposed to release")

	stored, err := store.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, workflow.StatusFailed, stored.Status,
		"if the compensation completed in full the status is failed and the key is released")
	assert.Empty(t, stored.IdempotencyKey,
		"the key has to be released; the customer has to be able to pay for the same cart again")
}

// TestRecoverySTOPSAtABlockingStepWithNoRecord draws the boundary of recovery,
// and that boundary exists because of the payment.
//
// The engine writes a step's record AFTER Invoke RETURNS, so a process dying in
// the middle of Invoke leaves no trace of that step. Recovery looks at the
// records, so it takes such a step as "never ran". For a capture that means the
// stock of a customer whose card was charged is released, their key is freed and
// they are charged A SECOND TIME. A step reports this with
// workflow.RecoveryBlocker and the decision goes back to a human.
func TestRecoverySTOPSAtABlockingStepWithNoRecord(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	engine := workflow.New(store, nil)

	compensations := []string{}
	reserve := &recoverableStep{name: "reserve_stock", output: "res_1", compensations: &compensations}
	capture := &blockingStep{recoverableStep: recoverableStep{
		name: "capture", output: "pay_1", compensations: &compensations}}
	wf := workflow.Workflow{
		Name:  "TestRecoverySTOPSAtABlockingStepWithNoRecord",
		Steps: []workflow.Step{reserve, capture},
	}

	const key = "abandoned_blocking"
	const id = "wfx_ABANDONED_BLOCK"
	// ONLY the first step has a record: the process MAY have died inside the
	// capture.
	buildAbandonedExecution(ctx, t, store, wf, key, id, []workflow.StepRecord{{
		Name: "reserve_stock", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
		Output: []byte(`"res_1"`),
	}})

	_, err := engine.Run(ctx, wf, nil,
		workflow.WithIdempotencyKey(key), workflow.WithLease(time.Minute))

	require.Error(t, err)
	assert.True(t, coreerrors.IsConflict(err), "error: %v", err)
	assert.Contains(t, err.Error(), "A HUMAN IS NEEDED")
	assert.Empty(t, compensations,
		"the capture may have been in flight; no compensation may be run")

	stored, err := store.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, workflow.StatusCompensationFailed, stored.Status)
	assert.Equal(t, key, stored.IdempotencyKey,
		"the key HAS TO BE HELD; releasing it would open the door to the customer paying "+
			"again and being charged a second time")
}

// TestNoRecoveryWhenTheDefinitionChanged guards against a workflow definition
// that changed between two deployments.
//
// The index is the record's identity, but the definition may have changed;
// without the name check, step 2's compensation would be called with the output
// of an entirely different step.
func TestNoRecoveryWhenTheDefinitionChanged(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	engine := workflow.New(store, nil)

	compensations := []string{}
	step := &recoverableStep{name: "NEW_NAME", output: "res_1", compensations: &compensations}
	wf := workflow.Workflow{Name: "TestNoRecoveryWhenTheDefinitionChanged", Steps: []workflow.Step{step}}

	const key = "abandoned_definition_changed"
	const id = "wfx_ABANDONED_DEF"
	buildAbandonedExecution(ctx, t, store, wf, key, id, []workflow.StepRecord{{
		Name: "OLD_NAME", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
		Output: []byte(`"res_1"`),
	}})

	_, err := engine.Run(ctx, wf, nil,
		workflow.WithIdempotencyKey(key), workflow.WithLease(time.Minute))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "A HUMAN IS NEEDED")
	assert.Empty(t, compensations, "with a definition whose name does not match, no compensation may be run")
}

// TestNoRecoveryWhenRestoreBlowsUp stops a compensation from running with
// incomplete state.
//
// If Restore cannot put the record's output back into Shared (the output is
// empty or its shape changed) the compensation cannot know what to undo. Running
// silently with empty state would produce a compensation that says "done" while
// releasing nothing.
func TestNoRecoveryWhenRestoreBlowsUp(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	engine := workflow.New(store, nil)

	compensations := []string{}
	step := &recoverableStep{
		name: "reserve_stock", output: "res_1", compensations: &compensations,
		restoreFailure: coreerrors.Internal("broken_output", "the output could not be decoded"),
	}
	wf := workflow.Workflow{Name: "TestNoRecoveryWhenRestoreBlowsUp", Steps: []workflow.Step{step}}

	const key = "abandoned_restore_fails"
	const id = "wfx_ABANDONED_RESTORE"
	buildAbandonedExecution(ctx, t, store, wf, key, id, []workflow.StepRecord{{
		Name: "reserve_stock", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
		Output: []byte(`"res_1"`),
	}})

	_, err := engine.Run(ctx, wf, nil,
		workflow.WithIdempotencyKey(key), workflow.WithLease(time.Minute))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "A HUMAN IS NEEDED")
	assert.Empty(t, compensations, "a chain whose state cannot be rebuilt must not be compensated")
}

// TestAbandonedExecutionThatDidWorkNeedsAHuman proves the DANGEROUS half of a
// crash against the real store.
//
// # Why here and not in the memory store
//
// The state to build is "has a step AND is stale", and a behavior BOTH stores
// share makes it unbuildable through the store surface: AppendStep REFRESHES the
// execution's updated_at. That is right — a saga making progress has to keep its
// lease alive — but it means the test has to wind time back, and that can only
// be done by updating the real row. Production reaches the same state by
// CRASHING: the process dies after writing the step and updated_at stays where
// it was.
//
// # The claim
//
// If the process was cut off after doing work the compensation NEVER ran: the
// reserved stock and the opened payment session are still out there. Retrying
// silently would reserve that stock A SECOND TIME. So the record is moved to
// compensation_failed, HOLDS its key, and the caller gets a conflict saying a
// human is needed.
func TestAbandonedExecutionThatDidWorkNeedsAHuman(t *testing.T) {
	ctx := context.Background()
	store := newStore()
	engine := workflow.New(store, nil)

	var ran bool
	wf := workflow.Workflow{
		Name: "TestAbandonedExecutionThatDidWorkNeedsAHuman",
		Steps: []workflow.Step{&fakeStep{name: "reserve_stock", output: "res_1", compensations: &[]string{},
			ran: &ran}},
	}

	const key = "abandoned_did_work"
	exec := &workflow.Execution{
		ID: "wfx_ABANDONED_PG", Workflow: wf.Name, IdempotencyKey: key,
		Status: workflow.StatusRunning,
	}
	require.NoError(t, store.Create(ctx, exec))
	require.NoError(t, store.AppendStep(ctx, exec.ID, workflow.StepRecord{
		Name: "reserve_stock", Index: 0, Status: workflow.StepInvoked, Attempts: 1,
		Output: []byte(`"res_1"`),
	}))

	// Wind time back: the process died after writing the step and an hour has
	// passed.
	_, err := testPool.Pool().Exec(ctx,
		`UPDATE workflow_executions SET updated_at = now() - interval '1 hour' WHERE id = $1`, exec.ID)
	require.NoError(t, err)

	_, err = engine.Run(ctx, wf, nil,
		workflow.WithIdempotencyKey(key), workflow.WithLease(time.Minute))

	require.Error(t, err)
	assert.True(t, coreerrors.IsConflict(err), "error: %v", err)
	assert.Contains(t, err.Error(), "A HUMAN IS NEEDED")
	assert.False(t, ran, "NO step may be run on top of half-finished work")

	stored, err := store.Get(ctx, exec.ID)
	require.NoError(t, err)
	assert.Equal(t, workflow.StatusCompensationFailed, stored.Status,
		"the status has to SAY what happened: work was done, the compensation did not run")
	assert.Equal(t, key, stored.IdempotencyKey,
		"the key HAS TO BE HELD; released, a new attempt would land on top of half-finished work")
}
