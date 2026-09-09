package cart

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestATaxInclusivePriceIsCHARGEDAsTheStickerSays is the whole point of the
// feature, asserted at the only place it can be seen: the cart total.
//
// A shopper in a market that quotes VAT-inclusive prices sees 199,00 and has to
// pay 199,00. The line arrives as a GROSS subtotal; the tax module takes the
// tax out of it and reports the net base; this side rewrites the subtotal to
// that base so the totals identity — Total = Subtotal - Discount + Shipping +
// Tax — lands back exactly on the sticker.
//
// The number is not rounded to make the test pretty: 199,00 at 20% extracts
// 33,16 and leaves 165,84, and 165,84 + 33,16 is 199,00. Had the cart instead
// re-taxed the extracted net with the ordinary calculation, the answer would
// have been 199,01 — measured, and the reason TaxIncludedIn exists.
func TestATaxInclusivePriceIsCHARGEDAsTheStickerSays(t *testing.T) {
	const gross int64 = 19_900

	snap := Snapshot{ID: "cart_inclusive"}
	lines := []LineTotals{{LineItemID: "li_1", Subtotal: gross}}

	resp := taxResponse{
		RegionFound:      true,
		PricesIncludeTax: true,
		TaxTotal:         3_316,
		Items: []taxResponseLine{{
			ID:            "li_1",
			RateBps:       2000,
			TaxableAmount: 16_584,
			TaxAmount:     3_316,
		}},
	}

	require.NoError(t, applyTaxResponse(snap, lines, resp))

	assert.Equal(t, int64(16_584), lines[0].Subtotal,
		"the line's subtotal has to become the NET the tax module extracted")
	assert.Equal(t, int64(3_316), lines[0].TaxTotal)

	totals, err := assembleTotals(snap, lines, 0, "tax")
	require.NoError(t, err)

	assert.Equal(t, gross, totals.Total,
		"the shopper saw %d and the cart charges %d", gross, totals.Total)
	assert.Equal(t, int64(16_584), totals.Subtotal)
	assert.Equal(t, int64(3_316), totals.TaxTotal)
}

// TestATaxInclusiveLineThatDoesNotAddUpIsREFUSED pins the check that replaced
// the old one.
//
// On the tax-exclusive path the cart demands that the returned base be EXACTLY
// what it sent. That check cannot survive extraction — the base comes back
// smaller on purpose — and the replacement is not a relaxation of it: base plus
// tax has to equal what was sent. A single kurus off is what the whole feature
// exists to prevent, so a single kurus off is refused.
func TestATaxInclusiveLineThatDoesNotAddUpIsREFUSED(t *testing.T) {
	const gross int64 = 19_900

	snap := Snapshot{ID: "cart_inclusive"}
	lines := []LineTotals{{LineItemID: "li_1", Subtotal: gross}}

	resp := taxResponse{
		RegionFound:      true,
		PricesIncludeTax: true,
		TaxTotal:         3_316,
		Items: []taxResponseLine{{
			ID:      "li_1",
			RateBps: 2000,
			// One kurus short: 16_583 + 3_316 = 19_899, and the shopper saw
			// 19_900.
			TaxableAmount: 16_583,
			TaxAmount:     3_316,
		}},
	}

	err := applyTaxResponse(snap, lines, resp)

	require.Error(t, err, "a line that does not add back up to the sticker has to be refused")
	assert.Contains(t, err.Error(), "does not add up")
	assert.Equal(t, gross, lines[0].Subtotal,
		"a refused response must not have written anything onto the lines")
}

// TestATaxExclusiveLineStillDemandsTheBaseItSent keeps the old check alive.
//
// The two modes check DIFFERENT invariants and the exclusive one did not move:
// nothing should have changed the base, so anything but equality is a fault. A
// single branch that accepted "base <= sent" would have quietly swallowed it.
func TestATaxExclusiveLineStillDemandsTheBaseItSent(t *testing.T) {
	snap := Snapshot{ID: "cart_exclusive"}
	lines := []LineTotals{{LineItemID: "li_1", Subtotal: 10_000}}

	resp := taxResponse{
		RegionFound: true,
		TaxTotal:    2_000,
		Items: []taxResponseLine{{
			ID:            "li_1",
			RateBps:       2000,
			TaxableAmount: 9_999,
			TaxAmount:     2_000,
		}},
	}

	err := applyTaxResponse(snap, lines, resp)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "differs from the one sent")
}

// TestATaxInclusiveDiscountLeavesTheIdentityExact covers the case the hybrid
// subtotal was chosen for.
//
// The discount is taken off the GROSS before the amount is sent, so the tax is
// extracted from what is actually being charged. The subtotal then becomes the
// extracted base PLUS that discount, which is what makes Subtotal - Discount +
// Tax land on the discounted gross. The discount stays a gross figure beside a
// net subtotal, and that hybrid is the accepted cost.
func TestATaxInclusiveDiscountLeavesTheIdentityExact(t *testing.T) {
	const gross int64 = 19_900
	const discount int64 = 1_900 // charged: 18_000

	snap := Snapshot{ID: "cart_inclusive_discount"}
	lines := []LineTotals{{LineItemID: "li_1", Subtotal: gross, DiscountTotal: discount}}

	// 18_000 at 20% inclusive: tax 3_000, base 15_000.
	resp := taxResponse{
		RegionFound:      true,
		PricesIncludeTax: true,
		TaxTotal:         3_000,
		Items: []taxResponseLine{{
			ID:            "li_1",
			RateBps:       2000,
			TaxableAmount: 15_000,
			TaxAmount:     3_000,
		}},
	}

	require.NoError(t, applyTaxResponse(snap, lines, resp))

	totals, err := assembleTotals(snap, lines, 0, "tax")
	require.NoError(t, err)

	assert.Equal(t, gross-discount, totals.Total,
		"a discounted tax-inclusive cart has to charge the discounted sticker")
	assert.Equal(t, int64(16_900), totals.Subtotal, "base + discount")
	assert.Equal(t, discount, totals.DiscountTotal, "the discount stays as it was entered")
}
