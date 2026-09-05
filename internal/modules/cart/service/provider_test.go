package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// TestProviderEntityNameMatchesTheRegistrationTemplate verifies that the entity
// name the provider offers is the prefix of the name it is registered in the
// container under.
//
// Query looks the provider up by the name "<entity>.query" and VALIDATES the
// overlap with Entity() (ADR 0004); if the two names drift apart, the error is
// left to runtime.
func TestProviderEntityNameMatchesTheRegistrationTemplate(t *testing.T) {
	svc, _ := newService(t)

	provider := service.NewQueryProvider(svc)

	assert.Equal(t, "cart", provider.Entity())
	assert.Equal(t, "cart.query", provider.Entity()+query.ProviderSuffix)
}

// TestProviderListReturnsRecords verifies that the listing returns records and
// that they carry the join key (id).
func TestProviderListReturnsRecords(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CustomerID: customerID, CurrencyCode: currency,
	})
	require.NoError(t, err)
	provider := service.NewQueryProvider(svc)

	records, err := provider.List(ctx, query.ListOptions{})

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, cart.ID, records[0][query.IDField])
	assert.Equal(t, regionID, records[0][service.FieldRegionID])
	assert.Equal(t, customerID, records[0][service.FieldCustomerID])
	assert.Equal(t, currency, records[0][service.FieldCurrencyCode])
	assert.Equal(t, false, records[0][service.FieldCompleted])
	assert.Equal(t, false, records[0][service.FieldTotalsStale])
	assert.Nil(t, records[0][service.FieldCompletedAt])
}

// TestProviderReportsStaleTotals verifies that the totals being stale IS VISIBLE
// in the provider's record.
//
// Had the staleness not been offered together with the totals, a cross-module
// read would take an old amount for a current one.
func TestProviderReportsStaleTotals(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)
	_, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 1,
	})
	require.NoError(t, err)
	provider := service.NewQueryProvider(svc)

	records, err := provider.FetchByIDs(ctx, []string{cart.ID},
		[]string{query.IDField, service.FieldTotal, service.FieldTotalsStale})

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, true, records[0][service.FieldTotalsStale])
	assert.Equal(t, int64(0), records[0][service.FieldTotal])
}

// TestProviderAppliesTheFieldSelection verifies that the requested set of fields
// comes back exactly.
func TestProviderAppliesTheFieldSelection(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)
	provider := service.NewQueryProvider(svc)

	records, err := provider.FetchByIDs(ctx, []string{cart.ID},
		[]string{query.IDField, service.FieldCurrencyCode})

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Len(t, records[0], 2, "only the requested fields must come back")
	assert.Contains(t, records[0], query.IDField)
	assert.Contains(t, records[0], service.FieldCurrencyCode)
}

// TestProviderRejectsAnUndefinedField verifies that a field that is not offered
// is rejected with errors.Invalid (ADR 0004).
func TestProviderRejectsAnUndefinedField(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	provider := service.NewQueryProvider(svc)

	_, err := provider.List(ctx, query.ListOptions{Fields: []string{"hidden_field"}})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = provider.FetchByIDs(ctx, []string{"cart_X"}, []string{"hidden_field"})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestProviderAppliesTheFilters verifies that the supported filters work and
// that an unsupported one is rejected.
func TestProviderAppliesTheFilters(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	provider := service.NewQueryProvider(svc)

	_, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CustomerID: customerID, CurrencyCode: currency,
	})
	require.NoError(t, err)
	_, err = svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionOther, CurrencyCode: currency,
	})
	require.NoError(t, err)

	records, err := provider.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FieldCustomerID: customerID},
	})
	require.NoError(t, err)
	assert.Len(t, records, 1)

	records, err = provider.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FieldCompleted: false},
	})
	require.NoError(t, err)
	assert.Len(t, records, 2)

	_, err = provider.List(ctx, query.ListOptions{
		Filters: map[string]any{"email": "a@b.c"},
	})
	require.Error(t, err, "an unsupported filter must be rejected")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = provider.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FieldCompleted: "yes"},
	})
	require.Error(t, err, "a filter with the wrong type must be rejected")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestProviderClampsAnUnlimitedRequestToTheCeiling verifies that the core's
// "0 = unlimited" contract is turned into the provider's ceiling.
//
// An unlimited root query would pull the whole cart table into memory; the
// clamping is silent and returns no error, because the limit here is not client
// input but a query definition.
func TestProviderClampsAnUnlimitedRequestToTheCeiling(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	provider := service.NewQueryProvider(svc)

	for range 3 {
		_, err := svc.CreateCart(ctx, service.CreateCartInput{
			RegionID: regionID, CurrencyCode: currency,
		})
		require.NoError(t, err)
	}

	for _, limit := range []int{0, -5, int(service.MaxLimit) + 1000} {
		records, err := provider.List(ctx, query.ListOptions{Limit: limit})
		require.NoError(t, err, "the limit %d must not produce an error", limit)
		assert.Len(t, records, 3)
	}
}

// TestProviderMissingIdentifierIsNotAnError verifies that a missing identifier
// returns no record but does not produce an error either (the ADR 0004
// contract).
func TestProviderMissingIdentifierIsNotAnError(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	cart := newCart(ctx, t, svc)
	provider := service.NewQueryProvider(svc)

	records, err := provider.FetchByIDs(ctx, []string{cart.ID, "cart_MISSING"}, nil)

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, cart.ID, records[0][query.IDField])
}

// TestProviderEmptyIdentifierListReturnsAnEmptySlice verifies that an empty
// identifier list returns an empty (non-nil) slice.
func TestProviderEmptyIdentifierListReturnsAnEmptySlice(t *testing.T) {
	svc, _ := newService(t)
	provider := service.NewQueryProvider(svc)

	records, err := provider.FetchByIDs(context.Background(), nil, nil)

	require.NoError(t, err)
	assert.NotNil(t, records)
	assert.Empty(t, records)
}
