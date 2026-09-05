//go:build integration

// These tests need a real PostgreSQL (and therefore Docker).
//
// # Why a fake proves nothing here
//
// This package is delivery machinery, and every claim it makes lives in SQL:
// the backoff is a comparison against the database's clock, the ceiling is a
// CASE inside the UPDATE that counts the attempt, and the reason a poisoned
// event stops blocking the queue is that a partial index no longer contains it.
// A fake would implement the rule the code was written to and then agree with
// itself. The starvation this package was changed to fix was FOUND by running
// the old code against a real database, not by reading it.
//
// # And why the migration is certified here
//
// The architecture gate that runs every up/down/up on a live database walks
// internal/modules only, so a core owner's migration pair is certified by
// nothing else (ADR 0018 recorded the same reasoning for the job schema). The
// outbox is a core owner, and 000002 drops an index and adds a constraint —
// two of the three shapes that leave golang-migrate's ledger dirty when the
// down is wrong.
package outbox_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/eventbus/outbox"
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
		tcpostgres.WithDatabase("gobit_outbox"),
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
	if err = db.Migrate(ctx, testDSN, outbox.Migrations(), outbox.MigrationOwner); err != nil {
		fmt.Fprintf(os.Stderr, "the outbox schema could not be applied: %v\n", err)

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

// --- helpers ----------------------------------------------------------------

// errReceiverDown is what a publish failure looks like from here.
var errReceiverDown = errors.New("the receiver is unreachable")

// refuse is a publish that never works.
func refuse(_ context.Context, _ outbox.Pending) error { return errReceiverDown }

// accept is a publish that always works.
func accept(_ context.Context, _ outbox.Pending) error { return nil }

// refuseOnly fails for the named ids and accepts everything else.
func refuseOnly(ids ...string) func(context.Context, outbox.Pending) error {
	poisoned := map[string]bool{}
	for _, id := range ids {
		poisoned[id] = true
	}

	return func(_ context.Context, p outbox.Pending) error {
		if poisoned[p.ID] {
			return errReceiverDown
		}

		return nil
	}
}

// freshStore empties the table and returns a store with the given policy.
//
// The tests are not parallel and each one truncates: the relay's whole subject
// is WHICH rows a query selects, so a row left behind by a neighboring test
// would not fail loudly, it would change what "the oldest due row" means.
func freshStore(t *testing.T, policy outbox.Policy) *outbox.Store {
	t.Helper()

	_, err := testPool.Pool().Exec(t.Context(), `TRUNCATE event_outbox`)
	require.NoError(t, err)

	return outbox.NewStoreWithPolicy(testPool.Pool(), policy)
}

// write puts one pending event in the table.
func write(t *testing.T, id string) {
	t.Helper()

	require.NoError(t, outbox.Write(t.Context(), testPool.Pool(), eventbus.Event{
		ID: id, Name: "order.placed", Data: map[string]any{"order_id": id},
	}))
}

// row is what a test asserts about, read straight out of the table.
type row struct {
	attempts  int64
	lastError string
	published bool
	dead      bool
	dueIn     time.Duration
}

// readRow reads one row's delivery state.
func readRow(t *testing.T, id string) row {
	t.Helper()

	var got row
	require.NoError(t, testPool.Pool().QueryRow(t.Context(), `
		SELECT attempts,
		       last_error,
		       published_at IS NOT NULL,
		       dead_lettered_at IS NOT NULL,
		       next_attempt_at - now()
		FROM event_outbox WHERE id = $1`, id).
		Scan(&got.attempts, &got.lastError, &got.published, &got.dead, &got.dueIn))

	return got
}

// --- the migration ----------------------------------------------------------

// TestTheMigrationIsReallyReversible certifies the up/down/up round trip.
//
// A down that leaves the new index behind blows up on the second up with
// "already exists", and a down that leaves the ledger dirty makes the
// installation permanently unable to migrate — which is the fault that once
// stopped this repository's server from coming up at all.
func TestTheMigrationIsReallyReversible(t *testing.T) {
	ctx := t.Context()
	dsn := freshDatabase(t)

	require.NoError(t, db.Migrate(ctx, dsn, outbox.Migrations(), outbox.MigrationOwner))

	version, dirty, err := db.Version(ctx, dsn, outbox.MigrationOwner)
	require.NoError(t, err)
	require.False(t, dirty)
	require.Equal(t, uint(2), version, "the retry schema is the second migration")

	// One step back, not all the way: this proves 000002's own down, and the
	// table has to survive it with 000001's shape.
	require.NoError(t, db.MigrateDown(ctx, dsn, outbox.Migrations(), outbox.MigrationOwner, 1))

	pool := poolFor(t, dsn)

	var columns int
	require.NoError(t, pool.Pool().QueryRow(ctx, `
		SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'event_outbox'
		  AND column_name IN ('next_attempt_at', 'dead_lettered_at')`).Scan(&columns))
	assert.Zero(t, columns, "the columns have to be gone after the rollback")

	var restored bool
	require.NoError(t, pool.Pool().QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE tablename = 'event_outbox' AND indexname = 'event_outbox_pending_idx'
		)`).Scan(&restored))
	assert.True(t, restored,
		"the rollback owes 000001's index back; without it the relay's query loses "+
			"its index and nothing says so")

	require.NoError(t, db.Migrate(ctx, dsn, outbox.Migrations(), outbox.MigrationOwner),
		"the up after the down failed")

	after, dirty, err := db.Version(ctx, dsn, outbox.MigrationOwner)
	require.NoError(t, err)
	assert.False(t, dirty)
	assert.Equal(t, version, after)
}

// TestARowCannotBeBothDeliveredAndGivenUpOn is the constraint's job.
//
// A dead letter carrying a publication instant would be unreadable: nobody
// could say whether the event went out. Nothing writes that state today, and
// this is what catches the writer that one day tries.
func TestARowCannotBeBothDeliveredAndGivenUpOn(t *testing.T) {
	freshStore(t, outbox.Policy{})
	write(t, "order_1")

	_, err := testPool.Pool().Exec(t.Context(),
		`UPDATE event_outbox SET dead_lettered_at = now(), published_at = now() WHERE id = $1`,
		"order_1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "event_outbox_dead_letter_is_unpublished")
}

// --- delivery ---------------------------------------------------------------

// TestAPendingEventIsPublishedAndClosed is the path everything else is a
// deviation from.
func TestAPendingEventIsPublishedAndClosed(t *testing.T) {
	store := freshStore(t, outbox.Policy{})
	write(t, "order_1")

	result, err := store.Relay(t.Context(), 10, accept)
	require.NoError(t, err)

	assert.Equal(t, 1, result.Published)
	assert.Zero(t, result.Failed)
	assert.Empty(t, result.DeadLettered)
	assert.True(t, readRow(t, "order_1").published)

	// A closed row is not offered again. Without this the relay would republish
	// every event it has ever sent, forever.
	second, err := store.Relay(t.Context(), 10, accept)
	require.NoError(t, err)
	assert.Zero(t, second.Published)
}

// --- retry with backoff -----------------------------------------------------

// TestAFailedEventIsNotTriedAgainImmediately is the first half of the gap.
//
// Before this schema, the row was eligible again on the very next pass — a
// minute later, and every minute after that for the life of the installation.
// The delay is what turns "retried forever" into "retried on a schedule that
// ends".
func TestAFailedEventIsNotTriedAgainImmediately(t *testing.T) {
	store := freshStore(t, outbox.Policy{FirstDelay: time.Hour, MaxAttempts: 10})
	write(t, "order_1")

	first, err := store.Relay(t.Context(), 10, refuse)
	require.NoError(t, err, "a refused publish is not a failed pass")
	assert.Equal(t, 1, first.Failed)

	got := readRow(t, "order_1")
	assert.Equal(t, int64(1), got.attempts)
	assert.Equal(t, errReceiverDown.Error(), got.lastError,
		"the reason has to reach the row; it is the whole content of a dead letter later")
	assert.False(t, got.dead, "one failure is not a reason to give up")
	assert.Greater(t, got.dueIn, 50*time.Minute, "the next attempt is an hour out")

	second, err := store.Relay(t.Context(), 10, accept)
	require.NoError(t, err)
	assert.Zero(t, second.Published,
		"the row is not due yet; a pass that picked it up would mean no backoff at all")
	assert.Zero(t, second.Failed)
}

// TestAFailedEventIsTriedAgainWhenItsDelayHasPassed is the other side of the
// same claim.
//
// A backoff that never expired would be indistinguishable from a drop, which is
// the failure mode next door.
func TestAFailedEventIsTriedAgainWhenItsDelayHasPassed(t *testing.T) {
	store := freshStore(t, outbox.Policy{FirstDelay: 10 * time.Millisecond, MaxAttempts: 10})
	write(t, "order_1")

	_, err := store.Relay(t.Context(), 10, refuse)
	require.NoError(t, err)

	waitForDue(t, "order_1")

	second, err := store.Relay(t.Context(), 10, accept)
	require.NoError(t, err)
	assert.Equal(t, 1, second.Published, "the event was owed and it was delivered")
	assert.True(t, readRow(t, "order_1").published)
}

// TestTheDelayGrowsWithTheAttempts checks that the schedule the policy computes
// is the schedule the database is given.
//
// The delay travels from Go into SQL as a number of seconds fed to
// make_interval. That conversion is the kind of thing that works for the first
// attempt and quietly stops growing after it.
func TestTheDelayGrowsWithTheAttempts(t *testing.T) {
	store := freshStore(t, outbox.Policy{
		FirstDelay: time.Minute, MaxDelay: time.Hour, MaxAttempts: 10,
	})
	write(t, "order_1")

	_, err := store.Relay(t.Context(), 10, refuse)
	require.NoError(t, err)
	assert.InDelta(t, time.Minute.Seconds(), readRow(t, "order_1").dueIn.Seconds(), 5)

	// The row is not due, so the relay cannot be used to reach the second
	// attempt; the clock is moved instead of waiting a minute for it.
	makeDue(t, "order_1")

	_, err = store.Relay(t.Context(), 10, refuse)
	require.NoError(t, err)

	got := readRow(t, "order_1")
	assert.Equal(t, int64(2), got.attempts)
	assert.InDelta(t, (2 * time.Minute).Seconds(), got.dueIn.Seconds(), 5,
		"the second failure waits twice as long as the first")
}

// --- the ceiling and the dead letter ----------------------------------------

// TestAnEventIsGivenUpOnAfterTheCeiling is the second half of the gap.
//
// Without a ceiling the retry is unbounded by construction: there is no attempt
// count at which the relay would stop. The row leaving the query is what ends
// it, and it is also what unblocks everything behind it.
func TestAnEventIsGivenUpOnAfterTheCeiling(t *testing.T) {
	store := freshStore(t, outbox.Policy{FirstDelay: time.Millisecond, MaxAttempts: 3})
	write(t, "order_1")

	var dead []string
	for pass := 1; pass <= 3; pass++ {
		waitForDue(t, "order_1")
		result, err := store.Relay(t.Context(), 10, refuse)
		require.NoError(t, err)
		require.Equal(t, 1, result.Failed, "pass %d", pass)
		dead = append(dead, result.DeadLettered...)
	}

	assert.Equal(t, []string{"order_1"}, dead,
		"the pass that crossed the ceiling reports the id, and only that pass does")

	got := readRow(t, "order_1")
	assert.Equal(t, int64(3), got.attempts)
	assert.True(t, got.dead)
	assert.False(t, got.published, "giving up is not delivering")

	// The point of the ceiling: no fourth attempt, ever, no matter how long the
	// relay runs.
	waitForDue(t, "order_1")
	after, err := store.Relay(t.Context(), 10, refuse)
	require.NoError(t, err)
	assert.Zero(t, after.Failed, "a dead letter is not retried")
	assert.Equal(t, int64(3), readRow(t, "order_1").attempts)
}

// TestADeadLetterIsREADABLE is the difference between a dead letter and a drop.
//
// A row nothing reads is a silent loss with extra columns, and this repository
// has already built one of those: audit_log is written and never read. The
// reader is what makes the ceiling safe to have.
func TestADeadLetterIsREADABLE(t *testing.T) {
	store := freshStore(t, outbox.Policy{FirstDelay: time.Millisecond, MaxAttempts: 1})
	write(t, "order_1")

	before, err := store.DeadLetters(t.Context(), 10)
	require.NoError(t, err)
	assert.True(t, before.Empty(), "nothing has been given up on yet")

	_, err = store.Relay(t.Context(), 10, refuse)
	require.NoError(t, err)

	report, err := store.DeadLetters(t.Context(), 10)
	require.NoError(t, err)

	require.Equal(t, int64(1), report.Count)
	require.Len(t, report.Oldest, 1)

	letter := report.Oldest[0]
	assert.Equal(t, "order_1", letter.ID)
	assert.Equal(t, "order.placed", letter.Name)
	assert.Equal(t, int64(1), letter.Attempts)
	assert.Equal(t, errReceiverDown.Error(), letter.LastError,
		"WHY it died is the question a human asks second, and the row is the only place it lives")
	assert.False(t, letter.DeadLetteredAt.IsZero())
	assert.False(t, letter.CreatedAt.IsZero())
}

// TestTheDeadLetterCountIsThePileNotThePage keeps the number honest.
//
// The count decides whether anybody is woken up, and it is computed over the
// filtered set rather than the returned page. A report that counted its own
// sample would say "5" during an outage that killed five thousand.
func TestTheDeadLetterCountIsThePileNotThePage(t *testing.T) {
	store := freshStore(t, outbox.Policy{FirstDelay: time.Millisecond, MaxAttempts: 1})
	for _, id := range []string{"order_1", "order_2", "order_3"} {
		write(t, id)
	}

	_, err := store.Relay(t.Context(), 10, refuse)
	require.NoError(t, err)

	report, err := store.DeadLetters(t.Context(), 1)
	require.NoError(t, err)

	assert.Equal(t, int64(3), report.Count)
	assert.Len(t, report.Oldest, 1)

	// A limit of zero would return no rows at all, and the count is read off
	// them — the report would say the pile is empty while it is full.
	clamped, err := store.DeadLetters(t.Context(), 0)
	require.NoError(t, err)
	assert.Equal(t, int64(3), clamped.Count)
}

// --- the starvation the gap exists for --------------------------------------

// TestAFailingEventDoesNotBLOCKTheOnesBehindIt is the measured fault, fixed.
//
// Measured on 2026-09-06 against this database with the previous version of the
// relay: two permanently failing rows and a batch limit of two produced five
// consecutive passes that published NOTHING, and the healthy event written
// behind them ended with attempts = 0 — it was never attempted once. The
// backlog did not slow delivery down, it ended it.
//
// The backoff alone fixes it: the failing rows stop being due, so the batch has
// room for the row behind them.
func TestAFailingEventDoesNotBLOCKTheOnesBehindIt(t *testing.T) {
	store := freshStore(t, outbox.Policy{FirstDelay: time.Hour, MaxAttempts: 10})
	for _, id := range []string{"poison_1", "poison_2", "healthy_3"} {
		write(t, id)
	}

	publish := refuseOnly("poison_1", "poison_2")

	// The limit stands in for the real batch of 200 being filled by a backlog
	// of rows that can never succeed.
	first, err := store.Relay(t.Context(), 2, publish)
	require.NoError(t, err)
	require.Equal(t, 2, first.Failed)
	require.Zero(t, first.Published, "the batch was full of rows that cannot succeed")

	second, err := store.Relay(t.Context(), 2, publish)
	require.NoError(t, err)
	assert.Equal(t, 1, second.Published,
		"the poisoned rows are waiting out their delay, so the queue moved")
	assert.True(t, readRow(t, "healthy_3").published)
}

// TestADeadLetterLeavesTheQueueForGood is the same claim one step later.
//
// Backoff buys time; it does not empty the queue. A row that keeps failing
// becomes due again and again, and with enough of them the batch is full at
// every instant its delays expire. Giving up is what removes them from the
// relay's query permanently.
func TestADeadLetterLeavesTheQueueForGood(t *testing.T) {
	store := freshStore(t, outbox.Policy{FirstDelay: time.Millisecond, MaxAttempts: 1})
	for _, id := range []string{"poison_1", "poison_2", "healthy_3"} {
		write(t, id)
	}

	publish := refuseOnly("poison_1", "poison_2")

	first, err := store.Relay(t.Context(), 2, publish)
	require.NoError(t, err)
	require.Len(t, first.DeadLettered, 2)

	// Both poisoned rows are due again by now — a millisecond has passed — and
	// they are still not selected, because they are dead rather than waiting.
	waitForDue(t, "poison_1")

	second, err := store.Relay(t.Context(), 2, publish)
	require.NoError(t, err)
	assert.Equal(t, 1, second.Published)
	assert.Zero(t, second.Failed, "the dead rows are not tried again")
}

// --- the way back out -------------------------------------------------------

// TestARedrivenEventIsDeliveredAgain is the operator's answer to "the receiver
// is back".
//
// Without it the failed job the dead letter causes would have no off switch,
// and an alarm that cannot be cleared is one that gets muted.
func TestARedrivenEventIsDeliveredAgain(t *testing.T) {
	store := freshStore(t, outbox.Policy{FirstDelay: time.Millisecond, MaxAttempts: 1})
	write(t, "order_1")

	_, err := store.Relay(t.Context(), 10, refuse)
	require.NoError(t, err)
	require.True(t, readRow(t, "order_1").dead)

	taken, err := store.Redrive(t.Context(), "order_1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), taken)

	got := readRow(t, "order_1")
	assert.False(t, got.dead)
	assert.Zero(t, got.attempts,
		"the count is reset; a row that kept it would be back on the pile after ONE "+
			"more failure, which is a single retry wearing the name of a second chance")

	result, err := store.Relay(t.Context(), 10, accept)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Published)

	report, err := store.DeadLetters(t.Context(), 10)
	require.NoError(t, err)
	assert.True(t, report.Empty(), "the pile empties, so the job's failure clears")
}

// TestRedriveAndDiscardRefuseALiveRow keeps the operator's verbs off the queue.
//
// Both take ids from a human during an incident, and a mistyped id must not
// reset a healthy event's history — or, far worse, delete a promise nobody has
// given up on. The predicate that stops it is in the SQL, not in the caller.
func TestRedriveAndDiscardRefuseALiveRow(t *testing.T) {
	store := freshStore(t, outbox.Policy{FirstDelay: time.Hour, MaxAttempts: 10})
	write(t, "order_1")

	_, err := store.Relay(t.Context(), 10, refuse)
	require.NoError(t, err)
	require.Equal(t, int64(1), readRow(t, "order_1").attempts)

	taken, err := store.Redrive(t.Context(), "order_1")
	require.NoError(t, err)
	assert.Zero(t, taken, "the row is failing, not dead")
	assert.Equal(t, int64(1), readRow(t, "order_1").attempts, "its history is untouched")

	removed, err := store.Discard(t.Context(), "order_1")
	require.NoError(t, err)
	assert.Zero(t, removed)

	var exists bool
	require.NoError(t, testPool.Pool().QueryRow(t.Context(),
		`SELECT EXISTS (SELECT 1 FROM event_outbox WHERE id = $1)`, "order_1").Scan(&exists))
	assert.True(t, exists, "a pending promise cannot be deleted through the dead-letter verbs")
}

// TestADiscardedDeadLetterIsGone is the other exit.
//
// Discarding says a human looked at the event and decided nobody is owed it.
// It is the only destructive verb in the package, which is why the test that
// it touches nothing else sits directly above this one.
func TestADiscardedDeadLetterIsGone(t *testing.T) {
	store := freshStore(t, outbox.Policy{FirstDelay: time.Millisecond, MaxAttempts: 1})
	write(t, "order_1")
	write(t, "order_2")

	_, err := store.Relay(t.Context(), 10, refuseOnly("order_1"))
	require.NoError(t, err)

	removed, err := store.Discard(t.Context(), "order_1", "order_2")
	require.NoError(t, err)
	assert.Equal(t, int64(1), removed, "order_2 was published, so it is not a dead letter")

	report, err := store.DeadLetters(t.Context(), 10)
	require.NoError(t, err)
	assert.True(t, report.Empty())

	var count int
	require.NoError(t, testPool.Pool().QueryRow(t.Context(),
		`SELECT count(*) FROM event_outbox`).Scan(&count))
	assert.Equal(t, 1, count, "the published row stays; only the dead letter was removed")
}

// TestTheOperatorVerbsIgnoreAnEmptyList keeps a caller with nothing to do from
// touching every row.
//
// `id = ANY('{}')` matches nothing in PostgreSQL, so the SQL is already safe;
// this pins that the Go side does not one day grow a branch that drops the
// predicate when the list is empty.
func TestTheOperatorVerbsIgnoreAnEmptyList(t *testing.T) {
	store := freshStore(t, outbox.Policy{FirstDelay: time.Millisecond, MaxAttempts: 1})
	write(t, "order_1")

	_, err := store.Relay(t.Context(), 10, refuse)
	require.NoError(t, err)

	taken, err := store.Redrive(t.Context())
	require.NoError(t, err)
	assert.Zero(t, taken)

	removed, err := store.Discard(t.Context())
	require.NoError(t, err)
	assert.Zero(t, removed)

	report, err := store.DeadLetters(t.Context(), 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), report.Count, "the dead letter is still there, untouched")
}

// --- clock helpers ----------------------------------------------------------

// waitForDue blocks until the row's delay has elapsed.
//
// It polls the DATABASE's clock rather than sleeping for the policy's delay:
// the relay compares against now() on the server, and a test that slept against
// its own clock would be measuring the wrong one.
func waitForDue(t *testing.T, id string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		var due bool
		require.NoError(t, testPool.Pool().QueryRow(t.Context(),
			`SELECT next_attempt_at <= now() FROM event_outbox WHERE id = $1`, id).Scan(&due))
		if due {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the row %s never became due", id)
		}
		time.Sleep(time.Millisecond)
	}
}

// makeDue moves a row's next attempt into the past.
//
// It exists so a test can reach the SECOND failure of a policy whose first
// delay is a realistic minute. Shortening the delay instead would test a
// policy nobody runs, and waiting a minute would put a minute into every run
// of the suite.
func makeDue(t *testing.T, id string) {
	t.Helper()

	_, err := testPool.Pool().Exec(t.Context(),
		`UPDATE event_outbox SET next_attempt_at = now() - interval '1 second' WHERE id = $1`, id)
	require.NoError(t, err)
}

// freshDatabase creates a throwaway database and returns its DSN.
func freshDatabase(t *testing.T) string {
	t.Helper()

	name := fmt.Sprintf("outbox_migration_%d", time.Now().UnixNano())
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
