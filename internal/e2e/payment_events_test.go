//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// This file proves that money moved by the PAYMENT module's own routes reaches
// the order that the money belongs to.
//
// # What only this file can show
//
// The chain has five links and no unit test holds more than one of them: the
// payment module writes an outbox row and publishes, the bus delivers, the
// order module's subscriber runs, it reaches the order BACKWARDS over the
// order_payment link through the query layer, and it merges the collection's
// cumulative totals onto the summary.
//
// The backward step is the one that has already broken once. core/query's own
// regression test records it: while the link and query packages were being
// written, the unit tests against a FAKE link service passed and every reverse
// expansion failed against the real one. This test runs on the real link
// service, the real link rows written by the checkout, and the real payment
// provider — and the payment provider had to gain a filter on the collection's
// own id for the expansion to be answerable at all.
//
// # Why the refund goes through the PAYMENT route and not the returns flow
//
// The returns flow writes the summary itself, so it proves nothing about the
// subscriber. Gap D55 is precisely the route that has no flow on its path: an
// operator refunds a capture directly, and until ADR 0121 the order's record
// never learned. Using the same route the gap names is what makes this test a
// reproduction of the gap rather than a test of something nearby.

// directRefundAmount is what the operator sends back through the payment route.
//
// It is deliberately NOT the order total and NOT the returns scenario's figure:
// a summary that ends up holding either of those is holding a number that came
// from somewhere else.
const directRefundAmount int64 = 12_345

// paymentRefundResponse is what POST /admin/v1/payments/{id}/refunds answers.
type paymentRefundResponse struct {
	Data struct {
		ID     string `json:"id"`
		Amount int64  `json:"amount"`
	} `json:"data"`
}

// TestARefundThroughThePaymentRouteReachesTheOrdersSummary refunds a capture
// with the payment module's own admin route and waits for the order to learn.
//
// If it stops holding, the shop reports as unrefunded a sale it has partly paid
// back. That figure is what the B2B spending window reads, so the customer's
// credit stays consumed by money they already have; and it is what an operator
// reads on the order, so the two halves of the story disagree with nobody
// noticing.
func TestARefundThroughThePaymentRouteReachesTheOrdersSummary(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Direct Refund Product", map[string]int64{
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

	before, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err, "the fixture order could not be read back")
	require.Equal(t, happyTotal, before.Summary.PaidTotal,
		"precondition: the checkout must have recorded the collection")
	require.Zero(t, before.Summary.RefundedTotal,
		"precondition: nothing has been sent back yet")

	// A NEWER collection that belongs to no order at all — an exchange's
	// difference collection is exactly this shape (ADR 0120), and so is a cart
	// that never became an order.
	//
	// It is here to make the test discriminating rather than to be asserted on.
	// The collection listing orders by `created_at DESC, id DESC`, so a reader
	// that failed to filter on the collection's own identifier would be handed
	// THIS row, find no order behind it, and stay silent — and the assertion
	// below would then fail rather than pass by accident. Without it the whole
	// backward path can be broken and this test still goes green, which was
	// measured rather than assumed.
	unbound, err := paymentSvc.CreatePaymentCollection(ctx, paymentsvc.CreateCollectionInput{
		Reference:    "cart_that_never_became_an_order",
		Amount:       directRefundAmount,
		CurrencyCode: taxedCurrency,
	})
	require.NoError(t, err, "the unbound collection could not be created")
	require.NotEqual(t, placed.PaymentCollectionID, unbound.ID)

	// The capture the operator refunds. There is exactly one, because the
	// checkout takes the whole order in a single capture.
	payments, err := paymentSvc.ListPayments(ctx, placed.PaymentCollectionID)
	require.NoError(t, err, "the collection's captures could not be listed")
	require.Len(t, payments, 1, "precondition: the checkout captures once")

	// No flow is on this path. This is the whole point of the test: the only
	// thing that can carry the news to the order is the event.
	answer, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/payments/"+payments[0].ID+"/refunds",
		map[string]any{"amount": directRefundAmount, "reason": "an operator sent part of it back"})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, answer.Code,
		"the refund must succeed; body: %s", answer.Body.String())

	var refund paymentRefundResponse
	require.NoError(t, json.Unmarshal(answer.Body.Bytes(), &refund),
		"the refund answer could not be decoded; body: %s", answer.Body.String())
	require.Equal(t, directRefundAmount, refund.Data.Amount,
		"precondition: the payment module refunded the amount that was asked for")

	// Delivery is asynchronous, so the order learns a moment later rather than
	// in the request. Waiting is the honest shape here; asserting immediately
	// would make the test flaky in the direction that hides a real failure.
	var after int64
	require.Eventually(t, func() bool {
		order, readErr := orderSvc.GetOrder(ctx, placed.OrderID)
		if readErr != nil {
			return false
		}
		after = order.Summary.RefundedTotal
		return after == directRefundAmount
	}, olayBeklemeSuresi, 20*time.Millisecond,
		"the ORDER's summary has to carry a refund made through the PAYMENT route "+
			"(expected %d, last read %d); this is gap D55 and the subscriber of ADR 0121 "+
			"is the only thing that closes it",
		directRefundAmount, after)

	final, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err)
	assert.Equal(t, happyTotal, final.Summary.PaidTotal,
		"the collected figure must NOT move: the subscriber reports the collection's "+
			"CUMULATIVE totals, and a capture total that fell would mean it reported an "+
			"amount from the refund instead")

	stillUnbound, err := paymentSvc.GetPaymentCollection(ctx, unbound.ID)
	require.NoError(t, err, "the unbound collection must still be readable")
	assert.Zero(t, stillUnbound.RefundedAmount,
		"the collection nobody is bound to must not have been touched; money moving on it "+
			"would mean the refund was routed by position rather than by identifier")

	collection, err := paymentSvc.GetPaymentCollection(ctx, placed.PaymentCollectionID)
	require.NoError(t, err)
	assert.Equal(t, collection.RefundedAmount, final.Summary.RefundedTotal,
		"the summary is a REPORT of what the payment module holds (ADR 0119); the two "+
			"figures disagreeing means the order kept a copy instead of asking")
}
