package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// The pools a drawn configuration names its products, types and classes from;
// small, so rules and lines meet.
var (
	trialProducts = []string{"prod_1", "prod_2", "prod_3"}
	trialTypes    = []string{"", "ptyp_1", "ptyp_2"}
	trialClasses  = []string{"taxclass_1", "taxclass_2"}
)

// TestABaselineIsWhatTheCartIsCharged is the comparison's baseline held to
// the calculation a cart runs (ADR 0387): for any configuration of the
// country's rates and rules, with or without inclusive prices, a stack and
// tax classes, each drawn as often as its absence, the baseline is the tax
// and the rate CalculateTax gives the same line.
func TestABaselineIsWhatTheCartIsCharged(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		repo := newMemRepo()
		svc := New(repo, Options{Now: func() time.Time { return testNow }})
		included := rapid.Bool().Draw(t, "included")
		repo.seedRegion(models.TaxRegion{ID: trRegionID, CountryCode: "TR", PricesIncludeTax: &included})

		target := ""
		if rapid.Bool().Draw(t, "default") {
			value := rapid.Int32Range(0, 6000).Draw(t, "default_bps")
			repo.seedDefaultRate(rateA, trRegionID, value)
			target = rateA
			if !included && rapid.Bool().Draw(t, "stack") {
				stacked := rapid.Int32Range(0, 10_000-value).Draw(t, "stacked_bps")
				compound := rapid.Bool().Draw(t, "compound")
				// Only a stack the write path accepts: a compound pair whose sum
				// is within the line can still take more than it.
				if assertStackWithinBase([]models.TaxRate{{RateBps: value}, {RateBps: stacked, Compound: compound}}) != nil {
					compound = false
				}
				repo.seedStackedRate(rateB, trRegionID, rateA, stacked, compound)
			}
		}
		for i, id := range []string{rateC, rateD, rateE}[:rapid.IntRange(0, 3).Draw(t, "ruled")] {
			repo.seedRuledRate(id, trRegionID, rapid.Int32Range(0, 10_000).Draw(t, fmt.Sprintf("ruled_bps_%d", i)))
			target = id
			for j := range rapid.IntRange(0, 2).Draw(t, fmt.Sprintf("rules_%d", i)) {
				reference := rapid.SampledFrom([]models.RuleReference{
					models.ReferenceProduct, models.ReferenceTaxClass, models.ReferenceProductType,
				}).Draw(t, fmt.Sprintf("reference_%d_%d", i, j))
				pool := map[models.RuleReference][]string{
					models.ReferenceProduct: trialProducts, models.ReferenceTaxClass: trialClasses,
					models.ReferenceProductType: trialTypes[1:],
				}[reference]
				repo.seedRule(fmt.Sprintf("%sR%d%d", models.TaxRateRuleIDPrefix, i, j),
					id, reference, rapid.SampledFrom(pool).Draw(t, fmt.Sprintf("reference_id_%d_%d", i, j)))
			}
		}
		if target == "" {
			return
		}
		for _, product := range trialProducts {
			if class := rapid.IntRange(-1, len(trialClasses)-1).Draw(t, "class_of_"+product); class >= 0 {
				repo.members[product] = trialClasses[class]
			}
		}

		items := make([]TaxableItem, rapid.IntRange(1, 6).Draw(t, "lines"))
		for i := range items {
			items[i] = TaxableItem{
				ID:            fmt.Sprintf("li_%d", i),
				ProductID:     rapid.SampledFrom(trialProducts).Draw(t, fmt.Sprintf("product_%d", i)),
				ProductTypeID: rapid.SampledFrom(trialTypes).Draw(t, fmt.Sprintf("type_%d", i)),
				Amount:        rapid.Int64Range(0, 10_000_000).Draw(t, fmt.Sprintf("amount_%d", i)),
			}
		}

		ctx := context.Background()
		cart, err := svc.CalculateTax(ctx, CalculateTaxInput{CountryCode: "TR", Items: items})
		require.NoError(t, err)
		answers, _, err := svc.CompareRate(ctx, target, RateChange{RateBps: new(int32)},
			[]CompareEntry{{Reference: "order_1", CountryCode: "TR", Items: items}})
		require.NoError(t, err)

		for i := range items {
			got := answers[0].Items[i].Baseline
			require.Equal(t, CompareLine{RateID: cart.Items[i].RateID, TaxAmount: cart.Items[i].TaxAmount}, got,
				"line %d", i)
		}
	})
}
