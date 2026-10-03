package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// TestRateTableTheDefaultRateIsUnaffectedByItsRules checks that a rule written
// by hand does NOT NARROW the default rate's scope.
//
// The service and repository layers refuse to write a rule onto a default
// rate; this test shows that a record opened directly with SQL does not break
// the calculation.
func TestRateTableTheDefaultRateIsUnaffectedByItsRules(t *testing.T) {
	table := newRateTable(
		[]string{trRegionID},
		[]models.TaxRate{{ID: rateA, TaxRegionID: trRegionID, RateBps: 2000, IsDefault: true}},
		[]models.TaxRateRule{{ID: ruleA, TaxRateID: rateA, Reference: models.ReferenceProduct, ReferenceID: "prod_1"}},
	)

	rate, ok := table.selectRate(nil)
	require.True(t, ok, "a line item with no rule still has to fall to the default")
	assert.Equal(t, rateA, rate.ID)
}

// TestRateTableOnASecondDefaultTheSmallerIDWins checks that the result stays
// DETERMINISTIC even on a broken data set.
func TestRateTableOnASecondDefaultTheSmallerIDWins(t *testing.T) {
	for range 20 {
		table := newRateTable(
			[]string{trRegionID},
			[]models.TaxRate{
				{ID: rateC, TaxRegionID: trRegionID, RateBps: 1000, IsDefault: true},
				{ID: rateA, TaxRegionID: trRegionID, RateBps: 2000, IsDefault: true},
			},
			nil,
		)

		rate, ok := table.selectRate(nil)
		require.True(t, ok)
		assert.Equal(t, rateA, rate.ID, "the default with the smaller id has to be kept")
	}
}

// TestRateTableTheChainWalksFromTheMostSpecificToTheGeneral checks that the
// chain order decides the result.
func TestRateTableTheChainWalksFromTheMostSpecificToTheGeneral(t *testing.T) {
	rates := []models.TaxRate{
		{ID: rateA, TaxRegionID: trRegionID, RateBps: 2000, IsDefault: true},
		{ID: rateB, TaxRegionID: trIstanbul, RateBps: 800, IsDefault: true},
	}

	specificFirst := newRateTable([]string{trIstanbul, trRegionID}, rates, nil)
	rate, ok := specificFirst.selectRate(nil)
	require.True(t, ok)
	assert.Equal(t, rateB, rate.ID, "the head of the chain has to win")

	generalFirst := newRateTable([]string{trRegionID, trIstanbul}, rates, nil)
	rate, ok = generalFirst.selectRate(nil)
	require.True(t, ok)
	assert.Equal(t, rateA, rate.ID, "if the order is reversed the result has to reverse too")
}

// TestRateTableWithoutAMatchNoRateIsFound checks that a table that yields no
// rate at all produces zero tax.
func TestRateTableWithoutAMatchNoRateIsFound(t *testing.T) {
	table := newRateTable(
		[]string{trRegionID},
		[]models.TaxRate{{ID: rateB, TaxRegionID: trRegionID, RateBps: 100}},
		[]models.TaxRateRule{{ID: ruleA, TaxRateID: rateB, Reference: models.ReferenceProduct, ReferenceID: "prod_1"}},
	)

	_, ok := table.selectRate([]matchKey{{models.ReferenceProduct, "prod_other"}})
	assert.False(t, ok)

	applied, err := table.applyTo([]matchKey{{models.ReferenceProduct, "prod_other"}}, "li_1", 10_000, false)
	require.NoError(t, err)
	// The base is REPORTED even when no rate is found, and it is the amount
	// itself. In a tax-inclusive market the validation looks for the equality
	// "base + tax = amount sent"; a zero base would break that equality there,
	// and every line item without a rate would count as out of contract.
	assert.Equal(t, ProviderItemTax{ID: "li_1", TaxableAmount: 10_000}, applied,
		"without a rate the tax has to be zero, the id empty, and the base the amount itself")
}

// TestRateTableReferenceKindMatchingIsStrict checks that the same id does not
// match under a different reference kind.
//
// This is the rule that prevents the wrong rate from being applied when a
// product id and a shipping option id collide by accident.
func TestRateTableReferenceKindMatchingIsStrict(t *testing.T) {
	table := newRateTable(
		[]string{trRegionID},
		[]models.TaxRate{{ID: rateB, TaxRegionID: trRegionID, RateBps: 100}},
		[]models.TaxRateRule{{ID: ruleA, TaxRateID: rateB, Reference: models.ReferenceShippingOption, ReferenceID: "x_1"}},
	)

	_, ok := table.selectRate([]matchKey{{models.ReferenceProduct, "x_1"}})
	assert.False(t, ok, "a product key must not match a shipping rule")

	_, ok = table.selectRate([]matchKey{{models.ReferenceShippingOption, "x_1"}})
	assert.True(t, ok)
}

