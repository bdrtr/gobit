package redisguard_test

import (
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/http/redisguard"
)

// This file needs NO Redis: it exercises the constructors' fast-failure path
// only. The contract tests that want a real Redis are in
// redisguard_integration_test.go.

// fakeClient produces a Redis client that never connects but is not nil either.
//
// go-redis opens the connection on the first command; because the constructors'
// validation runs no command, these tests run without Docker.
func fakeClient() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
}

// validPrefix is a prefix that passes the shape check; it is the current default.
const validPrefix = "gobit"

func TestNewLimiterRefusesAnInvalidSetting(t *testing.T) {
	t.Parallel()

	durumlar := map[string]struct {
		limit  int
		window time.Duration
	}{
		"a zero limit":    {limit: 0, window: time.Minute},
		"negatif limit":   {limit: -1, window: time.Minute},
		"a zero window":   {limit: 10, window: 0},
		"negatif pencere": {limit: 10, window: -time.Second},
		"both zero":       {limit: 0, window: 0},
	}

	for ad, d := range durumlar {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			lim, err := redisguard.NewLimiter(fakeClient(), validPrefix, d.limit, d.window)

			require.Error(t, err, "an invalid setting has to return an error")
			assert.Nil(t, lim)
			assert.True(t, coreerrors.IsInvalid(err), "the error has to be KindInvalid")
			assert.Equal(t, redisguard.CodeInvalidConfig, coreerrors.CodeOf(err))
		})
	}
}

func TestConstructorsRefuseANilClient(t *testing.T) {
	t.Parallel()

	lim, err := redisguard.NewLimiter(nil, validPrefix, 10, time.Minute)
	require.Error(t, err, "a nil client has to return an error")
	assert.Nil(t, lim)
	assert.Equal(t, redisguard.CodeInvalidConfig, coreerrors.CodeOf(err))

	store, err := redisguard.NewIdempotencyStore(nil, validPrefix, time.Hour)
	require.Error(t, err, "a nil client has to return an error")
	assert.Nil(t, store)
	assert.Equal(t, redisguard.CodeInvalidConfig, coreerrors.CodeOf(err))
}

// TestNewLimiterReturnsAnErrorRatherThanNil pins that the limiter does not fall
// into the "typed-nil" trap.
//
// corehttp.NewMemoryLimiter returns nil on an invalid setting, and handing that
// nil straight to corehttp.RateLimit does NOT produce a nil interface value; the
// middleware's limiter == nil check is missed and the first request panics. This
// test guarantees the same path stays closed in the Redis version (it returns an
// error and stops the caller).
func TestNewLimiterReturnsAnErrorRatherThanNil(t *testing.T) {
	t.Parallel()

	lim, err := redisguard.NewLimiter(fakeClient(), validPrefix, 0, time.Minute)
	require.Error(t, err)
	require.Nil(t, lim)

	// What would happen if the caller ignored the error: a nil *Limiter put into
	// an interface LOOKS non-nil. The constructor returning an error is the only
	// protection keeping that value from ever reaching the middleware.
	//
	// The comparison is made at the LANGUAGE level rather than with testify's
	// NotNil: testify looks through reflection and counts an interface holding a
	// nil as "nil", while the middleware's "limiter == nil" check is exactly the
	// comparison below.
	assert.True(t, asInterface(lim) != nil,
		"a nil *Limiter does NOT look nil in an interface; that is why the constructor errors")
}

// asInterface wraps the given limiter in an interface value.
//
// It is a separate function so the comparison is not constant-folded at the call
// site; what the test measures is RUNTIME behavior.
func asInterface(l *redisguard.Limiter) corehttp.RateLimiter { return l }

