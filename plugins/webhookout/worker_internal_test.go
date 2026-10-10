package webhookout

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/eventbus"
)

// The worker's timing is tested on a held clock (ADR 0436): every test here
// runs in a testing/synctest bubble, where time moves only when every
// goroutine in the bubble is blocked, so "the attempt left at the instant the
// row was queued" and "the retry left exactly one minute after the failure"
// are equalities rather than tolerances. The database and the network cannot
// enter a bubble, so the queue and the receiver are fakes with the store's
// and the sender's contracts; the claim's SQL is tested against PostgreSQL in
// the integration file.

// fakeRow is one delivery in the fake queue.
type fakeRow struct {
	d         delivery
	due       time.Time
	delivered bool
	dead      bool
}

// fakeQueue is the store's contract over a slice, on the bubble's clock.
type fakeQueue struct {
	mu   sync.Mutex
	rows []*fakeRow
	// perClaim caps what one claim returns, below the limit asked; zero is the
	// limit.
	perClaim int
	// released are the ids handed back.
	released []string
	// writeErrs are the contexts' errors each outcome write was made with.
	writeErrs []error
}

var _ deliveryQueue = (*fakeQueue)(nil)

func (q *fakeQueue) enqueue(
	_ context.Context, eventID, eventName string, occurredAt time.Time, payload map[string]any, redacted []string,
) (int64, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.rows = append(q.rows, &fakeRow{
		d: delivery{
			ID: "whd_" + eventID, EndpointID: "whe_1", URL: "https://receiver.test/hook",
			EventID: eventID, EventName: eventName, OccurredAt: occurredAt, Payload: payload, Redacted: redacted,
		},
		due: time.Now(),
	})

	return 1, nil
}

// add queues n rows due now.
func (q *fakeQueue) add(n int) {
	for i := range n {
		_, _ = q.enqueue(context.Background(), fmt.Sprintf("evt_%03d", i), topicOrderPlaced, time.Now(), nil, nil)
	}
}

func (q *fakeQueue) claimDue(_ context.Context, limit int32) ([]delivery, time.Time, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.perClaim > 0 && int32(q.perClaim) < limit {
		limit = int32(q.perClaim)
	}
	// The claim's turns: each receiver's rows ranked by age (the order they
	// were added), then a row never attempted first, then age.
	now := time.Now()
	type candidate struct {
		row          *fakeRow
		place, index int
	}
	var due []candidate
	places := map[string]int{}
	for i, r := range q.rows {
		if r.delivered || r.dead || r.due.After(now) {
			continue
		}
		places[r.d.EndpointID]++
		due = append(due, candidate{row: r, place: places[r.d.EndpointID], index: i})
	}
	sort.SliceStable(due, func(i, j int) bool {
		a, b := due[i], due[j]
		if a.place != b.place {
			return a.place < b.place
		}
		if a.row.d.Attempts != b.row.d.Attempts {
			return a.row.d.Attempts < b.row.d.Attempts
		}

		return a.index < b.index
	})

	var out []delivery
	for _, c := range due {
		if int32(len(out)) == limit {
			break
		}
		c.row.due = now.Add(claimLease)
		d := c.row.d
		d.LeasedUntil = c.row.due
		out = append(out, d)
	}

	return out, now, nil
}

// addFor queues n rows due now for a receiver, ids prefixed.
func (q *fakeQueue) addFor(endpointID, prefix string, n int) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i := range n {
		id := fmt.Sprintf("%s%03d", prefix, i)
		q.rows = append(q.rows, &fakeRow{
			d: delivery{
				ID: "whd_" + id, EndpointID: endpointID, URL: "https://" + endpointID + ".test/hook",
				EventID: id, EventName: topicOrderPlaced, OccurredAt: time.Now(),
			},
			due: time.Now(),
		})
	}
}

func (q *fakeQueue) row(id string) *fakeRow {
	for _, r := range q.rows {
		if r.d.ID == id {
			return r
		}
	}

	panic("no row " + id)
}

