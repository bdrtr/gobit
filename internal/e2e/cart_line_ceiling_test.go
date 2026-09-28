//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cartsvc "github.com/bdrtr/gobit/internal/modules/cart/service"
)

// TestAStorefrontCartStopsAtItsLineCeiling is D154 on the production wiring: a
// cart full of one variant under a hundred engravings refuses the same variant
// with a new engraving over the storefront, where the flow used to let it
// through because the variant was already in the cart, and still raises a line
// whose engraving it holds.
func TestAStorefrontCartStopsAtItsLineCeiling(t *testing.T) {
	ctx := t.Context()
	variantID := newVariant(ctx, t, "Engraved ring", map[string]int64{taxedCurrency: 10_000})
	cartID := openCartWithKey(t, publishableKey)
	// The lines are opened through the service: a hundred storefront adds would
	// reprice the cart a hundred times for lines this test does not examine.
	for i := range cartsvc.MaxLineItems {
		_, err := cartSvc.AddLineItem(ctx, cartID, cartsvc.AddLineItemInput{
			VariantID: variantID, Title: "Engraved ring", Quantity: 1, UnitPrice: 10_000,
			Properties: map[string]string{"Engraving": strconv.Itoa(i)},
		})
		require.NoError(t, err)
	}
	add := func(engraving string) (int, string) {
		rec := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/line-items",
			fmt.Sprintf(`{"variant_id":%q,"quantity":1,"properties":{"Engraving":%q}}`, variantID, engraving))
		return rec.Code, rec.Body.String()
	}

	code, body := add("new")
	assert.Equal(t, http.StatusUnprocessableEntity, code, body)
	assert.Contains(t, body, cartsvc.CodeLineLimit)

	code, body = add("7")
	require.Equal(t, http.StatusCreated, code, body)
	detail, err := cartSvc.GetCart(ctx, cartID)
	require.NoError(t, err)
	assert.Len(t, detail.Items, cartsvc.MaxLineItems)
}
