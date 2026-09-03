package http

import (
	"context"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
)

// CodeRateLimited is the machine-readable error code returned when the rate limit is exceeded.
const CodeRateLimited = "rate_limited"

// forwardedForHeader is the header reverse proxies carry the client IP chain in.
const forwardedForHeader = "X-Forwarded-For"

// gcInterval is how often the in-memory buckets are swept.
//
// Without the sweep every new IP leaves a permanent bucket behind and memory
// grows without bound; that turns the rate limiter itself into a DoS vector.
const gcInterval = time.Minute

// Decision is the result of a single rate limit query.
type Decision struct {
	// Allowed reports whether the request is to be let through.
	Allowed bool
	// Limit is the total number of requests allowed per window.
	Limit int
	// Remaining is the request budget left in this window; it is never negative.
	Remaining int
	// RetryAfter is how long to wait before trying again.
	// It is meaningless while Allowed is true.
	RetryAfter time.Duration
}

// RateLimiter tries to consume a key's quota.
//
// Implementations have to be safe for concurrent calls. On an error the
// middleware LETS THE REQUEST THROUGH: a fault in the limiter (Redis being
// unreachable, say) must not cut off all traffic. This is a deliberate
// "fail-open" choice, and its price is that the limit is not enforced during
// the fault window.
type RateLimiter interface {
	Allow(ctx context.Context, key string) (Decision, error)
}

// KeyFunc turns a request into a rate limit key.
//
// An empty string means the request is not limited.
//
// The key is limited to what can be derived from the request ITSELF. This
// middleware runs BEFORE authentication in the guard stack (the reasoning is in
// [APIGuards]'s ordering section) and at that point there is no [Principal] in
// the context yet: a KeyFunc looking at the caller's identity falls back to the
// IP on every request, and loses [TrustedProxyIPKey]'s proxy correction while
// doing so. Such a key partitions nothing, it only looks like it does.
//
// The ring that namespaces by the caller's identity is the only ring that runs
// AFTER identity in the stack: idempotency (see [Idempotency]).
type KeyFunc func(r *http.Request) string

// RateLimit produces middleware limiting requests per key.
//
// With a nil limiter the middleware is a no-op: the rate limit exists against
// abuse, not for the product's correctness. Rejecting all traffic over an
// unconfigured limiter would take down the very service it is protecting. This
// is the exact opposite of [RequireAdmin] rejecting EVERY REQUEST on a nil
// authenticator; both are right for their own failure model.
//
// With a nil keyFunc [ClientIPKey] is used.
func RateLimit(limiter RateLimiter, keyFunc KeyFunc) func(http.Handler) http.Handler {
	if keyFunc == nil {
		keyFunc = ClientIPKey
	}

	return func(next http.Handler) http.Handler {
		if limiter == nil {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFunc(r)
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}

			d, err := limiter.Allow(r.Context(), key)
			if err != nil {
				// Fail-open: log the limiter fault but do not cut off traffic.
				LoggerFromContext(r.Context()).WarnContext(r.Context(),
					"the rate limiter could not be queried, letting the request through",
					"error", err)
				next.ServeHTTP(w, r)
				return
			}

			writeRateLimitHeaders(w, d)
			if !d.Allowed {
				w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(d.RetryAfter)))
				WriteError(r.Context(), w, coreerrors.TooManyRequests(
					CodeRateLimited, "request limit exceeded, please try again later"))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// writeRateLimitHeaders writes the quota state into the response headers.
//
// The header names follow the RateLimit-* family of the RFC 9331 draft; they are
// written on successful responses too so the client can slow down before hitting
// the limit.
func writeRateLimitHeaders(w http.ResponseWriter, d Decision) {
	if d.Limit <= 0 {
		return
	}

	w.Header().Set("RateLimit-Limit", strconv.Itoa(d.Limit))
	w.Header().Set("RateLimit-Remaining", strconv.Itoa(max(d.Remaining, 0)))
	w.Header().Set("RateLimit-Reset", strconv.Itoa(retryAfterSeconds(d.RetryAfter)))
}

// retryAfterSeconds converts the duration to seconds, rounding UP.
//
// Rounding down would have the client retry before the quota refills and take a
// second 429. It returns at least 1: "wait 0 seconds" is not a wait.
func retryAfterSeconds(d time.Duration) int {
	if d <= 0 {
		return 1
	}

	return max(int(math.Ceil(d.Seconds())), 1)
}

// ClientIPKey keys the request by the client's network address.
//
// It looks ONLY at r.RemoteAddr; it does NOT look at headers like
// X-Forwarded-For. The client can make those up, and sending a different value
// on every request would render the limit entirely useless. Behind a reverse
// proxy use [TrustedProxyIPKey] and give it the number of hops you trust.
func ClientIPKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// Without a port RemoteAddr is the address itself.
		return strings.TrimSpace(r.RemoteAddr)
	}

	return host
}

