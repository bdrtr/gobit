//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// This file is gap D264 (ADR 0409): a parcel opened through the order's own
// route, the one the panel uses too, carried no items, so the units in it were
// counted by no write-off, no dispatch bound and no cancel. The write-off chain
// in order_cancel_test.go opens its parcels through the fulfillment module's
// endpoint, which always took items, and so never saw it.

// placedForParcels checks out the write-off fixture's order, three units of one
// line on no delivery, and returns it with its line and inventory item.
func placedForParcels(ctx context.Context, t *testing.T, name string) (orderID, lineID, itemID string) {
	t.Helper()

	customerID, email := newCustomer(ctx, t)
	variantID, inventoryItemID := newStockedVariant(ctx, t, name,
		map[string]int64{taxedCurrency: cancelUnitPrice}, cancelInitialStock)
	cartID, _ := prepareCart(ctx, t, customerID, variantID, cancelQuantity)
	placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: paymentmanual.ID,
		PaymentData:       paymentBehavior(t, paymentmanual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     cancelTotal,
	})
	require.NoError(t, err, "the fixture order could not be placed")
	order, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err)
	require.Len(t, order.Items, 1)

	return placed.OrderID, order.Items[0].ID, inventoryItemID
}

// openOrderParcel opens a parcel through POST /admin/v1/orders/{id}/fulfillments
// with the body given and returns the response and the parcel's id.
func openOrderParcel(t *testing.T, orderID string, body map[string]any) (code int, parcelID, answer string) {
	t.Helper()

	rec, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+orderID+"/fulfillments", body)
	require.NoError(t, err)
	var opened struct {
		Data struct {
			FulfillmentID string `json:"fulfillment_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &opened)

	return rec.Code, opened.Data.FulfillmentID, rec.Body.String()
}

// TestAParcelFromTheOrderRouteHoldsItsUnits is the defect and its three
// consequences, each in a subtest with its own order so that every red shows on
// a tree without the fix.
func TestAParcelFromTheOrderRouteHoldsItsUnits(t *testing.T) {
	t.Run("a write-off puts back no unit the parcel holds", func(t *testing.T) {
		ctx := t.Context()
		orderID, lineID, itemID := placedForParcels(ctx, t, "E2E Held Write-Off")
		profileID := newShippingProfile(ctx, t, "E2E Held Write-Off Profile")
		optionID := newShippingOption(ctx, t, profileID, "E2E Held Write-Off Shipping", shippingOptionFee, false)

		code, parcelID, body := openOrderParcel(t, orderID, map[string]any{
			"shipping_option_id": optionID,
			"idempotency_key":    "e2e-held-writeoff-" + orderID,
			"items":              []map[string]any{{"line_item_id": lineID, "quantity": cancelQuantity}},
		})
		require.Equal(t, http.StatusCreated, code, "body: %s", body)

		canceled, err := adminRequestWithBody(http.MethodPost,
			"/admin/v1/orders/"+orderID+"/line-cancellations", map[string]any{
				"order_line_item_id": lineID, "quantity": cancelQuantity, "reason": "changed their mind",
			})
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, canceled.Code, "body: %s", canceled.Body.String())

		require.Never(t, func() bool {
			levels, err := inventorySvc.ListInventoryLevels(ctx, itemID)
			if err != nil || len(levels) != 1 {
				return false
			}

			return levels[0].StockedQuantity != cancelStockAfterSale
		}, 2*time.Second, 100*time.Millisecond,
			"the three units are in a box the warehouse is picking; putting them on the shelf "+
				"sells them twice")

		dropped, err := adminRequestWithBody(http.MethodPost, "/admin/v1/fulfillments/"+parcelID+"/cancel", nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, dropped.Code, "body: %s", dropped.Body.String())
		requireStockEventually(ctx, t, itemID, cancelInitialStock,
			"the canceled parcel releases the three written-off units it held (ADR 0139)")
	})

	t.Run("a second parcel cannot take the units a first one holds", func(t *testing.T) {
		ctx := t.Context()
		orderID, lineID, _ := placedForParcels(ctx, t, "E2E Held Twice")
		profileID := newShippingProfile(ctx, t, "E2E Held Twice Profile")
		optionID := newShippingOption(ctx, t, profileID, "E2E Held Twice Shipping", shippingOptionFee, false)

		code, _, body := openOrderParcel(t, orderID, map[string]any{
			"shipping_option_id": optionID,
			"idempotency_key":    "e2e-held-twice-" + orderID,
			"items":              []map[string]any{{"line_item_id": lineID, "quantity": cancelInParcel}},
		})
		require.Equal(t, http.StatusCreated, code, "body: %s", body)

		moduleParcel := func(key string, units int64) *httptest.ResponseRecorder {
			t.Helper()
			rec, err := adminRequestWithBody(http.MethodPost, "/admin/v1/fulfillments", map[string]any{
				"reference":          orderID,
				"shipping_option_id": optionID,
				"idempotency_key":    key + "-" + orderID,
				"items":              []map[string]any{{"line_item_id": lineID, "quantity": units}},
			})
			require.NoError(t, err)
			return rec
		}
		second := moduleParcel("e2e-held-twice-module", cancelInParcel)
		assert.Equal(t, http.StatusConflict, second.Code, "body: %s", second.Body.String())
		assert.Contains(t, second.Body.String(), "fulfillment_line_not_dispatchable")

		rest := moduleParcel("e2e-held-twice-rest", cancelQuantity-cancelInParcel)
		assert.Equal(t, http.StatusCreated, rest.Code,
			"the unit the first parcel left is the second one's to take; body: %s", rest.Body.String())
	})

	t.Run("an order sold one delivery packs every unit it owes", func(t *testing.T) {
		ctx := t.Context()
		orderID, _ := deliveryOrder(t, spyOptionPriced(t, soldDeliveryFee, false))
		order, err := orderSvc.GetOrder(ctx, orderID)
		require.NoError(t, err)
		require.NotEmpty(t, order.Items)

		code, parcelID, body := openOrderParcel(t, orderID, map[string]any{
			"idempotency_key": fmt.Sprintf("e2e-held-owed-%d", fixtureCounter.Add(1)),
		})
		require.Equal(t, http.StatusCreated, code, "body: %s", body)

		parcel, err := shippingSvc.GetFulfillment(ctx, parcelID)
		require.NoError(t, err)
		held := make(map[string]int64, len(parcel.Items))
		for _, item := range parcel.Items {
			held[item.LineItemID] = item.Quantity
		}
		want := make(map[string]int64, len(order.Items))
		for _, line := range order.Items {
			want[line.ID] = line.Quantity
		}
		assert.Equal(t, want, held, "a parcel opened with no items holds every unit still owed")

		code, _, body = openOrderParcel(t, orderID, map[string]any{
			"idempotency_key": fmt.Sprintf("e2e-held-owed-%d", fixtureCounter.Add(1)),
		})
		assert.Equal(t, http.StatusConflict, code, "body: %s", body)
		assert.Contains(t, body, "fulfillment_nothing_owed")
	})

	t.Run("an order sold no delivery names its items", func(t *testing.T) {
		ctx := t.Context()
		orderID, _, _ := placedForParcels(ctx, t, "E2E Held Unnamed")
		profileID := newShippingProfile(ctx, t, "E2E Held Unnamed Profile")
		optionID := newShippingOption(ctx, t, profileID, "E2E Held Unnamed Shipping", shippingOptionFee, false)

		code, _, body := openOrderParcel(t, orderID, map[string]any{
			"shipping_option_id": optionID,
			"idempotency_key":    "e2e-held-unnamed-" + orderID,
		})
		assert.Equal(t, http.StatusUnprocessableEntity, code, "body: %s", body)
		assert.Contains(t, body, "fulfilling_items_required")
	})
}
