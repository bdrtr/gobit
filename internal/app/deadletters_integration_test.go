//go:build integration

package app

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/eventbus/outbox"
)

// deadLetterEnv points the binary at a database of its own and gives it the
// smallest configuration that boots.
//
// The command reads its configuration exactly the way the server does
// ([config.Load]), so the test has to speak to it the same way: through the
// environment. That is the point of the design — run inside the running
// container it is configured already — and a test that reached past it would be
// exercising a wiring nobody uses.
func deadLetterEnv(t *testing.T, dsn string) {
	t.Helper()

	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("JWT_SECRET", "deadletter-integration-test-secret-32-bytes")
	t.Setenv("LOG_LEVEL", "warn")
}

// errReceiverDown is what a publish failure looks like from here.
var errReceiverDown = errors.New("the receiver is unreachable")

// outboxSchema applies the outbox migrations.
//
// The command deliberately does NOT migrate — it opens a pool and nothing else
// (see [openOutboxStore]) — so the schema has to arrive from somewhere, and
// this is the same call [openApplication] makes at startup. If the command
// ever grew a migration of its own, this line would become redundant rather
// than wrong, and the test would still be reading the same table.
func outboxSchema(t *testing.T, dsn string) *db.Pool {
	t.Helper()

	ctx := context.Background()

	require.NoError(t, db.Migrate(ctx, dsn, outbox.Migrations(), outbox.MigrationOwner))

	pool, err := db.New(ctx, db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return pool
}

// writePending puts an unpublished event in the outbox and leaves it alone.
func writePending(t *testing.T, pool *db.Pool, id, name string) {
	t.Helper()

	require.NoError(t, outbox.Write(context.Background(), pool.Pool(), eventbus.Event{
		ID: id, Name: name, Data: map[string]any{"to": "shopper@example.com"},
	}))
}

// killPending runs the REAL relay against a receiver that refuses, until every
// pending row has been given up on.
//
// The dead letters are made by the relay rather than by an UPDATE, and that is
// the whole point of doing it here: what this command reads is whatever the
// relay writes, and a fixture that wrote the columns by hand would agree with
// the test's idea of the schema instead of with the relay's.
func killPending(t *testing.T, pool *db.Pool, want ...string) {
	t.Helper()

	store := outbox.NewStoreWithPolicy(pool.Pool(), outbox.Policy{
		FirstDelay: time.Millisecond, MaxAttempts: 1,
	})

	result, err := store.Relay(context.Background(), 100,
		func(context.Context, outbox.Pending) error { return errReceiverDown })
	require.NoError(t, err)

	for _, id := range want {
		require.Contains(t, result.DeadLettered, id, "the relay had to give up on %s", id)
	}
}

// outboxRow is one row as the assertions read it.
type outboxRow struct {
	exists    bool
	dead      bool
	attempts  int64
	published bool
}

// readOutboxRow reads the row straight from the table.
//
// The assertions go to the DATABASE rather than to a second run of the command:
// what has to be proved is that the row changed, and a command that reported a
// change it never made would satisfy an assertion on its own output.
func readOutboxRow(t *testing.T, pool *db.Pool, id string) outboxRow {
	t.Helper()

	var row outboxRow
	err := pool.Pool().QueryRow(context.Background(),
		`SELECT dead_lettered_at IS NOT NULL, attempts, published_at IS NOT NULL
		 FROM event_outbox WHERE id = $1`, id).Scan(&row.dead, &row.attempts, &row.published)
	if err != nil {
		return outboxRow{}
	}
	row.exists = true

	return row
}

// TestDeadLettersListsThePileFromTheTERMINAL is the wiring proof, and it is the
// finding this command was written for.
//
// [outbox.Store.Redrive] and [outbox.Store.Discard] were tested against a real
// database and had NO caller outside those tests: the pile could be seen in
// `gobit jobs` and reached from nowhere. This runs the binary's own dispatch —
// the whole path an operator types — against a real Postgres.
func TestDeadLettersListsThePileFromTheTERMINAL(t *testing.T) {
	dsn := migrateDSN(t)
	deadLetterEnv(t, dsn)

	pool := outboxSchema(t, dsn)
	writePending(t, pool, "evt_dead_1", "notification.requested")
	writePending(t, pool, "evt_dead_2", "order.placed")
	killPending(t, pool, "evt_dead_1", "evt_dead_2")
	// Written AFTER the relay pass, so it is still pending and must not appear.
	writePending(t, pool, "evt_alive", "order.placed")

	var out bytes.Buffer
	require.NoError(t, Main([]string{deadLettersCommand}, &out, Options{}))

	report := out.String()
	assert.Contains(t, report, "2 dead letter(s) in the outbox")
	assert.Contains(t, report, "evt_dead_1")
	assert.Contains(t, report, "notification.requested")
	assert.Contains(t, report, errReceiverDown.Error(),
		"the last error is what decides redrive against discard, and the row is where it lives")
	assert.NotContains(t, report, "evt_alive",
		"a pending promise is not a dead letter; listing it would send an operator to discard "+
			"an event the relay is still going to deliver")
}

// TestARedrivenDeadLetterIsBackInTheQUEUE proves the verb writes.
//
// The proof is read from the table, not from the report: the attempt count is
// reset and the row is no longer dead, which is exactly what puts it back in
// the relay's partial index. The report's own claim — that the relay will try
// it again — is then verified by running the relay.
func TestARedrivenDeadLetterIsBackInTheQUEUE(t *testing.T) {
	dsn := migrateDSN(t)
	deadLetterEnv(t, dsn)

	pool := outboxSchema(t, dsn)
	writePending(t, pool, "evt_redrive", "notification.requested")
	killPending(t, pool, "evt_redrive")
	require.True(t, readOutboxRow(t, pool, "evt_redrive").dead)

	var out bytes.Buffer
	require.NoError(t, Main(
		[]string{deadLettersCommand, cmdRedrive, "evt_redrive", "-" + flagConfirm, "evt_redrive"},
		&out, Options{}))

	assert.Contains(t, out.String(), "evt_redrive is back in the queue")
	assert.Contains(t, out.String(), "the pile is now EMPTY",
		"whether the failing job clears is the question the operator arrived with")

	row := readOutboxRow(t, pool, "evt_redrive")
	assert.False(t, row.dead)
	assert.Zero(t, row.attempts,
		"the count is reset; a row that kept it would be back on the pile after ONE more failure")

	// The report promised the relay would try it again. That promise is worth
	// exactly as much as the row being selectable, so it is checked.
	store := outbox.NewStore(pool.Pool())
	result, err := store.Relay(context.Background(), 10,
		func(context.Context, outbox.Pending) error { return nil })
	require.NoError(t, err)
	assert.Equal(t, 1, result.Published, "a redriven row must be reachable by the relay again")
}

// TestADiscardedDeadLetterIsGoneFromTheTABLE proves the destructive verb.
func TestADiscardedDeadLetterIsGoneFromTheTABLE(t *testing.T) {
	dsn := migrateDSN(t)
	deadLetterEnv(t, dsn)

	pool := outboxSchema(t, dsn)
	writePending(t, pool, "evt_discard", "notification.requested")
	writePending(t, pool, "evt_keep", "order.placed")
	killPending(t, pool, "evt_discard", "evt_keep")

	var out bytes.Buffer
	require.NoError(t, Main(
		[]string{deadLettersCommand, cmdDiscard, "evt_discard", "-" + flagConfirm, "evt_discard"},
		&out, Options{}))

	assert.Contains(t, out.String(), "evt_discard is gone")
	assert.Contains(t, out.String(), "1 dead letter(s) are still waiting",
		"the remaining count is what says the job stays red")

	assert.False(t, readOutboxRow(t, pool, "evt_discard").exists)
	assert.True(t, readOutboxRow(t, pool, "evt_keep").exists,
		"one id was named and one row was touched")
}

// TestTheGuardREFUSESAgainstARealDatabase is the safety property proved where
// it matters.
//
// The unit test shows the parse refuses. This shows what the refusal is worth:
// the process runs, the database is reachable, the id names a real dead letter
// — and the row is untouched afterwards. Without this, a future edit that
// parsed the confirmation and then ignored it would keep every unit test green.
func TestTheGuardREFUSESAgainstARealDatabase(t *testing.T) {
	dsn := migrateDSN(t)
	deadLetterEnv(t, dsn)

	pool := outboxSchema(t, dsn)
	writePending(t, pool, "evt_guarded", "notification.requested")
	killPending(t, pool, "evt_guarded")
	before := readOutboxRow(t, pool, "evt_guarded")
	require.True(t, before.dead)

	unconfirmed := map[string][]string{
		"discard with no confirmation": {cmdDiscard, "evt_guarded"},
		"redrive with no confirmation": {cmdRedrive, "evt_guarded"},
		"discard confirming another":   {cmdDiscard, "evt_guarded", "-" + flagConfirm, "evt_other"},
		"redrive confirming another":   {cmdRedrive, "evt_guarded", "-" + flagConfirm, "evt_other"},
	}

	for name, args := range unconfirmed {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			err := Main(append([]string{deadLettersCommand}, args...), &out, Options{})

			require.Error(t, err)
			assert.Equal(t, codeDeadLetterRefused, coreerrors.CodeOf(err))
			assert.Empty(t, out.String(), "a refusal reports nothing as having been done")
			assert.Equal(t, before, readOutboxRow(t, pool, "evt_guarded"),
				"the row must be byte for byte what it was")
		})
	}
}