func (q *fakeQueue) markDelivered(ctx context.Context, id string, _ int) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.writeErrs = append(q.writeErrs, ctx.Err())
	q.row(id).delivered = true

	return nil
}

func (q *fakeQueue) markFailed(ctx context.Context, id, _ string, _ int, delay time.Duration) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.writeErrs = append(q.writeErrs, ctx.Err())
	r := q.row(id)
	r.d.Attempts++
	r.due = time.Now().Add(delay)
	r.dead = r.d.Attempts >= maxAttempts

	return r.dead, nil
}

func (q *fakeQueue) release(_ context.Context, rows []delivery) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	for i := range rows {
		if r := q.row(rows[i].ID); r.due.Equal(rows[i].LeasedUntil) {
			r.due = time.Now()
			q.released = append(q.released, rows[i].ID)
		}
	}

	return nil
}

func (q *fakeQueue) nextWake(_ context.Context, claimed time.Time) (time.Duration, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()
	var earliest time.Time
	for _, r := range q.rows {
		if r.delivered || r.dead || !r.due.After(claimed) {
			continue
		}
		if earliest.IsZero() || r.due.Before(earliest) {
			earliest = r.due
		}
	}
	if earliest.IsZero() {
		return 0, false, nil
	}

	return earliest.Sub(now), true, nil
}

func (q *fakeQueue) orphanCount(context.Context) (int64, error) { return 0, nil }

func (q *fakeQueue) deadLetters(context.Context, int32) (deadLetterReport, error) {
	return deadLetterReport{}, nil
}

// delivered counts the rows closed as delivered.
func (q *fakeQueue) delivered() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	n := 0
	for _, r := range q.rows {
		if r.delivered {
			n++
		}
	}

	return n
}

// sent is one attempt as the fake receiver saw it.
type sent struct {
	id      string
	attempt int64
	at      time.Time
}

// fakePoster is the sender's contract on the bubble's clock.
type fakePoster struct {
	mu sync.Mutex
	// took is how long an attempt takes; the attempt gives up when its context
	// ends first, unless deaf is set.
	took time.Duration
	// deaf makes an attempt take its whole time whatever its context does, as
	// a request whose answer arrived just as its deadline passed.
	deaf bool
	// fails says which attempts the receiver refuses.
	fails func(d delivery) bool

	attempts              []sent
	inFlight, maxInFlight int
}

var _ poster = (*fakePoster)(nil)

func (p *fakePoster) send(ctx context.Context, d delivery) attemptResult {
	p.mu.Lock()
	p.attempts = append(p.attempts, sent{id: d.ID, attempt: d.Attempts + 1, at: time.Now()})
	p.inFlight++
	p.maxInFlight = max(p.maxInFlight, p.inFlight)
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		p.inFlight--
		p.mu.Unlock()
	}()

	if p.took > 0 && p.deaf {
		time.Sleep(p.took)
	} else if p.took > 0 {
		timer := time.NewTimer(p.took)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return attemptResult{Err: ctx.Err()}
		case <-timer.C:
		}
	}
	if p.fails != nil && p.fails(d) {
		return attemptResult{Status: 500, Err: errors.New("the receiver answered 500")}
	}

	return attemptResult{Status: 200}
}

// sends returns the attempts made so far.
func (p *fakePoster) sends() []sent {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]sent(nil), p.attempts...)
}

// of returns the attempts made for one delivery.
func (p *fakePoster) of(id string) []sent {
	var out []sent
	for _, s := range p.sends() {
		if s.id == id {
			out = append(out, s)
		}
	}

	return out
}

// testModule builds the module over the fakes. It has to be called inside the
// bubble, where its channels are made.
func testModule(q *fakeQueue, p *fakePoster) *webhookModule {
	m := newModule(nil)
	m.queue, m.post = q, p

	return m
}

// startWorker runs the worker until the test ends, and waits for it to return.
func startWorker(t *testing.T, m *webhookModule) (stop func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		assert.NoError(t, m.serve(ctx))
		close(done)
	}()

	return func() {
		cancel()
		<-done
	}
}