// TrustedProxyIPKey extracts the client IP from the X-Forwarded-For chain.
//
// hops is the number of TRUSTED reverse proxies between us and the request. The
// chain grows left to right as "client, proxy1, proxy2, ..." : each proxy
// APPENDS the address of the party connecting to it. So the rightmost entry is
// the one written by the trusted proxy closest to us, and with hops trusted hops
// the leftmost verified address is the hops-th entry FROM THE RIGHT:
// parts[len-hops].
//
// Shifting one element further left (parts[len-hops-1]) steps outside the
// verified chain and would pick the entry the client wrote into the header with
// its own hands; an attacker sending a different forged entry on every request
// gets a fresh bucket and bypasses the rate limit entirely. That is why the
// index is exactly len-hops, not one less.
//
// With hops zero or negative the header is not read at all and it falls back to
// [ClientIPKey]; that is the safe reading of "I am not behind a proxy".
//
// If the chain is not long enough to cover hops it falls back to [ClientIPKey]
// as well: a chain shorter than expected means either the configuration is wrong
// or the request arrived bypassing the proxy. Falling back to the FIRST entry
// the client supplied on a short chain would let the client pick the key — so it
// returns to RemoteAddr.
func TrustedProxyIPKey(hops int) KeyFunc {
	return func(r *http.Request) string {
		if hops <= 0 {
			return ClientIPKey(r)
		}

		ham := r.Header.Get(forwardedForHeader)
		if ham == "" {
			// strings.Split produces a ONE-element slice from an empty string; without
			// the early return "no entries at all" would count as a one-entry chain.
			return ClientIPKey(r)
		}

		parts := strings.Split(ham, ",")

		// The hops-th entry from the right: the leftmost address written by trusted proxies.
		idx := len(parts) - hops
		if idx < 0 {
			return ClientIPKey(r)
		}

		ip := strings.TrimSpace(parts[idx])
		if net.ParseIP(ip) == nil {
			return ClientIPKey(r)
		}

		return ip
	}
}

// bucket is a single key's token bucket.
type bucket struct {
	// tokens is the number of tokens left; it is a float so it keeps fractional accrual.
	tokens float64
	// last is the moment the bucket was last refilled.
	last time.Time
}

// MemoryLimiter is a token bucket limiter running in process memory.
//
// It is for single-instance installations and tests. In a horizontally scaled
// deployment every instance counts its own quota; that is, the real limit is
// multiplied by the instance count. A multi-instance installation needs a shared
// counter (Redis).
type MemoryLimiter struct {
	// limit is the number of requests allowed per window.
	limit int
	// window is the period over which the quota fully refills.
	window time.Duration
	// refill is the number of tokens added per second.
	refill float64
	// now reads the time; it is a field so tests can advance the clock.
	now func() time.Time

	mu sync.Mutex
	// buckets is one bucket per key; dead buckets are dropped when gcAt arrives.
	buckets map[string]*bucket
	gcAt    time.Time
}

// NewMemoryLimiter builds a limiter allowing limit requests over window.
//
// With limit or window zero/negative it returns nil: an "unlimited" limiter is
// the same thing as the caller never installing one, and [RateLimit] already
// treats nil as a no-op. That way a "0 limit" does not quietly turn into
// "reject everything".
func NewMemoryLimiter(limit int, window time.Duration) *MemoryLimiter {
	if limit <= 0 || window <= 0 {
		return nil
	}

	return &MemoryLimiter{
		limit:   limit,
		window:  window,
		refill:  float64(limit) / window.Seconds(),
		now:     time.Now,
		buckets: make(map[string]*bucket),
	}
}

// Allow tries to take a token out of the key's quota.
//
// It never returns an error; the signature is there to fit the [RateLimiter] interface.
func (l *MemoryLimiter) Allow(_ context.Context, key string) (Decision, error) {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.collect(now)

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: float64(l.limit), last: now}
		l.buckets[key] = b
	}

	// Add a token for every unit of elapsed time; do not let the bucket overflow.
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(float64(l.limit), b.tokens+elapsed*l.refill)
		b.last = now
	}

	if b.tokens < 1 {
		// The time it takes for one token to accrue.
		missing := 1 - b.tokens
		return Decision{
			Limit:      l.limit,
			Remaining:  0,
			RetryAfter: time.Duration(missing / l.refill * float64(time.Second)),
		}, nil
	}

	b.tokens--

	return Decision{
		Allowed:    true,
		Limit:      l.limit,
		Remaining:  int(b.tokens),
		RetryAfter: l.window,
	}, nil
}

// collect deletes the expired buckets. The caller must be holding l.mu.
//
// A bucket is deleted only after its tokens have fully refilled; deleting early
// would forget the key of a client that hit the limit and reset its quota.
func (l *MemoryLimiter) collect(now time.Time) {
	if now.Before(l.gcAt) {
		return
	}

	l.gcAt = now.Add(gcInterval)

	for k, b := range l.buckets {
		if now.Sub(b.last) >= l.window {
			delete(l.buckets, k)
		}
	}
}
