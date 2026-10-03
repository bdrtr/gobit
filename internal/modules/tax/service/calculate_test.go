package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// taxOfItem finds a line item in the result by its id.
func taxOfItem(t *testing.T, result CalculateTaxResult, id string) ItemTax {
	t.Helper()

	for i := range result.Items {
		if result.Items[i].ID == id {
			return result.Items[i]
		}
	}
	t.Fatalf("line item %q is not in the result: %+v", id, result.Items)
	return ItemTax{}
}

// requireTotalIdentity checks the total identity: TaxTotal = Σ lines +
// shipping.
//
// The identity is checked separately in every test because the total is not
// taken from the provider but summed again in the service; a summing error has
// to show in EVERY scenario, not in a single test.
func requireTotalIdentity(t *testing.T, result CalculateTaxResult) {
	t.Helper()

	var sum int64
	for i := range result.Items {
		sum += result.Items[i].TaxAmount
	}
	sum += result.Shipping.TaxAmount
	require.Equal(t, sum, result.TaxTotal, "the total tax has to be the sum of the line taxes")
}

// TestCalculateTaxAppliesTheDefaultRate checks that a line item with no rule
// falls back to the region's default rate.
func TestCalculateTaxAppliesTheDefaultRate(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", ProductID: "prod_1", Amount: 3000}},
	})
	require.NoError(t, err)

	assert.True(t, result.RegionFound)
	assert.Equal(t, trRegionID, result.RegionID)
	assert.Equal(t, LocalProviderID, result.ProviderID)

	line := taxOfItem(t, result, "li_1")
	assert.Equal(t, int32(2000), line.RateBps)
	assert.Equal(t, rateA, line.RateID)
	assert.Equal(t, int64(3000), line.TaxableAmount)
	assert.Equal(t, int64(600), line.TaxAmount)
	assert.Equal(t, int64(600), result.TaxTotal)
	requireTotalIdentity(t, result)
}

// TestCalculateTaxARuledRateAppliesOnlyToTheMatchingLine checks that a ruled
// rate's scope really narrows.
func TestCalculateTaxARuledRateAppliesOnlyToTheMatchingLine(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)
	repo.seedRuledRate(rateB, trRegionID, 100)
	repo.seedRule(ruleA, rateB, models.ReferenceProduct, "prod_book")

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items: []TaxableItem{
			{ID: "li_book", ProductID: "prod_book", Amount: 10_000},
			{ID: "li_other", ProductID: "prod_other", Amount: 10_000},
		},
	})
	require.NoError(t, err)

	book := taxOfItem(t, result, "li_book")
	assert.Equal(t, int32(100), book.RateBps, "the line matching the rule has to fall to the reduced rate")
	assert.Equal(t, int64(100), book.TaxAmount)

	other := taxOfItem(t, result, "li_other")
	assert.Equal(t, int32(2000), other.RateBps, "the line that does not match has to fall to the default")
	assert.Equal(t, int64(2000), other.TaxAmount)

	assert.Equal(t, int64(2100), result.TaxTotal)
	requireTotalIdentity(t, result)
}

// TestCalculateTaxAProductRuleBeatsAProductTypeRule checks the specificity
// order.
func TestCalculateTaxAProductRuleBeatsAProductTypeRule(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)
	// The rate with the LARGER id carries the product rule: the only reason it
	// wins has to be specificity, not order.
	repo.seedRuledRate(rateB, trRegionID, 800)
	repo.seedRule(ruleA, rateB, models.ReferenceProductType, "ptyp_food")
	repo.seedRuledRate(rateC, trRegionID, 100)
	repo.seedRule(ruleB, rateC, models.ReferenceProduct, "prod_bread")

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items: []TaxableItem{
			{ID: "li_1", ProductID: "prod_bread", ProductTypeID: "ptyp_food", Amount: 10_000},
		},
	})
	require.NoError(t, err)

	line := taxOfItem(t, result, "li_1")
	assert.Equal(t, int32(100), line.RateBps, "the product rule has to beat the product type rule")
	assert.Equal(t, rateC, line.RateID)
	requireTotalIdentity(t, result)
}

