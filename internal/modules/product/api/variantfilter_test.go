package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
