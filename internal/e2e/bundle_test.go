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
