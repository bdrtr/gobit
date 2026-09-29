//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAGiftBoxNamesWhatItIsMadeOf is ADR 0234 on the production wiring: the
// operator makes a gift box of a towel and two soaps; the admin surface reads
// the composition back, the storefront reads it on the box's variant over REST
// and GraphQL, a variant with its own stock item is refused as a bundle and a
// component is refused its deletion. Until the box's stock is read from its
// components, the box reads out of stock and checkout refuses it.
func TestAGiftBoxNamesWhatItIsMadeOf(t *testing.T) {
	ctx := t.Context()
	box := newVariant(ctx, t, "E2E Gift Box", map[string]int64{taxedCurrency: 30_000})
	soap, _ := newStockedVariant(ctx, t, "E2E Soap", map[string]int64{taxedCurrency: 5_000}, 10)
	towel, _ := newStockedVariant(ctx, t, "E2E Towel", map[string]int64{taxedCurrency: 8_000}, 10)
	composition := fmt.Sprintf(`[{"variant_id":%q,"quantity":1},{"variant_id":%q,"quantity":2}]`, towel, soap)

	put := func(variant, components string) (int, string) {
		rec, err := adminRequestWithBody(http.MethodPut, "/admin/v1/variants/"+variant+"/bundle",
			json.RawMessage(`{"components":`+components+`}`))
		require.NoError(t, err)
		return rec.Code, rec.Body.String()
	}
	code, body := put(soap, fmt.Sprintf(`[{"variant_id":%q,"quantity":1}]`, towel))
	assert.Equal(t, http.StatusConflict, code, "a variant with its own stock item is no bundle: %s", body)
	assert.Contains(t, body, "product_bundle_shape")
	code, body = put(box, composition)
	require.Equal(t, http.StatusOK, code, body)
	assert.JSONEq(t, `{"data":{"components":`+composition+`}}`, body)

	read, err := adminRequestWithBody(http.MethodGet, "/admin/v1/variants/"+box+"/bundle", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	assert.JSONEq(t, `{"data":{"components":`+composition+`}}`, read.Body.String())

	variant, err := productSvc.GetVariant(ctx, box)
	require.NoError(t, err)
	product, err := productSvc.GetProduct(ctx, variant.ProductID)
	require.NoError(t, err)
	shown := magazaIstegi(t, catalogPath(testChannelID, "/products/"+product.Handle), publishableKey)
	require.Equal(t, http.StatusOK, shown.Code, shown.Body.String())
	var store struct {
		Data struct {
			Variants []struct {
				ID               string          `json:"id"`
				InStock          bool            `json:"in_stock"`
				BundleComponents json.RawMessage `json:"bundle_components"`
			} `json:"variants"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(shown.Body.Bytes(), &store))
	require.Len(t, store.Data.Variants, 1, shown.Body.String())
	assert.JSONEq(t, composition, string(store.Data.Variants[0].BundleComponents))
	assert.False(t, store.Data.Variants[0].InStock, "a box nothing counts reads out of stock (ADR 0040)")

	graph := gqlRequest(t, publishableKey, `query($handle: String) {
		product(handle: $handle) { variants { inStock bundleComponents { variantId quantity } } }
	}`, map[string]any{"handle": product.Handle})
	require.Equal(t, http.StatusOK, graph.Code, graph.Body.String())
	assert.JSONEq(t, fmt.Sprintf(`{"data":{"product":{"variants":[{"inStock":false,"bundleComponents":[`+
		`{"variantId":%q,"quantity":1},{"variantId":%q,"quantity":2}]}]}}}`, towel, soap), graph.Body.String())

	rec, err := adminRequestWithBody(http.MethodDelete, "/admin/v1/variants/"+soap, nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, rec.Code, "a component is not deleted: %s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), box)

	cartID := openCartWithKey(t, publishableKey)
	added := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/line-items",
		fmt.Sprintf(`{"variant_id":%q,"quantity":1}`, box))
	require.Equal(t, http.StatusCreated, added.Code, added.Body.String())
	cart := storefrontData(t, added)
	total, ok := cart["total"].(float64)
	require.True(t, ok, added.Body.String())
	done := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		storefrontCompletionBody(t, int64(total)))
	assert.Equal(t, http.StatusUnprocessableEntity, done.Code, done.Body.String())
	assert.Contains(t, done.Body.String(), "checkout_workflow_variant_not_stocked")
}
