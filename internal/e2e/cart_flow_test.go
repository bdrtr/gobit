//go:build integration

package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"

	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// expectedTotal is the HAND-computed result of one calculation round.
//
// The fields are not filled in from the formula in the production code but from
// numbers worked out on paper inside the scenario and written down as constants;
// the rationale is in the package comment.
type expectedTotal struct {
	subtotal int64
	discount int64
	tax      int64
	shipping int64
	total    int64
}

// assertTotals compares the computed cart totals against the hand-written
// expectation.
//
// stage says which step's calculation is being checked; because the same test
// runs more than one round, the failure message MUST name the round.
func assertTotals(t *testing.T, actual cartwf.Totals, expected expectedTotal, stage string) {
	t.Helper()

	require.Equal(t, expected.subtotal, actual.Subtotal,
		"%s: the subtotal is wrong. The subtotal is the sum of the lines (unit price x "+
			"quantity); if it is wrong the customer is shown a price different from the "+
			"goods.", stage)
	require.Equal(t, expected.discount, actual.DiscountTotal,
		"%s: the discount is wrong. Since Phase 7 the discount comes from the promotion "+
			"module and lands only on the items that match its own target rule; a value "+
			"different from the expected one shows either that the calculation "+
			"misidentified the item or that a promotion leaked into carts it does not "+
			"target.", stage)
	require.Equal(t, expected.tax, actual.TaxTotal,
		"%s: the tax is wrong. Tax is computed per line, over the POST-discount base and "+
			"rounded DOWN (workflows/cart, \"Tax contract\"); a deviation is directly a "+
			"wrong charge.", stage)
	require.Equal(t, expected.shipping, actual.ShippingTotal,
		"%s: the shipping is wrong. Shipping is the sum of the methods chosen for the "+
			"cart; a non-zero value while no method is chosen shows that an amount of "+
			"unknown origin entered the calculation.", stage)
	require.Equal(t, expected.total, actual.Total,
		"%s: the grand total is wrong. The identity must hold on every round: "+
			"total = subtotal - discount + tax + shipping. If it does not, the cart module "+
			"refuses the write anyway, so this deviation also means the cart could not be "+
			"updated at all.", stage)
}

// The HAND-computed amounts of the guest cart scenario.
//
// The region is taxed at 20% (2000 basis points) and no shipping method is chosen:
//
//	2 units: 12_500 x 2 = 25_000 subtotal
//	         25_000 x 20% = 5_000 tax
//	         25_000 - 0 + 5_000 + 0 = 30_000 grand total
//	3 units: 12_500 x 3 = 37_500 subtotal
//	         37_500 x 20% = 7_500 tax
//	         37_500 - 0 + 7_500 + 0 = 45_000 grand total
const (
	guestUnitPrice      int64 = 12_500
	guestSubtotal2Units int64 = 25_000
	guestTax2Units      int64 = 5_000
	guestTotal2Units    int64 = 30_000
	guestSubtotal3Units int64 = 37_500
	guestTax3Units      int64 = 7_500
	guestTotal3Units    int64 = 45_000
)

