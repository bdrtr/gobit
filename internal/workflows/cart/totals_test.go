package cart

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// requireIdentity verifies that the totals identity holds at both the cart level
// and the line level.
//
// If the identity were written out in every test one by one, forgetting it in a
// single test would stay silent; one helper forces every scenario through the same
// invariant.
func requireIdentity(t *testing.T, totals Totals) {
	t.Helper()

	assert.Equal(t, totals.Subtotal-totals.DiscountTotal+totals.TaxTotal+totals.ShippingTotal, totals.Total,
		"cart identity: total = subtotal - discount + tax + shipping")
	assert.LessOrEqual(t, totals.DiscountTotal, totals.Subtotal, "the discount cannot exceed the subtotal")

	var lineSum int64
	for _, line := range totals.Lines {
		assert.Equal(t, line.Subtotal-line.DiscountTotal+line.TaxTotal, line.Total,
			"line identity: total = subtotal - discount + tax (%s)", line.LineItemID)
		lineSum += line.Subtotal
	}
	assert.Equal(t, lineSum, totals.Subtotal, "the cart subtotal is the sum of the line subtotals")
}

// TestCalculateTotalsEmptyCart verifies that the totals are zero on a cart with no
// lines.
func TestCalculateTotalsEmptyCart(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(0, nil, nil))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	assert.Equal(t, Totals{Revision: 0, TaxSource: TaxSourceRegion, Lines: []LineTotals{}}, totals)
	requireIdentity(t, totals)
	assert.Empty(t, h.prices.requests, "no round trip to pricing for a cart with no lines")
}

// TestCalculateTotalsShippingCountsOnLinelessCart verifies that on a cart with no
// lines but with shipping the total is the shipping alone.
func TestCalculateTotalsShippingCountsOnLinelessCart(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(3, nil, []SnapshotShippingMethod{{ID: "csm_1", Amount: 4990}}))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	assert.Equal(t, int64(0), totals.Subtotal)
	assert.Equal(t, int64(0), totals.TaxTotal, "shipping does not enter the tax base")
	assert.Equal(t, int64(4990), totals.ShippingTotal)
	assert.Equal(t, int64(4990), totals.Total)
	requireIdentity(t, totals)
}

// TestCalculateTotalsSingleLine verifies the totals of a cart with a single line.
func TestCalculateTotalsSingleLine(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 2}}, nil))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	// Unit 1000, quantity 2 -> subtotal 2000; 20% tax -> 400.
	//
	// The RATE is asserted next to the amount because the amount alone cannot
	// carry it: rounding down per line maps a range of rates onto one figure,
	// and an invoice has to print the rate that was charged.
	require.Len(t, totals.Lines, 1)
	assert.Equal(t, LineTotals{
		LineItemID: testLineA,
		UnitPrice:  1000,
		Subtotal:   2000,
		TaxTotal:   400,
		TaxRateBps: 2000,
		Total:      2400,
	}, totals.Lines[0])
	assert.Equal(t, int64(2000), totals.Subtotal)
	assert.Equal(t, int64(400), totals.TaxTotal)
	assert.Equal(t, int64(2400), totals.Total)
	assert.Equal(t, int64(1), totals.Revision)
	requireIdentity(t, totals)

	require.Len(t, h.carts.written, 1)
	assert.Equal(t, totals, h.carts.written[0], "the body written to the cart is the same as the returned result")
}

// TestCalculateTotalsMultipleLinesAndShipping verifies the totals of a cart with
// multiple lines and shipping.
func TestCalculateTotalsMultipleLinesAndShipping(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(7, []SnapshotItem{
		{ID: testLineA, VariantID: testVariantA, Quantity: 2},
		{ID: testLineB, VariantID: testVariantB, Quantity: 3},
	}, []SnapshotShippingMethod{
		{ID: "csm_1", Amount: 2000},
		{ID: "csm_2", Amount: 500},
	}))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	// A: 1000 x 2 = 2000, tax 400. B: 250 x 3 = 750, tax 150.
	assert.Equal(t, int64(2750), totals.Subtotal)
	assert.Equal(t, int64(550), totals.TaxTotal)
	assert.Equal(t, int64(2500), totals.ShippingTotal)
	assert.Equal(t, int64(5800), totals.Total)
	requireIdentity(t, totals)
}

