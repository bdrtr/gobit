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
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// This file exercises WHERE the storefront endpoints read their sales channels
// from, and the wiring of the product ↔ channel admin endpoints.

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

// TestStoreListReadsChannelsFromPrincipal verifies that the storefront listing
// reads its sales channels FROM THE AUTHENTICATED IDENTITY.
func TestStoreListReadsChannelsFromPrincipal(t *testing.T) {
	t.Parallel()

	var got service.StoreListOptions
	catalog := &fakeCatalog{
		listStoreProducts: func(
			_ context.Context, opts service.StoreListOptions,
		) (service.ListResult[service.StoreProduct], error) {
			got = opts
			return service.ListResult[service.StoreProduct]{}, nil
		},
	}

	rec := storeRequest(t, newRouter(catalog), "/store/v1/products", &corehttp.Principal{
		ID: "apk_1", Kind: "api_key", SalesChannelIDs: []string{"sc_a", "sc_b"},
	})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, []string{"sc_a", "sc_b"}, got.SalesChannelIDs,
		"the channels have to come from the key's identity")
}

// TestStoreListIgnoresChannelQueryParam verifies that the channel is NOT read
// FROM THE QUERY STRING.
//
// This is the most dangerous form of the fault: were the channel a value the
// client declares, a client arriving with any publishable key it happened to
// hold could read ANOTHER channel's catalog and the filter would stop being an
// authorization and turn into a display preference.
func TestStoreListIgnoresChannelQueryParam(t *testing.T) {
	t.Parallel()

	var got service.StoreListOptions
	catalog := &fakeCatalog{
		listStoreProducts: func(
			_ context.Context, opts service.StoreListOptions,
		) (service.ListResult[service.StoreProduct], error) {
			got = opts
			return service.ListResult[service.StoreProduct]{}, nil
		},
	}

	rec := storeRequest(t, newRouter(catalog),
		"/store/v1/products?sales_channel_id=sc_other&sales_channel_ids=sc_other",
		&corehttp.Principal{ID: "apk_1", Kind: "api_key", SalesChannelIDs: []string{"sc_a"}})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, []string{"sc_a"}, got.SalesChannelIDs,
		"the channel in the query string HAS TO BE IGNORED; the only source is the identity")
}

// TestStoreListWithoutPrincipalPassesNil verifies that the filter is not applied
// at all on a request with no identity.
//
// In the service contract nil means "no filtering"; in a setup where store
// authentication is not wired up (product on its own) the storefront keeps
// working this way.
func TestStoreListWithoutPrincipalPassesNil(t *testing.T) {
	t.Parallel()

	var got service.StoreListOptions
	catalog := &fakeCatalog{
		listStoreProducts: func(
			_ context.Context, opts service.StoreListOptions,
		) (service.ListResult[service.StoreProduct], error) {
			got = opts
			return service.ListResult[service.StoreProduct]{}, nil
		},
	}

	rec := storeRequest(t, newRouter(catalog), "/store/v1/products", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Nil(t, got.SalesChannelIDs, "if there is no identity the filter must not be applied")
}

// TestStoreListWithChannellessPrincipalPassesEmptySet verifies that an identity
// with no channel produces an empty set, NOT nil.
//
// Had the two been treated as one, an identity with no channel would go down the
// "no filtering" branch and read the catalog of ALL channels. The distinction
// can only be preserved here, in the place that reads the identity.
func TestStoreListWithChannellessPrincipalPassesEmptySet(t *testing.T) {
	t.Parallel()

	var got service.StoreListOptions
	catalog := &fakeCatalog{
		listStoreProducts: func(
			_ context.Context, opts service.StoreListOptions,
		) (service.ListResult[service.StoreProduct], error) {
			got = opts
			return service.ListResult[service.StoreProduct]{}, nil
		},
	}

	rec := storeRequest(t, newRouter(catalog), "/store/v1/products",
		&corehttp.Principal{ID: "apk_1", Kind: "api_key"})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotNil(t, got.SalesChannelIDs, "an identity with no channel has to produce an empty set, NOT nil")
	assert.Empty(t, got.SalesChannelIDs)
}

// TestStoreGetProductPassesChannels verifies that the single endpoint carries
// the channels too; hiding them in the listing and showing them on the single
// endpoint would make the hiding pointless.
func TestStoreGetProductPassesChannels(t *testing.T) {
	t.Parallel()

	var got []string
	catalog := &fakeCatalog{
		getStoreProduct: func(_ context.Context, _ string, channels []string) (service.StoreProduct, error) {
			got = channels
			return service.StoreProduct{}, nil
		},
	}

	rec := storeRequest(t, newRouter(catalog), "/store/v1/products/tisort",
		&corehttp.Principal{ID: "apk_1", Kind: "api_key", SalesChannelIDs: []string{"sc_a"}})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, []string{"sc_a"}, got)
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
