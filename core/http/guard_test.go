package http_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// marking produces middleware that writes its own invocation into a counter.
func marking(counter *int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*counter++
			next.ServeHTTP(w, r)
		})
	}
}

// rejecting is middleware that cuts the request off with a 418; it proves with
// the status code that paths meant to be out of scope really are not cut off.
func rejecting(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
}

// TestScopedRunsOnlyUnderThePrefix verifies in a single table both the paths the
// scope rule catches and the ones it does not.
//
// The boundary cases are deliberate: the "/admin/v1x" prefix LOOKS like it
// shares the prefix but is not at a segment boundary; a guard leaking in there
// would run on an endpoint it was not designed for.
func TestScopedRunsOnlyUnderThePrefix(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		path     string
		expected int
	}{
		"the prefix matches exactly":     {path: "/admin/v1", expected: http.StatusTeapot},
		"an endpoint under the prefix":   {path: "/admin/v1/users", expected: http.StatusTeapot},
		"a deep path":                    {path: "/admin/v1/users/usr_1/password", expected: http.StatusTeapot},
		"a similar but different prefix": {path: "/admin/v1x/users", expected: http.StatusOK},
		"another surface":                {path: "/store/v1/products", expected: http.StatusOK},
		"a health endpoint":              {path: "/health", expected: http.StatusOK},
		"the prefix inside as a string":  {path: "/x/admin/v1/users", expected: http.StatusOK},
	}

	for ad, tt := range cases {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			h := corehttp.Scoped("/admin/v1", nil, rejecting)(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				}))

			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tt.path, http.NoBody))

			assert.Equal(t, tt.expected, w.Code)
		})
	}
}

// TestScopedAnExemptPathSkipsTheMiddleware verifies that the login endpoint can
// stay exempt from the guard. Without the exemption nobody can log in and the
// system locks itself out.
func TestScopedAnExemptPathSkipsTheMiddleware(t *testing.T) {
	t.Parallel()

	h := corehttp.Scoped("/admin/v1", []string{"/admin/v1/auth/login"}, rejecting)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/v1/auth/login", http.NoBody))
	assert.Equal(t, http.StatusOK, w.Code, "an exempt path must not enter the middleware")

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/v1/auth/logout", http.NoBody))
	assert.Equal(t, http.StatusTeapot, w.Code, "a neighboring non-exempt path has to stay guarded")
}

// TestScopedTheChainKeepsTheOrder verifies that several middlewares run IN THE
// ORDER GIVEN: had the rate limit run after authentication, a rejected request
// would have spent the quota too.
func TestScopedTheChainKeepsTheOrder(t *testing.T) {
	t.Parallel()

	var order []string
	recording := func(ad string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, ad)
				next.ServeHTTP(w, r)
			})
		}
	}

	h := corehttp.Scoped("/admin/v1", nil, recording("one"), recording("two"))(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			order = append(order, "handler")
		}))

	h.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/admin/v1/users", http.NoBody))

	assert.Equal(t, []string{"one", "two", "handler"}, order)
}

// TestScopedANilMiddlewareIsSkipped verifies that handing an unconfigured
// component in as nil leads to that ring being skipped, not to a panic.
func TestScopedANilMiddlewareIsSkipped(t *testing.T) {
	t.Parallel()

	counter := 0
	h := corehttp.Scoped("/admin/v1", nil, nil, marking(&counter))(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

	w := httptest.NewRecorder()
	assert.NotPanics(t, func() {
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/v1/users", http.NoBody))
	})
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, counter)
}

// TestScopedDoesNotTouchTheChiRoutePattern verifies that the scoping does not
// break route matching: the middleware wraps the request but does not change the path.
func TestScopedDoesNotTouchTheChiRoutePattern(t *testing.T) {
	t.Parallel()

	counter := 0
	r := chi.NewRouter()
	r.Use(corehttp.Scoped("/admin/v1", nil, marking(&counter)))
	r.Get("/admin/v1/users/{id}", func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte(chi.URLParam(req, "id")))
	})
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/v1/users/usr_42", http.NoBody))
	assert.Equal(t, "usr_42", w.Body.String())
	assert.Equal(t, 1, counter)

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", http.NoBody))
	assert.Equal(t, 1, counter, "the health endpoint has to stay out of scope")
}

