//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/adminui"
	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	paymentmodels "github.com/bdrtr/gobit/internal/modules/payment/models"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// returnRecordBody is the return record as the routes publish it, with the
// lines and the figure a refund is held to (ADR 0433).
type returnRecordBody struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	SoldFor int64  `json:"sold_for"`
	Lines   []struct {
		OrderLineItemID string `json:"order_line_item_id"`
		Quantity        int64  `json:"quantity"`
	} `json:"lines"`
}

// placedHappyOrder places the happy path's order of two units of a new
// variant and answers it with its line.
func placedHappyOrder(t *testing.T, product string) (orderID, collectionID, lineID string) {
	t.Helper()
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, product, map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, happyInitialStock)
	cartID, _ := prepareCart(ctx, t, customerID, variantID, happyQuantity)
	placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID: cartID, LocationID: stockLocationID, PaymentProviderID: paymentmanual.ID,
		PaymentData: paymentBehavior(t, paymentmanual.OutcomeAuthorize), Email: email,
		ExpectedTotal: happyTotal,
	})
	require.NoError(t, err)
	order, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err)
	require.Len(t, order.Items, 1)

	return placed.OrderID, placed.PaymentCollectionID, order.Items[0].ID
}

// receivedOneUnitReturn places the happy path's order of two units, opens a
// storefront return of one of them and receives it, and answers the order,
// its line and the return.
func receivedOneUnitReturn(t *testing.T, product string) (orderID, collectionID, lineID, returnID string) {
	t.Helper()

	orderID, collectionID, lineID = placedHappyOrder(t, product)
	requested := storefrontRequest(t, http.MethodPost, "/store/v1/orders/"+orderID+"/returns",
		`{"lines":[{"order_line_item_id":"`+lineID+`","quantity":1}]}`)
	require.Equal(t, http.StatusCreated, requested.Code, requested.Body.String())
	var opened afterSalesRecordResponse
	require.NoError(t, json.Unmarshal(requested.Body.Bytes(), &opened))

	received, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+orderID+"/returns/"+opened.Data.ID+"/receive",
		map[string]any{"location_id": stockLocationID})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, received.Code, received.Body.String())

	return orderID, collectionID, lineID, opened.Data.ID
}

// refundedOf reads what the collection has given back.
func refundedOf(t *testing.T, collectionID string) int64 {
	t.Helper()

	collection, err := paymentSvc.GetPaymentCollection(t.Context(), collectionID)
	require.NoError(t, err)

	return collection.RefundedAmount
}

// operatorRefund refunds part of a collection's one capture the way an
// operator does through the payment module's own route, naming no cause.
func operatorRefund(t *testing.T, collectionID string, amount int64, reason string) paymentmodels.Refund {
	t.Helper()
	ctx := t.Context()

	payments, err := paymentSvc.ListPayments(ctx, collectionID)
	require.NoError(t, err)
	require.Len(t, payments, 1, "the collection was paid by one capture")
	refund, err := paymentSvc.RefundPayment(ctx, payments[0].ID, amount, reason)
	require.NoError(t, err)

	return refund
}

// TestAReturnGivesBackAtMostWhatItsUnitsSoldFor is D269 (ADR 0433) on the
// route: one of two units of a 108 000 line comes back, and its refunds add up
// to at most the 54 000 that unit was sold for, however they are asked. The
// record carries its lines and that figure.
func TestAReturnGivesBackAtMostWhatItsUnitsSoldFor(t *testing.T) {
	orderID, collectionID, lineID, returnID := receivedOneUnitReturn(t, "E2E Ceiling Product")
	base := "/admin/v1/orders/" + orderID + "/returns"

	read, err := adminRequestWithBody(http.MethodGet, base+"/"+returnID, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	var single struct {
		Data returnRecordBody `json:"data"`
	}
	require.NoError(t, json.Unmarshal(read.Body.Bytes(), &single))
	if assert.Len(t, single.Data.Lines, 1, "the record names its line: %s", read.Body.String()) {
		assert.Equal(t, lineID, single.Data.Lines[0].OrderLineItemID)
		assert.Equal(t, int64(1), single.Data.Lines[0].Quantity)
	}
	assert.Equal(t, returnRefundAmount, single.Data.SoldFor, "one unit of 108 000 for two")

	listed, err := adminRequestWithBody(http.MethodGet, base, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	var page struct {
		Data []returnRecordBody `json:"data"`
	}
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &page))
	require.Len(t, page.Data, 1)
	assert.Equal(t, returnRefundAmount, page.Data[0].SoldFor, "the listing carries the figure too")
	assert.Len(t, page.Data[0].Lines, 1, "and the lines")

	refund := func(orderID, returnID string, amount int64) (int, string) {
		t.Helper()
		rec, err := adminRequestWithBody(http.MethodPost,
			"/admin/v1/orders/"+orderID+"/returns/"+returnID+"/refund",
			map[string]any{"amount": amount, "reason": "ceiling"})
		require.NoError(t, err)
		return rec.Code, rec.Body.String()
	}

	code, body := refund(orderID, returnID, 20_000)
	require.Equal(t, http.StatusOK, code, body)
	code, body = refund(orderID, returnID, 34_000)
	require.Equal(t, http.StatusOK, code, "a second part up to the unit's worth pays: %s", body)
	assert.Equal(t, returnRefundAmount, refundedOf(t, collectionID))

	code, body = refund(orderID, returnID, 1)
	assert.Equal(t, http.StatusConflict, code, "one past the unit's worth: %s", body)
	assert.Contains(t, body, "returns_workflow_refund_exceeds_return")
	assert.Equal(t, returnRefundAmount, refundedOf(t, collectionID), "the refused refund moved nothing")

	orderID, collectionID, _, returnID = receivedOneUnitReturn(t, "E2E Ceiling Zero Product")
	code, body = refund(orderID, returnID, 0)
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, returnRefundAmount, refundedOf(t, collectionID),
		"zero gives back what the unit was sold for, not the order's %d", happyTotal)
	code, body = refund(orderID, returnID, 0)
	assert.Equal(t, http.StatusConflict, code, "zero on a spent return pays nothing: %s", body)
	assert.Contains(t, body, "returns_workflow_refund_exceeds_return")

	orderID, collectionID, _, returnID = receivedOneUnitReturn(t, "E2E Ceiling Twice Product")
	code, body = refund(orderID, returnID, returnRefundAmount)
	require.Equal(t, http.StatusOK, code, body)
	code, body = refund(orderID, returnID, returnRefundAmount)
	assert.Equal(t, http.StatusConflict, code, "a second full refund pays nothing: %s", body)
	assert.Contains(t, body, "returns_workflow_refund_exceeds_return")
	assert.Equal(t, returnRefundAmount, refundedOf(t, collectionID))

	orderID, collectionID, _, returnID = receivedOneUnitReturn(t, "E2E Ceiling Over Product")
	code, body = refund(orderID, returnID, returnRefundAmount+1)
	assert.Equal(t, http.StatusConflict, code, "more than the unit's worth at once: %s", body)
	assert.Contains(t, body, "returns_workflow_refund_exceeds_return")
	assert.Zero(t, refundedOf(t, collectionID), "nothing moved")
}

