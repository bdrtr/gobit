//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wishlistVariants reads the variant ids of a wishlist listing.
func wishlistVariants(t *testing.T, body []byte) []string {
	t.Helper()

	var envelope struct {
		Data []struct {
			VariantID string `json:"variant_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope), "body: %s", body)

	out := make([]string, 0, len(envelope.Data))
	for _, item := range envelope.Data {
		out = append(out, item.VariantID)
	}
	return out
}

// catalogVariantIDs reads the variant ids of every product in a catalog page.
func catalogVariantIDs(t *testing.T, body []byte) []string {
	t.Helper()

	var envelope struct {
		Data []struct {
			Variants []struct {
				ID string `json:"id"`
			} `json:"variants"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope), "body: %s", body)

	var out []string
	for _, product := range envelope.Data {
		for _, variant := range product.Variants {
			out = append(out, variant.ID)
		}
	}
	return out
}

// TestAShopperKeepsAWishlist is ADR 0190 on the production wiring: a proven
// shopper saves a variant, reads it back and removes it, another shopper's
// session cannot reach the list, and the operator reads it. The catalog shows
// the saved variants in one read (ADR 0191).
func TestAShopperKeepsAWishlist(t *testing.T) {
	ctx := t.Context()
	shopper, _ := newCustomer(ctx, t)
	stranger, _ := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Wishlist Product", map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, happyInitialStock)

	list := "/store/v1/customers/" + shopper + "/wishlist"
	item := list + "/" + variantID

	saved := identifiedStorefrontRequest(t, shopper, http.MethodPut, item, "")
	require.Equal(t, http.StatusOK, saved.Code, saved.Body.String())
	assert.Equal(t, variantID, storefrontData(t, saved)["variant_id"])

	read := identifiedStorefrontRequest(t, shopper, http.MethodGet, list, "")
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	assert.Equal(t, []string{variantID}, wishlistVariants(t, read.Body.Bytes()))

	// The list is shown through the catalog (ADR 0191): one read names the
	// saved variants and brings back their products, priced and stocked.
	shown := storefrontRequest(t, http.MethodGet,
		catalogPath(testChannelID, "/products")+"?variant_id="+variantID+"&variant_id=variant_never_existed", "")
	require.Equal(t, http.StatusOK, shown.Code, shown.Body.String())
	assert.Equal(t, []string{variantID}, catalogVariantIDs(t, shown.Body.Bytes()),
		"the saved variant's product comes back, and the unknown id brings back nothing")

	refused := identifiedStorefrontRequest(t, stranger, http.MethodGet, list, "")
	assert.Equal(t, http.StatusForbidden, refused.Code, refused.Body.String())

	operator, err := adminRequestWithBody(http.MethodGet, "/admin/v1/customers/"+shopper+"/wishlist", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, operator.Code, operator.Body.String())
	assert.Equal(t, []string{variantID}, wishlistVariants(t, operator.Body.Bytes()))

	removed := identifiedStorefrontRequest(t, shopper, http.MethodDelete, item, "")
	require.Equal(t, http.StatusNoContent, removed.Code, removed.Body.String())
	empty := identifiedStorefrontRequest(t, shopper, http.MethodGet, list, "")
	require.Equal(t, http.StatusOK, empty.Code, empty.Body.String())
	assert.Empty(t, wishlistVariants(t, empty.Body.Bytes()))
}
