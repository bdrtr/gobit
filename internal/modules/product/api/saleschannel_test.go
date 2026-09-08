package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// This file exercises WHERE the storefront endpoints read their sales channels
// from, and the wiring of the product ↔ channel admin endpoints.

// The storefront catalog addresses, written out rather than imported.
//
// The api package holds the same three strings as unexported constants and
// binds both the route and the OpenAPI description to them, which is what keeps
// those two from drifting. Importing them here would buy nothing and cost the
// only thing this file has to offer: a test that agreed with a typo by
// construction could not report it. Spelled out, the segment's shape is stated
// twice by two constructs, and changing it is an edit somebody has to mean.
const (
	// scopedChannel is the channel the scoped addresses below name.
	scopedChannel = "sc_a"
	// storeProductsPath is the channel-scoped storefront listing.
	storeProductsPath = "/store/v1/sales-channels/sc_a/products"
	// storeProductPath is the channel-scoped single product; the id or handle
	// is appended to it.
	storeProductPath = "/store/v1/sales-channels/sc_a/products/"
	// storeOptionValuesPath is the channel-scoped option vocabulary.
	storeOptionValuesPath = "/store/v1/sales-channels/sc_a/option-values"
)

// storeRequest runs a store request with the given identity.
//
// If principal is nil NO identity is put into the context; that represents the
// setup where store authentication is not wired up. In production the identity
// is put in place by corehttp.RequireStore.
func storeRequest(
	t *testing.T,
	r chi.Router,
	target string,
	principal *corehttp.Principal,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, target, strings.NewReader(""))
	if principal != nil {
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), *principal))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// listedChannels runs the storefront listing and returns the channel set that
// reached the service, together with the response.
//
// The three catalog reads make the SAME claim about the same rule, and a helper
// per read is what lets a claim hold on two of them and be forgotten on the
// third.
func listedChannels(
	t *testing.T,
	target string,
	principal *corehttp.Principal,
) (*httptest.ResponseRecorder, service.StoreListOptions) {
	t.Helper()

	var got service.StoreListOptions
	catalog := &fakeCatalog{
		listStoreProducts: func(
			_ context.Context, opts service.StoreListOptions,
		) (service.ListResult[service.StoreProduct], error) {
			got = opts
			return service.ListResult[service.StoreProduct]{}, nil
		},
	}

	return storeRequest(t, newRouter(catalog), target, principal), got
}

// TestStoreListTakesTheChannelFromThePath verifies that the listing is scoped to
// the ONE channel the path names, not to everything the key holds.
//
// This is the point of ADR 0044 and the half a careless build gets wrong: with
// the channel in the URL, a key bound to two channels must NOT keep receiving
// the union of both. If it did, the URL would not determine the body — two keys
// on the same URL would still get different bytes — and the cache key the whole
// decision exists to create would be a lie a shared cache cannot detect.
func TestStoreListTakesTheChannelFromThePath(t *testing.T) {
	t.Parallel()

	rec, got := listedChannels(t, storeProductsPath, &corehttp.Principal{
		ID: "apk_1", Kind: "api_key", SalesChannelIDs: []string{scopedChannel, "sc_b"},
	})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, []string{scopedChannel}, got.SalesChannelIDs,
		"the scope has to be the channel the PATH names; a key holding two channels "+
			"reads them one request at a time and gets no union")
}

// TestStoreListRefusesAChannelTheKeyDoesNotHold verifies that the path can only
// NARROW.
//
// The path is where the client writes its CLAIM; the key is what turns a claim
// into evidence (ADR 0008). Were the claim honored on its own, the segment would
// be the query-string mistake with a different spelling: anyone holding any
// publishable key could read any storefront's catalog by typing its channel id.
//
// The service must not be reached at all — an authorization that runs after the
// query has already been asked is not an authorization.
func TestStoreListRefusesAChannelTheKeyDoesNotHold(t *testing.T) {
	t.Parallel()

	called := false
	catalog := &fakeCatalog{
		listStoreProducts: func(
			context.Context, service.StoreListOptions,
		) (service.ListResult[service.StoreProduct], error) {
			called = true
			return service.ListResult[service.StoreProduct]{}, nil
		},
	}

	rec := storeRequest(t, newRouter(catalog), "/store/v1/sales-channels/sc_other/products",
		&corehttp.Principal{ID: "apk_1", Kind: "api_key", SalesChannelIDs: []string{scopedChannel}})

	assert.Equal(t, http.StatusForbidden, rec.Code,
		"a path naming a channel the key does not hold has to be REFUSED, not served; body: %s",
		rec.Body.String())
	assert.False(t, called, "the catalog must not be queried for a channel the key does not hold")
}

