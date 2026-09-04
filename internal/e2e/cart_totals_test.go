//go:build integration

package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	cartsvc "github.com/bdrtr/gobit/internal/modules/cart/service"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// The HAND-computed amounts of the multi-line cart scenario.
//
// The region is taxed at 20% (2000 basis points). The prices were chosen at a
// point where per-line rounding DIVERGES from rounding once at the cart level:
//
//	A: 3_333 x 1 =  3_333 subtotal; 3_333 x 20% =   666.6 ->   666 tax
//	B: 6_667 x 1 =  6_667 subtotal; 6_667 x 20% = 1_333.4 -> 1_333 tax
//	C: 10_000 x 2 = 20_000 subtotal; 20_000 x 20% = 4_000.0 -> 4_000 tax
//
//	Σ subtotal = 3_333 + 6_667 + 20_000 = 30_000
//	Σ line tax =   666 + 1_333 +  4_000 =  5_999
//	grand total = 30_000 - 0 + 5_999 + 0 = 35_999
//
// # WHERE the rounding difference stays
//
// Had the cart subtotal been taxed in one go, 30_000 x 20% = 6_000 would have come
// out. The contract computes tax PER LINE and rounds DOWN on every line; because
// the 0.6 and 0.4 fractions on lines A and B are dropped separately, the cart's tax
// stays at 5_999. The 1 minor unit in between is IN THE CUSTOMER'S FAVOR and is not
// lost: it is written to no line, so it does not show up on the invoice either. The
// per-line computation is a deliberate choice (workflows/cart, "Tax contract",
// decision 2): on an invoice the tax of every line must be explainable one by one,
// and when DIFFERENT per-line rates arrive in Phase 7 the definition of the base
// must not change.
const (
	multiLinePriceA int64 = 3_333
	multiLinePriceB int64 = 6_667
	multiLinePriceC int64 = 10_000

	multiLineSubtotalA int64 = 3_333
	multiLineSubtotalB int64 = 6_667
	multiLineSubtotalC int64 = 20_000

	multiLineTaxA int64 = 666
	multiLineTaxB int64 = 1_333
	multiLineTaxC int64 = 4_000

	multiLineSubtotal int64 = 30_000
	multiLineTax      int64 = 5_999
	multiLineTotal    int64 = 35_999

	// multiLineCartLevelTax is the amount that would come out had the cart
	// subtotal been taxed IN ONE GO; the contract does not take that path and
	// the test documents where the difference stays with this constant.
	multiLineCartLevelTax int64 = 6_000
)

