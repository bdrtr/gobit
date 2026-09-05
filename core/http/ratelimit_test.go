package http_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// countingLimiter is a fake limiter that keeps the call count and returns a fixed decision.
type countingLimiter struct {
	decision corehttp.Decision
	err      error
	keys     []string
}

// Allow returns the decision as it is and records the incoming key.
func (l *countingLimiter) Allow(_ context.Context, key string) (corehttp.Decision, error) {
	l.keys = append(l.keys, key)

	return l.decision, l.err
}

// makeRequest builds a test request with the given remote address and headers.
func makeRequest(remoteAddr string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/store/v1/products", http.NoBody)
	r.RemoteAddr = remoteAddr

	for k, v := range headers {
		r.Header.Set(k, v)
	}

	return r
}

// passingHandler is a handler that returns 200 and records having been called.
func passingHandler(called *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*called = true
		w.WriteHeader(http.StatusOK)
	})
}

// TestRateLimitDoesNotCallTheHandlerOnceTheQuotaIsGone verifies that the handler
// does NOT run at all once the limit is exceeded.
//
// Looking at the status code alone is not enough: had the 429 been written after
// the handler ran and left its side effect behind, the order would still be
// created.
func TestRateLimitDoesNotCallTheHandlerOnceTheQuotaIsGone(t *testing.T) {
	t.Parallel()

	lim := &countingLimiter{decision: corehttp.Decision{
		Allowed: false, Limit: 10, Remaining: 0, RetryAfter: 3 * time.Second,
	}}

	var called bool
	h := corehttp.RateLimit(lim, nil)(passingHandler(&called))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, makeRequest("203.0.113.7:1234", nil))

	assert.False(t, called, "with the limit exceeded the handler must not run at all")
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "3", w.Header().Get("Retry-After"))
	assert.Equal(t, "10", w.Header().Get("RateLimit-Limit"))
	assert.Equal(t, "0", w.Header().Get("RateLimit-Remaining"))
	assert.Contains(t, w.Body.String(), "rate_limited")
}

// TestRateLimitRoundsRetryAfterUp verifies that a fractional duration is rounded
// UP, not DOWN.
//
// Rounded down, the client would retry before the quota refills and take a
// second 429; the server would be contradicting its own advice.
func TestRateLimitRoundsRetryAfterUp(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		duration time.Duration
		expected string
	}{
		"1.1 seconds":       {1100 * time.Millisecond, "2"},
		"2.9 seconds":       {2900 * time.Millisecond, "3"},
		"exactly 2 seconds": {2 * time.Second, "2"},
		"zero":              {0, "1"},
		"negative":          {-time.Second, "1"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lim := &countingLimiter{decision: corehttp.Decision{Limit: 5, RetryAfter: tc.duration}}
			var called bool
			h := corehttp.RateLimit(lim, nil)(passingHandler(&called))

			w := httptest.NewRecorder()
			h.ServeHTTP(w, makeRequest("203.0.113.7:1234", nil))

			assert.Equal(t, tc.expected, w.Header().Get("Retry-After"))
		})
	}
}

// TestRateLimitLetsTheRequestThroughOnALimiterFault verifies that the request
// GOES THROUGH when the limiter returns an error.
//
// Rejecting all traffic while Redis is unreachable would turn the rate limiter
// into a full outage source.
func TestRateLimitLetsTheRequestThroughOnALimiterFault(t *testing.T) {
	t.Parallel()

	lim := &countingLimiter{err: errors.New("redis is unreachable")}
	var called bool
	h := corehttp.RateLimit(lim, nil)(passingHandler(&called))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, makeRequest("203.0.113.7:1234", nil))

	assert.True(t, called, "a limiter fault must not cut off the request")
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestRateLimitWithANilLimiterIsANoOp verifies that an unconfigured limiter does
// not cut off traffic.
func TestRateLimitWithANilLimiterIsANoOp(t *testing.T) {
	t.Parallel()

	var called bool
	h := corehttp.RateLimit(nil, nil)(passingHandler(&called))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, makeRequest("203.0.113.7:1234", nil))

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestClientIPKeyDoesNotTrustForwardedFor verifies that a forged X-Forwarded-For
// does NOT CHANGE the key.
//
// The hole this test guards is this: were the header trusted, an attacker
// sending a different X-Forwarded-For on every request would get a fresh bucket
// each time and render the rate limit entirely useless.
func TestClientIPKeyDoesNotTrustForwardedFor(t *testing.T) {
	t.Parallel()

	lim := &countingLimiter{decision: corehttp.Decision{Allowed: true, Limit: 100, Remaining: 99}}
	var called bool
	h := corehttp.RateLimit(lim, corehttp.ClientIPKey)(passingHandler(&called))

	for _, forged := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, makeRequest("203.0.113.7:1234",
			map[string]string{"X-Forwarded-For": forged}))
		require.Equal(t, http.StatusOK, w.Code)
	}

	assert.Equal(t, []string{"203.0.113.7", "203.0.113.7", "203.0.113.7"}, lim.keys,
		"the key has to come from RemoteAddr, not from a forgeable header")
}

