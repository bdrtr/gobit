//go:build integration

// This file needs a real Redis and is only compiled with `-tags=integration`
// (`make test-integration`), so `make test` stays fast and Docker-free.
package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/bdrtr/gobit/core/container"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/core/config"
)

// redisImage is the Redis image the integration tests run against.
const redisImage = "redis:7-alpine"

// probeReady calls /ready and returns the code, the decoded body and how long
// the call took.
func probeReady(t *testing.T, h http.Handler) (code int, body map[string]any, elapsed time.Duration) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/ready", http.NoBody)
	rec := httptest.NewRecorder()

	start := time.Now()
	h.ServeHTTP(rec, req)
	elapsed = time.Since(start)

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body),
		"the /ready body is not JSON: %s", rec.Body.String())

	return rec.Code, body, elapsed
}

// checkStatus reads one check's status out of a decoded /ready body.
func checkStatus(t *testing.T, body map[string]any, name string) string {
	t.Helper()

	checks, ok := body["checks"].(map[string]any)
	require.True(t, ok, "the body carries no checks: %v", body)
	entry, ok := checks[name].(map[string]any)
	require.True(t, ok, "the body does not mention %q: %v", name, checks)
	status, ok := entry["status"].(string)
	require.True(t, ok, "the check %q carries no status: %v", name, entry)

	return status
}

// TestRedisOutageKeepsTheInstanceInTraffic is the end-to-end proof of the
// readiness split, against a REAL Redis that is then killed.
//
// The unit tests in core/http prove the router honors the two check
// classes; they cannot prove the server puts Redis in the right one, and that
// single decision is what stands between a failover and a full storefront
// outage. So this test builds the probe through setupRedis — the shipped path,
// not a copy — starts a real Redis, terminates it mid-test, and asks /ready
// what the orchestrator would be told.
//
// The elapsed-time assertion matters as much as the status code: a kubelet's
// readinessProbe.timeoutSeconds defaults to 1, an unreachable Redis Ping was
// measured at 1.7s (the client dials five times), and a probe that times out is
// scored exactly like a 503. A 200 that arrives too late is not a 200.
func TestRedisOutageKeepsTheInstanceInTraffic(t *testing.T) {
	ctx := t.Context()

	redisContainer, err := tcredis.Run(ctx, redisImage)
	testcontainers.CleanupContainer(t, redisContainer)
	require.NoError(t, err, "the redis container could not be started")

	uri, err := redisContainer.ConnectionString(ctx)
	require.NoError(t, err, "the connection string could not be read")

	cfg := baseConfig()
	cfg.GuardBackend = config.BackendRedis
	cfg.RedisURL = uri

	c := container.New(discardLogger())
	defer func() {
		// The client is closed by the container; the error is ignored because
		// the connection is already gone by the end of this test.
		_ = c.Shutdown(context.WithoutCancel(ctx))
	}()

	degraded := corehttp.DegradingChecks{}
	_, err = setupRedis(ctx, c, cfg, degraded, discardLogger())
	require.NoError(t, err, "redis could not be set up")
	require.Contains(t, degraded, "redis",
		"setupRedis registered no probe at all; an unreported dependency is worse than a gating one")

	// Postgres stands in as the gating dependency: it keeps its vote on the
	// status code, and this test must not accidentally pass because nothing
	// gates any more.
	router := corehttp.NewRouter(corehttp.RouterOptions{
		Version:         "test",
		ReadinessChecks: corehttp.GatingChecks{"postgres": func(context.Context) error { return nil }},
		DegradedChecks:  degraded,
	})

	code, body, _ := probeReady(t, router)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "ok", body["status"])
	require.Equal(t, "ok", checkStatus(t, body, "redis"), "redis is alive at this point")

	require.NoError(t, redisContainer.Terminate(ctx), "the redis container could not be stopped")

	code, body, elapsed := probeReady(t, router)

	assert.Equal(t, http.StatusOK, code,
		"a Redis outage must not empty the load balancer: every replica shares this Redis and would leave at once")
	assert.Equal(t, "degraded", body["status"],
		"the operator has to be able to SEE the outage; the status code no longer says it")
	assert.Equal(t, "error", checkStatus(t, body, "redis"))
	assert.Less(t, elapsed, time.Second,
		"a probe slower than the kubelet's default 1s timeout is scored as a failure, outage included")
}
