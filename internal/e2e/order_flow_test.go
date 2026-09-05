//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	inventorymodels "github.com/bdrtr/gobit/internal/modules/inventory/models"
	ordermodels "github.com/bdrtr/gobit/internal/modules/order/models"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	paymentmodels "github.com/bdrtr/gobit/internal/modules/payment/models"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// This file proves the plan's Phase 6 DoD with the REAL modules.
//
// The DoD in one sentence: "The end-to-end cart -> order workflow works with the
// test provider; when the payment step fails the STOCK RESERVATION AND THE ORDER
// ARE ROLLED BACK (saga test); the order.placed event is published."
//
// # Why unit tests are not enough
//
// The checkout package's unit tests probe the saga's decisions through FAKE
// surfaces: "was the compensation called", "in which order". Those are the right
// questions, but on their own they do not prove Phase 6, because the DoD's claim
// is not that the calls are made but their OUTCOME — that the order really ends
// up "canceled", that the reserved stock really becomes sellable again, that the
// money really has been captured. All of that lives in the modules' database
// transactions and state machines; it can only be seen with the real modules.
//
// # Why the expected totals are written out by hand
//
// The same rationale as in the Phase 5 tests applies (see the package comment):
// every scenario's subtotal, tax and grand total are CONSTANTS computed on paper
// INSIDE the test. Repeating the production formula in the test would mean making
// the same mistake in two places at once.
//
// # The payment provider
//
// The provider is the manual/test provider (internal/modules/payment/manual) and
// its behaviour is steered by session data: the [manual.DataKeyOutcome] key says
// whether the authorization will be accepted or declined. The data is given to the
// workflow through the [checkoutwf.CompleteCartInput.PaymentData] field and is
// forwarded to the provider AS IS; that is, there is no test hook in the saga
// itself.

// The happy path scenario's HAND-computed amounts.
//
// The region is taxed at 20% (2000 basis points) and no shipping method is
// selected:
//
//	45_000 × 2 = 90_000 subtotal
//	90_000 × 20% = 18_000 tax
//	90_000 - 0 + 18_000 + 0 = 108_000 grand total
const (
	happyUnitPrice    int64 = 45_000
	happyQuantity     int64 = 2
	happySubtotal     int64 = 90_000
	happyTax          int64 = 18_000
	happyTotal        int64 = 108_000
	happyInitialStock int64 = 10
	// happyRemainingStock is the expected physical quantity after capture: 10 - 2.
	happyRemainingStock int64 = 8
)

