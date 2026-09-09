package service

// This file is English because ADR 0012 makes language a property of the FILE
// and every new file is English.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// seedStackedRate writes a rate standing on another.
func (m *memRepo) seedStackedRate(id, regionID, baseID string, rateBps int32, compound bool) models.TaxRate {
	base := baseID

	return m.seedRate(models.TaxRate{
		ID: id, TaxRegionID: regionID, Name: "stacked", RateBps: rateBps,
		StacksOnID: &base, Compound: compound,
	})
}

// TestTwoTaxesSideBySideAreBothComputedOnTheLine is the simple stack: neither
// component compounds, so both take the line's own amount.
func TestTwoTaxesSideBySideAreBothComputedOnTheLine(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 500)
	repo.seedStackedRate(rateB, trRegionID, rateA, 800, false)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", ProductID: "prod_1", Amount: 12345}},
	})
	require.NoError(t, err)

	line := taxOfItem(t, result, "li_1")
	// TaxOf(12345, 500) = 617 (617.25 floored) and TaxOf(12345, 800) = 987
	// (987.6 floored). Neither is computed on the other.
	assert.Equal(t, int64(617+987), line.TaxAmount)
	assert.Equal(t, int64(12345), line.TaxableAmount,
		"the base of a tax-exclusive line is the line's own amount")
	assert.Equal(t, int32(500), line.RateBps,
		"the rate the line carries is the stack's BASE: really applied, on a really "+
			"recorded amount")
}

// TestACompoundTaxIsComputedOnTheTaxBelowIt is the whole point of a stack.
//
// The numbers are worked by hand in minor units, and the difference from the
// side-by-side case is the compounding itself: 1036 rather than 987.
func TestACompoundTaxIsComputedOnTheTaxBelowIt(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 500)
	repo.seedStackedRate(rateB, trRegionID, rateA, 800, true)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", ProductID: "prod_1", Amount: 12345}},
	})
	require.NoError(t, err)

	// c1: TaxOf(12345, 500)          = 617   (617.25 -> floor)
	// c2: TaxOf(12345 + 617, 800)    = 1036  (1036.96 -> floor, on 12962)
	assert.Equal(t, int64(617+1036), taxOfItem(t, result, "li_1").TaxAmount,
		"the second component stands on the first: 12345 + 617 is its base")
}

// TestEachComponentIsRoundedOnItsOwn pins the rounding rule.
//
// One floor at the end would give a different number, and it would also make
// the per-component figures an invoice prints fail to add up to the line.
func TestEachComponentIsRoundedOnItsOwn(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 333)
	repo.seedStackedRate(rateB, trRegionID, rateA, 333, false)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", ProductID: "prod_1", Amount: 1000}},
	})
	require.NoError(t, err)

	// Each component is 33.3 and floors to 33; a single floor over the summed
	// 6.66% would give 66, and the two differ by one unit in the customer's
	// favor — the direction the module's rounding always takes.
	assert.Equal(t, int64(66), taxOfItem(t, result, "li_1").TaxAmount)
	assert.Equal(t, int64(33+33), taxOfItem(t, result, "li_1").TaxAmount,
		"the line's tax is the SUM of the per-component floors")
}

// TestAStackedRateIsNeverCHOSEN keeps the selection rules untouched.
//
// The stacked rate carries the higher rate and would win any comparison it
// entered. It never enters one: it is reached only by expanding the rate that
// was chosen.
func TestAStackedRateIsNeverCHOSEN(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 500)
	repo.seedRuledRate(rateB, trRegionID, 100)
	repo.seedRule(ruleA, rateB, models.ReferenceProduct, "prod_1")
	// Standing on the RULED rate, so a product matching that rule gets both.
	repo.seedStackedRate(rateC, trRegionID, rateB, 900, false)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items: []TaxableItem{
			{ID: "li_1", ProductID: "prod_1", Amount: 10000},
			{ID: "li_2", ProductID: "prod_2", Amount: 10000},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, int64(100+900), taxOfItem(t, result, "li_1").TaxAmount,
		"the ruled rate was chosen and its stack came with it")
	assert.Equal(t, int64(500), taxOfItem(t, result, "li_2").TaxAmount,
		"a line matching no rule gets the DEFAULT and not the stacked rate, which is "+
			"the higher one and would have won any comparison it entered")
}

// TestAStackInATaxInclusiveRegionIsRefusedAtCalculation is the loud stop.
//
// Both writes that could produce this pair refuse it, so reaching the
// calculation means one of those gates was gone around — and a wrong number is
// worse than an error.
func TestAStackInATaxInclusiveRegionIsRefusedAtCalculation(t *testing.T) {
	svc, repo := newTestService(t)
	includes := true
	repo.seedRegion(models.TaxRegion{
		ID: trRegionID, CountryCode: "TR", PricesIncludeTax: &includes,
	})
	repo.seedDefaultRate(rateA, trRegionID, 500)
	repo.seedStackedRate(rateB, trRegionID, rateA, 800, false)

	_, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", ProductID: "prod_1", Amount: 12000}},
	})

	require.Error(t, err)
	assert.Equal(t, CodeInconsistentConfig, coreerrors.CodeOf(err))
}