// TestCalculateTaxOnEqualSpecificityTheSmallerIDWins checks that the
// tie-break rule is DETERMINISTIC.
func TestCalculateTaxOnEqualSpecificityTheSmallerIDWins(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedRuledRate(rateB, trRegionID, 1000)
	repo.seedRule(ruleA, rateB, models.ReferenceProduct, "prod_1")
	repo.seedRuledRate(rateC, trRegionID, 2000)
	repo.seedRule(ruleB, rateC, models.ReferenceProduct, "prod_1")

	// The same input is run many times: a selection that depended on map
	// iteration order would give an unstable result here.
	for i := range 20 {
		result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
			CountryCode: "TR",
			Items:       []TaxableItem{{ID: "li_1", ProductID: "prod_1", Amount: 10_000}},
		})
		require.NoError(t, err, "round %d", i)
		assert.Equal(t, rateB, taxOfItem(t, result, "li_1").RateID,
			"round %d: the rate with the smaller id has to win", i)
	}
}

// TestCalculateTaxTheProvinceOverridesTheCountry checks that the province's
// default replaces the country rate entirely and that rates are NOT ADDED.
func TestCalculateTaxTheProvinceOverridesTheCountry(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(usRegionID, "US")
	repo.seedProvinceRegion(trIstanbul, "US", "CA", usRegionID)
	repo.seedDefaultRate(rateA, usRegionID, 2000)
	repo.seedDefaultRate(rateB, trIstanbul, 725)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode:  "US",
		ProvinceCode: "CA",
		Items:        []TaxableItem{{ID: "li_1", Amount: 10_000}},
	})
	require.NoError(t, err)

	assert.Equal(t, trIstanbul, result.RegionID, "the most specific region has to show in the result")
	line := taxOfItem(t, result, "li_1")
	assert.Equal(t, int32(725), line.RateBps, "the province rate has to OVERRIDE the country")
	assert.Equal(t, int64(725), line.TaxAmount, "rates must NOT BE ADDED (not 2725)")
	requireTotalIdentity(t, result)
}

// TestCalculateTaxFallsBackToTheCountryWhenTheProvinceGivesNoRate checks the
// move up the chain.
func TestCalculateTaxFallsBackToTheCountryWhenTheProvinceGivesNoRate(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(usRegionID, "US")
	repo.seedProvinceRegion(trIstanbul, "US", "CA", usRegionID)
	repo.seedDefaultRate(rateA, usRegionID, 2000)
	// The province has ONLY a ruled rate written for a single product.
	repo.seedRuledRate(rateB, trIstanbul, 0)
	repo.seedRule(ruleA, rateB, models.ReferenceProduct, "prod_exempt")

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode:  "US",
		ProvinceCode: "CA",
		Items: []TaxableItem{
			{ID: "li_exempt", ProductID: "prod_exempt", Amount: 10_000},
			{ID: "li_normal", ProductID: "prod_normal", Amount: 10_000},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, int32(0), taxOfItem(t, result, "li_exempt").RateBps,
		"the province's rule has to apply to the matching line")
	assert.Equal(t, int32(2000), taxOfItem(t, result, "li_normal").RateBps,
		"a line the province gives no rate to has to fall back to the country")
	assert.Equal(t, int64(2000), result.TaxTotal)
	requireTotalIdentity(t, result)
}

// TestCalculateTaxAnUnknownProvinceFallsBackToTheCountry checks that an
// undefined province code produces the country rate, not an error.
func TestCalculateTaxAnUnknownProvinceFallsBackToTheCountry(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(usRegionID, "US")
	repo.seedDefaultRate(rateA, usRegionID, 2000)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode:  "US",
		ProvinceCode: "ZZ",
		Items:        []TaxableItem{{ID: "li_1", Amount: 5000}},
	})
	require.NoError(t, err)

	assert.Equal(t, usRegionID, result.RegionID)
	assert.Equal(t, int64(1000), result.TaxTotal)
	requireTotalIdentity(t, result)
}

// TestCalculateTaxWithoutARegionIsZeroTax checks that when no region is found
// zero tax comes back, NOT an error, and that the situation is VISIBLE in the
// result.
func TestCalculateTaxWithoutARegionIsZeroTax(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "DE",
		Items:       []TaxableItem{{ID: "li_1", Amount: 10_000}},
	})
	require.NoError(t, err, "an unconfigured country must not fail the cart")

	assert.False(t, result.RegionFound, "the caller has to be able to see the missing configuration")
	assert.Empty(t, result.RegionID)
	assert.Empty(t, result.ProviderID)
	assert.Equal(t, int64(0), result.TaxTotal)

	line := taxOfItem(t, result, "li_1")
	assert.Equal(t, int64(10_000), line.TaxableAmount, "the base has to be reported all the same")
	assert.Equal(t, int64(0), line.TaxAmount)
	assert.Empty(t, line.RateID)
	requireTotalIdentity(t, result)
}

