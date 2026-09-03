package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// errDown is the failure a broken dependency reports.
var errDown = errors.New("dial tcp: connection refused")

// down is a check that always fails.
func down(context.Context) error { return errDown }

// up is a check that always passes.
func up(context.Context) error { return nil }

// dependencyTimeout is a check that times out on its OWN clock, the way a
// dependency with its own connect timeout does: it answers immediately, and the
// answer wraps context.DeadlineExceeded without the probe budget having expired.
// pgx produces exactly this shape when a dial hits ConnectTimeout.
func dependencyTimeout(context.Context) error {
	return fmt.Errorf("dial tcp 10.0.0.1:5432: %w", context.DeadlineExceeded)
}

// blocked is a check that never answers on its own and only returns when its
// budget runs out. It stands in for the real failure shape of an unreachable
// Redis, which is not a fast error but a dial that keeps retrying: measured at
// 1.7s per call against a closed port, which is longer than a kubelet's default
// one-second probe timeout.
func blocked(ctx context.Context) error {
	<-ctx.Done()

	return ctx.Err()
}

// readinessLogger returns a logger writing JSON lines into a buffer, so a test
// can assert on what an operator would actually see.
func readinessLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer

	return slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

// readyBody is the decoded /ready response.
type readyBody struct {
	Status string `json:"status"`
	Checks map[string]struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	} `json:"checks"`
}

// callReady probes /ready on a router built from the given options and returns
// the status code, the decoded body and how long the request took.
func callReady(t *testing.T, opts corehttp.RouterOptions) (int, readyBody, time.Duration) {
	t.Helper()

	r := corehttp.NewRouter(opts)
	req := httptest.NewRequest(http.MethodGet, "/ready", http.NoBody)
	rec := httptest.NewRecorder()

	start := time.Now()
	r.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	var body readyBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body),
		"the /ready body is not JSON: %s", rec.Body.String())

	return rec.Code, body, elapsed
}

// TestReadyGatingCheckFailureLeavesTraffic proves a dependency whose loss
// DISABLES the instance still empties it out of the load balancer.
//
// This is the half of readiness that must not be weakened while relaxing the
// other half: Postgres is registered here, and an instance whose pool is gone
// answers no endpoint correctly, so keeping it in traffic would serve errors to
// shoppers a healthy replica could have served.
func TestReadyGatingCheckFailureLeavesTraffic(t *testing.T) {
	t.Parallel()

	code, body, _ := callReady(t, corehttp.RouterOptions{
		ReadinessChecks: map[string]corehttp.HealthCheck{"postgres": down},
	})

	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Equal(t, "unavailable", body.Status)
	assert.Equal(t, "error", body.Checks["postgres"].Status)
	assert.Contains(t, body.Checks["postgres"].Error, "connection refused",
		"the reason must reach the operator, not just the status code")
}

// TestReadyDegradingCheckFailureKeepsTraffic proves a dependency whose loss
// only DEGRADES the instance leaves it serving.
//
// Redis is that dependency. Every replica shares one Redis, so a gate here
// fails every replica in the same second and Kubernetes empties the Service —
// the "fail-closed for everything" outage ADR 0007 rejected. Measured in
// cmd/server's TestRedisOutageMeasurement, an instance in this state still
// answers catalog reads with 200.
func TestReadyDegradingCheckFailureKeepsTraffic(t *testing.T) {
	t.Parallel()

	code, body, _ := callReady(t, corehttp.RouterOptions{
		ReadinessChecks: map[string]corehttp.HealthCheck{"postgres": up},
		DegradedChecks:  map[string]corehttp.HealthCheck{"redis": down},
	})

	assert.Equal(t, http.StatusOK, code, "a degrading dependency must not pull the instance out of traffic")
	assert.Equal(t, "degraded", body.Status,
		"200 alone would make the outage invisible; the body is where the operator sees it")
	assert.Equal(t, "error", body.Checks["redis"].Status)
	assert.Contains(t, body.Checks["redis"].Error, "connection refused")
	assert.Equal(t, "ok", body.Checks["postgres"].Status,
		"the healthy checks must still be listed, or a degraded body says nothing about what is left")
}

// TestReadyEverythingHealthy proves the degraded status is not sticky: both
// classes reported, status ok, code 200.
func TestReadyEverythingHealthy(t *testing.T) {
	t.Parallel()

	code, body, _ := callReady(t, corehttp.RouterOptions{
		ReadinessChecks: map[string]corehttp.HealthCheck{"postgres": up},
		DegradedChecks:  map[string]corehttp.HealthCheck{"redis": up},
	})

	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "ok", body.Status)
	assert.Equal(t, "ok", body.Checks["postgres"].Status)
	assert.Equal(t, "ok", body.Checks["redis"].Status)
}

