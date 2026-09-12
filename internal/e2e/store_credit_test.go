//go:build integration

package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	ordermodels "github.com/bdrtr/gobit/internal/modules/order/models"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	paymentmodels "github.com/bdrtr/gobit/internal/modules/payment/models"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
	"github.com/bdrtr/gobit/internal/modules/payment/storecredit"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// This file proves that a shop can hold money for a customer and that the
// customer can SPEND it, through the production stack (ADR 0152).
//
// # Why the proof has to be end to end
//
// The provider's own tests run against a memory store, which means they assume
// the thing that is actually in question: that the customer's identity REACHES
// the provider. It travels cart → plan → collection → session, across a module
// boundary and two rows, and every link in that chain is a place where it can
// arrive empty — at which point the provider refuses, and store credit is a
// tender nobody can use. A unit test cannot see that, because it builds the
// session itself.
//
// # The amounts are computed on paper
//
// The region is taxed at 20%, no shipping method is chosen:
//
//	30_000 × 1 = 30_000 subtotal
//	30_000 × 20% = 6_000 tax
//	30_000 + 6_000 = 36_000 grand total
const (
	creditUnitPrice    int64 = 30_000
	creditQuantity     int64 = 1
	creditSubtotal     int64 = 30_000
	creditTax          int64 = 6_000
	creditTotal        int64 = 36_000
	creditInitialStock int64 = 5
	creditRemainingStk int64 = 4
	creditIssued       int64 = 50_000
	creditLeft         int64 = 14_000
	creditNotEnough    int64 = 10_000
	creditIssuedReason       = "goodwill for a late delivery"
)

// TestStoreCreditPaysForAnOrder is the slice's whole claim in one run: money put
// on a customer's account is money they can check out with.
func TestStoreCreditPaysForAnOrder(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, inventoryItemID := newStockedVariant(ctx, t, "E2E Store Credit Product",
		map[string]int64{taxedCurrency: creditUnitPrice}, creditInitialStock)

	issued, err := paymentSvc.IssueCredit(ctx, paymentsvc.IssueCreditInput{
		CustomerID:   customerID,
		CurrencyCode: taxedCurrency,
		Amount:       creditIssued,
		Reason:       creditIssuedReason,
	})
	require.NoError(t, err, "an operator must be able to put money on a customer's account")
	require.Equal(t, paymentmodels.StoreCreditIssue, issued.Kind)

	balance, err := paymentSvc.StoreCreditBalance(ctx, customerID, taxedCurrency)
	require.NoError(t, err)
	require.Equal(t, creditIssued, balance,
		"the balance is the LEDGER's sum and nothing else holds it; if this is wrong the "+
			"rest of the test is measuring a number that was written twice")

	cartID, totals := prepareCart(ctx, t, customerID, variantID, creditQuantity)
	assertTotals(t, totals, expectedTotal{
		subtotal: creditSubtotal,
		discount: 0,
		tax:      creditTax,
		shipping: 0,
		total:    creditTotal,
	}, "after the store-credit cart was prepared")

	result, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: storecredit.ID,
		Email:             email,
		ExpectedTotal:     creditTotal,
	})
	require.NoError(t, err,
		"a cart whose customer holds enough credit must complete; the provider takes no "+
			"card data and no PaymentData, so a failure here means the customer's identity "+
			"did not reach it")
	require.NotEmpty(t, result.OrderID)
	require.Equal(t, creditTotal, result.Amount)

	// --- the money moved, and it moved ONCE ---

	after, err := paymentSvc.StoreCreditBalance(ctx, customerID, taxedCurrency)
	require.NoError(t, err)
	assert.Equal(t, creditLeft, after,
		"the balance must drop by the order's total exactly once: the hold takes it out and "+
			"the capture writes NOTHING, so a second negative row would charge the customer "+
			"twice for one order")

	entries, total, err := paymentSvc.ListStoreCredit(ctx, paymentsvc.ListStoreCreditInput{
		CustomerID:   customerID,
		CurrencyCode: taxedCurrency,
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), total,
		"two rows and no more: the issue and the hold. The capture is recorded on the "+
			"SESSION, not in the ledger")
	require.Len(t, entries, 2)
	assert.Equal(t, paymentmodels.StoreCreditHold, entries[0].Kind, "newest first")
	assert.Equal(t, -creditTotal, entries[0].Amount,
		"the hold is written NEGATIVE, which is what makes the sum above the answer to "+
			"'what can this customer still spend'")
	assert.Equal(t, creditIssuedReason, entries[1].Reason,
		"the reason survives into the history; it is the half a balance column cannot hold")

	// --- and the order is a real order ---

	listed, err := orderSvc.ListOrders(ctx, ordersvc.ListOrdersInput{CustomerID: &customerID})
	require.NoError(t, err)
	require.Len(t, listed.Items, 1)
	assert.Equal(t, ordermodels.OrderPending, listed.Items[0].Status)
	assert.Equal(t, creditRemainingStk, sellableQuantity(ctx, t, inventoryItemID),
		"stock leaves on a credit-paid order exactly as it does on a card-paid one")
}