// TestCalculateTaxARegionWithoutARateIsZeroTax checks that a country that has
// a region but no rate at all produces zero tax.
//
// It is separate from the NO region case: RegionFound stays true.
func TestCalculateTaxARegionWithoutARateIsZeroTax(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", Amount: 10_000}},
	})
	require.NoError(t, err)

	assert.True(t, result.RegionFound)
	assert.Equal(t, int64(0), result.TaxTotal)
	assert.Empty(t, taxOfItem(t, result, "li_1").RateID)
	requireTotalIdentity(t, result)
}

// TestCalculateTaxZeroBase checks that a zero base produces zero tax and that
// the rate is reported all the same.
func TestCalculateTaxZeroBase(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", Amount: 0}},
	})
	require.NoError(t, err)

	line := taxOfItem(t, result, "li_1")
	assert.Equal(t, int64(0), line.TaxAmount)
	assert.Equal(t, int32(2000), line.RateBps, "a zero base must not make the rate invisible")
	requireTotalIdentity(t, result)
}

// TestCalculateTaxCartWithoutLineItems checks that a calculation with no line
// items is valid.
func TestCalculateTaxCartWithoutLineItems(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{CountryCode: "TR"})
	require.NoError(t, err)

	assert.Empty(t, result.Items)
	assert.Equal(t, int64(0), result.TaxTotal)
	assert.Equal(t, ShippingLineID, result.Shipping.ID)
	requireTotalIdentity(t, result)
}

// TestCalculateTaxShippingIsNotTaxedByDefault checks that the cart flow's
// current contract is kept.
func TestCalculateTaxShippingIsNotTaxedByDefault(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", Amount: 10_000}},
		Shipping:    ShippingInput{OptionID: "sopt_1", Amount: 5000},
	})
	require.NoError(t, err)

	assert.Equal(t, int64(0), result.Shipping.TaxAmount, "shipping must NOT ENTER the base")
	assert.Equal(t, int64(0), result.Shipping.TaxableAmount)
	assert.Equal(t, int64(2000), result.TaxTotal)
	requireTotalIdentity(t, result)
}

// TestCalculateTaxShippingIsTaxedWhenAskedFor checks that an explicit request
// puts shipping into the base and that it falls back to the default rate.
func TestCalculateTaxShippingIsTaxedWhenAskedFor(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", Amount: 10_000}},
		Shipping:    ShippingInput{OptionID: "sopt_1", Amount: 5000, Taxable: true},
	})
	require.NoError(t, err)

	assert.Equal(t, ShippingLineID, result.Shipping.ID)
	assert.Equal(t, int64(5000), result.Shipping.TaxableAmount)
	assert.Equal(t, int64(1000), result.Shipping.TaxAmount)
	assert.Equal(t, int64(3000), result.TaxTotal)
	requireTotalIdentity(t, result)
}

// TestCalculateTaxShippingChoosesItsOwnRate checks that a "shipping_option"
// rule applies to the shipping line and NOT to the line items.
func TestCalculateTaxShippingChoosesItsOwnRate(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)
	repo.seedRuledRate(rateB, trRegionID, 800)
	repo.seedRule(ruleA, rateB, models.ReferenceShippingOption, "sopt_express")

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", ProductID: "sopt_express", Amount: 10_000}},
		Shipping:    ShippingInput{OptionID: "sopt_express", Amount: 10_000, Taxable: true},
	})
	require.NoError(t, err)

	assert.Equal(t, int32(800), result.Shipping.RateBps, "shipping has to fall to its own rule")
	assert.Equal(t, int32(2000), taxOfItem(t, result, "li_1").RateBps,
		"the shipping rule must not apply to a PRODUCT carrying the same id")
	requireTotalIdentity(t, result)
}