// TestTheOrderPageRefundsAReturnOnce is D269 on the panel's default path: the
// received return's refund form, left empty, gives back what the unit was
// sold for, the same form again pays nothing, and a return form naming no
// line opens nothing.
func TestTheOrderPageRefundsAReturnOnce(t *testing.T) {
	orderID, collectionID, lineID, returnID := receivedOneUnitReturn(t, "E2E Panel Ceiling Product")
	send := panelAs(t, "order:read", "order:write")
	pagePath := adminui.OrdersPath + "/" + orderID
	form := url.Values{"amount": {""}, "currency": {taxedCurrency}, "reason": {""}}

	first := send(http.MethodPost, pagePath+"/after-sales/return/"+returnID+"/refund", form)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	assert.Contains(t, first.Body.String(), "540.00 TRY was refunded.")
	assert.Equal(t, returnRefundAmount, refundedOf(t, collectionID),
		"an empty amount gives back the unit's worth, not the order's %d", happyTotal)

	again := send(http.MethodPost, pagePath+"/after-sales/return/"+returnID+"/refund", form)
	assert.Equal(t, http.StatusUnprocessableEntity, again.Code, again.Body.String())
	assert.Contains(t, again.Body.String(), "gives back at most what its units were sold for")
	assert.Equal(t, returnRefundAmount, refundedOf(t, collectionID), "the repeated form moved nothing")

	before, err := adminRequestWithBody(http.MethodGet, "/admin/v1/orders/"+orderID+"/returns", nil)
	require.NoError(t, err)
	empty := send(http.MethodPost, pagePath+"/after-sales/return", url.Values{
		"line_id": {lineID}, "quantity": {""}, "line_refund": {""}, "currency": {taxedCurrency},
	})
	assert.Equal(t, http.StatusUnprocessableEntity, empty.Code, empty.Body.String())
	assert.Contains(t, empty.Body.String(), "Name at least one line coming back.")
	after, err := adminRequestWithBody(http.MethodGet, "/admin/v1/orders/"+orderID+"/returns", nil)
	require.NoError(t, err)
	assert.JSONEq(t, before.Body.String(), after.Body.String(), "no return was opened")
}

// TestASettledClaimIsRefusedByItsStatus is what the settle route's text says
// (ADR 0433): a refund claim settled once answers a second settle with 409
// returns_workflow_invalid_input, its status, and the collection gives back
// its figure once.
func TestASettledClaimIsRefusedByItsStatus(t *testing.T) {
	orderID, collectionID, _ := placedHappyOrder(t, "E2E Settled Claim Product")

	opened, err := adminRequestWithBody(http.MethodPost, "/admin/v1/orders/"+orderID+"/claims",
		map[string]any{"type": "refund", "refund_amount": 3_000, "reason": "arrived broken"})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, opened.Code, opened.Body.String())
	var claim afterSalesRecordResponse
	require.NoError(t, json.Unmarshal(opened.Body.Bytes(), &claim))
	settle := "/admin/v1/orders/" + orderID + "/claims/" + claim.Data.ID + "/settle"

	first, err := adminRequestWithBody(http.MethodPost, settle, map[string]any{"amount": 0})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	assert.Equal(t, int64(3_000), refundedOf(t, collectionID), "zero is the claim's own figure")

	again, err := adminRequestWithBody(http.MethodPost, settle, map[string]any{"amount": 0})
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, again.Code, again.Body.String())
	assert.Contains(t, again.Body.String(), "returns_workflow_invalid_input", "refused by its status")
	assert.Equal(t, int64(3_000), refundedOf(t, collectionID), "the second settle moved nothing")
}
