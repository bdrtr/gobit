//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// engravedRings makes the given number of rings whose products each take one
// engraving as an add-on (ADR 0228), and returns the rings and the engraving.
func engravedRings(t *testing.T, titles ...string) (rings []string, engraving string) {
	t.Helper()
	ctx := t.Context()
	engraving, _ = newStockedVariant(ctx, t, "E2E Ordered Engraving", map[string]int64{taxedCurrency: 5_000}, 10)
	for _, title := range titles {
		ring, _ := newStockedVariant(ctx, t, title, map[string]int64{taxedCurrency: 20_000}, 10)
		variant, err := productSvc.GetVariant(ctx, ring)
		require.NoError(t, err)
		_, err = productSvc.SetProductAddOns(ctx, variant.ProductID, []string{engraving})
		require.NoError(t, err)
		rings = append(rings, ring)
	}
	return rings, engraving
}

// addEngravedRing adds one ring engraved with the given words to the cart over
// the storefront, and returns the response.
func addEngravedRing(t *testing.T, cartID, ring, engraving, words string) (code int, body string) {
	t.Helper()
	rec := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/line-items", fmt.Sprintf(
		`{"variant_id":%q,"quantity":1,"add_ons":[{"variant_id":%q,"properties":{"Text":%q}}]}`,
		ring, engraving, words))
	return rec.Code, rec.Body.String()
}

// storefrontCart reads the cart over the storefront: its lines and its total.
func storefrontCart(t *testing.T, cartID string) (items []map[string]any, total int64) {
	t.Helper()
	read := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+cartID, "")
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	cart := storefrontData(t, read)
	raw, ok := cart["items"].([]any)
	require.True(t, ok, read.Body.String())
	for _, item := range raw {
		line, ok := item.(map[string]any)
		require.True(t, ok)
		items = append(items, line)
	}
	amount, ok := cart["total"].(float64)
	require.True(t, ok)
	return items, int64(amount)
}

// completeStorefrontCart completes the cart at its total and returns the order.
func completeStorefrontCart(t *testing.T, cartID string) string {
	t.Helper()
	_, total := storefrontCart(t, cartID)
	done := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		storefrontCompletionBody(t, total))
	require.Equal(t, http.StatusOK, done.Code, "body: %s", done.Body.String())
	orderID, _ := storefrontData(t, done)["order_id"].(string)
	require.NotEmpty(t, orderID)
	return orderID
}

// TestTwoEngravedRingsPrintEachEngravingUnderItsRing is ADR 0393 on the
// production wiring: a cart of two rings, each engraved, becomes an order whose
// lines are read ring, its engraving, ring, its engraving, and the invoice
// issued for it prints its rows in that order.
func TestTwoEngravedRingsPrintEachEngravingUnderItsRing(t *testing.T) {
	ctx := t.Context()
	rings, engraving := engravedRings(t, "E2E Ordered Ring For Ada", "E2E Ordered Ring For Bo")
	cartID := openCartWithKey(t, publishableKey)
	code, body := addEngravedRing(t, cartID, rings[0], engraving, "Ada")
	require.Equal(t, http.StatusCreated, code, body)
	code, body = addEngravedRing(t, cartID, rings[1], engraving, "Bo")
	require.Equal(t, http.StatusCreated, code, body)

	orderID := completeStorefrontCart(t, cartID)

	order, err := orderSvc.GetOrder(ctx, orderID)
	require.NoError(t, err)
	require.Len(t, order.Items, 4)
	type read struct{ variant, words string }
	got := make([]read, 0, len(order.Items))
	for i, line := range order.Items {
		got = append(got, read{line.VariantID, line.Properties["Text"]})
		if line.ParentLineItemID != nil {
			require.Positive(t, i)
			assert.Equal(t, order.Items[i-1].ID, *line.ParentLineItemID, "line %d is under its own ring", i)
		}
	}
	assert.Equal(t, []read{{rings[0], ""}, {engraving, "Ada"}, {rings[1], ""}, {engraving, "Bo"}}, got)

	writeStoreProfile(t)
	recorder, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+orderID+"/invoice", issueInvoiceBody())
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	var issued invoiceIssueResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &issued))
	document, err := adminRequestWithBody(http.MethodGet, "/admin/v1/invoices/"+issued.Data.InvoiceID, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, document.Code, document.Body.String())
	var invoice invoiceDocumentResponse
	require.NoError(t, json.Unmarshal(document.Body.Bytes(), &invoice))
	rows := make(map[int32]string, len(invoice.Data.Lines))
	for _, line := range invoice.Data.Lines {
		rows[line.Position] = line.Description
	}
	descriptions := make([]string, 0, len(rows))
	for _, position := range slices.Sorted(maps.Keys(rows)) {
		descriptions = append(descriptions, rows[position])
	}
	assert.Equal(t, []string{
		"E2E Ordered Ring For Ada", "E2E Ordered Engraving", "E2E Ordered Ring For Bo", "E2E Ordered Engraving",
	}, descriptions, "the document prints each engraving under its ring")
}

// TestARaiseAsksTheEngravingsListAgain is ADR 0393 on the production wiring:
// the ring's product stops taking the engraving after the engraved ring entered
// the cart; raising the ring is refused with the add's 422 and the cart keeps
// one, while keeping it at one is written and the cart is completed as it is.
func TestARaiseAsksTheEngravingsListAgain(t *testing.T) {
	ctx := t.Context()
	rings, engraving := engravedRings(t, "E2E Delisted Engraving Ring")
	cartID := openCartWithKey(t, publishableKey)
	code, body := addEngravedRing(t, cartID, rings[0], engraving, "Ada")
	require.Equal(t, http.StatusCreated, code, body)
	variant, err := productSvc.GetVariant(ctx, rings[0])
	require.NoError(t, err)
	_, err = productSvc.SetProductAddOns(ctx, variant.ProductID, []string{})
	require.NoError(t, err)

	items, _ := storefrontCart(t, cartID)
	var ringID string
	for _, line := range items {
		if line["variant_id"] == rings[0] {
			ringID, _ = line["id"].(string)
		}
	}
	require.NotEmpty(t, ringID)
	patch := func(quantity int) (int, string) {
		rec := storefrontRequest(t, http.MethodPatch, "/store/v1/carts/"+cartID+"/line-items/"+ringID,
			fmt.Sprintf(`{"quantity":%d}`, quantity))
		return rec.Code, rec.Body.String()
	}

	code, body = patch(2)
	assert.Equal(t, http.StatusUnprocessableEntity, code, body)
	assert.Contains(t, body, "cart_workflow_add_on_not_accepted")
	items, _ = storefrontCart(t, cartID)
	for _, line := range items {
		assert.InDelta(t, 1, line["quantity"], 0, "the cart still holds one of each: %v", line)
	}

	code, body = patch(1)
	assert.Equal(t, http.StatusOK, code, "a quantity that does not rise asks nothing: %s", body)

	orderID := completeStorefrontCart(t, cartID)
	order, err := orderSvc.GetOrder(ctx, orderID)
	require.NoError(t, err)
	assert.Len(t, order.Items, 2, "the completion asks the list nothing")
}
