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
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/api"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// The catalog cache header (ADR 0151) and the validator that rides with it
// (ADR 0391).
//
// # What these tests are really about
//
// Not that a header can be written. Three things decide whether this feature is
// safe, and each has its own test: WHICH responses carry it (the channel-scoped
// reads, derived from the router, and nothing else), WHEN it is written (on the success path,
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

	return conditionalRead(t, r, target, nil, "")
}

// conditionalRead sends a storefront read carrying the given identity and, when
// it is not empty, the given If-None-Match.
func conditionalRead(
	t *testing.T, r chi.Router, target string, principal *corehttp.Principal, ifNoneMatch string,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	if principal != nil {
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), *principal))
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// readsCatalog answers every storefront catalog read with one record, so that
// each read the router serves can be asked for its headers.
//
// It wraps [fakeCatalog] with the reads that fake does not carry; a read the
// router gains and this fake does not answer panics on the nil interface, which
// is the loud failure the walk below wants.
type readsCatalog struct {
	*fakeCatalog
}

func (readsCatalog) StoreRelatedProducts(
	context.Context, string, models.RelationType, []string,
) ([]service.StoreProduct, error) {
	return []service.StoreProduct{{Product: models.Product{ID: "prod_2"}}}, nil
}

func (readsCatalog) StoreProductAddOns(context.Context, string, []string) ([]service.StoreAddOn, error) {
	return []service.StoreAddOn{{VariantID: "variant_1"}}, nil
}

func (readsCatalog) StoreFacets(context.Context, service.StoreListOptions) ([]service.Facet, error) {
	return []service.Facet{{Handle: "material", Title: "Material", Kind: models.AttributeSelect, Products: 1}}, nil
}

func (readsCatalog) ListAttributes(context.Context) ([]models.Attribute, error) {
	return []models.Attribute{{ID: "pattr_1", Handle: "material"}}, nil
}

// cachingCatalog answers every storefront catalog read with one record.
func cachingCatalog() readsCatalog {
	return readsCatalog{fakeCatalog: &fakeCatalog{
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
		listCollections: func(
			_ context.Context, _, _ int,
		) (service.ListResult[models.Collection], error) {
			return service.ListResult[models.Collection]{
				Items: []models.Collection{{ID: "pcol_1"}},
			}, nil
		},
		listCategories: func(
			_ context.Context, _ service.ListCategoriesOptions,
		) (service.ListResult[models.Category], error) {
			return service.ListResult[models.Category]{
				Items: []models.Category{{ID: "pcat_1"}},
			}, nil
		},
		listTags: func(
			_ context.Context, _, _ int,
		) (service.ListResult[models.Tag], error) {
			return service.ListResult[models.Tag]{
				Items: []models.Tag{{ID: "ptag_1"}},
			}, nil
		},
	}}
}

// scopedByChannel says whether a storefront route carries the freshness policy:
// the two directions the channel audit selects on (namesAChannel in
// internal/arch), so that renaming the placeholder fails one of them rather than
// shrinking the population.
func scopedByChannel(pattern string) bool {
	if strings.Contains(pattern, "/sales-channels/") {
		return true
	}
	for _, part := range strings.Split(pattern, "/") {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") && strings.Contains(part, "channel") {
			return true
		}
	}

	return false
}

// catalogReadAddress turns a route pattern into an address the fixture answers.
func catalogReadAddress(pattern string) string {
	parts := strings.Split(pattern, "/")
	for i, part := range parts {
		if !strings.HasPrefix(part, "{") {
			continue
		}
		parts[i] = "prod_1"
		if strings.Contains(part, "channel") {
			parts[i] = "sc_1"
		}
	}

	return strings.Join(parts, "/")
}

