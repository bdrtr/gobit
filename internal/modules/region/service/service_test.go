package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// testClock is the tests' fixed time source; it is used so that time-dependent
// fields are deterministic.
var testClock = time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

// newTestService returns a service running on the fake repository, together
// with the repository.
func newTestService(t *testing.T) (*Service, *memRepo) {
	t.Helper()

	repo := newMemRepo()
	svc := New(repo, Options{Now: func() time.Time { return testClock }})
	return svc, repo
}

// newRegion creates a region for a test.
func newRegion(t *testing.T, svc *Service, currency string) models.Region {
	t.Helper()

	region, err := svc.CreateRegion(context.Background(), CreateRegionInput{
		Name:           "Test " + currency,
		CurrencyCode:   currency,
		AutomaticTaxes: true,
		TaxRate:        2000,
	})
	require.NoError(t, err)
	return region
}

// TestCreateRegionNormalizesAndValidates proves the normalization and
// validation rules of region creation.
func TestCreateRegionNormalizesAndValidates(t *testing.T) {
	ctx := context.Background()

	t.Run("the currency is converted to upper case", func(t *testing.T) {
		svc, _ := newTestService(t)

		region, err := svc.CreateRegion(ctx, CreateRegionInput{
			Name: "  T\u00fcrkiye  ", CurrencyCode: " try ", TaxRate: 2000,
		})
		require.NoError(t, err)
		assert.Equal(t, "TRY", region.CurrencyCode, "the code has to be stored in UPPER case")
		assert.Equal(t, "T\u00fcrkiye", region.Name, "the name has to be trimmed")
		assert.True(t, strings.HasPrefix(region.ID, models.RegionIDPrefix),
			"the id has to start with the %q prefix, %q was generated", models.RegionIDPrefix, region.ID)
		assert.Equal(t, testClock, region.CreatedAt)
	})

	t.Run("an invalid currency code is rejected", func(t *testing.T) {
		svc, repo := newTestService(t)

		for _, code := range []string{"", "TR", "TRYX", "TR1", "T RY", "₺₺₺"} {
			_, err := svc.CreateRegion(ctx, CreateRegionInput{Name: "X", CurrencyCode: code})
			require.Error(t, err, "%q must not be accepted", code)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "code: %q", code)
		}
		assert.Zero(t, repo.callCount("CreateRegion"),
			"a formally invalid code must never reach the database")
	})

	t.Run("an undefined currency is rejected", func(t *testing.T) {
		svc, _ := newTestService(t)

		// Formally valid, but absent from the reference table.
		_, err := svc.CreateRegion(ctx, CreateRegionInput{Name: "X", CurrencyCode: "XYZ"})
		require.Error(t, err)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	})

	t.Run("an empty name is rejected", func(t *testing.T) {
		svc, _ := newTestService(t)

		_, err := svc.CreateRegion(ctx, CreateRegionInput{Name: "   ", CurrencyCode: "TRY"})
		require.Error(t, err)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	})

	t.Run("an out-of-range tax rate is rejected", func(t *testing.T) {
		svc, _ := newTestService(t)

		for _, rate := range []int32{-1, models.MaxTaxRate + 1} {
			_, err := svc.CreateRegion(ctx, CreateRegionInput{
				Name: "X", CurrencyCode: "TRY", TaxRate: rate,
			})
			require.Error(t, err, "rate: %d", rate)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "rate: %d", rate)
		}
	})

	t.Run("tax rates at the bounds are accepted", func(t *testing.T) {
		svc, _ := newTestService(t)

		for _, rate := range []int32{models.MinTaxRate, models.MaxTaxRate} {
			region, err := svc.CreateRegion(ctx, CreateRegionInput{
				Name: "X", CurrencyCode: "TRY", TaxRate: rate,
			})
			require.NoError(t, err, "rate: %d", rate)
			assert.Equal(t, rate, region.TaxRate)
		}
	})
}

