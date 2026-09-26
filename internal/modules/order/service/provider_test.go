package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
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

// TestQueryProviderSelectsOneOrderByID is the filter a caller holding an
// identifier needs.
//
// FetchByIDs already read orders by identifier, but that is the EXPANSION path
// — the read layer calls it when an order hangs off another record's link. A
// caller with an order id and a root query had no way through, and the product
// provider offers exactly this filter, so the inconsistency was one a caller
// discovered by getting a 422.
//
// It was discovered that way: the admin panel's order page asked for one order
// and the provider refused the filter.
func TestQueryProviderSelectsOneOrderByID(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := service.NewQueryProvider(e.svc)

	first, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	_, err = e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	records, err := p.List(ctx, query.ListOptions{
		Fields:  []string{service.FieldID, service.FieldTotal},
		Filters: map[string]any{service.FieldID: first.ID},
	})
	require.NoError(t, err)
	require.Len(t, records, 1, "the filter has to select ONE order out of two")
	assert.Equal(t, first.ID, records[0][service.FieldID])
}

// TestQueryProviderAcceptsAListOfIDs covers the batch shape of the same filter.
//
// A single string and a slice take the same path so a caller reading one record
// does not have to wrap its identifier, and the []any form is what the filter
// looks like when it arrives as JSON.
func TestQueryProviderAcceptsAListOfIDs(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := service.NewQueryProvider(e.svc)

	first, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	second, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	for name, filter := range map[string]any{
		"a string slice": []string{first.ID, second.ID},
		"a JSON list":    []any{first.ID, second.ID},
	} {
		t.Run(name, func(t *testing.T) {
			records, err := p.List(ctx, query.ListOptions{
				Fields:  []string{service.FieldID},
				Filters: map[string]any{service.FieldID: filter},
			})
			require.NoError(t, err)
			assert.Len(t, records, 2)
		})
	}
}

// TestQueryProviderRefusesAnIDFilterBesideAnother holds a combination the
// short-circuit cannot honor.
//
// The id filter answers from the batch read, which applies no other criterion.
// Accepting a second filter would silently ignore it — the caller would get the
// order it named even when that order does not match what else it asked for.
func TestQueryProviderRefusesAnIDFilterBesideAnother(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := service.NewQueryProvider(e.svc)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	_, err = p.List(ctx, query.ListOptions{
		Fields: []string{service.FieldID},
		Filters: map[string]any{
			service.FieldID:     order.ID,
			service.FieldStatus: models.OrderCanceled.String(),
		},
	})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestTheReadLayerCarriesWhereAnOrderGoes holds the address fields the panel
// reads (ADR 0196): the current addresses by the API's names, the moment of the
// latest correction, the order an addition adds to — and one address read for
// the whole page, made only when an address field is asked for.
func TestTheReadLayerCarriesWhereAnOrderGoes(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	p := service.NewQueryProvider(e.svc)

	in := validInput()
	in.Addresses = []models.OrderAddress{
		{Type: models.AddressShipping, FirstName: "Ada", Address1: "12 Wrong St", CountryCode: "TR"},
		{Type: models.AddressBilling, Company: "Engines Ltd", CountryCode: "TR",
			Metadata: map[string]any{"tax_office": "Central"}},
	}
	parent, err := e.svc.CreateOrder(ctx, in)
	require.NoError(t, err)
	addition, err := e.svc.CreateOrder(ctx, additionOf(parent.ID))
	require.NoError(t, err)

	reads := e.store.addressReads
	records, err := p.List(ctx, query.ListOptions{
		Fields: []string{service.FieldID, service.FieldAddsToOrderID},
	})
	require.NoError(t, err)
	assert.Equal(t, reads, e.store.addressReads, "no address field was asked for, so none was read")
	byID := map[string]query.Record{}
	for _, record := range records {
		id, ok := record[service.FieldID].(string)
		require.True(t, ok)
		byID[id] = record
	}
	assert.Equal(t, parent.ID, byID[addition.ID][service.FieldAddsToOrderID])
	assert.Empty(t, byID[parent.ID][service.FieldAddsToOrderID])

	additions, err := p.List(ctx, query.ListOptions{
		Fields:  []string{service.FieldID},
		Filters: map[string]any{service.FieldAddsToOrderID: parent.ID},
	})
	require.NoError(t, err)
	require.Len(t, additions, 1)
	assert.Equal(t, addition.ID, additions[0][service.FieldID])

	_, err = e.svc.CorrectShippingAddress(ctx, parent.ID, models.OrderAddress{
		FirstName: "Ada", Address1: "12 Almost St",
	})
	require.NoError(t, err)
	_, err = e.svc.CorrectShippingAddress(ctx, parent.ID, models.OrderAddress{
		FirstName: "Ada", Address1: "12 Right St",
	})
	require.NoError(t, err)
	held, err := e.store.OrderAddressesByOrderIDs(ctx, []string{parent.ID})
	require.NoError(t, err)
	var latest time.Time
	for _, address := range held[parent.ID] {
		if address.SupersededAt != nil && address.SupersededAt.After(latest) {
			latest = *address.SupersededAt
		}
	}

	reads = e.store.addressReads
	page, err := p.List(ctx, query.ListOptions{Fields: []string{
		service.FieldID, service.FieldShippingAddress, service.FieldBillingAddress,
		service.FieldShippingAddressCorrectedAt,
	}})
	require.NoError(t, err)
	assert.Equal(t, reads+1, e.store.addressReads, "one address read for the whole page")

	for _, record := range page {
		switch record[service.FieldID] {
		case parent.ID:
			assert.Equal(t, map[string]any{
				"first_name": "Ada", "address_1": "12 Right St", "country_code": "TR",
			}, record[service.FieldShippingAddress], "the current address, empty fields left out")
			assert.Equal(t, map[string]any{
				"company": "Engines Ltd", "country_code": "TR",
				"metadata": map[string]any{"tax_office": "Central"},
			}, record[service.FieldBillingAddress])
			assert.Equal(t, latest, record[service.FieldShippingAddressCorrectedAt],
				"after two corrections the page reads the LATER one")
		case addition.ID:
			assert.Nil(t, record[service.FieldShippingAddress], "an order with no address reads nil")
			assert.Nil(t, record[service.FieldShippingAddressCorrectedAt], "never corrected")
		}
	}
}
