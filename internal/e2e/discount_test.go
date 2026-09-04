//go:build integration

package e2e

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	promotionmodels "github.com/bdrtr/gobit/internal/modules/promotion/models"
	promotionsvc "github.com/bdrtr/gobit/internal/modules/promotion/service"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// This file proves the DISCOUNT leg of the plan's Phase 7 DoD: "a discount is
// applied to the cart and the total is updated CORRECTLY".
//
// In Phase 5 the discount field of the cart total was ALWAYS ZERO and
// internal/workflows/cart left it that way with a note saying "promotion will
// take this over in Phase 7". That the takeover really happened can only be seen
// with the REAL promotion module: the cart flow's unit tests read the discount
// from a fake, and that fake shares neither promotion's percentage arithmetic,
// nor its rounding direction, nor its JSON schema.
//
// # The compiler does not check both sides of the boundary
//
// internal/workflows/cart CANNOT import promotion (ADR 0006) and the discount
// request/response travels over JSON schemas that are defined SEPARATELY in the
// two packages. promotion REJECTS unknown fields, so if a field name drifts the
// calculation falls over at run time. This file is the only proof that those
// schemas really do meet each other.

// attrVariantID is the line item attribute that the promotion's TARGET rule
// looks at.
//
// The value has to be EXACTLY the same as the attrVariantID constant inside
// internal/workflows/cart, and it is repeated here because that one is
// unexported. The repetition is deliberate: the cart flow writes this name onto
// every line itself; if the name drifts the target rule matches no line at all,
// the promotion silently produces no discount, and the "discount does not work"
// failure blows up nowhere.
const attrVariantID = "variant_id"

// The MANUALLY computed amounts of the automatic promotion scenario.
//
// Setup: a 10% (1000 basis points) PERCENTAGE discount, target "items",
// allocation "each"; the region is taxed at 20% (2000 basis points) and no
// shipping method has been chosen.
//
//	A: 12_345 x 2 = 24_690 subtotal
//	   discount 24_690 x 10% = 2_469.0 -> 2_469
//	   tax base 24_690 - 2_469 = 22_221
//	   tax 22_221 x 20% = 4_444.2 -> 4_444
//
//	B:  7_777 x 1 =  7_777 subtotal
//	   discount  7_777 x 10% =   777.7 ->   777
//	   tax base  7_777 -   777 =  7_000
//	   tax  7_000 x 20% = 1_400.0 -> 1_400
//
//	subtotal = 24_690 +  7_777 = 32_467
//	discount =  2_469 +    777 =  3_246
//	tax      =  4_444 +  1_400 =  5_844
//	total    = 32_467 - 3_246 + 5_844 + 0 = 35_065
//
// Both the discount and the tax are rounded PER LINE and DOWN. That both
// directions are down does not favor the same side: a discount rounded down
// favors the SELLER, a tax rounded down favors the CUSTOMER (workflows/cart,
// assembleTotals).
const (
	promoRateBps int64 = 1_000

	promoPriceA    int64 = 12_345
	promoQuantityA int64 = 2
	promoPriceB    int64 = 7_777
	promoQuantityB int64 = 1

	promoSubtotalA int64 = 24_690
	promoSubtotalB int64 = 7_777

	promoLineDiscountA int64 = 2_469
	promoLineDiscountB int64 = 777

	promoTaxA int64 = 4_444
	promoTaxB int64 = 1_400

	promoSubtotal int64 = 32_467
	promoDiscount int64 = 3_246
	promoTax      int64 = 5_844
	promoTotal    int64 = 35_065
)

