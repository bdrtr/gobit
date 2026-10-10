//go:build integration

package webhookout

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/jobreport"
)

// The claim, the release and the wake time, against PostgreSQL (ADR 0436).
// None of these tests waits for a clock: a row's due instant is written
// relative to the database's now(), and the minute a cap counts in is passed
// to the claim.

// queueRow writes one delivery for a receiver, due at now() plus dueIn, in the
// state named: "pending", "dead" or "delivered". created orders the rows.
func queueRow(t *testing.T, id, endpointID string, dueIn time.Duration, state string, created int) {
	t.Helper()

	_, err := testPool.Pool().Exec(t.Context(), `
		INSERT INTO webhook_delivery (id, endpoint_id, url, event_id, event_name, occurred_at,
		                              next_attempt_at, created_at, dead_lettered_at, delivered_at)
		VALUES ($1, $2, 'https://receiver.test/hook', $1, 'order.placed', now(),
		        now() + make_interval(secs => $3), now() - make_interval(secs => 1000 - $4),
		        CASE WHEN $5 = 'dead' THEN now() END, CASE WHEN $5 = 'delivered' THEN now() END)`,
		id, endpointID, dueIn.Seconds(), created, state)
	require.NoError(t, err)
}

// cappedReceiver registers a receiver capped at rate deliveries a minute.
func cappedReceiver(t *testing.T, m *webhookModule, rate int64) endpoint {
	t.Helper()

	e, err := m.store.createEndpoint(t.Context(), "https://capped.test/hook", []string{topicOrderPlaced},
		nil, nil, "capped", rate)
	require.NoError(t, err)

	return e
}

// futureMinute is a minute's start an hour ahead by the database's clock: a
// claim named an instant built on it finds every row due now due, and the
// leases it writes stay ahead of it.
func futureMinute(t *testing.T) time.Time {
	t.Helper()

	var minute time.Time
	require.NoError(t, testPool.Pool().QueryRow(t.Context(),
		`SELECT date_trunc('minute', now()) + interval '1 hour'`).Scan(&minute))

	return minute.UTC()
}

// claimedBy counts a claim's rows per receiver.
func claimedBy(claimed []delivery) map[string]int {
	out := map[string]int{}
	for i := range claimed {
		out[claimed[i].EndpointID]++
	}

	return out
}

// TestTheNextWakeIsTheEarliestStepStillToCome: after a claim, the worker
// sleeps until the earliest pending delivery due after that claim's instant —
// not one due before it, which the claim left (a cap, another process), not a
// dead or delivered one, and not one whose receiver is gone — and claims again
// at once when that delivery fell due while it was sending (D273).
func TestTheNextWakeIsTheEarliestStepStillToCome(t *testing.T) {
	m := freshModule(t)
	e := register(t, m, "https://receiver.test/hook", topicOrderPlaced)
	gone := register(t, m, "https://gone.test/hook", topicOrderPlaced)

	var claimed time.Time
	require.NoError(t, testPool.Pool().QueryRow(t.Context(), `SELECT now() - interval '5 seconds'`).Scan(&claimed))
	queueRow(t, "whd_overdue", e.ID, -10*time.Second, "pending", 1)
	queueRow(t, "whd_during", e.ID, -2*time.Second, "pending", 2)
	queueRow(t, "whd_soon", e.ID, 37*time.Second, "pending", 3)
	queueRow(t, "whd_later", e.ID, 90*time.Second, "pending", 4)
	queueRow(t, "whd_dead", e.ID, 5*time.Second, "dead", 5)
	queueRow(t, "whd_delivered", e.ID, 3*time.Second, "delivered", 6)
	queueRow(t, "whd_orphan", gone.ID, 2*time.Second, "pending", 7)
	removed, err := m.store.deleteEndpoint(t.Context(), gone.ID)
	require.NoError(t, err)
	require.True(t, removed)

	wait, found, err := m.store.nextWake(t.Context(), claimed)
	require.NoError(t, err)
	require.True(t, found)
	assert.LessOrEqual(t, wait, -2*time.Second, "a row that fell due after the claim does not make the worker claim again at once")
	assert.Greater(t, wait, -3*time.Second, "the worker would wake for another row than the one due since the claim")

	_, err = testPool.Pool().Exec(t.Context(), `DELETE FROM webhook_delivery WHERE id = 'whd_during'`)
	require.NoError(t, err)
	wait, found, err = m.store.nextWake(t.Context(), claimed)
	require.NoError(t, err)
	require.True(t, found)
	assert.LessOrEqual(t, wait, 37*time.Second)
	assert.Greater(t, wait, 36*time.Second, "the worker would wake for another row than the one due in 37s")

	_, err = testPool.Pool().Exec(t.Context(), `DELETE FROM webhook_delivery WHERE id IN ('whd_soon', 'whd_later')`)
	require.NoError(t, err)
	_, found, err = m.store.nextWake(t.Context(), claimed)
	require.NoError(t, err)
	assert.False(t, found, "nothing pending is due after the claim, and the worker sleeps its ceiling")
}

