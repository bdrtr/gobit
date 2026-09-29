//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// TestAGiftBoxNamesWhatItIsMadeOf is ADR 0234 on the production wiring: the
// operator makes a gift box of a towel and two soaps; the admin surface reads
// the composition back, the storefront reads it on the box's variant over REST
// and GraphQL, a variant with its own stock item is refused as a bundle and a
// component is refused its deletion. The box reads in stock from its parts
// (ADR 0235).
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
	assert.True(t, store.Data.Variants[0].InStock, "ten towels and ten soaps make a box (ADR 0235)")

	graph := gqlRequest(t, publishableKey, `query($handle: String) {
		product(handle: $handle) { variants { inStock bundleComponents { variantId quantity } } }
	}`, map[string]any{"handle": product.Handle})
	require.Equal(t, http.StatusOK, graph.Code, graph.Body.String())
	assert.JSONEq(t, fmt.Sprintf(`{"data":{"product":{"variants":[{"inStock":true,"bundleComponents":[`+
		`{"variantId":%q,"quantity":1},{"variantId":%q,"quantity":2}]}]}}}`, towel, soap), graph.Body.String())

	rec, err := adminRequestWithBody(http.MethodDelete, "/admin/v1/variants/"+soap, nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, rec.Code, "a component is not deleted: %s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), box)
}

