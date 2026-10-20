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

// failingLimiter cannot count.
type failingLimiter struct{}

func (failingLimiter) Allow(context.Context, string) (corehttp.Decision, error) {
	return corehttp.Decision{}, errors.New("redis is down")
}

// quotaHandler is a reached counter behind the quotas, each client the
// request's X-Client header.
func quotaHandler(quotas ...corehttp.RouteQuota) (handler http.Handler, reachedCount *int) {
	reached := 0
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached++ })
	key := func(r *http.Request) string { return r.Header.Get("X-Client") }

	return corehttp.RouteQuotas(quotas, key)(next), &reached
}

// quotaCall sends one request and answers its status.
func quotaCall(h http.Handler, method, path, client string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, http.NoBody)
	r.Header.Set("X-Client", client)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	return rec
}

// TestARouteQuotaCountsItsRoutesTogetherPerClient is ADR 0452: the routes a
// quota names share one count per client, a parameter matches any segment,
// a route it does not name is not counted, and a spent quota answers 429
// rate_limited with Retry-After before the handler.
func TestARouteQuotaCountsItsRoutesTogetherPerClient(t *testing.T) {
	h, reached := quotaHandler(corehttp.RouteQuota{
		Name:    "sign-in",
		Routes:  []string{"POST /admin/v1/auth/login", "POST /store/v1/orders/{id}/returns"},
		Limiter: corehttp.NewMemoryLimiter(2, time.Hour),
	})

	assert.Equal(t, http.StatusOK, quotaCall(h, http.MethodPost, "/admin/v1/auth/login", "a").Code)
	assert.Equal(t, http.StatusOK, quotaCall(h, http.MethodPost, "/store/v1/orders/order_1/returns", "a").Code)
	refused := quotaCall(h, http.MethodPost, "/store/v1/orders/order_2/returns", "a")
	require.Equal(t, http.StatusTooManyRequests, refused.Code, "the two routes spend one quota")
	assert.NotEmpty(t, refused.Header().Get("Retry-After"))
	assert.Contains(t, refused.Body.String(), corehttp.CodeRateLimited)
	assert.Equal(t, 2, *reached, "a refused request never reaches the handler")

	assert.Equal(t, http.StatusOK, quotaCall(h, http.MethodPost, "/admin/v1/auth/login", "b").Code, "another client")
	for _, path := range []string{"/admin/v1/auth/login/x", "/store/v1/orders//returns", "/admin/v1/auth"} {
		assert.Equal(t, http.StatusOK, quotaCall(h, http.MethodPost, path, "a").Code, "%s is not a named route", path)
	}
	assert.Equal(t, http.StatusOK, quotaCall(h, http.MethodGet, "/admin/v1/auth/login", "a").Code, "another method")
}

// TestARequestSpendsEveryQuotaThatNamesItsRoute is ADR 0452: a burst and an
// hourly quota on one route are both spent; a quota that cannot count lets
// the request through, as the whole-API limit does (ADR 0007).
func TestARequestSpendsEveryQuotaThatNamesItsRoute(t *testing.T) {
	route := []string{"POST /admin/v1/auth/login"}
	h, reached := quotaHandler(
		corehttp.RouteQuota{Name: "down", Routes: route, Limiter: failingLimiter{}},
		corehttp.RouteQuota{Name: "burst", Routes: route, Limiter: corehttp.NewMemoryLimiter(5, time.Minute)},
		corehttp.RouteQuota{Name: "hour", Routes: route, Limiter: corehttp.NewMemoryLimiter(1, time.Hour)},
	)

	assert.Equal(t, http.StatusOK, quotaCall(h, http.MethodPost, "/admin/v1/auth/login", "a").Code)
	assert.Equal(t, http.StatusTooManyRequests, quotaCall(h, http.MethodPost, "/admin/v1/auth/login", "a").Code,
		"the hourly quota refuses though the burst has room")
	assert.Equal(t, 1, *reached)
}

