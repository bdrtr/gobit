//go:build integration

package e2e

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// This file proves the TAX leg of the plan's Phase 7 DoD: "the tax is computed
// according to the region".
//
// In Phase 5 the tax came from the region module's single flat rate (RegionTax ->
// rateBps, automatic) and the region's godoc had marked that as temporary, saying
// "in Phase 7 the tax module will take it over". The takeover was done; this file
// tests its RESULT.
//
// # Proving the takeover with "the tax came out right" is NOT ENOUGH
//
// Had both authorities reported the same rate for the same country, the result
// would have been the same whichever one the computation used, and the test would
// have stayed green while nothing was proven. That is why the fixture of the
// Phase 7 regions is deliberately CONTRADICTORY: region says one rate, tax says
// another, and the amount alone gives away which one was listened to (see
// [secondRegionRateBps], [unconfiguredRegionRateBps]).
//
// # Where the country comes from
//
// The cart does NOT PUBLISH the shipping address on the cross-module surface; the
// cart flow reads the tax country off the region, through the Query layer, and
// uses it only if the region is bound to a SINGLE country. Every region in the
// fixture carries exactly one country (see [setUpRegionFixtures]); had it not,
// the computation would never ask tax, would silently fall onto the region path,
// and every claim in this file would lose its meaning.

// The HAND-computed amounts of the two-region tax scenario.
//
// The same variant, the same price, the same quantity and the same currency; the
// only variable is the tax RATE.
//
//	subtotal = 10_000 x 3 = 30_000 (in both regions)
//
//	[taxedCountry]      -> tax 20%: 30_000 x 20% = 6_000; total 36_000
//	[secondTaxCountry]  -> tax 10%: 30_000 x 10% = 3_000; total 33_000
//
// The second region's OWN (region) rate is 50% and its automatic tax is ON; had
// the computation used it, the tax would have come out 15_000. The difference
// between the two is the measure of the takeover really having been made.
const (
	taxUnitPrice int64 = 10_000
	taxQuantity  int64 = 3
	taxSubtotal  int64 = 30_000

	// taxedRegionTax is the tax expected in the [taxedCountry] region (20%).
	taxedRegionTax int64 = 6_000
	// taxedRegionTotal is the grand total expected in the [taxedCountry] region.
	taxedRegionTotal int64 = 36_000

	// secondRegionTax is the tax expected in the [secondTaxCountry] region (10%).
	secondRegionTax int64 = 3_000
	// secondRegionTotal is the grand total expected in the [secondTaxCountry]
	// region.
	secondRegionTotal int64 = 33_000
	// secondRegionRateTax is the tax that would come out were the region's OWN
	// rate applied (50%); it must be seen in no round.
	secondRegionRateTax int64 = 15_000
)

// The HAND-computed amounts of the country whose tax region is not configured.
//
//	subtotal = 30_000; tax 0; total 30_000
//
// The region's own rate is 18% and its automatic tax is ON; had the computation
// fallen back to region, the tax would have come out 5_400.
const (
	unconfiguredTotal int64 = 30_000
	// unconfiguredRegionRateTax is the tax that would come out were the region
	// rate applied; it must not be seen.
	unconfiguredRegionRateTax int64 = 5_400
)

// TestSameProductProducesDifferentTaxInTwoRegions verifies that the same product
// produces a DIFFERENT tax in two regions.
//
// A single variant is put into two separate carts; the carts' regions (and
// therefore their tax countries) differ, everything else is the same. The
// subtotals being equal is the precondition of the comparison: were they not
// equal, the tax difference could come from the price rather than from the rate.
func TestSameProductProducesDifferentTaxInTwoRegions(t *testing.T) {
	ctx := t.Context()

	variantID := newVariant(ctx, t, "E2E Two-Region Product", map[string]int64{
		taxedCurrency: taxUnitPrice,
	})

	taxed := cartTotalsInCountry(ctx, t, taxedCountry, variantID, taxQuantity)
	second := cartTotalsInCountry(ctx, t, secondTaxCountry, variantID, taxQuantity)

	// --- 1) are both regions' amounts the same as the hand-computed values ---

	assertTotals(t, taxed, expectedTotal{
		subtotal: taxSubtotal,
		discount: 0,
		tax:      taxedRegionTax,
		shipping: 0,
		total:    taxedRegionTotal,
	}, "in the region taxed at 20%")

	assertTotals(t, second, expectedTotal{
		subtotal: taxSubtotal,
		discount: 0,
		tax:      secondRegionTax,
		shipping: 0,
		total:    secondRegionTotal,
	}, "in the second region taxed at 10%")

	// --- 2) does the difference REALLY come from the region ---

	require.Equal(t, taxed.Subtotal, second.Subtotal,
		"the two carts' subtotals must be the SAME; had they differed, it could not be "+
			"told apart whether the tax difference came from the rate or from the price")
	require.NotEqual(t, taxed.TaxTotal, second.TaxTotal,
		"the same product must produce a DIFFERENT tax in two regions. Coming out equal "+
			"shows that the tax country is not read off the cart's region and that both "+
			"carts were computed with the same jurisdiction")
	require.Equal(t, taxedRegionTax-secondRegionTax, taxed.TaxTotal-second.TaxTotal,
		"the tax difference must be the hand-computed 3_000: 30_000 x (20%% - 10%%)")

	// --- 3) is the authority the TAX module, or still region ---

	require.Equal(t, cartwf.TaxSourceTax, taxed.TaxSource,
		"the tax of the %s region must have been computed by the tax module", taxedCountry)
	require.Equal(t, cartwf.TaxSourceTax, second.TaxSource,
		"the tax of the %s region must have been computed by the tax module", secondTaxCountry)
	require.NotEqual(t, secondRegionRateTax, second.TaxTotal,
		"the second region's tax must NOT have been computed with the region's OWN 50%% "+
			"rate. Coming out 15_000 would say that the takeover was not made, that the "+
			"tax is still read from the region module — and the fixture writes different "+
			"rates to the two authorities exactly to make that visible")

	rate, automatic, err := regionSvc.RegionTax(ctx, secondTaxRegionID)
	require.NoError(t, err, "the second region's region setting must be readable")
	require.True(t, automatic,
		"the second region must keep automatic tax ON; had it been off, the region path "+
			"would produce zero as well and we could not tell the two authorities apart")
	require.Equal(t, secondRegionRateBps, rate,
		"the second region must carry 50%% on the region side; had it not, the claim "+
			"above would come to nothing")
}