// TestAGiftBoxSellsFromItsParts is ADR 0235 on the production wiring: three
// boxes of a towel and two soaps take three towels and six soaps off the
// shelf, the order line keeps what a box was made of, a written-off box puts
// its towel and two soaps back, and a returned box does the same at the
// receiving warehouse.
func TestAGiftBoxSellsFromItsParts(t *testing.T) {
	ctx := t.Context()
	customerID, email := newCustomer(ctx, t)
	box := newVariant(ctx, t, "E2E Sold Gift Box", map[string]int64{taxedCurrency: 30_000})
	towel, towelItem := newStockedVariant(ctx, t, "E2E Sold Towel", map[string]int64{taxedCurrency: 8_000}, 10)
	soap, soapItem := newStockedVariant(ctx, t, "E2E Sold Soap", map[string]int64{taxedCurrency: 5_000}, 10)
	_, err := productSvc.SetVariantBundle(ctx, box, []productmodels.BundleComponent{
		{VariantID: towel, Quantity: 1}, {VariantID: soap, Quantity: 2},
	})
	require.NoError(t, err)

	cartID, totals := prepareCart(ctx, t, customerID, box, 3)
	placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: paymentmanual.ID,
		PaymentData:       paymentBehavior(t, paymentmanual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     totals.Total,
	})
	require.NoError(t, err, "a box whose parts are on the shelf is sold")
	assert.Equal(t, int64(7), stockLevel(ctx, t, towelItem).StockedQuantity, "three boxes take three towels")
	assert.Equal(t, int64(4), stockLevel(ctx, t, soapItem).StockedQuantity, "and six soaps")

	read, err := adminRequestWithBody(http.MethodGet, "/admin/v1/orders/"+placed.OrderID, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	var order struct {
		Data struct {
			Items []struct {
				ID         string          `json:"id"`
				Components json.RawMessage `json:"components"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(read.Body.Bytes(), &order))
	require.Len(t, order.Data.Items, 1, read.Body.String())
	assert.JSONEq(t, fmt.Sprintf(`[{"variant_id":%q,"quantity":1},{"variant_id":%q,"quantity":2}]`, towel, soap),
		string(order.Data.Items[0].Components), "the order keeps what a box was made of")
	lineID := order.Data.Items[0].ID

	canceled, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+placed.OrderID+"/line-cancellations", map[string]any{
			"order_line_item_id": lineID, "quantity": 1, "reason": "one box was never made",
		})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, canceled.Code, canceled.Body.String())
	requireStockEventually(ctx, t, towelItem, 8, "a written-off box puts its towel back")
	requireStockEventually(ctx, t, soapItem, 6, "and its two soaps")

	requested := storefrontRequest(t, http.MethodPost, "/store/v1/orders/"+placed.OrderID+"/returns",
		`{"reason":"a gift nobody wanted","lines":[{"order_line_item_id":"`+lineID+`","quantity":1}]}`)
	require.Equal(t, http.StatusCreated, requested.Code, requested.Body.String())
	var opened afterSalesRecordResponse
	require.NoError(t, json.Unmarshal(requested.Body.Bytes(), &opened))
	received, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+placed.OrderID+"/returns/"+opened.Data.ID+"/receive",
		map[string]any{"location_id": stockLocationID})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, received.Code, received.Body.String())
	var receipt receiveReturnResponseBody
	require.NoError(t, json.Unmarshal(received.Body.Bytes(), &receipt))
	assert.Empty(t, receipt.Data.Warnings, "the box tracks no stock of its own and that is no warning")
	assert.Equal(t, 1, receipt.Data.RestockedLines)
	assert.Equal(t, int64(3), receipt.Data.RestockedUnits, "a towel and two soaps")
	assert.Equal(t, int64(9), stockLevel(ctx, t, towelItem).StockedQuantity, "a returned box puts its towel back")
	assert.Equal(t, int64(8), stockLevel(ctx, t, soapItem).StockedQuantity, "and its two soaps")
}

// TestAGiftBoxIsReplacedFromItsParts is ADR 0238 on the production wiring. A
// claim on two sold boxes asks for both again from a shelf short of soap: the
// dispatch holds the towels, is refused on the soaps, and the withdrawal gives
// the towels back. The box is then remade of three soaps in the catalog, and a
// replacement of one box still sends the towel and two soaps it was sold with.
func TestAGiftBoxIsReplacedFromItsParts(t *testing.T) {
	ctx := t.Context()
	customerID, email := newCustomer(ctx, t)
	box := newVariant(ctx, t, "E2E Replaced Gift Box", map[string]int64{taxedCurrency: 30_000})
	towel, towelItem := newStockedVariant(ctx, t, "E2E Replaced Towel", map[string]int64{taxedCurrency: 8_000}, 10)
	soap, soapItem := newStockedVariant(ctx, t, "E2E Replaced Soap", map[string]int64{taxedCurrency: 5_000}, 10)
	_, err := productSvc.SetVariantBundle(ctx, box, []productmodels.BundleComponent{
		{VariantID: towel, Quantity: 1}, {VariantID: soap, Quantity: 2},
	})
	require.NoError(t, err)

	cartID, totals := prepareCart(ctx, t, customerID, box, 2)
	placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: paymentmanual.ID,
		PaymentData:       paymentBehavior(t, paymentmanual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     totals.Total,
	})
	require.NoError(t, err)
	order, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err)
	lineID := order.Items[0].ID
	optionID := newShippingOption(ctx, t, newShippingProfile(ctx, t, "E2E Box Replacement Profile"),
		"E2E Box Replacement Shipping", 0, false)

	claimed, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+placed.OrderID+"/claims",
		map[string]any{"type": "replace", "reason": "both boxes arrived crushed"})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, claimed.Code, claimed.Body.String())
	var claim afterSalesRecordResponse
	require.NoError(t, json.Unmarshal(claimed.Body.Bytes(), &claim))
	base := "/admin/v1/orders/" + placed.OrderID + "/claims/" + claim.Data.ID + "/replacements"
	replace := func(boxes int64) string {
		t.Helper()
		recorded, err := adminRequestWithBody(http.MethodPost, base, map[string]any{
			"shipping_option_id": optionID,
			"location_id":        stockLocationID,
			"lines":              []map[string]any{{"order_line_item_id": lineID, "quantity": boxes}},
		})
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, recorded.Code, recorded.Body.String())
		var replacement replacementResponseBody
		require.NoError(t, json.Unmarshal(recorded.Body.Bytes(), &replacement))
		return replacement.Data.ID
	}

	_, err = inventorySvc.SetInventoryLevel(ctx, soapItem, stockLocationID, 3)
	require.NoError(t, err)
	short := replace(2)
	sent, err := adminRequestWithBody(http.MethodPost, base+"/"+short+"/dispatch", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusConflict, sent.Code, sent.Body.String())
	assert.Contains(t, sent.Body.String(), "returns_workflow_stock_not_held")
	require.Equal(t, int64(6), sellableQuantity(ctx, t, towelItem),
		"precondition: two towels are held for two boxes by the refused dispatch")

	read, err := adminRequestWithBody(http.MethodGet, base+"/"+short, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	var held struct {
		Data struct {
			Items []struct {
				Parts []struct {
					VariantID     string `json:"variant_id"`
					Quantity      int64  `json:"quantity"`
					ReservationID string `json:"reservation_id"`
				} `json:"parts"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(read.Body.Bytes(), &held))
	require.Len(t, held.Data.Items, 1)
	require.Len(t, held.Data.Items[0].Parts, 2, read.Body.String())
	assert.Equal(t, towel, held.Data.Items[0].Parts[0].VariantID)
	assert.NotEmpty(t, held.Data.Items[0].Parts[0].ReservationID, "the towel's promise is on its part")
	assert.Equal(t, soap, held.Data.Items[0].Parts[1].VariantID)
	assert.Empty(t, held.Data.Items[0].Parts[1].ReservationID, "no soap was set aside")

	withdrawn, err := adminRequestWithBody(http.MethodPost, base+"/"+short+"/cancel", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, withdrawn.Code, withdrawn.Body.String())
	assert.Equal(t, int64(8), sellableQuantity(ctx, t, towelItem), "the withdrawal gives the towels back")

	_, err = inventorySvc.SetInventoryLevel(ctx, soapItem, stockLocationID, 6)
	require.NoError(t, err)
	_, err = productSvc.SetVariantBundle(ctx, box, []productmodels.BundleComponent{{VariantID: soap, Quantity: 3}})
	require.NoError(t, err)

	one := replace(1)
	sent, err = adminRequestWithBody(http.MethodPost, base+"/"+one+"/dispatch", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, sent.Code, sent.Body.String())
	var dispatched dispatchResponseBody
	require.NoError(t, json.Unmarshal(sent.Body.Bytes(), &dispatched))
	assert.Equal(t, int64(3), dispatched.Data.SentUnits, "a towel and two soaps left the warehouse")
	assert.Equal(t, int64(7), stockLevel(ctx, t, towelItem).StockedQuantity,
		"the box sent is the one sold: its towel leaves, though the catalog's box holds none now")
	assert.Equal(t, int64(4), stockLevel(ctx, t, soapItem).StockedQuantity, "and two soaps, not three")
}