// TestGetRegionRejectsForeignID proves that an id of the wrong type returns a
// validation error, not "not found".
//
// This is why prefixed ids exist: a customer id standing in for a region has to
// be a 422 that says what it is, not a silent 404.
func TestGetRegionRejectsForeignID(t *testing.T) {
	svc, repo := newTestService(t)

	_, err := svc.GetRegion(context.Background(), "cust_01ABCDEF")

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Zero(t, repo.callCount("GetRegion"), "an id with the wrong prefix must not reach the repository")
}

// TestUpdateRegionIsPartial proves that a partial update changes only the
// given fields.
func TestUpdateRegionIsPartial(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	region := newRegion(t, svc, "TRY")

	name := "New Name"
	updated, err := svc.UpdateRegion(ctx, region.ID, UpdateRegionInput{Name: &name})
	require.NoError(t, err)

	assert.Equal(t, "New Name", updated.Name)
	assert.Equal(t, region.CurrencyCode, updated.CurrencyCode, "a currency that was not given must not change")
	assert.Equal(t, region.TaxRate, updated.TaxRate, "a tax rate that was not given must not change")
	assert.Equal(t, region.AutomaticTaxes, updated.AutomaticTaxes, "a flag that was not given must not change")
}

// TestUpdateRegionZeroValuesAreWritten proves that a zero-valued patch is not
// treated as "leave alone".
//
// That is the only reason for using pointers: false and 0 are valid values and
// have to be distinguishable from the field not being given at all.
func TestUpdateRegionZeroValuesAreWritten(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	region := newRegion(t, svc, "TRY")
	require.True(t, region.AutomaticTaxes)
	require.Equal(t, int32(2000), region.TaxRate)

	automatic := false
	rate := int32(0)
	updated, err := svc.UpdateRegion(ctx, region.ID, UpdateRegionInput{
		AutomaticTaxes: &automatic,
		TaxRate:        &rate,
	})
	require.NoError(t, err)

	assert.False(t, updated.AutomaticTaxes, "false has to be written, not treated as 'leave alone'")
	assert.Zero(t, updated.TaxRate, "0 has to be written, not treated as 'leave alone'")
	assert.Equal(t, region.Name, updated.Name)
}

// TestUpdateRegionRejectsEmptyPatch proves that an empty patch does not
// silently return success.
func TestUpdateRegionRejectsEmptyPatch(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService(t)
	region := newRegion(t, svc, "TRY")
	repo.resetCalls()

	_, err := svc.UpdateRegion(ctx, region.ID, UpdateRegionInput{})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Zero(t, repo.callCount("UpdateRegion"), "an empty patch must not reach the repository")
}

// TestUpdateRegionValidatesCurrency proves that the currency in a patch is
// normalized and validated too.
func TestUpdateRegionValidatesCurrency(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	region := newRegion(t, svc, "TRY")

	bad := "tryx"
	_, err := svc.UpdateRegion(ctx, region.ID, UpdateRegionInput{CurrencyCode: &bad})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	good := "usd"
	updated, err := svc.UpdateRegion(ctx, region.ID, UpdateRegionInput{CurrencyCode: &good})
	require.NoError(t, err)
	assert.Equal(t, "USD", updated.CurrencyCode, "the code in the patch has to be converted to UPPER case too")
}

// TestAddCountryToRegionUniqueness proves that a country can belong to at most
// one region.
func TestAddCountryToRegionUniqueness(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	first := newRegion(t, svc, "TRY")
	second := newRegion(t, svc, "USD")

	country, err := svc.AddCountryToRegion(ctx, first.ID, "tr")
	require.NoError(t, err)
	assert.Equal(t, "TR", country.Code, "the country code has to be converted to UPPER case")
	require.NotNil(t, country.RegionID)
	assert.Equal(t, first.ID, *country.RegionID)

	_, err = svc.AddCountryToRegion(ctx, second.ID, "TR")
	require.Error(t, err, "the same country must not be addable to a second region")
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))

	// The attachment to the first region has to be intact.
	resolved, err := svc.ResolveRegionForCountry(ctx, "TR")
	require.NoError(t, err)
	assert.Equal(t, first.ID, resolved.ID)
}