// TestACappedReceiverGetsItsCapPerClockMinuteWhoeverClaims is ADR 0436's
// amendment of ADR 0275: the cap counts what every claim leased in the clock
// minute, so a second claim in the same minute gets nothing and the first in
// the next minute gets the cap again, while an uncapped receiver is leased all
// it has.
func TestACappedReceiverGetsItsCapPerClockMinuteWhoeverClaims(t *testing.T) {
	m := freshModule(t)
	capped := cappedReceiver(t, m, 2)
	free := register(t, m, "https://free.test/hook", topicOrderPlaced)
	for i := range 5 {
		queueRow(t, "whd_capped_"+string(rune('a'+i)), capped.ID, -time.Second, "pending", i)
		queueRow(t, "whd_free_"+string(rune('a'+i)), free.ID, -time.Second, "pending", 10+i)
	}

	minute := futureMinute(t)
	at := func(seconds int) *time.Time {
		instant := minute.Add(time.Duration(seconds) * time.Second)
		return &instant
	}

	first, _, err := m.store.claimDueAt(t.Context(), deliveryLimit, at(30))
	require.NoError(t, err)
	assert.Equal(t, map[string]int{capped.ID: 2, free.ID: 5}, claimedBy(first))
	var oldest []string
	for _, d := range first {
		if d.EndpointID == capped.ID {
			oldest = append(oldest, d.ID)
		}
	}
	assert.ElementsMatch(t, []string{"whd_capped_a", "whd_capped_b"}, oldest, "the oldest first")

	again, _, err := m.store.claimDueAt(t.Context(), deliveryLimit, at(50))
	require.NoError(t, err)
	assert.Empty(t, again, "a second claim in the same minute was leased past the cap")

	next, _, err := m.store.claimDueAt(t.Context(), deliveryLimit, at(61))
	require.NoError(t, err)
	assert.Equal(t, map[string]int{capped.ID: 2}, claimedBy(next), "the next minute did not start the count again")

	var (
		window  time.Time
		claimed int
	)
	require.NoError(t, testPool.Pool().QueryRow(t.Context(),
		`SELECT rate_window, rate_claimed FROM webhook_endpoint WHERE id = $1`, capped.ID).Scan(&window, &claimed))
	assert.Equal(t, minute.Add(time.Minute), window.UTC())
	assert.Equal(t, 2, claimed)

	// The raw write is the witness for the schema's own refusal.
	_, err = testPool.Pool().Exec(t.Context(), `UPDATE webhook_endpoint SET rate_claimed = -1 WHERE id = $1`, capped.ID)
	require.Error(t, err, "a negative count is no count")
	assert.Contains(t, err.Error(), `"webhook_endpoint_rate_claimed_nonneg"`)
}

