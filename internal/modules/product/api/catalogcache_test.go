package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/api"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// The catalog cache header (ADR 0151).
//
// # What these tests are really about
//
// Not that a header can be written. Three things decide whether this feature is
// safe, and each has its own test: WHICH responses carry it (the three
// channel-scoped reads and nothing else), WHEN it is written (on the success path,
// never on a refusal — a cached 404 is a product that stays missing for the length
// of the TTL) and WHAT it says (`private` unless the installation asked for
// `public`, which is the difference between a shopper's own client and a CDN
// serving a body to callers with no key).

// cachingRouter builds a router whose catalog reads carry the given policy.
func cachingRouter(catalog api.Catalog, ttl time.Duration, shared bool) chi.Router {
	r := chi.NewRouter()
	api.New(catalog, graph.Options{}).WithCatalogCache(ttl, shared).Routes(r)

	return r
}

// storeRead sends a storefront read with no identity at all.
//
// A request with no principal is one a deployment without a bound key makes, and
// corehttp.SalesChannelScope reads it as "no identity in this deployment" — which
// is what lets these tests exercise the handler without minting a key. What the
// key gates is asserted where its subject is the gate (authorization_test.go).
func storeRead(t *testing.T, r chi.Router, target string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// cachingCatalog answers every channel-scoped read with one record.
func cachingCatalog() *fakeCatalog {
	return &fakeCatalog{
		listStoreProducts: func(
			_ context.Context, _ service.StoreListOptions,
		) (service.ListResult[service.StoreProduct], error) {
			return service.ListResult[service.StoreProduct]{
				Items: []service.StoreProduct{{Product: models.Product{ID: "prod_1"}}},
			}, nil
		},
		getStoreProduct: func(
			_ context.Context, _ string, _ []string,
		) (service.StoreProduct, error) {
			return service.StoreProduct{Product: models.Product{ID: "prod_1"}}, nil
		},
		listOptionValues: func(
			_ context.Context, _ service.ListOptionValuesOptions,
		) (service.ListResult[models.OptionValuePair], error) {
			return service.ListResult[models.OptionValuePair]{
				Items: []models.OptionValuePair{{OptionTitle: "Color", Value: "red"}},
			}, nil
		},
	}
}

// channelScopedReads are the three storefront reads whose body is a function of
// the URL alone (ADR 0044).
//
// The list is DATA and the test below walks it: a fourth channel-scoped read that
// arrives without a policy is the shape this feature fails in silently — half the
// catalog cacheable and half not, so a storefront's product page is a minute
// newer than the listing that linked to it.
var channelScopedReads = map[string]string{
	"the product listing":   "/store/v1/sales-channels/sc_1/products",
	"a single product":      "/store/v1/sales-channels/sc_1/products/prod_1",
	"the option vocabulary": "/store/v1/sales-channels/sc_1/option-values",
}

// TestTheChannelScopedReadsCarryThePolicy is the decision.
func TestTheChannelScopedReadsCarryThePolicy(t *testing.T) {
	t.Parallel()

	for name, target := range channelScopedReads {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := cachingRouter(cachingCatalog(), 90*time.Second, false)

			rec := storeRead(t, r, target)

			require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
			assert.Equal(t, "private, max-age=90", rec.Header().Get("Cache-Control"),
				"every channel-scoped read has to carry the same policy; a storefront whose "+
					"listing is cacheable and whose product page is not shows a shopper two "+
					"answers about one product")
		})
	}
}

// TestASharedPolicySaysPublic is the other value, and it is a security decision
// rather than a formatting one.
//
// `public` lets a CDN store the body and serve it to a caller that presents no
// publishable key — the gate is bypassed for as long as the entry lives. That is
// what an installation asks for when it sets the flag, and the word in the header
// is the only place the request itself says so.
func TestASharedPolicySaysPublic(t *testing.T) {
	t.Parallel()

	r := cachingRouter(cachingCatalog(), time.Minute, true)

	rec := storeRead(t, r, "/store/v1/sales-channels/sc_1/products")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "public, max-age=60", rec.Header().Get("Cache-Control"))
}