// TestCalculateTaxRoundsDown checks that the rounding direction is in the
// customer's favor.
func TestCalculateTaxRoundsDown(t *testing.T) {
	tests := []struct {
		name    string
		base    int64
		rateBps int32
		want    int64
	}{
		// 1999 × 18% = 359.82 -> 359
		{"a fractional remainder goes down", 1999, 1800, 359},
		// 1 × 20% = 0.2 -> 0
		{"below one cent becomes zero", 1, 2000, 0},
		// 5 × 50% = 2.5 -> 2 (rounding to nearest would give 3)
		{"an exact half goes down", 5, 5000, 2},
		// 10000 × 20% = 2000 -> exact
		{"an exact division does not change", 10_000, 2000, 2000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, repo := newTestService(t)
			repo.seedRootRegion(trRegionID, "TR")
			repo.seedDefaultRate(rateA, trRegionID, tt.rateBps)

			result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
				CountryCode: "TR",
				Items:       []TaxableItem{{ID: "li_1", Amount: tt.base}},
			})
			require.NoError(t, err)
			assert.Equal(t, tt.want, taxOfItem(t, result, "li_1").TaxAmount)
			requireTotalIdentity(t, result)
		})
	}
}

// TestCalculateTaxPerLineRoundingDifference pins the documented DIVERGENCE
// numerically.
//
// Three line items of 333 each at a rate of 20%: 66 per line (333×0.2 = 66.6),
// 198 in total. Computed over the whole cart it would have been 999×0.2 =
// 199.8 -> 199. The difference is exactly 1 minor unit and stays IN THE
// CUSTOMER'S FAVOR.
func TestCalculateTaxPerLineRoundingDifference(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items: []TaxableItem{
			{ID: "li_1", Amount: 333},
			{ID: "li_2", Amount: 333},
			{ID: "li_3", Amount: 333},
		},
	})
	require.NoError(t, err)

	for _, id := range []string{"li_1", "li_2", "li_3"} {
		assert.Equal(t, int64(66), taxOfItem(t, result, id).TaxAmount)
	}
	assert.Equal(t, int64(198), result.TaxTotal)

	inOneGo, err := TaxOf(999, 2000)
	require.NoError(t, err)
	assert.Equal(t, int64(199), inOneGo)
	assert.Less(t, result.TaxTotal, inOneGo,
		"the per-line calculation has to be LESS than or equal to the one over the cart base")
}

// TestCalculateTaxRejectsOverflow checks that the int64 bounds are applied at
// both the line and the total level.
func TestCalculateTaxRejectsOverflow(t *testing.T) {
	t.Run("a line base above the ceiling", func(t *testing.T) {
		svc, repo := newTestService(t)
		repo.seedRootRegion(trRegionID, "TR")
		repo.seedDefaultRate(rateA, trRegionID, 2000)

		_, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
			CountryCode: "TR",
			Items:       []TaxableItem{{ID: "li_1", Amount: MaxTaxableAmount + 1}},
		})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err))
		assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))
		assert.Zero(t, repo.callCount("ResolveTaxRegions"), "an input error must not go to the database")
	})

	t.Run("a negative line base", func(t *testing.T) {
		svc, repo := newTestService(t)
		repo.seedRootRegion(trRegionID, "TR")

		_, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
			CountryCode: "TR",
			Items:       []TaxableItem{{ID: "li_1", Amount: -1}},
		})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err))
		assert.Zero(t, repo.callCount("ResolveTaxRegions"))
	})

	t.Run("a tax total above the ceiling", func(t *testing.T) {
		svc, repo := newTestService(t)
		repo.seedRootRegion(trRegionID, "TR")
		repo.seedDefaultRate(rateA, trRegionID, models.MaxRateBps)

		// Each line item is valid on its own; at a 100% rate the total tax
		// exceeds the ceiling.
		items := make([]TaxableItem, 0, 3)
		for i := range 3 {
			items = append(items, TaxableItem{
				ID:     fmt.Sprintf("li_%d", i),
				Amount: MaxTaxableAmount / 2,
			})
		}

		_, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
			CountryCode: "TR",
			Items:       items,
		})
		require.Error(t, err)
		assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))
	})
}