// TestAClaimWaitsForTheClaimBeforeIt is the claim lock: a claim that starts
// while another holds the lock reads the receiver's count only after that one
// committed, so between them they lease the cap once.
//
// The two claims want different rows, so no row lock stands between them: the
// first claims at an instant before three of the receiver's older rows fall
// due and leases the two it can see, and the second, half a second later,
// wants those three. What the lock protects is the count, and the outcome
// asserted is the count's.
func TestAClaimWaitsForTheClaimBeforeIt(t *testing.T) {
	m := freshModule(t)
	capped := cappedReceiver(t, m, 2)
	at := futureMinute(t).Add(30 * time.Second)
	later := at.Add(500 * time.Millisecond)

	tx, err := testPool.Pool().Begin(t.Context())
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(context.Background()) }()

	// Due a microsecond after the first claim's instant, and queued before its
	// rows (queueRow's are at most a thousand seconds old).
	for i, id := range []string{"whd_older_a", "whd_older_b", "whd_older_c"} {
		_, err := testPool.Pool().Exec(t.Context(), `
			INSERT INTO webhook_delivery (id, endpoint_id, url, event_id, event_name, occurred_at,
			                              next_attempt_at, created_at)
			VALUES ($1, $2, 'https://capped.test/hook', $1, 'order.placed', now(),
			        $3::timestamptz + interval '1 microsecond', now() - make_interval(secs => 2000 - $4))`,
			id, capped.ID, at, i)
		require.NoError(t, err)
	}
	queueRow(t, "whd_newer_d", capped.ID, -time.Hour, "pending", 50)
	queueRow(t, "whd_newer_e", capped.ID, -time.Hour, "pending", 51)

	_, err = tx.Exec(t.Context(), claimLockSQL, claimLockKey)
	require.NoError(t, err)
	rows, err := tx.Query(t.Context(), claimSQL, int32(deliveryLimit), claimLease.Seconds(), at)
	require.NoError(t, err)
	var first []string
	for rows.Next() {
		var d delivery
		require.NoError(t, rows.Scan(&d.ID, &d.EndpointID, &d.URL, &d.Secret, &d.EventID, &d.EventName,
			&d.OccurredAt, &d.Payload, &d.Redacted, &d.Attempts, &d.LeasedUntil))
		first = append(first, d.ID)
	}
	rows.Close()
	require.NoError(t, rows.Err())
	require.ElementsMatch(t, []string{"whd_newer_d", "whd_newer_e"}, first,
		"the fixture's first claim has to see only the two rows due at its instant")

	type outcome struct {
		claimed []delivery
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		claimed, _, claimErr := m.store.claimDueAt(context.Background(), deliveryLimit, &later)
		done <- outcome{claimed, claimErr}
	}()

	// Polled, never asserted: the second claim is waiting on a lock, or it is
	// already done, and either way the first one may now commit.
	waiting := func() bool {
		if len(done) > 0 {
			return true
		}
		var n int
		if err := testPool.Pool().QueryRow(context.Background(), `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
			return false
		}
		return n > 0
	}
	require.Eventually(t, waiting, 10*time.Second, 5*time.Millisecond)
	require.NoError(t, tx.Commit(t.Context()))

	got := <-done
	require.NoError(t, got.err)
	assert.Empty(t, got.claimed, "the second claim leased past the cap the first one had just filled")

	var claimed int
	require.NoError(t, testPool.Pool().QueryRow(t.Context(),
		`SELECT rate_claimed FROM webhook_endpoint WHERE id = $1`, capped.ID).Scan(&claimed))
	assert.Equal(t, 2, claimed, "the receiver's count is over its cap")
}

// TestAClaimStuckBehindAnotherGivesUpInFiveSeconds: a claim does not hold a
// pooled connection behind a stuck one for longer than the lock timeout.
func TestAClaimStuckBehindAnotherGivesUpInFiveSeconds(t *testing.T) {
	m := freshModule(t)

	tx, err := testPool.Pool().Begin(t.Context())
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, err = tx.Exec(t.Context(), claimLockSQL, claimLockKey)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	_, _, err = m.store.claimDue(ctx, deliveryLimit)
	require.Error(t, err)

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr, "the claim ended on its context rather than on the lock timeout")
	assert.Equal(t, "55P03", pgErr.Code, "lock_not_available is the lock timeout's answer")
}

// TestAPassThatRanOutOfBudgetHandsItsRowsBack is D274's fix: rows a pass
// claimed and did not attempt are due again when it ends, so the sentence the
// listing prints about them is true, and the next pass sends them.
func TestAPassThatRanOutOfBudgetHandsItsRowsBack(t *testing.T) {
	m := freshModule(t)

	var secret string
	rec := newReceiver(t, func() string { return secret })
	secret = register(t, m, rec.server.URL, topicOrderPlaced).Secret
	for _, id := range []string{"ord_budget_1", "ord_budget_2", "ord_budget_3"} {
		require.NoError(t, m.onEvent(t.Context(), eventbus.Event{
			ID: "order.placed:" + id, Name: topicOrderPlaced, Data: map[string]any{"order_id": id},
		}))
	}

	// A budget shorter than one attempt: every claimed row is skipped.
	short, cancel := context.WithTimeout(jobreport.WithReporter(t.Context()), perAttemptTimeout/2)
	defer cancel()
	require.NoError(t, m.deliverPass(short))
	assert.Empty(t, rec.seen())
	assert.Equal(t, "claimed 3, delivered 0, failed 0, 3 left for the next pass", jobreport.Detail(short))

	var due, attempted int
	require.NoError(t, testPool.Pool().QueryRow(t.Context(), `
		SELECT count(*) FILTER (WHERE next_attempt_at <= now()), count(*) FILTER (WHERE attempts > 0)
		FROM webhook_delivery`).Scan(&due, &attempted))
	assert.Equal(t, 3, due, "the rows the pass did not attempt wait out their lease")
	assert.Zero(t, attempted, "a row that was not attempted was counted")

	require.NoError(t, runPass(t, m))
	assert.Len(t, rec.seen(), 3, "the next pass did not send what the last one left")
}

// TestAReleaseLeavesARowAnotherClaimTook: the release compares the lease, so a
// row whose lease elapsed and that another claim took since stays that
// claim's.
func TestAReleaseLeavesARowAnotherClaimTook(t *testing.T) {
	m := freshModule(t)
	e := register(t, m, "https://receiver.test/hook", topicOrderPlaced)
	queueRow(t, "whd_taken", e.ID, -time.Second, "pending", 1)
	queueRow(t, "whd_kept", e.ID, -time.Second, "pending", 2)

	claimed, _, err := m.store.claimDue(t.Context(), deliveryLimit)
	require.NoError(t, err)
	require.Len(t, claimed, 2)

	// Another claim's lease on the first row, written after this one's ended.
	_, err = testPool.Pool().Exec(t.Context(),
		`UPDATE webhook_delivery SET next_attempt_at = now() + interval '91 seconds' WHERE id = 'whd_taken'`)
	require.NoError(t, err)

	require.NoError(t, m.store.release(t.Context(), claimed))

	dueNow := map[string]bool{}
	rows, err := testPool.Pool().Query(t.Context(), `SELECT id, next_attempt_at <= now() FROM webhook_delivery`)
	require.NoError(t, err)
	for rows.Next() {
		var (
			id  string
			due bool
		)
		require.NoError(t, rows.Scan(&id, &due))
		dueNow[id] = due
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, map[string]bool{"whd_taken": false, "whd_kept": true}, dueNow)
}

// TestARedriveWakesTheWorker: a delivery an operator redrives goes out when
// the write has committed, not at the next pass.
func TestARedriveWakesTheWorker(t *testing.T) {
	m := freshModule(t)
	e := register(t, m, "https://receiver.test/hook", topicOrderPlaced)
	queueRow(t, "whd_dead", e.ID, 0, "dead", 1)

	select {
	case <-m.wake:
	default:
	}
	answer := call(t, adminRouter(m), http.MethodPost, "/admin/v1/webhooks/deliveries/whd_dead/redrive", "")
	require.Equal(t, http.StatusOK, answer.Code, answer.Body.String())
	assert.Len(t, m.wake, 1, "the redrive did not wake the worker")

	select {
	case <-m.wake:
	default:
	}
	missing := call(t, adminRouter(m), http.MethodPost, "/admin/v1/webhooks/deliveries/whd_none/redrive", "")
	require.Equal(t, http.StatusNotFound, missing.Code)
	assert.Empty(t, m.wake, "a redrive that did nothing woke the worker")
}

// TestAClaimFromAMinuteBeforeDoesNotResetTheCount is the review's case for the
// cap: two claims whose instants straddle a minute's end that hold the lock in
// the other order. The window never moves back, so the older claim counts
// against the newer minute and the next claim in that minute finds the count,
// and the receiver is leased its cap once in the minute, not three times.
// The instants are named, as each claim would have read them.
func TestAClaimFromAMinuteBeforeDoesNotResetTheCount(t *testing.T) {
	boundary := futureMinute(t)
	at := func(d time.Duration) *time.Time { v := boundary.Add(d); return &v }

	t.Run("the older claim finds the cap filled", func(t *testing.T) {
		m := freshModule(t)
		capped := cappedReceiver(t, m, 2)
		for i := range 10 {
			queueRow(t, fmt.Sprintf("whd_rv_%02d", i), capped.ID, -time.Second, "pending", i)
		}

		b, _, err := m.store.claimDueAt(t.Context(), deliveryLimit, at(10*time.Millisecond))
		require.NoError(t, err)
		a, _, err := m.store.claimDueAt(t.Context(), deliveryLimit, at(-5*time.Millisecond))
		require.NoError(t, err)
		c, _, err := m.store.claimDueAt(t.Context(), deliveryLimit, at(500*time.Millisecond))
		require.NoError(t, err)

		assert.Equal(t, []int{2, 0, 0}, []int{len(b), len(a), len(c)},
			"B, then A from the minute before, then C, all within half a second, against a cap of 2")
	})

	t.Run("the older claim's lease counts in the newer minute", func(t *testing.T) {
		m := freshModule(t)
		capped := cappedReceiver(t, m, 3)
		for i := range 10 {
			queueRow(t, fmt.Sprintf("whd_rv_%02d", i), capped.ID, -time.Second, "pending", i)
		}

		b, _, err := m.store.claimDueAt(t.Context(), 2, at(10*time.Millisecond))
		require.NoError(t, err)
		a, _, err := m.store.claimDueAt(t.Context(), deliveryLimit, at(-5*time.Millisecond))
		require.NoError(t, err)
		c, _, err := m.store.claimDueAt(t.Context(), deliveryLimit, at(500*time.Millisecond))
		require.NoError(t, err)

		assert.Equal(t, []int{2, 1, 0}, []int{len(b), len(a), len(c)},
			"B took two of a cap of 3, A from the minute before the last one, and C nothing")
		var window time.Time
		require.NoError(t, testPool.Pool().QueryRow(t.Context(),
			`SELECT rate_window FROM webhook_endpoint WHERE id = $1`, capped.ID).Scan(&window))
		assert.Equal(t, boundary, window.UTC(), "the window moved back a minute")
	})
}

// TestAClaimsInstantIsReadOnceItHoldsTheLock: a claim that waited for the
// lock measures its lease, what is due and its minute from the moment it got
// the lock, not from its BEGIN before the wait (the review measured 138 us
// after BEGIN with a two-second wait).
func TestAClaimsInstantIsReadOnceItHoldsTheLock(t *testing.T) {
	m := freshModule(t)
	e := register(t, m, "https://receiver.test/hook", topicOrderPlaced)
	queueRow(t, "whd_rv_wait", e.ID, -time.Second, "pending", 1)

	hold, err := testPool.Pool().Begin(t.Context())
	require.NoError(t, err)
	defer func() { _ = hold.Rollback(context.Background()) }()
	_, err = hold.Exec(t.Context(), claimLockSQL, claimLockKey)
	require.NoError(t, err)

	type outcome struct {
		claimed []delivery
		instant time.Time
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		claimed, instant, claimErr := m.store.claimDue(context.Background(), deliveryLimit)
		done <- outcome{claimed, instant, claimErr}
	}()

	// Polled, never asserted: the claim is waiting for the lock.
	waiting := func() bool {
		var n int
		if err := testPool.Pool().QueryRow(context.Background(), `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
			return false
		}
		return n > 0
	}
	require.Eventually(t, waiting, 10*time.Second, 5*time.Millisecond)

	// Falls due while the claim waits, after its BEGIN.
	_, err = testPool.Pool().Exec(t.Context(), `
		INSERT INTO webhook_delivery (id, endpoint_id, url, event_id, event_name, occurred_at, next_attempt_at)
		VALUES ('whd_rv_during', $1, 'https://receiver.test/hook', 'whd_rv_during', 'order.placed', now(),
		        clock_timestamp())`, e.ID)
	require.NoError(t, err)

	var released time.Time
	require.NoError(t, hold.QueryRow(t.Context(), `SELECT clock_timestamp()`).Scan(&released))
	require.NoError(t, hold.Commit(t.Context()))

	got := <-done
	require.NoError(t, got.err)
	require.Len(t, got.claimed, 2, "a row that fell due while the claim waited for the lock was not claimed")
	assert.False(t, got.instant.Before(released), "the claim's instant is from before the lock was released")
	for i := range got.claimed {
		assert.Equal(t, got.instant.Add(claimLease), got.claimed[i].LeasedUntil,
			"the lease is not measured from the claim's instant")
	}
}

