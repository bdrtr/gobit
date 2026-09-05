//go:build integration

// The tests in this file need a real PostgreSQL instance (and therefore
// Docker); they are separated behind the `integration` tag so `make test` stays
// fast. To run them: make test-integration
package db_test

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
)

// alphaMigrations and betaMigrations are the migrations of the two fake
// modules used in the tests. Both write to the same database; the point is to
// show that separate version tables do not corrupt each other (plan Sections
// 2.1/2.3).
//
//go:embed testdata/alpha
var alphaMigrations embed.FS

//go:embed testdata/beta
var betaMigrations embed.FS

// brokenMigrations carries a deliberately failing migration, to check that an
// execution error surfaces as a typed error.
//
//go:embed testdata/broken
var brokenMigrations embed.FS

// rollbackMigrations are the rollback tests' own migrations; they are
// independent of the alpha/beta state.
//
//go:embed testdata/rollback
var rollbackMigrations embed.FS

const postgresImage = "postgres:16-alpine"

// testDSN is the connection address of the container TestMain brings up.
var testDSN string

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres brings up a single Postgres container and runs every test
// against it. It is a separate function because os.Exit skips deferred calls.
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
		fmt.Fprintf(os.Stderr, "the connection address could not be obtained: %v\n", err)
		return 1
	}

	return m.Run()
}

// TestMigrateIsolatesOwners checks end to end that two modules keep independent
// migration ledgers in the same database. The subtests run in order; each
// inherits the state the previous one left behind.
func TestMigrateIsolatesOwners(t *testing.T) {
	ctx := context.Background()
	pool := openPool(ctx, t)

	alphaSrc := migrationsFor(t, alphaMigrations, "alpha")
	betaSrc := migrationsFor(t, betaMigrations, "beta")

	t.Run("each module creates its own version table", func(t *testing.T) {
		require.NoError(t, db.Migrate(ctx, testDSN, alphaSrc, "alpha"))
		require.NoError(t, db.Migrate(ctx, testDSN, betaSrc, "beta"))

		assert.True(t, tableExists(ctx, t, pool, migrationsTable(t, "alpha")),
			"alpha_schema_migrations must be created")
		assert.True(t, tableExists(ctx, t, pool, migrationsTable(t, "beta")),
			"beta_schema_migrations must be created")
		assert.False(t, tableExists(ctx, t, pool, "schema_migrations"),
			"a shared schema_migrations table is NEVER created; if it was, the modules are not isolated")

		assert.True(t, tableExists(ctx, t, pool, "alpha_items"))
		assert.True(t, tableExists(ctx, t, pool, "beta_items"))
	})

	t.Run("the versions advance independently", func(t *testing.T) {
		alphaVersion, dirty, err := db.Version(ctx, testDSN, "alpha")
		require.NoError(t, err)
		assert.Equal(t, uint(2), alphaVersion, "alpha has two migrations")
		assert.False(t, dirty)

		betaVersion, dirty, err := db.Version(ctx, testDSN, "beta")
		require.NoError(t, err)
		assert.Equal(t, uint(1), betaVersion, "beta has a single migration")
		assert.False(t, dirty)
	})

	t.Run("running it again returns no error", func(t *testing.T) {
		// migrate.ErrNoChange must be swallowed: an idempotent startup flow
		// requires it.
		require.NoError(t, db.Migrate(ctx, testDSN, alphaSrc, "alpha"))
		require.NoError(t, db.Migrate(ctx, testDSN, betaSrc, "beta"))

		alphaVersion, _, err := db.Version(ctx, testDSN, "alpha")
		require.NoError(t, err)
		assert.Equal(t, uint(2), alphaVersion)
	})

	t.Run("a single-step rollback affects only its owner", func(t *testing.T) {
		require.NoError(t, db.MigrateDown(ctx, testDSN, alphaSrc, "alpha", 1))

		alphaVersion, dirty, err := db.Version(ctx, testDSN, "alpha")
		require.NoError(t, err)
		assert.Equal(t, uint(1), alphaVersion)
		assert.False(t, dirty)

		assert.False(t, columnExists(ctx, t, pool, "alpha_items", "label"),
			"the label column must be gone because the second migration was rolled back")
		assert.True(t, tableExists(ctx, t, pool, "alpha_items"),
			"the first migration must still be applied")

		betaVersion, _, err := db.Version(ctx, testDSN, "beta")
		require.NoError(t, err)
		assert.Equal(t, uint(1), betaVersion, "beta must not be affected by alpha's rollback")
		assert.True(t, tableExists(ctx, t, pool, "beta_items"))
	})

	t.Run("rolling everything back does not touch the other module's tables", func(t *testing.T) {
		require.NoError(t, db.MigrateDown(ctx, testDSN, alphaSrc, "alpha", 0))

		alphaVersion, dirty, err := db.Version(ctx, testDSN, "alpha")
		require.NoError(t, err)
		assert.Equal(t, uint(0), alphaVersion)
		assert.False(t, dirty)
		assert.False(t, tableExists(ctx, t, pool, "alpha_items"))

		assert.True(t, tableExists(ctx, t, pool, "beta_items"), "beta's data must survive")
		betaVersion, _, err := db.Version(ctx, testDSN, "beta")
		require.NoError(t, err)
		assert.Equal(t, uint(1), betaVersion)
	})

	t.Run("it returns no error when there is nothing left to roll back", func(t *testing.T) {
		require.NoError(t, db.MigrateDown(ctx, testDSN, alphaSrc, "alpha", 0))
	})

	t.Run("it can be applied again", func(t *testing.T) {
		require.NoError(t, db.Migrate(ctx, testDSN, alphaSrc, "alpha"))

		alphaVersion, _, err := db.Version(ctx, testDSN, "alpha")
		require.NoError(t, err)
		assert.Equal(t, uint(2), alphaVersion)
		assert.True(t, columnExists(ctx, t, pool, "alpha_items", "label"))
	})
}