// TestMultiLineCartTotalsAreConsistent verifies that on three lines with different
// prices the cart totals are consistent with the line totals.
//
// Two claims are tested: Σ(line subtotal) EQUALS the cart's subtotal (the cart
// module verifies this separately and if it does not hold nothing is written) and
// Σ(line tax) EQUALS the cart's tax — the cart's tax is by definition the sum of
// the line taxes, not an independent computation.
func TestMultiLineCartTotalsAreConsistent(t *testing.T) {
	ctx := t.Context()

	variantA := newVariant(ctx, t, "E2E Multi Line A", map[string]int64{taxedCurrency: multiLinePriceA})
	variantB := newVariant(ctx, t, "E2E Multi Line B", map[string]int64{taxedCurrency: multiLinePriceB})
	variantC := newVariant(ctx, t, "E2E Multi Line C", map[string]int64{taxedCurrency: multiLinePriceC})

	cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: taxedCountry})
	require.NoError(t, err, "the cart must be openable")

	lineA, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: cart.CartID, VariantID: variantA, Quantity: 1,
	})
	require.NoError(t, err, "line A must be addable")
	lineB, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: cart.CartID, VariantID: variantB, Quantity: 1,
	})
	require.NoError(t, err, "line B must be addable")
	result, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: cart.CartID, VariantID: variantC, Quantity: 2,
	})
	require.NoError(t, err, "line C must be addable")

	assertTotals(t, result.Totals, expectedTotal{
		subtotal: multiLineSubtotal,
		discount: 0,
		tax:      multiLineTax,
		shipping: 0,
		total:    multiLineTotal,
	}, "after adding three lines")

	require.Len(t, result.Totals.Lines, 3,
		"the computation must cover ALL THREE lines of the cart; a missing line is rejected by the cart module")

	// Lines are matched by identity: an order-dependent claim would have verified
	// the wrong line should the line order change in the future.
	lines := make(map[string]cartwf.LineTotals, len(result.Totals.Lines))
	var subtotalSum, taxSum int64
	for _, line := range result.Totals.Lines {
		lines[line.LineItemID] = line
		subtotalSum += line.Subtotal
		taxSum += line.TaxTotal
	}

	require.Equal(t, subtotalSum, result.Totals.Subtotal,
		"Σ(line subtotal) must EQUAL the cart's subtotal; if it does not, the cart module "+
			"writes no computation at all and the cart's totals stay stale")
	require.Equal(t, multiLineSubtotal, subtotalSum,
		"the lines' subtotals must equal the hand-computed 30_000")
	require.Equal(t, taxSum, result.Totals.TaxTotal,
		"Σ(line tax) must EQUAL the cart's tax; the cart's tax is not an independent "+
			"computation but the sum of the line taxes")

	// The hand-computed per-line amounts.
	for _, expected := range []struct {
		name     string
		id       string
		unit     int64
		subtotal int64
		tax      int64
	}{
		{"A", lineA.LineItemID, multiLinePriceA, multiLineSubtotalA, multiLineTaxA},
		{"B", lineB.LineItemID, multiLinePriceB, multiLineSubtotalB, multiLineTaxB},
		{"C", result.LineItemID, multiLinePriceC, multiLineSubtotalC, multiLineTaxC},
	} {
		line, found := lines[expected.id]
		require.True(t, found, "the amounts of line %s must be present in the computation", expected.name)
		require.Equal(t, expected.unit, line.UnitPrice,
			"the unit price of line %s must be the price pricing chose; every line is priced "+
				"from its OWN price set and if the sets get mixed up the wrong product is charged",
			expected.name)
		require.Equal(t, expected.subtotal, line.Subtotal,
			"the subtotal of line %s must equal the hand-computed value", expected.name)
		require.Equal(t, expected.tax, line.TaxTotal,
			"the tax of line %s must be computed over its own base, rounded DOWN",
			expected.name)
		require.Equal(t, expected.subtotal+expected.tax, line.Total,
			"the total of line %s must be subtotal - discount + tax", expected.name)
	}

	// WHERE the rounding difference stays is documented explicitly.
	require.Equal(t, int64(1), multiLineCartLevelTax-result.Totals.TaxTotal,
		"the difference between per-line rounding and rounding once at the cart level must be "+
			"exactly 1 minor unit: the 0.6 fraction on line A and the 0.4 fraction on line B are "+
			"dropped DOWN separately. The difference is written to no line, that is, it does not "+
			"show up on the invoice and is not collected from the customer")
	require.Less(t, result.Totals.TaxTotal, multiLineCartLevelTax,
		"rounding must always be IN THE CUSTOMER'S FAVOR; round-half-up overcharges the customer "+
			"and would leave the question \"where did the excess come from\" to reconciliation")

	detail, err := cartSvc.GetCart(ctx, cart.CartID)
	require.NoError(t, err, "it must be readable from the cart module")
	require.Len(t, detail.Items, 3, "three separate variants must be three separate lines")
	require.Equal(t, multiLineTotal, detail.Total,
		"the stored grand total must equal the hand-computed value")
	require.False(t, detail.TotalsStale(), "the totals must be stamped against the current shape")
}

// The HAND-computed amounts of the untaxed-region scenario.
//
//	5_000 x 2 = 10_000 subtotal
//	tax 0 (the country has NO tax region in the tax module; the 19% rate the
//	       region carries is not applied either)
//	10_000 - 0 + 0 + 0 = 10_000 grand total
const (
	untaxedUnitPrice int64 = 5_000
	untaxedSubtotal  int64 = 10_000
	untaxedTotal     int64 = 10_000
)

