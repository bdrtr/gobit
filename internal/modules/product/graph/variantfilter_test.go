package graph_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheVariantFilterReachesTheServiceFromGraphQL shows the variantIds
// argument handed to the service as given, and its absence as no filter
// (ADR 0191).
func TestTheVariantFilterReachesTheServiceFromGraphQL(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}
	response, status := runQuery(t, identityWith([]string{"sc_1"}), svc,
		`{ products(variantIds: ["variant_b", "variant_a"]) { items { id } } }`)
	require.Empty(t, response.Errors)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, []string{"variant_b", "variant_a"}, svc.lastList(t).VariantIDs)

	unfiltered := &fakeStorefront{}
	response, status = runQuery(t, identityWith([]string{"sc_1"}), unfiltered, `{ products { items { id } } }`)
	require.Empty(t, response.Errors)
	require.Equal(t, http.StatusOK, status)
	assert.Nil(t, unfiltered.lastList(t).VariantIDs)
}
