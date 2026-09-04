package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestQueryProviderEntityName validates that the provider matches the name it is
// registered under.
//
// Query looks the provider up under the name "<entity>.query" and checks that
// the name matches Entity() (ADR 0004); the two diverging would mean a NotFound
// at run time.
func TestQueryProviderEntityName(t *testing.T) {
	e := newEnv(t)

	assert.Equal(t, service.EntityName, service.NewQueryProvider(e.svc).Entity())
	assert.Equal(t, "order", service.EntityName)
}

// TestQueryProviderProducesTheRequestedFields validates that the requested
// fields are produced.
func TestQueryProviderProducesTheRequestedFields(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := service.NewQueryProvider(e.svc)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	records, err := p.List(ctx, query.ListOptions{
		Fields: []string{service.FieldID, service.FieldDisplayID, service.FieldStatus, service.FieldTotal},
	})
	require.NoError(t, err)
	require.Len(t, records, 1)

	assert.Equal(t, query.Record{
		service.FieldID:        order.ID,
		service.FieldDisplayID: order.DisplayID,
		service.FieldStatus:    models.OrderPending.String(),
		service.FieldTotal:     int64(6100),
	}, records[0])
}

// TestQueryProviderARequestWithoutFieldsReturnsAllFields validates that the
// default set is returned when no field is selected.
func TestQueryProviderARequestWithoutFieldsReturnsAllFields(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := service.NewQueryProvider(e.svc)

	_, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	records, err := p.List(ctx, query.ListOptions{})
	require.NoError(t, err)
	require.Len(t, records, 1)

	for _, field := range []string{
		service.FieldID, service.FieldDisplayID, service.FieldStatus,
		service.FieldRegionID, service.FieldCustomerID, service.FieldEmail,
		service.FieldCurrencyCode, service.FieldCartID, service.FieldSubtotal,
		service.FieldDiscountTotal, service.FieldTaxTotal, service.FieldShippingTotal,
		service.FieldTotal, service.FieldPlacedAt, service.FieldCompletedAt,
		service.FieldCanceledAt, service.FieldCreatedAt, service.FieldUpdatedAt,
	} {
		assert.Contains(t, records[0], field)
	}
	assert.Nil(t, records[0][service.FieldCompletedAt],
		"on an order that is not completed the stamp has to be nil")
}

// TestQueryProviderRejectsAnUndefinedField validates that a field that is not
// offered is rejected with Invalid (ADR 0004).
func TestQueryProviderRejectsAnUndefinedField(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := service.NewQueryProvider(e.svc)

	_, err := p.List(ctx, query.ListOptions{Fields: []string{"cancel_reason"}})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = p.FetchByIDs(ctx, []string{"order_1"}, []string{"hidden_field"})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestQueryProviderFilters validates the supported and the unsupported filters.
func TestQueryProviderFilters(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := service.NewQueryProvider(e.svc)

	guest := validInput()
	guest.CustomerID = ""
	_, err := e.svc.CreateOrder(ctx, guest)
	require.NoError(t, err)
	_, err = e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	records, err := p.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FieldCustomerID: testCustomerID},
		Fields:  []string{service.FieldID},
	})
	require.NoError(t, err)
	assert.Len(t, records, 1)

	records, err = p.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FieldStatus: models.OrderPending.String()},
		Fields:  []string{service.FieldID},
	})
	require.NoError(t, err)
	assert.Len(t, records, 2)

	_, err = p.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FieldCustomerID: 42},
	})
	require.Error(t, err, "a filter with the wrong type has to be rejected")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = p.List(ctx, query.ListOptions{
		Filters: map[string]any{"email": "a@b.com"},
	})
	require.Error(t, err, "an unsupported filter has to be rejected")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestQueryProviderFetchByIDsReadsInBatch validates the behavior of the batch
// read.
//
// An identifier that is not found IS NOT AN ERROR; it simply returns no record
// (ADR 0004).
func TestQueryProviderFetchByIDsReadsInBatch(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := service.NewQueryProvider(e.svc)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	records, err := p.FetchByIDs(ctx, []string{order.ID, "order_MISSING"}, []string{service.FieldID})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, order.ID, records[0][service.FieldID])

	empty, err := p.FetchByIDs(ctx, nil, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

// TestQueryProviderClipsAnUnlimitedRequestToTheCeiling validates that the core's
// "0 = unlimited" contract is brought down to the provider's ceiling.
//
// An unlimited root query would take the whole order table into memory. The
// request is not rejected, it is CLIPPED: the caller explicitly said "I want all
// of them" and has to get the most it can get.
func TestQueryProviderClipsAnUnlimitedRequestToTheCeiling(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := service.NewQueryProvider(e.svc)

	_, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	for _, limit := range []int{0, -5, int(service.MaxLimit) + 1000} {
		records, err := p.List(ctx, query.ListOptions{Limit: limit, Fields: []string{service.FieldID}})
		require.NoError(t, err, "limit=%d must not be rejected", limit)
		assert.Len(t, records, 1)
	}
}
