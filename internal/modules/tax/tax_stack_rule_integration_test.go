//go:build integration

package tax_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

// TestAStackOnARuledBaseIsWrittenInEitherOrder is gap D249 on the real schema
// and rule write (ADR 0405): the base's rule goes before the stack or after it,
// the tax comes out the same, the rate on top still takes no rule, and the
// base's last rule deleted leaves the stack applying to no line.
func TestAStackOnARuledBaseIsWrittenInEitherOrder(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	for _, order := range []struct {
		name      string
		ruleFirst bool
	}{{"rule first", true}, {"stack first", false}} {
		t.Run(order.name, func(t *testing.T) {
			region := newRootRegion(ctx, t, svc)
			def, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
				TaxRegionID: region.ID, Name: "VAT", RateBps: 2000, IsDefault: true,
			})
			require.NoError(t, err)
			base, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
				TaxRegionID: region.ID, Name: "state", RateBps: 500,
			})
			require.NoError(t, err)
			in := service.CreateRateRuleInput{TaxRateID: base.ID, Reference: "product", ReferenceID: "prod_stacked"}
			var ruleID string
			addRule := func() {
				rule, err := svc.CreateRateRule(ctx, in)
				require.NoError(t, err)
				ruleID = rule.ID
			}
			if order.ruleFirst {
				addRule()
			}
			top, err := svc.CreateTaxRate(ctx, service.CreateTaxRateInput{
				TaxRegionID: region.ID, Name: "county", RateBps: 800, StacksOnID: base.ID, Compound: true,
			})
			require.NoError(t, err, "a stack is built on a ruled base")
			if !order.ruleFirst {
				addRule()
			}

			_, err = svc.CreateRateRule(ctx, service.CreateRateRuleInput{
				TaxRateID: top.ID, Reference: "product", ReferenceID: "prod_other",
			})
			require.Error(t, err, "a rate standing on another takes no rule of its own")
			assert.True(t, errors.IsConflict(err))
			assert.Equal(t, "tax_constraint_violation", errors.CodeOf(err))

			items := []service.TaxableItem{
				{ID: "li_stacked", ProductID: "prod_stacked", Amount: 12_345},
				{ID: "li_other", ProductID: "prod_other", Amount: 12_345},
			}
			result, err := svc.CalculateTax(ctx, service.CalculateTaxInput{CountryCode: region.CountryCode, Items: items})
			require.NoError(t, err)
			require.Len(t, result.Items, 2)
			stacked, other := result.Items[0], result.Items[1]
			require.Equal(t, "li_stacked", stacked.ID)
			assert.Equal(t, base.ID, stacked.RateID)
			// 12345 x 5% = 617.25 floors to 617; the compound rate takes the tax
			// below it as well, (12345 + 617) x 8% = 1036.96, which floors to 1036.
			assert.Equal(t, int64(617+1036), stacked.TaxAmount, "the base's rule chose the whole stack")
			require.Len(t, stacked.Components, 2)
			assert.Equal(t, base.ID, stacked.Components[0].RateID)
			assert.Equal(t, top.ID, stacked.Components[1].RateID)
			require.Equal(t, "li_other", other.ID)
			assert.Equal(t, def.ID, other.RateID)
			assert.Equal(t, int64(2469), other.TaxAmount, "12345 x 20% = 2469.0, the default alone")
			assert.Empty(t, other.Components)

			require.NoError(t, svc.DeleteRateRule(ctx, ruleID))
			result, err = svc.CalculateTax(ctx, service.CalculateTaxInput{CountryCode: region.CountryCode, Items: items})
			require.NoError(t, err)
			require.Len(t, result.Items, 2)
			require.Equal(t, "li_stacked", result.Items[0].ID)
			assert.Equal(t, def.ID, result.Items[0].RateID, "a base without rules is not chosen")
			assert.Equal(t, int64(2469), result.Items[0].TaxAmount, "the stack left with its base's last rule")
			assert.Empty(t, result.Items[0].Components)
		})
	}
}