// TestTwoClaimsAtOnceNeverShareARow is D275 under real concurrency: two
// claims started together over a large due set, two hundred times, never
// lease the same row (the shipped statement leased the same hundred in 8 of
// 200 rounds).
func TestTwoClaimsAtOnceNeverShareARow(t *testing.T) {
	m := freshModule(t)
	e := register(t, m, "https://receiver.test/hook", topicOrderPlaced)
	_, err := testPool.Pool().Exec(t.Context(), `
		INSERT INTO webhook_delivery (id, endpoint_id, url, event_id, event_name, occurred_at, next_attempt_at, created_at)
		SELECT 'whd_bulk_' || g, $1, 'https://receiver.test/hook', 'evt_' || g, 'order.placed', now(),
		       now() - interval '1 second', now() - make_interval(secs => 5000 - g)
		FROM generate_series(1, 3000) g`, e.ID)
	require.NoError(t, err)

	rounds, shared := 200, 0
	for round := range rounds {
		_, err := testPool.Pool().Exec(t.Context(),
			`UPDATE webhook_delivery SET next_attempt_at = now() - interval '1 second'`)
		require.NoError(t, err)

		var (
			wg     sync.WaitGroup
			a, b   []delivery
			ea, eb error
		)
		gate := make(chan struct{})
		wg.Go(func() {
			<-gate
			a, _, ea = m.store.claimDue(context.Background(), deliveryLimit)
		})
		wg.Go(func() {
			<-gate
			// A spread of starts, the second up to 1.4 ms after the first, so
			// that some rounds meet inside each other's statement.
			time.Sleep(time.Duration(round%8) * 200 * time.Microsecond)
			b, _, eb = m.store.claimDue(context.Background(), deliveryLimit)
		})
		close(gate)
		wg.Wait()
		require.NoError(t, ea)
		require.NoError(t, eb)

		seen := make(map[string]bool, len(a))
		for i := range a {
			seen[a[i].ID] = true
		}
		for i := range b {
			if seen[b[i].ID] {
				shared++

				break
			}
		}
	}
	assert.Zero(t, shared, "%d of %d rounds leased a row to two claims", shared, rounds)
}