// TestTaxIsZeroInUntaxedRegion verifies that in a region with no tax the cart
// carries zero tax.
//
// # In Phase 7 the REASON the tax stays zero changed
//
// In Phase 5 the reason was the region's flag: automatic_taxes was off and the flow
// listened to it. In Phase 7 the tax module took tax over and no tax region was set
// up for this country ([setUpTaxFixtures]); tax gives an AUTHORITATIVE answer of
// "not configured" and there is no falling back to region.
//
// The two reasons produce the SAME amount, that is, the cart's total does not say
// which one is in force. The only thing that tells them apart is the
// [cartwf.Totals.TaxSource] field and this scenario tests exactly that: without the
// field, the handover never having run in this region could hide behind the same
// numbers just as well.
//
// That the region still carries a NON-zero rate ([untaxedRateBps]) is not a
// leftover: the region path was NOT deleted, it stands there as the fallback path,
// and had that path been taken the tax would have a visible value.
func TestTaxIsZeroInUntaxedRegion(t *testing.T) {
	ctx := t.Context()

	rate, automatic, err := regionSvc.RegionTax(ctx, untaxedRegionID)
	require.NoError(t, err, "the region's tax setting must be readable")
	require.False(t, automatic,
		"the fixture region must keep automatic tax OFF; in Phase 5 this was the reason the "+
			"tax came out zero and the fixture is preserved in that shape")
	require.Equal(t, untaxedRateBps, rate,
		"the fixture region must carry a NON-zero rate; the tax coming out zero must not come "+
			"from the rate being small")

	_, found, err := taxInterop.RateForCountry(ctx, untaxedCountry)
	require.NoError(t, err, "the rate must be queryable from the tax surface")
	require.False(t, found,
		"country %s must have NO tax region in the tax module; the zero of this scenario "+
			"comes from the absence of configuration", untaxedCountry)

	variantID := newVariant(ctx, t, "E2E Untaxed Product", map[string]int64{
		untaxedCurrency: untaxedUnitPrice,
	})

	cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: untaxedCountry})
	require.NoError(t, err, "a cart must be openable in the untaxed region")
	require.Equal(t, untaxedRegionID, cart.RegionID,
		"the cart must be bound to the untaxed region")
	require.Equal(t, untaxedCurrency, cart.CurrencyCode,
		"the cart's currency must be the same as the untaxed region's; were it different no "+
			"price would be found at all and the test would be testing the absence of the price, not of the tax")

	added, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    cart.CartID,
		VariantID: variantID,
		Quantity:  2,
	})
	require.NoError(t, err, "a line must be addable in the untaxed region too")

	assertTotals(t, added.Totals, expectedTotal{
		subtotal: untaxedSubtotal,
		discount: 0,
		tax:      0,
		shipping: 0,
		total:    untaxedTotal,
	}, "after adding 2 units in the untaxed region")

	require.Equal(t, cartwf.TaxSourceTaxUnconfigured, added.Totals.TaxSource,
		"the TAX module must have computed the tax and reported that the country is NOT "+
			"CONFIGURED. Had the source come out %q the computation would have fallen back "+
			"to the region path; since the amount would be zero either way the difference "+
			"shows up in no other claim, that is, the handover never having run in this "+
			"region would pass silently",
		cartwf.TaxSourceRegion)

	require.Len(t, added.Totals.Lines, 1, "a single line is expected")
	require.Equal(t, int64(0), added.Totals.Lines[0].TaxTotal,
		"the line tax must be zero too; since the cart's tax is the sum of the line taxes, "+
			"a tax left on the line would show up on the cart as well")
	require.Equal(t, untaxedSubtotal, added.Totals.Lines[0].Total,
		"the total of the untaxed line must equal its subtotal")
}

// TestUnpricedVariantCannotEnterCart verifies that a variant with no price is
// rejected.
//
// The expected decision is written in the package godoc (workflows/cart,
// priceSetsFor): the request is REJECTED with errors.Invalid. It is NOT NotFound,
// because the variant EXISTS; what is missing is its being sellable and the caller
// can fix the request. Were it allowed, a line with a zero unit price would be
// opened and the cart would silently get cheaper — this silent loss of money is
// exactly what the cart module's totals contract tries to close.
func TestUnpricedVariantCannotEnterCart(t *testing.T) {
	ctx := t.Context()

	// The price set is NEVER set up: the variant has no "product_variant_price_set"
	// link.
	unpricedVariant := newVariant(ctx, t, "E2E Unpriced Product", nil)

	cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: taxedCountry})
	require.NoError(t, err, "the cart must be openable")

	_, err = workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    cart.CartID,
		VariantID: unpricedVariant,
		Quantity:  1,
	})
	require.Error(t, err,
		"a variant with no price must NOT ENTER the cart; were it to, a line with a zero "+
			"unit price would be opened and the cart would silently get cheaper")
	require.True(t, errors.IsInvalid(err),
		"the error must be errors.Invalid (422): the variant EXISTS, what is missing is its "+
			"being sellable and the caller can fix the request. Had it been NotFound (404) the "+
			"client would take a fixable situation for a lost one. Got: %v", err)
	require.Equal(t, cartwf.CodeVariantNotPriced, errors.CodeOf(err),
		"the error code must be %q; clients branch on the code, not on the message",
		cartwf.CodeVariantNotPriced)

	detail, err := cartSvc.GetCart(ctx, cart.CartID)
	require.NoError(t, err, "the cart must be readable after the rejected request too")
	require.Empty(t, detail.Items,
		"a rejected request must NOT TOUCH the cart; a half-written line would leave a "+
			"product the customer never approved in the cart")
	require.Equal(t, int64(0), detail.Total,
		"the total of an untouched cart must stay zero")
}

