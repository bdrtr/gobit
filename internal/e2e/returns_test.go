//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// This file proves that AFTER-SALES works end to end: a customer asks to send
// goods back, an operator receives them, and money goes out again.
//
// # What only this file can show
//
// The three steps live in three different modules and one flow, and no unit
// test can see more than one of them at a time. internal/workflows/returns has
// unit tests for all three of its entry points, but every one of them runs on
// fakes: the "order" they receive is a struct the test wrote, the "warehouse"
// counts in a map, and the "payment collection" gives back whatever the fake
// was told to give back. What those tests cannot witness is the part that is
// only true of a real database and a real router:
//
//   - the UPDATE that marks a return received and the read that pages its lines
//     are SQL, and a mistyped column or a WHERE that matches nothing compiles;
//   - the stock that comes back has to land in the inventory module's ledger,
//     which is a different module reached through a link table;
//   - the refund has to find the order's payment collection through that same
//     link service, move money in the payment module, and be written back onto
//     the ORDER's summary — three modules that do not import each other;
//   - and the whole chain is only reachable if the flow is registered in the
//     container under a name that is written out in two packages, because the
//     order module resolves it BY NAME at request time and fails closed.
//
// # Why the customer returns ONE of the two units
//
// Every number in the scenario is then distinct, and a figure that leaks from
// the wrong place gives itself away. Had the whole order been sent back, "the
// refund" and "the order total" would be the same integer, "the stock that came
// back" and "the stock that went out" would be the same integer, and a refund
// that silently paid out the order total instead of the amount asked for would
// look correct.

// The hand-computed figures of the return scenario.
//
// They are derived from the happy path's figures (see order_flow_test.go) by
// hand, and deliberately not recomputed here from the same formula the
// production code uses — that would be making the same mistake twice.
const (
	// returnedQuantity is how many of the two bought units come back.
	returnedQuantity int64 = 1
	// stockAfterReceipt is the physical quantity once the unit is back on the
	// shelf: 10 sold down to 8, plus the one that came back.
	stockAfterReceipt int64 = happyRemainingStock + returnedQuantity
	// returnRefundAmount is one unit's worth of money: 45 000 for the goods
	// plus 9 000 of tax at 20%.
	returnRefundAmount int64 = 54_000
)

// receiveReturnResponseBody is what the receive endpoint answers with.
type receiveReturnResponseBody struct {
	Data struct {
		RestockedLines int      `json:"restocked_lines"`
		RestockedUnits int64    `json:"restocked_units"`
		Warnings       []string `json:"warnings"`
	} `json:"data"`
}

// refundReturnResponseBody is what the refund and settle endpoints answer with.
type refundReturnResponseBody struct {
	Data struct {
		RefundedAmount  int64    `json:"refunded_amount"`
		SummaryRecorded bool     `json:"summary_recorded"`
		Warnings        []string `json:"warnings"`
	} `json:"data"`
}

// afterSalesRecordResponse is the shape the return, exchange and claim records
// share on the wire; only the fields the scenarios assert on are named.
type afterSalesRecordResponse struct {
	Data struct {
		ID         string  `json:"id"`
		OrderID    string  `json:"order_id"`
		Status     string  `json:"status"`
		ReceivedAt *string `json:"received_at"`
	} `json:"data"`
}

