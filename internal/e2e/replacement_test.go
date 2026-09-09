//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	inventorymodels "github.com/bdrtr/gobit/internal/modules/inventory/models"
	inventorysvc "github.com/bdrtr/gobit/internal/modules/inventory/service"
	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"

	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// The hand-computed figures of the replacement scenario, derived from the happy
// path's by hand for the reason returns_test.go gives: recomputing them with
// the formula the production code uses would be making the same mistake twice.
const (
	// replacedQuantity is how many of the two bought units are sent again.
	replacedQuantity int64 = 1
	// stockAfterDispatch is the physical count once the replacement leaves:
	// 10 sold down to 8, minus the one sent to settle the claim.
	stockAfterDispatch int64 = happyRemainingStock - replacedQuantity
)

// replacementResponseBody is what the replacement endpoints answer with.
type replacementResponseBody struct {
	Data struct {
		ID            string  `json:"id"`
		ClaimID       string  `json:"claim_id"`
		Status        string  `json:"status"`
		FulfillmentID string  `json:"fulfillment_id"`
		DispatchedAt  *string `json:"dispatched_at"`
	} `json:"data"`
}

// dispatchResponseBody is what the dispatch endpoint answers with.
type dispatchResponseBody struct {
	Data struct {
		FulfillmentID string `json:"fulfillment_id"`
		SentUnits     int64  `json:"sent_units"`
		AlreadySent   bool   `json:"already_sent"`
	} `json:"data"`
}

