//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAnEngravingReachesTheOrder is ADR 0223 and D152 on the production
// wiring: the storefront adds one variant with an engraving and a note, again
// with the same engraving, and once with another; the cart holds two lines, and
// the order placed from it keeps each line's words and note.
func TestAnEngravingReachesTheOrder(t *testing.T) {
	ctx := t.Context()
	variantID, _ := newStockedVariant(ctx, t, "E2E Engraved Ring", map[string]int64{taxedCurrency: 20_000}, 10)
	cartID := openCartWithKey(t, publishableKey)

	add := func(quantity int, words string) string {
		t.Helper()
		rec := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/line-items", fmt.Sprintf(
			`{"variant_id":%q,"quantity":%d,"metadata":{"gift_wrap":true},"properties":{"Engraving":%q}}`,
			variantID, quantity, words))
		require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
		return createdID(t, rec)
	}
	ada := add(1, "Ada")
	again := add(2, "Ada")
	bo := add(1, "Bo")
	assert.Equal(t, ada, again, "the same words raise the line already there")
	assert.NotEqual(t, ada, bo, "other words are another line")

	refused := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/line-items", fmt.Sprintf(
		`{"variant_id":%q,"quantity":1,"properties":{"Engraving":""}}`, variantID))
	assert.Equal(t, http.StatusUnprocessableEntity, refused.Code, "body: %s", refused.Body.String())

	read := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+cartID, "")
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	cart := storefrontData(t, read)
	items, ok := cart["items"].([]any)
	require.True(t, ok, read.Body.String())
	require.Len(t, items, 2)
	shown := map[string]bool{}
	for _, item := range items {
		line, ok := item.(map[string]any)
		require.True(t, ok)
		words, _ := line["properties"].(map[string]any)
		shown[fmt.Sprint(words["Engraving"])] = true
	}
	assert.Equal(t, map[string]bool{"Ada": true, "Bo": true}, shown, "the cart shows each line's words")
	total, ok := cart["total"].(float64)
	require.True(t, ok)

	done := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		storefrontCompletionBody(t, int64(total)))
	require.Equal(t, http.StatusOK, done.Code, "body: %s", done.Body.String())
	orderID, _ := storefrontData(t, done)["order_id"].(string)
	require.NotEmpty(t, orderID)

	order, err := orderSvc.GetOrder(ctx, orderID)
	require.NoError(t, err)
	require.Len(t, order.Items, 2)
	byWords := map[string]int64{}
	for _, line := range order.Items {
		byWords[line.Properties["Engraving"]] = line.Quantity
		assert.Equal(t, map[string]any{"gift_wrap": true}, line.Metadata, "the note reaches the order (D152)")
	}
	assert.Equal(t, map[string]int64{"Ada": 3, "Bo": 1}, byWords)

	rec := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+orderID, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"properties":{"Engraving":"Bo"}`)
}

// TestTwoEngravingsOfOneVariantHaveToFitItsStockTogether holds ADR 0223's
// reservation claim: with two in stock, one Ada and two Bo each fit alone and
// not together, so the completion is refused and the stock stays whole.
func TestTwoEngravingsOfOneVariantHaveToFitItsStockTogether(t *testing.T) {
	ctx := t.Context()
	variantID, itemID := newStockedVariant(ctx, t, "E2E Scarce Ring", map[string]int64{taxedCurrency: 20_000}, 2)
	cartID := openCartWithKey(t, publishableKey)
	for words, quantity := range map[string]int{"Ada": 1, "Bo": 2} {
		rec := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/line-items", fmt.Sprintf(
			`{"variant_id":%q,"quantity":%d,"properties":{"Engraving":%q}}`, variantID, quantity, words))
		require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	}
	read := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+cartID, "")
	total, ok := storefrontData(t, read)["total"].(float64)
	require.True(t, ok, read.Body.String())

	done := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		storefrontCompletionBody(t, int64(total)))

	assert.Equal(t, http.StatusConflict, done.Code, "body: %s", done.Body.String())
	assert.Equal(t, int64(2), sellableQuantity(ctx, t, itemID), "the first line's reservation was released")
}