// TestNewIdempotencyStoreFallsBackOnAnInvalidTTL pins behavioral equality with
// the in-memory store.
//
// The two stores are interchangeable; behaving differently on the same input
// (one falling back to a default while the other errors) would become a silent
// surprise for an installation that changed its backend.
func TestNewIdempotencyStoreFallsBackOnAnInvalidTTL(t *testing.T) {
	t.Parallel()

	for ad, ttl := range map[string]time.Duration{
		"zero":    0,
		"negatif": -time.Hour,
	} {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			store, err := redisguard.NewIdempotencyStore(fakeClient(), validPrefix, ttl)

			require.NoError(t, err, "an invalid ttl has to fall back to the default, not error")
			assert.NotNil(t, store)
		})
	}
}

// TestConstructorsRefuseAnInvalidPrefix verifies that a namespace prefix which
// is empty or carries the separator is not accepted SILENTLY.
//
// The prefix is the only thing separating two installations sharing one Redis;
// a prefix fixed silently (trimmed, or replaced with the default) lands the two
// installations in the same namespace again and the failure the package exists
// to solve — one's response going to the other's client — comes back. So the
// constructor does not fix it, it STOPS.
func TestConstructorsRefuseAnInvalidPrefix(t *testing.T) {
	t.Parallel()

	onekler := map[string]string{
		// That means no namespace at all, while the caller asked for one by
		// passing the prefix parameter.
		"empty": "",
		// A prefix carrying the separator opens a real clash: an idempotency key
		// the client invents can land two installations on the same key.
		"carries the separator":   "gobit:staging",
		"ends with the separator": "gobit:",
		// Invisible characters move an installation into another namespace
		// unnoticed; every counter and every in-flight record is ignored at once.
		"trailing space": "gobit ",
		"leading space":  " gobit",
		"carries a tab":  "gobit\tprod",
		"a newline":      "gobit\n",
		// Glob characters break the operator's "<prefix>:idem:*" scan.
		"carries a star":     "gobit*",
		"carries a bracket":  "gobit[1]",
		"carries a question": "gobit?",
		// Visually indistinguishable characters make two namespaces look like ONE
		// (the 'a' here is Cyrillic).
		"a non-Latin letter": "gоbit",
	}

	for ad, prefix := range onekler {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			lim, err := redisguard.NewLimiter(fakeClient(), prefix, 10, time.Minute)
			require.Error(t, err, "an invalid prefix has to error in the limiter")
			assert.Nil(t, lim)
			assert.True(t, coreerrors.IsInvalid(err), "the error has to be KindInvalid")
			assert.Equal(t, redisguard.CodeInvalidConfig, coreerrors.CodeOf(err))

			store, err := redisguard.NewIdempotencyStore(fakeClient(), prefix, time.Hour)
			require.Error(t, err, "an invalid prefix has to error in the store")
			assert.Nil(t, store)
			assert.True(t, coreerrors.IsInvalid(err), "the error has to be KindInvalid")
			assert.Equal(t, redisguard.CodeInvalidConfig, coreerrors.CodeOf(err))
		})
	}
}

// TestConstructorsAcceptAValidPrefix verifies that the shape check does not hold
// legitimate installation names at the door either.
//
// Were the check too strict the cost would be concrete: the operator cannot
// separate the installations, and because they cannot, they leave the default —
// bringing back with their own hands the failure the check meant to refuse.
func TestConstructorsAcceptAValidPrefix(t *testing.T) {
	t.Parallel()

	for _, prefix := range []string{
		"gobit", "gobit-staging", "gobit_prod", "magaza.42", "GOBIT", "g",
	} {
		t.Run(prefix, func(t *testing.T) {
			t.Parallel()

			lim, err := redisguard.NewLimiter(fakeClient(), prefix, 10, time.Minute)
			require.NoError(t, err, "a valid prefix must not be refused")
			assert.NotNil(t, lim)

			store, err := redisguard.NewIdempotencyStore(fakeClient(), prefix, time.Hour)
			require.NoError(t, err, "a valid prefix must not be refused")
			assert.NotNil(t, store)
		})
	}
}