// The MANUALLY computed amounts of the SAME cart WITHOUT the discount.
//
// The prices and the quantities are the same as above; the only difference is
// that the variants DO NOT FALL INTO the promotion's target rule:
//
//	A': 24_690 x 20% = 4_938.0 -> 4_938
//	B':  7_777 x 20% = 1_555.4 -> 1_555
//	tax = 6_493, total = 32_467 - 0 + 6_493 + 0 = 38_960
//
// The tax difference can be explained line by line:
//
//	A: 4_938 - 4_444 =   494
//	B: 1_555 - 1_400 =   155
//	                   -----
//	                     649
//
// The difference is the direct measure of the tax base being POST-DISCOUNT. Had
// the base been PRE-DISCOUNT the two carts would have come out with identical
// tax and the customer would also have paid the tax of the 3_246 units they
// never paid for.
const (
	noPromoTaxA  int64 = 4_938
	noPromoTaxB  int64 = 1_555
	noPromoTax   int64 = 6_493
	noPromoTotal int64 = 38_960

	// promoTaxDeltaA and promoTaxDeltaB are the per-line tax difference.
	promoTaxDeltaA int64 = 494
	promoTaxDeltaB int64 = 155
	// promoTaxDelta is the total difference between the tax of the two carts.
	promoTaxDelta int64 = 649
)

