//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file is gap D268 (ADR 0423): a parcel that came back to the sender
// undelivered kept holding its units in every count, so the order owed them no
// more and a write-off put none of them back on the shelf; only an order
// return, which nothing offered, did. It now holds them only as far as a return
// or a replacement speaks for them.

// shippedParcel places the write-off fixture's order, opens a parcel of all its
// units on the order's own route and ships it.
func shippedParcel(t *testing.T, name string) (orderID, lineID, itemID, optionID, parcelID string) {
	t.Helper()
	ctx := t.Context()

	orderID, lineID, itemID = placedForParcels(ctx, t, name)
	profileID := newShippingProfile(ctx, t, name+" Profile")
	optionID = newShippingOption(ctx, t, profileID, name+" Shipping", shippingOptionFee, false)

	code, parcelID, body := openOrderParcel(t, orderID, map[string]any{
		"shipping_option_id": optionID,
		"idempotency_key":    "e2e-came-back-" + orderID,
		"items":              []map[string]any{{"line_item_id": lineID, "quantity": cancelQuantity}},
	})
	require.Equal(t, http.StatusCreated, code, "body: %s", body)

	shipped, err := adminRequestWithBody(http.MethodPost, "/admin/v1/fulfillments/"+parcelID+"/ship", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, shipped.Code, "body: %s", shipped.Body.String())

	return orderID, lineID, itemID, optionID, parcelID
}

// markCameBack reports the parcel come back undelivered.
func markCameBack(t *testing.T, parcelID string) {
	t.Helper()

	back, err := adminRequestWithBody(http.MethodPost, "/admin/v1/fulfillments/"+parcelID+"/returned", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, back.Code, "body: %s", back.Body.String())
}

// shippedThenCameBack is [shippedParcel] with the parcel then reported come
// back undelivered.
func shippedThenCameBack(t *testing.T, name string) (orderID, lineID, itemID, optionID string) {
	t.Helper()

	orderID, lineID, itemID, optionID, parcelID := shippedParcel(t, name)
	markCameBack(t, parcelID)

	return orderID, lineID, itemID, optionID
}

// writeOff writes units of the line off on the order's route.
func writeOff(t *testing.T, orderID, lineID string, units int64) {
	t.Helper()

	canceled, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+orderID+"/line-cancellations", map[string]any{
			"order_line_item_id": lineID, "quantity": units, "reason": "returned to sender",
		})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, canceled.Code, "body: %s", canceled.Body.String())
}

// requireStockStays fails when the shelf leaves want within two seconds, which
// is also the time the bus is given to run the restock it would make.
func requireStockStays(t *testing.T, itemID string, want int64, why string) {
	t.Helper()

	require.Never(t, func() bool {
		levels, err := inventorySvc.ListInventoryLevels(t.Context(), itemID)
		if err != nil || len(levels) != 1 {
			return false
		}

		return levels[0].StockedQuantity != want
	}, 2*time.Second, 100*time.Millisecond, why)
}