// TestCalculateTaxRejectsInvalidInput checks that input validation is done
// WITHOUT GOING to the database.
func TestCalculateTaxRejectsInvalidInput(t *testing.T) {
	tests := map[string]CalculateTaxInput{
		"empty country code":          {Items: []TaxableItem{{ID: "li_1", Amount: 1}}},
		"three-letter country code":   {CountryCode: "TUR"},
		"country code with a digit":   {CountryCode: "T1"},
		"province code with a hyphen": {CountryCode: "US", ProvinceCode: "-CA"},
		"province code too long":      {CountryCode: "US", ProvinceCode: "CALIFORNIAAA"},
		"empty line item id": {
			CountryCode: "TR",
			Items:       []TaxableItem{{Amount: 1}},
		},
		"repeated line item id": {
			CountryCode: "TR",
			Items:       []TaxableItem{{ID: "li_1", Amount: 1}, {ID: "li_1", Amount: 2}},
		},
		"line item id reserved for shipping": {
			CountryCode: "TR",
			Items:       []TaxableItem{{ID: ShippingLineID, Amount: 1}},
		},
		"negative shipping amount": {
			CountryCode: "TR",
			Shipping:    ShippingInput{Amount: -5, Taxable: true},
		},
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			svc, repo := newTestService(t)
			repo.seedRootRegion(trRegionID, "TR")

			_, err := svc.CalculateTax(context.Background(), in)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "error: %v", err)
			assert.Zero(t, repo.callCount("ResolveTaxRegions"))
		})
	}
}

// TestCalculateTaxBoundsTheNumberOfLineItems checks that an unbounded list is
// rejected.
func TestCalculateTaxBoundsTheNumberOfLineItems(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")

	items := make([]TaxableItem, 0, MaxItems+1)
	for i := range MaxItems + 1 {
		items = append(items, TaxableItem{ID: fmt.Sprintf("li_%d", i), Amount: 1})
	}

	_, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       items,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Zero(t, repo.callCount("ResolveTaxRegions"))
}

// TestCalculateTaxQueryCountIsIndependentOfLineCount proves that there is no
// N+1.
func TestCalculateTaxQueryCountIsIndependentOfLineCount(t *testing.T) {
	counts := map[int]int{}

	for _, itemCount := range []int{1, 50} {
		svc, repo := newTestService(t)
		repo.seedRootRegion(trRegionID, "TR")
		repo.seedProvinceRegion(trIstanbul, "TR", "34", trRegionID)
		repo.seedDefaultRate(rateA, trRegionID, 2000)
		repo.seedRuledRate(rateB, trIstanbul, 100)
		repo.seedRule(ruleA, rateB, models.ReferenceProduct, "prod_1")

		items := make([]TaxableItem, 0, itemCount)
		for i := range itemCount {
			items = append(items, TaxableItem{
				ID: fmt.Sprintf("li_%d", i), ProductID: "prod_1", Amount: 1000,
			})
		}

		_, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
			CountryCode: "TR", ProvinceCode: "34", Items: items,
		})
		require.NoError(t, err)

		counts[itemCount] = repo.callCount("ResolveTaxRegions") +
			repo.callCount("ListTaxRatesByRegions") +
			repo.callCount("ListTaxRateRulesByRates")
	}

	assert.Equal(t, 3, counts[1], "region + rate + rule: three queries")
	assert.Equal(t, counts[1], counts[50],
		"the number of queries must NOT GROW with the number of line items (no N+1)")
}

// TestCalculateTaxAnUnknownProviderIsASetupError checks that a region pointing
// to a provider that is not registered does NOT silently FALL BACK to local.
func TestCalculateTaxAnUnknownProviderIsASetupError(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRegion(models.TaxRegion{ID: trRegionID, CountryCode: "TR", ProviderID: "avalara"})
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	_, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", Amount: 10_000}},
	})
	require.Error(t, err)
	assert.Equal(t, CodeProviderMisconfigured, errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindInternal),
		"a setup error has to come back as a server error, not as a 404 to the client")
}