// TestCalculateTotalsTaxRoundsDown verifies that the basis-point division rounds
// down.
func TestCalculateTotalsTaxRoundsDown(t *testing.T) {
	h := newHarness(t)
	h.regions.rateBps = 1850 // 18.5%
	h.prices.amounts[testPriceSetA] = 101
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	// 101 x 1850 / 10000 = 18.685 -> 18 (down; in the customer's favor).
	assert.Equal(t, int64(18), totals.TaxTotal)
	assert.Equal(t, int64(119), totals.Total)
	requireIdentity(t, totals)
}

// TestCalculateTotalsTaxIsComputedPerLine proves that the tax is rounded per line
// and is NOT computed IN ONE PASS over the cart base.
//
// Both lines have a base of 101 and the rate is 18.5%: per line 18 + 18 = 36. Had
// the cart base (202) been taxed in one pass the result would have been 37. The
// difference DISTINGUISHES which branch of the contract is implemented.
func TestCalculateTotalsTaxIsComputedPerLine(t *testing.T) {
	h := newHarness(t)
	h.regions.rateBps = 1850
	h.prices.amounts[testPriceSetA] = 101
	h.prices.amounts[testPriceSetB] = 101
	serveSnapshot(h.carts, snapshotOf(2, []SnapshotItem{
		{ID: testLineA, VariantID: testVariantA, Quantity: 1},
		{ID: testLineB, VariantID: testVariantB, Quantity: 1},
	}, nil))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	assert.Equal(t, int64(36), totals.TaxTotal, "per-line rounding: 18 + 18")
	assert.NotEqual(t, int64(37), totals.TaxTotal, "the cart base must not be taxed in one pass")
	requireIdentity(t, totals)
}

// TestCalculateTotalsShippingIsNotInTheTaxBase verifies that shipping is not taxed.
func TestCalculateTotalsShippingIsNotInTheTaxBase(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}},
		[]SnapshotShippingMethod{{ID: "csm_1", Amount: 5000}}))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	// Only the 1000 worth of goods is taxed: 200. Had shipping been included it
	// would have been 1200.
	assert.Equal(t, int64(200), totals.TaxTotal)
	assert.Equal(t, int64(6200), totals.Total)
	requireIdentity(t, totals)
}

// TestCalculateTotalsNoTaxWhenNotAutomatic verifies that the tax stays zero when the
// region does not apply tax automatically.
func TestCalculateTotalsNoTaxWhenNotAutomatic(t *testing.T) {
	h := newHarness(t)
	h.regions.automatic = false
	h.regions.rateBps = 2000
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	assert.Zero(t, totals.TaxTotal)
	assert.Equal(t, int64(1000), totals.Total)
}

// TestCalculateTotalsOutOfContractTaxRateRejected verifies that an out-of-range rate
// from the region does not enter the calculation.
func TestCalculateTotalsOutOfContractTaxRateRejected(t *testing.T) {
	h := newHarness(t)
	h.regions.rateBps = MaxTaxRateBps + 1
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.Error(t, err)
	assert.Equal(t, CodeTaxRateInvalid, errors.CodeOf(err))
	assert.Empty(t, h.carts.written, "nothing may be written with an invalid rate")
}

// TestCalculateTotalsPriceContextCarriesTheRegion verifies that the pricing call
// puts the region into the rule context.
//
// Had the context gone out empty, the region-specific price rule would not match and
// the base price would be picked silently; nothing would blow up anywhere, only the
// amount would be wrong.
func TestCalculateTotalsPriceContextCarriesTheRegion(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 4}}, nil))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	require.Len(t, h.prices.requests, 1)
	req := h.prices.requests[0]
	assert.Equal(t, testCurrency, req.CurrencyCode)
	assert.Equal(t, map[string]string{attrRegionID: testRegionID}, req.Attributes)
	assert.Equal(t, []priceRequestItem{{PriceSetID: testPriceSetA, Quantity: 4}}, req.Items,
		"the quantity must be carried for tier selection")
}

// TestCalculateTotalsPriceQueryIsBatched verifies that exactly one price round trip
// is made no matter how many lines there are.
//
// The assertion is performance itself: when the price was asked per line, building a
// cart of N lines cost N² round trips (the measurement is in the
// [Workflows.unitPrices] godoc). The CALL COUNT is inspected instead of a duration
// because measuring time binds the test to the machine, while the number of round
// trips does not.
func TestCalculateTotalsPriceQueryIsBatched(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(3, []SnapshotItem{
		{ID: testLineA, VariantID: testVariantA, Quantity: 1},
		{ID: testLineB, VariantID: testVariantB, Quantity: 2},
		{ID: "li_c", VariantID: testVariantA, Quantity: 5},
	}, nil))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	assert.Empty(t, h.prices.seen, "the totals round must not ask for the price per line")
	require.Len(t, h.prices.requests, 1, "a single round trip, independent of the line count")
	assert.Equal(t, []priceRequestItem{
		{PriceSetID: testPriceSetA, Quantity: 1},
		{PriceSetID: testPriceSetB, Quantity: 2},
		{PriceSetID: testPriceSetA, Quantity: 5},
	}, h.prices.requests[0].Items, "the items go out in line order and with their quantities")

	// The same price set is asked for twice and both lines are priced with THEIR OWN
	// quantity; deduplicating the price sets would bind the lines to each other's price.
	require.Len(t, totals.Lines, 3)
	assert.Equal(t, int64(1000), totals.Lines[0].UnitPrice)
	assert.Equal(t, int64(250), totals.Lines[1].UnitPrice)
	assert.Equal(t, int64(1000), totals.Lines[2].UnitPrice)
}

