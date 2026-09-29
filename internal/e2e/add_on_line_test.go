//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAnEngravingIsALineOfItsRingsOwn is ADR 0229 on the production wiring: a
// ring's product accepts an engraving; the storefront adds the ring with the
// engraving, which opens as a line bound to it at its own price, is refused a
// wrap the ring does not take, raises the ring and sees the engraving follow,
// is refused a write to the engraving alone, and the order placed keeps the
// engraving bound to its ring; after the sale the ring comes back and is
// written off only with its engraving (ADR 0230).
func TestAnEngravingIsALineOfItsRingsOwn(t *testing.T) {
	ctx := t.Context()
	ring, _ := newStockedVariant(ctx, t, "E2E Add-on Ring", map[string]int64{taxedCurrency: 20_000}, 10)
	engraving, _ := newStockedVariant(ctx, t, "E2E Engraving", map[string]int64{taxedCurrency: 5_000}, 10)
	wrap, _ := newStockedVariant(ctx, t, "E2E Wrap", map[string]int64{taxedCurrency: 1_000}, 10)
	variant, err := productSvc.GetVariant(ctx, ring)
	require.NoError(t, err)
	_, err = productSvc.SetProductAddOns(ctx, variant.ProductID, []string{engraving})
	require.NoError(t, err)
	cartID := openCartWithKey(t, publishableKey)

	add := func(addOn string) (int, string) {
		rec := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/line-items", fmt.Sprintf(
			`{"variant_id":%q,"quantity":1,"add_ons":[{"variant_id":%q,"properties":{"Text":"Ada"}}]}`,
			ring, addOn))
		return rec.Code, rec.Body.String()
	}
	code, body := add(wrap)
	assert.Equal(t, http.StatusUnprocessableEntity, code, body)
	assert.Contains(t, body, "cart_workflow_add_on_not_accepted")
	code, body = add(engraving)
	require.Equal(t, http.StatusCreated, code, body)

	lines := func() (ringLine, engravingLine map[string]any, total int64) {
		t.Helper()
		read := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+cartID, "")
		require.Equal(t, http.StatusOK, read.Code, read.Body.String())
		cart := storefrontData(t, read)
		items, ok := cart["items"].([]any)
		require.True(t, ok, read.Body.String())
		require.Len(t, items, 2, read.Body.String())
		for _, item := range items {
			line, ok := item.(map[string]any)
			require.True(t, ok)
			if line["variant_id"] == ring {
				ringLine = line
			} else {
				engravingLine = line
			}
		}
		amount, ok := cart["total"].(float64)
		require.True(t, ok)
		return ringLine, engravingLine, int64(amount)
	}
	ringLine, engravingLine, _ := lines()
	require.NotNil(t, ringLine)
	require.NotNil(t, engravingLine)
	assert.Equal(t, ringLine["id"], engravingLine["parent_line_id"], "the engraving is bound to its ring")
	assert.InDelta(t, 5_000, engravingLine["unit_price"], 0, "at its own price")
	assert.Equal(t, map[string]any{"Text": "Ada"}, engravingLine["properties"])

	ringID, _ := ringLine["id"].(string)
	engravingID, _ := engravingLine["id"].(string)
	raised := storefrontRequest(t, http.MethodPatch, "/store/v1/carts/"+cartID+"/line-items/"+ringID,
		`{"quantity":2}`)
	require.Equal(t, http.StatusOK, raised.Code, raised.Body.String())
	alone := storefrontRequest(t, http.MethodPatch, "/store/v1/carts/"+cartID+"/line-items/"+engravingID,
		`{"quantity":1}`)
	assert.Equal(t, http.StatusUnprocessableEntity, alone.Code, alone.Body.String())
	assert.Contains(t, alone.Body.String(), "cart_line_is_an_add_on")
	_, engravingLine, total := lines()
	assert.InDelta(t, 2, engravingLine["quantity"], 0, "the engraving follows its ring")

	done := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		storefrontCompletionBody(t, total))
	require.Equal(t, http.StatusOK, done.Code, "body: %s", done.Body.String())
	orderID, _ := storefrontData(t, done)["order_id"].(string)
	require.NotEmpty(t, orderID)

	order, err := orderSvc.GetOrder(ctx, orderID)
	require.NoError(t, err)
	require.Len(t, order.Items, 2)
	assert.Equal(t, ring, order.Items[0].VariantID, "the ring is listed before its engraving (ADR 0233)")
	var ringOrderLine string
	for _, line := range order.Items {
		if line.VariantID == ring {
			ringOrderLine = line.ID
			assert.Nil(t, line.ParentLineItemID)
		}
	}
	for _, line := range order.Items {
		if line.VariantID == engraving {
			require.NotNil(t, line.ParentLineItemID)
			assert.Equal(t, ringOrderLine, *line.ParentLineItemID, "the order keeps the engraving with its ring")
			assert.Equal(t, int64(2), line.Quantity)
			assert.Equal(t, int64(5_000), line.UnitPrice)
		}
	}

	rec := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+orderID, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"parent_line_item_id":"`+ringOrderLine+`"`)

	// After the sale (ADR 0230): the ring comes back only with its engraving,
	// and a ring written off takes its engraving with it.
	var engravingOrderLine string
	for _, line := range order.Items {
		if line.VariantID == engraving {
			engravingOrderLine = line.ID
		}
	}
	askBack := func(lines string) (int, string) {
		rec := storefrontRequest(t, http.MethodPost, "/store/v1/orders/"+orderID+"/returns",
			`{"reason":"too small","lines":[`+lines+`]}`)
		return rec.Code, rec.Body.String()
	}
	code, body = askBack(`{"order_line_item_id":"` + ringOrderLine + `","quantity":1}`)
	assert.Equal(t, http.StatusUnprocessableEntity, code, body)
	assert.Contains(t, body, "order_add_on_follows_its_line")
	code, body = askBack(`{"order_line_item_id":"` + ringOrderLine + `","quantity":1},` +
		`{"order_line_item_id":"` + engravingOrderLine + `","quantity":1}`)
	assert.Equal(t, http.StatusCreated, code, body)

	offAlone, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+orderID+"/line-cancellations",
		map[string]any{"order_line_item_id": engravingOrderLine, "quantity": 1, "reason": "no longer wanted"})
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, offAlone.Code, offAlone.Body.String())
	written, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+orderID+"/line-cancellations",
		map[string]any{"order_line_item_id": ringOrderLine, "quantity": 1, "reason": "out of stock"})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, written.Code, written.Body.String())
	cancellations, err := orderSvc.ListLineCancellations(ctx, orderID)
	require.NoError(t, err)
	byLine := map[string]int64{}
	for _, c := range cancellations {
		byLine[c.OrderLineItemID] += c.Quantity
	}
	assert.Equal(t, map[string]int64{ringOrderLine: 1, engravingOrderLine: 1}, byLine,
		"the engraving is written off with its ring")
}