// placed is an order.placed event.
func placed(id string) eventbus.Event {
	return eventbus.Event{ID: id, Name: topicOrderPlaced, Data: map[string]any{"order_id": id}}
}

// TestAQueuedDeliveryLeavesBeforeTheClockMoves is the consumer's fault: a
// delivery queued while the worker sleeps is attempted at the instant it was
// queued, not at the next tick a minute away.
func TestAQueuedDeliveryLeavesBeforeTheClockMoves(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		q, p := &fakeQueue{}, &fakePoster{}
		m := testModule(q, p)
		stop := startWorker(t, m)
		defer stop()

		// The worker's first pass found nothing, and it is asleep.
		synctest.Wait()
		queued := time.Now()
		require.NoError(t, m.onEvent(t.Context(), placed("ord_1")))
		synctest.Wait()

		got := p.sends()
		require.Len(t, got, 1, "the delivery was not attempted when it was queued")
		assert.Equal(t, queued, got[0].at, "the attempt waited for the clock")
		assert.Equal(t, 1, q.delivered())
	})
}

// TestABacklogLargerThanAClaimGoesOutWithoutWaiting: a claim that comes back
// full is followed by another at once, so a burst of a hundred and fifty does
// not leave fifty for a minute later.
func TestABacklogLargerThanAClaimGoesOutWithoutWaiting(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		q, p := &fakeQueue{}, &fakePoster{}
		q.add(deliveryLimit + deliveryLimit/2)
		m := testModule(q, p)
		stop := startWorker(t, m)
		defer stop()

		synctest.Wait()

		assert.Len(t, p.sends(), deliveryLimit+deliveryLimit/2, "a full claim was not followed by another")
		assert.Equal(t, deliveryLimit+deliveryLimit/2, q.delivered())
	})
}

// TestARetryLeavesWhenItsStepEnds is D273's fix: a failed delivery is tried
// again exactly when its step of the ladder ends, measured from the failure,
// even when the worker woke for something else in between.
func TestARetryLeavesWhenItsStepEnds(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		q := &fakeQueue{}
		p := &fakePoster{fails: func(d delivery) bool { return d.ID == "whd_ord_a" && d.Attempts == 0 }}
		m := testModule(q, p)
		stop := startWorker(t, m)
		defer stop()

		synctest.Wait()
		failed := time.Now()
		require.NoError(t, m.onEvent(t.Context(), placed("ord_a")))
		synctest.Wait()
		require.Len(t, p.of("whd_ord_a"), 1)

		// Another delivery wakes the worker twenty seconds into the step.
		time.Sleep(20 * time.Second)
		require.NoError(t, m.onEvent(t.Context(), placed("ord_b")))
		synctest.Wait()
		require.Len(t, p.of("whd_ord_b"), 1)

		time.Sleep(firstDelay - 20*time.Second - time.Millisecond)
		synctest.Wait()
		assert.Len(t, p.of("whd_ord_a"), 1, "the retry left before its step ended")

		time.Sleep(time.Millisecond)
		synctest.Wait()
		retries := p.of("whd_ord_a")
		require.Len(t, retries, 2, "the retry did not leave when its step ended")
		assert.Equal(t, int64(2), retries[1].attempt)
		assert.Equal(t, firstDelay, retries[1].at.Sub(failed),
			"the retry left %s after the failure; the ladder's first step is %s", retries[1].at.Sub(failed), firstDelay)
	})
}

