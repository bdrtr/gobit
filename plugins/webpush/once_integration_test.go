//go:build integration

package webpush

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/eventbus/outbox"
	"github.com/bdrtr/gobit/internal/testdb"
)

// TestTheOutboxsTwoDeliveriesPushOnce is D236 as the order module produces it.
//
// The order module writes `order.placed` to the outbox inside its transaction
// and publishes it after the commit; only the relay closes the row, so the
// relay publishes it again under the same id. The test does the same three
// things on its own database, with the real outbox and the in-memory bus, and
// counts the requests the push service received for one online device.
//
// The push count is asserted before the record is read: on a tree without the
// record the failure is "two pushes", not a missing table.
func TestTheOutboxsTwoDeliveriesPushOnce(t *testing.T) {
	ctx := t.Context()

	dsn := testdb.New(t, testDSN, "webpush_outbox")
	require.NoError(t, db.Migrate(ctx, dsn, migrationsRoot, ModuleName))
	require.NoError(t, db.Migrate(ctx, dsn, outbox.Migrations(), outbox.MigrationOwner))
	pool := testPoolFor(t, dsn)

	st := newStore(pool.Pool())
	push := newPushServer(t, http.StatusCreated)
	m := newTestModule(t, st)
	device(t, st, push.URL+"/p/online", "cust_1", m.opts.fingerprint)

	bus := eventbus.NewInMemory(slog.New(slog.DiscardHandler))
	require.NoError(t, bus.Subscribe(orderPlacedEvent, m.onOrderPlaced))

	event := eventbus.Event{
		ID:   "order.placed:order_outbox",
		Name: orderPlacedEvent,
		Data: map[string]any{
			"order_id":    "order_outbox",
			"display_id":  "1001",
			"customer_id": "cust_1",
			"item_count":  "2",
			"placed_at":   time.Now().UTC().Format(time.RFC3339Nano),
		},
	}

	// The order's transaction writes the row.
	tx, err := pool.Pool().Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, outbox.Write(ctx, tx, event))
	require.NoError(t, tx.Commit(ctx))

	// The fast path publishes after the commit and closes nothing.
	require.NoError(t, bus.Publish(ctx, event))

	// The relay reads the row the fast path left open and publishes it again.
	result, err := outbox.NewStore(pool.Pool()).Relay(ctx, 10,
		func(ctx context.Context, p outbox.Pending) error { return bus.Publish(ctx, p.Event()) })
	require.NoError(t, err)
	require.Equal(t, 1, result.Published, "the relay has to deliver the row the fast path left open")

	require.NoError(t, bus.Shutdown(ctx), "both deliveries have to finish")

	assert.Len(t, push.received(), 1,
		"one order, two deliveries of its event: the online device is pushed once")

	var recorded int
	require.NoError(t, pool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM webpush_claimed_event WHERE event_id = $1`, event.ID).Scan(&recorded))
	assert.Equal(t, 1, recorded, "the event is recorded once")
}

// countClaims reports how many events the shared database's record holds.
func countClaims(t *testing.T) int {
	t.Helper()

	var n int
	require.NoError(t, testPool.Pool().
		QueryRow(t.Context(), `SELECT count(*) FROM webpush_claimed_event`).Scan(&n))

	return n
}

// claimedIDs lists the event ids the shared database's record holds.
func claimedIDs(t *testing.T) []string {
	t.Helper()

	rows, err := testPool.Pool().Query(t.Context(), `SELECT event_id FROM webpush_claimed_event`)
	require.NoError(t, err)
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)

	return ids
}

// onlineCustomer stores one device for cust_1 on a push service that accepts
// every push, and returns the module and the service.
func onlineCustomer(t *testing.T) (*webpushModule, *pushServer) {
	t.Helper()

	st := freshStore(t)
	push := newPushServer(t, http.StatusCreated)
	m := newTestModule(t, st)
	device(t, st, push.URL+"/p/online", "cust_1", m.opts.fingerprint)

	return m, push
}

// TestAClaimIsFirstOnlyOnce pins the store's answer to an id it already holds:
// not first, and not an error.
func TestAClaimIsFirstOnlyOnce(t *testing.T) {
	st := freshStore(t)

	first, err := st.claim(t.Context(), "order.placed:order_claim")
	require.NoError(t, err)
	assert.True(t, first, "the first claim of an id writes it")

	first, err = st.claim(t.Context(), "order.placed:order_claim")
	require.NoError(t, err, "an id already recorded is the ordinary second delivery, not a fault")
	assert.False(t, first)
}

// TestASecondDeliveryPushesNothingAndFailsNothing delivers one event twice to
// the handler.
//
// The second call has to push nothing AND return nil: an error would make the
// bus call it twice more for a delivery that was right to do nothing.
func TestASecondDeliveryPushesNothingAndFailsNothing(t *testing.T) {
	m, push := onlineCustomer(t)
	event := placedEvent("cust_1")

	require.NoError(t, m.onOrderPlaced(t.Context(), event))
	require.NoError(t, m.onOrderPlaced(t.Context(), event),
		"the second delivery of an event is not a fault")

	assert.Len(t, push.received(), 1, "one event pushes once")
	assert.Equal(t, 1, countClaims(t))
}

// TestADeliveryWaitingOnAnotherClaimPushesNothing holds a delivery at the
// record while another has written the id and not committed.
//
// It is the window two deliveries arriving together open, made deterministic: a
// raw transaction writes the id and stays open, the handler runs, and the test
// waits until the database reports the handler blocked on a lock before it
// commits. A handler that reads before it writes, or pushes before it records,
// has pushed by then.
func TestADeliveryWaitingOnAnotherClaimPushesNothing(t *testing.T) {
	m, push := onlineCustomer(t)
	event := placedEvent("cust_1")
	ctx := t.Context()

	held, err := testPool.Pool().Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Rollback(context.Background()) })
	_, err = held.Exec(ctx, `INSERT INTO webpush_claimed_event (event_id) VALUES ($1)`, event.ID)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- m.onOrderPlaced(ctx, event) }()

	// The condition only reports; an assertion inside a polled function would
	// end the poller's goroutine rather than the test.
	waiting := func() bool {
		var n int
		err := testPool.Pool().QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database()
			  AND wait_event_type = 'Lock'
			  AND query LIKE '%webpush_claimed_event%'`).Scan(&n)

		return err == nil && n > 0
	}
	require.Eventually(t, waiting, 10*time.Second, 10*time.Millisecond,
		"the delivery has to wait on the id the other transaction holds")

	require.NoError(t, held.Commit(ctx))

	select {
	case err := <-done:
		require.NoError(t, err, "losing to another delivery is not a fault")
	case <-time.After(10 * time.Second):
		t.Fatal("the delivery did not return after the other transaction committed")
	}
	assert.Empty(t, push.received(), "the delivery that waited pushes nothing")
}

