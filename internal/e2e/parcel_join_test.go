//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fulfillingwf "github.com/bdrtr/gobit/internal/workflows/fulfilling"
)

// This file proves ADR 0197 on the production wiring: an addition joins a
// pending parcel of the order it adds to, the parcel answers for both, and the
// widened link lets one parcel be bound to two orders.

// joinParcel binds the order to the parcel through the admin surface, the
// request carrying request as its body ("" for none).
func joinParcel(t *testing.T, orderID, fulfillmentID, request string) (status int, body, code string) {
	t.Helper()

	rec := adminCartRequest(t, http.MethodPut,
		"/admin/v1/orders/"+orderID+"/fulfillments/"+fulfillmentID, request)
	if rec.Code != http.StatusOK {
		return rec.Code, rec.Body.String(), errorCode(t, rec)
	}

	return rec.Code, rec.Body.String(), ""
}

// TestAnAdditionTravelsInItsParentsParcel is the whole act: the addition,
// which recorded no address of its own, joins the parent's waiting parcel, and
// the parcel is then among the addition's shipments and on its timeline.
func TestAnAdditionTravelsInItsParentsParcel(t *testing.T) {
	f, parentID := addressedParent(t)
	_, parcelID := openSpyParcel(t, parentID, spyOption(t))
	additionID := f.checkout(t, f.openCart(t, parentID))
	request := naming(onlyLine(t, additionID), 1)

	status, body, code := joinParcel(t, additionID, parcelID, request)
	require.Equal(t, http.StatusOK, status, "code %s: %s", code, body)
	assert.Contains(t, body, parcelID, "the answer is the addition's shipments, the parcel among them")

	again, _, code := joinParcel(t, additionID, parcelID, request)
	assert.Equal(t, http.StatusOK, again, "joining twice binds nothing new; code %s", code)

	listed := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+parentID+"/fulfillments", "")
	require.Equal(t, http.StatusOK, listed.Code)
	assert.Contains(t, listed.Body.String(), parcelID, "the parcel is still the parent's")

	timeline := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+additionID+"/timeline", "")
	require.Equal(t, http.StatusOK, timeline.Code, "body: %s", timeline.Body.String())
	assert.Contains(t, timeline.Body.String(), `"shipment.opened"`,
		"the addition's timeline shows the parcel it travels in")
}

// TestAnAdditionGoingElsewhereDoesNotJoin refuses an addition whose own
// shipping address is not its parent's, and a parcel no longer waiting.
func TestAnAdditionGoingElsewhereDoesNotJoin(t *testing.T) {
	f, parentID := addressedParent(t)
	_, parcelID := openSpyParcel(t, parentID, spyOption(t))

	cartID := f.openCart(t, parentID)
	elsewhere := fmt.Sprintf(`{"first_name":"Someone","address_1":"1 Other St","city":"Elsewhere",`+
		`"postal_code":"22222","country_code":%q}`, taxedCountry)
	rec := storefrontRequest(t, http.MethodPut, "/store/v1/carts/"+cartID+"/shipping-address", elsewhere)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	additionID := f.checkout(t, cartID)

	status, _, code := joinParcel(t, additionID, parcelID, naming(onlyLine(t, additionID), 1))
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "order_ships_elsewhere", code)

	canceled, err := adminRequestWithBody(http.MethodPost, "/admin/v1/fulfillments/"+parcelID+"/cancel", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, canceled.Code, "body: %s", canceled.Body.String())

	plain := f.checkout(t, f.openCart(t, parentID))
	status, _, code = joinParcel(t, plain, parcelID, naming(onlyLine(t, plain), 1))
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "fulfilling_parcel_not_waiting", code)
}

// joinedAddition places an addressed parent, opens a parcel of its one unit,
// places an addition of one unit to it, and joins the addition to the parcel.
// It returns the fixture, both orders' single lines and the parcel.
func joinedAddition(t *testing.T) (f additionFixture, parentLine, additionID, additionLine, parcelID string) {
	t.Helper()

	f, parentID := addressedParent(t)
	_, parcelID = openSpyParcel(t, parentID, spyOption(t))
	additionID = f.checkout(t, f.openCart(t, parentID))
	additionLine = onlyLine(t, additionID)

	status, body, code := joinParcel(t, additionID, parcelID, naming(additionLine, 1))
	require.Equal(t, http.StatusOK, status, "code %s: %s", code, body)

	return f, onlyLine(t, parentID), additionID, additionLine, parcelID
}