// TestStoreCreditDeclinesWhenTheBalanceIsShort proves that not enough money is an
// ordinary DECLINE and not a fault.
//
// The distinction is the whole reason the provider answers an insufficient
// balance with a failed session rather than an error: a decline leaves the
// shopper able to pay another way, while a fault would be indistinguishable from
// a broken compensation. And the balance has to be untouched afterwards — a hold
// written and never released would quietly shrink what the customer can spend.
func TestStoreCreditDeclinesWhenTheBalanceIsShort(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, inventoryItemID := newStockedVariant(ctx, t, "E2E Short Credit Product",
		map[string]int64{taxedCurrency: creditUnitPrice}, creditInitialStock)

	_, err := paymentSvc.IssueCredit(ctx, paymentsvc.IssueCreditInput{
		CustomerID:   customerID,
		CurrencyCode: taxedCurrency,
		Amount:       creditNotEnough,
		Reason:       "a partial refund",
	})
	require.NoError(t, err)

	cartID, _ := prepareCart(ctx, t, customerID, variantID, creditQuantity)

	result, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: storecredit.ID,
		Email:             email,
		ExpectedTotal:     creditTotal,
	})
	require.Error(t, err, "an order must not be placed against money the customer does not have")
	require.Equal(t, checkoutwf.CompleteCartResult{}, result)
	require.True(t, errors.IsConflict(err),
		"an insufficient balance is a clash with the state of the world, not a server "+
			"fault: the shopper can pay another way and try again. Returned error: %v", err)
	require.ErrorContains(t, err, paymentsvc.CodeAuthorizationDeclined,
		"the decline must travel as a decline, so the operator reading the log can tell it "+
			"from a compensation that failed")

	balance, err := paymentSvc.StoreCreditBalance(ctx, customerID, taxedCurrency)
	require.NoError(t, err)
	assert.Equal(t, creditNotEnough, balance,
		"a refused authorization must leave the ledger exactly as it was; a hold written "+
			"before the balance check would strand the money forever")

	entries, _, err := paymentSvc.ListStoreCredit(ctx, paymentsvc.ListStoreCreditInput{
		CustomerID:   customerID,
		CurrencyCode: taxedCurrency,
	})
	require.NoError(t, err)
	require.Len(t, entries, 1, "only the issue row: the decline writes nothing to the ledger")

	assert.Equal(t, creditInitialStock, sellableQuantity(ctx, t, inventoryItemID),
		"the saga's compensation must give the stock back, the same as for a declined card")
}

// TestStoreCreditRefusesACartWithNoCustomer proves the guard that makes the
// tender safe to publish.
//
// Store credit is ONE PERSON's money. A guest cart names nobody, so there is no
// balance to spend and no owner to charge; the session is refused before any
// ledger is read. Without this the customer id arriving empty would not fail —
// it would read the balance of the customer whose id is "".
func TestStoreCreditRefusesACartWithNoCustomer(t *testing.T) {
	ctx := t.Context()

	variantID, _ := newStockedVariant(ctx, t, "E2E Guest Credit Product",
		map[string]int64{taxedCurrency: creditUnitPrice}, creditInitialStock)

	cartID, _ := prepareCart(ctx, t, "", variantID, creditQuantity)

	_, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: storecredit.ID,
		Email:             "guest@example.com",
		ExpectedTotal:     creditTotal,
	})
	require.Error(t, err, "a guest cart must not be able to open a store-credit session")
	require.ErrorContains(t, err, storecredit.CodeNoCustomer)
}