// TestAReturnedUnitComesBackToStockAndItsRefundReachesTheOrder walks the whole
// after-sales journey over HTTP.
//
// The claim is that the three movements a return causes all land, in three
// different modules, from three separate requests that a shop really makes:
// the customer opens the request from the storefront, an operator receives the
// goods into a NAMED warehouse, and the shop decides afterwards to pay part of
// the money back.
//
// If it stops holding, the shop's books and its shelves disagree with each
// other in the most expensive direction: goods are in the building that the
// warehouse count says are not (they get bought again and cannot be shipped),
// or money has left the payment provider that the order still reports as
// unrefunded — which is the figure the B2B spending window reads, so the
// customer's credit stays consumed by a sale that was partly undone.
func TestAReturnedUnitComesBackToStockAndItsRefundReachesTheOrder(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, inventoryItemID := newStockedVariant(ctx, t, "E2E Returned Product", map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, happyInitialStock)

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
	require.NoError(t, err, "the fixture order could not be read back")
	require.Len(t, order.Items, 1, "precondition: the fixture order has a single line")
	lineID := order.Items[0].ID

	// --- 1) the CUSTOMER asks, from the storefront, and nothing moves yet ---
	//
	// A request is a request. The endpoint is reachable with a publishable key
	// and nothing else (ADR 0008 leaves proving the order is yours to the
	// embedder), which is only defensible while the request itself moves no
	// stock and no money. That is the property asserted here rather than
	// assumed.
	requested := storefrontRequest(t, http.MethodPost,
		"/store/v1/orders/"+placed.OrderID+"/returns",
		`{"reason":"one of the two arrived scratched","lines":[{"order_line_item_id":"`+
			lineID+`","quantity":1}]}`)
	require.Equal(t, http.StatusCreated, requested.Code,
		"the storefront must be able to open a return request; body: %s", requested.Body.String())

	var opened afterSalesRecordResponse
	require.NoError(t, json.Unmarshal(requested.Body.Bytes(), &opened),
		"the return record could not be decoded; body: %s", requested.Body.String())
	returnID := opened.Data.ID
	require.NotEmpty(t, returnID, "the opened return must carry an identity")
	assert.Equal(t, placed.OrderID, opened.Data.OrderID,
		"the return must be written against the order it was opened on")
	assert.Equal(t, "requested", opened.Data.Status,
		"a return the customer opened is only REQUESTED; anything else would mean the "+
			"storefront had already moved goods or money")

	assert.Equal(t, happyRemainingStock, stockLevel(ctx, t, inventoryItemID).StockedQuantity,
		"asking to return something must not put it back on the shelf; if it does, a "+
			"customer who never posts the parcel has created stock out of nothing")

	// --- 2) the OPERATOR receives the goods into a named warehouse ---
	received, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+placed.OrderID+"/returns/"+returnID+"/receive",
		map[string]any{"location_id": stockLocationID})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, received.Code,
		"the receipt must succeed; a 500 here means the return flow is not bound in the "+
			"container and the endpoint fell into its fail-closed branch. body: %s",
		received.Body.String())

	var receipt receiveReturnResponseBody
	require.NoError(t, json.Unmarshal(received.Body.Bytes(), &receipt),
		"the receipt could not be decoded; body: %s", received.Body.String())
	assert.Empty(t, receipt.Data.Warnings,
		"there must be NO warning: every entry means the goods arrived and the warehouse "+
			"count was left wrong, which is work for a human")
	assert.Equal(t, 1, receipt.Data.RestockedLines,
		"the one line that came back has to be restocked")
	assert.Equal(t, returnedQuantity, receipt.Data.RestockedUnits,
		"the receipt must put back the quantity the CUSTOMER named (%d), not the quantity "+
			"they bought (%d); the return record is the only place the agreed figure lives",
		returnedQuantity, happyQuantity)

	level := stockLevel(ctx, t, inventoryItemID)
	assert.Equal(t, stockAfterReceipt, level.StockedQuantity,
		"the PHYSICAL quantity has to rise by the returned unit (%d + %d): the goods are in "+
			"the building whether the ledger says so or not, and a count that stays at %d "+
			"means the shop cannot sell what it is holding",
		happyRemainingStock, returnedQuantity, happyRemainingStock)
	assert.Equal(t, int64(0), level.ReservedQuantity,
		"a receipt promises the returned unit to nobody; a reservation appearing here would "+
			"make the unit unsellable forever")
	assert.Equal(t, stockAfterReceipt, sellableQuantity(ctx, t, inventoryItemID),
		"the returned unit has to be SELLABLE again; stock that is counted but not sellable "+
			"is the same as stock that never came back")

	// The record has to say the goods arrived, and say it in the row rather
	// than only in the answer that was written a moment ago.
	stored, err := adminRequestWithBody(http.MethodGet,
		"/admin/v1/orders/"+placed.OrderID+"/returns/"+returnID, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, stored.Code, stored.Body.String())

	var storedReturn afterSalesRecordResponse
	require.NoError(t, json.Unmarshal(stored.Body.Bytes(), &storedReturn),
		"the stored return could not be decoded; body: %s", stored.Body.String())
	assert.Equal(t, "received", storedReturn.Data.Status,
		"the stored record has to say the goods arrived; it is the only thing that says "+
			"where the units on the shelf came from")
	assert.NotNil(t, storedReturn.Data.ReceivedAt,
		"the receipt has to be STAMPED: without a moment, nobody can tell a return that "+
			"arrived today from one that has been sitting unopened for a month")

	// --- 3) the SHOP decides what to pay back ---
	//
	// Refunding is a separate request because it is a separate decision: the
	// goods being here is a fact, what they are worth after inspection is a
	// judgement. The amount is PARTIAL on purpose (see the file header).
	refunded, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+placed.OrderID+"/returns/"+returnID+"/refund",
		map[string]any{"amount": returnRefundAmount, "reason": "one unit, minus nothing"})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, refunded.Code,
		"the refund must succeed; body: %s", refunded.Body.String())

	var refund refundReturnResponseBody
	require.NoError(t, json.Unmarshal(refunded.Body.Bytes(), &refund),
		"the refund answer could not be decoded; body: %s", refunded.Body.String())
	assert.Empty(t, refund.Data.Warnings,
		"a warning here means the money LEFT and something about recording it failed")
	assert.Equal(t, returnRefundAmount, refund.Data.RefundedAmount,
		"the refund has to be the amount that was ASKED for; the order's total (%d) coming "+
			"back here would mean the endpoint refunded the whole sale for a single unit",
		happyTotal)
	assert.True(t, refund.Data.SummaryRecorded,
		"false means the money left and the ORDER does not say so, which is the one "+
			"outcome an operator has to be told about explicitly")

	collection, err := paymentSvc.GetPaymentCollection(ctx, placed.PaymentCollectionID)
	require.NoError(t, err, "the payment collection must be readable from the payment module")
	assert.Equal(t, returnRefundAmount, collection.RefundedAmount,
		"the money has to have moved in the PAYMENT module; the endpoint answering a figure "+
			"it never sent is exactly what a fake-backed test cannot tell apart")
	assert.Equal(t, happyTotal, collection.CapturedAmount,
		"a refund must not unwind the capture: what was taken stays taken and what went "+
			"back is its own figure, or the collection can no longer say what it holds")

	afterRefund, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err, "the order could not be read back after the refund")
	assert.Equal(t, returnRefundAmount, afterRefund.Summary.RefundedTotal,
		"the ORDER's summary has to carry the refund; it is the figure the B2B spending "+
			"window reads, so a zero here keeps the customer's credit consumed by a sale "+
			"that was partly undone")
	assert.Equal(t, happyTotal, afterRefund.Summary.PaidTotal,
		"the paid total is what was COLLECTED and a refund does not lower it; the two "+
			"figures are kept apart so an operator can see both halves of the story")
}