// TestCalculateTotalsMisalignedPriceResponseRejected verifies that a batch response
// that is not the same length as the request FAILS the calculation.
//
// Had it passed silently, lines would be written with the prices of other variants: a
// short response leaves the missing line at zero, a long one shifts the alignment, and
// neither would be caught by the cart's identity checks.
func TestCalculateTotalsMisalignedPriceResponseRejected(t *testing.T) {
	for name, count := range map[string]int{"short": 1, "long": 3} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			serveSnapshot(h.carts, snapshotOf(2, []SnapshotItem{
				{ID: testLineA, VariantID: testVariantA, Quantity: 1},
				{ID: testLineB, VariantID: testVariantB, Quantity: 1},
			}, nil))
			h.prices.batchFn = func(_ priceRequest) (priceResponse, error) {
				items := make([]priceResponseItem, count)
				for i := range items {
					items[i] = priceResponseItem{Amount: 100, Priced: true}
				}
				return priceResponse{Items: items}, nil
			}

			_, err := h.wf.CalculateTotals(context.Background(), testCartID)

			require.Error(t, err)
			assert.Equal(t, errors.KindInternal, errors.KindOf(err),
				"a contract violation is a server error: %v", err)
			assert.Equal(t, CodePriceResponseInvalid, errors.CodeOf(err))
			assert.Empty(t, h.carts.written, "a misaligned response must not be written to the cart")
		})
	}
}

// TestCalculateTotalsUnpricedLineRejectedByFlag verifies that the batch path turns the
// "no price" flag into the SAME error as the single path.
//
// The kind MUST be Invalid: the line IS in the cart, what is missing is its price in
// this currency. Had NotFound gone out, the client would read it as "no cart/line".
func TestCalculateTotalsUnpricedLineRejectedByFlag(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(2, []SnapshotItem{
		{ID: testLineA, VariantID: testVariantA, Quantity: 1},
		{ID: testLineB, VariantID: testVariantB, Quantity: 7},
	}, nil))
	h.prices.batchFn = func(req priceRequest) (priceResponse, error) {
		return priceResponse{Items: []priceResponseItem{
			{Amount: 1000, Priced: true},
			{Priced: false},
		}}, nil
	}

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "expected Invalid: %v", err)
	assert.Equal(t, CodePriceUnavailable, errors.CodeOf(err))
	assert.Contains(t, err.Error(), testVariantB, "which variant has no price must be written out")
	assert.Contains(t, err.Error(), testLineB, "which line it is must be written out")
}

// TestCalculateTotalsALLUnpricedLinesCountedInOneError verifies that several unpriced
// lines are reported in ONE error.
//
// The batch response carries all of the lines at once; returning at the first unpriced
// line would be throwing away information already at hand, and the customer would
// repair their cart one request at a time. This is the only observable thing the
// per-item flag (instead of an error) buys — confirmed by mutation: an implementation
// that returns at the first unpriced line fails this test and passes all of the other
// price tests.
func TestCalculateTotalsALLUnpricedLinesCountedInOneError(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(2, []SnapshotItem{
		{ID: testLineA, VariantID: testVariantA, Quantity: 1},
		{ID: testLineB, VariantID: testVariantB, Quantity: 7},
	}, nil))
	h.prices.batchFn = func(_ priceRequest) (priceResponse, error) {
		return priceResponse{Items: []priceResponseItem{{Priced: false}, {Priced: false}}}, nil
	}

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "expected Invalid: %v", err)
	assert.Equal(t, CodePriceUnavailable, errors.CodeOf(err))
	assert.Contains(t, err.Error(), testVariantA, "the first unpriced variant must be written out")
	assert.Contains(t, err.Error(), testVariantB, "the SECOND unpriced variant must be written out too")
	assert.Contains(t, err.Error(), testLineA, "the ID of the first line must be written out")
	assert.Contains(t, err.Error(), testLineB, "the ID of the second line must be written out")
}

