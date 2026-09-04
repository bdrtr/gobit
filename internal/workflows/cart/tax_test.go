package cart

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
)

// TestCalculateTotalsTaxComesFromTaxModule verifies that when the tax surface is
// registered the tax is computed there and that the source is reported in the
// result.
func TestCalculateTotalsTaxComesFromTaxModule(t *testing.T) {
	h := newModuleHarness(t)
	h.taxes.rateBps = 1000 // 10%; the region fake carries 20% so a mix-up is visible.
	serveSnapshot(h.carts, twoLineCart(1))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	assert.Equal(t, TaxSourceTax, totals.TaxSource)
	assert.Equal(t, int64(275), totals.TaxTotal, "2000 x 10% + 750 x 10%")
	assert.NotEqual(t, int64(550), totals.TaxTotal, "the region's 20% rate must not be used")
	assert.Equal(t, 1, h.taxes.calls)
	requireIdentity(t, totals)
}

// TestCalculateTotalsTaxRequestShape verifies that the body going to tax conforms
// to the contract.
func TestCalculateTotalsTaxRequestShape(t *testing.T) {
	h := newModuleHarness(t)
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 2}},
		[]SnapshotShippingMethod{{ID: "csm_1", Amount: 3000}, {ID: "csm_2", Amount: 1990}}))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	require.Len(t, h.taxes.requests, 1)
	req := h.taxes.requests[0]
	assert.Equal(t, "TR", req.CountryCode, "the country comes from the region's country")
	assert.Empty(t, req.ProvinceCode, "the cart carries no province")
	require.Len(t, req.Items, 1)
	assert.Equal(t, taxRequestItem{ID: testLineA, Amount: 2000}, req.Items[0])
	assert.Equal(t, taxRequestShipping{Amount: 4990, Taxable: false}, req.Shipping,
		"the shipping amount is reported but does NOT enter the base")
}

// TestCalculateTotalsShippingUntaxedOnModulePathToo verifies that shipping does not
// enter the tax base on the tax module path either.
func TestCalculateTotalsShippingUntaxedOnModulePathToo(t *testing.T) {
	h := newModuleHarness(t)
	serveSnapshot(h.carts, snapshotOf(1,
		[]SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}},
		[]SnapshotShippingMethod{{ID: "csm_1", Amount: 5000}}))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	assert.Equal(t, int64(200), totals.TaxTotal, "only the 1000 worth of goods is taxed")
	assert.Equal(t, int64(6200), totals.Total)
	requireIdentity(t, totals)
}

// TestCalculateTotalsFallsBackToRegionWhenTaxUnregistered verifies that when the tax
// surface is not registered the tax does not drop to ZERO, that it is computed with
// the region's rate and that the source is reported.
//
// The decision is in the [Workflows.applyTaxes] godoc: a missing tax silently comes
// out of the merchant's pocket; the region is Phase 5's still-valid authority.
func TestCalculateTotalsFallsBackToRegionWhenTaxUnregistered(t *testing.T) {
	h := newHarnessWith(t, &stubDiscounts{perLine: map[string]int64{}}, nil)
	serveSnapshot(h.carts, twoLineCart(1))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	assert.Equal(t, TaxSourceRegion, totals.TaxSource)
	assert.Equal(t, int64(550), totals.TaxTotal, "the region's 20% rate")
	requireIdentity(t, totals)
}

// TestCalculateTotalsFallsBackToRegionWhenCountryUnresolvable verifies that when the
// region cannot be resolved to a single country tax is NEVER ASKED and the region is
// fallen back to.
//
// The distinction is in the [Workflows.countryForRegion] godoc: if it is not known
// which jurisdiction to ask about, an authority that has no answer does not overthrow
// the previous authority.
func TestCalculateTotalsFallsBackToRegionWhenCountryUnresolvable(t *testing.T) {
	tests := map[string]func(h *harness){
		"region is bound to more than one country": func(h *harness) {
			h.catalog.countries[testRegionID] = []string{"TR", "DE"}
		},
		"no country is bound to the region": func(h *harness) {
			h.catalog.countries[testRegionID] = nil
		},
		"region not found in Query": func(h *harness) {
			delete(h.catalog.countries, testRegionID)
		},
		"region provider is not registered": func(h *harness) {
			h.catalog.regionErr = errors.NotFound(codeProviderNotFound,
				"the %q provider is not registered", EntityRegion+query.ProviderSuffix)
		},
	}

	for name, setup := range tests {
		t.Run(name, func(t *testing.T) {
			h := newModuleHarness(t)
			setup(h)
			serveSnapshot(h.carts, twoLineCart(1))

			totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
			require.NoError(t, err)

			assert.Equal(t, TaxSourceRegion, totals.TaxSource)
			assert.Equal(t, int64(550), totals.TaxTotal, "the region's 20% rate")
			assert.Zero(t, h.taxes.calls, "tax must not be called without a known country")
			requireIdentity(t, totals)
		})
	}
}