// TestTwoOrdersArePushedEach proves the record is keyed on the event, not on
// its name: two orders are two pushes.
func TestTwoOrdersArePushedEach(t *testing.T) {
	m, push := onlineCustomer(t)

	require.NoError(t, m.onOrderPlaced(t.Context(), placedEvent("cust_1")))
	require.NoError(t, m.onOrderPlaced(t.Context(), placedEvent("cust_1")))

	assert.Len(t, push.received(), 2, "every order is pushed once, not the first order alone")
}

// TestAnEventWithNobodyToPushToLeavesNoRecord proves the record holds fan-outs
// that started, not events that arrived.
//
// A customer who subscribes between two deliveries of an event is pushed by
// the second; a record written before the device read would have spent the
// event on a delivery that had nobody to push to.
func TestAnEventWithNobodyToPushToLeavesNoRecord(t *testing.T) {
	st := freshStore(t)
	push := newPushServer(t, http.StatusCreated)
	m := newTestModule(t, st)
	event := placedEvent("cust_1")

	require.NoError(t, m.onOrderPlaced(t.Context(), event))
	assert.Zero(t, countClaims(t), "a delivery with no device records nothing")

	device(t, st, push.URL+"/p/late", "cust_1", m.opts.fingerprint)
	require.NoError(t, m.onOrderPlaced(t.Context(), event))

	assert.Len(t, push.received(), 1, "the delivery after the device arrived is the first push")
}

// isolatedStore gives a test a database of its own with the plugin's schema,
// for a test that drops a table: in the shared one it would pull the table out
// from under every test after it (D135).
func isolatedStore(t *testing.T, prefix string) (*store, *db.Pool) {
	t.Helper()

	dsn := testdb.New(t, testDSN, prefix)
	require.NoError(t, db.Migrate(t.Context(), dsn, migrationsRoot, ModuleName))
	pool := testPoolFor(t, dsn)

	return newStore(pool.Pool()), pool
}

