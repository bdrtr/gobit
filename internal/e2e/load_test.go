//go:build integration

package e2e

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	authmodels "github.com/bdrtr/gobit/internal/modules/auth/models"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
	"github.com/bdrtr/gobit/internal/rig"
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
// # It used to measure an EMPTY catalog
//
// Until the rig became rebuildable this test seeded nothing and asserted
// nothing about the response body. TestMain's harness creates regions, tax
// fixtures, an identity and one stock location and NOT ONE PRODUCT, and the
// `make load-test` target selects this test alone, so no other file's fixtures
// ran either. The listing therefore returned an empty page, the count query
// counted nothing, and the target printed a green requests/s line for a catalog
// of zero products.
//
// That is the same failure class the Makefile's dead -run pattern already cost
// this repository — a check that sees nothing being indistinguishable from a
// check that passes — except the emptiness was in the DATA, where no selector
// gate can see it. So the test now BUILDS what it measures over, through the
// same generator that rebuilds the measurement rig (internal/rig), and refuses
// to report on a void: the catalog is asserted non-empty before the first
// request and every response is checked for at least one product.
//
// # Why its own channel and its own key
//
// The seeded products are assigned to a sales channel this test mints for
// itself, so they are invisible to every other scenario in the package: the
// storefront's visibility rule shows a product with an assignment only in the
// channel it is assigned to. A load fixture that leaked into the shared
// storefront would silently change what the catalog tests see.
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
	// defaultLoadProducts is how many single-variant products are seeded.
	//
	// It is two hundred rather than the rig's fifty thousand because this test
	// measures CORRECTNESS UNDER CONCURRENCY and not catalog scale; the full
	// rig takes about fourteen seconds to build and every integration run would
	// pay it. What the size has to be is NON-ZERO — enough to fill the page the
	// requests ask for, so that "the handler returned 200" and "the handler
	// returned something" stop being the same observation.
	defaultLoadProducts = 200
	// p99Ceiling is the highest accepted 99th percentile latency.
	//
	// It is extremely generous for an in-process call; that is deliberate. A
	// tight threshold would go red on a slow CI machine without any real
	// regression and destroy the test's trustworthiness.
	p99Ceiling = 2 * time.Second
	// loadChannelName is the sales channel the load fixture is assigned to.
	loadChannelName = "e2e-load"
	// loadPageSize is the page the requests ask for.
	loadPageSize = 10
)

// productIDMarker is what a response body has to carry for the page not to be
// empty.
//
// A substring check rather than a JSON decode, and the reason is the loop it
// runs in: decoding ten products on every one of a thousand concurrent requests
// would measure the test's own decoder as much as the server. The marker is the
// id prefix every seeded product carries, so a 200 over an empty page — the
// exact failure this test used to have — cannot pass it.
var productIDMarker = []byte(`"id":"prod_`)

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

	loadKey := seedLoadCatalog(t)

	var (
		mu          sync.Mutex
		durations   = make([]time.Duration, 0, requestCount)
		failedCount atomic.Int64
		server5xx   atomic.Int64
		emptyPage   atomic.Int64
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
				request := httptest.NewRequest(http.MethodGet, loadListPath, http.NoBody)
				request.Header.Set(corehttp.PublishableKeyHeader, loadKey)

				recorder := httptest.NewRecorder()

				requestStart := time.Now()
				testRouter.ServeHTTP(recorder, request)
				elapsed := time.Since(requestStart)

				switch {
				case recorder.Code >= http.StatusInternalServerError:
					server5xx.Add(1)
				case recorder.Code != http.StatusOK:
					failedCount.Add(1)
				case !bytes.Contains(recorder.Body.Bytes(), productIDMarker):
					emptyPage.Add(1)
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
	assert.Zero(t, emptyPage.Load(),
		"every response must carry products; a 200 over an empty page is the failure this "+
			"test reported as green for as long as it seeded nothing")
	assert.Less(t, p99, p99Ceiling, "the p99 latency must stay under the ceiling")
}

// loadListPath is the endpoint the load is aimed at.
var loadListPath = "/store/v1/products?limit=" + strconv.Itoa(loadPageSize)

// seedLoadCatalog builds the catalog this test measures over and returns the
// publishable key that can see it.
//
// The generator is the same one `gobit seed` rebuilds the measurement rig with,
// at a much smaller size. That sharing is worth more than the fixture: the
// seeder writes bulk SQL naming other modules' tables, and this is the place
// where that SQL is run against a schema built by the modules' OWN migrations
// on every integration run. A column the seeder names that a migration renamed
// fails HERE, on the commit that renamed it, instead of a year later in front
// of whoever next tried to rebuild the rig.
func seedLoadCatalog(t *testing.T) string {
	t.Helper()

	ctx := context.Background()

	channel, err := authSvc.CreateSalesChannel(ctx, authsvc.SalesChannelInput{
		Name:        loadChannelName,
		Description: "the baseline load test's own storefront",
	})
	require.NoError(t, err, "the load test's sales channel could not be created")

	_, key, err := authSvc.CreateAPIKey(ctx, authsvc.CreateAPIKeyInput{
		Type:            authmodels.APIKeyPublishable,
		Title:           "e2e load key",
		CreatedBy:       adminID,
		SalesChannelIDs: []string{channel.ID},
	})
	require.NoError(t, err, "the load test's publishable key could not be created")

	// The spec starts from the rig's own defaults and only the two family sizes
	// are lowered, so every statement the generator has — the taxonomy included
	// — is exercised here. Zeroing the categories would leave two of its
	// statements unrun by any test and free to rot.
	spec := rig.DefaultSpec()
	spec.SingleVariantProducts = envInt(t, "GOBIT_LOAD_PRODUCTS", defaultLoadProducts)
	spec.MultiVariantProducts = max(spec.SingleVariantProducts/10, 1)
	spec.SalesChannelID = channel.ID

	counts, err := rig.Seed(ctx, testPool, spec)
	require.NoError(t, err, "the load catalog could not be seeded")
	require.Positive(t, counts.Of(rig.ProductTable), "the seeded catalog must not be empty")

	// The claim is checked THROUGH THE STOREFRONT and not against the table: a
	// row that exists but is not visible in this channel would satisfy a count
	// over product and still leave the load measuring an empty page.
	catalog := vitrinKatalogu(t, key, url.Values{"limit": {strconv.Itoa(loadPageSize)}})
	require.Len(t, catalog.Data, loadPageSize,
		"the first page must be FULL; a shorter page means the load would be measured "+
			"over fewer products than were seeded")
	require.GreaterOrEqual(t, catalog.Count, spec.SingleVariantProducts+spec.MultiVariantProducts,
		"the storefront must count at least every product seeded into this channel")

	return key
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