// TestAClaimCountsInTheMinuteOfItsInstant: the minute a cap counts in is the
// claim's instant's, not the transaction's BEGIN. Two claims a fifth of a
// second apart across a minute's start each get the whole cap; counted from
// the BEGIN, which both share with the wall clock, the second got nothing.
func TestAClaimCountsInTheMinuteOfItsInstant(t *testing.T) {
	m := freshModule(t)
	capped := cappedReceiver(t, m, 2)
	for i := range 10 {
		queueRow(t, fmt.Sprintf("whd_mi_%02d", i), capped.ID, -time.Second, "pending", i)
	}
	boundary := futureMinute(t)
	before, after := boundary.Add(-100*time.Millisecond), boundary.Add(100*time.Millisecond)

	first, _, err := m.store.claimDueAt(t.Context(), deliveryLimit, &before)
	require.NoError(t, err)
	second, _, err := m.store.claimDueAt(t.Context(), deliveryLimit, &after)
	require.NoError(t, err)

	assert.Equal(t, []int{2, 2}, []int{len(first), len(second)},
		"a claim at the start of a minute did not get the cap of that minute")
	var window time.Time
	require.NoError(t, testPool.Pool().QueryRow(t.Context(),
		`SELECT rate_window FROM webhook_endpoint WHERE id = $1`, capped.ID).Scan(&window))
	assert.Equal(t, boundary, window.UTC())
}