// TestTaxIsZeroInCountryWithoutTaxRegion verifies that in a country whose tax
// region is NOT CONFIGURED in the tax module the tax stays zero and that there is
// NO FALLING BACK to region.
//
// What is tested is not an amount but an AUTHORITY rule (see cartwf applyTaxes,
// "The authority is SINGLE and is chosen AT SETUP"): tax was called and gave an
// AUTHORITATIVE answer, "this country has no tax region"; it is not an authority
// without an answer but an authority with an answer that spoke, and the previous
// authority is not brought in.
//
// The region carries a NON-zero rate and its automatic tax is on. That is
// deliberate: had it been set up with a zero-rate region, "region was not fallen
// back to" and "region was fallen back to but the rate was zero anyway" could not
// be told apart.
func TestTaxIsZeroInCountryWithoutTaxRegion(t *testing.T) {
	ctx := t.Context()

	rate, automatic, err := regionSvc.RegionTax(ctx, unconfiguredRegionID)
	require.NoError(t, err, "the region's region setting must be readable")
	require.True(t, automatic,
		"the region must keep automatic tax ON; had it been off, the tax coming out "+
			"zero would prove nothing")
	require.Equal(t, unconfiguredRegionRateBps, rate,
		"the region must carry a NON-zero region rate")

	_, found, err := taxInterop.RateForCountry(ctx, unconfiguredCountry)
	require.NoError(t, err, "the rate must be queryable from the tax surface")
	require.False(t, found,
		"country %s must have NO tax region in the tax module; had it had one, this "+
			"scenario would test a configured rate rather than the absence of "+
			"configuration",
		unconfiguredCountry)

	variantID := newVariant(ctx, t, "E2E Product Without Tax Region", map[string]int64{
		taxedCurrency: taxUnitPrice,
	})
	totals := cartTotalsInCountry(ctx, t, unconfiguredCountry, variantID, taxQuantity)

	assertTotals(t, totals, expectedTotal{
		subtotal: taxSubtotal,
		discount: 0,
		tax:      0,
		shipping: 0,
		total:    unconfiguredTotal,
	}, "in the country whose tax region is not configured")

	require.NotEqual(t, unconfiguredRegionRateTax, totals.TaxTotal,
		"the tax must NOT have been computed with the region's 18%% region rate. Coming "+
			"out 5_400 would show that tax's authoritative \"no region\" answer was "+
			"ignored and that the previous authority was fallen back to; then which "+
			"authority the tax came from would change silently according to which "+
			"country a record happens to be entered for")
	require.Equal(t, cartwf.TaxSourceTaxUnconfigured, totals.TaxSource,
		"the source must be %q. This value is kept apart from %q because a zero tax is "+
			"born of two different reasons: the rate really is zero, or there is no "+
			"configuration at all for that country. A field that swallows the "+
			"distinction would be an invitation to mistake a missing setup for a "+
			"\"tax-free country\"",
		cartwf.TaxSourceTaxUnconfigured, cartwf.TaxSourceTax)
	require.NotEqual(t, cartwf.TaxSourceRegion, totals.TaxSource,
		"the source must NOT be %q; had the region path been fallen onto, the tax would "+
			"not have come out zero in the first place and this field would be the only "+
			"sign that the two paths got mixed up",
		cartwf.TaxSourceRegion)
}

