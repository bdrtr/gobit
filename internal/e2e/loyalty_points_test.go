//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	ordermodels "github.com/bdrtr/gobit/internal/modules/order/models"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	"github.com/bdrtr/gobit/internal/modules/payment/loyaltypoints"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	paymentmodels "github.com/bdrtr/gobit/internal/modules/payment/models"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
	"github.com/bdrtr/gobit/internal/modules/payment/storecredit"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// This file proves that the points a customer earned by paying can PAY for the
// next order, through the production stack (ADR 0165).
//
// # Why the proof has to be end to end
//
// The tender's own tests run the state machine against a memory store and
// build the session themselves, so they assume the two things actually in
// question here. First, that a balance EXISTS to spend: nothing issues points
// by hand (ADR 0164 rejected an operator write), so the only way to hold any is
// to have paid for an order and been earned them by the capture that took the
// money. Second, that the customer's identity reaches the tender across cart,
// plan, collection and session — the chain store_credit_test.go walks for the
// credit ledger, which the points tender shares by running the same machine.
//
// And one thing no unit test can see at all: that the capture paid with points
// EARNS NOTHING. The earn path reads the collection's captures one table down
// and excludes the points tender's; a unit fixture that fakes that read would
// be asserting its own fake.
//
// # The amounts are computed on paper
//
// The region is taxed at 20%, no shipping method is chosen, and the harness
// earns at the ceiling rate — one point per minor unit — so a paid order earns
// exactly what it cost:
//
//	27_500 × 1 = 27_500 subtotal
//	27_500 × 20% = 5_500 tax
//	27_500 + 5_500 = 33_000 grand total
//	33_000 × 10_000 / 10_000 = 33_000 points earned
//
// A point is worth one minor unit, so the second cart of the same total is
// covered entirely and the balance lands on ZERO. The unit price is one no
// other scenario uses; every test below takes a fresh customer.
const (
	pointsUnitPrice    int64 = 27_500
	pointsQuantity     int64 = 1
	pointsSubtotal     int64 = 27_500
	pointsTax          int64 = 5_500
	pointsTotal        int64 = 33_000
	pointsEarned       int64 = 33_000
	pointsInitialStock int64 = 5
	// pointsStockAfterOne and pointsStockAfterTwo are the sellable quantities
	// after one and after two units left on paid orders: 5 - 1 and 5 - 2.
	pointsStockAfterOne int64 = 4
	pointsStockAfterTwo int64 = 3
	pointsGuestEmail          = "guest-points@example.test"
)

// pointsCartTotals is what every cart in this file must compute to.
var pointsCartTotals = expectedTotal{
	subtotal: pointsSubtotal,
	discount: 0,
	tax:      pointsTax,
	shipping: 0,
	total:    pointsTotal,
}

