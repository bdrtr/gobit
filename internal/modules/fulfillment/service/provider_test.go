package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// querySetup prepares the Query provider and a catalog with two options.
func querySetup(t *testing.T) (*service.QueryProvider, testSetup, string) {
	t.Helper()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	optionID := setup.createOption(t, service.CreateOptionInput{
		Name:              "Standard shipping",
		ShippingProfileID: profileID,
		Amount:            2_000,
		RegionID:          "reg_tr",
	})
	setup.createOption(t, service.CreateOptionInput{
		Name:              "Calculated shipping",
		ShippingProfileID: profileID,
		PriceType:         "calculated",
	})
	return service.NewQueryProvider(setup.svc), setup, optionID
}

// TestQueryProviderEntityName proves the name overlap of ADR 0004.
func TestQueryProviderEntityName(t *testing.T) {
	t.Parallel()

	provider, _, _ := querySetup(t)
	assert.Equal(t, "shipping_option", provider.Entity())
	assert.Equal(t, service.EntityName, provider.Entity())
}

// TestQueryListAppliesTheFilter proves that the supported filter works.
func TestQueryListAppliesTheFilter(t *testing.T) {
	t.Parallel()

	provider, _, optionID := querySetup(t)

	records, err := provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{"region_id": "reg_tr"},
		Fields:  []string{"id", "name", "amount"},
	})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, optionID, records[0]["id"])
	assert.Equal(t, int64(2_000), records[0]["amount"])
	assert.Len(t, records[0], 3, "only the requested fields have to be returned")
}

// TestQueryRejectsAnUnrecognizedFilter proves the requirement of ADR 0004.
func TestQueryRejectsAnUnrecognizedFilter(t *testing.T) {
	t.Parallel()

	provider, _, _ := querySetup(t)

	_, err := provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{"tracking_number": "TK-1"},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the error has to be errors.Invalid: %v", err)

	_, err = provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{"region_id": 42},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a non-text filter value has to be rejected: %v", err)
}

// TestQueryRejectsAnUnrecognizedField proves that a field that is not offered
// cannot be requested.
func TestQueryRejectsAnUnrecognizedField(t *testing.T) {
	t.Parallel()

	provider, _, _ := querySetup(t)

	_, err := provider.List(context.Background(), query.ListOptions{Fields: []string{"data"}})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the error has to be errors.Invalid: %v", err)

	_, err = provider.FetchByIDs(context.Background(), []string{"sopt_1"}, []string{"metadata"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the error has to be errors.Invalid: %v", err)
}

// TestQueryProviderDoesNotOfferInternalData proves that the "data" and
// "metadata" fields ARE NOT on the read surface.
//
// data is the provider's internal configuration; it must not appear on a
// cross-module read surface at all.
func TestQueryProviderDoesNotOfferInternalData(t *testing.T) {
	t.Parallel()

	provider, _, _ := querySetup(t)

	records, err := provider.List(context.Background(), query.ListOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, records)
	assert.NotContains(t, records[0], "data", "the provider configuration must not be offered")
	assert.NotContains(t, records[0], "metadata", "schemaless free-form data must not be offered")
	assert.Contains(t, records[0], "admin_only", "a cross-module read has to see admin_only")
}

// TestQueryFetchByIDsReturnsABatch proves the counterpart of the N+1 ban: an
// identifier that is not found is not an error, it simply returns no record.
func TestQueryFetchByIDsReturnsABatch(t *testing.T) {
	t.Parallel()

	provider, _, optionID := querySetup(t)

	records, err := provider.FetchByIDs(context.Background(),
		[]string{optionID, "sopt_NOSUCH"}, []string{"id"})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, optionID, records[0]["id"])

	empty, err := provider.FetchByIDs(context.Background(), nil, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

// TestQueryUnlimitedLimitIsClampedToTheCeiling proves that the core's
// "0 = unlimited" contract is turned into the ceiling in this provider.
//
// An unlimited root query would pull the entire option table into memory.
func TestQueryUnlimitedLimitIsClampedToTheCeiling(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	for i := range int(service.MaxLimit) + 5 {
		setup.createOption(t, service.CreateOptionInput{
			Name:              "Shipping " + string(rune('A'+i%26)) + string(rune('0'+i/26)),
			ShippingProfileID: profileID,
			Amount:            int64(1_000 + i),
		})
	}
	provider := service.NewQueryProvider(setup.svc)

	records, err := provider.List(context.Background(), query.ListOptions{Limit: 0})
	require.NoError(t, err)
	assert.Len(t, records, int(service.MaxLimit), "an unlimited request has to be clamped to the ceiling")
}
