package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// componentProvider is an external provider that returns whatever breakdown the
// test hands it.
//
// It exists to try the CONTRACT rather than the local arithmetic: the local
// provider can only produce breakdowns that are already correct, so the refusals
// below would be unreachable through it.
type componentProvider struct {
	components []TaxComponent
	tax        int64
}

// ID returns the provider's identifier.
func (p *componentProvider) ID() string { return "component_test" }

// Calculate writes the configured tax and breakdown onto every line.
func (p *componentProvider) Calculate(
	_ context.Context, in ProviderInput,
) (ProviderResult, error) {
	out := ProviderResult{
		Items:    make([]ProviderItemTax, 0, len(in.Items)),
		Shipping: ProviderItemTax{ID: ShippingLineID},
	}
	for i := range in.Items {
		out.Items = append(out.Items, ProviderItemTax{
			ID:            in.Items[i].ID,
			RateBps:       1000,
			TaxAmount:     p.tax,
			TaxableAmount: in.Items[i].Amount,
			Components:    p.components,
		})
	}
	return out, nil
}

// calculateWithComponents runs a calculation through a provider returning the
// given breakdown.
func calculateWithComponents(
	t *testing.T, components []TaxComponent, tax int64,
) (CalculateTaxResult, error) {
	t.Helper()

	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 1000)

	registry := NewProviderRegistry()
	require.NoError(t, registry.Register(&componentProvider{components: components, tax: tax}))
	svc.providers = registry

	region := repo.regions[trRegionID]
	region.ProviderID = "component_test"
	repo.seedRegion(region)

	return svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", Amount: 10_000}},
	})
}

// TestAStackedLineCarriesEveryComponent is the record the invoice will print.
//
// The line keeps carrying the stack's base rate; what is new is that the second
// rate and the amount taken under it are no longer lost.
func TestAStackedLineCarriesEveryComponent(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 500)
	repo.seedStackedRate(rateB, trRegionID, rateA, 800, true)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", ProductID: "prod_1", Amount: 12345}},
	})
	require.NoError(t, err)

	line := taxOfItem(t, result, "li_1")
	require.Len(t, line.Components, 2, "both rates that taxed the line must be there")

	assert.Equal(t, TaxComponent{
		RateID: rateA, RateBps: 500, Compound: false,
		TaxableAmount: 12345, TaxAmount: 617,
	}, line.Components[0], "the base comes first, on the line's own amount")
	assert.Equal(t, TaxComponent{
		RateID: rateB, RateBps: 800, Compound: true,
		TaxableAmount: 12962, TaxAmount: 1036,
	}, line.Components[1], "the compound component's base is 12345 + 617")

	assert.Equal(t, line.TaxAmount,
		line.Components[0].TaxAmount+line.Components[1].TaxAmount,
		"the components must add up to the line, or a document cannot print them "+
			"INSTEAD of the line's own figure")
}

// TestASingleRateLineCarriesNoComponents keeps "a breakdown is present" meaning
// "a stack taxed this line".
//
// A list of one would repeat what the line's own rate says, and every consumer
// would then have to compare a list against the line to learn nothing.
func TestASingleRateLineCarriesNoComponents(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", Amount: 10_000}},
	})
	require.NoError(t, err)

	line := taxOfItem(t, result, "li_1")
	assert.Empty(t, line.Components)
	assert.Equal(t, int32(2000), line.RateBps, "the one rate that applied is on the line")
}

// TestALineWithNoRateCarriesNoComponents covers the branch where selection
// found nothing: there is no rate, so there is nothing to break down.
func TestALineWithNoRateCarriesNoComponents(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", Amount: 10_000}},
	})
	require.NoError(t, err)

	line := taxOfItem(t, result, "li_1")
	assert.Empty(t, line.Components)
	assert.Zero(t, line.TaxAmount)
}

// TestComponentsThatDoNotAddUpAreRefused is the invariant the whole slice rests
// on.
//
// If this passes silently a document prints components whose sum is not what the
// customer was charged, and the two figures are read by two different people.
func TestComponentsThatDoNotAddUpAreRefused(t *testing.T) {
	_, err := calculateWithComponents(t, []TaxComponent{
		{RateBps: 500, TaxableAmount: 10_000, TaxAmount: 500},
		{RateBps: 800, TaxableAmount: 10_000, TaxAmount: 800},
	}, 1000)

	require.Error(t, err)
	assert.Equal(t, CodeProviderInvalidResult, coreerrors.CodeOf(err))
	assert.Contains(t, err.Error(), "do not add up")
}

// TestASingleComponentIsRefused keeps the empty list the only way to say "one
// rate".
func TestASingleComponentIsRefused(t *testing.T) {
	_, err := calculateWithComponents(t, []TaxComponent{
		{RateBps: 1000, TaxableAmount: 10_000, TaxAmount: 1000},
	}, 1000)

	require.Error(t, err)
	assert.Equal(t, CodeProviderInvalidResult, coreerrors.CodeOf(err))
	assert.Contains(t, err.Error(), "a single tax component")
}

