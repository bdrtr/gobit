//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// This file walks the money of an EXCHANGE from end to end: the customer owes a
// difference, an operator collects it, the order records which collection
// answered, and the operator sends it back again.
//
// # Why it has to be here
//
// ADR 0120 gave the exchange a funded state, four database CHECKs and two
// routes, and nothing had ever walked the chain. The flow's unit tests give it
// a fake payment module that answers with whatever the test set, so the figures
// it compares are the test's own; the order module's tests see the row and not
// the money. What only this file can show is that the two really do line up on
// a real database: the collection the operator opened, the amount the exchange
// owes, and the CHECK that refuses the pair when they disagree.
//
// # And the part that must NOT happen
//
// An exchange's difference collection belongs to no order (ADR 0117: the
// order_payment link is one-to-one and carries the sale). Money moving on it
// publishes `payment.captured` all the same, and the order module now listens
// (ADR 0121). So this file is also the proof that the subscriber stays SILENT
// for it: the order's paid total must not gain the difference. If it did, the
// shop would report a sale larger than it made, and the B2B spending window
// would consume the customer's credit for money that answered a different act.

// The exchange scenario's figures, computed by hand.
const (
	// exchangeDifference is what the customer owes for the swap. It is distinct
	// from every other figure in the suite so a number that leaks from
	// somewhere else gives itself away.
	exchangeDifference int64 = 7_800
	// exchangeOverOpened is a collection opened for MORE than the exchange
	// owes, and then captured down to exactly what it owes.
	//
	// The pair is chosen to isolate one rule from another. The flow makes two
	// separate comparisons — the collection's AMOUNT against the difference,
	// and what it HOLDS against the difference — and a collection that is
	// simply wrong all the way through is refused by the second, so it proves
	// nothing about the first. Held here equals the difference exactly, so only
	// the amount rule can refuse it. Measured: with the two figures equal, the
	// mutation that deletes the amount rule leaves the test green.
	exchangeOverOpened int64 = exchangeDifference + 1
)

// exchangeResponse is the exchange record on the wire.
type exchangeResponse struct {
	Data struct {
		ID                  string  `json:"id"`
		Status              string  `json:"status"`
		DifferenceDue       int64   `json:"difference_due"`
		PaymentCollectionID string  `json:"payment_collection_id"`
		FundedAt            *string `json:"funded_at"`
	} `json:"data"`
}

// collectDifference opens a collection for openFor, takes `take` of it and
// returns the collection's id.
//
// The two amounts are separate on purpose. A collection's amount caps every
// capture on it but does not have to equal what was taken, and the flow judges
// BOTH figures against the difference; a fixture that could only produce
// collections where the two agree would exercise one of the two rules and hide
// the other.
//
// This is the half an operator does through the PAYMENT module's own published
// endpoints before naming the collection on the order; it is fixture here, and
// the subject of the test is what the ORDER does with it afterwards.
func collectDifference(t *testing.T, openFor, take int64) string {
	t.Helper()

	ctx := t.Context()
	collection, err := paymentSvc.CreatePaymentCollection(ctx, paymentsvc.CreateCollectionInput{
		Reference:    "exchange difference",
		Amount:       openFor,
		CurrencyCode: taxedCurrency,
	})
	require.NoError(t, err, "the difference collection could not be opened")

	session, err := paymentSvc.CreateSession(ctx, collection.ID, paymentmanual.ID,
		paymentsvc.CreateSessionInput{
			Amount:         take,
			IdempotencyKey: collection.ID,
			Data:           map[string]any{paymentmanual.DataKeyOutcome: paymentmanual.OutcomeAuthorize},
		})
	require.NoError(t, err, "the difference session could not be opened")

	_, err = paymentSvc.AuthorizePayment(ctx, session.ID)
	require.NoError(t, err, "the difference could not be authorized")

	_, err = paymentSvc.CapturePayment(ctx, session.ID, 0)
	require.NoError(t, err, "the difference could not be captured")

	return collection.ID
}