// TestCartToOrderHappyPath runs the first half of the Phase 6 DoD end to end: the
// cart turns into an order, the money is captured, stock drops, the cart closes.
//
// The chain: product + variant + price + inventory item + stock level -> cart ->
// line -> compute -> complete_cart -> order + capture + confirmed reservation +
// closed cart. If one of these links breaks, the failure shows up here.
func TestCartToOrderHappyPath(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	const variantTitle = "E2E Happy Path Product"
	variantID, inventoryItemID := newStockedVariant(ctx, t, variantTitle, map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, happyInitialStock)

	cartID, totals := prepareCart(ctx, t, customerID, variantID, happyQuantity)
	assertTotals(t, totals, expectedTotal{
		subtotal: happySubtotal,
		discount: 0,
		tax:      happyTax,
		shipping: 0,
		total:    happyTotal,
	}, "after the happy path cart was prepared")

	require.Equal(t, happyInitialStock, sellableQuantity(ctx, t, inventoryItemID),
		"adding a line to the cart must NOT reserve stock; the reservation is made at "+
			"order time, otherwise every abandoned cart would lock stock")

	// --- the workflow itself ---

	result, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: manual.ID,
		PaymentData:       paymentBehavior(t, manual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     happyTotal,
	})
	require.NoError(t, err, "a prepared cart must be convertible into an order")

	require.Equal(t, cartID, result.CartID, "the result must report the cart the order was born from")
	require.NotEmpty(t, result.OrderID, "the result must carry the ID of the order created")
	require.Equal(t, happyTotal, result.Amount,
		"the amount the workflow captured must be the HAND-computed grand total; since the "+
			"ExpectedTotal check has already passed, a divergence here shows the result carries "+
			"the wrong field")
	require.Equal(t, taxedCurrency, result.CurrencyCode,
		"the capture must be made in the cart's currency")
	require.True(t, result.CartCompleted,
		"the cart must be stamped completed; if it were not stamped, the same cart could be "+
			"the source of a second order")
	require.True(t, result.ReservationsConfirmed,
		"the reservations must be confirmed; an unconfirmed reservation leaves the stock "+
			"'reserved' and the goods sold never drop out of physical stock at all")
	require.Empty(t, result.Warnings,
		"on the happy path there must be NO warning; a warning reports that a module blew up "+
			"after the pivot and that manual repair is needed")

	// --- 1) was the order created, are its totals the SAME as the cart's ---

	order, err := orderSvc.GetOrder(ctx, result.OrderID)
	require.NoError(t, err, "the order created must be readable from the order module")
	require.Equal(t, ordermodels.OrderPending, order.Status,
		"the order must come out of this workflow as 'pending'; the 'completed' stamp is the "+
			"outcome of delivery (Phase 7) and a completed order can no longer BE CANCELED — "+
			"the saga would have made its own compensation impossible")
	require.Equal(t, cartID, order.CartID,
		"the order must document the cart it was born from; if the origin is lost, "+
			"reconciliation cannot be done by hand")
	require.Equal(t, customerID, order.CustomerID,
		"the order must be written to the cart's customer")
	require.Equal(t, email, order.Email,
		"the order's contact e-mail must be the value given to the workflow; the cart's e-mail "+
			"is not published on the cross-module surface, which is why it is asked for at the "+
			"payment step and given to the workflow")
	require.Equal(t, taxedRegionID, order.RegionID,
		"the order must be written to the cart's region; the region is the context of the tax "+
			"rate and of the currency")
	require.Equal(t, taxedCurrency, order.CurrencyCode,
		"the order must be opened in the cart's currency")

	require.Equal(t, happySubtotal, order.Subtotal,
		"the order's subtotal must be the SAME as the cart's; a divergence means the amount "+
			"shown to the customer differs from the amount invoiced")
	require.Equal(t, int64(0), order.DiscountTotal,
		"with no source producing a discount, the order must not carry a discount")
	require.Equal(t, happyTax, order.TaxTotal,
		"the order's tax must be the SAME as the cart's")
	require.Equal(t, int64(0), order.ShippingTotal,
		"with no shipping method selected, the order must not carry a shipping amount")
	require.Equal(t, happyTotal, order.Total,
		"the order's grand total must be the same as the cart's and as the amount CAPTURED")
	require.True(t, order.TotalsConsistent(),
		"the order must satisfy the totals identity: total = subtotal - discount + tax + shipping")

	require.Len(t, order.Items, 1,
		"the cart's single line must pass into the order as a single line")
	line := order.Items[0]
	require.Equal(t, variantID, line.VariantID, "the order line must point at the variant in the cart")
	require.Equal(t, variantTitle, line.Title,
		"the line title must be copied FROM THE CATALOG; even if the catalog changes later, the "+
			"name on the invoice must not change")
	require.Equal(t, happyQuantity, line.Quantity, "the order line's quantity must be the same as the cart's")
	require.Equal(t, happyUnitPrice, line.UnitPrice,
		"the unit price must be the value taken from pricing during the compute round")
	require.Equal(t, happySubtotal, line.Subtotal, "the line subtotal must be unit price × quantity")
	require.Equal(t, happyTax, line.TaxTotal, "the line tax must be the same as the cart line's")
	require.Equal(t, happyTotal, line.Total, "the line total must be the same as the cart line's")

	// The summary IS written, and it carries what the payment collection actually
	// holds (ADR 0022). Until that landed this assertion read the opposite way,
	// on the argument that a "paid total" kept in two places at once could
	// diverge and that reconciling the two was Phase 7+ work.
	//
	// That argument was answered rather than dropped, and the answer is in three
	// parts. The duplication was already decided: order_summaries exists as a
	// table with these columns, SetOrderSummaryTotals was written to fill them,
	// and the B2B spending window already READS refunded_total from it. What was
	// missing was never the decision, it was the writer. The summary is a
	// DERIVED report and the collection stays the source of truth, which is why
	// the number written here is the collection's own and not the plan's. And
	// divergence is not a risk created by having two figures — it is a condition
	// that becomes DETECTABLE because there are two, which is the same argument
	// ADR 0020 makes about a session and its provider.
	//
	// The phase the old comment deferred to no longer exists.
	require.Equal(t, happyTotal, order.Summary.PaidTotal,
		"the order has to record what was collected on it; zero here means an operator "+
			"cannot tell a paid order from an unpaid one")
	require.Zero(t, order.Summary.RefundedTotal,
		"nothing was refunded in this flow")

	// The order's customer and region are in THEIR OWN columns; that is the only place
	// they are carried from cart to order, and filtering is done from exactly those
	// columns.
	require.Equal(t, customerID, order.CustomerID,
		"the order must be written to the customer who owns the cart")
	require.Equal(t, taxedRegionID, order.RegionID,
		"the order must be opened with the cart's region")

	// --- 2) was the stock reservation CONFIRMED ---

	require.Len(t, result.ReservationIDs, 1,
		"one reservation must be taken for each cart line")
	reservation, err := inventorySvc.GetReservation(ctx, result.ReservationIDs[0])
	require.NoError(t, err, "the reservation must be readable from the inventory module")
	require.Equal(t, inventorymodels.ReservationConfirmed, reservation.Status,
		"the reservation must be 'confirmed': it means the goods sold HAVE BEEN DEDUCTED from "+
			"physical stock. Had it stayed 'active', the stock would look reserved forever and "+
			"would behave as if it had never been shipped")
	require.Equal(t, happyQuantity, reservation.Quantity,
		"the reservation must be for the cart line's quantity")

	require.Equal(t, happyRemainingStock, sellableQuantity(ctx, t, inventoryItemID),
		"the sellable quantity must DROP by the amount ordered (%d - %d); if it does not drop, "+
			"the same goods can be sold a second time", happyInitialStock, happyQuantity)
	level := stockLevel(ctx, t, inventoryItemID)
	require.Equal(t, happyRemainingStock, level.StockedQuantity,
		"the PHYSICAL quantity must drop too: confirmation deducts the reserved quantity from "+
			"stock. Only the sellable quantity dropping would mean the reservation is still 'active'")
	require.Equal(t, int64(0), level.ReservedQuantity,
		"after confirmation the reserved quantity must be ZEROED; leaving it would mean the same "+
			"quantity counted as both deducted and promised")

	// --- 3) is the cart completed (is a second write a Conflict) ---

	cart, err := cartSvc.GetCart(ctx, cartID)
	require.NoError(t, err, "the cart must be readable from the cart module")
	require.True(t, cart.Completed(),
		"the cart must be stamped completed")

	_, err = workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    cartID,
		VariantID: variantID,
		Quantity:  1,
	})
	require.Error(t, err,
		"a line must NOT BE ADDABLE to a completed cart; if it could be added, the shape of an "+
			"already-ordered cart would change afterwards and the order's origin would be a lie")
	require.True(t, errors.IsConflict(err),
		"writing to a completed cart must be an errors.Conflict (code: %s); the class maps to 409 "+
			"over HTTP and the client tells 'retry' apart from 'this cart is closed'. "+
			"Returned error: %v", cartwf.CodeCartCompleted, err)

	// --- 4) was the payment captured ---

	require.NotEmpty(t, result.PaymentID, "the result must carry the ID of the capture")
	collection, err := paymentSvc.GetPaymentCollection(ctx, result.PaymentCollectionID)
	require.NoError(t, err, "the payment collection must be readable from the payment module")
	require.Equal(t, cartID, collection.Reference,
		"the collection must reference the cart; the reference is the only trace of which piece "+
			"of work gave birth to the payment")
	require.Equal(t, happyTotal, collection.Amount,
		"the amount the collection has to collect must be the order's total")
	require.GreaterOrEqual(t, collection.CapturedAmount, collection.Amount,
		"the amount CAPTURED must COVER the amount to be collected (captured >= amount). "+
			"The rule looks at the NUMBER, not at the status string: on a partial capture the "+
			"status can still look like 'there is a payment' and a check that only looks at the "+
			"status would approve an unpaid order")
	require.Equal(t, paymentmodels.CollectionCaptured, collection.Status,
		"the collection's status must be 'captured'; the status is DERIVED from the amounts, so "+
			"this assertion also probes that the derivation works correctly")
	require.Equal(t, int64(0), collection.AuthorizedAmount,
		"after capture no held amount must REMAIN; if it did, the same money would count as both "+
			"held on the customer's card and captured by the store")
	require.Equal(t, int64(0), collection.RefundedAmount,
		"on the happy path there must be no refund")

	// --- 5) was order.placed published ---

	event := eventLog.waitFor(t, result.OrderID)
	require.Equal(t, result.OrderID, olayAlani(t, event, ordersvc.EventFieldOrderID),
		"the event's payload must carry the order's ID")
	require.Equal(t, "108000", olayAlani(t, event, ordersvc.EventFieldTotal),
		"the event's payload must carry the order's total as a STRING without decimals")
}