// TestMoreComponentsThanTheStackLimitAreRefused applies the same depth limit to
// a provider that never walked a stack of ours.
func TestMoreComponentsThanTheStackLimitAreRefused(t *testing.T) {
	components := make([]TaxComponent, 0, maxStackDepth+1)
	for range maxStackDepth + 1 {
		components = append(components, TaxComponent{
			RateBps: 100, TaxableAmount: 10_000, TaxAmount: 200,
		})
	}

	_, err := calculateWithComponents(t, components, 200*int64(maxStackDepth+1))

	require.Error(t, err)
	assert.Equal(t, CodeProviderInvalidResult, coreerrors.CodeOf(err))
	assert.Contains(t, err.Error(), "at most")
}

// TestAComponentOutsideTheRateRangeIsRefused applies the line's own rate bound
// to every component.
func TestAComponentOutsideTheRateRangeIsRefused(t *testing.T) {
	_, err := calculateWithComponents(t, []TaxComponent{
		{RateBps: 10_001, TaxableAmount: 10_000, TaxAmount: 500},
		{RateBps: 800, TaxableAmount: 10_000, TaxAmount: 500},
	}, 1000)

	require.Error(t, err)
	assert.Equal(t, CodeProviderInvalidResult, coreerrors.CodeOf(err))
	assert.Contains(t, err.Error(), "out-of-contract rate in component 0")
}

// TestAComponentTakingMoreThanItsBaseIsRefused catches the unit mix-up one
// component at a time.
//
// The line-level check cannot: a component may be wrong while the line's total
// stays inside the line's own amount.
func TestAComponentTakingMoreThanItsBaseIsRefused(t *testing.T) {
	_, err := calculateWithComponents(t, []TaxComponent{
		{RateBps: 500, TaxableAmount: 100, TaxAmount: 900},
		{RateBps: 800, TaxableAmount: 10_000, TaxAmount: 100},
	}, 1000)

	require.Error(t, err)
	assert.Equal(t, CodeProviderInvalidResult, coreerrors.CodeOf(err))
	assert.Contains(t, err.Error(), "out-of-contract tax in component 0")
}

// TestAProviderThatKnowsNothingOfComponentsStaysCorrect is the compatibility
// claim, and it is the reason the empty list is legal rather than an error.
func TestAProviderThatKnowsNothingOfComponentsStaysCorrect(t *testing.T) {
	result, err := calculateWithComponents(t, nil, 1000)

	require.NoError(t, err)
	line := taxOfItem(t, result, "li_1")
	assert.Empty(t, line.Components)
	assert.Equal(t, int64(1000), line.TaxAmount)
}

// TestTheInteropSchemaCarriesTheComponents pins the WIRE names.
//
// The conversion into the schema is a direct struct conversion, so the compiler
// already refuses to drop a field; what it cannot check is the names on the
// wire, and the consumer of this schema cannot import this package to find out.
func TestTheInteropSchemaCarriesTheComponents(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 500)
	repo.seedStackedRate(rateB, trRegionID, rateA, 800, true)

	raw, err := NewInterop(svc).CalculateTaxJSON(context.Background(),
		[]byte(`{"country_code":"TR","items":[{"id":"li_1","amount":12345}]}`))
	require.NoError(t, err)

	var body struct {
		Items []struct {
			RateBps    int32 `json:"rate_bps"`
			Components []struct {
				RateID        string `json:"rate_id"`
				RateBps       int32  `json:"rate_bps"`
				Compound      bool   `json:"compound"`
				TaxableAmount int64  `json:"taxable_amount"`
				TaxAmount     int64  `json:"tax_amount"`
			} `json:"components"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	require.Len(t, body.Items, 1)

	assert.Equal(t, int32(500), body.Items[0].RateBps, "the line still carries the base")
	require.Len(t, body.Items[0].Components, 2)
	assert.Equal(t, rateB, body.Items[0].Components[1].RateID)
	assert.Equal(t, int32(800), body.Items[0].Components[1].RateBps)
	assert.True(t, body.Items[0].Components[1].Compound)
	assert.Equal(t, int64(12962), body.Items[0].Components[1].TaxableAmount)
	assert.Equal(t, int64(1036), body.Items[0].Components[1].TaxAmount)
}

// TestTheInteropSchemaOmitsAnEmptyBreakdown keeps the common line's body the
// size it was.
//
// Almost every line is taxed by one rate, and a "components": null on each of
// them would be bytes on every cart round trip saying nothing.
func TestTheInteropSchemaOmitsAnEmptyBreakdown(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	raw, err := NewInterop(svc).CalculateTaxJSON(context.Background(),
		[]byte(`{"country_code":"TR","items":[{"id":"li_1","amount":10000}]}`))
	require.NoError(t, err)

	assert.NotContains(t, string(raw), "components",
		"a single-rate line must not carry the key at all")
}