// TestShutdownLetsTheAttemptsInFlightFinish: a worker told to stop starts
// nothing more, lets the attempts in flight finish and write their outcome on
// a live context, hands back what it did not attempt, and only then returns.
func TestShutdownLetsTheAttemptsInFlightFinish(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		q, p := &fakeQueue{}, &fakePoster{took: 5 * time.Second}
		q.add(concurrency + 2)
		m := testModule(q, p)
		start := time.Now()
		stop := startWorker(t, m)

		time.Sleep(time.Second)
		stop()

		assert.Equal(t, 5*time.Second, time.Since(start), "the worker returned before its attempts finished")
		assert.Len(t, p.sends(), concurrency, "an attempt was started after the worker was told to stop")
		assert.Equal(t, concurrency, q.delivered(), "an attempt in flight was cut off by the shutdown")
		for _, err := range q.writeErrs {
			assert.NoError(t, err, "an outcome was written on a context the shutdown canceled")
		}
		assert.Equal(t, []string{"whd_evt_008", "whd_evt_009"}, q.released,
			"the rows the worker did not attempt were not handed back")
	})
}

// TestOneProcessKeepsEightInFlight: the worker's pass and the job's share the
// process's eight, so the two at once never send nine.
func TestOneProcessKeepsEightInFlight(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		q, p := &fakeQueue{perClaim: concurrency}, &fakePoster{took: time.Second}
		q.add(2 * concurrency)
		m := testModule(q, p)
		stop := startWorker(t, m)
		defer stop()

		// The worker's eight are in flight; the job's pass claims the other eight.
		synctest.Wait()
		ctx, cancel := context.WithTimeout(t.Context(), maxRun)
		defer cancel()
		require.NoError(t, m.deliverPass(ctx))

		assert.Equal(t, 2*concurrency, q.delivered())
		assert.Equal(t, concurrency, p.maxInFlight, "one process had more than %d attempts in flight", concurrency)
	})
}

// TestAPassDoesNotStartWhatItsBudgetCannotFinish: a pass that waited for a
// slot while the other pass held all eight reads its budget again, and hands
// back a row it no longer has time to attempt rather than starting a request
// its deadline would cut off and record as a failure.
func TestAPassDoesNotStartWhatItsBudgetCannotFinish(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		q, p := &fakeQueue{}, &fakePoster{took: perAttemptTimeout}
		q.add(1)
		m := testModule(q, p)

		// The other pass holds every slot for longer than this one's budget
		// leaves an attempt.
		for range concurrency {
			m.slots <- struct{}{}
		}
		go func() {
			time.Sleep(maxRun - perAttemptTimeout/2)
			for range concurrency {
				<-m.slots
			}
		}()

		ctx, cancel := context.WithTimeout(t.Context(), maxRun)
		defer cancel()
		require.NoError(t, m.deliverPass(ctx))

		assert.Empty(t, p.sends(), "the pass started an attempt its budget could not finish")
		assert.Empty(t, q.writeErrs, "an attempt's outcome was written")
		assert.Equal(t, []string{"whd_evt_000"}, q.released)
	})
}

// TestAWorkerToldToStopDoesNotWaitForASlot: a worker whose feed is waiting
// for one of the process's eight, held by the job's pass, returns the moment
// it is told to stop and hands its rows back, rather than holding the shutdown
// until the other pass lets a slot go.
func TestAWorkerToldToStopDoesNotWaitForASlot(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		q, p := &fakeQueue{}, &fakePoster{}
		q.add(1)
		m := testModule(q, p)
		for range concurrency {
			m.slots <- struct{}{}
		}

		start := time.Now()
		stop := startWorker(t, m)
		synctest.Wait()
		stop()

		assert.Zero(t, time.Since(start), "the worker waited for a slot after it was told to stop")
		assert.Empty(t, p.sends())
		assert.Equal(t, []string{"whd_evt_000"}, q.released)
		for range concurrency {
			<-m.slots
		}
	})
}