// TestPointsPayForAnOrder is the slice's whole claim in one run: the points a
// paid order earned pay for the next one, and the order they pay for earns
// none.
func TestPointsPayForAnOrder(t *testing.T) {
	ctx := t.Context()

	require.Equal(t, pointsEarned, pointsTotal*loyaltyEarnBasisPoints/10_000,
		"the hand constants above assume the harness earns at the CEILING; the rate "+
			"moved, so pointsEarned is stale and every balance below would be measured "+
			"against the wrong number")

	customerID, email := newCustomer(ctx, t)
	variantID, inventoryItemID := newStockedVariant(ctx, t, "E2E Loyalty Points Product",
		map[string]int64{taxedCurrency: pointsUnitPrice}, pointsInitialStock)

	before, err := paymentSvc.LoyaltyBalance(ctx, customerID, taxedCurrency)
	require.NoError(t, err)
	require.Zero(t, before, "a customer who never paid holds no points")

	// --- the first order is paid with MONEY and earns the points ---

	firstCart, totals := prepareCart(ctx, t, customerID, variantID, pointsQuantity)
	assertTotals(t, totals, pointsCartTotals, "after the first (card-paid) cart was prepared")

	first, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            firstCart,
		LocationID:        stockLocationID,
		PaymentProviderID: manual.ID,
		PaymentData:       paymentBehavior(t, manual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     pointsTotal,
	})
	require.NoError(t, err, "the card-paid order that earns the points must complete")
	require.NotEmpty(t, first.OrderID)
	require.NotEmpty(t, first.PaymentCollectionID)

	earned, err := paymentSvc.LoyaltyBalance(ctx, customerID, taxedCurrency)
	require.NoError(t, err)
	require.Equal(t, pointsEarned, earned,
		"at the ceiling rate one paid order earns as many points as it cost; the earn is "+
			"written by the capture, inside the same transaction as the money")
	require.Equal(t, pointsStockAfterOne, sellableQuantity(ctx, t, inventoryItemID))

	// --- the second order of the same total is paid with the POINTS ---

	secondCart, totals := prepareCart(ctx, t, customerID, variantID, pointsQuantity)
	assertTotals(t, totals, pointsCartTotals, "after the second (points-paid) cart was prepared")

	second, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            secondCart,
		LocationID:        stockLocationID,
		PaymentProviderID: loyaltypoints.ID,
		Email:             email,
		ExpectedTotal:     pointsTotal,
	})
	require.NoError(t, err,
		"a cart whose customer holds enough points must complete; the tender takes no "+
			"card data and no PaymentData, so a failure here means either the customer's "+
			"identity did not reach it or the tender is not registered")
	require.NotEmpty(t, second.OrderID)
	require.NotEmpty(t, second.PaymentCollectionID)
	require.NotEqual(t, first.PaymentCollectionID, second.PaymentCollectionID)
	require.Equal(t, pointsTotal, second.Amount,
		"the amount captured is the order's total in minor units, and a point is worth "+
			"one of them: nothing is converted at the boundary")

	// --- the points moved, and they moved ONCE ---

	after, err := paymentSvc.LoyaltyBalance(ctx, customerID, taxedCurrency)
	require.NoError(t, err)
	assert.Zero(t, after,
		"the balance must drop by the order's total exactly once and land on zero: the "+
			"hold takes the points out, the capture writes NOTHING, and the capture paid "+
			"with points EARNS nothing. A non-zero balance here is one of those three "+
			"written wrong — at this rate a capture that earned would earn itself back")

	entries, total, err := paymentSvc.ListLoyalty(ctx, paymentsvc.ListLoyaltyInput{
		CustomerID:   customerID,
		CurrencyCode: taxedCurrency,
	})
	require.NoError(t, err)

	// Checked BEFORE the row count, so that when a points-paid capture does
	// earn, the failure names the row rather than only miscounting it.
	for _, entry := range entries {
		assert.NotEqual(t, second.PaymentCollectionID, entry.Reference,
			"no row may reference the points-paid collection: its capture earns nothing "+
				"(the earn base excludes money captured through the points tender) and its "+
				"hold references the tender's session. Row %s (%s, %d points) does",
			entry.ID, entry.Kind, entry.Points)
	}

	require.Equal(t, int64(2), total,
		"two rows and no more: the earn of the first order and the hold of the second. "+
			"The capture is recorded on the tender's SESSION, not in the ledger, and the "+
			"points-paid capture earned no third row")
	require.Len(t, entries, 2)

	hold, earn := entries[0], entries[1]
	assert.Equal(t, paymentmodels.LoyaltyHold, hold.Kind, "newest first")
	assert.Equal(t, -pointsTotal, hold.Points,
		"the hold is written NEGATIVE, which is what makes the sum the answer to 'what can "+
			"this customer still spend'")
	assert.True(t, strings.HasPrefix(hold.Reference, paymentmodels.LoyaltySessionIDPrefix),
		"a spend row references the tender's OWN session and never a collection: the earn "+
			"target is recomputed per collection, and a hold carrying a collection id would "+
			"read as points already written. Reference: %q", hold.Reference)

	assert.Equal(t, paymentmodels.LoyaltyEarn, earn.Kind)
	assert.Equal(t, pointsEarned, earn.Points)
	assert.True(t, strings.HasPrefix(earn.Reference, paymentmodels.PaymentCollectionIDPrefix),
		"an earn row references the payment collection it was earned against. Reference: %q",
		earn.Reference)
	assert.Equal(t, first.PaymentCollectionID, earn.Reference,
		"the earn belongs to the CARD-paid collection")

	// --- the collection paid with points is a CAPTURED collection ---

	collection, err := paymentSvc.GetPaymentCollection(ctx, second.PaymentCollectionID)
	require.NoError(t, err, "the points-paid collection must be readable from the payment module")
	assert.Equal(t, paymentmodels.CollectionCaptured, collection.Status,
		"the module's own record of the points-paid order must say CAPTURED; the tender's "+
			"capture writes no ledger row, so this status is the only place the capture "+
			"is visible to the rest of the module")
	assert.Equal(t, pointsTotal, collection.CapturedAmount)
	assert.Equal(t, int64(0), collection.AuthorizedAmount,
		"after capture nothing must remain on hold")
	assert.Equal(t, customerID, collection.CustomerID,
		"the collection carries the customer; it is where the tender took the owner from")

	// --- and both orders are real orders ---

	listed, err := orderSvc.ListOrders(ctx, ordersvc.ListOrdersInput{CustomerID: &customerID})
	require.NoError(t, err)
	require.Len(t, listed.Items, 2)
	for _, order := range listed.Items {
		assert.Equal(t, ordermodels.OrderPending, order.Status, "order %s", order.ID)
		assert.Equal(t, pointsTotal, order.Total, "order %s", order.ID)
	}
	assert.Equal(t, pointsStockAfterTwo, sellableQuantity(ctx, t, inventoryItemID),
		"stock leaves on a points-paid order exactly as it does on a card-paid one")
}