// TestCalculateTotalsReturnsErrorWhenRegionUnreadable verifies that a TRANSIENT
// failure of the Query layer does not silently change the source.
//
// If an unregistered provider and an unreachable database went through the same
// gate, every cart would silently be taxed with the region rate for the duration of
// an outage and nobody would notice.
func TestCalculateTotalsReturnsErrorWhenRegionUnreadable(t *testing.T) {
	h := newModuleHarness(t)
	h.catalog.regionErr = errors.Unavailable("query_provider_failed", "the database is unreachable")
	serveSnapshot(h.carts, twoLineCart(1))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.Error(t, err)
	assert.Equal(t, CodeRegionReadFailed, errors.CodeOf(err))
	assert.Zero(t, h.taxes.calls)
	assert.Empty(t, h.carts.written)
}

// TestCalculateTotalsTaxRegionUnconfigured verifies that tax's "this country has no
// tax region" answer is accepted AS IS and that the region is NOT fallen back to.
//
// The reason for a zero tax is readable in the result: the difference between
// [TaxSourceTaxUnconfigured] and [TaxSourceTax] is the difference between "the rate
// was zero" and "there was no configuration".
func TestCalculateTotalsTaxRegionUnconfigured(t *testing.T) {
	h := newModuleHarness(t)
	h.taxes.regionFound = false
	serveSnapshot(h.carts, twoLineCart(1))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	assert.Equal(t, TaxSourceTaxUnconfigured, totals.TaxSource)
	assert.Zero(t, totals.TaxTotal)
	assert.NotEqual(t, int64(550), totals.TaxTotal, "there is NO falling back to the region's rate")
	assert.Equal(t, 1, h.taxes.calls)
	requireIdentity(t, totals)
}

// TestCalculateTotalsTaxRoundingRemainderStaysOnTheLine proves that rounding down is
// done per line and that the remainder is NOT CARRIED to another line.
//
// The line bases are 999 and 750 and the rate is 18.5%: per line 184 + 138 = 322. If
// the cart base (1749) were taxed in one go it would come out as 323. The 1 minor
// unit in between is the remainder dropped on the lines it was born on, and it is in
// the customer's favor.
func TestCalculateTotalsTaxRoundingRemainderStaysOnTheLine(t *testing.T) {
	h := newModuleHarness(t)
	h.taxes.rateBps = 1850
	h.discounts.perLine = map[string]int64{testLineA: 1}
	serveSnapshot(h.carts, snapshotOf(1, []SnapshotItem{
		{ID: testLineA, VariantID: testVariantA, Quantity: 1},
		{ID: testLineB, VariantID: testVariantB, Quantity: 3},
	}, nil))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	require.Len(t, totals.Lines, 2)
	assert.Equal(t, int64(184), totals.Lines[0].TaxTotal, "999 x 18.5% = 184.815 -> 184")
	assert.Equal(t, int64(138), totals.Lines[1].TaxTotal, "750 x 18.5% = 138.75 -> 138")
	assert.Equal(t, int64(322), totals.TaxTotal)
	assert.NotEqual(t, int64(323), totals.TaxTotal, "the cart base must not be taxed in one go")
	requireIdentity(t, totals)
}

