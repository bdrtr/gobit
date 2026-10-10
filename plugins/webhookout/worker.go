package webhookout

import (
	"context"
	"time"
)

// workerName names the worker in the server's log (ADR 0436). It is prefixed
// with the plugin's name, as [jobName] is, because the namespace is every
// plugin's.
const workerName = "webhookout-send"

// releaseTimeout bounds the write that hands unattempted rows back. It runs
// on a context detached from the pass's, which may be done by then.
const releaseTimeout = 5 * time.Second

// deliveryQueue is the store as the two passes use it: the worker's and the
// minute job's. [store] is the real one; a unit test drives a pass with a fake
// and a fake clock.
type deliveryQueue interface {
	enqueue(
		ctx context.Context, eventID, eventName string, occurredAt time.Time,
		payload map[string]any, redacted []string,
	) (int64, error)
	claimDue(ctx context.Context, limit int32) ([]delivery, time.Time, error)
	markDelivered(ctx context.Context, id string, status int) error
	markFailed(ctx context.Context, id, message string, status int, delay time.Duration) (bool, error)
	release(ctx context.Context, rows []delivery) error
	nextWake(ctx context.Context, claimed time.Time) (time.Duration, bool, error)
	orphanCount(ctx context.Context) (int64, error)
	deadLetters(ctx context.Context, limit int32) (deadLetterReport, error)
}

// The store is the queue at compile time.
var _ deliveryQueue = (*store)(nil)

// serve is the worker: its first claim after a delivery is queued or
// redriven, or after a failed one's step of the ladder ends, attempts it — at
// once when the worker is idle, or when its pass in flight ends (ADR 0436).
//
// # Why a worker and not a shorter job
//
// The job runner ticks once a minute for every job, so the minute job was the
// only sender and a delivery waited for the next tick: up to a minute for the
// first attempt, and for every retry a tick past its step, since the backoff
// is timed from the failure and the next claim came exactly one tick after the
// one that made it (D273). An interval cannot be finer than the tick, and a
// tick a few seconds long would multiply every job's election by twelve.
//
// # The loop
//
// It claims what is due and sends it, then sleeps until the first pending row
// due after that claim falls due by the database's clock, or [every] at the
// longest, or until it is woken: [webhookModule.onEvent] and the redrive wake
// it after their write has committed, so the claim that follows sees the row.
// Wakes that arrive during a pass fold into one claim after it. A batch that
// came back full, a pass that ran out of budget, and a row that fell due while
// the pass was sending all make it claim again at once, so a step of the
// ladder that ends during a pass is sent when that pass ends. Every claim
// takes the claim lock, so the workers of N processes and the minute job never
// lease one row twice; any of them may send it.
//
// It returns only when ctx is done. A failed claim is logged and tried again
// after [every]; the minute job is still behind it.
func (m *webhookModule) serve(ctx context.Context) error {
	if m.queue == nil {
		m.log.WarnContext(ctx, "the webhookout module was not registered; its worker sends nothing")
		<-ctx.Done()

		return nil
	}

	for {
		claimed, again, ok := m.sendDue(ctx)
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if again {
			continue
		}

		// A wait that is not positive fires at once: a row fell due while the
		// pass was sending.
		wait := every
		if ok {
			wait = m.untilNextDue(ctx, claimed)
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()

			return nil
		case <-m.wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}

// sendDue is one pass of the worker: claim, send, release what was not
// attempted. It returns the claim's instant, whether more is due already, and
// whether the claim was made at all.
//
// The attempts run under a context detached from ctx with [maxRun] as its
// deadline, and ctx only stops the feed: an attempt in flight at shutdown
// finishes and writes its outcome (see [deliverAll]).
func (m *webhookModule) sendDue(ctx context.Context) (claimedAt time.Time, again, ok bool) {
	claimed, claimedAt, err := m.queue.claimDue(ctx, deliveryLimit)
	if err != nil {
		if ctx.Err() == nil {
			m.log.WarnContext(ctx, "the webhook worker could not claim what is due; it tries again "+
				"within a minute, and the delivery job is behind it", "error", err)
		}

		return time.Time{}, false, false
	}
	if len(claimed) == 0 {
		return claimedAt, false, true
	}

	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), maxRun)
	defer cancel()

	result, left := deliverAll(sendCtx, ctx.Done(), m.slots, m.post, m.queue, m.log, claimed)
	m.release(ctx, left)

	m.logDeadLetters(ctx, result)
	// DEBUG: a line per event at INFO would be the loudest thing in the log of
	// a busy shop. The failures were logged one by one already.
	m.log.DebugContext(ctx, "the webhook worker sent what was due",
		"claimed", result.Claimed,
		"delivered", result.Delivered,
		"failed", result.Failed,
		"released", result.Skipped)

	return claimedAt, len(claimed) == deliveryLimit || len(left) > 0, true
}

// untilNextDue is how long the worker sleeps after a claim made at claimed:
// until the first pending row due after it falls due, never longer than
// [every], and not at all when that row is due already.
//
// The ceiling is what keeps a worker useful when its wake is lost or the row
// was written by another process: no delivery is due later than [every] after
// the last time any worker looked, because the shortest step of the ladder is
// [firstDelay], which is [every].
func (m *webhookModule) untilNextDue(ctx context.Context, claimed time.Time) time.Duration {
	wait, found, err := m.queue.nextWake(ctx, claimed)
	if err != nil {
		if ctx.Err() == nil {
			m.log.WarnContext(ctx, "the webhook worker could not read when the next delivery is due; "+
				"it looks again within a minute", "error", err)
		}

		return every
	}
	if !found || wait > every {
		return every
	}

	return wait
}

// kick wakes the worker without waiting. A wake that finds one already
// pending is dropped, because one claim after the pass in progress takes
// every row the two wakes were for.
func (m *webhookModule) kick() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// release hands back the rows a pass did not attempt (D274), on a context the
// pass's end cannot cancel. A failure leaves them to their lease, which is the
// worst it can do, and is logged.
func (m *webhookModule) release(ctx context.Context, left []delivery) {
	if len(left) == 0 {
		return
	}

	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()

	if err := m.queue.release(releaseCtx, left); err != nil {
		m.log.WarnContext(ctx, "the webhook deliveries a pass did not attempt could not be released; "+
			"they come back when their lease ends", "deliveries", len(left), "error", err)
	}
}