// TestTrustedProxyIPKey proves which address is picked for hops=0/1/2 with
// 0/1/2/3 entries in the XFF, in every combination.
//
// The contract: the chain grows left to right as "client, proxy1, proxy2, ...",
// and the rightmost entry is written by the trusted proxy closest to us. With
// hops trusted hops the verified address is the hops-th FROM THE RIGHT
// (parts[len-hops]). The table is written as a full product deliberately: an
// off-by-one shift in the index arithmetic can pass unnoticed if it is tested at
// a single length.
//
// On a short chain (len < hops) it falls back to RemoteAddr, NOT to the first
// entry the client supplied. Otherwise the client could pick the key by lowering
// the hop count.
func TestTrustedProxyIPKey(t *testing.T) {
	t.Parallel()

	const remoteAddr = "203.0.113.7"

	tests := map[string]struct {
		hops     int
		xff      string
		expected string
		why      string
	}{
		// hops=0: we are not behind a proxy, the header is never read.
		"hops 0 · 0 entries": {0, "", remoteAddr, "the header must not be read"},
		"hops 0 · 1 entry":   {0, "1.1.1.1", remoteAddr, "the header must not be read"},
		"hops 0 · 2 entries": {0, "1.1.1.1, 2.2.2.2", remoteAddr, "the header must not be read"},
		"hops 0 · 3 entries": {
			0, "1.1.1.1, 2.2.2.2, 3.3.3.3", remoteAddr, "the header must not be read",
		},

		// hops=1: a single trusted proxy; the verified address is the LAST entry.
		"hops 1 · 0 entries": {1, "", remoteAddr, "no chain, it has to fall back to RemoteAddr"},
		"hops 1 · 1 entry": {
			1, "198.51.100.9", "198.51.100.9", "the single entry was written by the trusted proxy",
		},
		"hops 1 · 2 entries": {
			1, "198.51.100.9, 10.0.0.1", "10.0.0.1",
			"the 1st entry from the right has to be picked; the one to its left may be the client's forgery",
		},
		"hops 1 · 3 entries": {
			1, "198.51.100.9, 10.0.0.1, 10.0.0.2", "10.0.0.2", "the 1st entry from the right",
		},

		// hops=2: two trusted proxies; the verified address is the SECOND FROM THE END.
		"hops 2 · 0 entries": {2, "", remoteAddr, "no chain"},
		"hops 2 · 1 entry": {
			2, "198.51.100.9", remoteAddr,
			"the chain is shorter than expected; the client's single entry must not be trusted",
		},
		"hops 2 · 2 entries": {
			2, "198.51.100.9, 10.0.0.1", "198.51.100.9",
			"the chain is exactly as long as expected; the first entry was written by the outer proxy",
		},
		"hops 2 · 3 entries": {
			2, "198.51.100.9, 10.0.0.1, 10.0.0.2", "10.0.0.1", "the 2nd entry from the right",
		},

		// Format edges.
		"whitespace is trimmed": {
			1, "  198.51.100.9 ,   10.0.0.1   ", "10.0.0.1", "the entries have to be trimmed",
		},
		"the picked entry is empty": {
			2, "198.51.100.9, , 10.0.0.1", remoteAddr, "an empty entry is not an IP",
		},
		"the picked entry is an invalid IP": {
			1, "198.51.100.9, no-such-ip", remoteAddr, "an unparseable address is not trusted",
		},
		"an IPv6 entry": {
			1, "198.51.100.9, 2001:db8::1", "2001:db8::1", "IPv6 is a valid address too",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			headers := map[string]string{}
			if tc.xff != "" {
				headers["X-Forwarded-For"] = tc.xff
			}

			key := corehttp.TrustedProxyIPKey(tc.hops)(
				makeRequest(remoteAddr+":1234", headers))
			assert.Equal(t, tc.expected, key, tc.why)
		})
	}
}