// TestReadyGatingFailureOutranksDegrading proves the two classes do not race
// for the reported status when both fail.
//
// The checks run concurrently and the results land in a map, so an
// implementation that simply overwrote the status as results arrived would
// report "degraded" with a 200 about half the time — an instance with no
// database silently kept in traffic, and the failure would be intermittent
// enough to be blamed on anything else.
func TestReadyGatingFailureOutranksDegrading(t *testing.T) {
	t.Parallel()

	for range 20 {
		code, body, _ := callReady(t, corehttp.RouterOptions{
			ReadinessChecks: map[string]corehttp.HealthCheck{"postgres": down},
			DegradedChecks:  map[string]corehttp.HealthCheck{"redis": down},
		})

		require.Equal(t, http.StatusServiceUnavailable, code)
		require.Equal(t, "unavailable", body.Status)
	}
}

// TestReadyNameInBothClassesGates proves an ambiguous configuration is read the
// safe way.
//
// A dependency the operator meant to gate on could be duplicated into the
// degrading map by a copy-paste; resolving that toward "only mentioned in a
// body" would silently downgrade a real gate.
// Resolving it safely is not enough on its own: the dropped copy is a
// configuration mistake, so it is also reported ONCE when the router is built —
// silently discarding it would leave an operator who meant to degrade waiting
// for the outage to find out.
func TestReadyNameInBothClassesGates(t *testing.T) {
	t.Parallel()

	log, buf := readinessLogger()
	code, body, _ := callReady(t, corehttp.RouterOptions{
		Logger:          log,
		ReadinessChecks: map[string]corehttp.HealthCheck{"postgres": down},
		DegradedChecks:  map[string]corehttp.HealthCheck{"postgres": up},
	})

	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Equal(t, "unavailable", body.Status)
	assert.Len(t, body.Checks, 1, "the name must be probed once, not twice")
	assert.Contains(t, buf.String(), "BOTH sides",
		"the dropped degrading copy must be reported: %s", buf.String())
}

// TestReadyDegradingCheckCannotSlowTheProbe is the test that keeps the fix from
// being undone by a timeout.
//
// Returning 200 during a Redis outage is worthless if producing that 200 takes
// longer than the kubelet is willing to wait: readinessProbe.timeoutSeconds
// defaults to 1, an unreachable Redis Ping was measured at 1.7s (five dial
// attempts), and a probe that times out is scored exactly like a 503 — every
// replica NotReady, the outage the split exists to prevent.
//
// The check here blocks until its own budget expires, so the elapsed time IS
// the cap. The gating budget is left at its 5s default: if the degrading check
// ever fell under that budget instead of its own, this would take 5s.
func TestReadyDegradingCheckCannotSlowTheProbe(t *testing.T) {
	t.Parallel()

	code, body, elapsed := callReady(t, corehttp.RouterOptions{
		ReadinessChecks: map[string]corehttp.HealthCheck{"postgres": up},
		DegradedChecks:  map[string]corehttp.HealthCheck{"redis": blocked},
	})

	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "degraded", body.Status)
	assert.Less(t, elapsed, time.Second,
		"the default degrading budget is 250ms; anything at or past a second is a probe timeout")
	assert.GreaterOrEqual(t, elapsed, 200*time.Millisecond,
		"finishing early would mean the budget under test was not the one applied")
	assert.Equal(t, "no answer within the 250ms probe budget", body.Checks["redis"].Error,
		"a bare context error reads like a bug in gobit and names neither the budget nor the dependency")
}

// TestReadyCallerDeadlineIsNotBlamedOnTheBudget guards the sentence the
// operator reads when the CALLER gave up first.
//
// The probe budget is only an explanation when the probe budget is what
// expired. If the kubelet's own timeout fires while a healthy dependency is
// mid-answer, reporting "no answer within the 250ms probe budget" would send an
// operator hunting a dependency that was never slow.
func TestReadyCallerDeadlineIsNotBlamedOnTheBudget(t *testing.T) {
	t.Parallel()

	r := corehttp.NewRouter(corehttp.RouterOptions{
		DegradedChecks: map[string]corehttp.HealthCheck{"redis": blocked},
	})

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/ready", http.NoBody).WithContext(ctx)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var body readyBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "the body is not JSON: %s", rec.Body.String())
	assert.NotContains(t, body.Checks["redis"].Error, "probe budget",
		"the caller's deadline expired, not the probe's")
}

// TestReadyDependencyTimeoutIsNotBlamedOnTheBudget proves the budget sentence is
// only used when the BUDGET is what expired.
//
// A dependency carrying its own timeout reports context.DeadlineExceeded of its
// own making — pgx normalizes a dial timeout into exactly that — while the probe
// budget is still wide open. Attributing it to the budget would print "no answer
// within the 5s probe budget" for a check that gave up at 2s and send an
// operator to tune the wrong number. The distinction cannot be made from the
// ERROR (both are the same sentinel); it can only be made from the probe
// context, which is what the code asks.
func TestReadyDependencyTimeoutIsNotBlamedOnTheBudget(t *testing.T) {
	t.Parallel()

	code, body, _ := callReady(t, corehttp.RouterOptions{
		ReadinessChecks: map[string]corehttp.HealthCheck{"postgres": dependencyTimeout},
	})

	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.NotContains(t, body.Checks["postgres"].Error, "probe budget",
		"the dependency's own timeout is not the probe budget")
	assert.Contains(t, body.Checks["postgres"].Error, "10.0.0.1:5432",
		"the dependency's own error must reach the operator")
}

