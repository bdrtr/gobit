package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
	"github.com/bdrtr/gobit/internal/core/config"
)

// outageRouter builds the PRODUCTION guard stack (GUARD_BACKEND=redis) against
// a Redis that cannot be reached, and mounts one read and two write endpoints
// under the storefront prefix.
//
// The stack comes from guardStack, not from a copy assembled here: the whole
// point of the test is what the SHIPPED configuration does during an outage,
// and a hand-built stack would drift away from it without anyone noticing.
func outageRouter(t *testing.T, handled *int) http.Handler {
	t.Helper()

	cfg := baseConfig()
	cfg.GuardBackend = config.BackendRedis

	guards, err := guardStack(cfg, validIdentity{}, &adminui.Ring{}, unconnectedRedis(), nil, discardLogger())
	require.NoError(t, err, "the guard stack could not be built")

	r := corehttp.NewRouter(corehttp.RouterOptions{
		Version:     "test",
		Middlewares: guards,
	})

	served := func(w http.ResponseWriter, _ *http.Request) {
		*handled++
		w.WriteHeader(http.StatusOK)
	}

	r.Get("/store/v1/products", served)
	r.Post("/store/v1/carts/c1/line-items", served)
	r.Post("/store/v1/carts", served)

	return r
}

// outageCall sends a storefront request carrying the publishable key.
func outageCall(h http.Handler, method, path, idempotencyKey string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
	req.Header.Set(corehttp.PublishableKeyHeader, "pk_test")
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set(corehttp.IdempotencyKeyHeader, idempotencyKey)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// TestRedisOutageMeasurement is the measurement the readiness split rests on:
// what a storefront still serves while Redis is unreachable.
//
// It is not a hypothetical. README makes GUARD_BACKEND=redis mandatory for any
// multi-instance deployment, so a production storefront runs exactly this
// configuration, and a Redis failover puts every replica in exactly this state
// at the same instant.
//
// The recorded answers, each of which follows a decision ADR 0007 made per
// component rather than a blanket rule:
//
//	catalog read                       200  the read path never touches Redis
//	write without an Idempotency-Key   200  the rate limiter fails OPEN, with a WARN
//	write with an Idempotency-Key      503  idempotency_store_unavailable, handler NOT run
//	exempt write with a key            200  cart creation is exempt from idempotency
//
// Read that table as the answer to "does losing Redis DISABLE this instance or
// DEGRADE it". Nothing is served incorrectly: the single class of request whose
// exactly-once protection cannot be honored is the single class refused, and it
// is refused per request, with a code the client can retry — not by removing
// the instance from the load balancer, which would have taken the three 200s
// down with it. That is why [corehttp.RouterOptions.DegradedChecks] and not
// [corehttp.RouterOptions.ReadinessChecks] carries the Redis probe.
//
// The 503 is asserted here as deliberately as the 200s. Serving that write
// anyway would be the "pass through when the idempotency store fails" option
// ADR 0007 rejected — the most likely way to produce the double charge
// idempotency exists to prevent.
func TestRedisOutageMeasurement(t *testing.T) {
	t.Parallel()

	handled := 0
	r := outageRouter(t, &handled)

	tests := []struct {
		name       string
		method     string
		path       string
		key        string
		wantStatus int
		wantServed bool
		wantCode   string
	}{
		{
			name: "catalog read", method: http.MethodGet, path: "/store/v1/products",
			wantStatus: http.StatusOK, wantServed: true,
		},
		{
			name: "write without an idempotency key", method: http.MethodPost, path: "/store/v1/carts/c1/line-items",
			wantStatus: http.StatusOK, wantServed: true,
		},
		{
			name: "write with an idempotency key", method: http.MethodPost, path: "/store/v1/carts/c1/line-items",
			key: "k-1", wantStatus: http.StatusServiceUnavailable, wantServed: false,
			wantCode: "idempotency_store_unavailable",
		},
		{
			name: "exempt write with an idempotency key", method: http.MethodPost, path: "/store/v1/carts",
			key: "k-2", wantStatus: http.StatusOK, wantServed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := handled
			rec := outageCall(r, tt.method, tt.path, tt.key)

			assert.Equal(t, tt.wantStatus, rec.Code, "body: %s", strings.TrimSpace(rec.Body.String()))
			assert.Equal(t, tt.wantServed, handled > before, "whether the handler ran")
			if tt.wantCode != "" {
				assert.Contains(t, rec.Body.String(), tt.wantCode,
					"the client has to be told WHICH dependency refused it, or it cannot decide to retry")
			}
		})
	}
}