// TestTaxSurfaceReportsDifferentRatePerCountry verifies that two countries are set
// up with DIFFERENT rates in the tax module and that the surface tells the two
// apart.
//
// The cart computation never calls this method — it asks for tax per line and
// always uses CalculateTaxJSON. [taxSurface.RateForCountry] is tested all the
// same: it is the exact counterpart of the region module's stopgap RegionTax
// method, and no other production package calls the "new surface replacing the old
// one" side of the takeover.
//
// The MEANING of the second return value is pinned down here too: the flag in
// region was the "apply / do not apply the tax" preference, while this one is the
// "is there configuration" information.
func TestTaxSurfaceReportsDifferentRatePerCountry(t *testing.T) {
	ctx := t.Context()

	for _, scenario := range []struct {
		country     string
		rate        int32
		found       bool
		description string
	}{
		{taxedCountry, taxRateBps, true, "the rate the Phase 5/6 scenarios rest on"},
		{secondTaxCountry, secondTaxRateBps, true, "the second region's rate"},
		{unconfiguredCountry, 0, false, "the country with no tax region set up"},
	} {
		rate, found, err := taxInterop.RateForCountry(ctx, scenario.country)
		require.NoError(t, err, "the rate must be queryable for %s", scenario.country)
		require.Equal(t, scenario.found, found,
			"%s (%s): the EXISTENCE of the configuration must be reported correctly. "+
				"Without this flag the caller cannot tell a country whose rate really "+
				"is zero from a country that was never configured, and would silently "+
				"sell with a missing setup",
			scenario.country, scenario.description)
		require.Equal(t, scenario.rate, rate,
			"%s (%s): the rate must be the value written in the fixture",
			scenario.country, scenario.description)
	}

	require.NotEqual(t, taxRateBps, secondTaxRateBps,
		"the two countries' rates must have been set up DIFFERENT; had they been the "+
			"same, the claim \"the same product produces a different tax in two "+
			"regions\" could not be tested")
}

// cartTotalsInCountry opens a cart in the given country, adds a single line and
// returns the result of the computation.
//
// The cart is opened with the country CODE, not with the region id: the step that
// resolves the region from the country is the flow's own job as well, and the
// fixture skipping it would hide a cart bound to the wrong region from the test.
func cartTotalsInCountry(
	ctx context.Context,
	t *testing.T,
	countryCode, variantID string,
	quantity int64,
) cartwf.Totals {
	t.Helper()

	cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: countryCode})
	require.NoError(t, err, "could not open a cart in country %s", countryCode)

	added, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    cart.CartID,
		VariantID: variantID,
		Quantity:  quantity,
	})
	require.NoError(t, err, "could not add a line to the %s cart", countryCode)

	return added.Totals
}

// TestTaxIsComputedFromRegionInMultiCountryRegion proves the path on which the tax
// falls onto REGION with the real wiring.
//
// It was a coverage gap: every fixture carried SINGLE-country regions and tax was
// always registered, so no e2e round PRODUCED the TaxSource "region". The fallback
// path was tested only with in-memory fakes.
//
// The trigger is utterly ordinary in production: a multi-country "Europe" region.
// The cart computation reads the tax country off the region; when the region
// carries more than one country, which one to ask cannot be known, tax is NOT asked
// AT ALL and the region rate is used. This path silently changes the tax authority,
// so reporting the source and locking it down here is a must.
func TestTaxIsComputedFromRegionInMultiCountryRegion(t *testing.T) {
	ctx := t.Context()

	// Precondition: the region must REALLY be multi-country, otherwise the test tests
	// another path.
	require.Len(t, multiCountryCountries, 2,
		"the scenario rests on the region carrying MORE THAN ONE country")

	// Precondition: these countries must have NO region in the tax module; had they
	// had one, what the test tests would be "tax answered" rather than "the country
	// could not be resolved".
	for _, country := range multiCountryCountries {
		_, found, err := taxInterop.RateForCountry(ctx, country)
		require.NoError(t, err)
		require.False(t, found, "%s must be unconfigured in the tax module", country)
	}

	variantID := newVariant(ctx, t, "E2E Multi-Country Region Product", map[string]int64{
		untaxedCurrency: taxUnitPrice,
	})
	totals := cartTotalsInCountry(ctx, t, multiCountryCountries[0], variantID, taxQuantity)

	// The expectation is computed BY HAND: 30_000 x 30% = 9_000, total 39_000.
	const expectedTax int64 = 9_000
	const expectedGrandTotal int64 = 39_000

	assertTotals(t, totals, expectedTotal{
		subtotal: taxSubtotal,
		discount: 0,
		tax:      expectedTax,
		shipping: 0,
		total:    expectedGrandTotal,
	}, "in the multi-country region")

	require.Equal(t, cartwf.TaxSourceRegion, totals.TaxSource,
		"the source must be %q: because the country could not be resolved from the "+
			"region, tax was never asked and the computation fell onto the region rate. "+
			"This field is the ONLY sign that the authority changed silently",
		cartwf.TaxSourceRegion)
	require.NotEqual(t, cartwf.TaxSourceTaxUnconfigured, totals.TaxSource,
		"tax must NOT have been asked and given a 'no region' answer; the country was "+
			"never resolved at all")
}