// TestTrustedProxyIPKeyAForgedEntryDoesNotChangeTheKey verifies that however
// many forged entries the client puts at the head of X-Forwarded-For, it CANNOT
// CHANGE the key.
//
// The hole this guards: were the selection index to shift one element to the
// left (len-hops-1), the picked entry would no longer be the one written by the
// trusted proxy but the one written by the client. An attacker sending a
// different forged entry on every request would get a fresh bucket each time and
// the rate limit would be entirely useless.
//
// The setup: a single trusted proxy (hops=1) APPENDS the real address
// (198.51.100.9) to the header the client sent; everything to its left is the
// client's forgery.
func TestTrustedProxyIPKeyAForgedEntryDoesNotChangeTheKey(t *testing.T) {
	t.Parallel()

	const trueAddr = "198.51.100.9"

	keyOf := corehttp.TrustedProxyIPKey(1)

	forgeries := []string{
		"1.1.1.1",
		"2.2.2.2",
		"9.9.9.9, 8.8.8.8",
		"203.0.113.7, 1.1.1.1, 2.2.2.2, 3.3.3.3",
	}

	keys := make([]string, 0, len(forgeries))
	for _, forgery := range forgeries {
		// The real address the proxy appends is always the rightmost one.
		keys = append(keys, keyOf(makeRequest("10.0.0.1:1234",
			map[string]string{"X-Forwarded-For": forgery + ", " + trueAddr})))
	}

	assert.Equal(t, []string{trueAddr, trueAddr, trueAddr, trueAddr}, keys,
		"the key has to come only from the address the trusted proxy wrote")
}

// TestTrustedProxyIPKeyFallsBackToRemoteAddrOnAShortChain verifies that the
// client cannot pick the key by SHORTENING the chain.
//
// If adding forged entries does not work, the attacker's second move is to send
// no entries at all, or fewer than expected. In that case the single entry in
// the header is the client's own; falling back to it would again let the client
// pick the key.
func TestTrustedProxyIPKeyFallsBackToRemoteAddrOnAShortChain(t *testing.T) {
	t.Parallel()

	keyOf := corehttp.TrustedProxyIPKey(2)

	for _, forgery := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		key := keyOf(makeRequest("203.0.113.7:1234",
			map[string]string{"X-Forwarded-For": forgery}))
		assert.Equal(t, "203.0.113.7", key,
			"on a short chain it has to fall back to RemoteAddr, not to the client's entry")
	}
}

// TestMemoryLimiterConsumesTheQuota verifies that the quota runs out after
// exactly limit requests.
func TestMemoryLimiterConsumesTheQuota(t *testing.T) {
	t.Parallel()

	lim := corehttp.NewMemoryLimiter(3, time.Minute)
	require.NotNil(t, lim)

	for i := range 3 {
		d, err := lim.Allow(t.Context(), "k")
		require.NoError(t, err)
		assert.True(t, d.Allowed, "request %d should have gone through", i+1)
		assert.Equal(t, 3-i-1, d.Remaining)
	}

	d, err := lim.Allow(t.Context(), "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "it has to reject once the quota is gone")
	assert.Positive(t, d.RetryAfter)
}

// TestMemoryLimiterSeparatesKeys verifies that consuming one key's quota does
// not affect another's.
func TestMemoryLimiterSeparatesKeys(t *testing.T) {
	t.Parallel()

	lim := corehttp.NewMemoryLimiter(1, time.Minute)
	require.NotNil(t, lim)

	d, err := lim.Allow(t.Context(), "a")
	require.NoError(t, err)
	require.True(t, d.Allowed)

	d, err = lim.Allow(t.Context(), "a")
	require.NoError(t, err)
	require.False(t, d.Allowed, "a's quota has to be gone")

	d, err = lim.Allow(t.Context(), "b")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "b's quota has to be independent of a's")
}

// TestNewMemoryLimiterReturnsNilOnAnInvalidConfiguration verifies that a zero
// limit does NOT mean "reject everything".
func TestNewMemoryLimiterReturnsNilOnAnInvalidConfiguration(t *testing.T) {
	t.Parallel()

	assert.Nil(t, corehttp.NewMemoryLimiter(0, time.Minute))
	assert.Nil(t, corehttp.NewMemoryLimiter(-1, time.Minute))
	assert.Nil(t, corehttp.NewMemoryLimiter(10, 0))
	assert.Nil(t, corehttp.NewMemoryLimiter(10, -time.Minute))
}

// TestMemoryLimiterIsConsistentUnderConcurrentUse verifies under the race
// detector that the total number of requests let through does NOT EXCEED the
// limit.
func TestMemoryLimiterIsConsistentUnderConcurrentUse(t *testing.T) {
	t.Parallel()

	const limit = 50
	lim := corehttp.NewMemoryLimiter(limit, time.Hour)
	require.NotNil(t, lim)

	results := make(chan bool, 200)
	for range 200 {
		go func() {
			d, err := lim.Allow(context.Background(), "shared")
			assert.NoError(t, err)
			results <- d.Allowed
		}()
	}

	passed := 0
	for range 200 {
		if <-results {
			passed++
		}
	}

	assert.Equal(t, limit, passed, "concurrency must not be able to exceed the quota")
}
