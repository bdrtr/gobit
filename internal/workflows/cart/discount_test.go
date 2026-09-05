package cart

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// twoLineCart produces a two-line snapshot (A: 2 units, B: 3 units).
//
// The subtotals are 2000 and 750; most of the tests speak in terms of these two
// numbers.
func twoLineCart(revision int64) Snapshot {
	return snapshotOf(revision, []SnapshotItem{
		{ID: testLineA, VariantID: testVariantA, Quantity: 2},
		{ID: testLineB, VariantID: testVariantB, Quantity: 3},
	}, nil)
}

// TestCalculateTotalsDiscountWrittenOntoLinesAndCart verifies that the per-item
// discount promotion gives is written onto both the lines and the cart.
func TestCalculateTotalsDiscountWrittenOntoLinesAndCart(t *testing.T) {
	h := newModuleHarness(t)
	h.discounts.perLine = map[string]int64{testLineA: 500, testLineB: 100}
	serveSnapshot(h.carts, twoLineCart(1))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	require.Len(t, totals.Lines, 2)
	assert.Equal(t, int64(500), totals.Lines[0].DiscountTotal)
	assert.Equal(t, int64(100), totals.Lines[1].DiscountTotal)
	assert.Equal(t, int64(600), totals.DiscountTotal)
	assert.Equal(t, int64(2750), totals.Subtotal)

	// Tax comes off the POST-DISCOUNT base: (2000-500) x 20% = 300, (750-100) x 20% = 130.
	assert.Equal(t, int64(300), totals.Lines[0].TaxTotal)
	assert.Equal(t, int64(130), totals.Lines[1].TaxTotal)
	assert.Equal(t, int64(430), totals.TaxTotal)
	assert.Equal(t, int64(2580), totals.Total, "2750 - 600 + 430")
	requireIdentity(t, totals)
}

// TestCalculateTotalsTaxBaseIsPostDiscount proves in a DISCRIMINATING way that the
// base is not the PRE-DISCOUNT amount.
//
// Had the pre-discount base been taxed the tax would have come out 400; when the
// post-discount base is taxed it is 300. The difference between the two numbers
// shows on its own which branch of the contract was applied.
func TestCalculateTotalsTaxBaseIsPostDiscount(t *testing.T) {
	h := newModuleHarness(t)
	h.discounts.perLine = map[string]int64{testLineA: 500}
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 2}}, nil))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	require.Len(t, h.taxes.requests, 1)
	require.Len(t, h.taxes.requests[0].Items, 1)
	assert.Equal(t, int64(1500), h.taxes.requests[0].Items[0].Amount,
		"the tax base is the line subtotal MINUS the line discount")

	assert.Equal(t, int64(300), totals.TaxTotal)
	assert.NotEqual(t, int64(400), totals.TaxTotal, "the pre-discount base must not be taxed")
	requireIdentity(t, totals)
}

// TestCalculateTotalsDiscountZeroWhenPromotionUnregistered verifies that the
// calculation DOES NOT FALL OVER while the promotion surface is not registered and
// that the discount stays zero.
func TestCalculateTotalsDiscountZeroWhenPromotionUnregistered(t *testing.T) {
	h := newHarnessWith(t, nil, newStubTaxes())
	serveSnapshot(h.carts, twoLineCart(1))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	assert.Zero(t, totals.DiscountTotal, "with no promotion there is no source to produce a discount either")
	for _, line := range totals.Lines {
		assert.Zero(t, line.DiscountTotal)
	}
	// The storefront keeps working: the tax and the total have been calculated.
	assert.Equal(t, int64(550), totals.TaxTotal)
	assert.Equal(t, int64(3300), totals.Total)
	requireIdentity(t, totals)
}

// TestCalculateTotalsDiscountRequestShape verifies that the body going to promotion
// obeys the contract.
//
// Inspecting the body field by field is necessary: the other side REJECTS an
// unknown field, but silently counts a missing one as zero, and the silent case,
// left untested, shows up in production as a cart with no discount.
func TestCalculateTotalsDiscountRequestShape(t *testing.T) {
	h := newModuleHarness(t)
	serveSnapshot(h.carts, twoLineCart(1))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	require.Len(t, h.discounts.requests, 1)
	req := h.discounts.requests[0]
	assert.Equal(t, testCurrency, req.CurrencyCode)
	assert.Equal(t, map[string]string{attrRegionID: testRegionID}, req.Context)
	require.Len(t, req.Items, 2)
	assert.Equal(t, discountRequestItem{
		ID:         testLineA,
		Amount:     2000,
		Quantity:   2,
		Attributes: map[string]string{attrVariantID: testVariantA},
	}, req.Items[0], "the item amount is the PRE-DISCOUNT subtotal and the quantity is carried for tiering")
	assert.Equal(t, testLineB, req.Items[1].ID, "the order is the cart's order")
}

