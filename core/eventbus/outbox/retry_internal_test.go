package outbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The retry policy is arithmetic with two cliffs — a cap and an overflow — and
// both of them fail SILENTLY in the direction of "no backoff at all". These
// tests are internal because the schedule is not part of the published surface:
// what is promised is the behavior ([Policy]'s fields), not the function that
// computes it.

// TestTheDelayGrowsAndThenStops pins the shape of the backoff.
//
// A fixed delay treats a receiver that is down for a second and one that is
// down for an hour identically, and the second decides the load; growth is the
// whole point of the policy. The CAP is equally load-bearing in the other
// direction: uncapped doubling reaches days, and an event nobody retries for a
// day is a dead letter that was never declared one.
func TestTheDelayGrowsAndThenStops(t *testing.T) {
	t.Parallel()

	policy := Policy{FirstDelay: time.Minute, MaxDelay: time.Hour, MaxAttempts: 10}.normalized()

	want := []time.Duration{
		time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute,
		16 * time.Minute, 32 * time.Minute, time.Hour, time.Hour, time.Hour,
	}

	for i, expected := range want {
		attempts := int64(i + 1)
		assert.Equal(t, expected, policy.delayAfter(attempts),
			"the delay after attempt %d", attempts)
	}
}

// TestTheDefaultScheduleIsAboutFourHours holds the documented number to the
// arithmetic that produces it.
//
// [DefaultMaxAttempts] and the two delay constants are chosen TOGETHER against
// one question: how long may a receiver be down before its events are declared
// dead? The answer is written in prose in this package's documentation, and
// prose is exactly the thing that keeps its old value when a constant changes.
func TestTheDefaultScheduleIsAboutFourHours(t *testing.T) {
	t.Parallel()

	policy := DefaultPolicy()

	var total time.Duration
	for attempts := int64(1); attempts < policy.MaxAttempts; attempts++ {
		total += policy.delayAfter(attempts)
	}

	assert.Equal(t, 243*time.Minute, total,
		"the default policy spends this long trying before it gives up; if this "+
			"changed, the four-hour claim in the package documentation changed with it")
}

// TestAZeroFieldMeansTheDefault keeps a partial struct literal from disarming
// the machinery.
//
// A caller who sets only MaxAttempts is writing ordinary Go. Read literally,
// the zero FirstDelay would restore the every-minute retry this package was
// changed to end, and a zero MaxAttempts would dead-letter every event on its
// first failure. Both are silent, and one of them loses deliveries.
func TestAZeroFieldMeansTheDefault(t *testing.T) {
	t.Parallel()

	got := Policy{}.normalized()

	assert.Equal(t, DefaultPolicy(), got)

	partial := Policy{MaxAttempts: 3}.normalized()
	assert.Equal(t, int64(3), partial.MaxAttempts, "what the caller DID choose stands")
	assert.Equal(t, DefaultFirstDelay, partial.FirstDelay)
	assert.Equal(t, DefaultMaxDelay, partial.MaxDelay)
}

// TestACapBelowTheFirstDelayIsHonored leaves one flat delay expressible.
//
// Correcting it would make MaxDelay the only field a caller cannot really set,
// and "retry every ten seconds, five times" is a legitimate policy for an
// embedder whose receiver is in the same data center.
func TestACapBelowTheFirstDelayIsHonored(t *testing.T) {
	t.Parallel()

	policy := Policy{FirstDelay: time.Minute, MaxDelay: time.Second, MaxAttempts: 3}.normalized()

	assert.Equal(t, time.Second, policy.delayAfter(1))
	assert.Equal(t, time.Second, policy.delayAfter(2))
}

// TestTheDelayIsNeverNegative is the overflow guard.
//
// A time.Duration is an int64 of nanoseconds, so doubling far enough wraps into
// a NEGATIVE duration — and a negative delay makes the row due in the PAST.
// That is the worst possible failure of this function: the backoff silently
// becomes no backoff at exactly the attempt count where waiting matters most.
// The ceiling normally stops the count long before, so this is what happens
// when an embedder sets a large MaxAttempts.
func TestTheDelayIsNeverNegative(t *testing.T) {
	t.Parallel()

	policy := Policy{FirstDelay: time.Hour, MaxDelay: 24 * time.Hour, MaxAttempts: 1_000}.normalized()

	for _, attempts := range []int64{1, 10, 40, 63, 64, 200, 1_000_000} {
		delay := policy.delayAfter(attempts)
		require.Positive(t, delay, "the delay after attempt %d", attempts)
		assert.LessOrEqual(t, delay, policy.MaxDelay, "the delay after attempt %d", attempts)
	}
}

// TestAnImpossibleAttemptCountDoesNotPanic guards the relay's error path.
//
// Nothing calls this with less than one today — the relay only asks after a
// failure it has counted — but a shift by a negative amount is a PANIC, and a
// panic on the error path would turn a receiver outage into a dead job.
func TestAnImpossibleAttemptCountDoesNotPanic(t *testing.T) {
	t.Parallel()

	policy := DefaultPolicy()

	assert.Equal(t, policy.FirstDelay, policy.delayAfter(0))
	assert.Equal(t, policy.FirstDelay, policy.delayAfter(-5))
}