// TestARetryDueDuringAnotherPassLeavesWhenThatPassEnds is the review's case
// for D273: the step of a failed delivery ends while the worker is sending
// another one. The claim that began that pass could not take it, so the wake
// is measured from that claim, and the retry leaves the moment the pass ends,
// not a minute later.
func TestARetryDueDuringAnotherPassLeavesWhenThatPassEnds(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		q := &fakeQueue{}
		p := &fakePoster{
			took:  time.Second,
			fails: func(d delivery) bool { return d.ID == "whd_ord_a" && d.Attempts == 0 },
		}
		m := testModule(q, p)
		stop := startWorker(t, m)
		defer stop()

		synctest.Wait()
		require.NoError(t, m.onEvent(t.Context(), placed("ord_a")))
		// The first attempt takes a second and fails; its step ends a minute
		// after the failure.
		time.Sleep(time.Second)
		synctest.Wait()
		require.Len(t, p.of("whd_ord_a"), 1)
		failed := time.Now()

		// Half a second before the step ends another delivery is queued; its
		// attempt takes a second, so the step ends in the middle of the pass.
		time.Sleep(firstDelay - 500*time.Millisecond)
		require.NoError(t, m.onEvent(t.Context(), placed("ord_b")))
		time.Sleep(time.Second)
		synctest.Wait()

		retries := p.of("whd_ord_a")
		require.Len(t, retries, 2, "the retry did not leave when the pass it fell due in ended")
		assert.Equal(t, firstDelay+500*time.Millisecond, retries[1].at.Sub(failed),
			"the retry left %s after the failure; its step ended 500ms before the pass did",
			retries[1].at.Sub(failed))
	})
}

// TestAPassWithNoBudgetLeftDoesNotWaitForASlot: a pass whose budget cannot
// hold an attempt while every slot is held hands its rows back at once rather
// than holding them until its deadline (the review's killing test for the
// mutant first reported as equivalent).
func TestAPassWithNoBudgetLeftDoesNotWaitForASlot(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		q, p := &fakeQueue{}, &fakePoster{}
		q.add(1)
		m := testModule(q, p)
		for range concurrency {
			m.slots <- struct{}{}
		}

		ctx, cancel := context.WithTimeout(t.Context(), perAttemptTimeout/2)
		defer cancel()
		start := time.Now()
		require.NoError(t, m.deliverPass(ctx))

		assert.Zero(t, time.Since(start), "the pass waited for a slot its budget could not use")
		assert.Equal(t, []string{"whd_evt_000"}, q.released)
		for range concurrency {
			<-m.slots
		}
	})
}

// TestAnAnswerAtTheBudgetsEndIsRecorded: the outcome of an attempt whose
// answer arrives a millisecond after its pass's budget ran out is written on a
// context of its own, so a 2xx the receiver sent is recorded rather than sent
// again after the lease. A millisecond and not the same instant: at the same
// instant the bubble may run the deadline's timer either side of the answer
// (the review measured the mutant killed in 17 runs of 50).
func TestAnAnswerAtTheBudgetsEndIsRecorded(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		q, p := &fakeQueue{}, &fakePoster{took: maxRun + time.Millisecond, deaf: true}
		q.add(1)
		m := testModule(q, p)

		ctx, cancel := context.WithTimeout(t.Context(), maxRun)
		defer cancel()
		require.NoError(t, m.deliverPass(ctx))

		require.Len(t, q.writeErrs, 1)
		assert.NoError(t, q.writeErrs[0], "the outcome was written on the pass's spent budget")
		assert.Equal(t, 1, q.delivered())
	})
}

// TestTheJobsAttemptsInFlightFinishAtShutdown: the job's pass, canceled by
// the runner at shutdown, lets the attempt in flight finish and records it, as
// the worker does.
func TestTheJobsAttemptsInFlightFinishAtShutdown(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		q, p := &fakeQueue{}, &fakePoster{took: 5 * time.Second}
		q.add(1)
		m := testModule(q, p)

		ctx, cancel := context.WithTimeout(t.Context(), maxRun)
		defer cancel()
		go func() {
			time.Sleep(time.Second)
			cancel()
		}()
		start := time.Now()
		require.NoError(t, m.deliverPass(ctx))

		assert.Equal(t, 5*time.Second, time.Since(start), "the pass did not wait for its attempt")
		assert.Equal(t, 1, q.delivered(), "the attempt in flight was cut off by the shutdown")
	})
}

