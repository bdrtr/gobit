package outbox

import "time"

// Policy is what the relay does with an event it could not publish.
//
// # The two failure modes it stands between
//
// An outbox has exactly two ways to betray the promise it exists to keep, and
// they are opposites, so a policy that avoids one by leaning harder is how you
// arrive at the other:
//
//   - RETRYING FOREVER. The receiver will never accept this payload, and the
//     relay re-sends it every minute for the life of the installation. Worse
//     than the wasted call, the row keeps its place at the head of the queue:
//     measured on a real PostgreSQL, a batch limit's worth of permanently
//     failing rows stops delivery for EVERY event behind them. [Policy.MaxAttempts]
//     is what prevents this — the row is given up on and leaves the relay's
//     index.
//   - DROPPING SILENTLY. The relay decides an event is hopeless and the row
//     disappears, or stays but is indistinguishable from one written a second
//     ago. Nobody is owed anything, because nobody can see what was lost. The
//     dead letter is what prevents this: giving up WRITES the moment it gave
//     up, the attempt count and the last error, and [Store.DeadLetters] is a
//     reader for exactly those rows — a ledger with no reader is the mistake
//     this repository already made once, in audit_log.
//
// # Why the delay grows
//
// A fixed delay treats a receiver that is down for two seconds and one that is
// down for two hours the same way, and the second is the one that decides the
// load: at a fixed minute, an hour-long outage over a thousand pending events
// is sixty thousand refused publishes. Doubling turns the same hour into six
// attempts per event. The growth is capped by [Policy.MaxDelay] because an
// uncapped doubling reaches days, and an event nobody retries for a day is a
// dead letter that was never declared one.
type Policy struct {
	// FirstDelay is how long the first failure waits before the next attempt.
	//
	// A value under the relay's interval is not wrong, it is merely
	// unobservable: the relay looks once a minute, so any delay shorter than
	// that means "the next pass".
	FirstDelay time.Duration

	// MaxDelay caps the growth. The doubling stops here and every later
	// attempt waits exactly this long.
	MaxDelay time.Duration

	// MaxAttempts is how many failed attempts a row is allowed before it is
	// dead-lettered.
	//
	// It counts ATTEMPTS, not retries, so 1 would mean "one try and no retry
	// at all". It is a ceiling on tries rather than on elapsed time because
	// elapsed time is not the relay's to promise: a process that was down for
	// a day has made no attempts in it, and killing a row for time it was
	// never given would dead-letter a backlog the moment the relay came back.
	MaxAttempts int64
}

// Default policy values.
//
// The schedule they produce is 1, 2, 4, 8, 16, 32, 60, 60, 60 minutes and then
// the dead letter — four hours and three minutes of trying, spread over ten
// attempts. That number is chosen against the thing it has to survive: a
// receiver outage. Anything shorter than four hours is one an on-call human
// can plausibly fix before the events are declared dead, and anything longer
// means a genuinely poisoned event is retried into the next working day before
// anybody is told.
const (
	// DefaultFirstDelay is [Policy.FirstDelay] when none is given. It equals
	// the relay's interval: a shorter one would be rounded up to it anyway.
	DefaultFirstDelay = time.Minute
	// DefaultMaxDelay is [Policy.MaxDelay] when none is given.
	DefaultMaxDelay = time.Hour
	// DefaultMaxAttempts is [Policy.MaxAttempts] when none is given.
	DefaultMaxAttempts int64 = 10
)

// DefaultPolicy returns the policy the relay uses when none is chosen.
func DefaultPolicy() Policy {
	return Policy{
		FirstDelay:  DefaultFirstDelay,
		MaxDelay:    DefaultMaxDelay,
		MaxAttempts: DefaultMaxAttempts,
	}
}

// normalized fills in the fields the caller left at zero.
//
// A zero field means "not chosen", never "zero": a MaxAttempts of 0 read
// literally would dead-letter every event on its first failure, and a
// FirstDelay of 0 would restore the unbounded per-minute retry this package
// was changed to end. Both are silent catastrophes, and a struct literal that
// omits a field is the ordinary way to write Go — so the safe reading is the
// only one available here.
//
// A MaxDelay smaller than FirstDelay is honored rather than corrected: it
// means "one flat delay", which is a legitimate policy, and second-guessing it
// would make the cap the only field a caller cannot actually set.
func (p Policy) normalized() Policy {
	if p.FirstDelay <= 0 {
		p.FirstDelay = DefaultFirstDelay
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = DefaultMaxDelay
	}
	if p.MaxAttempts <= 0 {
		p.MaxAttempts = DefaultMaxAttempts
	}

	return p
}

// maxShift is where the doubling stops being arithmetic.
//
// A time.Duration is an int64 of nanoseconds, so FirstDelay<<62 overflows into
// a NEGATIVE duration and a negative delay makes the row due in the past —
// that is, the backoff would silently become no backoff at exactly the attempt
// count where the wait matters most. The shift is bounded well below that and
// the cap in [Policy.delayAfter] takes over long before it is reached; this
// constant exists so the arithmetic cannot be the thing that decides.
const maxShift = 32

// delayAfter returns how long to wait after the given number of failed
// attempts.
//
// The argument is the count INCLUDING the failure being recorded, so the first
// failure passes 1 and waits [Policy.FirstDelay]. Doubling from there, capped.
//
// # No jitter, and that is a decision
//
// Jitter is the standard companion to exponential backoff, and it earns its
// place when many independent clients would otherwise retry in step. Nothing
// here is independent: the relay is one scheduled job, elected by an advisory
// lock so exactly one instance runs it, and a batch is published serially
// inside one transaction. The retries of a thousand rows are already spread
// across the batch by the publishes themselves, and the occurrence schedule
// quantizes every wake-up to the minute regardless. Jitter would buy nothing
// and cost the property that makes this testable: a delay you can predict.
func (p Policy) delayAfter(attempts int64) time.Duration {
	if attempts < 1 {
		// Not reachable through the relay, which only calls this after a
		// failure it has counted. Guarding anyway: a shift by a negative
		// amount panics, and a panic in the relay's error path would turn a
		// receiver outage into a dead job.
		attempts = 1
	}

	shift := attempts - 1
	if shift > maxShift {
		return p.MaxDelay
	}

	delay := p.FirstDelay << uint(shift)
	if delay > p.MaxDelay || delay <= 0 {
		return p.MaxDelay
	}

	return delay
}