// TestCalculateTotalsNoCouponCodeIsSent PINS the fact that no code at all is sent
// because the cart has no coupon field.
//
// The decision is under the "Coupon codes" heading in the package comment: only
// AUTOMATIC promotions are applied. The test is the guard of that decision — if
// someone adds a code parameter to CalculateTotals this test falls over and the
// decision has to be taken again.
func TestCalculateTotalsNoCouponCodeIsSent(t *testing.T) {
	h := newModuleHarness(t)
	serveSnapshot(h.carts, twoLineCart(1))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	require.Len(t, h.discounts.requests, 1)
	assert.Empty(t, h.discounts.requests[0].Codes)
	assert.Empty(t, h.discounts.requests[0].At, "the instant of the calculation is ALWAYS now")
}

// TestCalculateTotalsShippingIsNotSentToDiscount verifies that shipping methods
// never go to promotion.
//
// The rationale is in the [Workflows.discountRequestFor] godoc: the [Totals] schema
// cannot carry a shipping discount, and folding it into the cart discount could
// violate cart's "a discount cannot exceed the subtotal" rule.
func TestCalculateTotalsShippingIsNotSentToDiscount(t *testing.T) {
	h := newModuleHarness(t)
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}},
		[]SnapshotShippingMethod{{ID: "csm_1", Amount: 4990}}))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	require.Len(t, h.discounts.requests, 1)
	assert.Empty(t, h.discounts.requests[0].ShippingMethods)
	assert.Equal(t, int64(4990), totals.ShippingTotal, "shipping passes through undiscounted")
	requireIdentity(t, totals)
}

// TestCalculateTotalsDiscountCannotExceedSubtotal verifies that a discount
// exceeding the line amount is not silently accepted.
//
// promotion already promises this; the test pins that the calculation FALLS OVER
// when the promise is BROKEN. Were it accepted the line's total would drop below
// zero and the cart module's consistency check would only kick in at write time.
func TestCalculateTotalsDiscountCannotExceedSubtotal(t *testing.T) {
	h := newModuleHarness(t)
	h.discounts.fn = func(req discountRequest) (discountResponse, error) {
		excessive := req.Items[0].Amount + 1
		return discountResponse{
			CurrencyCode:       req.CurrencyCode,
			Items:              []discountLine{{ID: req.Items[0].ID, Amount: excessive}},
			ItemsDiscountTotal: excessive,
			DiscountTotal:      excessive,
		}, nil
	}
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.Error(t, err)
	assert.Equal(t, CodeDiscountInvalid, errors.CodeOf(err))
	assert.Empty(t, h.carts.written, "nothing must be written with an out-of-contract discount")
}

// TestCalculateTotalsCorruptDiscountResultRejected verifies that responses from
// promotion which break the contract do not enter the calculation.
func TestCalculateTotalsCorruptDiscountResultRejected(t *testing.T) {
	tests := map[string]func(req discountRequest) (discountResponse, error){
		"order not preserved": func(req discountRequest) (discountResponse, error) {
			return discountResponse{
				CurrencyCode: req.CurrencyCode,
				Items: []discountLine{
					{ID: req.Items[1].ID, Amount: 0},
					{ID: req.Items[0].ID, Amount: 0},
				},
			}, nil
		},
		"line missing": func(req discountRequest) (discountResponse, error) {
			return discountResponse{
				CurrencyCode: req.CurrencyCode,
				Items:        []discountLine{{ID: req.Items[0].ID, Amount: 0}},
			}, nil
		},
		"total does not match the lines": func(req discountRequest) (discountResponse, error) {
			return discountResponse{
				CurrencyCode:       req.CurrencyCode,
				Items:              []discountLine{{ID: req.Items[0].ID}, {ID: req.Items[1].ID}},
				ItemsDiscountTotal: 100,
				DiscountTotal:      100,
			}, nil
		},
		"discount on shipping that was not sent": func(req discountRequest) (discountResponse, error) {
			return discountResponse{
				CurrencyCode:          req.CurrencyCode,
				Items:                 []discountLine{{ID: req.Items[0].ID}, {ID: req.Items[1].ID}},
				ShippingDiscountTotal: 50,
			}, nil
		},
		"different currency": func(req discountRequest) (discountResponse, error) {
			return discountResponse{
				CurrencyCode: "EUR",
				Items:        []discountLine{{ID: req.Items[0].ID}, {ID: req.Items[1].ID}},
			}, nil
		},
	}

	for name, script := range tests {
		t.Run(name, func(t *testing.T) {
			h := newModuleHarness(t)
			h.discounts.fn = script
			serveSnapshot(h.carts, twoLineCart(1))

			_, err := h.wf.CalculateTotals(context.Background(), testCartID)
			require.Error(t, err)
			assert.Equal(t, CodeDiscountInvalid, errors.CodeOf(err))
			assert.Empty(t, h.carts.written)
		})
	}
}