// TestStoreListRefusesEveryChannelForAChannellessIdentity verifies that an
// identity holding NO channel can reach nothing.
//
// nil (no identity at all) and an EMPTY BUT NON-nil set (an identity with no
// channels) are two different sentences, and this is the one where collapsing
// them leaks: an identity with no channels holds nothing to narrow to, so every
// path value is a channel it does not hold. Treating it like the nil case would
// let a channelless key read whatever channel it typed.
func TestStoreListRefusesEveryChannelForAChannellessIdentity(t *testing.T) {
	t.Parallel()

	rec, _ := listedChannels(t, storeProductsPath,
		&corehttp.Principal{ID: "apk_1", Kind: "api_key"})

	assert.Equal(t, http.StatusForbidden, rec.Code,
		"an identity with no channels holds nothing the path can narrow to; body: %s",
		rec.Body.String())
}

// TestStoreListIgnoresChannelQueryParam verifies that the channel is STILL NOT
// read from the query string.
//
// The channel now has a live input, and that is exactly why this claim had to
// survive the move rather than be retired with it: the query parameter is the
// one input three godocs promise is dead, and a second live spelling of the same
// scope would mean two answers to "which channel is this" with no rule saying
// which wins. The path names the key's own channel here and the query names
// another; the body must be the path's.
func TestStoreListIgnoresChannelQueryParam(t *testing.T) {
	t.Parallel()

	rec, got := listedChannels(t,
		storeProductsPath+"?sales_channel_id=sc_other&sales_channel_ids=sc_other",
		&corehttp.Principal{ID: "apk_1", Kind: "api_key", SalesChannelIDs: []string{scopedChannel}})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, []string{scopedChannel}, got.SalesChannelIDs,
		"the channel in the query string HAS TO BE IGNORED; the scope comes from the path")
}

// TestStoreListWithoutPrincipalTakesThePathAlone verifies what happens where
// there is no identity to intersect against.
//
// That is the deployment which never wired store authentication up — product can
// be deployed on its own — and the filter used to be skipped entirely there, so
// the storefront returned EVERY channel's catalog. With the channel in the path
// there is now something to scope to even with no key, and the answer narrows to
// it: strictly less than before, never more.
func TestStoreListWithoutPrincipalTakesThePathAlone(t *testing.T) {
	t.Parallel()

	rec, got := listedChannels(t, storeProductsPath, nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, []string{scopedChannel}, got.SalesChannelIDs,
		"with no identity the path value stands alone; it must not fall back to nil, "+
			"which the service reads as \"do not filter\"")
}

// TestStoreGetProductTakesTheChannelFromThePath verifies that the single
// endpoint carries the same scope; hiding a product in the listing and showing
// it on the single endpoint would make the hiding pointless, and storefront
// addresses carry a handle, which makes this the easiest endpoint to guess.
func TestStoreGetProductTakesTheChannelFromThePath(t *testing.T) {
	t.Parallel()

	var got []string
	catalog := &fakeCatalog{
		getStoreProduct: func(_ context.Context, _ string, channels []string) (service.StoreProduct, error) {
			got = channels
			return service.StoreProduct{}, nil
		},
	}

	rec := storeRequest(t, newRouter(catalog), storeProductPath+"tisort",
		&corehttp.Principal{ID: "apk_1", Kind: "api_key", SalesChannelIDs: []string{scopedChannel, "sc_b"}})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, []string{scopedChannel}, got)
}

// TestStoreGetProductRefusesAChannelTheKeyDoesNotHold verifies that the single
// endpoint refuses too, and refuses BEFORE the lookup.
func TestStoreGetProductRefusesAChannelTheKeyDoesNotHold(t *testing.T) {
	t.Parallel()

	called := false
	catalog := &fakeCatalog{
		getStoreProduct: func(context.Context, string, []string) (service.StoreProduct, error) {
			called = true
			return service.StoreProduct{}, nil
		},
	}

	rec := storeRequest(t, newRouter(catalog), "/store/v1/sales-channels/sc_other/products/tisort",
		&corehttp.Principal{ID: "apk_1", Kind: "api_key", SalesChannelIDs: []string{scopedChannel}})

	assert.Equal(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())
	assert.False(t, called, "the product must not be looked up for a channel the key does not hold")
}