// TestCalculateTaxInheritsTheProviderAlongTheChain checks that a province
// region does not silently DROP the country's EXTERNAL tax provider to LOCAL.
//
// The scenario is an INVISIBLE money bug: while the country root is bound to an
// external authority, if a province region opened for a single exception
// keeps an empty provider_id, every cart in that province is taxed by the
// wrong authority and the invoice comes out wrong. The fake providers
// deliberately return amounts the local calculation CANNOT PRODUCE; the amount
// in the result is the proof of who made the calculation.
func TestCalculateTaxInheritsTheProviderAlongTheChain(t *testing.T) {
	const provinceCode = "34"

	avalara := &stubProvider{id: "avalara", result: ProviderResult{
		Items: []ProviderItemTax{{ID: "li_1", RateBps: 1000, TaxAmount: 999}},
	}}
	taxjar := &stubProvider{id: "taxjar", result: ProviderResult{
		Items: []ProviderItemTax{{ID: "li_1", RateBps: 500, TaxAmount: 111}},
	}}

	// build produces a service whose country is bound to "avalara" and whose
	// province carries the given provider.
	build := func(t *testing.T, provinceProvider string) *Service {
		t.Helper()

		repo := newMemRepo()
		repo.seedRegion(models.TaxRegion{ID: trRegionID, CountryCode: "TR", ProviderID: "avalara"})
		province, parent := provinceCode, trRegionID
		repo.seedRegion(models.TaxRegion{
			ID:           trIstanbul,
			CountryCode:  "TR",
			ProvinceCode: &province,
			ParentID:     &parent,
			ProviderID:   provinceProvider,
		})
		// The province has its own default rate: a calculation that falls back
		// to local finds 725 and is distinguishable from the fake providers'
		// amounts.
		repo.seedDefaultRate(rateA, trIstanbul, 725)

		registry := NewProviderRegistry()
		require.NoError(t, registry.Register(NewLocalProvider(repo)))
		require.NoError(t, registry.Register(avalara))
		require.NoError(t, registry.Register(taxjar))
		return New(repo, Options{Providers: registry, Now: func() time.Time { return testNow }})
	}

	// calculate runs a single-line calculation for the province.
	calculate := func(t *testing.T, svc *Service) CalculateTaxResult {
		t.Helper()

		result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
			CountryCode:  "TR",
			ProvinceCode: provinceCode,
			Items:        []TaxableItem{{ID: "li_1", Amount: 10_000}},
		})
		require.NoError(t, err)
		return result
	}

	t.Run("a province left empty inherits the country's provider", func(t *testing.T) {
		result := calculate(t, build(t, ""))

		assert.Equal(t, "avalara", result.ProviderID,
			"the province's empty provider_id must NOT DROP the country's external authority to local")
		line := taxOfItem(t, result, "li_1")
		assert.Equal(t, int64(999), line.TaxAmount, "the country's provider has to make the calculation")
		assert.Equal(t, int32(1000), line.RateBps)
		assert.Equal(t, trIstanbul, result.RegionID, "the calculation still rests on the MOST SPECIFIC region")
		requireTotalIdentity(t, result)
	})

	t.Run("a province can choose the local calculation EXPLICITLY", func(t *testing.T) {
		result := calculate(t, build(t, LocalProviderID))

		assert.Equal(t, LocalProviderID, result.ProviderID,
			"inheritance must not prevent local from being chosen explicitly")
		line := taxOfItem(t, result, "li_1")
		assert.Equal(t, int32(725), line.RateBps)
		assert.Equal(t, int64(725), line.TaxAmount)
		requireTotalIdentity(t, result)
	})

	t.Run("the province's own provider overrides the country", func(t *testing.T) {
		result := calculate(t, build(t, "taxjar"))

		assert.Equal(t, "taxjar", result.ProviderID)
		assert.Equal(t, int64(111), taxOfItem(t, result, "li_1").TaxAmount)
		requireTotalIdentity(t, result)
	})
}

// TestCalculateTaxValidatesTheProviderResult checks that an external provider's
// out-of-contract output does NOT LEAK into the cart total.
func TestCalculateTaxValidatesTheProviderResult(t *testing.T) {
	tests := map[string]ProviderResult{
		"tax larger than the base": {
			Items: []ProviderItemTax{{ID: "li_1", RateBps: 2000, TaxAmount: 10_001}},
		},
		"negative tax": {
			Items: []ProviderItemTax{{ID: "li_1", RateBps: 2000, TaxAmount: -1}},
		},
		"rate out of range": {
			Items: []ProviderItemTax{{ID: "li_1", RateBps: 20_000, TaxAmount: 10}},
		},
		"missing line item": {
			Items: []ProviderItemTax{},
		},
		"unknown line item": {
			Items: []ProviderItemTax{{ID: "li_unknown", RateBps: 0, TaxAmount: 0}},
		},
		"repeated line item": {
			Items: []ProviderItemTax{
				{ID: "li_1", RateBps: 0, TaxAmount: 0},
				{ID: "li_1", RateBps: 0, TaxAmount: 0},
			},
		},
		"shipping tax written onto untaxed shipping": {
			Items:    []ProviderItemTax{{ID: "li_1", RateBps: 2000, TaxAmount: 2000}},
			Shipping: ProviderItemTax{ID: ShippingLineID, RateBps: 2000, TaxAmount: 500},
		},
	}

	for name, result := range tests {
		t.Run(name, func(t *testing.T) {
			repo := newMemRepo()
			repo.seedRegion(models.TaxRegion{ID: trRegionID, CountryCode: "TR", ProviderID: "fake"})

			registry := NewProviderRegistry()
			require.NoError(t, registry.Register(&stubProvider{id: "fake", result: result}))
			svc := New(repo, Options{Providers: registry, Now: func() time.Time { return testNow }})

			_, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
				CountryCode: "TR",
				Items:       []TaxableItem{{ID: "li_1", Amount: 10_000}},
				Shipping:    ShippingInput{Amount: 5000},
			})
			require.Error(t, err)
			assert.Equal(t, CodeProviderInvalidResult, errors.CodeOf(err))
			assert.True(t, errors.HasKind(err, errors.KindInternal))
		})
	}
}

