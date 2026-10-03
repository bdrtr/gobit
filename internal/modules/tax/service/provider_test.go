package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// newTestQueryProvider builds a seeded Query provider.
func newTestQueryProvider(t *testing.T) (*QueryProvider, *memRepo) {
	t.Helper()

	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedRootRegion(usRegionID, "US")
	repo.seedProvinceRegion(trIstanbul, "TR", "34", trRegionID)
	repo.seedDefaultRate(rateA, trRegionID, 2000)
	repo.seedRuledRate(rateB, trRegionID, 100)
	repo.seedDefaultRate(rateC, usRegionID, 725)
	return NewQueryProvider(svc), repo
}

// TestQueryProviderEntityName checks that the provider is consistent with its
// registration name.
//
// Query looks the provider up by the name "<entity>.query" and checks that
// Entity() and the name agree; if the two diverge the provider is never found.
func TestQueryProviderEntityName(t *testing.T) {
	provider, _ := newTestQueryProvider(t)
	assert.Equal(t, "tax_region", provider.Entity())
	assert.Equal(t, Entity, provider.Entity())
}

// TestQueryProviderAllFields checks the default field set.
func TestQueryProviderAllFields(t *testing.T) {
	provider, _ := newTestQueryProvider(t)

	records, err := provider.FetchByIDs(context.Background(), []string{trRegionID}, nil)
	require.NoError(t, err)
	require.Len(t, records, 1)

	record := records[0]
	assert.Equal(t, trRegionID, record["id"])
	assert.Equal(t, "TR", record["country_code"])
	assert.Equal(t, "", record["province_code"], "a root region's province code has to be an empty string")
	assert.Equal(t, "", record["parent_id"])
	assert.Equal(t, "", record["provider_id"])
	assert.Contains(t, record, "created_at")
	assert.Contains(t, record, "updated_at")

	rates, ok := record["rates"].([]map[string]any)
	require.True(t, ok, "the rates have to be a slice of sub-records: %#v", record["rates"])
	require.Len(t, rates, 2)
	assert.Equal(t, rateA, rates[0]["id"], "the default rate has to come first")
	assert.Equal(t, int32(2000), rates[0]["rate_bps"])
	assert.Equal(t, true, rates[0]["is_default"])
}

// TestQueryProviderFieldSelection checks narrowing the fields with Fields.
func TestQueryProviderFieldSelection(t *testing.T) {
	provider, repo := newTestQueryProvider(t)

	records, err := provider.FetchByIDs(context.Background(), []string{trRegionID}, []string{"country_code"})
	require.NoError(t, err)
	require.Len(t, records, 1)

	assert.Contains(t, records[0], "id", "the id has to be added even when not asked for (the join key)")
	assert.Contains(t, records[0], "country_code")
	assert.NotContains(t, records[0], "rates")
	assert.Zero(t, repo.callCount("ListTaxRatesByRegions"),
		"if the rates were not asked for, the rate query must NOT be made at all")
}