// fixedAuthenticator is an authenticator that returns the given identity and
// queries nothing.
type fixedAuthenticator struct {
	principal corehttp.Principal
	err       error
}

// AuthenticateAdmin returns the fixed identity or the fixed error.
func (d fixedAuthenticator) AuthenticateAdmin(
	_ context.Context, _, _ string,
) (corehttp.Principal, error) {
	return d.principal, d.err
}

// AuthenticateStore returns the fixed identity or the fixed error.
func (d fixedAuthenticator) AuthenticateStore(_ context.Context, _ string) (corehttp.Principal, error) {
	return d.principal, d.err
}

// guardedRouter builds a router carrying the stack produced from the given options.
func guardedRouter(t *testing.T, opts corehttp.GuardOptions) chi.Router {
	t.Helper()

	r := corehttp.NewRouter(corehttp.RouterOptions{
		Version:     "test",
		Middlewares: corehttp.APIGuards(opts),
	})
	r.Post("/admin/v1/users", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	r.Get("/store/v1/products", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	return r
}

// call sends the given request to the router.
func call(r chi.Router, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// TestAPIGuardsTheRateLimitRunsBeforeAuthentication proves the ORDER of the stack.
//
// Were the order reversed, every request of an attacker trying passwords would
// first make us pay the authentication cost (bcrypt + a database lookup), and
// only then would the quota drop. The second request below would return a 401
// instead of a 429.
func TestAPIGuardsTheRateLimitRunsBeforeAuthentication(t *testing.T) {
	t.Parallel()

	r := guardedRouter(t, corehttp.GuardOptions{
		Authenticator: fixedAuthenticator{err: errors.New("invalid")},
		Limiter:       corehttp.NewMemoryLimiter(1, time.Minute),
	})

	first := call(r, httptest.NewRequest(http.MethodGet, "/store/v1/products", http.NoBody))
	assert.Equal(t, http.StatusUnauthorized, first.Code,
		"the first request spends the quota and is rejected at identity")

	second := call(r, httptest.NewRequest(http.MethodGet, "/store/v1/products", http.NoBody))
	assert.Equal(t, http.StatusTooManyRequests, second.Code,
		"once the quota is gone authentication must NOT be reached at all")
	assert.NotEmpty(t, second.Header().Get("Retry-After"),
		"the 429 has to tell the client when to come back")
}

// TestAPIGuardsDoNotCoverTheHealthEndpoints verifies that the orchestrator's
// path is exempt from the stack.
//
// Were it covered, a /ready request hitting the rate limit would show the process
// as "unhealthy" and the orchestrator would pull a healthy instance out of traffic.
func TestAPIGuardsDoNotCoverTheHealthEndpoints(t *testing.T) {
	t.Parallel()

	r := guardedRouter(t, corehttp.GuardOptions{
		Authenticator: fixedAuthenticator{err: errors.New("invalid")},
		Limiter:       corehttp.NewMemoryLimiter(1, time.Minute),
	})

	for i := range 5 {
		rec := call(r, httptest.NewRequest(http.MethodGet, "/health", http.NoBody))
		require.Equal(t, http.StatusOK, rec.Code, "health request %d has to go through", i+1)
	}
}

// TestAPIGuardsIdempotencyRunsAfterIdentity proves the place of the THIRD ring
// of the stack.
//
// A rejected request MUST NOT CONSUME the idempotency key: had it consumed it,
// the 401 response would be frozen for the client that comes back with the same
// key after fixing its identity, and the request would never run.
func TestAPIGuardsIdempotencyRunsAfterIdentity(t *testing.T) {
	t.Parallel()

	reject := fixedAuthenticator{err: errors.New("invalid")}
	accept := fixedAuthenticator{principal: corehttp.Principal{ID: "usr_1", Kind: "user"}}

	deferred := &corehttp.DeferredAuthenticator{}
	deferred.Bind(reject)

	r := guardedRouter(t, corehttp.GuardOptions{
		Authenticator:    deferred,
		IdempotencyStore: corehttp.NewMemoryIdempotencyStore(time.Hour, 0),
	})

	makeReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/admin/v1/users", http.NoBody)
		// The header is there in BOTH cases; the only thing that changes is the answer
		// the authenticator gives. A request without the header would be rejected
		// before ever reaching identity and the difference the test wants to tell
		// apart would be lost.
		req.Header.Set("Authorization", "Bearer token")
		req.Header.Set(corehttp.IdempotencyKeyHeader, "same-key")

		return req
	}

	rejected := call(r, makeReq())
	require.Equal(t, http.StatusUnauthorized, rejected.Code)

	deferred.Bind(accept)

	accepted := call(r, makeReq())
	assert.Equal(t, http.StatusCreated, accepted.Code,
		"the 401 must not consume the key; once identity is fixed the request has to run")
	assert.Empty(t, accepted.Header().Get(corehttp.IdempotencyReplayedHeader),
		"the first REAL run is not a replay")

	replay := call(r, makeReq())
	assert.Equal(t, http.StatusCreated, replay.Code)
	assert.Equal(t, "true", replay.Header().Get(corehttp.IdempotencyReplayedHeader),
		"a second request with the same key has to replay the record")
}

// TestAPIGuardsAnUnconfiguredAuthenticatorRejectsEverything verifies ADR 0007's
// identity line: without an authenticator the surface is CLOSED.
func TestAPIGuardsAnUnconfiguredAuthenticatorRejectsEverything(t *testing.T) {
	t.Parallel()

	r := guardedRouter(t, corehttp.GuardOptions{})

	admin := call(r, httptest.NewRequest(http.MethodPost, "/admin/v1/users", http.NoBody))
	assert.Equal(t, http.StatusUnauthorized, admin.Code)

	magaza := call(r, httptest.NewRequest(http.MethodGet, "/store/v1/products", http.NoBody))
	assert.Equal(t, http.StatusUnauthorized, magaza.Code)
}

// TestAPIGuardsAnExemptPathAsksForNoIdentity verifies that the login endpoint
// goes through the stack but skips the identity ring.
func TestAPIGuardsAnExemptPathAsksForNoIdentity(t *testing.T) {
	t.Parallel()

	r := corehttp.NewRouter(corehttp.RouterOptions{
		Version: "test",
		Middlewares: corehttp.APIGuards(corehttp.GuardOptions{
			Authenticator: fixedAuthenticator{err: errors.New("invalid")},
			AdminExempt:   []string{"/admin/v1/auth/login"},
			Limiter:       corehttp.NewMemoryLimiter(1, time.Minute),
		}),
	})
	r.Post("/admin/v1/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rec := call(r, httptest.NewRequest(http.MethodPost, "/admin/v1/auth/login", http.NoBody))
	assert.Equal(t, http.StatusOK, rec.Code, "the login endpoint must not ask for identity")

	// The exemption is ONLY from the identity ring: the rate limit has to run on the
	// login endpoint too, because an unguarded endpoint is exactly what brute force targets.
	second := call(r, httptest.NewRequest(http.MethodPost, "/admin/v1/auth/login", http.NoBody))
	assert.Equal(t, http.StatusTooManyRequests, second.Code,
		"the login endpoint is unguarded but NOT unlimited")
}

// TestDeferredAuthenticatorRejectsBeforeBinding verifies that an unbound
// authenticator does not quietly let requests through (ADR 0007).
func TestDeferredAuthenticatorRejectsBeforeBinding(t *testing.T) {
	t.Parallel()

	var d corehttp.DeferredAuthenticator

	_, err := d.AuthenticateAdmin(context.Background(), "bearer", "x")
	assert.True(t, coreerrors.IsUnauthorized(err),
		"an unbound authenticator has to return an authentication error, it returned %v", err)

	_, err = d.AuthenticateStore(context.Background(), "pk_x")
	assert.True(t, coreerrors.IsUnauthorized(err))
}

// TestAPIGuardsLimitTheIdentityFreePrefixToo verifies that an endpoint asking
// for no identity is NOT one without a quota.
//
// Identity and quota are SEPARATE decisions. Uploaded files are served without
// identity because the <img> tag in the storefront cannot send a header — but
// every request does a database read and a disk access. Leaving it without a
// quota would leave a load that can be thrown at us without even paying the
// authentication cost.
func TestAPIGuardsLimitTheIdentityFreePrefixToo(t *testing.T) {
	t.Parallel()

	r := corehttp.NewRouter(corehttp.RouterOptions{
		Version: "test",
		Middlewares: corehttp.APIGuards(corehttp.GuardOptions{
			Authenticator: fixedAuthenticator{err: errors.New("invalid")},
			Limiter:       corehttp.NewMemoryLimiter(1, time.Minute),
			OpenPrefixes:  []string{"/files"},
		}),
	})
	r.Get("/files/{key}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	first := call(r, httptest.NewRequest(http.MethodGet, "/files/abc.png", http.NoBody))
	assert.Equal(t, http.StatusOK, first.Code, "an identity-free endpoint ASKS FOR NO identity")

	second := call(r, httptest.NewRequest(http.MethodGet, "/files/abc.png", http.NoBody))
	assert.Equal(t, http.StatusTooManyRequests, second.Code,
		"being identity-free is NOT being quota-free")

	// The health endpoint is identity-free AND quota-free; having it hit the quota
	// would make the orchestrator pull a healthy instance out of traffic.
	health := call(r, httptest.NewRequest(http.MethodGet, "/health", http.NoBody))
	assert.Equal(t, http.StatusOK, health.Code, "the health endpoint must not enter the quota")
}

// TestIdempotencyDoesNotBufferAStreamingBody verifies that the body of a
// multipart request is NOT READ by the idempotency middleware.
//
// Were it read, two things would break at once: streaming would lose its meaning
// (the same bytes both in memory and on disk) and the middleware's own 1 MiB
// buffer would engage BEFORE the upload endpoint's far larger limit and report
// the wrong limit to the client.
func TestIdempotencyDoesNotBufferAStreamingBody(t *testing.T) {
	t.Parallel()

	var readLength int

	r := corehttp.NewRouter(corehttp.RouterOptions{
		Version: "test",
		Middlewares: corehttp.APIGuards(corehttp.GuardOptions{
			Authenticator:    fixedAuthenticator{principal: corehttp.Principal{ID: "usr_1", Kind: "user"}},
			IdempotencyStore: corehttp.NewMemoryIdempotencyStore(time.Hour, 0),
		}),
	})
	r.Post("/admin/v1/uploads", func(w http.ResponseWriter, req *http.Request) {
		// The handler has to be able to read the body IN FULL: if the middleware
		// consumed it, zero bytes arrive here.
		b, _ := io.ReadAll(req.Body)
		readLength = len(b)

		w.WriteHeader(http.StatusCreated)
	})

	body := strings.Repeat("x", 4096)
	req := httptest.NewRequest(http.MethodPost, "/admin/v1/uploads", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "multipart/form-data; boundary=bnd")
	req.Header.Set(corehttp.IdempotencyKeyHeader, "upload-1")

	rec := call(r, req)

	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, len(body), readLength,
		"the multipart body has to reach the handler IN FULL; the middleware must not consume it")
	assert.Empty(t, rec.Header().Get(corehttp.IdempotencyReplayedHeader),
		"a streaming body is not recorded, so it cannot be replayed")
}

// TestAPIGuardsAnExemptPathIsNotRecordedForIdempotency verifies that an exempt
// path's response is NOT STORED and that every request runs again.
//
// The fault measured was this: an endpoint that answers with a 200 even in the
// error case (that is what the GraphQL contract does) falls outside the "a 5xx is
// not recorded" guard. The handler below imitates exactly that — first an error
// body inside a 200, then a fixed response. Without the exemption the second
// request would get the FIRST body back with Idempotency-Replayed for the whole
// TTL (24 hours by default), even after the fault was fixed.
func TestAPIGuardsAnExemptPathIsNotRecordedForIdempotency(t *testing.T) {
	t.Parallel()

	const exemptPath = "/store/v1/graphql"

	faulty := true

	r := corehttp.NewRouter(corehttp.RouterOptions{
		Version: "test",
		Middlewares: corehttp.APIGuards(corehttp.GuardOptions{
			Authenticator:     fixedAuthenticator{principal: corehttp.Principal{ID: "pk_1", Kind: "api_key"}},
			IdempotencyStore:  corehttp.NewMemoryIdempotencyStore(time.Hour, 0),
			IdempotencyExempt: []string{exemptPath},
		}),
	})
	r.Post(exemptPath, func(w http.ResponseWriter, _ *http.Request) {
		// The status code is 200 in BOTH cases; only the body carries the difference.
		w.WriteHeader(http.StatusOK)

		if faulty {
			_, _ = w.Write([]byte(`{"errors":[{"message":"internal error"}]}`))
			return
		}

		_, _ = w.Write([]byte(`{"data":{"products":{"count":42}}}`))
	})
	r.Post("/store/v1/carts", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"id":"cart_1"}}`))
	})

	makeReq := func(path string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"query":"{ products { count } }"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(corehttp.PublishableKeyHeader, "pk_test")
		req.Header.Set(corehttp.IdempotencyKeyHeader, "same-key")

		return req
	}

	first := call(r, makeReq(exemptPath))
	require.Equal(t, http.StatusOK, first.Code)
	require.Contains(t, first.Body.String(), "internal error")

	faulty = false

	second := call(r, makeReq(exemptPath))
	assert.Equal(t, http.StatusOK, second.Code)
	assert.Empty(t, second.Header().Get(corehttp.IdempotencyReplayedHeader),
		"on an exempt path no record is taken at all, so there is nothing to replay either")
	assert.Contains(t, second.Body.String(), `"count":42`,
		"after the fault is fixed the client has to get the CURRENT response, not a 24-hour-old record")

	// The exemption is on the FULL PATH: another endpoint under the same prefix
	// keeps being recorded. Otherwise a single exemption would quietly remove the
	// guard from the whole surface.
	cartFirst := call(r, makeReq("/store/v1/carts"))
	require.Equal(t, http.StatusOK, cartFirst.Code)

	cartSecond := call(r, makeReq("/store/v1/carts"))
	assert.Equal(t, "true", cartSecond.Header().Get(corehttp.IdempotencyReplayedHeader),
		"an endpoint that is NOT exempt has to replay the record for the same key")
}

// TestAPIGuardsAnExemptPathStillGoesThroughIdentityAndQuota draws the SCOPE of
// the exemption.
//
// The exemption is only from the idempotency ring. Applied to the whole stack by
// accident, the decision to leave a read endpoint out of the record would quietly
// take that endpoint out of authentication and out of the quota too — the
// storefront catalog would become readable without a key.
func TestAPIGuardsAnExemptPathStillGoesThroughIdentityAndQuota(t *testing.T) {
	t.Parallel()

	const exemptPath = "/store/v1/graphql"

	r := corehttp.NewRouter(corehttp.RouterOptions{
		Version: "test",
		Middlewares: corehttp.APIGuards(corehttp.GuardOptions{
			Authenticator:     fixedAuthenticator{err: errors.New("invalid")},
			Limiter:           corehttp.NewMemoryLimiter(1, time.Minute),
			IdempotencyStore:  corehttp.NewMemoryIdempotencyStore(time.Hour, 0),
			IdempotencyExempt: []string{exemptPath},
		}),
	})
	r.Post(exemptPath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	first := call(r, httptest.NewRequest(http.MethodPost, exemptPath, http.NoBody))
	assert.Equal(t, http.StatusUnauthorized, first.Code,
		"an idempotency exemption does not remove authentication")

	second := call(r, httptest.NewRequest(http.MethodPost, exemptPath, http.NoBody))
	assert.Equal(t, http.StatusTooManyRequests, second.Code,
		"an idempotency exemption does not remove the quota either")
}
