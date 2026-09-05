//go:build integration

// These tests need a real PostgreSQL (and therefore Docker).
//
// Two claims can be proved nowhere else. The first is that the occurrence row
// really elects ONE winner when instances race — a fake store implements the
// rule the code was written to, which proves only that the fake agrees with
// itself. The second is that the advisory lock is released when the holder
// DISAPPEARS, which is the whole reason the lock is the liveness half rather
// than a lease: it has no duration to tune because nothing is waiting on a
// clock.
//
// The migration rollback is here for the reason ADR 0018 recorded: the
// architecture gates walk internal/modules/ only, so a core owner's up/down
// pair added outside that tree is certified by nothing else either.
package jobpg

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/internal/core/job"
)

const postgresImage = "postgres:16-alpine"

var (
	testPool *db.Pool
	testDSN  string
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_job"),
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

	// The schema is applied exactly as startup applies it.
	if err = db.Migrate(ctx, testDSN, Migrations(), MigrationOwner); err != nil {
		fmt.Fprintf(os.Stderr, "the job schema could not be applied: %v\n", err)

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

// freshStore empties the table and returns a store over it.
func freshStore(t *testing.T) *Store {
	t.Helper()

	_, err := testPool.Pool().Exec(t.Context(), `TRUNCATE job_run`)
	require.NoError(t, err)

	return New(testPool)
}

// --- the migration ----------------------------------------------------------

// TestTheMigrationIsReallyReversible certifies the up/down pair.
func TestTheMigrationIsReallyReversible(t *testing.T) {
	ctx := t.Context()
	dsn := freshDatabase(t)

	require.NoError(t, db.Migrate(ctx, dsn, Migrations(), MigrationOwner))

	version, dirty, err := db.Version(ctx, dsn, MigrationOwner)
	require.NoError(t, err)
	require.False(t, dirty)
	require.Equal(t, uint(1), version)

	require.NoError(t, db.MigrateDown(ctx, dsn, Migrations(), MigrationOwner, 1))

	var exists bool
	require.NoError(t, poolFor(t, dsn).Pool().QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables WHERE table_name = 'job_run'
		)`).Scan(&exists))
	assert.False(t, exists, "the table has to be gone after the rollback")

	require.NoError(t, db.Migrate(ctx, dsn, Migrations(), MigrationOwner),
		"the schema has to apply again; a down that leaves the index behind fails here")
}

// --- the election -----------------------------------------------------------

// TestOnlyOneClaimWinsARace is the proof a fake store cannot give.
//
// Sixteen goroutines on independent connections claim the same occurrence at
// once, which is what sixteen replicas do at the top of the hour. The primary
// key has to elect exactly one — no leader, no coordinator, no vote.
func TestOnlyOneClaimWinsARace(t *testing.T) {
	store := freshStore(t)
	due := time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)

	var won atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})

	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			claimed, err := store.Claim(t.Context(), "nightly", due)
			if err == nil && claimed {
				won.Add(1)
			}
		}()
	}

	close(start)
	wg.Wait()

	assert.Equal(t, int64(1), won.Load(),
		"exactly one instance may claim an occurrence; %d did", won.Load())
}

// TestADifferentOccurrenceIsADifferentClaim proves the schedule advances.
//
// Without it the first claim would win forever and the job would run once in
// the lifetime of the installation.
func TestADifferentOccurrenceIsADifferentClaim(t *testing.T) {
	store := freshStore(t)
	ctx := t.Context()

	first, err := store.Claim(ctx, "nightly", time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.True(t, first)

	again, err := store.Claim(ctx, "nightly", time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.False(t, again, "the same occurrence cannot be claimed twice")

	next, err := store.Claim(ctx, "nightly", time.Date(2026, 3, 14, 10, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.True(t, next, "the next occurrence is a new claim")
}

// TestAnUnfinishedRunIsVisibleAsUnfinished proves a process that died leaves a
// trace.
//
// It is the only trace there would be: the lock is gone with the backend, so
// nothing else in the system remembers that a run started.
func TestAnUnfinishedRunIsVisibleAsUnfinished(t *testing.T) {
	store := freshStore(t)
	ctx := t.Context()
	due := time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)

	claimed, err := store.Claim(ctx, "nightly", due)
	require.NoError(t, err)
	require.True(t, claimed)
	// No Finish: the process "died" here.

	history, err := store.Last(ctx, []string{"nightly"})
	require.NoError(t, err)

	run := history["nightly"]
	assert.True(t, run.Unfinished(), "a claimed-but-unfinished run has to read as unfinished")
	assert.False(t, run.Succeeded())
}

// TestAFailureIsRecordedAsARunThatHappened proves a failed run is not hidden.
func TestAFailureIsRecordedAsARunThatHappened(t *testing.T) {
	store := freshStore(t)
	ctx := t.Context()
	due := time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)

	_, err := store.Claim(ctx, "nightly", due)
	require.NoError(t, err)
	require.NoError(t, store.Finish(ctx, "nightly", due, job.Outcome{
		Err: errors.New("the work could not be done"), Detail: "0 of 3",
	}))

	history, err := store.Last(ctx, []string{"nightly"})
	require.NoError(t, err)

	run := history["nightly"]
	assert.False(t, run.Unfinished(), "it finished — badly, but it finished")
	assert.False(t, run.Succeeded())
	assert.Contains(t, run.Failure, "could not be done")
	assert.Equal(t, "0 of 3", run.Detail)
}

// TestLastReturnsTheMostRecentOccurrence proves the listing does not show an
// old success while a newer failure exists.
func TestLastReturnsTheMostRecentOccurrence(t *testing.T) {
	store := freshStore(t)
	ctx := t.Context()

	older := time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 3, 14, 10, 0, 0, 0, time.UTC)

	_, err := store.Claim(ctx, "nightly", older)
	require.NoError(t, err)
	require.NoError(t, store.Finish(ctx, "nightly", older, job.Outcome{}))

	_, err = store.Claim(ctx, "nightly", newer)
	require.NoError(t, err)
	require.NoError(t, store.Finish(ctx, "nightly", newer,
		job.Outcome{Err: errors.New("it broke")}))

	history, err := store.Last(ctx, []string{"nightly"})
	require.NoError(t, err)

	assert.Equal(t, newer.UTC(), history["nightly"].Due.UTC())
	assert.False(t, history["nightly"].Succeeded(),
		"a newer failure must not be hidden behind an older success")
}

// --- the lock ---------------------------------------------------------------

// TestTheLockExcludesASecondHolder proves the liveness half works.
func TestTheLockExcludesASecondHolder(t *testing.T) {
	store := freshStore(t)
	key := job.LockKey("nightly")

	inside := make(chan struct{})
	release := make(chan struct{})

	var secondLocked bool
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		_, err := store.WithLock(t.Context(), key, func(context.Context) error {
			close(inside)
			<-release

			return nil
		})
		assert.NoError(t, err)
	}()

	<-inside
	locked, err := store.WithLock(t.Context(), key, func(context.Context) error {
		secondLocked = true

		return nil
	})
	require.NoError(t, err)
	close(release)
	wg.Wait()

	assert.False(t, locked, "a second holder must be refused while the first holds the lock")
	assert.False(t, secondLocked, "and its function must never run")
}

// TestTheLockIsReleasedWhenTheHOLDERDisappears is the reason the lock is the
// liveness half rather than a lease.
//
// A separate CONNECTION takes the lock and is then closed without unlocking —
// which is what a process being killed looks like from the database's side.
// PostgreSQL releases a session lock when the backend exits, and it does so
// without any timer. That is what makes the mechanism free of a duration
// nobody can choose correctly: a lease forces the bargain "long enough for the
// longest run" against "how long a dead run stays invisible".
func TestTheLockIsReleasedWhenTheHOLDERDisappears(t *testing.T) {
	store := freshStore(t)
	ctx := t.Context()
	key := job.LockKey("nightly")

	// A second pool stands in for another process.
	other, err := db.New(ctx, db.DefaultConfig(testDSN), nil)
	require.NoError(t, err)

	conn, err := other.Pool().Acquire(ctx)
	require.NoError(t, err)

	var held bool
	require.NoError(t, conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&held))
	require.True(t, held, "the other process took the lock")

	// While it holds it, we cannot.
	locked, err := store.WithLock(ctx, key, func(context.Context) error { return nil })
	require.NoError(t, err)
	require.False(t, locked, "the lock is held elsewhere")

	// The other process dies: no unlock, the connection simply goes away.
	conn.Release()
	other.Close()

	// Postgres reaps it. No timer, no lease, no sweeper.
	require.Eventually(t, func() bool {
		got, err := store.WithLock(ctx, key, func(context.Context) error { return nil })

		return err == nil && got
	}, 10*time.Second, 50*time.Millisecond,
		"the lock has to become free once the holder's backend exits")
}

// TestTheLockIsReleasedAfterTheFunctionReturns proves the happy path does not
// leak the lock back into the pool.
//
// A leaked session lock would be held by a pooled connection for as long as that
// connection lives, and the job would simply stop running with nothing to show
// why.
func TestTheLockIsReleasedAfterTheFunctionReturns(t *testing.T) {
	store := freshStore(t)
	key := job.LockKey("nightly")

	for range 3 {
		locked, err := store.WithLock(t.Context(), key, func(context.Context) error { return nil })
		require.NoError(t, err)
		require.True(t, locked, "each pass has to be able to take the lock again")
	}
}

// TestTheLockSurvivesAFailingFunction proves an error does not leak it either.
func TestTheLockSurvivesAFailingFunction(t *testing.T) {
	store := freshStore(t)
	key := job.LockKey("nightly")
	wanted := errors.New("the job failed")

	locked, err := store.WithLock(t.Context(), key, func(context.Context) error { return wanted })
	require.True(t, locked)
	require.ErrorIs(t, err, wanted)

	locked, err = store.WithLock(t.Context(), key, func(context.Context) error { return nil })
	require.NoError(t, err)
	assert.True(t, locked, "a failing job must not leave its lock behind")
}

// --- helpers ----------------------------------------------------------------

// freshDatabase creates an empty database for the migration test.
func freshDatabase(t *testing.T) string {
	t.Helper()

	name := fmt.Sprintf("job_migration_%d", time.Now().UnixNano())
	_, err := testPool.Pool().Exec(t.Context(), `CREATE DATABASE `+name)
	require.NoError(t, err)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := testPool.Pool().Exec(ctx, `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`); err != nil {
			t.Logf("the temporary database %s could not be dropped: %v", name, err)
		}
	})

	u, err := url.Parse(testDSN)
	require.NoError(t, err)
	u.Path = "/" + name

	return u.String()
}

// poolFor opens a pool against one of the temporary databases.
func poolFor(t *testing.T, dsn string) *db.Pool {
	t.Helper()

	pool, err := db.New(t.Context(), db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return pool
}