// TestPointsDeclineThenTheSameCartPaysByCard walks the ordinary path of a
// points tender: not enough points, so pay by card instead — with the SAME
// cart.
//
// Two claims. Not having enough is a DECLINE and not a fault, so the shopper
// keeps the cart and the ledger is untouched. And a failed execution releases
// its idempotency key, so the same cart can be completed again with another
// tender; without that a customer whose points fell short could never pay for
// that cart at all, which is the failure the engine's failed state exists to
// prevent. store_credit_test.go proves the decline and stops; this is the half
// it left, and for points it is the common case.
func TestPointsDeclineThenTheSameCartPaysByCard(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, inventoryItemID := newStockedVariant(ctx, t, "E2E Short Points Product",
		map[string]int64{taxedCurrency: pointsUnitPrice}, pointsInitialStock)

	balance, err := paymentSvc.LoyaltyBalance(ctx, customerID, taxedCurrency)
	require.NoError(t, err)
	require.Zero(t, balance, "the customer must hold NO points for the decline to be genuine")

	cartID, totals := prepareCart(ctx, t, customerID, variantID, pointsQuantity)
	assertTotals(t, totals, pointsCartTotals, "after the cart was prepared")

	// --- points: DECLINED ---

	result, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: loyaltypoints.ID,
		Email:             email,
		ExpectedTotal:     pointsTotal,
	})
	require.Error(t, err, "an order must not be placed against points the customer does not have")
	require.Equal(t, checkoutwf.CompleteCartResult{}, result)
	require.True(t, errors.IsConflict(err),
		"an insufficient balance is a clash with the state of the world, not a server "+
			"fault: the shopper can pay another way and try again. Returned error: %v", err)
	require.Equal(t, paymentsvc.CodeAuthorizationDeclined, errors.CodeOf(err),
		"the OUTERMOST code must be the decline, not the engine's generic step failure: it "+
			"is the field the storefront branches on. Returned error: %v", err)

	unchanged, err := paymentSvc.LoyaltyBalance(ctx, customerID, taxedCurrency)
	require.NoError(t, err)
	assert.Zero(t, unchanged,
		"a refused authorization must leave the ledger exactly as it was; a hold written "+
			"before the balance check would strand the points forever")

	_, rows, err := paymentSvc.ListLoyalty(ctx, paymentsvc.ListLoyaltyInput{
		CustomerID:   customerID,
		CurrencyCode: taxedCurrency,
	})
	require.NoError(t, err)
	assert.Zero(t, rows, "the decline writes nothing to the ledger, not even a released hold")

	assert.Equal(t, pointsInitialStock, sellableQuantity(ctx, t, inventoryItemID),
		"the saga's compensation must give the stock back, the same as for a declined card")

	open, err := cartSvc.GetCart(ctx, cartID)
	require.NoError(t, err)
	require.False(t, open.Completed(),
		"the cart of a declined payment must stay OPEN; a closed one would have to be rebuilt")

	// --- the SAME cart: paid by card ---

	paid, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: manual.ID,
		PaymentData:       paymentBehavior(t, manual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     pointsTotal,
	})
	require.NoError(t, err,
		"the same cart must complete with another tender after a decline: a failed "+
			"execution RELEASES its idempotency key, or the customer could never pay for "+
			"this cart again")
	require.Equal(t, cartID, paid.CartID)
	require.NotEmpty(t, paid.OrderID)
	require.Equal(t, pointsTotal, paid.Amount)

	listed, err := orderSvc.ListOrders(ctx, ordersvc.ListOrdersInput{CustomerID: &customerID})
	require.NoError(t, err)
	statuses := map[ordermodels.OrderStatus]int{}
	for _, order := range listed.Items {
		statuses[order.Status]++
	}
	assert.Equal(t, map[ordermodels.OrderStatus]int{
		ordermodels.OrderCanceled: 1,
		ordermodels.OrderPending:  1,
	}, statuses,
		"the declined attempt leaves its order CANCELED (compensation cancels, it does not "+
			"delete) and the card-paid attempt leaves one PENDING order")

	assert.Equal(t, pointsStockAfterOne, sellableQuantity(ctx, t, inventoryItemID),
		"one unit left on the paid order and none on the declined one")

	entries, _, err := paymentSvc.ListLoyalty(ctx, paymentsvc.ListLoyaltyInput{
		CustomerID:   customerID,
		CurrencyCode: taxedCurrency,
	})
	require.NoError(t, err)
	require.Len(t, entries, 1,
		"the card-paid retry earns; the declined attempt left no row of any kind")
	assert.Equal(t, paymentmodels.LoyaltyEarn, entries[0].Kind)
	assert.Equal(t, paid.PaymentCollectionID, entries[0].Reference)
}

