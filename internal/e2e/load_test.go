//go:build integration

package e2e

import (
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// This file satisfies the "the baseline load test passes" item in the plan's
// Phase 9 DoD.
//
// # What it measures, what it does not
//
// WHAT IT MEASURES: that the system stays CORRECT under concurrent requests —
// no request may be dropped, none may return 5xx, the guard stack may not
// produce a race, and latency must stay under a reasonable ceiling.
//
// WHAT IT DOES NOT MEASURE: absolute performance. The measurement happens
// in-process (httptest), there is no network and no kernel stack; the numbers
// that come out are NOT a capacity plan. That is also why the threshold is
// generous: the goal is to catch a deadlock, pool exhaustion and an N+1
// explosion, not to chase milliseconds.
//
// The parameters are tunable from the environment; the defaults are small
// enough not to slow CI down:
//
//	GOBIT_LOAD_REQUESTS=5000 GOBIT_LOAD_CONCURRENCY=32 make test-integration

// Default parameters of the load test.
const (
	// defaultRequests is the total number of requests.
	defaultRequests = 1000
	// defaultConcurrency is the number of concurrent workers.
	defaultConcurrency = 16
	// p99Ceiling is the highest accepted 99th percentile latency.
	//
	// It is extremely generous for an in-process call; that is deliberate. A
	// tight threshold would go red on a slow CI machine without any real
	// regression and destroy the test's trustworthiness.
	p99Ceiling = 2 * time.Second
)

// TestStaysCorrectUnderBaselineLoad verifies that no request is dropped under
// concurrent load.
//
// The path is the STORE surface and it is called with a publishable key: the
// whole guard stack (rate limit -> identity -> idempotency) runs on every
// request, so the load exercises not only the handler but the hardening layer
// as well. If the shared state in the stack (token buckets, the idempotency
// map) produced a race, it would show up here under -race.
func TestStaysCorrectUnderBaselineLoad(t *testing.T) {
	requestCount := envInt(t, "GOBIT_LOAD_REQUESTS", defaultRequests)
	concurrency := envInt(t, "GOBIT_LOAD_CONCURRENCY", defaultConcurrency)
	require.Positive(t, concurrency, "concurrency must be positive")

	var (
		mu          sync.Mutex
		durations   = make([]time.Duration, 0, requestCount)
		failedCount atomic.Int64
		server5xx   atomic.Int64
	)

	jobs := make(chan int, requestCount)
	for i := range requestCount {
		jobs <- i
	}
	close(jobs)

	startedAt := time.Now()

	var wg sync.WaitGroup

	for range concurrency {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for range jobs {
				request := httptest.NewRequest(http.MethodGet, "/store/v1/products?limit=10", http.NoBody)
				request.Header.Set(corehttp.PublishableKeyHeader, publishableKey)

				recorder := httptest.NewRecorder()

				requestStart := time.Now()
				testRouter.ServeHTTP(recorder, request)
				elapsed := time.Since(requestStart)

				switch {
				case recorder.Code >= http.StatusInternalServerError:
					server5xx.Add(1)
				case recorder.Code != http.StatusOK:
					failedCount.Add(1)
				}

				mu.Lock()
				durations = append(durations, elapsed)
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	totalDuration := time.Since(startedAt)
	require.Len(t, durations, requestCount, "every request must complete")

	slices.Sort(durations)
	p50 := percentile(durations, 50)
	p99 := percentile(durations, 99)

	t.Logf("load: %d requests / %d concurrent, duration %s, %.0f requests/s, p50 %s, p99 %s",
		requestCount, concurrency, totalDuration.Round(time.Millisecond),
		float64(requestCount)/totalDuration.Seconds(),
		p50.Round(time.Microsecond), p99.Round(time.Microsecond))

	assert.Zero(t, server5xx.Load(), "there must be no server error under load")
	assert.Zero(t, failedCount.Load(), "no request may be rejected under load")
	assert.Less(t, p99, p99Ceiling, "the p99 latency must stay under the ceiling")
}

// percentile returns the given percentile from a SORTED slice of durations.
//
// The nearest-rank method is used: on small samples interpolation would report
// a value that was never actually measured.
func percentile(sorted []time.Duration, percent int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}

	rank := (percent*len(sorted) + 99) / 100
	if rank < 1 {
		rank = 1
	}

	if rank > len(sorted) {
		rank = len(sorted)
	}

	return sorted[rank-1]
}

// envInt reads an environment variable as a positive integer.
//
// An invalid value does NOT silently FALL BACK to the default: not telling
// someone who wrote "GOBIT_LOAD_REQUESTS=lots" that the test ran with the
// default would hide the fact that it is not measuring what they think it is.
func envInt(t *testing.T, name string, fallback int) int {
	t.Helper()

	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}

	value, err := strconv.Atoi(raw)
	require.NoError(t, err, "%s must be a number, got %q", name, raw)
	require.Positive(t, value, "%s must be positive", name)

	return value
}