// TestEveryCatalogReadCarriesItsValidator is the decision, over the reads the
// router serves rather than a list of them (ADR 0391, D241).
//
// Every storefront GET answers with a tag and a 304 for that tag; the
// channel-scoped ones carry the policy on both, and the unscoped vocabularies
// on neither. ADR 0151's test walked a hand-written list of three while six
// carried the policy, so three could lose it with every test green — the shape
// that list's own godoc named as how this feature fails silently.
func TestEveryCatalogReadCarriesItsValidator(t *testing.T) {
	t.Parallel()

	r := cachingRouter(cachingCatalog(), 90*time.Second, false)

	var scoped, unscoped []string
	require.NoError(t, chi.Walk(r, func(
		method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler,
	) error {
		if method != http.MethodGet || !strings.HasPrefix(route, "/store/") {
			return nil
		}
		if scopedByChannel(route) {
			scoped = append(scoped, route)
		} else {
			unscoped = append(unscoped, route)
		}

		return nil
	}))
	// Neither half may be empty: a population that shrank to nothing would pass.
	require.NotEmpty(t, scoped, "no channel-scoped storefront read was found; the walk is blind")
	require.NotEmpty(t, unscoped, "no unscoped storefront read was found; the walk is blind")

	for _, route := range append(scoped, unscoped...) {
		policy := ""
		if scopedByChannel(route) {
			policy = "private, max-age=90"
		}
		t.Run(route, func(t *testing.T) {
			t.Parallel()

			target := catalogReadAddress(route)
			first := storeRead(t, r, target)

			require.Equal(t, http.StatusOK, first.Code, "body: %s", first.Body.String())
			tag := first.Header().Get("ETag")
			require.NotEmpty(t, tag, "every catalog read answers with a tag of its body")
			assert.Equal(t, policy, first.Header().Get("Cache-Control"),
				"the channel-scoped reads carry the policy and the unscoped ones do not; a "+
					"storefront whose listing is cacheable and whose product page is not shows "+
					"a shopper two answers about one product")

			again := conditionalRead(t, r, target, nil, tag)

			require.Equal(t, http.StatusNotModified, again.Code, "body: %s", again.Body.String())
			assert.Empty(t, again.Body.Bytes())
			assert.Equal(t, tag, again.Header().Get("ETag"))
			assert.Equal(t, policy, again.Header().Get("Cache-Control"),
				"a 304 carries the policy the 200 would, or none")
		})
	}
}

// TestTheTagFollowsTheAnswerNotTheAddress is why the tag is a hash: the same
// address answers different bytes after a write that bumps no version, and the
// tag the client holds must stop matching.
func TestTheTagFollowsTheAnswerNotTheAddress(t *testing.T) {
	t.Parallel()

	title := "First"
	catalog := cachingCatalog()
	catalog.getStoreProduct = func(_ context.Context, _ string, _ []string) (service.StoreProduct, error) {
		return service.StoreProduct{Product: models.Product{ID: "prod_1", Title: title, Version: 1}}, nil
	}
	r := cachingRouter(catalog, 0, false)
	target := "/store/v1/sales-channels/sc_1/products/prod_1"

	held := storeRead(t, r, target)
	require.Equal(t, http.StatusOK, held.Code)

	title = "Second"
	rec := conditionalRead(t, r, target, nil, held.Header().Get("ETag"))

	require.Equal(t, http.StatusOK, rec.Code, "a changed body must be sent, not confirmed")
	assert.Contains(t, rec.Body.String(), `"Second"`)
	assert.NotEqual(t, held.Header().Get("ETag"), rec.Header().Get("ETag"))
}

// TestARefusalIsNeverValidated holds the position of the writer: a refusal is
// written by the error path and carries no tag, so `*` cannot turn it into 304.
func TestARefusalIsNeverValidated(t *testing.T) {
	t.Parallel()

	catalog := cachingCatalog()
	catalog.getStoreProduct = func(_ context.Context, _ string, _ []string) (service.StoreProduct, error) {
		return service.StoreProduct{}, coreerrors.NotFound("product_not_found", "no such product")
	}
	r := cachingRouter(catalog, time.Hour, true)

	rec := conditionalRead(t, r, "/store/v1/sales-channels/sc_1/products/gone", nil, "*")

	require.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
	assert.Empty(t, rec.Header().Get("ETag"), "a refusal carries no tag")
}

// TestAForeignChannelIsRefusedWhateverTheTag: the comparison runs after the
// key's channel has been asked, so a key that does not hold the channel cannot
// learn from a 304 that its tag is the channel's current body.
func TestAForeignChannelIsRefusedWhateverTheTag(t *testing.T) {
	t.Parallel()

	r := cachingRouter(cachingCatalog(), time.Hour, true)
	key := &corehttp.Principal{ID: "pk_1", Kind: "publishable_key", SalesChannelIDs: []string{"sc_a"}}

	rec := conditionalRead(t, r, "/store/v1/sales-channels/sc_other/products", key, "*")

	require.Equal(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())
	assert.Empty(t, rec.Header().Get("ETag"))
	assert.Empty(t, rec.Header().Get("Cache-Control"))
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

// TestTheDefaultWritesNoFreshnessPolicy is what every existing installation gets.
//
// A zero TTL is the default and it has to write no freshness: a `max-age=0`
// would still be a header, and a client or a proxy that revalidates on it
// behaves differently from one that was told nothing. The ETag is not this
// question (ADR 0391): it is written at every TTL and adds no freshness.
func TestTheDefaultWritesNoFreshnessPolicy(t *testing.T) {
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
				"the default must write no freshness policy")
			assert.NotEmpty(t, rec.Header().Get("ETag"),
				"the tag is written whatever the TTL; the installation without one is the "+
					"one whose clients revalidate most")
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

	again := conditionalRead(t, r, "/store/v1/sales-channels/sc_1/products?collection_id=pcol_1",
		nil, rec.Header().Get("ETag"))
	require.Equal(t, http.StatusNotModified, again.Code)
	assert.Empty(t, again.Header().Get("Vary"), "the 304 reads no request header either")
}