// TestAutomaticPromotionLowersCartTotal runs the discount leg of the Phase 7 DoD
// end to end.
//
// The chain: promotion + application method + target rule -> product/variant/price
// -> cart -> lines -> calculation. The discount leg of the calculation happens in
// the promotion module, the tax leg in the tax module, and the assembly in the
// cart flow; all three are REAL.
//
// Four things are checked and each one catches a separate failure: the AMOUNT of
// the discount (percentage arithmetic and rounding), the Σ identity (what is
// written onto the lines not drifting from what is written onto the cart), the
// BASE of the tax (post-discount) and the grand total identity.
func TestAutomaticPromotionLowersCartTotal(t *testing.T) {
	ctx := t.Context()

	variantA := newVariant(ctx, t, "E2E Discounted A", map[string]int64{
		taxedCurrency: promoPriceA,
	})
	variantB := newVariant(ctx, t, "E2E Discounted B", map[string]int64{
		taxedCurrency: promoPriceB,
	})

	promotionID := newAutomaticPercentagePromotion(ctx, t, "E2E-AUTOMATIC-10", promoRateBps,
		[]string{variantA, variantB})

	cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: taxedCountry})
	require.NoError(t, err, "the cart must open")

	lineA, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: cart.CartID, VariantID: variantA, Quantity: promoQuantityA,
	})
	require.NoError(t, err, "line A must be addable")
	result, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: cart.CartID, VariantID: variantB, Quantity: promoQuantityB,
	})
	require.NoError(t, err, "line B must be addable")

	// --- 1) are the cart's totals the same as the manually computed values ---

	assertTotals(t, result.Totals, expectedTotal{
		subtotal: promoSubtotal,
		discount: promoDiscount,
		tax:      promoTax,
		shipping: 0,
		total:    promoTotal,
	}, "in the cart with the automatic promotion")

	require.Equal(t, cartwf.TaxSourceTax, result.Totals.TaxSource,
		"the TAX module must have computed the tax. Had the source been %q the "+
			"calculation would have fallen back to Phase 5's region rate and this "+
			"scenario's tax claim would prove that the takeover was NOT done rather than "+
			"that it was",
		cartwf.TaxSourceRegion)

	// --- 2) is Σ(line discount) EQUAL to the cart's discount ---

	lines := lineTotalsByID(t, result.Totals)
	var discountSum int64
	for i := range result.Totals.Lines {
		discountSum += result.Totals.Lines[i].DiscountTotal
	}
	require.Equal(t, discountSum, result.Totals.DiscountTotal,
		"Σ(line discount) must be EQUAL to the cart's discount. The cart discount is not "+
			"an independent calculation, it is the sum of the line discounts; if they "+
			"drift apart the discount shown to the customer differs from the discount "+
			"written on the lines and the invoice cannot be explained line by line")
	require.Equal(t, promoDiscount, discountSum,
		"the sum of the line discounts must equal the manually computed 3_246")

	// --- 3) per-line discount, tax and total ---

	for _, expected := range []struct {
		name     string
		id       string
		subtotal int64
		discount int64
		tax      int64
	}{
		{"A", lineA.LineItemID, promoSubtotalA, promoLineDiscountA, promoTaxA},
		{"B", result.LineItemID, promoSubtotalB, promoLineDiscountB, promoTaxB},
	} {
		line, found := lines[expected.id]
		require.True(t, found, "the amounts of line %s must be present in the calculation", expected.name)
		require.Equal(t, expected.subtotal, line.Subtotal,
			"the subtotal of line %s must be unit price x quantity", expected.name)
		require.Equal(t, expected.discount, line.DiscountTotal,
			"the discount of line %s must be 10%% of the line's OWN subtotal (rounded "+
				"down). Had it been computed over the cart total and then distributed, the "+
				"leftover cent would drift onto another line and the line's discount would "+
				"differ from what its own rate says", expected.name)
		require.Equal(t, expected.tax, line.TaxTotal,
			"the tax of line %s must be computed over the POST-DISCOUNT base", expected.name)
		require.Equal(t, expected.subtotal-expected.discount+expected.tax, line.Total,
			"the total of line %s must be subtotal - discount + tax", expected.name)
		require.LessOrEqual(t, line.DiscountTotal, line.Subtotal,
			"the discount of line %s must NEVER exceed its subtotal; if it does the line "+
				"has been sold at a negative price and the cart module rejects the whole "+
				"calculation", expected.name)
	}

	// --- 4) was the calculation REALLY written onto the cart ---

	detail, err := cartSvc.GetCart(ctx, cart.CartID)
	require.NoError(t, err, "it must be readable from the cart module")
	require.Equal(t, promoDiscount, detail.DiscountTotal,
		"the discount must have been WRITTEN onto the cart. Had the amount the flow "+
			"returned been right while the one written onto the cart was wrong, the "+
			"customer would pay the undiscounted amount, because the Phase 6 saga that "+
			"creates the order reads the cart's stored total")
	require.Equal(t, promoTotal, detail.Total,
		"the stored grand total must equal the manually computed value")
	require.True(t, detail.TotalsConsistent(),
		"the cart must satisfy the total identity: total = subtotal - discount + tax + shipping")
	require.False(t, detail.TotalsStale(),
		"the totals must be stamped onto the cart's current shape")

	// --- 5) is the TAX BASE post-discount: compare with the same cart undiscounted ---

	controlVariantA := newVariant(ctx, t, "E2E Undiscounted A", map[string]int64{
		taxedCurrency: promoPriceA,
	})
	controlVariantB := newVariant(ctx, t, "E2E Undiscounted B", map[string]int64{
		taxedCurrency: promoPriceB,
	})

	controlCart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: taxedCountry})
	require.NoError(t, err, "the control cart must open")
	controlLineA, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: controlCart.CartID, VariantID: controlVariantA, Quantity: promoQuantityA,
	})
	require.NoError(t, err, "line A must be addable to the control cart")
	controlResult, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: controlCart.CartID, VariantID: controlVariantB, Quantity: promoQuantityB,
	})
	require.NoError(t, err, "line B must be addable to the control cart")

	assertTotals(t, controlResult.Totals, expectedTotal{
		subtotal: promoSubtotal,
		discount: 0,
		tax:      noPromoTax,
		shipping: 0,
		total:    noPromoTotal,
	}, "in the control cart built from variants the promotion does not target")

	require.Equal(t, promoSubtotal, controlResult.Totals.Subtotal,
		"the control cart's subtotal must be the SAME as the discounted cart's; were it "+
			"not the same the tax difference would come from the price rather than from "+
			"the rate and the comparison would prove nothing")
	require.Zero(t, controlResult.Totals.DiscountTotal,
		"the promotion's TARGET rule must select only its own variants. A discount other "+
			"than zero shows that an automatic promotion has leaked into every cart and "+
			"that the amounts of the other scenarios are broken too")

	require.NotEqual(t, controlResult.Totals.TaxTotal, result.Totals.TaxTotal,
		"the tax of the discounted and of the undiscounted cart must be DIFFERENT. Were "+
			"they equal the tax base would be the PRE-discount amount and the customer "+
			"would pay the tax of money they never paid")
	require.Equal(t, promoTaxDelta, controlResult.Totals.TaxTotal-result.Totals.TaxTotal,
		"the tax difference must be the manually computed 649: %d on line A, %d on line B",
		promoTaxDeltaA, promoTaxDeltaB)

	controlLines := lineTotalsByID(t, controlResult.Totals)
	require.Equal(t, promoTaxDeltaA,
		controlLines[controlLineA.LineItemID].TaxTotal-lines[lineA.LineItemID].TaxTotal,
		"the tax difference of line A must be %d: 4_938 - 4_444. The difference must be "+
			"explainable line by line; holding only at the cart level could hide the base "+
			"being right on one line and wrong on another", promoTaxDeltaA)
	require.Equal(t, promoTaxDeltaB,
		controlLines[controlResult.LineItemID].TaxTotal-lines[result.LineItemID].TaxTotal,
		"the tax difference of line B must be %d: 1_555 - 1_400", promoTaxDeltaB)

	require.Equal(t, promoDiscount+promoTaxDelta, noPromoTotal-promoTotal,
		"the difference between the grand totals of the two carts must be the discount "+
			"ITSELF plus the tax that discount removed. This is the money that stays in "+
			"the customer's pocket and both of its components must be computable by hand")

	// --- 6) did the calculation leave the coupon COUNTER unspent ---

	promotion, err := promotionSvc.GetPromotion(ctx, promotionID)
	require.NoError(t, err, "the promotion must be readable from the promotion module")
	require.Zero(t, promotion.UsageCount,
		"the cart calculation must NOT spend the promotion's usage counter. The cart is "+
			"recalculated every time it changes; every calculation spending a coupon would "+
			"make looking at the cart and using the coupon the same thing. The only path "+
			"that spends the counter is RedeemPromotion, and the order calls it")
}

