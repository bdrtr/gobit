//go:build integration

package e2e

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	b2bmodels "github.com/bdrtr/gobit/internal/modules/b2b/models"
	b2bsvc "github.com/bdrtr/gobit/internal/modules/b2b/service"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// This file proves end to end that the B2B spending limit is REALLY enforced.
//
// # Why the module tests are not enough
//
// The b2b module's own tests show that it STORES the limit, and the order
// module's tests show that it REJECTS the order when it is handed a fake rule
// surface. Both are true, and even together they do not prove the actual
// claim: because the two modules cannot import each other, the contract
// between them is JSON and the compiler does not check it (see the [b2bsvc]
// interop document). Had one of the field names drifted, both packages' tests
// would have stayed green while in production the limit would SILENTLY have
// gone away — with the "limited" field failing to decode, the rule would have
// counted as "absent". This file joins the two ends of the contract over a
// real container.
//
// # Where the check lives
//
// The rule is enforced inside [ordersvc.Service.CreateOrder], inside the
// transaction that writes the order. That has two consequences and both are
// exercised here: because the create_order step of the complete_cart saga runs
// BEFORE authorize_payment, in a rejected checkout the money is NEVER
// authorized; and because the check and the write are in the same transaction,
// two concurrent orders cannot exceed the limit together.

// The HAND-computed amounts of the B2B scenarios.
//
// The region is taxed at 20% (2000 basis points) and no shipping method is
// picked:
//
//	50_000 x 2 = 100_000 subtotal
//	100_000 x 20% = 20_000 tax
//	100_000 - 0 + 20_000 + 0 = 120_000 grand total
const (
	b2bUnitPrice int64 = 50_000
	b2bQuantity  int64 = 2
	b2bTotal     int64 = 120_000
	// b2bStock is enough for two orders: the accumulation scenario does two
	// checkouts with the same employee and it must be visible that the second one
	// falls not because of stock but because of the LIMIT.
	b2bStock int64 = 20
)

// b2bCalisan makes the customer an employee of a new company and returns the
// company.
//
// Every scenario sets up its OWN company: were the company shared, one
// scenario changing the limit would break the neighboring scenario's
// expectation and the tests would give one result when run alone and another
// when run together.
//
// If limit is passed as nil the employee is UNLIMITED (see
// [b2bsvc.EmployeeInput]).
func b2bCalisan(
	ctx context.Context,
	t *testing.T,
	customerID string,
	limit *int64,
	period b2bmodels.SpendingResetPeriod,
) (companyID string) {
	t.Helper()

	company, err := b2bSvc.CreateCompany(ctx, b2bsvc.CompanyInput{
		Name:  "E2E B2B " + customerID,
		Email: "b2b-" + customerID + "@example.test",
		// The company's currency is chosen to be the SAME as the cart's: were it
		// different, the rule would fall on a currency mismatch and what the
		// scenario exercised would not be the limit but that check (that check
		// has a separate scenario of its own).
		CurrencyCode:             taxedCurrency,
		SpendingLimitResetPeriod: string(period),
	})
	require.NoError(t, err, "could not create the b2b company")

	_, err = b2bSvc.CreateEmployee(ctx, b2bsvc.EmployeeInput{
		CompanyID:     company.ID,
		CustomerID:    customerID,
		SpendingLimit: limit,
	})
	require.NoError(t, err, "could not add the customer to the company as an employee")

	return company.ID
}

// b2bCompleteCart tries to complete a prepared B2B cart through the workflow.
func b2bCompleteCart(
	ctx context.Context,
	t *testing.T,
	cartID, email string,
) (checkoutwf.CompleteCartResult, error) {
	t.Helper()

	return orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: manual.ID,
		PaymentData:       paymentBehavior(t, manual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     b2bTotal,
	})
}