// TestAnExchangeCollectsItsDifferenceAndCanSendItBack walks ADR 0120's chain
// over HTTP and proves the order's own money is untouched by it.
//
// If it stops holding, the shop is holding the customer's money against a swap
// its records do not connect to any collection — and no operator can tell
// whether it was taken, because the exchange row is the only place the answer
// lives.
func TestAnExchangeCollectsItsDifferenceAndCanSendItBack(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Exchange Product", map[string]int64{
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

	opened, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+placed.OrderID+"/exchanges",
		map[string]any{"difference_due": exchangeDifference, "note": "a larger size, and it costs more"})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, opened.Code,
		"the exchange could not be opened; body: %s", opened.Body.String())

	var exchange exchangeResponse
	require.NoError(t, json.Unmarshal(opened.Body.Bytes(), &exchange))
	require.Equal(t, "requested", exchange.Data.Status)
	exchangeID := exchange.Data.ID
	fundingPath := "/admin/v1/orders/" + placed.OrderID + "/exchanges/" + exchangeID + "/funding"

	// A collection opened for MORE than the exchange owes is refused even when
	// it holds exactly the right money. That is the whole value of naming a
	// collection rather than a number: the amount caps every capture on it, so
	// one opened wide could later take more than the customer agreed to, and
	// nothing on the order would object.
	wrong, err := adminRequestWithBody(http.MethodPost, fundingPath,
		map[string]any{
			"payment_collection_id": collectDifference(t, exchangeOverOpened, exchangeDifference),
		})
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, wrong.Code,
		"a collection opened for the wrong amount must be REFUSED even though it holds the "+
			"right figure; body: %s", wrong.Body.String())

	// And a collection that took the right money and gave ALL of it back is
	// refused too, though its amount is exactly right and its CAPTURE is
	// exactly right. It holds nothing, and a rule reading the capture alone
	// would call it funded — which is the sentence ADR 0120 wrote the rule as
	// an equality on `captured - refunded` for. Nothing else in this test can
	// tell the two readings apart: measured, both the held rule and the
	// subtraction survive every other case here untouched.
	emptied := collectDifference(t, exchangeDifference, exchangeDifference)
	_, err = paymentSvc.RefundCollection(t.Context(), emptied, exchangeDifference, "sent back again")
	require.NoError(t, err, "the difference could not be sent back")

	drained, err := adminRequestWithBody(http.MethodPost, fundingPath,
		map[string]any{"payment_collection_id": emptied})
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, drained.Code,
		"a collection that holds NOTHING must be refused; its capture is right and its "+
			"amount is right, and the money is gone; body: %s", drained.Body.String())

	collectionID := collectDifference(t, exchangeDifference, exchangeDifference)
	funded, err := adminRequestWithBody(http.MethodPost, fundingPath,
		map[string]any{"payment_collection_id": collectionID})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, funded.Code,
		"the funding must succeed; body: %s", funded.Body.String())

	var afterFunding exchangeResponse
	require.NoError(t, json.Unmarshal(funded.Body.Bytes(), &afterFunding))
	assert.Equal(t, "funded", afterFunding.Data.Status)
	assert.Equal(t, collectionID, afterFunding.Data.PaymentCollectionID,
		"the row has to name the collection; it is the only record of WHERE the money went")
	assert.NotNil(t, afterFunding.Data.FundedAt,
		"the moment has to be stamped: a database CHECK ties the stamp and the collection "+
			"together, so a missing one means the row is half-written")

	// The exchange's money is NOT the order's money. The capture published an
	// event, the order module is subscribed to it, and the collection is bound
	// to no order — so the subscriber must have found nothing and said nothing.
	untouched, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err)
	assert.Equal(t, happyTotal, untouched.Summary.PaidTotal,
		"the ORDER's collected total must not gain the exchange's difference (%d): the "+
			"difference answers a different act, and a shop reporting a larger sale than "+
			"it made consumes the customer's B2B credit for money that was never the sale's",
		exchangeDifference)
	assert.Zero(t, untouched.Summary.RefundedTotal)

	// The exit. What goes back is everything the collection still holds, and no
	// amount is sent: a partial refund would leave the exchange holding money
	// while its record said it was withdrawn.
	sentBack, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/orders/"+placed.OrderID+"/exchanges/"+exchangeID+"/refund",
		map[string]any{"reason": "the goods turned out to be unsendable"})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, sentBack.Code,
		"the refund must succeed; body: %s", sentBack.Body.String())

	var withdrawn exchangeResponse
	require.NoError(t, json.Unmarshal(sentBack.Body.Bytes(), &withdrawn))
	assert.Equal(t, "canceled", withdrawn.Data.Status,
		"a funded exchange leaves through this route and nowhere else; the cancel route "+
			"refuses it while the customer's money is on it")

	collection, err := paymentSvc.GetPaymentCollection(ctx, collectionID)
	require.NoError(t, err)
	assert.Equal(t, exchangeDifference, collection.RefundedAmount,
		"the money has to have really gone back in the PAYMENT module; a row that says "+
			"withdrawn over a collection that still holds the money is the one state "+
			"nobody can see from either side")
	assert.Zero(t, collection.CapturedAmount-collection.RefundedAmount,
		"nothing may be left held: what remains is what a repeat of the exit would send "+
			"back a second time")

	final, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err)
	assert.Equal(t, happyTotal, final.Summary.PaidTotal,
		"the refund of a difference must not move the order's figures either")
	assert.Zero(t, final.Summary.RefundedTotal,
		"the order was never refunded; the exchange's difference went back on its OWN "+
			"collection and the two must not be added together")
}