// TestAClaimSettledWithGoodsTakesThemOffTheShelfAndPutsThemInAParcel walks the
// whole replacement journey over HTTP.
//
// The claim is that all four movements land, in three modules, from the
// requests a shop really makes: an operator opens a claim to be settled with
// goods, records WHAT to send, and sends it. Then the units are off the shelf,
// a parcel exists on the order, the ledger says why the units left, and the
// claim is closed.
//
// If it stops holding, the shop is wrong in the direction it cannot see: the
// customer is told a replacement is on its way, the warehouse count still
// includes goods that are in a box, and the month's stock report explains the
// gap as a sale nobody was paid for.
func TestAClaimSettledWithGoodsTakesThemOffTheShelfAndPutsThemInAParcel(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, inventoryItemID := newStockedVariant(ctx, t, "E2E Replaced Product",
		map[string]int64{taxedCurrency: happyUnitPrice}, happyInitialStock)

	cartID, _ := prepareCart(ctx, t, customerID, variantID, happyQuantity)
	placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: paymentmanual.ID,
		PaymentData:       paymentBehavior(t, paymentmanual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     happyTotal,
	})
	require.NoError(t, err, "the fixture order could not be placed")
	require.Equal(t, happyRemainingStock, stockLevel(ctx, t, inventoryItemID).StockedQuantity,
		"precondition: the sale must have taken the two units off the shelf")

	order, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err)
	require.Len(t, order.Items, 1, "precondition: the fixture order has a single line")
	lineID := order.Items[0].ID

	profileID := newShippingProfile(ctx, t, "E2E Replacement Profile")
	optionID := newShippingOption(ctx, t, profileID, "E2E Replacement Shipping", 0, false)

	// --- 1) the claim: this one is settled with GOODS, not with money ---
	claimed, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+placed.OrderID+"/claims",
		map[string]any{"type": "replace", "reason": "one of the two arrived broken"})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, claimed.Code,
		"the claim could not be opened; body: %s", claimed.Body.String())

	var claim afterSalesRecordResponse
	require.NoError(t, json.Unmarshal(claimed.Body.Bytes(), &claim))
	claimID := claim.Data.ID
	require.NotEmpty(t, claimID)

	// --- 2) the record of WHAT to send, which sends nothing ---
	recorded, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+placed.OrderID+"/claims/"+claimID+"/replacements",
		map[string]any{
			"shipping_option_id": optionID,
			"location_id":        stockLocationID,
			"lines": []map[string]any{
				{"order_line_item_id": lineID, "quantity": replacedQuantity},
			},
		})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, recorded.Code,
		"the replacement could not be recorded; body: %s", recorded.Body.String())

	var replacement replacementResponseBody
	require.NoError(t, json.Unmarshal(recorded.Body.Bytes(), &replacement))
	replacementID := replacement.Data.ID
	require.NotEmpty(t, replacementID)
	assert.Equal(t, "requested", replacement.Data.Status)

	assert.Equal(t, happyRemainingStock, stockLevel(ctx, t, inventoryItemID).StockedQuantity,
		"recording what to send must not take it off the shelf; if it does, a claim opened "+
			"and withdrawn has made stock disappear")

	// --- 3) the dispatch: three modules move ---
	sent, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+placed.OrderID+"/claims/"+claimID+
			"/replacements/"+replacementID+"/dispatch", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, sent.Code,
		"the dispatch must succeed; a 500 here means the return flow is not bound in the "+
			"container and the endpoint fell into its fail-closed branch. body: %s",
		sent.Body.String())

	var dispatch dispatchResponseBody
	require.NoError(t, json.Unmarshal(sent.Body.Bytes(), &dispatch))
	assert.Equal(t, replacedQuantity, dispatch.Data.SentUnits)
	assert.False(t, dispatch.Data.AlreadySent)
	require.NotEmpty(t, dispatch.Data.FulfillmentID, "the goods have to leave in a parcel")

	// The shelf.
	assert.Equal(t, stockAfterDispatch, stockLevel(ctx, t, inventoryItemID).StockedQuantity,
		"the goods sent have to come OFF the shelf; if they do not, they are counted as "+
			"sellable while they are in a box")

	// The ledger, which is the sentence that could not be written before.
	movement := newestMovement(ctx, t, inventoryItemID)
	assert.Equal(t, inventorymodels.MovementReplacement, movement.Reason,
		"nobody paid for these units, and a 'sale' here is the wrong word in the one "+
			"column an operator reads the table by")
	assert.Equal(t, -replacedQuantity, movement.Delta)
	assert.NotEmpty(t, movement.ReservationID,
		"units that leave against a promise name the promise")

	// The parcel, bound to the order.
	parcels, err := adminRequestWithBody(http.MethodGet,
		"/admin/v1/orders/"+placed.OrderID+"/fulfillments", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, parcels.Code)
	assert.Contains(t, parcels.Body.String(), dispatch.Data.FulfillmentID,
		"the parcel the goods left in has to be findable from the order")

	// The record, and the claim it settles.
	read, err := adminRequestWithBody(http.MethodGet,
		"/admin/v1/orders/"+placed.OrderID+"/claims/"+claimID+"/replacements/"+replacementID,
		nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, read.Code)

	var afterDispatch replacementResponseBody
	require.NoError(t, json.Unmarshal(read.Body.Bytes(), &afterDispatch))
	assert.Equal(t, "dispatched", afterDispatch.Data.Status)
	assert.Equal(t, dispatch.Data.FulfillmentID, afterDispatch.Data.FulfillmentID)
	require.NotNil(t, afterDispatch.Data.DispatchedAt, "the moment has to be on the record")

	settled, err := orderSvc.GetClaim(ctx, claimID)
	require.NoError(t, err)
	assert.Equal(t, "completed", settled.Status.String(),
		"sending the goods IS the settlement; a claim left open would ask an operator to "+
			"settle a second time")

	// --- 4) the second press of the button sends nothing ---
	again, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+placed.OrderID+"/claims/"+claimID+
			"/replacements/"+replacementID+"/dispatch", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, again.Code, "body: %s", again.Body.String())

	var repeated dispatchResponseBody
	require.NoError(t, json.Unmarshal(again.Body.Bytes(), &repeated))
	assert.True(t, repeated.Data.AlreadySent)
	assert.Zero(t, repeated.Data.SentUnits)
	assert.Equal(t, dispatch.Data.FulfillmentID, repeated.Data.FulfillmentID,
		"the answer names the parcel the goods really left in")

	assert.Equal(t, stockAfterDispatch, stockLevel(ctx, t, inventoryItemID).StockedQuantity,
		"a second press must not take a second unit off the shelf")
}

// newestMovement returns the item's most recent ledger row.
func newestMovement(
	ctx context.Context, t *testing.T, inventoryItemID string,
) inventorymodels.Movement {
	t.Helper()

	movements, err := inventorySvc.ListMovements(ctx, inventorysvc.ListMovementsInput{
		InventoryItemID: inventoryItemID,
	})
	require.NoError(t, err, "the movement ledger could not be read")
	require.NotEmpty(t, movements, "the item has no movements at all")

	return movements[0]
}