// TestTheVerbsREFUSEARowThatIsNotDead keeps the operator's verbs off the queue.
//
// A mistyped id must not delete a promise nobody has given up on. The predicate
// that stops it is in the store's SQL; what this checks is that the command
// does not paper over the zero row count with a cheerful "done".
func TestTheVerbsREFUSEARowThatIsNotDead(t *testing.T) {
	dsn := migrateDSN(t)
	deadLetterEnv(t, dsn)

	pool := outboxSchema(t, dsn)
	writePending(t, pool, "evt_pending", "order.placed")

	for _, verb := range []string{cmdRedrive, cmdDiscard} {
		t.Run(verb, func(t *testing.T) {
			var out bytes.Buffer
			err := Main(
				[]string{deadLettersCommand, verb, "evt_pending", "-" + flagConfirm, "evt_pending"},
				&out, Options{})

			require.Error(t, err)
			assert.True(t, coreerrors.IsNotFound(err), "error: %v", err)
			assert.Contains(t, err.Error(), "NOTHING was changed")
			assert.True(t, readOutboxRow(t, pool, "evt_pending").exists,
				"a pending promise cannot be deleted through the dead-letter verbs")
		})
	}
}

// TestAnEmptyPileIsReportedFromTheTERMINAL is the healthy answer, end to end.
//
// It also proves the command needs nothing but a pool: the schema is applied
// and no module has ever been bootstrapped in this database, which is the
// installation an operator has when the thing that is broken is startup.
func TestAnEmptyPileIsReportedFromTheTERMINAL(t *testing.T) {
	dsn := migrateDSN(t)
	deadLetterEnv(t, dsn)
	outboxSchema(t, dsn)

	var out bytes.Buffer
	require.NoError(t, Main([]string{deadLettersCommand}, &out, Options{}))

	assert.Contains(t, out.String(), "the pile is EMPTY")
}