// queueAged writes one pending delivery for a receiver, due now, with an age
// in seconds and a count of attempts already made.
func queueAged(t *testing.T, id string, e endpoint, ageSeconds, attempts int) {
	t.Helper()

	_, err := testPool.Pool().Exec(t.Context(), `
		INSERT INTO webhook_delivery (id, endpoint_id, url, event_id, event_name, occurred_at,
		                              next_attempt_at, created_at, attempts)
		VALUES ($1, $2, $3, $1, 'order.placed', now(), now() - interval '1 second',
		        now() - make_interval(secs => $4), $5)`,
		id, e.ID, e.URL, ageSeconds, attempts)
	require.NoError(t, err)
}

// TestReceiversTakeTurnsInAClaim is the claim's fairness (ADR 0436): a fresh
// delivery to one receiver is claimed, and comes first, beside another
// receiver's older backlog, and a delivery never attempted comes before the
// retries of other receivers in its turn. Oldest first across receivers, the
// review's form, claimed a hundred of the backlog and not the fresh one.
func TestReceiversTakeTurnsInAClaim(t *testing.T) {
	t.Run("a backlog takes its own turns", func(t *testing.T) {
		m := freshModule(t)
		x := register(t, m, "https://hanging.test/hook", topicOrderPlaced)
		y := register(t, m, "https://healthy.test/hook", topicOrderPlaced)
		for i := 1; i <= 150; i++ {
			queueAged(t, fmt.Sprintf("whd_x_%03d", i), x, 10000-i, 1)
		}
		queueAged(t, "whd_y", y, 0, 0)

		claimed, _, err := m.store.claimDue(t.Context(), deliveryLimit)
		require.NoError(t, err)
		require.Len(t, claimed, deliveryLimit)
		assert.Equal(t, map[string]int{x.ID: deliveryLimit - 1, y.ID: 1}, claimedBy(claimed))
		assert.Equal(t, []string{"whd_y", "whd_x_001", "whd_x_002"},
			[]string{claimed[0].ID, claimed[1].ID, claimed[2].ID},
			"the turns: the fresh delivery, then the backlog's oldest, in that order")
	})

	t.Run("a fresh delivery goes before others' retries in its turn", func(t *testing.T) {
		m := freshModule(t)
		y := register(t, m, "https://healthy.test/hook", topicOrderPlaced)
		for i := range concurrency + 1 {
			x := register(t, m, fmt.Sprintf("https://hanging-%d.test/hook", i), topicOrderPlaced)
			queueAged(t, fmt.Sprintf("whd_x%d", i), x, 1000+i, 3)
		}
		queueAged(t, "whd_y", y, 0, 0)

		claimed, _, err := m.store.claimDue(t.Context(), deliveryLimit)
		require.NoError(t, err)
		require.Len(t, claimed, concurrency+2)
		assert.Equal(t, "whd_y", claimed[0].ID,
			"the fresh delivery waits behind %d receivers' retries, past the first %d attempts", concurrency+1, concurrency)

		// And a claim whose limit cuts the turn keeps the fresh delivery in it.
		_, err = testPool.Pool().Exec(t.Context(), `UPDATE webhook_delivery SET next_attempt_at = now() - interval '1 second'`)
		require.NoError(t, err)
		cut, _, err := m.store.claimDueAt(t.Context(), concurrency, nil)
		require.NoError(t, err)
		require.Len(t, cut, concurrency)
		assert.Contains(t, ids(cut), "whd_y", "a claim cut at %d left the fresh delivery out of its turn", concurrency)
	})

	t.Run("the rows come back turn by turn", func(t *testing.T) {
		m := freshModule(t)
		y := register(t, m, "https://healthy.test/hook", topicOrderPlaced)
		const receivers, each = 40, 3
		for i := range receivers {
			x := register(t, m, fmt.Sprintf("https://hanging-%d.test/hook", i), topicOrderPlaced)
			for j := range each {
				queueAged(t, fmt.Sprintf("whd_x%02d_%d", i, j), x, 5000-i*10-j, 3)
			}
		}
		queueAged(t, "whd_y", y, 0, 0)
		// With the tables analyzed, as a running installation's are, the
		// claim's plan returns its rows grouped by receiver unless it orders
		// them.
		_, err := testPool.Pool().Exec(t.Context(), `ANALYZE webhook_endpoint; ANALYZE webhook_delivery`)
		require.NoError(t, err)

		claimed, _, err := m.store.claimDue(t.Context(), deliveryLimit)
		require.NoError(t, err)
		require.Len(t, claimed, deliveryLimit)
		assert.Equal(t, "whd_y", claimed[0].ID)
		first := map[string]bool{}
		for i := range receivers + 1 {
			first[claimed[i].EndpointID] = true
		}
		assert.Len(t, first, receivers+1,
			"the first %d rows the claim returned are not one from each receiver: %v", receivers+1, ids(claimed[:receivers+1]))
		assert.Equal(t, "whd_x00_0", claimed[1].ID, "a turn is not in age order")
	})
}