// TestVersionOnUnmigratedOwner checks that a module with no migration ever
// applied reports version zero.
func TestVersionOnUnmigratedOwner(t *testing.T) {
	ctx := context.Background()

	version, dirty, err := db.Version(ctx, testDSN, "gamma")
	require.NoError(t, err)
	assert.Equal(t, uint(0), version)
	assert.False(t, dirty)
}

// recordingLogger returns a logger that writes into the buffer it also returns.
//
// A text handler is used on purpose: the assertions read the rendered line, so
// they check what an operator would actually see rather than the attributes a
// structured capture would expose.
func recordingLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}

	return slog.New(slog.NewTextHandler(buf, nil)), buf
}

// TestPoolCarriesLargeLimitsUnchanged guards the ceiling from ABOVE.
//
// [TestPoolLifecycle] pins the bottom of the range; a cap would live at the top
// and would be just as invisible: "if pool.MaxConns > 64 { pool.MaxConns = 64 }"
// anywhere between the configuration and pgx passes every test written with
// small numbers, while an operator who asked for 100 silently runs 64 and reads
// 100 in the log. The DBMaxConns godoc argues at length that there is NO upper
// bound — config cannot know the cluster's max_connections — so the absence of
// one is a decision and gets a test.
//
// The number is a ceiling, not an allocation: MinConns stays at 1, so this opens
// one connection and asserts on the configuration the pool is running with.
func TestPoolCarriesLargeLimitsUnchanged(t *testing.T) {
	ctx := context.Background()

	cfg := db.DefaultConfig(testDSN)
	cfg.MaxConns = 250
	cfg.MinConns = 1

	log, records := recordingLogger()

	pool, err := db.New(ctx, cfg, log)
	require.NoError(t, err)
	defer pool.Close()

	assert.Equal(t, int32(250), pool.Pool().Config().MaxConns,
		"a large ceiling must reach the pool unchanged; nothing may cap it silently")
	assert.Contains(t, records.String(), "max_conns=250",
		"the startup log must report the ceiling the pool is running with")
}

// TestPoolLifecycle checks that the pool opens with the limits it was GIVEN,
// passes the health check, reports those limits, and cannot be used after it is
// closed.
//
// The limit assertions were added when a mutation survived: deleting
// "pgCfg.MaxConns = cfg.MaxConns" from New left every test in this package
// green. That line is the only thing standing between the caller's number and
// pgx's own default of max(4, NumCPU), and losing it is invisible from the
// outside — the pool still opens, still answers, and the startup log still
// prints the number it was HANDED, so the log would go on reporting a ceiling
// that is not in force. Since DB_MAX_CONNS became an operator knob, that line is
// what the knob is made of.
//
// MaxConns and MinConns are both 1, and that number is chosen against the
// failure it catches rather than for tidiness. Four would have agreed with a
// silent floor — pgx's own default is max(4, NumCPU), so "pgCfg.MaxConns =
// max(cfg.MaxConns, 4)" is a mutation that passes a test written at 4 while
// running a pool four times the size the log claims. One is below every default
// in play, so nothing can agree with it by accident.
//
// A pool of one is also the configuration the
// [github.com/bdrtr/gobit/internal/core/config.Config.DBMinConns] godoc
// recommends to an installation connecting many instances to a shared cluster.
// Until this test it was a claim about a struct: the value was validated, and
// no process had ever opened a pool that small. Here one does, and it answers.
func TestPoolLifecycle(t *testing.T) {
	ctx := context.Background()

	cfg := db.DefaultConfig(testDSN)
	cfg.MaxConns = 1
	cfg.MinConns = 1

	log, records := recordingLogger()

	pool, err := db.New(ctx, cfg, log)
	require.NoError(t, err)

	live := pool.Pool().Config()
	assert.Equal(t, int32(1), live.MaxConns, "the pool is not running with the MaxConns it was given")
	assert.Equal(t, int32(1), live.MinConns, "the pool is not running with the MinConns it was given")

	// The operator's only window onto the ceiling is this line; it has to carry
	// the number that is actually in force, not the one that was requested.
	assert.Contains(t, records.String(), "max_conns=1",
		"the startup log must report the ceiling the pool is running with")
	assert.Contains(t, records.String(), "min_conns=1")

	require.NoError(t, pool.Ping(ctx))
	require.NotNil(t, pool.Pool())
	assert.NotContains(t, pool.Target(), "gobit:gobit@", "the target representation must contain no credentials")

	var one int
	require.NoError(t, pool.Pool().QueryRow(ctx, "SELECT 1").Scan(&one))
	assert.Equal(t, 1, one)

	pool.Close()

	// A call on a closed pool returns a typed error.
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	require.Error(t, pool.Ping(pingCtx))
}

