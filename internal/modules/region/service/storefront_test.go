package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// TestListStoreRegionsBatchesReads proves that the number of queries for the
// storefront list is INDEPENDENT of the number of regions.
//
// Reading the currency/countries per region would mean N+1, and the storefront
// list is exactly where the most records come back. The claim is proven with a
// counter: for three regions, three reads are made in total.
func TestListStoreRegionsBatchesReads(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService(t)

	first := newRegion(t, svc, "TRY")
	second := newRegion(t, svc, "JPY")
	newRegion(t, svc, "USD")

	_, err := svc.AddCountryToRegion(ctx, first.ID, "TR")
	require.NoError(t, err)
	_, err = svc.AddCountryToRegion(ctx, first.ID, "DE")
	require.NoError(t, err)
	_, err = svc.AddCountryToRegion(ctx, second.ID, "JP")
	require.NoError(t, err)

	repo.resetCalls()
	page, err := svc.ListStoreRegions(ctx, 0, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 3)
	assert.Equal(t, int64(3), page.Count)

	assert.Equal(t, 1, repo.callCount("ListRegions"))
	assert.Equal(t, 1, repo.callCount("GetCurrenciesByCodes"),
		"the currencies have to be fetched with a SINGLE batch read")
	assert.Equal(t, 1, repo.callCount("ListCountriesByRegions"),
		"the countries have to be fetched with a SINGLE batch read")
	assert.Zero(t, repo.callCount("GetCurrency"), "the currency must not be read per region")
	assert.Zero(t, repo.callCount("ListCountries"), "the countries must not be read per region")

	byID := map[string]StoreRegion{}
	for _, item := range page.Items {
		byID[item.Region.ID] = item
	}

	tr := byID[first.ID]
	require.NotNil(t, tr.Currency)
	assert.Equal(t, "TRY", tr.Currency.Code)
	assert.Equal(t, int32(2), tr.Currency.DecimalDigits)
	require.Len(t, tr.Countries, 2)
	assert.Equal(t, "DE", tr.Countries[0].Code, "the countries have to be sorted by code")
	assert.Equal(t, "TR", tr.Countries[1].Code)

	jp := byID[second.ID]
	require.NotNil(t, jp.Currency)
	assert.Equal(t, int32(0), jp.Currency.DecimalDigits, "JPY has no decimal digits")
	require.Len(t, jp.Countries, 1)
}

// TestListStoreRegionsEmptyCountriesAreSlices proves that a region without
// countries returns an empty slice, not nil.
//
// Seeing [] instead of null in the JSON means the consumer sees a uniform
// surface.
func TestListStoreRegionsEmptyCountriesAreSlices(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	newRegion(t, svc, "USD")

	page, err := svc.ListStoreRegions(ctx, 0, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.NotNil(t, page.Items[0].Countries)
	assert.Empty(t, page.Items[0].Countries)
}

// TestGetStoreRegion proves that the single region read carries the currency
// and the countries as well.
func TestGetStoreRegion(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	region := newRegion(t, svc, "KWD")
	_, err := svc.AddCountryToRegion(ctx, region.ID, "US")
	require.NoError(t, err)

	item, err := svc.GetStoreRegion(ctx, region.ID)
	require.NoError(t, err)
	assert.Equal(t, region.ID, item.Region.ID)
	require.NotNil(t, item.Currency)
	assert.Equal(t, int32(3), item.Currency.DecimalDigits, "KWD has three decimal digits")
	require.Len(t, item.Countries, 1)
	assert.Equal(t, "US", item.Countries[0].Code)

	_, err = svc.GetStoreRegion(ctx, "reg_MISSING")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestStoreRegionCurrencyMissingIsNil proves that a currency not found in the
// reference table is represented by nil, NOT by a zero value.
//
// A zero value would report the decimal digits as 0 and show amounts at the
// wrong scale; nil tells the consumer "unknown".
func TestStoreRegionCurrencyMissingIsNil(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService(t)
	region := newRegion(t, svc, "TRY")

	// Because of the foreign key this cannot happen for real; it is set up by
	// hand in the fake repository.
	repo.mu.Lock()
	delete(repo.currencies, "TRY")
	repo.mu.Unlock()

	item, err := svc.GetStoreRegion(ctx, region.ID)
	require.NoError(t, err)
	assert.Nil(t, item.Currency)
	assert.Equal(t, "TRY", item.Region.CurrencyCode, "the code has to be visible all the same")
}