// TestTheRefusalDoesNotDependOnTheChannelExisting verifies that the 403 is
// produced WITHOUT consulting any channel record.
//
// This is what makes 403 the right code rather than the 404 a hidden product
// gets. A hidden product is 404 so that the key's owner cannot enumerate another
// storefront's handles; no such oracle exists here, because the only thing read
// is the key's own set. Two channel ids the key does not hold — one that could
// plausibly exist and one that is nonsense — therefore have to be
// indistinguishable.
func TestTheRefusalDoesNotDependOnTheChannelExisting(t *testing.T) {
	t.Parallel()

	principal := &corehttp.Principal{ID: "apk_1", Kind: "api_key", SalesChannelIDs: []string{scopedChannel}}
	// The catalog answers rather than being left nil, so that a build which
	// stopped refusing reports an assertion here instead of panicking on an
	// unimplemented fake and taking the rest of the package's results with it.
	r := newRouter(&fakeCatalog{
		listStoreProducts: func(
			context.Context, service.StoreListOptions,
		) (service.ListResult[service.StoreProduct], error) {
			return service.ListResult[service.StoreProduct]{}, nil
		},
	})

	plausible := storeRequest(t, r, "/store/v1/sales-channels/sc_01jd8kq2n0000000000000000/products", principal)
	nonsense := storeRequest(t, r, "/store/v1/sales-channels/not-an-id-at-all/products", principal)

	require.Equal(t, http.StatusForbidden, plausible.Code, "body: %s", plausible.Body.String())
	require.Equal(t, http.StatusForbidden, nonsense.Code, "body: %s", nonsense.Body.String())
	assert.Equal(t, errorCode(t, nonsense), errorCode(t, plausible),
		"both refusals have to carry the SAME code; a difference would say whether the "+
			"named channel exists, which is a fact about another merchant")
}

// TestStoreOptionValuesRefusesAChannelTheKeyDoesNotHold verifies that the third
// channel-scoped read applies the same rule.
//
// It is the read most easily forgotten, and the one where forgetting is worst:
// the vocabulary names the colors and sizes of the very products the listing
// refuses to show.
func TestStoreOptionValuesRefusesAChannelTheKeyDoesNotHold(t *testing.T) {
	t.Parallel()

	called := false
	catalog := &fakeCatalog{
		listOptionValues: func(
			context.Context, service.ListOptionValuesOptions,
		) (service.ListResult[models.OptionValuePair], error) {
			called = true
			return service.ListResult[models.OptionValuePair]{}, nil
		},
	}

	rec := storeRequest(t, newRouter(catalog), "/store/v1/sales-channels/sc_other/option-values",
		&corehttp.Principal{ID: "apk_1", Kind: "api_key", SalesChannelIDs: []string{scopedChannel}})

	assert.Equal(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())
	assert.False(t, called, "the vocabulary must not be read for a channel the key does not hold")
}

// errorCode returns the "code" of the error envelope in the response.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	envelope, ok := decodeBody(t, rec)["error"].(map[string]any)
	require.True(t, ok, "the refusal has to carry an error envelope: %s", rec.Body.String())

	code, ok := envelope["code"].(string)
	require.True(t, ok, "the error envelope has to carry a code: %s", rec.Body.String())

	return code
}

// TestAdminAddSalesChannelReturnsCurrentList verifies that the linking endpoint
// passes the right ids to the service and returns the CURRENT list.
func TestAdminAddSalesChannelReturnsCurrentList(t *testing.T) {
	t.Parallel()

	var gotProduct, gotChannel string
	catalog := &fakeCatalog{
		addSalesChannel: func(_ context.Context, productID, channelID string) error {
			gotProduct, gotChannel = productID, channelID
			return nil
		},
		salesChannelIDs: func(context.Context, string) ([]string, error) {
			return []string{"sc_a"}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodPost, "/admin/v1/products/prod_1/sales-channels",
		`{"sales_channel_id": "sc_a"}`)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "prod_1", gotProduct)
	assert.Equal(t, "sc_a", gotChannel)

	data, ok := decodeBody(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "prod_1", data["product_id"])
	assert.Equal(t, []any{"sc_a"}, data["sales_channel_ids"])
}

// TestAdminRemoveSalesChannelReadsChannelFromPath verifies that the removal
// endpoint reads the channel id FROM THE PATH.
func TestAdminRemoveSalesChannelReadsChannelFromPath(t *testing.T) {
	t.Parallel()

	var gotProduct, gotChannel string
	catalog := &fakeCatalog{
		removeSalesChannel: func(_ context.Context, productID, channelID string) error {
			gotProduct, gotChannel = productID, channelID
			return nil
		},
		salesChannelIDs: func(context.Context, string) ([]string, error) { return nil, nil },
	}

	rec := do(t, newRouter(catalog), http.MethodDelete,
		"/admin/v1/products/prod_1/sales-channels/sc_a", "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "prod_1", gotProduct)
	assert.Equal(t, "sc_a", gotChannel)

	assert.Contains(t, rec.Body.String(), `"sales_channel_ids":[]`,
		"an empty list has to be an empty array, not null: %s", rec.Body.String())
}

// TestAdminSalesChannelErrorKeepsErrorClass verifies that the service's typed
// error is turned into an HTTP code FROM ITS CLASS, not by hand.
func TestAdminSalesChannelErrorKeepsErrorClass(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		addSalesChannel: func(context.Context, string, string) error {
			return coreerrors.NotFound("product_not_found", "product not found: prod_missing")
		},
	}

	rec := do(t, newRouter(catalog), http.MethodPost, "/admin/v1/products/prod_missing/sales-channels",
		`{"sales_channel_id": "sc_a"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
}