// TestNewFailsOnUnreachableDatabase checks that an unreachable target produces
// an error of class KindUnavailable that leaks no password.
func TestNewFailsOnUnreachableDatabase(t *testing.T) {
	ctx := context.Background()

	const password = "a-very-secret-password"
	cfg := db.DefaultConfig("postgres://gobit:" + password + "@127.0.0.1:1/gobit?sslmode=disable")
	cfg.ConnectTimeout = 2 * time.Second

	pool, err := db.New(ctx, cfg, nil)

	require.Error(t, err)
	assert.Nil(t, pool)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable),
		"the error class must be KindUnavailable, got %v", errors.KindOf(err))
	assert.NotContains(t, err.Error(), password, "the error message cannot leak the password")
}

// TestMigrateReportsFailedMigration checks that a migration containing broken
// SQL produces a typed error and leaves the version ledger dirty. This is the
// package's most critical error path: swallowed in silence, a broken schema
// ships to production under a "migration succeeded" log line.
func TestMigrateReportsFailedMigration(t *testing.T) {
	ctx := context.Background()
	src := migrationsFor(t, brokenMigrations, "broken")

	err := db.Migrate(ctx, testDSN, src, "broken")

	require.Error(t, err, "a failed migration must return an error")
	assert.Equal(t, "db_migration_failed", errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindInternal),
		"the error class must be KindInternal, got %v", errors.KindOf(err))

	version, dirty, verErr := db.Version(ctx, testDSN, "broken")
	require.NoError(t, verErr)
	assert.Equal(t, uint(1), version)
	assert.True(t, dirty, "a half-finished migration must leave the dirty flag")
}

// TestMigrateDownWithNothingToRollBack checks that MigrateDown's godoc promise
// — "with no migrations left to roll back it returns no error" — holds on the
// steps > 0 path as well. golang-migrate reports those two cases with
// os.ErrNotExist and ErrShortLimit, NOT with ErrNoChange.
func TestMigrateDownWithNothingToRollBack(t *testing.T) {
	ctx := context.Background()
	src := migrationsFor(t, rollbackMigrations, "rollback")

	t.Run("a module with no migration ever applied", func(t *testing.T) {
		require.NoError(t, db.MigrateDown(ctx, testDSN, src, "rollbackfresh", 1))

		version, dirty, err := db.Version(ctx, testDSN, "rollbackfresh")
		require.NoError(t, err)
		assert.Equal(t, uint(0), version)
		assert.False(t, dirty)
	})

	t.Run("more steps than exist", func(t *testing.T) {
		require.NoError(t, db.Migrate(ctx, testDSN, src, "rollbacksteps"))
		require.NoError(t, db.MigrateDown(ctx, testDSN, src, "rollbacksteps", 5))

		version, dirty, err := db.Version(ctx, testDSN, "rollbacksteps")
		require.NoError(t, err)
		assert.Equal(t, uint(0), version, "every migration on hand must be rolled back")
		assert.False(t, dirty)
	})
}