// TestAStackDeeperThanTheLimitIsRefused stops a chain nobody can verify.
//
// The write guard refuses the fifth component, so this configuration can only
// arrive past the service — by hand, in SQL. The calculation then stops rather
// than silently pricing the first four: a line taxed by four of five rates is a
// wrong number that looks like a right one.
//
// It is also the only reachable failure of the walk. A CYCLE is not: the walk
// starts at the SELECTED rate, a selected rate stands on nothing, and every
// step follows a rate's single base — so the chain cannot return to a start
// that has no base. The guard for it was written, measured unreachable, and
// deleted.
func TestAStackDeeperThanTheLimitIsRefused(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedRuledRate(rateA, trRegionID, 100)
	repo.seedRule(ruleA, rateA, models.ReferenceProduct, "prod_1")
	repo.seedStackedRate(rateB, trRegionID, rateA, 100, false)
	repo.seedStackedRate(rateC, trRegionID, rateB, 100, false)
	repo.seedStackedRate(rateD, trRegionID, rateC, 100, false)
	repo.seedStackedRate(models.TaxRateIDPrefix+"E0000000000000000000000000",
		trRegionID, rateD, 100, false)

	_, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", ProductID: "prod_1", Amount: 10000}},
	})

	require.Error(t, err)
	assert.Equal(t, CodeInconsistentConfig, coreerrors.CodeOf(err))
}

// TestAStackCannotTakeMoreThanTheLine is the guard at the write.
//
// Two legal rates take 1.2x the line together. Without the guard the
// calculation would answer a tax greater than the amount, validateLine would
// call it a PROVIDER fault, and the merchant would read an accusation against
// the provider for their own configuration.
func TestAStackCannotTakeMoreThanTheLine(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedRuledRate(rateA, trRegionID, 6000)

	_, err := svc.CreateTaxRate(context.Background(), CreateTaxRateInput{
		TaxRegionID: trRegionID, Name: "second", RateBps: 6000, StacksOnID: rateA,
	})

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))
	assert.Equal(t, CodeStackExceedsBase, coreerrors.CodeOf(err))
}

// TestADefaultRateCannotStandOnAnother keeps the two ways of applying a rate
// apart.
func TestADefaultRateCannotStandOnAnother(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedRuledRate(rateA, trRegionID, 500)

	_, err := svc.CreateTaxRate(context.Background(), CreateTaxRateInput{
		TaxRegionID: trRegionID, Name: "both", RateBps: 100,
		StacksOnID: rateA, IsDefault: true,
	})

	require.Error(t, err)
	assert.Equal(t, CodeStackNotAllowed, coreerrors.CodeOf(err))
}

// TestARateCannotCompoundOnNothing refuses the word without the relation.
func TestARateCannotCompoundOnNothing(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")

	_, err := svc.CreateTaxRate(context.Background(), CreateTaxRateInput{
		TaxRegionID: trRegionID, Name: "floating", RateBps: 100, Compound: true,
	})

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err))
	assert.Equal(t, CodeStackNotAllowed, coreerrors.CodeOf(err))
}

// TestAStackCannotBeBuiltWherePricesIncludeTheirTax is the refusal at the rate
// write; its sibling at the region write is in service_test.go's file.
func TestAStackCannotBeBuiltWherePricesIncludeTheirTax(t *testing.T) {
	svc, repo := newTestService(t)
	includes := true
	repo.seedRegion(models.TaxRegion{
		ID: trRegionID, CountryCode: "TR", PricesIncludeTax: &includes,
	})
	repo.seedRuledRate(rateA, trRegionID, 500)

	_, err := svc.CreateTaxRate(context.Background(), CreateTaxRateInput{
		TaxRegionID: trRegionID, Name: "on top", RateBps: 100, StacksOnID: rateA,
	})

	require.Error(t, err)
	assert.Equal(t, CodeStackNotAllowed, coreerrors.CodeOf(err))
}

// TestATaxInclusiveProvinceCannotBeOpenedOverAStack is the other order of the
// same forbidden pair.
//
// The rate write cannot see this one: the region does not exist yet when the
// stack is written.
func TestATaxInclusiveProvinceCannotBeOpenedOverAStack(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedRuledRate(rateA, trRegionID, 500)
	repo.seedStackedRate(rateB, trRegionID, rateA, 100, false)

	includes := true
	_, err := svc.CreateTaxRegion(context.Background(), CreateTaxRegionInput{
		CountryCode:      "TR",
		ProvinceCode:     "34",
		ParentID:         trRegionID,
		PricesIncludeTax: &includes,
	})

	require.Error(t, err)
	assert.Equal(t, CodeStackNotAllowed, coreerrors.CodeOf(err))
}