// TestASecondReceiveIsRefused pins down the one guard that makes restocking
// safe to do at all.
//
// The order module's transition table turns a second receive into a NO-OP —
// the record keeps the first arrival's moment — and if that were the whole
// story a double-click would be harmless. It is not, because the WAREHOUSE
// does not no-op: putting stock back is deliberately not idempotent, since two
// calls are meant to describe two physical arrivals. So the second receive has
// to be refused ABOVE the record, in the flow, and be refused loudly enough
// that an operator can tell it apart from a success.
//
// What breaks if it stops holding is silent and permanent: every repeated click
// invents goods that were never sent, and the shelf count drifts up with no row
// anywhere saying where the extra units came from.
func TestASecondReceiveIsRefused(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, inventoryItemID := newStockedVariant(ctx, t, "E2E Twice-Received Product", map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, happyInitialStock)

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

	order, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err, "the fixture order could not be read back")
	require.Len(t, order.Items, 1, "precondition: the fixture order has a single line")

	requested := storefrontRequest(t, http.MethodPost,
		"/store/v1/orders/"+placed.OrderID+"/returns",
		`{"lines":[{"order_line_item_id":"`+order.Items[0].ID+`","quantity":1}]}`)
	require.Equal(t, http.StatusCreated, requested.Code,
		"the fixture return could not be opened; body: %s", requested.Body.String())

	var opened afterSalesRecordResponse
	require.NoError(t, json.Unmarshal(requested.Body.Bytes(), &opened))
	returnID := opened.Data.ID

	path := "/admin/v1/orders/" + placed.OrderID + "/returns/" + returnID + "/receive"
	body := map[string]any{"location_id": stockLocationID}

	first, err := adminRequestWithBody(http.MethodPost, path, body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, first.Code,
		"precondition: the first receipt has to succeed; body: %s", first.Body.String())
	require.Equal(t, stockAfterReceipt, stockLevel(ctx, t, inventoryItemID).StockedQuantity,
		"precondition: the first receipt has to put the unit back")

	second, err := adminRequestWithBody(http.MethodPost, path, body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, second.Code,
		"receiving the same return twice has to be a CONFLICT. A 200 would mean the "+
			"operator was told the second parcel arrived when no second parcel exists, "+
			"and 409 is what lets a client tell 'already done' apart from 'try again'. "+
			"body: %s", second.Body.String())

	assert.Equal(t, stockAfterReceipt, stockLevel(ctx, t, inventoryItemID).StockedQuantity,
		"the refused receipt must leave the shelf exactly as the first one left it (%d); "+
			"%d here would mean the same unit was counted in twice and the shop now "+
			"believes it holds goods nobody ever sent",
		stockAfterReceipt, stockAfterReceipt+returnedQuantity)
}