// The saga scenario's HAND-computed amounts.
//
// The price is chosen so that the tax rounding DOWN is also made visible:
//
//	33_333 × 3 = 99_999 subtotal
//	99_999 × 20% = 19_999.8 -> rounded DOWN -> 19_999 tax
//	99_999 - 0 + 19_999 + 0 = 119_998 grand total
const (
	sagaUnitPrice    int64 = 33_333
	sagaQuantity     int64 = 3
	sagaSubtotal     int64 = 99_999
	sagaTax          int64 = 19_999
	sagaTotal        int64 = 119_998
	sagaInitialStock int64 = 7
)

// TestSagaRollsBackWhenPaymentFails is the CORE of the Phase 6 DoD: it proves that
// when the payment step blows up the stock reservation and the order ARE ROLLED
// BACK.
//
// The setup picks the saga's most expensive failure point: the payment is declined
// once the reservation has been taken and the order has been opened. That is, the
// compensation chain is not EMPTY; two steps have to be undone in reverse order.
// The provider declines with [manual.OutcomeDecline] — this is not the provider
// being UNREACHABLE but a DECLINE response it gives deliberately, and it is the
// most frequently seen payment failure in real life.
//
// Every assertion is written out separately and states WHY it matters: this test
// failing does not mean "a call is missing", it means "the customer's goods were
// locked without their money being taken, or they have an order that does not
// exist".
func TestSagaRollsBackWhenPaymentFails(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, inventoryItemID := newStockedVariant(ctx, t, "E2E Saga Product", map[string]int64{
		taxedCurrency: sagaUnitPrice,
	}, sagaInitialStock)

	cartID, totals := prepareCart(ctx, t, customerID, variantID, sagaQuantity)
	assertTotals(t, totals, expectedTotal{
		subtotal: sagaSubtotal,
		discount: 0,
		tax:      sagaTax,
		shipping: 0,
		total:    sagaTotal,
	}, "after the saga cart was prepared")

	// The initial state is measured: the claim "it was rolled back" is only
	// meaningful if there is a BEFORE. Reading instead of writing the constant again
	// also verifies that the fixture really did set up the expected stock.
	sellableBefore := sellableQuantity(ctx, t, inventoryItemID)
	require.Equal(t, sagaInitialStock, sellableBefore,
		"the fixture must have set up the stock with the expected quantity")
	levelBefore := stockLevel(ctx, t, inventoryItemID)

	// --- the workflow MUST BLOW UP ---

	result, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: manual.ID,
		PaymentData:       paymentBehavior(t, manual.OutcomeDecline),
		Email:             email,
		ExpectedTotal:     sagaTotal,
	})
	require.Error(t, err,
		"with the payment declined the workflow must not return SUCCESS; if it did, an unpaid "+
			"order would enter shipping")
	require.Equal(t, checkoutwf.CompleteCartResult{}, result,
		"a workflow that returns an error must NOT LEAK a half result; even if the caller ignores "+
			"the error, it must not be left holding a usable order ID")

	require.True(t, errors.IsConflict(err),
		"the decline must be an errors.Conflict: it is not a server fault but a clash with the "+
			"state of the world, and the client can change the card and try AGAIN. Had it been "+
			"escalated to Internal, it could not be told apart from the case where the "+
			"compensation failed (which needs manual intervention). Returned error: %v", err)
	require.ErrorContains(t, err, paymentsvc.CodeAuthorizationDeclined,
		"the error chain must carry the DECLINE reason; if it does not, the operator cannot read "+
			"from the log whether the fault was in the payment or in the stock")
	require.ErrorContains(t, err, checkoutwf.StepAuthorizePayment,
		"the error chain must carry the name of the STEP THAT BLEW UP; that name is written to "+
			"the execution record too")

	// --- 1) WAS THE ORDER CANCELED ---
	//
	// The order had been opened BEFORE the payment step: create_order is the saga's
	// second step. Had the compensation not run, a "pending" order would be left
	// behind — that is, an order whose payment was never taken would look like the
	// next job in the shipping queue. Since the ID is not returned by the workflow,
	// the order is read from the customer's records; that is what a real operator
	// would do too.
	listed, err := orderSvc.ListOrders(ctx, ordersvc.ListOrdersInput{CustomerID: &customerID})
	require.NoError(t, err, "the customer's orders must be readable")

	orders := listed.Items
	require.Len(t, orders, 1,
		"the compensation does NOT DELETE the order, it CANCELS it: the record must remain so "+
			"that the trace of the attempt is not lost. The record not being there at all would "+
			"mean the order was never written (that is, the test is probing the wrong step)")
	canceled := orders[0]
	require.Equal(t, ordermodels.OrderCanceled, canceled.Status,
		"the order must be 'canceled'. Had it stayed 'pending': (1) the shipping team would "+
			"prepare an order whose payment was never taken, (2) the customer would see an order "+
			"in their account that does not exist, (3) an amount that is not revenue would be "+
			"counted in the reports")
	require.NotNil(t, canceled.CanceledAt,
		"the cancellation stamp must be set; the stamp is the only record of WHEN the "+
			"cancellation happened")
	require.NotEmpty(t, canceled.CancelReason,
		"the cancellation REASON must be written; a cancellation without a reason is a record "+
			"that cannot be answered for when the customer asks")
	require.Equal(t, sagaTotal, canceled.Total,
		"the cancellation must not change the order's AMOUNT; the order is the permanent answer "+
			"to the question 'what was meant to be sold at that moment' and the cancellation only "+
			"changes its status")

	// --- 2) WAS THE STOCK RESERVATION RELEASED ---
	//
	// The reservation was the saga's FIRST step, so it is the LAST of the compensation
	// chain. Had it not been released, unsold goods would stay reserved and the next
	// customer would see "out of stock" — and since no order consumed that stock, the
	// mistake would only be noticed at stocktaking.
	require.Equal(t, sellableBefore, sellableQuantity(ctx, t, inventoryItemID),
		"the sellable quantity must return to its OLD value (%d). If it does not, the reserved "+
			"stock stays dangling and unsold goods become unsellable", sellableBefore)
	levelAfter := stockLevel(ctx, t, inventoryItemID)
	require.Equal(t, levelBefore.StockedQuantity, levelAfter.StockedQuantity,
		"the PHYSICAL quantity must not change at all: releasing erases an unconfirmed promise; "+
			"deducting from stock only happens on confirmation")
	require.Equal(t, int64(0), levelAfter.ReservedQuantity,
		"the reserved quantity must return to ZERO; staying different from zero means the promise "+
			"is still standing")

	levels, err := inventorySvc.ListInventoryLevels(ctx, inventoryItemID)
	require.NoError(t, err, "the stock levels must be readable")
	require.Len(t, levels, 1, "the fixture must be levelled at a single location")

	// --- 3) THE CART MUST NOT BE COMPLETED (it is still modifiable) ---
	//
	// clear_cart is the saga's last step and, because the payment step blew up, it did
	// NOT run at all. The cart staying open is not a detail but the purpose of the
	// workflow: the customer must be able to change their card and try again with the
	// same cart.
	cart, err := cartSvc.GetCart(ctx, cartID)
	require.NoError(t, err, "the cart must be readable from the cart module")
	require.False(t, cart.Completed(),
		"the cart must NOT be stamped completed; had it been stamped, the customer would have "+
			"both failed to pay and lost their cart")
	require.Len(t, cart.Items, 1, "the cart's line must still be in place")
	require.Equal(t, sagaQuantity, cart.Items[0].Quantity, "the cart line's quantity must not change")

	updated, err := workflows.UpdateLineItem(ctx, cartwf.UpdateLineItemInput{
		CartID:     cartID,
		LineItemID: cart.Items[0].ID,
		Quantity:   1,
	})
	require.NoError(t, err,
		"the cart must STILL BE MODIFIABLE; a cart that is readable but not writable is a state "+
			"in which telling the customer to 'try again' is impossible")
	require.Equal(t, int64(1), updated.Quantity, "the update must really be applied")

	// --- 4) order.placed: the OBSERVED behaviour ---
	//
	// The event HAS BEEN PUBLISHED and that is correct: the order REALLY did come into
	// being for a moment, and an event is the announcement of a fact that happened.
	// Because the payment was declined afterwards the order was canceled, but "it
	// happened and then was canceled" and "it never happened" are different things,
	// and the event tells the first.
	//
	// This has a CONSEQUENCE for consumers, and the test exists to document it:
	// "order.placed" on its own does NOT mean "this order is valid". Subscribers
	// (invoicing, notification, accounting) are obliged to read the order's current
	// status. That the payload contains a status field is meaningful for exactly this
	// reason.
	//
	// A separate event announcing the cancellation (e.g. "order.canceled") does NOT
	// exist today; the plan's Phase 6 only asks for order.placed. The day it is added,
	// this block must grow to assert that both events reach the subscriber.
	event := eventLog.waitFor(t, canceled.ID)
	require.Equal(t, ordermodels.OrderPending.String(), olayAlani(t, event, ordersvc.EventFieldStatus),
		"the event carries the order's status AT THE MOMENT OF PUBLICATION and at that moment it "+
			"was 'pending'; a cancellation after the event was published does NOT retroactively "+
			"CHANGE the payload. The subscriber is obliged to read the current status from the order")
	require.Equal(t, "119998", olayAlani(t, event, ordersvc.EventFieldTotal),
		"the event must carry the order's total as a STRING without decimals")
}