// TestCalculateTotalsOutOfRangeUnitPriceRejected verifies that an out-of-range unit
// price in the batch response FAILS the calculation.
//
// The check is made even though the database enforces the same ceiling: the compiler
// does not see the other side of the boundary, and if an amount above the ceiling were
// written to the cart the error would show up days later, where the multiplication
// overflows — in the line's subtotal.
func TestCalculateTotalsOutOfRangeUnitPriceRejected(t *testing.T) {
	for name, amount := range map[string]int64{"above the ceiling": MaxAmount + 1, "negative": -1} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			serveSnapshot(h.carts, snapshotOf(1,
				[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil))
			h.prices.batchFn = func(_ priceRequest) (priceResponse, error) {
				return priceResponse{Items: []priceResponseItem{{Amount: amount, Priced: true}}}, nil
			}

			_, err := h.wf.CalculateTotals(context.Background(), testCartID)

			require.Error(t, err)
			assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))
			assert.Empty(t, h.carts.written, "an out-of-range amount must not be written to the cart")
		})
	}
}

// TestCalculateTotalsLinkQueryIsBatched verifies that exactly one link query is made
// no matter how many lines there are, and that the variants go out without repetition.
func TestCalculateTotalsLinkQueryIsBatched(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(3, []SnapshotItem{
		{ID: testLineA, VariantID: testVariantA, Quantity: 1},
		{ID: testLineB, VariantID: testVariantB, Quantity: 1},
		{ID: "li_c", VariantID: testVariantA, Quantity: 5},
	}, nil))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	require.Len(t, h.links.batches, 1, "one batched query, not one per line")
	assert.Equal(t, []string{testVariantA, testVariantB}, h.links.batches[0])
	assert.Len(t, totals.Lines, 3, "both lines of the same variant are priced")
}

// TestCalculateTotalsVariantWithoutPriceSetRejected verifies that a variant with no
// price fails the calculation.
func TestCalculateTotalsVariantWithoutPriceSetRejected(t *testing.T) {
	h := newHarness(t)
	delete(h.links.links, testVariantA)
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a variant with no price is a condition the client can fix")
	assert.Equal(t, CodeVariantNotPriced, errors.CodeOf(err))
	assert.Empty(t, h.carts.written)
}

// TestCalculateTotalsMultiplePriceSetsRejected verifies that no price is picked
// silently when a link that must be singular turns out to be plural.
func TestCalculateTotalsMultiplePriceSetsRejected(t *testing.T) {
	h := newHarness(t)
	h.links.links[testVariantA] = []string{testPriceSetA, testPriceSetB}
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.Error(t, err)
	assert.Equal(t, CodeVariantPriceSetAmbiguous, errors.CodeOf(err))
}

// TestCalculateTotalsMissingPriceInCurrencyIsInvalid verifies that pricing's NotFound
// error does not leak to the client as a 404.
func TestCalculateTotalsMissingPriceInCurrencyIsInvalid(t *testing.T) {
	h := newHarness(t)
	delete(h.prices.amounts, testPriceSetA)
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.Error(t, err)
	assert.False(t, errors.IsNotFound(err), "the line is there; only its price is missing")
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, CodePriceUnavailable, errors.CodeOf(err))
}

// TestCalculateTotalsRejectsCompletedCart verifies that no calculation is done on a
// closed cart and that pricing is never called.
func TestCalculateTotalsRejectsCompletedCart(t *testing.T) {
	h := newHarness(t)
	snap := snapshotOf(4, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil)
	snap.Completed = true
	serveSnapshot(h.carts, snap)

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, CodeCartCompleted, errors.CodeOf(err))
	assert.Empty(t, h.prices.seen, "pricing must not be called for a round whose outcome is known up front")
}

