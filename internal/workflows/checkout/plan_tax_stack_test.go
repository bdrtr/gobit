package checkout

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// TestTheOrderSnapshotCarriesTheTaxBreakdown is the sending half of the hop the
// order ignores unknown fields on.
//
// The plan holds the breakdown as typed values; what this pins is the WIRE, and
// the order cannot import this package to check the names for itself.
func TestTheOrderSnapshotCarriesTheTaxBreakdown(t *testing.T) {
	plan := &checkoutPlan{
		CartID: "cart_1",
		Lines: []planLine{{
			LineItemID: "li_1",
			VariantID:  "var_1",
			Title:      "Red T-Shirt",
			Quantity:   1,
			UnitPrice:  12345,
			Subtotal:   12345,
			TaxTotal:   1653,
			TaxRateBps: 500,
			Total:      13998,
			TaxComponents: []cartwf.LineTaxComponent{
				{RateID: "txr_base", RateBps: 500, TaxableAmount: 12345, TaxAmount: 617},
				{RateID: "txr_top", RateBps: 800, Compound: true, TaxableAmount: 12962, TaxAmount: 1036},
			},
		}},
	}

	raw, err := plan.orderSnapshotJSON("idem_1")
	require.NoError(t, err)

	var body struct {
		Items []struct {
			TaxRateBps    int32 `json:"tax_rate_bps"`
			TaxComponents []struct {
				RateID        string `json:"rate_id"`
				RateBps       int32  `json:"rate_bps"`
				Compound      bool   `json:"compound"`
				TaxableAmount int64  `json:"taxable_amount"`
				TaxAmount     int64  `json:"tax_amount"`
			} `json:"tax_components"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	require.Len(t, body.Items, 1)

	assert.Equal(t, int32(500), body.Items[0].TaxRateBps, "the line still carries the base")
	require.Len(t, body.Items[0].TaxComponents, 2)
	assert.Equal(t, "txr_top", body.Items[0].TaxComponents[1].RateID)
	assert.Equal(t, int32(800), body.Items[0].TaxComponents[1].RateBps)
	assert.True(t, body.Items[0].TaxComponents[1].Compound)
	assert.Equal(t, int64(12962), body.Items[0].TaxComponents[1].TaxableAmount)
	assert.Equal(t, int64(1036), body.Items[0].TaxComponents[1].TaxAmount)
}

// TestAnUnstackedSnapshotOmitsTheBreakdown keeps the body the size it was for
// the line every shop mostly sells.
func TestAnUnstackedSnapshotOmitsTheBreakdown(t *testing.T) {
	plan := &checkoutPlan{
		CartID: "cart_1",
		Lines: []planLine{{
			LineItemID: "li_1", VariantID: "var_1", Title: "Red T-Shirt",
			Quantity: 1, UnitPrice: 10_000, Subtotal: 10_000,
			TaxTotal: 2000, TaxRateBps: 2000, Total: 12_000,
		}},
	}

	raw, err := plan.orderSnapshotJSON("idem_1")
	require.NoError(t, err)

	assert.NotContains(t, string(raw), "tax_components",
		"a single-rate line must not carry the key at all")
}