// TestReadyDegradingFailureIsLogged proves the WARN line exists, because it is
// the ONLY alerting channel a degradation has.
//
// A degrading failure keeps the 200, so kubectl shows the pod Ready and no
// orchestrator event fires; if the log line went missing, a Redis outage would
// be visible solely to whoever thought to curl /ready. The line must also say
// the instance keeps serving — a bare "a readiness check failed" reads like an
// outage and would send an operator looking for a pod that left traffic.
func TestReadyDegradingFailureIsLogged(t *testing.T) {
	t.Parallel()

	log, buf := readinessLogger()
	_, _, _ = callReady(t, corehttp.RouterOptions{
		Logger:         log,
		DegradedChecks: map[string]corehttp.HealthCheck{"redis": down},
	})

	line := buf.String()
	assert.Contains(t, line, "\"level\":\"WARN\"", "the degradation must be logged at WARN: %s", line)
	assert.Contains(t, line, "KEEPS SERVING", "the line must say the instance stays in traffic: %s", line)
	assert.Contains(t, line, "redis", "the line must name the dependency: %s", line)
	assert.Contains(t, line, errDown.Error(), "the line must carry the error: %s", line)
}

// TestReadyGatingFailureIsLoggedDifferently proves the two WARN lines are
// DISTINGUISHABLE.
//
// Both classes log at WARN, so a log-based alert can only tell an outage from a
// degradation by the text. If the two lines were the same sentence, the split
// this endpoint makes would exist in the status code and nowhere else.
func TestReadyGatingFailureIsLoggedDifferently(t *testing.T) {
	t.Parallel()

	log, buf := readinessLogger()
	_, _, _ = callReady(t, corehttp.RouterOptions{
		Logger:          log,
		ReadinessChecks: map[string]corehttp.HealthCheck{"postgres": down},
	})

	line := buf.String()
	assert.Contains(t, line, "leaving traffic", "the gating line must say the instance leaves traffic: %s", line)
	assert.NotContains(t, line, "KEEPS SERVING", "the gating line must not claim the instance stays: %s", line)
}

// TestReadyDegradingBudgetIsConfigurable proves the cap is an operator's number
// and not a constant buried in the binary.
func TestReadyDegradingBudgetIsConfigurable(t *testing.T) {
	t.Parallel()

	code, body, elapsed := callReady(t, corehttp.RouterOptions{
		DegradedChecks:       map[string]corehttp.HealthCheck{"redis": blocked},
		DegradedCheckTimeout: 20 * time.Millisecond,
	})

	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "degraded", body.Status)
	assert.Less(t, elapsed, 200*time.Millisecond, "the configured budget replaced the 250ms default")
}

// TestReadyGatingBudgetIsStillGenerous guards the other direction: the tight
// degrading budget must not have been applied to the gating checks, whose
// dependency is worth waiting seconds for — a Postgres that answers in 400ms
// under load is slow, not absent, and cutting it off would take a serving
// instance out of traffic.
func TestReadyGatingBudgetIsStillGenerous(t *testing.T) {
	t.Parallel()

	slow := func(ctx context.Context) error {
		select {
		case <-time.After(400 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	code, body, _ := callReady(t, corehttp.RouterOptions{
		ReadinessChecks: map[string]corehttp.HealthCheck{"postgres": slow},
	})

	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "ok", body.Status)
}

// TestReadyProbesTheMapAsItStandsNow proves a check registered AFTER the router
// was built is still probed.
//
// The server fills these maps while wiring up its dependencies, and the order
// of that wiring is easy to change by accident. A router that snapshotted the
// maps once would then stop probing a dependency entirely — and an unprobed
// dependency reports nothing at all: no 503, no degraded body, no log line. Of
// every way this endpoint can be wrong, that is the only one with no symptom.
func TestReadyProbesTheMapAsItStandsNow(t *testing.T) {
	t.Parallel()

	degrading := map[string]corehttp.HealthCheck{}
	r := corehttp.NewRouter(corehttp.RouterOptions{DegradedChecks: degrading})

	degrading["redis"] = down

	req := httptest.NewRequest(http.MethodGet, "/ready", http.NoBody)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var body readyBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "the body is not JSON: %s", rec.Body.String())
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "degraded", body.Status)
	assert.Equal(t, "error", body.Checks["redis"].Status)
}

// TestReadyWithoutChecksIsReady keeps the documented behavior of an
// installation that registered nothing.
func TestReadyWithoutChecksIsReady(t *testing.T) {
	t.Parallel()

	code, body, _ := callReady(t, corehttp.RouterOptions{})

	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "ok", body.Status)
	assert.Empty(t, body.Checks)
}