// TestCalculateTotalsRecomputesOnRevisionConflict verifies that the conflicting round
// is thrown away and the cart is recomputed with its NEW revision.
func TestCalculateTotalsRecomputesOnRevisionConflict(t *testing.T) {
	h := newHarness(t)
	// The first round reads revision 4; by the time it writes, the cart has moved to
	// 5. The second round reads 5 and the second line is now in the cart.
	serveSnapshot(h.carts,
		snapshotOf(4, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil),
		snapshotOf(5, []SnapshotItem{
			{ID: testLineA, VariantID: testVariantA, Quantity: 1},
			{ID: testLineB, VariantID: testVariantB, Quantity: 2},
		}, nil),
	)
	h.carts.setTotalsFn = func(_ context.Context, _ string, _ json.RawMessage) error {
		if len(h.carts.written) == 1 {
			return errors.Conflict("cart_totals_stale", "the totals do not belong to the cart's current revision")
		}
		return nil
	}

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	assert.Equal(t, 2, h.carts.snapshotCalls, "the conflicting round must RE-READ the cart")
	assert.Equal(t, int64(5), totals.Revision)
	assert.Len(t, totals.Lines, 2)
	assert.Equal(t, int64(1500), totals.Subtotal, "1000 + 250 x 2")
	requireIdentity(t, totals)

	require.Len(t, h.carts.written, 2)
	assert.Equal(t, int64(4), h.carts.written[0].Revision, "the first round had been stamped with the old revision")
	assert.Equal(t, int64(5), h.carts.written[1].Revision)
}

// TestCalculateTotalsGivesUpWhenConflictPersists verifies that retrying is BOUNDED and
// that a conflict error is returned once the bound is exceeded.
func TestCalculateTotalsGivesUpWhenConflictPersists(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil))
	h.carts.setTotalsFn = func(_ context.Context, _ string, _ json.RawMessage) error {
		return errors.Conflict("cart_totals_stale", "the cart changed again")
	}

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, CodeTotalsConflict, errors.CodeOf(err))
	assert.Equal(t, MaxTotalsAttempts, h.carts.snapshotCalls, "attempted up to the bound, not more")
}

// TestCalculateTotalsNonConflictErrorIsNotRetried verifies that the round is not
// repeated on a write error that is NOT a conflict.
func TestCalculateTotalsNonConflictErrorIsNotRetried(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil))
	h.carts.setTotalsFn = func(_ context.Context, _ string, _ json.RawMessage) error {
		return errors.Invalid("cart_totals_inconsistent", "the total is inconsistent")
	}

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, 1, h.carts.snapshotCalls, "an input error gives the same result when repeated")
}

// TestCalculateTotalsCorruptSnapshotRejected verifies that an out-of-contract body
// from the cart module does not enter the calculation.
func TestCalculateTotalsCorruptSnapshotRejected(t *testing.T) {
	tests := map[string]Snapshot{
		"empty currency": {ID: testCartID, RegionID: testRegionID, Revision: 1},
		"empty region":   {ID: testCartID, CurrencyCode: testCurrency, Revision: 1},
		"another cart":   {ID: "cart_other", RegionID: testRegionID, CurrencyCode: testCurrency},
		"zero quantity": {
			ID: testCartID, RegionID: testRegionID, CurrencyCode: testCurrency,
			Items: []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 0}},
		},
		"line without variant": {
			ID: testCartID, RegionID: testRegionID, CurrencyCode: testCurrency,
			Items: []SnapshotItem{{ID: testLineA, Quantity: 1}},
		},
		"negative shipping": {
			ID: testCartID, RegionID: testRegionID, CurrencyCode: testCurrency,
			ShippingMethods: []SnapshotShippingMethod{{ID: "csm_1", Amount: -1}},
		},
	}

	for name, snap := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.carts.snapshotFn = func(_ context.Context, _ string) (json.RawMessage, error) {
				return json.Marshal(snap)
			}

			_, err := h.wf.CalculateTotals(context.Background(), testCartID)
			require.Error(t, err)
			assert.Equal(t, CodeSnapshotInvalid, errors.CodeOf(err))
			assert.Empty(t, h.carts.written)
		})
	}
}

// TestCalculateTotalsInvalidCartIDRejected verifies that the ID validation is done
// without going to the cart module at all.
func TestCalculateTotalsInvalidCartIDRejected(t *testing.T) {
	h := newHarness(t)

	_, err := h.wf.CalculateTotals(context.Background(), "  ")
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Zero(t, h.carts.snapshotCalls)
}

// TestComputeTotalsCatchesOverflow verifies that an overflowing subtotal does not
// silently turn negative.
func TestComputeTotalsCatchesOverflow(t *testing.T) {
	h := newHarness(t)
	h.prices.amounts[testPriceSetA] = MaxAmount
	h.prices.amounts[testPriceSetB] = MaxAmount

	snap := snapshotOf(1, []SnapshotItem{
		{ID: testLineA, VariantID: testVariantA, Quantity: MaxQuantity},
		{ID: testLineB, VariantID: testVariantB, Quantity: MaxQuantity},
	}, nil)

	_, err := h.wf.computeTotals(context.Background(), snap)
	require.Error(t, err)
	assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))
}