// TestDefaultRateForCountry checks the behavior of the plain path.
func TestDefaultRateForCountry(t *testing.T) {
	t.Run("the default rate comes back", func(t *testing.T) {
		svc, repo := newTestService(t)
		repo.seedRootRegion(trRegionID, "TR")
		repo.seedDefaultRate(rateA, trRegionID, 2000)
		repo.seedRuledRate(rateB, trRegionID, 100)

		rate, found, err := svc.DefaultRateForCountry(context.Background(), "tr")
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, int32(2000), rate, "a ruled rate must not show on the plain path")
	})

	t.Run("found is false without a region", func(t *testing.T) {
		svc, _ := newTestService(t)

		rate, found, err := svc.DefaultRateForCountry(context.Background(), "DE")
		require.NoError(t, err)
		assert.False(t, found)
		assert.Equal(t, int32(0), rate)
	})

	t.Run("found is false without a default rate", func(t *testing.T) {
		svc, repo := newTestService(t)
		repo.seedRootRegion(trRegionID, "TR")
		repo.seedRuledRate(rateB, trRegionID, 100)

		_, found, err := svc.DefaultRateForCountry(context.Background(), "TR")
		require.NoError(t, err)
		assert.False(t, found, "a region with only a ruled rate must give no rate on the plain path")
	})

	t.Run("the province rate does not show on the plain path", func(t *testing.T) {
		svc, repo := newTestService(t)
		repo.seedRootRegion(usRegionID, "US")
		repo.seedProvinceRegion(trIstanbul, "US", "CA", usRegionID)
		repo.seedDefaultRate(rateA, usRegionID, 2000)
		repo.seedDefaultRate(rateB, trIstanbul, 725)

		rate, found, err := svc.DefaultRateForCountry(context.Background(), "US")
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, int32(2000), rate)
	})

	t.Run("an out-of-contract rate returns an error", func(t *testing.T) {
		svc, repo := newTestService(t)
		repo.seedRootRegion(trRegionID, "TR")
		repo.seedRate(models.TaxRate{
			ID: rateA, TaxRegionID: trRegionID, Name: "broken", RateBps: 99_999, IsDefault: true,
		})

		_, _, err := svc.DefaultRateForCountry(context.Background(), "TR")
		require.Error(t, err)
		assert.Equal(t, CodeRateOutOfRange, errors.CodeOf(err))
	})

	t.Run("an invalid country code is rejected", func(t *testing.T) {
		svc, repo := newTestService(t)

		_, _, err := svc.DefaultRateForCountry(context.Background(), "TUR")
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err))
		assert.Zero(t, repo.callCount("ResolveTaxRegions"))
	})
}

// stubProvider is a fake provider that returns a scripted result.
//
// Its purpose is to prove that the service VALIDATES the provider's output;
// since the local provider never produces out-of-contract output, this path
// can be tested only with a fake.
type stubProvider struct {
	id     string
	result ProviderResult
}

var _ TaxProvider = (*stubProvider)(nil)

// ID returns the provider's id.
func (p *stubProvider) ID() string { return p.id }

// Calculate returns the scripted result.
func (p *stubProvider) Calculate(_ context.Context, _ ProviderInput) (ProviderResult, error) {
	return p.result, nil
}