// TestMigrateReportsCancellationMidRun checks that a context running out IN THE
// MIDDLE of a migration run is not reported as a success. Because golang-migrate
// returns nil on a graceful stop, this is a path that opens onto silent data
// corruption.
func TestMigrateReportsCancellationMidRun(t *testing.T) {
	pool := openPool(context.Background(), t)

	// The first migration takes longer than the context bound; the second one
	// is NEVER reached.
	src := fstest.MapFS{
		"000001_slow.up.sql":     &fstest.MapFile{Data: []byte("SELECT pg_sleep(5);")},
		"000001_slow.down.sql":   &fstest.MapFile{Data: []byte("SELECT 1;")},
		"000002_second.up.sql":   &fstest.MapFile{Data: []byte("CREATE TABLE slowcancel_items (id TEXT PRIMARY KEY);")},
		"000002_second.down.sql": &fstest.MapFile{Data: []byte("DROP TABLE IF EXISTS slowcancel_items;")},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	err := db.Migrate(ctx, testDSN, src, "slowcancel")
	elapsed := time.Since(start)

	require.Error(t, err, "a migration cut short can NEVER be reported as a success")
	assert.Equal(t, "db_migration_canceled", errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindUnavailable),
		"the error class must be KindUnavailable, got %v", errors.KindOf(err))
	assert.True(t, errors.Is(err, context.DeadlineExceeded))
	assert.Less(t, elapsed, 4*time.Second, "the call must return at the context bound")

	assert.False(t, tableExists(context.Background(), t, pool, "slowcancel_items"),
		"the migration after the cancellation must not be applied")
}

// TestCancellationActuallyStopsRemainingMigrations checks that a cancellation
// stops not only the WAIT but the WORK.
//
// Regression: had the cancellation path abandoned the work in a separate
// goroutine and returned at the ctx bound, the abandoned goroutine would carry
// on applying the remaining migrations. The caller would get a "cut short"
// error while the schema completed itself behind their back.
//
// The scenario is built to make that visible: every statement ON ITS OWN stays
// UNDER the context bound, so a statement-level timeout cannot stop this run.
// The check is made AFTER waiting long enough for an abandoned goroutine to
// finish the remaining two migrations.
func TestCancellationActuallyStopsRemainingMigrations(t *testing.T) {
	pool := openPool(context.Background(), t)

	src := fstest.MapFS{
		"000001_slow_one.up.sql":   &fstest.MapFile{Data: []byte("SELECT pg_sleep(0.7);")},
		"000001_slow_one.down.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
		"000002_slow_two.up.sql":   &fstest.MapFile{Data: []byte("SELECT pg_sleep(0.7);")},
		"000002_slow_two.down.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
		"000003_marker.up.sql":     &fstest.MapFile{Data: []byte("CREATE TABLE stopafter_items (id TEXT PRIMARY KEY);")},
		"000003_marker.down.sql":   &fstest.MapFile{Data: []byte("DROP TABLE IF EXISTS stopafter_items;")},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := db.Migrate(ctx, testDSN, src, "stopafter")
	require.Error(t, err, "a migration cut short can NEVER be reported as a success")
	assert.Equal(t, "db_migration_canceled", errors.CodeOf(err))

	// An abandoned goroutine would comfortably finish the remaining two
	// migrations within this time.
	time.Sleep(3 * time.Second)

	assert.False(t, tableExists(context.Background(), t, pool, "stopafter_items"),
		"a canceled migration run must not advance AFTER RETURNING either")

	// The version ledger must not show the later migrations either.
	version, _, verErr := db.Version(context.Background(), testDSN, "stopafter")
	require.NoError(t, verErr)
	assert.Less(t, version, uint(3), "the 3rd migration must not be recorded after the cancellation")
}

// migrationsFor carves the module's migration directory out of the embedded
// file system.
func migrationsFor(t *testing.T, embedded embed.FS, owner string) fs.FS {
	t.Helper()

	sub, err := fs.Sub(embedded, "testdata/"+owner)
	require.NoError(t, err)
	return sub
}

// migrationsTable produces the table name together with the error check.
func migrationsTable(t *testing.T, owner string) string {
	t.Helper()

	table, err := db.MigrationsTable(owner)
	require.NoError(t, err)
	return table
}

// openPool opens a verification pool that stays open for the test's duration.
func openPool(ctx context.Context, t *testing.T) *db.Pool {
	t.Helper()

	pool, err := db.New(ctx, db.DefaultConfig(testDSN), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// tableExists reports whether the given table exists in the public schema.
func tableExists(ctx context.Context, t *testing.T, pool *db.Pool, table string) bool {
	t.Helper()

	var exists bool
	err := pool.Pool().QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = $1
		)`, table).Scan(&exists)
	require.NoError(t, err)
	return exists
}

// columnExists reports whether a column exists in the given table.
func columnExists(ctx context.Context, t *testing.T, pool *db.Pool, table, column string) bool {
	t.Helper()

	var exists bool
	err := pool.Pool().QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
		)`, table, column).Scan(&exists)
	require.NoError(t, err)
	return exists
}