// TestAQuotaRouteIsOneRoute is ADR 0452's spelling: "METHOD /path", a method
// the API binds, and no catch-all, query or percent-encoded byte.
func TestAQuotaRouteIsOneRoute(t *testing.T) {
	method, pattern, err := corehttp.ParseQuotaRoute("POST /store/v1/carts/{id}/payment-sessions")
	require.NoError(t, err)
	assert.Equal(t, http.MethodPost, method)
	assert.Equal(t, "/store/v1/carts/{id}/payment-sessions", pattern)

	for _, bad := range []string{
		"/admin/v1/auth/login", "post /admin/v1/auth/login", "OPTIONS /admin/v1/x", "POST admin/v1",
		"POST /admin/v1/*", "POST /admin/v1/a%2Fb", "POST /admin/v1/x?y=1", "POST  /admin/v1/x",
	} {
		_, _, err := corehttp.ParseQuotaRoute(bad)
		assert.Error(t, err, bad)
	}

	require.NoError(t, corehttp.CheckRouteQuotaName("dtm.auth-burst_1"))
	for _, bad := range []string{"", "auth burst", "auth:burst", "auth/burst"} {
		assert.Error(t, corehttp.CheckRouteQuotaName(bad), bad)
	}
}

// TestARouteQuotaStandsBeforeIdentity is ADR 0452 in the guard stack: a
// spent quota answers before the identity guard, so a refused sign-in costs
// no password hash, and it is counted with the whole-API limit off.
func TestARouteQuotaStandsBeforeIdentity(t *testing.T) {
	stack := corehttp.APIGuards(corehttp.GuardOptions{
		AdminExempt: []string{"/admin/v1/auth/login"},
		LimitKey:    func(*http.Request) string { return "one-client" },
		RouteQuotas: []corehttp.RouteQuota{{
			Name: "admin-sign-in", Routes: []string{"POST /admin/v1/auth/login", "POST /admin/v1/orders"},
			Limiter: corehttp.NewMemoryLimiter(1, time.Hour),
		}},
	})
	var h http.Handler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for i := len(stack) - 1; i >= 0; i-- {
		h = stack[i](h)
	}

	assert.Equal(t, http.StatusOK, quotaCall(h, http.MethodPost, "/admin/v1/auth/login", "").Code)
	assert.Equal(t, http.StatusTooManyRequests, quotaCall(h, http.MethodPost, "/admin/v1/auth/login", "").Code)
	assert.Equal(t, http.StatusTooManyRequests, quotaCall(h, http.MethodPost, "/admin/v1/orders", "").Code,
		"refused before the identity guard would have answered 401")
}

// TestAnIPv6ClientIsCountedByItsNetwork is ADR 0452: a host rotating the low
// half of one /64 is one client to a quota, another /64 is another, and an
// IPv4 address is counted as it is.
func TestAnIPv6ClientIsCountedByItsNetwork(t *testing.T) {
	h, reached := quotaHandler(corehttp.RouteQuota{
		Name:    "sign-in",
		Routes:  []string{"POST /admin/v1/auth/login"},
		Limiter: corehttp.NewMemoryLimiter(2, time.Hour),
	})

	assert.Equal(t, http.StatusOK, quotaCall(h, http.MethodPost, "/admin/v1/auth/login", "2001:db8:1:2::1").Code)
	assert.Equal(t, http.StatusOK, quotaCall(h, http.MethodPost, "/admin/v1/auth/login", "2001:db8:1:2::2").Code)
	assert.Equal(t, http.StatusTooManyRequests,
		quotaCall(h, http.MethodPost, "/admin/v1/auth/login", "2001:db8:1:2:ffff::3").Code,
		"the third address of one /64 meets the same quota")
	assert.Equal(t, http.StatusOK, quotaCall(h, http.MethodPost, "/admin/v1/auth/login", "2001:db8:1:3::1").Code)
	assert.Equal(t, http.StatusOK, quotaCall(h, http.MethodPost, "/admin/v1/auth/login", "203.0.113.1").Code)
	assert.Equal(t, http.StatusOK, quotaCall(h, http.MethodPost, "/admin/v1/auth/login", "203.0.113.2").Code)
	assert.Equal(t, 5, *reached)
}