// The order.placed scenario's HAND-computed amounts.
//
//	12_000 × 1 = 12_000 subtotal
//	12_000 × 20% = 2_400 tax
//	12_000 - 0 + 2_400 + 0 = 14_400 grand total
const (
	eventUnitPrice    int64 = 12_000
	eventQuantity     int64 = 1
	eventSubtotal     int64 = 12_000
	eventTax          int64 = 2_400
	eventTotal        int64 = 14_400
	eventInitialStock int64 = 5
)

// TestOrderPlacedEventIsPublished proves the event half of the Phase 6 DoD:
// "order.placed" REALLY is published and its payload carries the fields in the
// contract.
//
// The subscriber is attached to core.eventbus in TestMain (see [orderEventLog]);
// it is the very same data path as in production and there is no fake in the test.
// Seeing only that the event "arrived" is not enough: every field is a CROSS-MODULE
// CONTRACT, and a missing field or one whose type has drifted drops subscribers
// silently.
func TestOrderPlacedEventIsPublished(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Event Product", map[string]int64{
		taxedCurrency: eventUnitPrice,
	}, eventInitialStock)

	cartID, totals := prepareCart(ctx, t, customerID, variantID, eventQuantity)
	assertTotals(t, totals, expectedTotal{
		subtotal: eventSubtotal,
		discount: 0,
		tax:      eventTax,
		shipping: 0,
		total:    eventTotal,
	}, "after the event cart was prepared")

	result, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: manual.ID,
		PaymentData:       paymentBehavior(t, manual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     eventTotal,
	})
	require.NoError(t, err, "the cart must be convertible into an order")

	event := eventLog.waitFor(t, result.OrderID)

	require.Equal(t, ordersvc.EventOrderPlaced, event.Name,
		"the event's name must be the name in the contract; on the Redis backend the name is "+
			"also the STREAM name, and changing it means every subscriber silently stops "+
			"receiving events")
	require.NotEmpty(t, event.ID,
		"the event's ID must be filled in; consumers use it as an idempotency key")
	require.False(t, event.OccurredAt.IsZero(), "the moment the event occurred must be stamped")

	order, err := orderSvc.GetOrder(ctx, result.OrderID)
	require.NoError(t, err, "the order must be readable")

	require.Equal(t, result.OrderID, olayAlani(t, event, ordersvc.EventFieldOrderID),
		"the payload must carry the order's ID; it is the ONLY way for the subscriber to reach "+
			"the details")
	require.Equal(t, "14400", olayAlani(t, event, ordersvc.EventFieldTotal),
		"the payload must carry the order's total as a STRING without decimals: %d minor units "+
			"-> %q. Had it been carried as a number, it would resolve to a float64 on the Redis "+
			"backend and amounts above 2^53 would be silently rounded (plan Section 8: NEVER "+
			"float)", eventTotal, "14400")
	require.Equal(t, ordermodels.OrderPending.String(), olayAlani(t, event, ordersvc.EventFieldStatus),
		"the payload must carry the order's status")
	require.Equal(t, taxedRegionID, olayAlani(t, event, ordersvc.EventFieldRegionID),
		"the payload must carry the order's region")
	require.Equal(t, customerID, olayAlani(t, event, ordersvc.EventFieldCustomerID),
		"the payload must carry the order's customer")
	require.Equal(t, taxedCurrency, olayAlani(t, event, ordersvc.EventFieldCurrencyCode),
		"the payload must carry the order's currency; the amount alone is meaningless")
	require.Equal(t, "1", olayAlani(t, event, ordersvc.EventFieldItemCount),
		"the payload must carry the line count as a STRING without decimals")
	require.NotEmpty(t, olayAlani(t, event, ordersvc.EventFieldDisplayID),
		"the payload must carry the order number shown to the customer")
	require.Equal(t, order.PlacedAt.UTC().Format("2006-01-02"),
		olayAlani(t, event, ordersvc.EventFieldPlacedAt)[:len("2006-01-02")],
		"the timestamp in the payload must come from the order's PlacedAt value")

	// The e-mail is DELIBERATELY absent from the payload: events are written to Redis
	// and are PERMANENT there; putting personal data into a permanent stream is an
	// unnecessary spread for information that already sits on the order itself (plan
	// Section 8).
	require.NotContains(t, event.Data, "email",
		"the payload must NOT carry the e-mail; personal data is not put into a permanent event "+
			"stream")
}