// TestARecordThatCannotBeWrittenAsksForTheRetry proves the handler neither
// pushes without the record nor swallows the fault.
//
// Pushing anyway would push twice the moment the record came back; returning
// nil would lose the push the bus's retry (ADR 0240) can still make.
func TestARecordThatCannotBeWrittenAsksForTheRetry(t *testing.T) {
	st, pool := isolatedStore(t, "webpush_norecord")
	push := newPushServer(t, http.StatusCreated)
	m := newTestModule(t, st)
	device(t, st, push.URL+"/p/online", "cust_1", m.opts.fingerprint)

	_, err := pool.Pool().Exec(t.Context(), `DROP TABLE webpush_claimed_event`)
	require.NoError(t, err)

	err = m.onOrderPlaced(t.Context(), placedEvent("cust_1"))

	require.Error(t, err, "a record that failed is returned to the bus")
	assert.Equal(t, coreerrors.KindUnavailable, coreerrors.KindOf(err),
		"and as a fault that may pass, which the bus calls again")
	assert.Empty(t, push.received(), "nothing is pushed without the record")

	devices, err := st.byCustomer(t.Context(), "cust_1")
	require.NoError(t, err)
	assert.Len(t, devices, 1, "the device is untouched")
}

// TestADeviceReadThatFailsAsksForTheRetry proves a failed device read is
// returned to the bus rather than logged and dropped. It comes before the
// record, so a retry cannot push twice.
func TestADeviceReadThatFailsAsksForTheRetry(t *testing.T) {
	st, pool := isolatedStore(t, "webpush_nodevices")
	m := newTestModule(t, st)

	_, err := pool.Pool().Exec(t.Context(), `DROP TABLE webpush_subscription`)
	require.NoError(t, err)

	err = m.onOrderPlaced(t.Context(), placedEvent("cust_1"))

	require.Error(t, err, "a device read that failed is returned to the bus")
	assert.Equal(t, coreerrors.KindUnavailable, coreerrors.KindOf(err))
}

// TestAnOrderOlderThanThePushTTLIsNotPushed proves a late delivery pushes
// nothing and records nothing.
//
// A redrive or a consumer back from an outage delivers an order hours later,
// after the record may have forgotten it; the age check is what stops it.
func TestAnOrderOlderThanThePushTTLIsNotPushed(t *testing.T) {
	m, push := onlineCustomer(t)
	event := placedEvent("cust_1")
	event.Data[fieldPlacedAt] = time.Now().Add(-pushHorizon - time.Hour).UTC().Format(time.RFC3339Nano)

	require.NoError(t, m.onOrderPlaced(t.Context(), event))

	assert.Empty(t, push.received(), "an order older than the push's TTL is not pushed")
	assert.Zero(t, countClaims(t))
}

// TestAnUnreadablePlacementMomentIsStillPushed proves a placed_at the plugin
// cannot read costs the age check, not the push.
func TestAnUnreadablePlacementMomentIsStillPushed(t *testing.T) {
	m, push := onlineCustomer(t)

	missing := placedEvent("cust_1")
	delete(missing.Data, fieldPlacedAt)
	require.NoError(t, m.onOrderPlaced(t.Context(), missing))
	assert.Len(t, push.received(), 1, "an order without placed_at is pushed")

	malformed := placedEvent("cust_1")
	malformed.Data[fieldPlacedAt] = "yesterday"
	require.NoError(t, m.onOrderPlaced(t.Context(), malformed))
	assert.Len(t, push.received(), 2, "an order whose placed_at is not a time is pushed")
}

// TestAnEventWithoutAnIDPushesNothing proves an event that cannot be told from
// its second delivery is not pushed, and is not a fault the bus would retry.
//
// The blank id is the one that arrives through the bus: it fills only an empty
// id, and the outbox refuses only an empty one.
func TestAnEventWithoutAnIDPushesNothing(t *testing.T) {
	for name, id := range map[string]string{"empty": "", "blank": "   "} {
		t.Run(name, func(t *testing.T) {
			m, push := onlineCustomer(t)
			event := placedEvent("cust_1")
			event.ID = id

			require.NoError(t, m.onOrderPlaced(t.Context(), event),
				"an event without an id cannot succeed on a retry either")

			assert.Empty(t, push.received())
			assert.Zero(t, countClaims(t))
		})
	}
}