// TestCalculateTotalsDiscountErrorKindPreserved verifies that promotion's error
// KIND is not turned into Internal along the way.
//
// If the kind is not preserved a fixable wiring error (e.g. an out-of-contract
// request) reaches the client as a server fault and nobody sets out to fix it.
func TestCalculateTotalsDiscountErrorKindPreserved(t *testing.T) {
	h := newModuleHarness(t)
	h.discounts.err = errors.Invalid("promotion_interop_request_invalid", "the request could not be parsed")
	serveSnapshot(h.carts, twoLineCart(1))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, CodeDiscountFailed, errors.CodeOf(err))
	assert.Empty(t, h.carts.written)
	assert.Zero(t, h.taxes.calls, "if the discount failed the tax must not be called at all")
}

// TestCalculateTotalsSigmaHoldsOnDiscountedCart verifies that the Σ identities hold
// while the discount and the tax carry real values too.
//
// In Phase 5 both fields were zero and the identities held by themselves; this test
// is where they are REALLY exercised for the first time.
func TestCalculateTotalsSigmaHoldsOnDiscountedCart(t *testing.T) {
	h := newModuleHarness(t)
	h.discounts.perLine = map[string]int64{testLineA: 333, testLineB: 77}
	serveSnapshot(h.carts, twoLineCart(9))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	var discountSum, taxSum int64
	for _, line := range totals.Lines {
		discountSum += line.DiscountTotal
		taxSum += line.TaxTotal
	}
	assert.Equal(t, discountSum, totals.DiscountTotal, "Σ line discount = cart discount")
	assert.Equal(t, taxSum, totals.TaxTotal, "Σ line tax = cart tax")
	requireIdentity(t, totals)

	require.Len(t, h.carts.written, 1)
	assert.Equal(t, totals, h.carts.written[0], "the body written onto the cart is the same as the returned result")
}

// TestCalculateTotalsDiscountCatchesOverflow verifies that an overflowing discount
// total does not silently turn negative and that the overflow is caught BEFORE THE
// TAX.
//
// When the discount on both lines is near the ceiling their sum exceeds int64; an
// unchecked addition produces a negative "discount", and a negative discount would
// GROW the cart's total without bound. Catching the overflow at the discount step
// also ensures this: a cart of out-of-contract magnitude is never sent to the tax
// module and does not needlessly hit the base check over there.
func TestCalculateTotalsDiscountCatchesOverflow(t *testing.T) {
	h := newModuleHarness(t)
	h.prices.amounts[testPriceSetA] = MaxAmount
	h.prices.amounts[testPriceSetB] = MaxAmount
	h.discounts.fn = func(req discountRequest) (discountResponse, error) {
		// Each line is discounted by its own subtotal: valid one by one, their
		// sum above [MaxTotal].
		items := make([]discountLine, 0, len(req.Items))
		var sum int64
		for i := range req.Items {
			items = append(items, discountLine{ID: req.Items[i].ID, Amount: req.Items[i].Amount})
			sum += req.Items[i].Amount
		}
		return discountResponse{
			CurrencyCode:       req.CurrencyCode,
			Items:              items,
			ItemsDiscountTotal: sum,
			DiscountTotal:      sum,
		}, nil
	}

	snap := snapshotOf(1, []SnapshotItem{
		{ID: testLineA, VariantID: testVariantA, Quantity: MaxQuantity},
		{ID: testLineB, VariantID: testVariantB, Quantity: MaxQuantity},
	}, nil)

	_, err := h.wf.computeTotals(context.Background(), snap)
	require.Error(t, err)
	assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))
	assert.Zero(t, h.taxes.calls, "an overflowing cart must not be sent to the tax module")
}
