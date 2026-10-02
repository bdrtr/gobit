package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// newTestProvider returns a provider running on the fake repository, together
// with its service and repository.
func newTestProvider(t *testing.T) (*QueryProvider, *Service, *memRepo) {
	t.Helper()

	svc, repo := newTestService(t)
	return NewQueryProvider(svc), svc, repo
}

// TestProviderEntityMatchesRegistrationName proves that the provider's entity
// name matches the name it is registered under in the container.
//
// Query looks up the target of an expansion under the name "<entity>.query"; if
// the names did not match, errors.NotFound would be returned and the error
// would only show at run time.
func TestProviderEntityMatchesRegistrationName(t *testing.T) {
	provider, _, _ := newTestProvider(t)

	assert.Equal(t, "region", provider.Entity())
	assert.Equal(t, "region.query", provider.Entity()+query.ProviderSuffix)
}

// TestProviderListReturnsFullRecords proves that the default field set carries
// the region, its currency and its countries.
func TestProviderListReturnsFullRecords(t *testing.T) {
	ctx := context.Background()
	provider, svc, _ := newTestProvider(t)
	region := newRegion(t, svc, "JPY")
	_, err := svc.AddCountryToRegion(ctx, region.ID, "JP")
	require.NoError(t, err)

	records, err := provider.List(ctx, query.ListOptions{})
	require.NoError(t, err)
	require.Len(t, records, 1)

	record := records[0]
	assert.Equal(t, region.ID, record[query.IDField])
	assert.Equal(t, region.Name, record["name"])
	assert.Equal(t, "JPY", record["currency_code"])
	assert.Equal(t, int32(2000), record["tax_rate"])
	assert.Equal(t, true, record["automatic_taxes"])

	currency, ok := record["currency"].(map[string]any)
	require.True(t, ok, "there has to be a currency sub-record")
	assert.Equal(t, "JPY", currency["code"])
	assert.Equal(t, int32(0), currency["decimal_digits"],
		"the storefront learns the division factor from this field")

	countries, ok := record["countries"].([]map[string]any)
	require.True(t, ok, "there have to be country sub-records")
	require.Len(t, countries, 1)
	assert.Equal(t, "JP", countries[0]["code"])
}

// TestProviderFetchByIDsBatchesReads proves that a CONSTANT number of reads is
// made per expansion (ADR 0004's N+1 ban).
func TestProviderFetchByIDsBatchesReads(t *testing.T) {
	ctx := context.Background()
	provider, svc, repo := newTestProvider(t)

	first := newRegion(t, svc, "TRY")
	second := newRegion(t, svc, "USD")
	third := newRegion(t, svc, "JPY")
	_, err := svc.AddCountryToRegion(ctx, first.ID, "TR")
	require.NoError(t, err)
	_, err = svc.AddCountryToRegion(ctx, second.ID, "US")
	require.NoError(t, err)

	repo.resetCalls()
	records, err := provider.FetchByIDs(ctx, []string{first.ID, second.ID, third.ID}, nil)
	require.NoError(t, err)
	assert.Len(t, records, 3)

	assert.Equal(t, 1, repo.callCount("GetRegionsByIDs"))
	assert.Equal(t, 1, repo.callCount("GetCurrenciesByCodes"))
	assert.Equal(t, 1, repo.callCount("ListCountriesByRegions"))
	assert.Zero(t, repo.callCount("GetCurrency"), "the currency must not be read per record")
	assert.Zero(t, repo.callCount("ListCountries"), "the countries must not be read per record")
}

// TestProviderSkipsUnrequestedJoins proves that no query at all is made for
// sub-records that were not asked for.
//
// Field selection is not only for shrinking the response; the COST of an
// expansion that was not asked for must not be paid either.
func TestProviderSkipsUnrequestedJoins(t *testing.T) {
	ctx := context.Background()
	provider, svc, repo := newTestProvider(t)
	region := newRegion(t, svc, "TRY")
	_, err := svc.AddCountryToRegion(ctx, region.ID, "TR")
	require.NoError(t, err)

	repo.resetCalls()
	records, err := provider.FetchByIDs(ctx, []string{region.ID}, []string{"currency_code"})
	require.NoError(t, err)
	require.Len(t, records, 1)

	assert.Zero(t, repo.callCount("GetCurrenciesByCodes"), "the currency was not requested, it must not be read")
	assert.Zero(t, repo.callCount("ListCountriesByRegions"), "the countries were not requested, they must not be read")
	assert.NotContains(t, records[0], "currency")
	assert.NotContains(t, records[0], "countries")
	assert.Contains(t, records[0], query.IDField, "the id has to be added even when it is not requested")
}

