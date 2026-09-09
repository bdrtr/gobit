package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// includeTax builds a flag pointer.
func includeTax(v bool) *bool { return &v }

// TestCalculateTaxExtractsTheBaseFromTheGrossInAnInclusiveMarket verifies that
// the computation the measurement asked for is the one that runs.
//
// The 20% VAT inside 19,900 is 3,316 and 16,584 is left. Together they are
// 19,900 — the figure on the sticker. In a tax-exclusive market the same amount
// would be a base of 19,900 plus 3,980 of tax; what decides between the two
// answers is the FLAG, not the input.
func TestCalculateTaxExtractsTheBaseFromTheGrossInAnInclusiveMarket(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRegion(models.TaxRegion{
		ID: trRegionID, CountryCode: "TR", PricesIncludeTax: includeTax(true),
	})
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", ProductID: "prod_1", Amount: 19_900}},
	})
	require.NoError(t, err)

	assert.True(t, result.PricesIncludeTax,
		"the result has to SAY which mode it ran in; the caller reads the base against it")

	line := taxOfItem(t, result, "li_1")
	assert.Equal(t, int64(3_316), line.TaxAmount)
	assert.Equal(t, int64(16_584), line.TaxableAmount)
	assert.Equal(t, int64(19_900), line.TaxableAmount+line.TaxAmount,
		"base and tax have to hit the gross that was sent — the whole feature is this")
	requireTotalIdentity(t, result)
}

// TestCalculateTaxInheritsTheInclusiveFlagFromTheCountry verifies that the walk
// is the provider's walk.
//
// Not inventing a second inheritance rule was the decision: the provider says
// "empty means inherit", the flag says "nil means inherit". A province with
// nothing to say takes its country's answer.
func TestCalculateTaxInheritsTheInclusiveFlagFromTheCountry(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRegion(models.TaxRegion{
		ID: trRegionID, CountryCode: "TR", PricesIncludeTax: includeTax(true),
	})
	province := repo.seedProvinceRegion("taxreg_34", "TR", "34", trRegionID)
	require.Nil(t, province.PricesIncludeTax, "the province says nothing of its own")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR", ProvinceCode: "34",
		Items: []TaxableItem{{ID: "li_1", ProductID: "prod_1", Amount: 19_900}},
	})
	require.NoError(t, err)

	assert.True(t, result.PricesIncludeTax, "a silent province has to take its country's answer")
	assert.Equal(t, int64(16_584), taxOfItem(t, result, "li_1").TaxableAmount)
}

// TestCalculateTaxLetsAProvinceOverrideTheCountry pins the other direction.
//
// Sending FALSE is not the same as sending nothing: it OVERRIDES an inherited
// true. The pointer exists for exactly this distinction — with a plain bool a
// province could not say "whatever my country says".
func TestCalculateTaxLetsAProvinceOverrideTheCountry(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRegion(models.TaxRegion{
		ID: trRegionID, CountryCode: "TR", PricesIncludeTax: includeTax(true),
	})
	province := "34"
	parent := trRegionID
	repo.seedRegion(models.TaxRegion{
		ID: "taxreg_34", CountryCode: "TR", ProvinceCode: &province, ParentID: &parent,
		PricesIncludeTax: includeTax(false),
	})
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR", ProvinceCode: "34",
		Items: []TaxableItem{{ID: "li_1", ProductID: "prod_1", Amount: 19_900}},
	})
	require.NoError(t, err)

	assert.False(t, result.PricesIncludeTax, "the province's FALSE has to beat the country's TRUE")
	line := taxOfItem(t, result, "li_1")
	assert.Equal(t, int64(19_900), line.TaxableAmount, "in exclusive mode the base is the amount itself")
	assert.Equal(t, int64(3_980), line.TaxAmount)
}

// TestCalculateTaxIsExclusiveWhenNobodySaysOtherwise verifies that the
// behavior from before the column is preserved.
//
// An existing row must not acquire an opinion nobody wrote for it: before the
// column existed every installation computed tax-exclusive, and NULL has to
// keep meaning that.
func TestCalculateTaxIsExclusiveWhenNobodySaysOtherwise(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", ProductID: "prod_1", Amount: 3_000}},
	})
	require.NoError(t, err)

	assert.False(t, result.PricesIncludeTax)
	line := taxOfItem(t, result, "li_1")
	assert.Equal(t, int64(3_000), line.TaxableAmount)
	assert.Equal(t, int64(600), line.TaxAmount)
}