// newAutomaticPercentagePromotion sets up an AUTOMATIC percentage promotion that
// targets specific variants and returns its ID.
//
// # Why the target rule is MANDATORY
//
// An automatic promotion is applied without a code, so if no rule is set it lands
// on EVERY cart that shares the same database. The tests run one after another on
// a single Postgres instance; a promotion without a rule would silently lower the
// hand-written amounts of every scenario that runs after it, and the failure would
// show up not in the test that set the promotion up but in a completely different
// test.
//
// The rule looks at the line's variant ([attrVariantID]) because that is the only
// catalog fact the cart flow reports to promotion about the line. Since every
// scenario creates its own variants, this confines the promotion to the inside of
// the scenario.
func newAutomaticPercentagePromotion(
	ctx context.Context,
	t *testing.T,
	code string,
	rateBps int64,
	variantIDs []string,
) string {
	t.Helper()

	promotion, err := promotionSvc.CreatePromotion(ctx, promotionsvc.PromotionInput{
		Code:        code,
		IsAutomatic: true,
		Status:      promotionmodels.PromotionActive,
	})
	require.NoError(t, err, "the fixture promotion could not be created")
	require.True(t, promotion.IsAutomatic,
		"the promotion must be AUTOMATIC; a promotion that asks for a coupon code would "+
			"never reach the cart flow, because the flow SENDS no code (see cartwf "+
			"discountRequestFor)")

	_, err = promotionSvc.SetApplicationMethod(ctx, promotion.ID, promotionsvc.ApplicationMethodInput{
		Type:       promotionmodels.MethodPercentage,
		TargetType: promotionmodels.TargetItems,
		Allocation: promotionmodels.AllocationEach,
		Value:      rateBps,
	})
	require.NoError(t, err, "the fixture promotion's application method could not be written")

	_, err = promotionSvc.AddPromotionRule(ctx, promotion.ID, promotionsvc.RuleInput{
		RuleType:  promotionmodels.RuleTarget,
		Attribute: attrVariantID,
		Operator:  promotionmodels.OpIn,
		Values:    variantIDs,
	})
	require.NoError(t, err, "the fixture promotion's target rule could not be written")

	return promotion.ID
}

// lineTotalsByID maps the computed lines BY ID.
//
// The mapping rests on the ID rather than on the order: an assertion that rests on
// the order would verify the wrong line once the line order changes later, and the
// test would stay green while the assertion lost its meaning.
func lineTotalsByID(t *testing.T, totals cartwf.Totals) map[string]cartwf.LineTotals {
	t.Helper()

	out := make(map[string]cartwf.LineTotals, len(totals.Lines))
	for i := range totals.Lines {
		out[totals.Lines[i].LineItemID] = totals.Lines[i]
	}
	require.Len(t, out, len(totals.Lines),
		"every line must come back with a UNIQUE ID; a repeated ID would blur which line "+
			"the discount and the tax belong to")
	return out
}
