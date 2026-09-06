package webhookout

import "time"

// The retry ladder, and why it is not the outbox's.
//
// # What was reused, and what could not be
//
// The SHAPE is the outbox's, deliberately: a doubling delay from a first delay,
// capped, with a ceiling on attempts and a dead letter for what the ceiling
// stops (core/eventbus/outbox.Policy). Nothing here invents a second idea of
// what retrying means, and the two failure modes it stands between are the same
// two — retrying forever, and dropping silently.
//
// The CODE could not be reused, and that is a fact about the package rather
// than a preference: [github.com/bdrtr/gobit/core/eventbus/outbox.Policy] is
// exported but the arithmetic that turns an attempt count into a delay is a
// method on it that is not — so an outside caller can hold a Policy and cannot
// ask it anything. Reuse would mean widening a published surface for a consumer
// in this repository, which is the trade ADR 0026 refuses.
//
// # The numbers ARE different, and the difference is the whole point
//
// The outbox's ladder is 1, 2, 4, 8, 16, 32, 60, 60, 60 minutes and then the
// dead letter: ten attempts, four hours and three minutes. That is measured
// against what it delivers to — the LOCAL event bus. Four hours is longer than
// any in-process publish failure that is not a bug.
//
// A webhook delivers to a third party that can be down for a working day, and
// four hours is inside a single night. The ladder below spans more than
// twenty-six hours over thirteen attempts, so a receiver that broke on Friday
// evening is still owed its delivery on Saturday night, and a receiver that has
// genuinely gone away is declared dead within a day and a bit rather than
// retried into the next week.
//
// # No jitter, for the outbox's reason, checked against this case
//
// Jitter earns its place when many independent clients would retry in step.
// This is one scheduled job, elected by an advisory lock, so exactly one process
// retries; a pass spreads its own rows across the batch by making the requests.
// What is NEW here and was worth checking is that many deliveries can share one
// receiver: an outage that fails a thousand deliveries at once makes them all
// due at the same minute afterwards. That is bounded by the per-pass limit
// rather than by jitter — a pass takes [deliveryLimit] rows and no more — and
// bounding it by the limit is better, because it is a bound the operator can
// see in the job's report rather than a random spread they cannot.
const (
	// firstDelay is how long the first failure waits.
	//
	// One minute, and it is the same value as the outbox's for the same
	// reason: the job runs once a minute, so any shorter delay would be
	// rounded up to the next pass anyway, and a policy whose smallest step the
	// scheduler cannot express documents a schedule the installation does not
	// follow.
	firstDelay = time.Minute

	// maxDelay caps the doubling.
	//
	// Six hours. It is what makes the ladder reach past a day without needing
	// twenty attempts: from the ninth failure on, every wait is this one. An
	// uncapped doubling would reach four days by attempt thirteen, and a
	// delivery nobody retried for four days is a dead letter that was never
	// declared one.
	maxDelay = 6 * time.Hour

	// maxAttempts is how many failed attempts a delivery is allowed before it
	// is dead-lettered.
	//
	// It counts ATTEMPTS, not retries, so 1 would mean "one try and no retry".
	// Thirteen produces the schedule in [deliverySchedule]: twenty-six hours
	// and thirty-one minutes of trying.
	//
	// It is a ceiling on tries rather than on elapsed time, and the outbox's
	// reason carries over unchanged: a process that was down for a day has made
	// no attempts in it, and killing a row for time it was never given would
	// dead-letter the whole backlog the moment the sender came back.
	maxAttempts int64 = 13
)

// maxShift is where the doubling stops being arithmetic.
//
// A time.Duration is an int64 of nanoseconds, so shifting far enough overflows
// into a NEGATIVE duration, and a negative delay makes a row due in the past —
// the backoff would silently become no backoff at exactly the attempt count
// where the wait matters most. The cap in [delayAfter] takes over long before
// this is reached; the constant exists so the arithmetic cannot be the thing
// that decides.
const maxShift = 32

// delayAfter returns how long to wait after the given number of failed
// attempts.
//
// The argument INCLUDES the failure being recorded, so the first failure passes
// 1 and waits [firstDelay].
func delayAfter(attempts int64) time.Duration {
	if attempts < 1 {
		// Not reachable through the delivery pass, which only calls this after
		// a failure it has counted. Guarded anyway: a shift by a negative
		// amount panics, and a panic in the sender's error path would turn a
		// receiver outage into a dead job.
		attempts = 1
	}

	shift := attempts - 1
	if shift > maxShift {
		return maxDelay
	}

	delay := firstDelay << uint(shift)
	if delay > maxDelay || delay <= 0 {
		return maxDelay
	}

	return delay
}

// deliverySchedule returns the wait after each failed attempt, in order.
//
// It exists so the sentence "twenty-six hours and thirty-one minutes" is
// COMPUTED rather than asserted: the startup log prints the total and a test
// checks it against the day a receiver is allowed to be down for. A retry
// window stated in a comment and contradicted by the constants is the kind of
// claim this repository has already had to correct once.
//
// The last element is the wait that never happens — after the final allowed
// attempt the row is dead-lettered rather than rescheduled — so the sum of the
// first maxAttempts-1 elements is the window. [deliveryWindow] does that sum.
func deliverySchedule() []time.Duration {
	out := make([]time.Duration, 0, maxAttempts)
	for i := int64(1); i <= maxAttempts; i++ {
		out = append(out, delayAfter(i))
	}

	return out
}

// deliveryWindow is how long a receiver may be down before its deliveries are
// given up on.
//
// It is the sum of the waits BETWEEN the allowed attempts — twelve of them for
// thirteen attempts — and it deliberately ignores the time the requests
// themselves take, which is at most [perAttemptTimeout] each and is noise next
// to hours.
func deliveryWindow() time.Duration {
	var total time.Duration
	for _, delay := range deliverySchedule()[:maxAttempts-1] {
		total += delay
	}

	return total
}