// TestCalculateTotalsRejectsMalformedTaxResult verifies that tax responses breaking
// the contract do not enter the calculation.
func TestCalculateTotalsRejectsMalformedTaxResult(t *testing.T) {
	tests := map[string]func(req taxRequest) (taxResponse, error){
		"order not preserved": func(req taxRequest) (taxResponse, error) {
			return taxResponse{
				RegionFound: true,
				Items: []taxResponseLine{
					{ID: req.Items[1].ID, TaxableAmount: req.Items[1].Amount},
					{ID: req.Items[0].ID, TaxableAmount: req.Items[0].Amount},
				},
			}, nil
		},
		"line missing": func(req taxRequest) (taxResponse, error) {
			return taxResponse{
				RegionFound: true,
				Items:       []taxResponseLine{{ID: req.Items[0].ID, TaxableAmount: req.Items[0].Amount}},
			}, nil
		},
		"base differs from what was sent": func(req taxRequest) (taxResponse, error) {
			return taxResponse{
				RegionFound: true,
				Items: []taxResponseLine{
					{ID: req.Items[0].ID, TaxableAmount: req.Items[0].Amount + 1},
					{ID: req.Items[1].ID, TaxableAmount: req.Items[1].Amount},
				},
			}, nil
		},
		"tax exceeds the base": func(req taxRequest) (taxResponse, error) {
			excess := req.Items[0].Amount + 1
			return taxResponse{
				RegionFound: true,
				TaxTotal:    excess,
				Items: []taxResponseLine{
					{ID: req.Items[0].ID, TaxableAmount: req.Items[0].Amount, TaxAmount: excess},
					{ID: req.Items[1].ID, TaxableAmount: req.Items[1].Amount},
				},
			}, nil
		},
		"total does not match the lines": func(req taxRequest) (taxResponse, error) {
			return taxResponse{
				RegionFound: true,
				TaxTotal:    100,
				Items: []taxResponseLine{
					{ID: req.Items[0].ID, TaxableAmount: req.Items[0].Amount},
					{ID: req.Items[1].ID, TaxableAmount: req.Items[1].Amount},
				},
			}, nil
		},
		"unrequested shipping tax": func(req taxRequest) (taxResponse, error) {
			return taxResponse{
				RegionFound: true,
				TaxTotal:    7,
				Items: []taxResponseLine{
					{ID: req.Items[0].ID, TaxableAmount: req.Items[0].Amount},
					{ID: req.Items[1].ID, TaxableAmount: req.Items[1].Amount},
				},
				Shipping: taxResponseLine{ID: "_shipping", TaxAmount: 7},
			}, nil
		},
	}

	for name, script := range tests {
		t.Run(name, func(t *testing.T) {
			h := newModuleHarness(t)
			h.taxes.fn = script
			serveSnapshot(h.carts, twoLineCart(1))

			_, err := h.wf.CalculateTotals(context.Background(), testCartID)
			require.Error(t, err)
			assert.Equal(t, CodeTaxInvalid, errors.CodeOf(err))
			assert.Empty(t, h.carts.written)
		})
	}
}

// TestCalculateTotalsTaxErrorKindIsPreserved verifies that tax's error KIND is not
// turned into Internal along the way.
func TestCalculateTotalsTaxErrorKindIsPreserved(t *testing.T) {
	h := newModuleHarness(t)
	h.taxes.err = errors.Unavailable("tax_unconfigured", "the tax service is not configured")
	serveSnapshot(h.carts, twoLineCart(1))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.Error(t, err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err))
	assert.Equal(t, CodeTaxFailed, errors.CodeOf(err))
	assert.Empty(t, h.carts.written, "if the tax could not be computed no stale total must be written")
}

// TestCountryCodes verifies that the country sub-records can be read in all three
// shapes.
//
// The shape can change on the way through the Query layer (the provider writes
// []map[string]any, a JSON round trip turns it into []any); a single type assertion
// would silently swallow the code and the region would look "countryless".
func TestCountryCodes(t *testing.T) {
	tests := map[string]struct {
		value any
		want  []string
	}{
		"provider shape": {
			value: []map[string]any{{FieldCode: "TR"}, {FieldCode: "DE"}},
			want:  []string{"TR", "DE"},
		},
		"query record": {
			value: []query.Record{{FieldCode: "TR"}},
			want:  []string{"TR"},
		},
		"passed through a JSON round trip": {
			value: []any{map[string]any{FieldCode: "TR"}},
			want:  []string{"TR"},
		},
		"sub-record without a code is skipped": {
			value: []map[string]any{{"name": "Turkey"}, {FieldCode: ""}},
			want:  []string{},
		},
		"unrecognized shape": {value: "TR", want: nil},
		"field absent":       {value: nil, want: nil},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, countryCodes(tc.value))
		})
	}
}

// TestCalculateTotalsRegionQueryIsSingleAndNarrow verifies that the region query
// asks for a single record and a single field.
//
// Without field selection the provider would run extra queries to gather all of the
// region's fields (and the currency sub-record); the calculation runs on every round.
func TestCalculateTotalsRegionQueryIsSingleAndNarrow(t *testing.T) {
	h := newModuleHarness(t)
	serveSnapshot(h.carts, twoLineCart(1))

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	var regionSpecs []query.GraphSpec
	for _, spec := range h.catalog.specs {
		if spec.Entity == EntityRegion {
			regionSpecs = append(regionSpecs, spec)
		}
	}
	require.Len(t, regionSpecs, 1, "a single region query per calculation round")
	assert.Equal(t, []string{query.IDField, FieldCountries}, regionSpecs[0].Fields)
	assert.Equal(t, map[string]any{query.IDField: testRegionID}, regionSpecs[0].Filters)
	assert.Equal(t, 1, regionSpecs[0].Limit)
	assert.Empty(t, regionSpecs[0].Expand, "no expansion is requested")
}