// ids lists a claim's delivery ids in order.
func ids(claimed []delivery) []string {
	out := make([]string, len(claimed))
	for i := range claimed {
		out[i] = claimed[i].ID
	}

	return out
}

// TestAHangingReceiverDoesNotHoldAnotherBack is the review's starvation on the
// real store, the real sender and the worker: a receiver whose every request
// hangs to the attempt timeout has 120 due deliveries, and a fresh one to a
// healthy receiver still arrives at once. Oldest first, it waited for minutes.
func TestAHangingReceiverDoesNotHoldAnotherBack(t *testing.T) {
	m := freshModule(t)
	release := make(chan struct{})
	hanging := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer hanging.Close()
	arrived := make(chan struct{})
	var once sync.Once
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() { close(arrived) })
		w.WriteHeader(http.StatusOK)
	}))
	defer healthy.Close()

	x := register(t, m, hanging.URL, topicOrderPlaced)
	y := register(t, m, healthy.URL, topicOrderPlaced)
	for i := 1; i <= 120; i++ {
		queueAged(t, fmt.Sprintf("whd_x_%03d", i), x, 10000-i, 1)
	}
	queueAged(t, "whd_y", y, 0, 0)

	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	started := time.Now()
	go func() {
		assert.NoError(t, m.serve(ctx))
		close(served)
	}()
	defer func() {
		cancel()
		close(release)
		<-served
	}()

	select {
	case <-arrived:
		t.Logf("the healthy receiver's delivery arrived %s after the worker started", time.Since(started))
	case <-time.After(perAttemptTimeout):
		t.Fatal("the healthy receiver's delivery did not arrive within one attempt's timeout of a hanging receiver's backlog")
	}
}