// TestAClaimSettledWithAReplacementIsRefused holds the framework to what it can
// actually do.
//
// A claim is opened when goods arrive damaged or short, and it is settled
// either with money or with a replacement. Sending a replacement means shipping
// goods out against an order that already exists, and there is no capability
// for that anywhere in this repository. The refusal is therefore the honest
// answer, and the alternative is the dangerous one: stamping the claim complete
// would close the case in the shop's records while the customer is still
// holding a broken product and nothing was ever dispatched.
//
// The claim's KIND lives in a database column and the decision is taken by
// reading it back, so a unit test with a fake claim cannot show that the value
// written by one endpoint is the value the other one reads.
func TestAClaimSettledWithAReplacementIsRefused(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Claimed Product", map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, happyInitialStock)

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

	created, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+placed.OrderID+"/claims",
		map[string]any{"type": "replace", "reason": "arrived broken"})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, created.Code,
		"the claim could not be opened; body: %s", created.Body.String())

	var claim afterSalesRecordResponse
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &claim),
		"the claim record could not be decoded; body: %s", created.Body.String())
	require.NotEmpty(t, claim.Data.ID, "the opened claim must carry an identity")

	settled, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+placed.OrderID+"/claims/"+claim.Data.ID+"/settle",
		map[string]any{"amount": returnRefundAmount})
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, settled.Code,
		"settling a REPLACEMENT claim has to be refused. A 200 would close the case in "+
			"the shop's records while nothing was shipped, and the customer would be "+
			"left holding a broken product against a claim marked done. body: %s",
		settled.Body.String())

	collection, err := paymentSvc.GetPaymentCollection(ctx, placed.PaymentCollectionID)
	require.NoError(t, err, "the payment collection must be readable from the payment module")
	assert.Equal(t, int64(0), collection.RefundedAmount,
		"the refused settlement must not have sent money either; a refusal that pays out "+
			"first is not a refusal")

	// The claim itself has to be untouched, because a refusal that half-applies
	// is worse than none: an operator would come back to a record that says it
	// was settled and a customer who never got anything.
	after, err := adminRequestWithBody(http.MethodGet,
		"/admin/v1/orders/"+placed.OrderID+"/claims/"+claim.Data.ID, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, after.Code, after.Body.String())

	var stored afterSalesRecordResponse
	require.NoError(t, json.Unmarshal(after.Body.Bytes(), &stored),
		"the stored claim could not be decoded; body: %s", after.Body.String())
	assert.Equal(t, "requested", stored.Data.Status,
		"the refused claim has to still be open; anything else would record a settlement "+
			"that never reached the customer")
}