// The constants of the insufficient stock scenario.
//
// The cart's quantity is MORE than the stock; the correctness of the amounts is not
// this scenario's subject, because the workflow stops at the FIRST step after doing
// the computation.
const (
	insufficientUnitPrice    int64 = 20_000
	insufficientQuantity     int64 = 5
	insufficientInitialStock int64 = 2
)

// TestNoOrderIsCreatedWhenStockIsInsufficient verifies that a cart asking for more
// than the stock does NOT TURN INTO an order.
//
// The step is the saga's FIRST, so there is nothing to compensate; what is probed
// is that the workflow STOPS here and leaves no trace behind it. The stock check is
// done INSIDE the reservation call, under the lock: a "is there enough" read done
// beforehand would be a copy open to a race (see checkoutwf.Inventory).
func TestNoOrderIsCreatedWhenStockIsInsufficient(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, inventoryItemID := newStockedVariant(ctx, t, "E2E Insufficient Stock Product", map[string]int64{
		taxedCurrency: insufficientUnitPrice,
	}, insufficientInitialStock)

	cartID, _ := prepareCart(ctx, t, customerID, variantID, insufficientQuantity)

	_, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: manual.ID,
		PaymentData:       paymentBehavior(t, manual.OutcomeAuthorize),
		Email:             email,
	})
	require.Error(t, err,
		"more than the stock must NOT BE ORDERABLE; if it were, the store would collect money "+
			"for goods it cannot deliver")
	require.True(t, errors.IsConflict(err),
		"insufficient stock must be an errors.Conflict: the input is valid, the state of the "+
			"world is unfavourable, and the client can lower the quantity and try AGAIN. "+
			"Returned error: %v", err)
	require.ErrorContains(t, err, checkoutwf.StepReserveInventory,
		"the error must NAME THE STEP THAT BLEW UP; the step name is written to the execution "+
			"record too and it is the only thing the operator sees")

	// --- no order MUST BE CREATED ---
	listed, err := orderSvc.ListOrders(ctx, ordersvc.ListOrdersInput{CustomerID: &customerID})
	require.NoError(t, err, "the customer's orders must be readable")

	orders, totalCount := listed.Items, listed.Count
	require.Empty(t, orders,
		"no order must be created AT ALL: create_order comes AFTER reserve_inventory and, since "+
			"the stock step blew up, it must not have run at all. An order record — even a "+
			"canceled one — would mean an order that was never attempted exists")
	require.Zero(t, totalCount, "the counter must be zero too")

	// --- the payment step must NOT be reached at all ---
	require.Equal(t, insufficientInitialStock, sellableQuantity(ctx, t, inventoryItemID),
		"the stock must be COMPLETELY UNTOUCHED; had a partial reservation (say the 2 units on "+
			"hand) been made and left, the stock would be temporarily locked even though the "+
			"whole cart cannot be met")
	level := stockLevel(ctx, t, inventoryItemID)
	require.Equal(t, int64(0), level.ReservedQuantity,
		"the reserved quantity must stay zero; a reservation either happens ENTIRELY or not at all")

	cart, err := cartSvc.GetCart(ctx, cartID)
	require.NoError(t, err, "the cart must be readable")
	require.False(t, cart.Completed(),
		"the cart must stay open; the customer must be able to lower the quantity and try again")
}