// TestAGuestChoosingPointsGetsAConflictOverHTTP proves that a cart naming
// nobody, completed with a balance tender, is refused with a CONFLICT and not
// with a server error — over the production transport, for both tenders,
// because they are one state machine (ADR 0165; defect D113).
//
// # What arrives
//
// The tender refuses at CreateSession with errors.Conflict carrying its own
// NoCustomer code. The payment service returns that error unchanged, the
// checkout's authorize step returns it to the engine, and the engine's wrap
// keeps the step error's kind and code (workflow's stepFailureCode) while the
// compensation cancels the order and releases the stock. core/http turns the
// conflict into 409 with the tender's code in the body — so the body's code is
// loyaltypoints.CodeNoCustomer or storecredit.CodeNoCustomer itself, with
// nothing in between. Before ADR 0165 the store-credit tender answered this
// case with errors.Internal, which the transport masked into a 500 whose body
// told the storefront nothing.
//
// # Why 409 and not 500 matters
//
// A guest who picked a person-bound tender from a list that should not have
// offered it needs to be told to sign in or pick another; a 500 tells them the
// shop is broken and tells the operator's alerting the same. The request is
// well formed and the state it meets — a cart with no owner — is what refuses
// it, which is what a conflict says.
func TestAGuestChoosingPointsGetsAConflictOverHTTP(t *testing.T) {
	ctx := t.Context()

	variantID, inventoryItemID := newStockedVariant(ctx, t, "E2E Guest Points Product",
		map[string]int64{taxedCurrency: pointsUnitPrice}, pointsInitialStock)

	tenders := []struct {
		name       string
		providerID string
		code       string
	}{
		{name: "loyalty points", providerID: loyaltypoints.ID, code: loyaltypoints.CodeNoCustomer},
		{name: "store credit", providerID: storecredit.ID, code: storecredit.CodeNoCustomer},
	}

	for _, tender := range tenders {
		t.Run(tender.name, func(t *testing.T) {
			// A GUEST cart: no customer header, no customer in the body.
			cartID := openStorefrontCart(t, "", pointsGuestEmail)

			added := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/line-items",
				fmt.Sprintf(`{"variant_id":%q,"quantity":%d}`, variantID, pointsQuantity))
			require.Equal(t, http.StatusCreated, added.Code, "body: %s", added.Body.String())

			// The body is what storefrontCompletionBody builds for the manual
			// provider, with the tender's id and no payment data: a balance
			// tender takes none.
			rejected := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
				fmt.Sprintf(`{"payment_provider_id":%q,"expected_total":%d}`, tender.providerID, pointsTotal))

			require.Equal(t, http.StatusConflict, rejected.Code,
				"a guest choosing %s must get a CONFLICT, not a server error: the request is "+
					"well formed and the cart's missing owner is what refuses it (D113); body: %s",
				tender.name, rejected.Body.String())
			assert.Equal(t, tender.code, errorCode(t, rejected),
				"the body's code must NAME the tender's refusal so the storefront can tell "+
					"'sign in' from 'try again'; body: %s", rejected.Body.String())

			open, err := cartSvc.GetCart(ctx, cartID)
			require.NoError(t, err, "the cart of a refused completion must still be readable")
			assert.False(t, open.Completed(),
				"the cart must NOT close on a refusal; the guest picks another tender or signs in")
		})
	}

	assert.Equal(t, pointsInitialStock, sellableQuantity(ctx, t, inventoryItemID),
		"the stock has to be where it started: whatever either refusal reserved came "+
			"back, and neither left a unit on an order that was never paid")
}