// TestProviderRejectsUnknownField proves that errors.Invalid is returned for an
// unrecognized field (ADR 0004: field validation belongs to the provider).
func TestProviderRejectsUnknownField(t *testing.T) {
	ctx := context.Background()
	provider, _, repo := newTestProvider(t)

	_, err := provider.FetchByIDs(ctx, []string{"reg_1"}, []string{"tax_rate", "hidden_field"})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Zero(t, repo.callCount("GetRegionsByIDs"), "field validation has to happen BEFORE the read")
}

// TestProviderIDFilter proves the forms the id filter accepts and the forms it
// rejects.
func TestProviderIDFilter(t *testing.T) {
	ctx := context.Background()
	provider, svc, _ := newTestProvider(t)
	first := newRegion(t, svc, "TRY")
	newRegion(t, svc, "USD")

	records, err := provider.List(ctx, query.ListOptions{Filters: map[string]any{"id": first.ID}})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, first.ID, records[0][query.IDField])

	records, err = provider.List(ctx, query.ListOptions{
		Filters: map[string]any{"id": []string{first.ID}},
	})
	require.NoError(t, err)
	assert.Len(t, records, 1)

	// An empty slice means "no ids"; its meaning is distinct from nil.
	records, err = provider.List(ctx, query.ListOptions{
		Filters: map[string]any{"id": []string{}},
	})
	require.NoError(t, err)
	assert.Empty(t, records)

	_, err = provider.List(ctx, query.ListOptions{Filters: map[string]any{"name": "X"}})
	require.Error(t, err, "an unsupported filter has to be rejected")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = provider.List(ctx, query.ListOptions{Filters: map[string]any{"id": 42}})
	require.Error(t, err, "a filter of the wrong type has to be rejected")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestProviderListAppliesModuleDefaultLimit proves that the module's page size
// is applied in place of the "unlimited" in the Query contract.
//
// An unlimited root list would load the whole table into memory in a single
// request.
func TestProviderListAppliesModuleDefaultLimit(t *testing.T) {
	ctx := context.Background()
	provider, svc, repo := newTestProvider(t)
	for range 3 {
		newRegion(t, svc, "TRY")
	}

	records, err := provider.List(ctx, query.ListOptions{Limit: 0})
	require.NoError(t, err)
	assert.Len(t, records, 3)
	limit, offset := repo.lastPaging()
	assert.Equal(t, DefaultLimit, limit, "an unlimited request has to fall back to the module's default")
	assert.Zero(t, offset)

	// A limit that does not fit in int32 must NOT WRAP; after being clamped it
	// has to be cut to the maximum value. Had it wrapped, a negative limit
	// would have gone to the database.
	records, err = provider.List(ctx, query.ListOptions{Limit: 1 << 40})
	require.NoError(t, err)
	assert.Len(t, records, 3)
	limit, _ = repo.lastPaging()
	assert.Equal(t, MaxLimit, limit, "the limit has to be cut to the maximum value")

	_, err = provider.List(ctx, query.ListOptions{Offset: -1})
	require.Error(t, err, "a negative offset has to be rejected")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestProviderRecordsAreIndependent proves that the returned records do not
// share each other's state.
//
// Query writes the expansion result INTO the record; a shared map would mean
// that one record's expansion also shows up in another.
func TestProviderRecordsAreIndependent(t *testing.T) {
	ctx := context.Background()
	provider, svc, _ := newTestProvider(t)
	newRegion(t, svc, "TRY")
	newRegion(t, svc, "USD")

	records, err := provider.List(ctx, query.ListOptions{})
	require.NoError(t, err)
	require.Len(t, records, 2)

	records[0]["extra_field"] = "x"
	assert.NotContains(t, records[1], "extra_field")
}