// TestAddCountryToRegionIsIdempotent proves that a repeated request to add a
// country to the same region does not produce an error.
func TestAddCountryToRegionIsIdempotent(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	region := newRegion(t, svc, "TRY")

	_, err := svc.AddCountryToRegion(ctx, region.ID, "TR")
	require.NoError(t, err)

	again, err := svc.AddCountryToRegion(ctx, region.ID, "TR")
	require.NoError(t, err, "a repeated admin request must not produce an error")
	require.NotNil(t, again.RegionID)
	assert.Equal(t, region.ID, *again.RegionID)
}

// TestAddCountryToRegionValidatesInput proves that an invalid id and an invalid
// country code are rejected without reaching the repository at all.
func TestAddCountryToRegionValidatesInput(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService(t)
	region := newRegion(t, svc, "TRY")
	repo.resetCalls()

	_, err := svc.AddCountryToRegion(ctx, "prod_01", "TR")
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	for _, code := range []string{"", "T", "TUR", "T1"} {
		_, err = svc.AddCountryToRegion(ctx, region.ID, code)
		require.Error(t, err, "%q must not be accepted", code)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "code: %q", code)
	}
	assert.Zero(t, repo.callCount("AssignCountry"), "invalid input must not reach the repository")
}

// TestRemoveCountryFromRegion proves that a country is removed from its region
// and that a call made with the wrong region is rejected.
func TestRemoveCountryFromRegion(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	first := newRegion(t, svc, "TRY")
	second := newRegion(t, svc, "USD")

	_, err := svc.AddCountryToRegion(ctx, first.ID, "TR")
	require.NoError(t, err)

	err = svc.RemoveCountryFromRegion(ctx, second.ID, "TR")
	require.Error(t, err, "another region's country must not be removable")
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))

	require.NoError(t, svc.RemoveCountryFromRegion(ctx, first.ID, "tr"))

	_, err = svc.ResolveRegionForCountry(ctx, "TR")
	require.Error(t, err)
	assert.Equal(t, CodeCountryUnassigned, errors.CodeOf(err))
}

// TestResolveRegionForCountry proves the happy path of the resolution and the
// three distinct failure cases.
//
// All three return errors.NotFound but their CODES differ; the caller knows
// from the code which fix is needed.
func TestResolveRegionForCountry(t *testing.T) {
	ctx := context.Background()

	t.Run("country to region takes a single query", func(t *testing.T) {
		svc, repo := newTestService(t)
		region := newRegion(t, svc, "TRY")
		_, err := svc.AddCountryToRegion(ctx, region.ID, "TR")
		require.NoError(t, err)
		repo.resetCalls()

		resolved, err := svc.ResolveRegionForCountry(ctx, "tr")
		require.NoError(t, err)
		assert.Equal(t, region.ID, resolved.ID)
		assert.Equal(t, "TRY", resolved.CurrencyCode)
		assert.Equal(t, 1, repo.callCount("GetRegionByCountry"))
		assert.Zero(t, repo.callCount("GetCountry"), "the happy path must not make a second query")
	})

	t.Run("an undefined country code is eliminated by validation", func(t *testing.T) {
		svc, repo := newTestService(t)

		_, err := svc.ResolveRegionForCountry(ctx, "TURKEY")
		require.Error(t, err)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		assert.Zero(t, repo.callCount("GetRegionByCountry"))
	})

	t.Run("an unknown country returns not found", func(t *testing.T) {
		svc, _ := newTestService(t)

		_, err := svc.ResolveRegionForCountry(ctx, "ZZ")
		require.Error(t, err)
		assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
		assert.Equal(t, "country_not_found", errors.CodeOf(err))
	})

	t.Run("a country without a region returns a separate code", func(t *testing.T) {
		svc, _ := newTestService(t)

		_, err := svc.ResolveRegionForCountry(ctx, "DE")
		require.Error(t, err)
		assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
		assert.Equal(t, CodeCountryUnassigned, errors.CodeOf(err))
	})

	t.Run("a country whose region was deleted returns the inconsistency code", func(t *testing.T) {
		svc, repo := newTestService(t)
		region := newRegion(t, svc, "TRY")
		_, err := svc.AddCountryToRegion(ctx, region.ID, "TR")
		require.NoError(t, err)

		// Delete the region WITHOUT releasing its countries: an inconsistent
		// state that does not arise in the real repository, but that the
		// service has to be able to tell apart.
		repo.mu.Lock()
		stale := repo.regions[region.ID]
		deleted := testClock
		stale.DeletedAt = &deleted
		repo.regions[region.ID] = stale
		repo.mu.Unlock()

		_, err = svc.ResolveRegionForCountry(ctx, "TR")
		require.Error(t, err)
		assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
		assert.Equal(t, CodeCountryRegionMissing, errors.CodeOf(err))
	})
}