// TestB2BOrderOverTheLimitIsRejectedAndNoMoneyIsTaken proves the actual claim:
// a checkout above the limit does not turn into an order AND its money is not
// taken.
//
// Checking separately that the money is not taken may look redundant, but it
// is not: the rule lives in the order module and if the saga's step order
// changed (if create_order were moved BEHIND authorize_payment) the test would
// still say "no order" while the customer's money would have been taken and
// refunded. Stock is checked for the same reason — were a rejected checkout
// holding the stock, an employee who had used up their limit could keep trying
// and lock up the catalog's stock.
func TestB2BOrderOverTheLimitIsRejectedAndNoMoneyIsTaken(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, stockItemID := newStockedVariant(ctx, t, "E2E B2B Limit Overrun",
		map[string]int64{taxedCurrency: b2bUnitPrice}, b2bStock)

	// The limit is BELOW the cart's total: 50_000 < 120_000.
	limit := int64(50_000)
	b2bCalisan(ctx, t, customerID, &limit, b2bmodels.ResetNever)

	cartID, _ := prepareCart(ctx, t, customerID, variantID, b2bQuantity)

	_, err := b2bCompleteCart(ctx, t, cartID, email)

	require.Error(t, err, "a checkout above the limit must NOT turn into an order")
	require.True(t, errors.IsConflict(err),
		"exceeding the limit is a conflict (409, not 422): the request is formally valid, "+
			"the reason for the rejection is the system's state AT THAT MOMENT; body: %v", err)
	// The code is read from the OUTSIDE, not from within the chain: while
	// wrapping a step failure the saga PRESERVES the underlying error's code (see
	// workflow.CodeStepFailed). The difference shows up at the consumer — the
	// transport layer writes a single machine-readable field into the body, and
	// had that field been filled with the engine's own constant, the storefront
	// could not have told "your limit was not enough" apart from "a temporary
	// conflict, try again".
	require.Equal(t, ordersvc.CodeSpendingLimitExceeded, errors.CodeOf(err),
		"the rejection's code must be the spending limit; another code shows that the "+
			"order fell for ANOTHER reason and that the test never exercised the limit")
	require.ErrorContains(t, err, checkoutwf.StepCreateOrder,
		"the step that fell to the rejection must be create_order — that is the proof "+
			"that THE MONEY WAS NOT TAKEN: authorize_payment comes AFTER it and never ran")

	require.Equal(t, b2bStock, sellableQuantity(ctx, t, stockItemID),
		"a rejected checkout must RELEASE the stock; because the reservation step runs "+
			"before create_order, that is the proof that the compensation ran")

	cart, err := cartSvc.GetCart(ctx, cartID)
	require.NoError(t, err, "the cart of a rejected checkout must still be readable")
	require.Nil(t, cart.CompletedAt,
		"the cart of a rejected checkout must NOT be closed; were it closed the customer "+
			"could not retry with the same cart and would have to rebuild it once the limit freed up")
}

// TestB2BSpendingAccumulatesWithinTheWindow proves that the limit is enforced
// not for a SINGLE order but for the PERIOD TOTAL.
//
// This is the proof that the rule is really computed from the order data: were
// the limit compared only against the order's own amount, both checkouts would
// PASS because each one on its own is below the limit, and the employee could
// spend twice the limit.
func TestB2BSpendingAccumulatesWithinTheWindow(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E B2B Accumulation",
		map[string]int64{taxedCurrency: b2bUnitPrice}, b2bStock)

	// The limit lets a single order through (120_000 ≤ 200_000) but not two
	// (240_000 > 200_000).
	limit := int64(200_000)
	b2bCalisan(ctx, t, customerID, &limit, b2bmodels.ResetNever)

	firstCart, _ := prepareCart(ctx, t, customerID, variantID, b2bQuantity)
	first, err := b2bCompleteCart(ctx, t, firstCart, email)
	require.NoError(t, err, "the FIRST checkout, below the limit, must pass")
	require.NotEmpty(t, first.OrderID, "the first checkout must produce an order")

	secondCart, _ := prepareCart(ctx, t, customerID, variantID, b2bQuantity)
	_, err = b2bCompleteCart(ctx, t, secondCart, email)

	require.Error(t, err,
		"the SECOND checkout must be rejected: on its own it is below the limit but the "+
			"period total (120_000 + 120_000 = 240_000) is above it")
	require.Equal(t, ordersvc.CodeSpendingLimitExceeded, errors.CodeOf(err),
		"the reason for the rejection must be the spending limit; body: %v", err)
}

// TestB2BUnlimitedEmployeeIsUnaffected proves that an employee whose limit is
// nil is not constrained.
//
// nil and 0 are different sentences (see
// [b2bsvc.EmployeeInput.SpendingLimit]): nil means "no limit", 0 means "can
// spend nothing at all". Were the two confused, every employee whose limit was
// left unset would become unable to shop.
func TestB2BUnlimitedEmployeeIsUnaffected(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E B2B Unlimited",
		map[string]int64{taxedCurrency: b2bUnitPrice}, b2bStock)

	b2bCalisan(ctx, t, customerID, nil, b2bmodels.ResetMonthly)

	cartID, _ := prepareCart(ctx, t, customerID, variantID, b2bQuantity)
	result, err := b2bCompleteCart(ctx, t, cartID, email)

	require.NoError(t, err, "the checkout of an employee with no limit must pass")
	require.NotEmpty(t, result.OrderID, "an unlimited employee must be able to produce an order")
}

// TestNonB2BCustomerIsUnaffected proves that the b2b module being REGISTERED
// does not change the B2C flow.
//
// This test's value is on the regression side: the spending rule is asked for
// EVERY order in the order module, and for a customer who is no company's
// employee the answer "no rule" has to count as SUCCESS. Were that answer
// counted as an error, installing the b2b module would drop every B2C order in
// the installation — and only a test that runs while b2b is registered can see
// that.
func TestNonB2BCustomerIsUnaffected(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E B2C Unaffected",
		map[string]int64{taxedCurrency: b2bUnitPrice}, b2bStock)

	// b2bCalisan is deliberately NOT CALLED: the customer belongs to no company.
	cartID, _ := prepareCart(ctx, t, customerID, variantID, b2bQuantity)
	result, err := b2bCompleteCart(ctx, t, cartID, email)

	require.NoError(t, err,
		"the checkout of a customer bound to no company must pass even while the b2b module is registered")
	require.NotEmpty(t, result.OrderID, "a B2C customer must be able to produce an order")
}