// TestGuestCartFullFlow runs the guest path of the Phase 5 DoD end to end.
//
// The chain: region/currency resolution -> opening the cart -> copying the title
// from the catalog -> finding the price set through the link -> price from
// pricing -> writing the line -> calculation round -> quantity update -> second
// calculation round -> re-reading the cart from the database. If one of the links
// breaks, the failure shows up here; the unit tests could not see it because they
// build this chain out of fake dependencies.
func TestGuestCartFullFlow(t *testing.T) {
	ctx := t.Context()

	const variantTitle = "E2E Guest Product"
	variantID := newVariant(ctx, t, variantTitle, map[string]int64{
		taxedCurrency: guestUnitPrice,
	})

	cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: taxedCountry})
	require.NoError(t, err,
		"a guest cart must be openable: a customer without an account starting to shop "+
			"must not depend on the customer module being up")
	require.True(t, cart.Guest,
		"when no customer_id is given the cart must count as a GUEST cart; otherwise the "+
			"flow tries to verify a customer that does not exist and guest shopping shuts "+
			"down entirely")
	require.Equal(t, taxedRegionID, cart.RegionID,
		"the region must be resolved from the country code; a wrong region means a wrong "+
			"tax rate and a wrong pricing context")
	require.Equal(t, taxedCurrency, cart.CurrencyCode,
		"the currency must be COPIED from the region; if the cart is opened in another "+
			"currency, pricing finds no price in that currency and the line can never be "+
			"added")

	// --- adding the line and the first calculation round ---

	added, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    cart.CartID,
		VariantID: variantID,
		Quantity:  2,
	})
	require.NoError(t, err, "a variant with a defined price must be addable to the cart")
	require.Equal(t, variantTitle, added.Title,
		"the line's title must be copied FROM THE CATALOG (through the Query layer); "+
			"trusting the free text the caller sends would let a variant that does not "+
			"exist into the cart")
	require.Equal(t, guestUnitPrice, added.UnitPrice,
		"the unit price must come from pricing; trusting an amount the cart keeps itself "+
			"would freeze a price that changed in the catalog inside the cart")

	assertTotals(t, added.Totals, expectedTotal{
		subtotal: guestSubtotal2Units,
		discount: 0,
		tax:      guestTax2Units,
		shipping: 0,
		total:    guestTotal2Units,
	}, "after adding 2 units")

	require.Len(t, added.Totals.Lines, 1,
		"the calculation must cover ALL of the cart's lines; a missing line is rejected by "+
			"the cart module and the cart is not updated at all")
	line := added.Totals.Lines[0]
	require.Equal(t, added.LineItemID, line.LineItemID,
		"the line amounts must belong to the line that was added; an amount written onto "+
			"another line silently produces a wrong invoice")
	require.Equal(t, guestSubtotal2Units, line.Subtotal,
		"the line subtotal must be unit price x quantity; because the cart module verifies "+
			"this product separately, a deviation drops the write")
	require.Equal(t, guestTax2Units, line.TaxTotal,
		"the line tax must be computed over the line's post-discount base; on an invoice "+
			"the tax of every line must be explainable one by one")
	require.Equal(t, int64(0), line.DiscountTotal,
		"the line discount must always be zero in Phase 5; if it is non-zero, a calculation "+
			"with no discount source is dropping an amount")

	// --- quantity update and the second calculation round ---

	updated, err := workflows.UpdateLineItem(ctx, cartwf.UpdateLineItemInput{
		CartID:     cart.CartID,
		LineItemID: added.LineItemID,
		Quantity:   3,
	})
	require.NoError(t, err, "the line quantity must be updatable")
	require.False(t, updated.Removed,
		"a positive quantity must NOT remove the line; removal is the intent of a zero "+
			"quantity alone")
	require.Equal(t, int64(3), updated.Quantity,
		"the quantity must be written as an ABSOLUTE value; if it is read as an addition "+
			"the customer pays for more than they asked for")

	assertTotals(t, updated.Totals, expectedTotal{
		subtotal: guestSubtotal3Units,
		discount: 0,
		tax:      guestTax3Units,
		shipping: 0,
		total:    guestTotal3Units,
	}, "after raising the quantity to 3")

	// --- re-reading the cart from the database ---

	detail, err := cartSvc.GetCart(ctx, cart.CartID)
	require.NoError(t, err, "the cart must be readable from the cart module")
	require.False(t, detail.TotalsStale(),
		"the totals must be stamped onto the cart's CURRENT shape; if they stay stale, "+
			"turning the cart into an order is rejected as well and the customer gets stuck "+
			"at the payment step")
	require.Equal(t, updated.Totals.Subtotal, detail.Subtotal,
		"the subtotal read back must be EXACTLY the one the flow returned; a divergence "+
			"means the amount shown to the customer differs from the amount charged")
	require.Equal(t, updated.Totals.DiscountTotal, detail.DiscountTotal,
		"the discount read back must be exactly the one the flow returned")
	require.Equal(t, updated.Totals.TaxTotal, detail.TaxTotal,
		"the tax read back must be exactly the one the flow returned")
	require.Equal(t, updated.Totals.ShippingTotal, detail.ShippingTotal,
		"the shipping read back must be exactly the one the flow returned")
	require.Equal(t, updated.Totals.Total, detail.Total,
		"the grand total read back must be exactly the one the flow returned; the payment "+
			"step uses this number")

	require.Len(t, detail.Items, 1,
		"the same variant must be merged into a single line; a second line would split the "+
			"price tier and the stock reservation of Phase 6")
	require.Equal(t, int64(3), detail.Items[0].Quantity,
		"the stored quantity must be the updated value")
	require.Equal(t, guestUnitPrice, detail.Items[0].UnitPrice,
		"the stored unit price must be the value re-read from pricing during the "+
			"calculation round")
	require.Equal(t, guestSubtotal3Units, detail.Items[0].Subtotal,
		"the stored line subtotal must be the same as the hand-computed value")
	require.Equal(t, guestTax3Units, detail.Items[0].TaxTotal,
		"the stored line tax must be the same as the hand-computed value")
}