// TestDeleteRegionReleasesCountries proves that the countries of a deleted
// region are released.
//
// Had they not been released, the country would stay attached to a dead region,
// it could not be added to any other region, and no cart could be opened for a
// customer in that country.
func TestDeleteRegionReleasesCountries(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	first := newRegion(t, svc, "TRY")
	second := newRegion(t, svc, "USD")

	_, err := svc.AddCountryToRegion(ctx, first.ID, "TR")
	require.NoError(t, err)

	require.NoError(t, svc.DeleteRegion(ctx, first.ID))

	_, err = svc.GetRegion(ctx, first.ID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err), "a deleted region must not be readable")

	// The country is now free and can be added to another region.
	country, err := svc.AddCountryToRegion(ctx, second.ID, "TR")
	require.NoError(t, err, "a released country has to be addable to another region")
	require.NotNil(t, country.RegionID)
	assert.Equal(t, second.ID, *country.RegionID)
}

// TestListCountriesValidatesRegionFilter proves that the region filter is
// validated.
//
// Without validation an id of the wrong type would return an empty list and the
// client would conclude that the region has no countries.
func TestListCountriesValidatesRegionFilter(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService(t)

	for _, id := range []string{"", "prod_01"} {
		filter := id
		_, err := svc.ListCountries(ctx, ListCountriesInput{RegionID: &filter})
		require.Error(t, err, "id: %q", id)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "id: %q", id)
	}
	assert.Zero(t, repo.callCount("ListCountries"))
}

// TestPagingIsNormalized proves that the paging bounds are applied and that the
// APPLIED value is reported back.
func TestPagingIsNormalized(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	newRegion(t, svc, "TRY")

	page, err := svc.ListRegions(ctx, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, DefaultLimit, page.Limit, "if no limit is given the default has to be applied")

	page, err = svc.ListRegions(ctx, MaxLimit+1000, 0)
	require.NoError(t, err)
	assert.Equal(t, MaxLimit, page.Limit, "the limit has to be cut to the maximum value")

	_, err = svc.ListRegions(ctx, 10, -1)
	require.Error(t, err, "a negative offset has to be rejected")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestListCurrenciesReturnsSeededSet proves that the currency list comes back
// with the decimal digits.
func TestListCurrenciesReturnsSeededSet(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	page, err := svc.ListCurrencies(ctx, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(4), page.Count)

	digits := map[string]int32{}
	for _, currency := range page.Items {
		digits[currency.Code] = currency.DecimalDigits
	}
	assert.Equal(t, int32(2), digits["TRY"])
	assert.Equal(t, int32(0), digits["JPY"], "JPY has no decimal digits")
	assert.Equal(t, int32(3), digits["KWD"], "KWD has three decimal digits")
}

// TestGetCurrencyNormalizesCode proves that reading a currency normalizes the
// code.
func TestGetCurrencyNormalizesCode(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	currency, err := svc.GetCurrency(ctx, " jpy ")
	require.NoError(t, err)
	assert.Equal(t, "JPY", currency.Code)
	assert.Zero(t, currency.DecimalDigits)

	_, err = svc.GetCurrency(ctx, "JP")
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = svc.GetCurrency(ctx, "XYZ")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestUnconfiguredServiceReturnsTypedError proves that a service without a
// repository returns a typed error, not a panic.
func TestUnconfiguredServiceReturnsTypedError(t *testing.T) {
	ctx := context.Background()
	svc := New(nil, Options{})

	_, err := svc.CreateRegion(ctx, CreateRegionInput{Name: "X", CurrencyCode: "TRY"})
	require.Error(t, err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err))

	_, err = svc.ResolveRegionForCountry(ctx, "TR")
	require.Error(t, err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err))
}