// TestVariantWithoutPriceInCartCurrencyIsRejected tests a variant that has a price
// set but no price in the cart's currency.
//
// This is a SEPARATE situation from the unpriced variant and the flow reports the
// two with separate codes: here the link exists and is read, what is missing is only
// the price in that currency. The distinction has to be tested, because reduced to a
// single code "the product has no price" and "it is not sold in this country" would
// look the same.
func TestVariantWithoutPriceInCartCurrencyIsRejected(t *testing.T) {
	ctx := t.Context()

	// The price is defined only in USD; the cart, on the other hand, will be opened
	// in the TRY currency.
	wrongCurrencyVariant := newVariant(ctx, t, "E2E Product Priced Only in USD",
		map[string]int64{"USD": 4_200})

	cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: taxedCountry})
	require.NoError(t, err, "the cart must be openable")

	_, err = workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    cart.CartID,
		VariantID: wrongCurrencyVariant,
		Quantity:  1,
	})
	require.Error(t, err,
		"a variant with no price in the cart's currency must not enter the cart")
	require.True(t, errors.IsInvalid(err),
		"the error must be errors.Invalid; pricing's NotFound is reclassified here, because "+
			"what is missing is not the variant but the price in that currency. Got: %v", err)
	require.Equal(t, cartwf.CodePriceUnavailable, errors.CodeOf(err),
		"the error code must be %q and must stay SEPARATE from the unpriced variant's code "+
			"(%q); the two call for different fixes",
		cartwf.CodePriceUnavailable, cartwf.CodeVariantNotPriced)
}

// TestShippingDoesNotEnterTaxBase tests the shipping path end to end.
//
// It was a coverage gap: none of the five scenarios added a shipping method, that
// is, ShippingTotal was expected to be 0 on every round and the shipping computation
// never ran on the REAL stack. workflows/cart's decision under the "Tax contract"
// heading (shipping does NOT ENTER the tax base), the shipping being read from the
// cart_shipping_methods table and carried into the snapshot, and cart.SetTotals's
// identity check against the shipping_total column — all three were tested only in
// unit tests, over a fake snapshot.
func TestShippingDoesNotEnterTaxBase(t *testing.T) {
	ctx := t.Context()

	const unitPrice int64 = 10_000
	variantID := newVariant(ctx, t, "Shipped product", map[string]int64{taxedCurrency: unitPrice})

	cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{
		CountryCode: taxedCountry,
		Email:       "shipping@example.test",
	})
	require.NoError(t, err)

	_, err = workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    cart.CartID,
		VariantID: variantID,
		Quantity:  1,
	})
	require.NoError(t, err)

	// The shipping method is added from the cart service; the flow must see it in
	// the snapshot.
	const shippingAmount int64 = 4_990
	_, err = cartSvc.AddShippingMethod(ctx, cart.CartID, cartsvc.AddShippingMethodInput{
		Name:   "Standard shipping",
		Amount: shippingAmount,
	})
	require.NoError(t, err, "the shipping method must be addable")

	totals, err := workflows.CalculateTotals(ctx, cart.CartID)
	require.NoError(t, err)

	// The expectations are computed BY HAND; the production formula is not repeated.
	//   subtotal = 10,000
	//   tax base = subtotal (shipping EXCLUDED) -> 10,000 x 20% = 2,000
	//   total = 10,000 - 0 + 2,000 + 4,990 = 16,990
	assertTotals(t, totals, expectedTotal{
		subtotal: 10_000,
		discount: 0,
		tax:      2_000,
		shipping: 4_990,
		total:    16_990,
	}, "after adding shipping")

	// The REAL claim of the contract: had shipping been taxed the tax would be 2,998
	// ((10,000 + 4,990) x 20%). This separate claim documents that the 2,000 above is
	// that value BY DECISION, not by accident.
	const taxIfShippingWereTaxed int64 = 2_998
	require.NotEqual(t, taxIfShippingWereTaxed, totals.TaxTotal,
		"shipping must NOT ENTER the tax base; were it to, the tax would be %d", taxIfShippingWereTaxed)

	// The total read from the cart must be exactly the same as what the flow returned.
	detail, err := cartSvc.GetCart(ctx, cart.CartID)
	require.NoError(t, err)
	require.Equal(t, totals.ShippingTotal, detail.ShippingTotal,
		"the shipping amount must be written to the database; SetTotals's identity check rests on it")
	require.Equal(t, totals.Total, detail.Total)
}
