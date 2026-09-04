package redisguard

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// limitScript increments the counter and returns the time left in the window.
//
// Why Lua: sent as SEPARATE commands, something can go wrong between INCR and
// PEXPIRE (the process dies, the connection drops, the context is canceled) and
// what is left behind is a counter with no TTL — one that lives FOREVER. That
// counter never resets again: the key is blocked for good and that client can
// make no request until somebody deletes it by hand. EVAL runs as one piece
// on the server; there is no instant to slip into.
//
// Why not MULTI/EXEC: it is atomic too, but it queues the commands WITHOUT
// SEEING THE INTERMEDIATE RESULTS. Unable to make PEXPIRE conditional on "the
// counter equals 1", we would have to refresh the TTL on every request; that
// turns a fixed window into a sliding one that never resets, and a client that
// hit the limit stays blocked FOREVER as long as it keeps sending. The script
// sees INCR's result, so it sets the TTL only on the window's first request.
//
// PTTL is not expected to come back negative (a counter is always born with a
// TTL); a key found without one is repaired anyway. Without the repair the
// "blocked forever" case above would come back through a key SET by hand or left
// behind by an older version.
var limitScript = redis.NewScript(`
local counter = redis.call('INCR', KEYS[1])
if counter == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
local remaining = redis.call('PTTL', KEYS[1])
if remaining < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
  remaining = tonumber(ARGV[1])
end
return {counter, remaining}
`)

// Limiter is the rate limiter counting a FIXED WINDOW on Redis.
//
// One counter is kept per key; it is born on the first request, lives for the
// window duration and disappears when it expires. The quota returns in full the
// counter disappears.
//
// # How it differs from corehttp.MemoryLimiter
//
// The in-memory version is a TOKEN BUCKET: tokens accumulate continuously and at
// an even rate, so the quota opens smoothly across the window. In a fixed window
// the quota opens all at once at the start. The practical difference shows AT
// THE WINDOW BOUNDARY: a client can send the full limit at the end of window N
// and the full limit again at the start of N+1 — that is, up to 2x the limit in
// a very short interval. A token bucket has no such burst.
//
// The trade is accepted deliberately: a fixed window means ONE counter per key
// and ONE round trip per request. A sliding window (request timestamps in a
// sorted set) would remove the boundary burst but stores as many members per key
// as there are requests, does an O(log n) insert plus a trim of old members on
// every request, and grows memory in proportion to the requests under attack —
// that is, the rate limiter itself would become the attack surface. A 2x burst
// is a tight enough bound to stop abuse; endpoints wanting a precise threshold
// belong to a quota or a license rather than to a limiter.
//
// # The key shape
//
// Counters are written to "<prefix>:rl:<key>"; with the default prefix the key
// "client-a" lands on the counter "gobit:rl:client-a". The prefix comes from the
// constructor and is what separates two installations sharing one Redis (see the
// package godoc).
type Limiter struct {
	client *redis.Client
	// prefix is the FULL prefix of the counter keys (for example "gobit:rl:").
	//
	// It is built once in the constructor so the namespace prefix and the section
	// name are not joined on every request.
	prefix string
	// limit is how many requests are allowed per window.
	limit int
	// window is the period over which the quota is fully renewed.
	window time.Duration
	// windowMs is the window in milliseconds; it is derived once in the
	// constructor so it is not recomputed on every request.
	windowMs int64
}

var _ corehttp.RateLimiter = (*Limiter)(nil)

// NewLimiter builds a Redis limiter allowing limit requests per window.
//
// keyPrefix is the namespace prefix of the counters; they are written to
// "<keyPrefix>:rl:<key>". [validatePrefix] checks its shape and an invalid prefix
// is an ERROR: separating two installations sharing one Redis depends on it, and
// fixing it silently (by trimming, or by falling back to a default) would land
// them on the same counter again.
//
// It also returns an ERROR when client is nil or limit/window is not positive.
// Unlike corehttp.NewMemoryLimiter it does NOT return nil: a nil *Limiter handed
// to corehttp.RateLimit as an interface produces a "non-nil interface holding
// nil" value, the middleware's limiter == nil check comes out FALSE and the
// first request panics. With the constructor already returning an error there is
// no reason to carry that trap; a caller wanting no limit simply does not plug
// nil verir.
func NewLimiter(client *redis.Client, keyPrefix string, limit int, window time.Duration) (*Limiter, error) {
	if client == nil {
		return nil, coreerrors.Invalid(CodeInvalidConfig, "the redis client cannot be nil")
	}

	if err := validatePrefix(keyPrefix); err != nil {
		return nil, err
	}

	if limit <= 0 {
		return nil, coreerrors.Invalid(CodeInvalidConfig, "the rate limit has to be positive, given: %d", limit)
	}

	if window <= 0 {
		return nil, coreerrors.Invalid(CodeInvalidConfig, "the rate limit window has to be positive, given: %s", window)
	}

	return &Limiter{
		client: client,
		prefix: keyPrefix + separator + rateLimitSection + separator,
		limit:  limit,
		window: window,
		// Redis's finest resolution is the millisecond; a shorter window would
		// turn PEXPIRE 0 into a command error.
		windowMs: max(window.Milliseconds(), 1),
	}, nil
}

// Allow tries to take one request off the key's quota.
//
// When Redis is unreachable it returns KindUnavailable; corehttp.RateLimit logs
// that error and lets the request through (fail-open, see the package godoc).
//
// RetryAfter is filled in for an allowed request too: its value is the time LEFT
// in the window, which answers "when does the quota renew" and the middleware
// writes it into the RateLimit-Reset header.
func (l *Limiter) Allow(ctx context.Context, key string) (corehttp.Decision, error) {
	result, err := limitScript.Run(ctx, l.client,
		[]string{l.prefix + key},
		l.windowMs,
	).Int64Slice()
	if err != nil {
		return corehttp.Decision{}, coreerrors.Wrap(err, coreerrors.KindUnavailable,
			CodeRateLimiterFailed, "the rate limit counter could not be updated")
	}

	if len(result) != 2 {
		return corehttp.Decision{}, coreerrors.Internal(CodeRateLimiterFailed,
			"the rate limit script returned %d values, 2 were expected", len(result))
	}

	counter, kalanMs := result[0], result[1]

	yenilenme := time.Duration(kalanMs) * time.Millisecond
	if yenilenme <= 0 {
		// The counter was on its last breath when we read it. A zero duration
		// means "no wait"; it is rounded up to the full window so the client is
		// not sent to retry instantly and collect a second 429.
		yenilenme = l.window
	}

	if counter > int64(l.limit) {
		return corehttp.Decision{
			Limit:      l.limit,
			Remaining:  0,
			RetryAfter: yenilenme,
		}, nil
	}

	// counter is in the 1..limit range on this branch; narrowing to int is safe.
	return corehttp.Decision{
		Allowed:    true,
		Limit:      l.limit,
		Remaining:  l.limit - int(counter),
		RetryAfter: yenilenme,
	}, nil
}