// downQueue is a database whose claims fail; with wakes set, it still answers
// when the next delivery is due.
type downQueue struct {
	fakeQueue

	mu     sync.Mutex
	claims int
	wakes  bool
}

func (q *downQueue) claimDue(context.Context, int32) ([]delivery, time.Time, error) {
	q.mu.Lock()
	q.claims++
	q.mu.Unlock()

	return nil, time.Time{}, errors.New("connection refused")
}

func (q *downQueue) nextWake(ctx context.Context, claimed time.Time) (time.Duration, bool, error) {
	if q.wakes {
		return q.fakeQueue.nextWake(ctx, claimed)
	}

	return 0, false, errors.New("connection refused")
}

// TestAWorkerOverADownDatabaseDoesNotSpin: a worker whose claims fail tries
// again once a minute, not in a loop — also when the wake time can still be
// read and a delivery is overdue, since a failed claim has no instant to
// measure the wake from.
func TestAWorkerOverADownDatabaseDoesNotSpin(t *testing.T) {
	t.Parallel()

	for _, wakes := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			q := &downQueue{wakes: wakes}
			q.add(1)
			m := newModule(nil)
			m.queue, m.post = q, &fakePoster{}
			stop := startWorker(t, m)

			time.Sleep(10*time.Minute + time.Second)
			synctest.Wait()
			stop()

			q.mu.Lock()
			defer q.mu.Unlock()
			assert.Equal(t, 11, q.claims,
				"ten minutes of failing claims (wake readable: %t), one at the start and one a minute", wakes)
		})
	}
}

// hangingPoster is a receiver per row prefix: whd_x rows hang to the attempt
// timeout and fail, every other row is accepted at once.
type hangingPoster struct {
	mu       sync.Mutex
	attempts []sent
}

func (p *hangingPoster) send(_ context.Context, d delivery) attemptResult {
	p.mu.Lock()
	p.attempts = append(p.attempts, sent{id: d.ID, attempt: d.Attempts + 1, at: time.Now()})
	p.mu.Unlock()

	if strings.HasPrefix(d.ID, "whd_x") {
		time.Sleep(perAttemptTimeout)

		return attemptResult{Err: errors.New("the receiver did not answer in time")}
	}

	return attemptResult{Status: 200}
}

func (p *hangingPoster) first(id string) (time.Time, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, s := range p.attempts {
		if s.id == id {
			return s.at, true
		}
	}

	return time.Time{}, false
}

// TestAHangingReceiversBacklogDoesNotHoldBackAFreshDelivery is the review's
// starvation, on a held clock: a receiver whose every request hangs to the
// attempt timeout has a due backlog of 40, 100 or 200, and a delivery to
// another receiver queued while the worker's pass is sending waits only for
// that pass, at most [maxRun]. Oldest first across receivers it waited 49 s,
// 3m39s and 12m29s.
func TestAHangingReceiversBacklogDoesNotHoldBackAFreshDelivery(t *testing.T) {
	t.Parallel()

	for _, backlog := range []int{40, 100, 200} {
		synctest.Test(t, func(t *testing.T) {
			q, p := &fakeQueue{}, &hangingPoster{}
			q.addFor("whe_hanging", "x", backlog)
			m := newModule(nil)
			m.queue, m.post = q, p
			stop := startWorker(t, m)
			defer stop()

			time.Sleep(time.Second)
			queued := time.Now()
			require.NoError(t, m.onEvent(t.Context(), placed("y")))
			time.Sleep(maxRun)
			synctest.Wait()

			at, sent := p.first("whd_y")
			require.True(t, sent, "behind a backlog of %d of a hanging receiver, the fresh delivery was not "+
				"attempted within %s", backlog, maxRun)
			assert.LessOrEqual(t, at.Sub(queued), maxRun)
			t.Logf("behind a backlog of %d of a hanging receiver, the fresh delivery waited %s", backlog, at.Sub(queued))
		})
	}
}