// The repeat scenario's HAND-computed amounts.
//
//	25_000 × 2 = 50_000 subtotal
//	50_000 × 20% = 10_000 tax
//	50_000 - 0 + 10_000 + 0 = 60_000 grand total
const (
	repeatUnitPrice    int64 = 25_000
	repeatQuantity     int64 = 2
	repeatSubtotal     int64 = 50_000
	repeatTax          int64 = 10_000
	repeatTotal        int64 = 60_000
	repeatInitialStock int64 = 4
)

// TestSameCartCannotBeCompletedTwice verifies that a cart that has been ordered
// successfully cannot be ordered a second time, and documents WHERE the second call
// stops.
//
// # Observed behaviour
//
// The second call NEVER REACHES the saga engine. The preparation phase (prepare)
// runs BEFORE the engine's idempotency check and its first job is to refresh the
// computation; whereas the successful first execution has stamped the cart as
// completed and the cart module refuses to compute on a completed cart. So the
// error returned is the cart computation's conflict (code: cartwf.CodeCartCompleted),
// not the engine's "this key has already been used" answer.
//
// # Why this is the RIGHT behaviour
//
// There are three lines of defence and the second call hits the CHEAPEST one:
//
//  1. The cart stamp (the line here): it stops without making any outside call.
//  2. The engine's idempotency key ("complete_cart:<cart>"): the record is in the
//     database, so TWO REPLICAS cannot order the same cart at the same time. The
//     stamp alone would not have been enough for that.
//  3. The modules' own idempotency guards (order key, payment session key): if the
//     saga retries a step, a second order or a second capture is not born.
//
// The cheap line being hit first is what we want: the second call neither reserves
// stock, nor opens an order, nor goes to the payment provider. The error's CLASS
// matters for the same reason — Conflict tells the client "this work is already
// done"; had it said Internal the client would have retried needlessly.
func TestSameCartCannotBeCompletedTwice(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, inventoryItemID := newStockedVariant(ctx, t, "E2E Repeat Product", map[string]int64{
		taxedCurrency: repeatUnitPrice,
	}, repeatInitialStock)

	cartID, totals := prepareCart(ctx, t, customerID, variantID, repeatQuantity)
	assertTotals(t, totals, expectedTotal{
		subtotal: repeatSubtotal,
		discount: 0,
		tax:      repeatTax,
		shipping: 0,
		total:    repeatTotal,
	}, "after the repeat cart was prepared")

	input := checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: manual.ID,
		PaymentData:       paymentBehavior(t, manual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     repeatTotal,
	}

	first, err := orderWorkflows.CompleteCart(ctx, input)
	require.NoError(t, err, "the first call must be able to turn the cart into an order")
	require.True(t, first.CartCompleted, "the first call must stamp the cart completed")

	// --- the second call ---

	second, err := orderWorkflows.CompleteCart(ctx, input)
	require.Error(t, err,
		"the same cart must not be orderable a SECOND TIME; if it were, the customer would be "+
			"charged twice for the same cart and the stock would be deducted twice")
	require.Equal(t, checkoutwf.CompleteCartResult{}, second,
		"the second call must NOT LEAK a half result")
	require.True(t, errors.IsConflict(err),
		"the second call must be an errors.Conflict; the client reads it as 'this work is already "+
			"done' and does not retry. Returned error: %v", err)
	require.Equal(t, cartwf.CodeCartCompleted, errors.CodeOf(err),
		"the second call must stop AT THE CART COMPUTATION (code: %s): the preparation runs "+
			"BEFORE the engine's idempotency check and a completed cart cannot be computed. The "+
			"code changing shows the workflow has started hitting a more expensive line (the "+
			"engine or a module guard) and that line may have made an outside call. "+
			"Returned error: %v", cartwf.CodeCartCompleted, err)

	// --- the second call must leave no side effect ---

	listed, err := orderSvc.ListOrders(ctx, ordersvc.ListOrdersInput{CustomerID: &customerID})
	require.NoError(t, err, "the customer's orders must be readable")

	orders := listed.Items
	require.Len(t, orders, 1,
		"only ONE order must be created; a second order would mean the same cart was sold twice")
	require.Equal(t, first.OrderID, orders[0].ID,
		"the remaining order must be the first call's order")

	require.Equal(t, repeatInitialStock-repeatQuantity, sellableQuantity(ctx, t, inventoryItemID),
		"the stock must drop ONLY ONCE (%d - %d); a second deduction would erase goods that were "+
			"never sold from the stock", repeatInitialStock, repeatQuantity)

	collection, err := paymentSvc.GetPaymentCollection(ctx, first.PaymentCollectionID)
	require.NoError(t, err, "the payment collection must be readable")
	require.Equal(t, repeatTotal, collection.CapturedAmount,
		"the amount captured must be that of a SINGLE order's total; had it been twice as much, "+
			"the customer would have been charged twice for the same cart")
}

