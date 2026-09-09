package cart

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// stackedResponse is what tax returns for a line taxed at 5% + 8% compound on a
// base of 12345: 617 and 1036, adding to the 1653 the line carries.
func stackedResponse() taxResponse {
	return taxResponse{
		RegionFound: true,
		TaxTotal:    1653,
		Items: []taxResponseLine{{
			ID:            "li_1",
			RateID:        "txr_base",
			RateBps:       500,
			TaxableAmount: 12345,
			TaxAmount:     1653,
			Components: []taxResponseComponent{
				{RateID: "txr_base", RateBps: 500, TaxableAmount: 12345, TaxAmount: 617},
				{RateID: "txr_top", RateBps: 800, Compound: true, TaxableAmount: 12962, TaxAmount: 1036},
			},
		}},
	}
}

// TestAStackedLineCarriesItsBreakdownIntoTheTotals is the cart's half of the
// chain: what tax says has to survive as far as the totals the checkout reads.
func TestAStackedLineCarriesItsBreakdownIntoTheTotals(t *testing.T) {
	snap := Snapshot{ID: "cart_stack"}
	lines := []LineTotals{{LineItemID: "li_1", Subtotal: 12345}}

	require.NoError(t, applyTaxResponse(snap, lines, stackedResponse()))

	require.Len(t, lines[0].TaxComponents, 2)
	assert.Equal(t, LineTaxComponent{
		RateID: "txr_base", RateBps: 500, Compound: false,
		TaxableAmount: 12345, TaxAmount: 617,
	}, lines[0].TaxComponents[0])
	assert.Equal(t, LineTaxComponent{
		RateID: "txr_top", RateBps: 800, Compound: true,
		TaxableAmount: 12962, TaxAmount: 1036,
	}, lines[0].TaxComponents[1])

	assert.Equal(t, int64(1653), lines[0].TaxTotal)
	assert.Equal(t, int32(500), lines[0].TaxRateBps,
		"the line keeps the stack's BASE rate beside the list")
}

// TestASingleRateLineCarriesNoBreakdown keeps the absence meaningful.
func TestASingleRateLineCarriesNoBreakdown(t *testing.T) {
	snap := Snapshot{ID: "cart_plain"}
	lines := []LineTotals{{LineItemID: "li_1", Subtotal: 10_000}}

	resp := taxResponse{
		RegionFound: true,
		TaxTotal:    2000,
		Items: []taxResponseLine{{
			ID: "li_1", RateBps: 2000, TaxableAmount: 10_000, TaxAmount: 2000,
		}},
	}

	require.NoError(t, applyTaxResponse(snap, lines, resp))
	assert.Empty(t, lines[0].TaxComponents)
}

// TestABreakdownThatDoesNotAddUpIsRefused stops the cart from passing on a
// breakdown a document would print INSTEAD of the line's own figure.
//
// The line's own tax stays inside its base, so no existing check sees this.
func TestABreakdownThatDoesNotAddUpIsRefused(t *testing.T) {
	snap := Snapshot{ID: "cart_stack"}
	lines := []LineTotals{{LineItemID: "li_1", Subtotal: 12345}}

	resp := stackedResponse()
	resp.Items[0].Components[1].TaxAmount = 1035

	err := applyTaxResponse(snap, lines, resp)

	require.Error(t, err)
	assert.Equal(t, CodeTaxInvalid, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "do not add up to its tax")
	assert.Empty(t, lines[0].TaxComponents, "nothing may be written when the response is refused")
}