// TestARecordThatCannotForgetStillPushes proves a failed deletion of old
// records is logged, not returned.
//
// It runs after the fan-out: an error there would send the bus to call the
// handler twice more for a push already made, and each call would find the
// record and push nothing, so it would buy three calls and a red log for a
// table that the next push trims anyway.
func TestARecordThatCannotForgetStillPushes(t *testing.T) {
	st, pool := isolatedStore(t, "webpush_noforget")
	push := newPushServer(t, http.StatusCreated)
	m := newTestModule(t, st)
	device(t, st, push.URL+"/p/online", "cust_1", m.opts.fingerprint)

	// A statement-level trigger fires on a DELETE that matches no row, which is
	// what the handler's deletion matches on a fresh table.
	for _, statement := range []string{`
		CREATE FUNCTION webpush_refuse_delete() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'the record refuses to forget';
		END $$`, `
		CREATE TRIGGER webpush_refuse_delete BEFORE DELETE ON webpush_claimed_event
			FOR EACH STATEMENT EXECUTE FUNCTION webpush_refuse_delete()`,
	} {
		_, err := pool.Pool().Exec(t.Context(), statement)
		require.NoError(t, err)
	}
	_, err := st.forget(t.Context(), claimRetention)
	require.Error(t, err, "the fixture has to make the deletion fail")

	event := placedEvent("cust_1")
	require.NoError(t, m.onOrderPlaced(t.Context(), event),
		"a deletion that failed after the push is not returned to the bus")

	assert.Len(t, push.received(), 1, "the push is made")
	first, err := st.claim(t.Context(), event.ID)
	require.NoError(t, err)
	assert.False(t, first, "and its event is recorded")
}

// TestTheRecordForgetsOnlyWhatIsOlderThanItsRetention proves a push deletes
// the rows past claimRetention and keeps the rest.
//
// The two seeded rows sit an hour either side of the retention, written on the
// database's clock as the handler's DELETE reads it.
func TestTheRecordForgetsOnlyWhatIsOlderThanItsRetention(t *testing.T) {
	m, _ := onlineCustomer(t)
	ctx := t.Context()

	_, err := testPool.Pool().Exec(ctx, `
		INSERT INTO webpush_claimed_event (event_id, claimed_at) VALUES
		    ('order.placed:past', now() - make_interval(secs => $1::float8)),
		    ('order.placed:within', now() - make_interval(secs => $2::float8))`,
		(claimRetention + time.Hour).Seconds(), (claimRetention - time.Hour).Seconds())
	require.NoError(t, err)

	event := placedEvent("cust_1")
	require.NoError(t, m.onOrderPlaced(ctx, event))

	assert.ElementsMatch(t, []string{"order.placed:within", event.ID}, claimedIDs(t),
		"the row past the retention is deleted, the one within it and the new one are kept")
}

// TestTheRecordRefusesABlankEventID proves the database refuses what the
// handler refuses, for a writer that is not the handler.
func TestTheRecordRefusesABlankEventID(t *testing.T) {
	freshStore(t)

	_, err := testPool.Pool().Exec(t.Context(), `INSERT INTO webpush_claimed_event (event_id) VALUES ('  ')`)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `check constraint "webpush_claimed_event_id_not_blank"`)
}

// beforeTheRecord is the plugin's version before migration 000002 added the
// record.
const beforeTheRecord = 1

// TestRollingTheRecordBackKeepsTheDevices proves migration 000002's down takes
// the record and nothing else: the devices are the one thing only the browsers
// can re-create.
func TestRollingTheRecordBackKeepsTheDevices(t *testing.T) {
	ctx := t.Context()

	dsn := testdb.New(t, testDSN, "webpush_rollback")
	require.NoError(t, db.Migrate(ctx, dsn, migrationsRoot, ModuleName))
	pool := testPoolFor(t, dsn)
	st := newStore(pool.Pool())
	device(t, st, "https://push.example.test/p/kept", "cust_1", "fp")
	_, err := st.claim(ctx, "order.placed:order_rollback")
	require.NoError(t, err)

	rollBackTo(ctx, t, dsn, beforeTheRecord)

	assert.False(t, tableExists(ctx, t, pool, tableClaimedEvent), "the record is gone")
	devices, err := st.byCustomer(ctx, "cust_1")
	require.NoError(t, err, "the device table has to survive the record's rollback")
	assert.Len(t, devices, 1, "and keep its rows")
}
