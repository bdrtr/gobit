package http

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemoryLimiterRefillsOverTime verifies that the quota really does fill up
// again as time passes.
//
// It is an IN-PACKAGE test: the refill behavior can only be exercised reliably
// by controlling the clock. Waiting on the real clock would make the test both
// slow and flaky in CI.
func TestMemoryLimiterRefillsOverTime(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lim := NewMemoryLimiter(2, 2*time.Second)
	require.NotNil(t, lim)
	lim.now = func() time.Time { return now }

	// Spend the quota.
	for range 2 {
		d, err := lim.Allow(context.Background(), "k")
		require.NoError(t, err)
		require.True(t, d.Allowed)
	}

	d, err := lim.Allow(context.Background(), "k")
	require.NoError(t, err)
	require.False(t, d.Allowed)

	// Half a second: at 2/2s = 1 token per second that is 0.5 tokens, not enough.
	now = now.Add(500 * time.Millisecond)
	d, err = lim.Allow(context.Background(), "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "half a token must not be enough for one request")

	// One more second: a full token accumulates.
	now = now.Add(time.Second)
	d, err = lim.Allow(context.Background(), "k")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "the accumulated token has to be usable")
}

// TestMemoryLimiterBucketDoesNotOverflow verifies that the refill stays UNDER the limit.
//
// The gap is deliberately kept SHORTER THAN THE WINDOW. A long gap lets the
// garbage collector delete the bucket and the quota resets anyway; in that
// scenario the test would pass even with the ceiling removed — that is, it would
// measure nothing. The first version of this test was a false positive for exactly that.
//
// 3 tokens / 1s means 2.7 tokens accumulate in 0.9s. Without a ceiling: 2 + 2.7 = 4.7
// tokens would let 4 requests through. With the ceiling: it stops at 3.
func TestMemoryLimiterBucketDoesNotOverflow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lim := NewMemoryLimiter(3, time.Second)
	require.NotNil(t, lim)
	lim.now = func() time.Time { return now }

	d, err := lim.Allow(context.Background(), "k")
	require.NoError(t, err)
	require.True(t, d.Allowed)

	// A gap shorter than the window: the bucket does not get garbage collected.
	now = now.Add(900 * time.Millisecond)
	require.Len(t, lim.buckets, 1, "the bucket has to be alive still")

	passed := 0

	for range 10 {
		d, err := lim.Allow(context.Background(), "k")
		require.NoError(t, err)

		if !d.Allowed {
			break
		}

		passed++
	}

	assert.Equal(t, 3, passed, "the refill must not exceed the limit")
}

// TestMemoryLimiterLongSilenceRefreshesTheQuota verifies that a key silent for
// longer than the window comes back with its full quota.
//
// It does not contradict [TestMemoryLimiterBucketDoesNotOverflow]: a bucket that
// stayed silent for the whole window would already be full, so the garbage
// collector deleting it DOES NOT CHANGE the quota. Together the two tests say
// this: a bucket neither overflows nor swallows a quota that was earned.
func TestMemoryLimiterLongSilenceRefreshesTheQuota(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lim := NewMemoryLimiter(3, time.Second)
	require.NotNil(t, lim)
	lim.now = func() time.Time { return now }

	for range 3 {
		d, err := lim.Allow(context.Background(), "k")
		require.NoError(t, err)
		require.True(t, d.Allowed)
	}

	d, err := lim.Allow(context.Background(), "k")
	require.NoError(t, err)
	require.False(t, d.Allowed, "the quota has to be spent")

	now = now.Add(24 * time.Hour)

	passed := 0

	for range 10 {
		d, err := lim.Allow(context.Background(), "k")
		require.NoError(t, err)

		if !d.Allowed {
			break
		}

		passed++
	}

	assert.Equal(t, 3, passed, "after a long silence the full quota has to come back")
}

// TestMemoryLimiterCleansDeadBuckets verifies that memory use does not grow
// without bound with the number of keys.
//
// Without the cleanup a server seeing a new source IP on every request would
// exhaust its memory: the limiter itself would be the DoS vector.
func TestMemoryLimiterCleansDeadBuckets(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lim := NewMemoryLimiter(5, time.Second)
	require.NotNil(t, lim)
	lim.now = func() time.Time { return now }

	for i := range 1000 {
		_, err := lim.Allow(context.Background(), strconv.Itoa(i))
		require.NoError(t, err)
	}

	require.Len(t, lim.buckets, 1000, "the buckets have to pile up first")

	// Move past gcInterval so the cleanup triggers; the window expires too.
	now = now.Add(gcInterval + time.Second)

	_, err := lim.Allow(context.Background(), "fresh")
	require.NoError(t, err)

	assert.Len(t, lim.buckets, 1, "the dead buckets have to go, only the new key stays")
}

// TestMemoryLimiterKeepsAnActiveBucket verifies that the quota of a client which
// hit the limit is NOT RESET by the cleanup.
//
// Were an active bucket deleted early, a client over the limit would find a fresh
// quota on every cleanup round and the limit would do nothing.
func TestMemoryLimiterKeepsAnActiveBucket(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lim := NewMemoryLimiter(1, time.Hour)
	require.NotNil(t, lim)
	lim.now = func() time.Time { return now }

	d, err := lim.Allow(context.Background(), "k")
	require.NoError(t, err)
	require.True(t, d.Allowed)

	// Trigger the cleanup, but not late enough for the window (1h) to expire.
	now = now.Add(gcInterval + time.Second)

	d, err = lim.Allow(context.Background(), "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "a bucket whose window is still open must not be cleaned")
}