// TestQueryProviderRejectsAnUnknownField checks that field validation belongs
// to the provider (ADR 0004).
func TestQueryProviderRejectsAnUnknownField(t *testing.T) {
	provider, _ := newTestQueryProvider(t)

	_, err := provider.FetchByIDs(context.Background(), []string{trRegionID}, []string{"tax_rate"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Contains(t, err.Error(), "tax_region")
}

// TestQueryProviderReadsInBulk checks that a FIXED number of queries is made
// per expansion.
func TestQueryProviderReadsInBulk(t *testing.T) {
	provider, repo := newTestQueryProvider(t)

	records, err := provider.FetchByIDs(context.Background(),
		[]string{trRegionID, usRegionID, trIstanbul}, nil)
	require.NoError(t, err)
	assert.Len(t, records, 3)

	assert.Equal(t, 1, repo.callCount("GetTaxRegionsByIDs"))
	assert.Equal(t, 1, repo.callCount("ListTaxRatesByRegions"),
		"no rate query may be made per region (no N+1)")
}

// TestQueryProviderAnIDNotFoundIsNotAnError checks that a missing id is
// skipped silently (ADR 0004).
func TestQueryProviderAnIDNotFoundIsNotAnError(t *testing.T) {
	provider, _ := newTestQueryProvider(t)

	records, err := provider.FetchByIDs(context.Background(),
		[]string{trRegionID, models.TaxRegionIDPrefix + "MISSING"}, nil)
	require.NoError(t, err)
	assert.Len(t, records, 1)

	records, err = provider.FetchByIDs(context.Background(), nil, nil)
	require.NoError(t, err)
	assert.Empty(t, records)
}

// TestQueryProviderListFilters checks the supported and unsupported filters.
func TestQueryProviderListFilters(t *testing.T) {
	t.Run("id string", func(t *testing.T) {
		provider, _ := newTestQueryProvider(t)
		records, err := provider.List(context.Background(), query.ListOptions{
			Filters: map[string]any{"id": trRegionID},
		})
		require.NoError(t, err)
		require.Len(t, records, 1)
		assert.Equal(t, trRegionID, records[0]["id"])
	})

	t.Run("id slice", func(t *testing.T) {
		provider, _ := newTestQueryProvider(t)
		records, err := provider.List(context.Background(), query.ListOptions{
			Filters: map[string]any{"id": []string{trRegionID, usRegionID}},
		})
		require.NoError(t, err)
		assert.Len(t, records, 2)
	})

	t.Run("an empty id slice means no records", func(t *testing.T) {
		provider, _ := newTestQueryProvider(t)
		records, err := provider.List(context.Background(), query.ListOptions{
			Filters: map[string]any{"id": []string{}},
		})
		require.NoError(t, err)
		assert.Empty(t, records)
	})

	t.Run("country code", func(t *testing.T) {
		provider, _ := newTestQueryProvider(t)
		records, err := provider.List(context.Background(), query.ListOptions{
			Filters: map[string]any{"country_code": "TR"},
		})
		require.NoError(t, err)
		assert.Len(t, records, 2, "the TR root and its province have to come back")
	})

	t.Run("unsupported filter", func(t *testing.T) {
		provider, _ := newTestQueryProvider(t)
		_, err := provider.List(context.Background(), query.ListOptions{
			Filters: map[string]any{"province_code": "34"},
		})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err))
	})

	t.Run("wrong filter type", func(t *testing.T) {
		provider, _ := newTestQueryProvider(t)
		_, err := provider.List(context.Background(), query.ListOptions{
			Filters: map[string]any{"id": 42},
		})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err))
	})
}

// TestQueryProviderTheIDFilterIsUsedOnItsOwn checks that a narrowing put next
// to the id filter is NOT dropped SILENTLY.
//
// Dropping it silently goes against the principle of this very provider, which
// rejects an unsupported filter: the caller believes the narrowing it sent was
// applied and ends up with a wider set than it asked for.
func TestQueryProviderTheIDFilterIsUsedOnItsOwn(t *testing.T) {
	provider, _ := newTestQueryProvider(t)

	_, err := provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{
			fieldID:          []string{trRegionID, usRegionID},
			fieldCountryCode: "TR",
		},
	})
	require.Error(t, err, "the country filter must not be ignored silently")
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, CodeInvalidInput, errors.CodeOf(err))
}

// TestQueryProviderListPages checks that while the limit is zero the default
// page size applies, NOT an unbounded one.
func TestQueryProviderListPages(t *testing.T) {
	provider, _ := newTestQueryProvider(t)

	records, err := provider.List(context.Background(), query.ListOptions{Limit: 1})
	require.NoError(t, err)
	assert.Len(t, records, 1)

	all, err := provider.List(context.Background(), query.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, all, 3, "if no limit is given the default page size has to apply")
}

// TestQueryProviderUnconfiguredService checks that with a nil repository a
// typed error comes back instead of a panic.
func TestQueryProviderUnconfiguredService(t *testing.T) {
	provider := NewQueryProvider(New(nil, Options{}))

	_, err := provider.List(context.Background(), query.ListOptions{})
	require.Error(t, err)
	assert.Equal(t, CodeUnconfigured, errors.CodeOf(err))

	_, err = provider.FetchByIDs(context.Background(), []string{trRegionID}, nil)
	require.Error(t, err)
}