// TestOrderWorkflowBuildsFromProductionWiring verifies SEPARATELY that the order
// completion workflow can be built from the PRODUCTION registrations.
//
// The other scenarios USE the workflow; this test probes that it CAN BE BUILT and,
// when a signature drifts, tells through the container's typed error which surface
// is missing or mismatched. The distinction matters because the wiring is not
// checked at compile time: surfaces are resolved by name and the modules do not
// know this package (per ADR 0006), so compatibility can only be proven at runtime.
func TestOrderWorkflowBuildsFromProductionWiring(t *testing.T) {
	workflow, err := checkoutwf.FromContainer(ctr)
	require.NoError(t, err,
		"the order completion workflow must be buildable from the PRODUCTION registrations in "+
			"the ctr; the error names which surface is missing")
	require.NotNil(t, workflow)
}

// prepareCart opens a cart for a registered customer, adds a single line and
// returns the cart's ID along with its computed totals.
//
// The cart is opened for a REGISTERED customer because the Phase 6 scenarios read
// orders by customer: a guest order's customer_id is empty and the tests would see
// each other's orders. Opening the cart and adding the line are done with the cart
// WORKFLOWS (not with the cart module's service), so that price and tax come from
// exactly the Phase 5 path.
func prepareCart(
	ctx context.Context,
	t *testing.T,
	customerID, variantID string,
	quantity int64,
) (cartID string, totals cartwf.Totals) {
	t.Helper()

	cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{
		CountryCode: taxedCountry,
		CustomerID:  customerID,
	})
	require.NoError(t, err, "the fixture cart could not be opened")

	added, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    cart.CartID,
		VariantID: variantID,
		Quantity:  quantity,
	})
	require.NoError(t, err, "a line could not be added to the fixture cart")

	return cart.CartID, added.Totals
}

// paymentBehavior produces the session data that determines the manual provider's
// authorization behaviour.
//
// The data is given to the workflow as PaymentData and is forwarded to the provider
// AS IS; there is no test hook in the saga itself. The key and the values come from
// the manual package's constants: had they been written out as strings, the tests
// would keep compiling when the provider changed the contract but would silently
// start probing the DEFAULT behaviour.
func paymentBehavior(t *testing.T, outcome string) json.RawMessage {
	t.Helper()

	raw, err := json.Marshal(map[string]string{manual.DataKeyOutcome: outcome})
	require.NoError(t, err, "the payment behaviour data could not be encoded")
	return raw
}
