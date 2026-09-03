package errorreport

import (
	"sync"
	"time"
)

// The rate limit's defaults.
const (
	// DefaultBurst is how many reports of ONE code are sent per window.
	//
	// Three rather than one: the first occurrence of a failure and its third
	// are often different requests, and a collector that saw only the first
	// would show a single example of something that has two shapes.
	DefaultBurst = 3
	// DefaultWindow is how long a code stays limited.
	DefaultWindow = time.Minute
	// maxCodes bounds the limiter's memory.
	//
	// The key is a machine code from a closed set, so the map cannot grow with
	// traffic in normal operation. The bound is here for the case the set stops
	// being closed — an unclassified error reports under the empty code today,
	// and a future caller that puts a request-derived string in Code would
	// otherwise turn this map into an unbounded cache with an attacker holding
	// the keys.
	maxCodes = 1024
)

// Limiter decides whether one more report of a code may be sent.
//
// # Why per code and not overall
//
// An outage does not produce one failure, it produces every failure at once: a
// database that stops answering makes every request fail, and an overall limit
// would fill with whichever endpoint is busiest and hide the rest. Grouping by
// code sends a few of EACH distinct failure, which is the set an operator
// actually needs.
//
// # Why the suppressed count travels
//
// A limiter that silently dropped the rest would turn an outage affecting every
// request into a collector showing three events. The count rides along on the
// next report that gets through, so the magnitude survives even though the
// individual events do not.
type Limiter struct {
	mu     sync.Mutex
	seen   map[string]*window
	burst  int
	period time.Duration
	// now is the clock, injected so the tests do not sleep.
	now func() time.Time
}

// window is one code's state inside the current period.
type window struct {
	started    time.Time
	sent       int
	suppressed int
}

// NewLimiter builds a limiter. A non-positive burst or period falls back to the
// default.
func NewLimiter(burst int, period time.Duration, now func() time.Time) *Limiter {
	if burst <= 0 {
		burst = DefaultBurst
	}
	if period <= 0 {
		period = DefaultWindow
	}
	if now == nil {
		now = time.Now
	}

	return &Limiter{seen: map[string]*window{}, burst: burst, period: period, now: now}
}

// Allow reports whether this occurrence may be sent, and how many occurrences
// of the same code were dropped since the last one that was.
func (l *Limiter) Allow(code string) (allowed bool, suppressed int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	at := l.now()

	state, ok := l.seen[code]
	if !ok {
		// The eviction is deliberately blunt: the whole table is dropped rather
		// than a victim chosen. Choosing needs a recency order to maintain on
		// every call, and this map is on the path of every failing request; a
		// table that starts over costs one duplicated report per code, which is
		// cheaper than the bookkeeping and cannot itself become a bug.
		if len(l.seen) >= maxCodes {
			l.seen = map[string]*window{}
		}
		state = &window{started: at}
		l.seen[code] = state
	}

	if at.Sub(state.started) >= l.period {
		state.started = at
		state.sent = 0
		// suppressed is NOT reset here. It is carried across the window
		// boundary so that a failure suppressed at the end of one minute is
		// still counted by the report that opens the next one; zeroing it would
		// lose exactly the occurrences the count exists to preserve.
	}

	if state.sent >= l.burst {
		state.suppressed++

		return false, 0
	}

	state.sent++
	suppressed, state.suppressed = state.suppressed, 0

	return true, suppressed
}