// TestItemKeysSkipsEmptyIDs checks that empty fields produce no keys.
func TestItemKeysSkipsEmptyIDs(t *testing.T) {
	assert.Empty(t, itemKeys(TaxableItem{ID: "li_1"}))
	assert.Equal(t,
		[]matchKey{{models.ReferenceProduct, "p"}},
		itemKeys(TaxableItem{ID: "li_1", ProductID: "p"}))
	assert.Equal(t,
		[]matchKey{{models.ReferenceProduct, "p"}, {models.ReferenceProductType, "t"}},
		itemKeys(TaxableItem{ID: "li_1", ProductID: "p", ProductTypeID: "t"}))
	assert.Nil(t, shippingKeys(ShippingInput{Amount: 100, Taxable: true}))
}

// TestLocalProviderMakesNoQueryWithoutARegion checks that no read is made at
// all while the chain is empty.
func TestLocalProviderMakesNoQueryWithoutARegion(t *testing.T) {
	repo := newMemRepo()
	provider := NewLocalProvider(repo)

	result, err := provider.Calculate(context.Background(), ProviderInput{
		CountryCode: "DE",
		Items:       []TaxableItem{{ID: "li_1", Amount: 1000}},
	})
	require.NoError(t, err)

	require.Len(t, result.Items, 1)
	assert.Equal(t, ProviderItemTax{ID: "li_1", TaxableAmount: 1_000}, result.Items[0],
		"without a region the base is the amount itself too")
	assert.Zero(t, repo.callCount("ListTaxRatesByRegions"))
	assert.Zero(t, repo.callCount("ListTaxRateRulesByRates"))
}

// TestLocalProviderMakesNoRuleQueryInARegionWithoutRules checks that the
// second round trip is skipped in a region that has only a default rate.
func TestLocalProviderMakesNoRuleQueryInARegionWithoutRules(t *testing.T) {
	repo := newMemRepo()
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)
	provider := NewLocalProvider(repo)

	_, err := provider.Calculate(context.Background(), ProviderInput{
		RegionIDs:   []string{trRegionID},
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", Amount: 1000}},
	})
	require.NoError(t, err)

	assert.Equal(t, 1, repo.callCount("ListTaxRatesByRegions"))
	assert.Zero(t, repo.callCount("ListTaxRateRulesByRates"),
		"without a ruled rate the rule query must not be made at all")
}

// TestLocalProviderSurfacesARateSourceError checks that a read error is not
// swallowed.
func TestLocalProviderSurfacesARateSourceError(t *testing.T) {
	repo := newMemRepo()
	repo.seedRootRegion(trRegionID, "TR")
	repo.failOn["ListTaxRatesByRegions"] = errors.Unavailable("db_down", "the database is unreachable")
	provider := NewLocalProvider(repo)

	_, err := provider.Calculate(context.Background(), ProviderInput{
		RegionIDs:   []string{trRegionID},
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", Amount: 1000}},
	})
	require.Error(t, err)
	assert.Equal(t, "db_down", errors.CodeOf(err))
}

// TestProviderRegistryRegistrationAndResolution checks the provider registry's
// contract.
func TestProviderRegistryRegistrationAndResolution(t *testing.T) {
	registry := NewProviderRegistry()
	local := NewLocalProvider(newMemRepo())
	require.NoError(t, registry.Register(local))

	t.Run("the same id is rejected a second time", func(t *testing.T) {
		err := registry.Register(NewLocalProvider(newMemRepo()))
		require.Error(t, err)
		assert.True(t, errors.IsConflict(err))
		assert.Equal(t, CodeProviderExists, errors.CodeOf(err))

		got, getErr := registry.Get(LocalProviderID)
		require.NoError(t, getErr)
		assert.Same(t, local, got, "on a conflict the EXISTING provider has to be kept")
	})

	t.Run("an empty id falls to the local provider", func(t *testing.T) {
		got, err := registry.Get("")
		require.NoError(t, err)
		assert.Equal(t, LocalProviderID, got.ID())
	})

	t.Run("an unknown id is NotFound", func(t *testing.T) {
		_, err := registry.Get("avalara")
		require.Error(t, err)
		assert.True(t, errors.IsNotFound(err))
		assert.Contains(t, err.Error(), LocalProviderID, "the message has to name the registered ids")
	})

	t.Run("a nil provider and one with an empty id are rejected", func(t *testing.T) {
		require.Error(t, registry.Register(nil))

		err := registry.Register(&stubProvider{id: "   "})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err))
	})

	t.Run("the id list is sorted", func(t *testing.T) {
		r := NewProviderRegistry()
		require.NoError(t, r.Register(&stubProvider{id: "zeta"}))
		require.NoError(t, r.Register(&stubProvider{id: "alfa"}))
		assert.Equal(t, []string{"alfa", "zeta"}, r.IDs())
	})
}