// TestTheDefaultWritesNoHeaderAtAll is what every existing installation gets.
//
// A zero TTL is the default and it has to be indistinguishable from the state
// before the setting existed: a `max-age=0` would still be a header, and a client
// or a proxy that revalidates on it behaves differently from one that was told
// nothing.
func TestTheDefaultWritesNoHeaderAtAll(t *testing.T) {
	t.Parallel()

	// Both spellings of the default: a handler nobody set a policy on, and one set
	// to zero explicitly. They have to behave the same, or "the default" would
	// depend on whether the composition root remembered to call the setter.
	unset := chi.NewRouter()
	api.New(cachingCatalog(), graph.Options{}).Routes(unset)

	for name, router := range map[string]chi.Router{
		"no policy set": unset,
		"zero ttl":      cachingRouter(cachingCatalog(), 0, false),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := storeRead(t, router, "/store/v1/sales-channels/sc_1/products")

			require.Equal(t, http.StatusOK, rec.Code)
			assert.Empty(t, rec.Header().Get("Cache-Control"),
				"the default must leave the response exactly as it was")
		})
	}
}

// TestARefusalIsNeverCacheable is the position of the call, not its value.
//
// A header written before the outcome is known lands on the refusals too, and a
// 404 stored by a CDN for the length of the TTL is a product that stays missing
// after somebody fixes it. The refusals are what a storefront hits most often —
// a handle that moved, a channel a key does not hold — so this is the failure with
// the highest chance of being seen.
func TestARefusalIsNeverCacheable(t *testing.T) {
	t.Parallel()

	catalog := cachingCatalog()
	catalog.getStoreProduct = func(
		_ context.Context, _ string, _ []string,
	) (service.StoreProduct, error) {
		return service.StoreProduct{}, coreerrors.NotFound("product_not_found", "no such product")
	}

	r := cachingRouter(catalog, time.Hour, true)

	rec := storeRead(t, r, "/store/v1/sales-channels/sc_1/products/gone")

	require.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
	assert.Empty(t, rec.Header().Get("Cache-Control"),
		"a refusal must carry no cache header; a cached 404 outlives the fix")
}

// TestTheUNSCOPEDStoreReadsCarryNoPolicy holds the boundary of the claim.
//
// Collections, categories and tags are served UNFILTERED (ADR 0044 says so and
// does not scope them), so their bodies are not a function of a channel — and this
// decision is about the reads whose body the URL alone decides. Writing the header
// on them would be a wider claim than the one that was measured.
func TestTheUNSCOPEDStoreReadsCarryNoPolicy(t *testing.T) {
	t.Parallel()

	catalog := cachingCatalog()
	catalog.listCollections = func(
		_ context.Context, _, _ int,
	) (service.ListResult[models.Collection], error) {
		return service.ListResult[models.Collection]{
			Items: []models.Collection{{ID: "pcol_1"}},
		}, nil
	}

	r := cachingRouter(catalog, time.Hour, true)

	rec := storeRead(t, r, "/store/v1/collections")

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Empty(t, rec.Header().Get("Cache-Control"),
		"an unscoped taxonomy read is outside this decision")
}

// TestTheGraphQLReadIsNotCacheableEither keeps the POST surface out.
//
// ADR 0044 left the GraphQL transport POST-only and uncached for reasons this
// decision does not touch: the query would land in URLs, proxy logs and browser
// history, and a long one dies at a proxy's limit with a 414 nobody can diagnose.
// A cache header on a POST is meaningless to most caches and misleading to the
// rest.
func TestTheGraphQLReadIsNotCacheableEither(t *testing.T) {
	t.Parallel()

	r := cachingRouter(cachingCatalog(), time.Hour, true)

	req := httptest.NewRequest(http.MethodPost, "/store/v1/graphql",
		strings.NewReader(`{"query":"{ products { id } }"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Empty(t, rec.Header().Get("Cache-Control"),
		"the GraphQL endpoint carries no cache header at any TTL")
}

// TestTheQueryStringIsPartOfTheKeyAndTheHeaderDoesNotSayOtherwise is the
// assumption a cache makes and this decision relies on.
//
// The listing's filters are query parameters, so two different filters are two
// different URLs and a cache keyed on the URL stores them apart. What would break
// that is a `Vary` this endpoint does not write, or a normalization it does not
// do — the test is here so that adding either one has to argue with it.
func TestTheQueryStringIsPartOfTheKeyAndTheHeaderDoesNotSayOtherwise(t *testing.T) {
	t.Parallel()

	r := cachingRouter(cachingCatalog(), time.Minute, true)

	rec := storeRead(t, r, "/store/v1/sales-channels/sc_1/products?collection_id=pcol_1")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "public, max-age=60", rec.Header().Get("Cache-Control"))
	assert.Empty(t, rec.Header().Get("Vary"),
		"a Vary on this response would key the cache on a header again, which is the "+
			"thing ADR 0044 moved into the path to stop")
}