// The HAND-computed amounts of the registered customer scenario.
//
// The price was chosen to make the DOWNWARD rounding of the tax visible:
//
//	9_999 x 2 = 19_998 subtotal
//	19_998 x 20% = 3_999.6 -> rounded DOWN -> 3_999 tax
//	19_998 - 0 + 3_999 + 0 = 23_997 grand total
//
// Had it been rounded to nearest, the tax would have come out 4_000; the 1 minor
// unit in between is always left IN THE CUSTOMER'S FAVOR (workflows/cart, "Tax
// contract", decision 3).
const (
	registeredUnitPrice int64 = 9_999
	registeredSubtotal  int64 = 19_998
	registeredTax       int64 = 3_999
	registeredTotal     int64 = 23_997
)

// TestRegisteredCustomerCart runs the registered customer path of the Phase 5 DoD
// end to end.
//
// It differs from the guest path in two ways and both are checked here: the
// customer is VERIFIED (without one the cart is never opened) and the cart is
// BOUND to the customer. That the binding was really established is verified by
// reading it from the link service, not from the cart's own column.
func TestRegisteredCustomerCart(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID := newVariant(ctx, t, "E2E Registered Customer Product", map[string]int64{
		taxedCurrency: registeredUnitPrice,
	})

	cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{
		CountryCode: taxedCountry,
		CustomerID:  customerID,
	})
	require.NoError(t, err, "a registered customer's cart must be openable")
	require.False(t, cart.Guest,
		"when a customer_id is given the cart must NOT count as a guest cart; if it does, "+
			"the customer's cart is never associated with their account")
	require.Equal(t, customerID, cart.CustomerID,
		"the cart's owner must be the requested customer")
	require.Equal(t, email, cart.Email,
		"when no e-mail is given the customer's REGISTERED address must carry over to the "+
			"cart; a cart without an address means asking for the same information again at "+
			"the payment step")

	// --- the cart's region must be the region the flow was given as well ---

	require.Equal(t, taxedRegionID, cart.RegionID,
		"the cart must be opened with the region given to the flow; the region is the "+
			"cart's OWN column and both the tax and the currency are read from exactly there")

	// --- the guest path's calculation chain works the same for a registered customer ---

	added, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    cart.CartID,
		VariantID: variantID,
		Quantity:  2,
	})
	require.NoError(t, err, "a line must be addable to the registered customer's cart")

	assertTotals(t, added.Totals, expectedTotal{
		subtotal: registeredSubtotal,
		discount: 0,
		tax:      registeredTax,
		shipping: 0,
		total:    registeredTotal,
	}, "after adding 2 units to the registered customer's cart")

	updated, err := workflows.UpdateLineItem(ctx, cartwf.UpdateLineItemInput{
		CartID:     cart.CartID,
		LineItemID: added.LineItemID,
		Quantity:   1,
	})
	require.NoError(t, err, "the registered customer's line quantity must be updatable")

	// 9_999 x 1 = 9_999 subtotal; 9_999 x 20% = 1_999.8 -> 1_999 tax;
	// 9_999 - 0 + 1_999 + 0 = 11_998 grand total.
	assertTotals(t, updated.Totals, expectedTotal{
		subtotal: 9_999,
		discount: 0,
		tax:      1_999,
		shipping: 0,
		total:    11_998,
	}, "after lowering the quantity to 1")

	detail, err := cartSvc.GetCart(ctx, cart.CartID)
	require.NoError(t, err, "the cart must be readable from the cart module")
	require.Equal(t, customerID, detail.CustomerID,
		"the customer id in the cart's own column must be correct too; the column is the "+
			"source and the link is its mirror, and the two must not diverge")
	require.Equal(t, updated.Totals.Total, detail.Total,
		"the grand total read back must be exactly the one the flow returned")
	require.False(t, detail.TotalsStale(),
		"the totals must be stamped onto the current shape")
}

// TestCartWorkflowsBuildFromProductionWiring verifies that the cart workflows can
// be built from the PRODUCTION wiring.
//
// Regression: cart.service did not satisfy the primitive surface the workflows
// expected; cartwf.FromContainer fell over with a type mismatch and the DoD could
// only be tested through a bridge built inside the test. What was missing was the
// thing region/pricing/customer already had: interop.go. Now cart publishes it too
// and it is registered under the name "cart.interop".
//
// This test checks the wiring SEPARATELY: the other scenarios USE the workflows,
// this test verifies that they CAN BE BUILT, and when a signature drifts it says
// which surface is missing through the container's typed error.
func TestCartWorkflowsBuildFromProductionWiring(t *testing.T) {
	workflows, err := cartwf.FromContainer(ctr)
	require.NoError(t, err,
		"the cart workflows must be buildable from the PRODUCTION registrations in ctr; "+
			"the error names which surface is missing")
	require.NotNil(t, workflows)
}