// naming is a join's body naming units of one line.
func naming(lineID string, units int64) string {
	return fmt.Sprintf(`{"items":[{"line_item_id":%q,"quantity":%d}]}`, lineID, units)
}

// onlyLine is the id of the order's one line.
func onlyLine(t *testing.T, orderID string) string {
	t.Helper()

	order, err := orderSvc.GetOrder(t.Context(), orderID)
	require.NoError(t, err)
	require.Len(t, order.Items, 1)

	return order.Items[0].ID
}

// TestAnAdditionsUnitsTravelAsItsParcelsItems is gap D264 (b), ADR 0428: an
// addition joined to its parent's parcel rode itemless, so its units were
// counted by nothing. Each consequence has an order of its own, so every red
// shows on a tree without the fix.
func TestAnAdditionsUnitsTravelAsItsParcelsItems(t *testing.T) {
	t.Run("the parcel holds the addition's units and nothing offers them again", func(t *testing.T) {
		ctx := t.Context()
		_, parentLine, additionID, additionLine, parcelID := joinedAddition(t)

		parcel, err := shippingSvc.GetFulfillment(ctx, parcelID)
		require.NoError(t, err)
		held := make(map[string]int64, len(parcel.Items))
		for _, item := range parcel.Items {
			held[item.LineItemID] += item.Quantity
		}
		assert.Equal(t, map[string]int64{parentLine: 1, additionLine: 1}, held,
			"the parcel holds the parent's unit and the one the addition owed")

		again, _, code := joinParcel(t, additionID, parcelID, naming(additionLine, 1))
		require.Equal(t, http.StatusOK, again, "a repeated join changes nothing; code %s", code)
		repeated, err := shippingSvc.QuantitiesOfFulfillment(ctx, parcelID)
		require.NoError(t, err)
		assert.Equal(t, held, repeated, "a repeated join puts no unit in twice")

		flow, err := fulfillingwf.FromContainer(ctr)
		require.NoError(t, err)
		owed, err := flow.DispatchableQuantities(ctx, additionID, nil)
		require.NoError(t, err)
		assert.Equal(t, map[string]int64{additionLine: 0}, owed,
			"the order route and the panel offer none of the units the parcel holds")

		opened, _, body := openOrderParcel(t, additionID, map[string]any{
			"shipping_option_id": spyOption(t),
			"idempotency_key":    "e2e-joined-twice-" + additionID,
			"items":              []map[string]any{{"line_item_id": additionLine, "quantity": 1}},
		})
		assert.Equal(t, http.StatusConflict, opened, "a second parcel takes no unit the first holds: %s", body)
		assert.Contains(t, body, "fulfillment_line_not_dispatchable")
	})

	t.Run("a write-off puts back none of the addition's units in the box", func(t *testing.T) {
		ctx := t.Context()
		f, parentLine, additionID, additionLine, parcelID := joinedAddition(t)
		afterSales := additionStock - 2

		writeOff(t, additionID, additionLine, 1)
		requireStockStays(t, f.stockItemID, afterSales,
			"the addition's unit is in the parent's box; putting it on the shelf sells it twice")

		writeOff(t, f.parentID, parentLine, 1)
		requireStockStays(t, f.stockItemID, afterSales, "the parent's unit is in the box too")

		dropped, err := adminRequestWithBody(http.MethodPost, "/admin/v1/fulfillments/"+parcelID+"/cancel", nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, dropped.Code, "body: %s", dropped.Body.String())
		requireStockEventually(ctx, t, f.stockItemID, additionStock,
			"the canceled parcel puts each written-off unit back against the order whose line it is")
	})

	t.Run("an addition that owes nothing does not join", func(t *testing.T) {
		f, parentID := addressedParent(t)
		_, parcelID := openSpyParcel(t, parentID, spyOption(t))
		additionID := f.checkout(t, f.openCart(t, parentID))
		_, ownParcel := openSpyParcel(t, additionID, spyOption(t))

		status, body, code := joinParcel(t, additionID, parcelID, naming(onlyLine(t, additionID), 1))
		assert.Equal(t, http.StatusConflict, status, "body: %s", body)
		assert.Equal(t, "fulfillment_line_not_dispatchable", code,
			"the addition's unit is in parcel %s already, so it adds no goods to the parent's", ownParcel)

		listed := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+additionID+"/fulfillments", "")
		require.Equal(t, http.StatusOK, listed.Code)
		assert.NotContains(t, listed.Body.String(), parcelID, "a refused join binds nothing")
	})

	t.Run("an addition sold no delivery names what it puts in", func(t *testing.T) {
		f, parentID := addressedParent(t)
		_, parcelID := openSpyParcel(t, parentID, spyOption(t))
		additionID := f.checkout(t, f.openCart(t, parentID))

		status, body, code := joinParcel(t, additionID, parcelID, "")
		assert.Equal(t, http.StatusUnprocessableEntity, status, "body: %s", body)
		assert.Equal(t, "fulfilling_items_required", code,
			"there is no one delivery to default the parcel's goods to, as an open's")
		held, err := shippingSvc.QuantitiesOfFulfillment(t.Context(), parcelID)
		require.NoError(t, err)
		assert.Len(t, held, 1, "the parcel holds the parent's unit alone")
	})

	t.Run("a join holds what it names and leaves the rest owed", func(t *testing.T) {
		ctx := t.Context()
		f, parentID := addressedParent(t)
		_, parcelID := openSpyParcel(t, parentID, spyOption(t))
		second, _ := newStockedVariant(ctx, t, "E2E Addition Second", map[string]int64{
			taxedCurrency: additionUnitPrice,
		}, additionStock)
		cartID := f.openCart(t, parentID)
		added := addAdminLine(t, cartID, testChannelID, second, 1)
		require.Equal(t, http.StatusCreated, added.Code, "body: %s", added.Body.String())
		done := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
			storefrontCompletionBody(t, 2*additionTotal))
		require.Equal(t, http.StatusOK, done.Code, "body: %s", done.Body.String())
		additionID, _ := storefrontData(t, done)["order_id"].(string)
		order, err := orderSvc.GetOrder(ctx, additionID)
		require.NoError(t, err)
		require.Len(t, order.Items, 2)
		named, left := order.Items[0].ID, order.Items[1].ID

		status, body, code := joinParcel(t, additionID, parcelID, naming(named, 1))
		require.Equal(t, http.StatusOK, status, "code %s: %s", code, body)

		held, err := shippingSvc.QuantitiesOfFulfillment(ctx, parcelID)
		require.NoError(t, err)
		assert.Equal(t, int64(1), held[named], "the named unit is in the box")
		assert.Zero(t, held[left], "the unit nobody named is not")
		flow, err := fulfillingwf.FromContainer(ctr)
		require.NoError(t, err)
		owed, err := flow.DispatchableQuantities(ctx, additionID, nil)
		require.NoError(t, err)
		assert.Equal(t, map[string]int64{named: 0, left: 1}, owed,
			"the unit left out is still owed, and offered to a parcel of its own")
	})

	t.Run("a join naming more than is owed is refused", func(t *testing.T) {
		f, parentID := addressedParent(t)
		_, parcelID := openSpyParcel(t, parentID, spyOption(t))
		additionID := f.checkout(t, f.openCart(t, parentID))

		status, body, code := joinParcel(t, additionID, parcelID, naming(onlyLine(t, additionID), 2))
		assert.Equal(t, http.StatusConflict, status, "body: %s", body)
		assert.Equal(t, "fulfillment_line_not_dispatchable", code)

		listed := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+additionID+"/fulfillments", "")
		require.Equal(t, http.StatusOK, listed.Code)
		assert.NotContains(t, listed.Body.String(), parcelID, "a refused join binds nothing")
	})

	t.Run("a binding that was lost is written again after the parcel left", func(t *testing.T) {
		ctx := t.Context()
		f, _, additionID, additionLine, parcelID := joinedAddition(t)
		// What a link write that failed after the items committed leaves.
		require.NoError(t, links.Delete(ctx, "order_fulfillment", additionID, parcelID))
		shipped, err := adminRequestWithBody(http.MethodPost, "/admin/v1/fulfillments/"+parcelID+"/ship", nil)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, shipped.Code, "body: %s", shipped.Body.String())
		writeOff(t, additionID, additionLine, 1)
		requireStockStays(t, f.stockItemID, additionStock-2, "the written-off unit left in the box")

		status, body, code := joinParcel(t, additionID, parcelID, naming(additionLine, 1))
		require.Equal(t, http.StatusOK, status, "asking again binds the addition, whatever the parcel's state; "+
			"code %s: %s", code, body)
		assert.Contains(t, body, parcelID, "the parcel is among the addition's shipments again")

		markCameBack(t, parcelID)
		requireStockEventually(ctx, t, f.stockItemID, additionStock-1,
			"the parcel came back, so the addition's written-off unit goes back on the shelf")
	})
}