// TestAParcelThatCameBackIsSentAgainOrPutBack is the defect's two operator acts,
// each on its own order so that both reds show on a tree without the fix, and
// the path a return takes, which the fix keeps whole.
func TestAParcelThatCameBackIsSentAgainOrPutBack(t *testing.T) {
	t.Run("a new parcel takes the units again", func(t *testing.T) {
		orderID, lineID, _, optionID := shippedThenCameBack(t, "E2E Came Back Resent")

		// The fixture's order was sold no delivery, so the order route asks for
		// the units by name rather than filling the parcel with what is owed.
		code, parcelID, body := openOrderParcel(t, orderID, map[string]any{
			"shipping_option_id": optionID,
			"idempotency_key":    "e2e-came-back-again-" + orderID,
			"items":              []map[string]any{{"line_item_id": lineID, "quantity": cancelQuantity}},
		})
		require.Equal(t, http.StatusCreated, code,
			"the units came back with no return behind them, so the order owes them again: %s", body)

		held, err := shippingSvc.QuantitiesOfFulfillment(t.Context(), parcelID)
		require.NoError(t, err)
		assert.Equal(t, map[string]int64{lineID: cancelQuantity}, held,
			"the new parcel holds every unit that came back")
	})

	t.Run("a write-off puts the units back on the shelf", func(t *testing.T) {
		orderID, lineID, itemID, _ := shippedThenCameBack(t, "E2E Came Back Written Off")

		writeOff(t, orderID, lineID, cancelQuantity)

		requireStockEventually(t.Context(), t, itemID, cancelInitialStock,
			"the three units are back in the warehouse and no parcel or return speaks for them")
	})

	t.Run("a return keeps the units it asks back", func(t *testing.T) {
		orderID, lineID, itemID, optionID := shippedThenCameBack(t, "E2E Came Back Returned")

		opened, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+orderID+"/returns",
			map[string]any{"reason": "the carrier brought it back", "lines": []map[string]any{{
				"order_line_item_id": lineID, "quantity": cancelQuantity,
			}}})
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, opened.Code, opened.Body.String())

		code, _, body := openOrderParcel(t, orderID, map[string]any{
			"shipping_option_id": optionID,
			"idempotency_key":    "e2e-came-back-returned-" + orderID,
			"items":              []map[string]any{{"line_item_id": lineID, "quantity": 1}},
		})
		assert.Equal(t, http.StatusConflict, code,
			"the return asks the three units back, so the order owes none to a parcel: %s", body)
		assert.Contains(t, body, "fulfillment_line_not_dispatchable")

		var record afterSalesRecordResponse
		require.NoError(t, json.Unmarshal(opened.Body.Bytes(), &record))
		received, err := adminRequestWithBody(http.MethodPost,
			"/admin/v1/orders/"+orderID+"/returns/"+record.Data.ID+"/receive",
			map[string]any{"location_id": stockLocationID})
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, received.Code, received.Body.String())
		requireStockEventually(t.Context(), t, itemID, cancelInitialStock,
			"the return's receipt puts the three units back")
	})

	t.Run("a replacement keeps the units it sends again", func(t *testing.T) {
		orderID, lineID, _, optionID := shippedThenCameBack(t, "E2E Came Back Replaced")

		claimed, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+orderID+"/claims",
			map[string]any{"type": "replace", "reason": "the carrier brought it back"})
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, claimed.Code, claimed.Body.String())
		var claim afterSalesRecordResponse
		require.NoError(t, json.Unmarshal(claimed.Body.Bytes(), &claim))
		recorded, err := adminRequestWithBody(http.MethodPost,
			"/admin/v1/orders/"+orderID+"/claims/"+claim.Data.ID+"/replacements", map[string]any{
				"shipping_option_id": optionID, "location_id": stockLocationID,
				"lines": []map[string]any{{"order_line_item_id": lineID, "quantity": cancelQuantity}},
			})
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, recorded.Code, recorded.Body.String())

		code, _, body := openOrderParcel(t, orderID, map[string]any{
			"shipping_option_id": optionID,
			"idempotency_key":    "e2e-came-back-replaced-" + orderID,
			"items":              []map[string]any{{"line_item_id": lineID, "quantity": 1}},
		})
		assert.Equal(t, http.StatusConflict, code,
			"the replacement sends the three units again, so the order owes none to a parcel: %s", body)
		assert.Contains(t, body, "fulfillment_line_not_dispatchable")
	})

	t.Run("a write-off puts back no unit the parcel sent again holds", func(t *testing.T) {
		orderID, lineID, itemID, optionID := shippedThenCameBack(t, "E2E Came Back Sent Again")

		opened, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+orderID+"/returns",
			map[string]any{"reason": "one of them is not wanted", "lines": []map[string]any{{
				"order_line_item_id": lineID, "quantity": 1,
			}}})
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, opened.Code, opened.Body.String())
		code, _, body := openOrderParcel(t, orderID, map[string]any{
			"shipping_option_id": optionID,
			"idempotency_key":    "e2e-came-back-sent-again-" + orderID,
			"items":              []map[string]any{{"line_item_id": lineID, "quantity": cancelQuantity - 1}},
		})
		require.Equal(t, http.StatusCreated, code,
			"the return asks one unit back, so the order owes the other two: %s", body)

		writeOff(t, orderID, lineID, cancelQuantity-1)

		requireStockStays(t, itemID, cancelStockAfterSale,
			"the two written-off units are in the parcel sent again and the third is the return's, "+
				"so none of them goes back on the shelf")
	})
}

// TestAWrittenOffParcelThatComesBackPutsItsUnitsBack is a line written off
// whole while its parcel was on the way: the write-off puts nothing back, since
// the units left, and the parcel then comes back undelivered. Marking it come
// back is what puts them on the shelf, through the cancellation flow's recount
// (ADR 0423).
func TestAWrittenOffParcelThatComesBackPutsItsUnitsBack(t *testing.T) {
	orderID, lineID, itemID, _, parcelID := shippedParcel(t, "E2E Came Back After Write-Off")

	writeOff(t, orderID, lineID, cancelQuantity)
	requireStockStays(t, itemID, cancelStockAfterSale, "the three written-off units are on their way")

	markCameBack(t, parcelID)
	requireStockEventually(t.Context(), t, itemID, cancelInitialStock,
		"the parcel came back and no return or replacement speaks for its units, so the "+
			"written-off units are back on the shelf")
}
