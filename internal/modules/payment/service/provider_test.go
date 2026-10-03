package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// TestQueryProviderEntityName verifies that the provider matches the name it is
// registered under; Query checks this at registration time (ADR 0004).
func TestQueryProviderEntityName(t *testing.T) {
	svc, _, _ := newTestService(t)

	assert.Equal(t, "payment_collection", service.NewQueryProvider(svc).Entity())
	assert.Equal(t, service.EntityName, service.NewQueryProvider(svc).Entity())
}

// TestQueryProviderListProducesTheFields verifies that field selection works.
func TestQueryProviderListProducesTheFields(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	col := openCollection(t, svc, testAmount)
	p := service.NewQueryProvider(svc)

	records, err := p.List(ctx, query.ListOptions{
		Fields: []string{service.FieldID, service.FieldAmount, service.FieldStatus},
	})

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, col.ID, records[0][service.FieldID])
	assert.Equal(t, testAmount, records[0][service.FieldAmount])
	assert.Equal(t, models.CollectionNotPaid.String(), records[0][service.FieldStatus])
	assert.Len(t, records[0], 3, "only the requested fields must come back")
}

// TestQueryProviderRequestWithoutFieldsReturnsAllFields verifies that when no
// field is given, all of the offered fields come back.
func TestQueryProviderRequestWithoutFieldsReturnsAllFields(t *testing.T) {
	svc, _, _ := newTestService(t)
	openCollection(t, svc, testAmount)
	p := service.NewQueryProvider(svc)

	records, err := p.List(context.Background(), query.ListOptions{})

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Contains(t, records[0], service.FieldReference)
	assert.Contains(t, records[0], service.FieldCurrencyCode)
	assert.Contains(t, records[0], service.FieldAuthorizedAmount)
	assert.Contains(t, records[0], service.FieldCapturedAmount)
	assert.Contains(t, records[0], service.FieldRefundedAmount)
	assert.Contains(t, records[0], service.FieldCreatedAt)
	assert.Contains(t, records[0], service.FieldUpdatedAt)
	assert.NotContains(t, records[0], "metadata", "metadata is deliberately not offered")
}

// TestQueryProviderRejectsAnUnknownField verifies ADR 0004's requirement: when
// the provider sees a field it does not support, it must return errors.Invalid.
func TestQueryProviderRejectsAnUnknownField(t *testing.T) {
	svc, _, _ := newTestService(t)
	p := service.NewQueryProvider(svc)
	ctx := context.Background()

	_, err := p.List(ctx, query.ListOptions{Fields: []string{"metadata"}})
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "error: %v", err)

	_, err = p.FetchByIDs(ctx, []string{"paycol_X"}, []string{"secret"})
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "error: %v", err)
}

// TestQueryProviderFilters tests the supported and the unsupported filters.
func TestQueryProviderFilters(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	openCollection(t, svc, testAmount)
	p := service.NewQueryProvider(svc)

	records, err := p.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FieldReference: testReference},
	})
	require.NoError(t, err)
	assert.Len(t, records, 1)

	records, err = p.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FieldReference: "cart_MISSING"},
	})
	require.NoError(t, err)
	assert.Empty(t, records)

	_, err = p.List(ctx, query.ListOptions{Filters: map[string]any{"amount": "x"}})
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "error: %v", err)

	_, err = p.List(ctx, query.ListOptions{Filters: map[string]any{service.FieldStatus: 42}})
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "the filter must be text: %v", err)
}

// TestQueryProviderFetchByIDsBatch verifies that a set of ids is resolved in a
// single round and that a missing id is NOT an error (ADR 0004).
func TestQueryProviderFetchByIDsBatch(t *testing.T) {
	svc, _, _ := newTestService(t)
	ctx := context.Background()
	first := openCollection(t, svc, testAmount)
	second := openCollection(t, svc, testAmount)
	p := service.NewQueryProvider(svc)

	records, err := p.FetchByIDs(ctx, []string{first.ID, second.ID, "paycol_MISSING"}, []string{service.FieldID})

	require.NoError(t, err)
	assert.Len(t, records, 2, "NO record comes back for an id that was not found")

	empty, err := p.FetchByIDs(ctx, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

// TestQueryProviderLimitIsClampedToTheCeiling verifies that the core's
// "unlimited" limit is brought down to the provider's ceiling.
//
// An unlimited root query would pull the whole collection table into memory;
// the clamping is silent and returns no error, because the limit here does not
// come from client input but from another module's query definition.
func TestQueryProviderLimitIsClampedToTheCeiling(t *testing.T) {
	svc, store, _ := newTestService(t)
	ctx := context.Background()
	openCollection(t, svc, testAmount)
	p := service.NewQueryProvider(svc)

	for _, limit := range []int{0, -5, int(service.MaxLimit) + 1} {
		_, err := p.List(ctx, query.ListOptions{Limit: limit})
		require.NoError(t, err, "limit %d must not fail", limit)
	}

	// That the clamping really is applied shows in the request passing the
	// service's paging validation: had a limit above the ceiling gone straight
	// through, errors.Invalid would have come back.
	_, count, err := store.ListPaymentCollections(ctx, models.CollectionFilter{Limit: service.MaxLimit})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}
