package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestTheVariantFilterReachesTheServiceAsGiven shows the repeated variant_id
// parameter arriving in order, and its absence arriving as no filter
// (ADR 0191). The bound and the form of the ids are the service's to judge.
func TestTheVariantFilterReachesTheServiceAsGiven(t *testing.T) {
	t.Parallel()

	rec, got := listedChannels(t, storeProductsPath+"?variant_id=variant_b&variant_id=variant_a", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"variant_b", "variant_a"}, got.VariantIDs)

	rec, got = listedChannels(t, storeProductsPath, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Nil(t, got.VariantIDs, "an absent parameter is no filter, not a filter naming nothing")
}

// TestTheRegionReachesTheStoreReads shows region_id arriving trimmed on the
// listing and on the single read, and its absence arriving as no region
// (ADR 0422).
func TestTheRegionReachesTheStoreReads(t *testing.T) {
	t.Parallel()

	rec, got := listedChannels(t, storeProductsPath+"?region_id=%20reg_tr%20", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "reg_tr", got.RegionID)

	rec, got = listedChannels(t, storeProductsPath, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, got.RegionID, "an absent parameter narrows nothing")

	catalog := &fakeCatalog{
		getStoreProduct: func(context.Context, string, []string) (service.StoreProduct, error) {
			return service.StoreProduct{}, nil
		},
	}
	rec = storeRequest(t, newRouter(catalog), storeProductPath+"shirt?region_id=reg_eu", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"reg_eu"}, catalog.storeProductRegions)
}
