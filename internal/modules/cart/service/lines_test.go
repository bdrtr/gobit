package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// A cart's lines through the read layer (ADR 0290).

// lineCountingStore counts the batch reads the extra fields cost: the lines,
// the addresses and the shipping methods, one each.
type lineCountingStore struct {
	*fakeStore
	reads, addressReads, methodReads int
}

func (s *lineCountingStore) ListLineItemsOfCarts(ctx context.Context, cartIDs []string) ([]models.LineItem, error) {
	s.reads++

	return s.fakeStore.ListLineItemsOfCarts(ctx, cartIDs)
}

func (s *lineCountingStore) ListCartAddressesOfCarts(ctx context.Context, cartIDs []string) ([]models.CartAddress, error) {
	s.addressReads++

	return s.fakeStore.ListCartAddressesOfCarts(ctx, cartIDs)
}

func (s *lineCountingStore) ListShippingMethodsOfCarts(
	ctx context.Context, cartIDs []string,
) ([]models.ShippingMethod, error) {
	s.methodReads++

	return s.fakeStore.ListShippingMethodsOfCarts(ctx, cartIDs)
}

// linedService is a service over a counting store, and a cart with two lines
// beside an empty cart.
func linedService(t *testing.T) (svc *service.Service, store *lineCountingStore, lined, empty string) {
	t.Helper()

	store = &lineCountingStore{fakeStore: newFakeStore()}
	svc, err := service.New(service.Options{Repo: store, Events: &fakeBus{}})
	require.NoError(t, err)

	ctx := context.Background()
	open := func() string {
		cart, err := svc.CreateCart(ctx, service.CreateCartInput{RegionID: regionID, CurrencyCode: currency})
		require.NoError(t, err)
		return cart.ID
	}
	lined, empty = open(), open()
	for _, line := range []service.AddLineItemInput{
		{VariantID: variantA, Title: "T-shirt", Quantity: 3, UnitPrice: 1_000},
		{VariantID: variantB, Title: "Trousers", Quantity: 1, UnitPrice: 4_000},
	} {
		_, err := svc.AddLineItem(ctx, lined, line)
		require.NoError(t, err)
	}

	return svc, store, lined, empty
}

// TestTheProviderNamesACartsLines: each line with its variant, title,
// quantity and unit price, in the order they were written; a cart with none
// answers an empty list, not a missing one.
func TestTheProviderNamesACartsLines(t *testing.T) {
	svc, _, lined, empty := linedService(t)

	records, err := service.NewQueryProvider(svc).FetchByIDs(context.Background(),
		[]string{lined, empty}, []string{query.IDField, service.FieldLines})
	require.NoError(t, err)
	require.Len(t, records, 2)

	byCart := map[string][]map[string]any{}
	for _, record := range records {
		lines, ok := record[service.FieldLines].([]map[string]any)
		require.True(t, ok, "lines is a list of records: %T", record[service.FieldLines])
		id, ok := record[query.IDField].(string)
		require.True(t, ok)
		byCart[id] = lines
	}

	require.Len(t, byCart[lined], 2)
	first := byCart[lined][0]
	assert.Equal(t, variantA, first[service.LineVariantID])
	assert.Equal(t, "T-shirt", first[service.LineTitle])
	assert.Equal(t, int64(3), first[service.LineQuantity])
	assert.Equal(t, int64(1_000), first[service.LineUnitPrice])
	assert.Equal(t, "", first[service.LineParentID], "a line standing on its own follows nothing")
	assert.NotEmpty(t, first[service.LineID])
	assert.Equal(t, "Trousers", byCart[lined][1][service.LineTitle])

	assert.NotNil(t, byCart[empty])
	assert.Empty(t, byCart[empty])
}

// TestTheLinesAreReadOnlyWhenAskedFor is the movements' cost rule, for the
// lines and for the address and the methods (ADR 0291): a read that names other
// fields reads none of them, and a read that names none gets them with every
// other field the entity offers, in one read each for the page.
func TestTheLinesAreReadOnlyWhenAskedFor(t *testing.T) {
	svc, store, lined, _ := linedService(t)
	provider := service.NewQueryProvider(svc)

	records, err := provider.FetchByIDs(context.Background(), []string{lined},
		[]string{query.IDField, service.FieldTotal})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.NotContains(t, records[0], service.FieldLines)
	assert.Zero(t, store.reads)
	assert.Zero(t, store.addressReads)
	assert.Zero(t, store.methodReads)

	all, err := provider.List(context.Background(), query.ListOptions{})
	require.NoError(t, err)
	require.Len(t, all, 2)
	for _, field := range []string{service.FieldLines, service.FieldShippingAddress, service.FieldShippingMethods} {
		assert.Contains(t, all[0], field, "a read that names no field gets every field the entity offers")
	}
	assert.Equal(t, 1, store.reads, "two carts, one read")
	assert.Equal(t, 1, store.addressReads)
	assert.Equal(t, 1, store.methodReads)
}

// TestACartIsReadByIDThroughARootQuery: the id filter answers the batch read,
// one id or a list, and refuses to be combined or given anything but text
// (ADR 0290).
func TestACartIsReadByIDThroughARootQuery(t *testing.T) {
	svc, _, lined, empty := linedService(t)
	provider := service.NewQueryProvider(svc)
	ctx := context.Background()

	one, err := provider.List(ctx, query.ListOptions{
		Fields: []string{query.IDField}, Filters: map[string]any{query.IDField: lined},
	})
	require.NoError(t, err)
	require.Len(t, one, 1)
	assert.Equal(t, lined, one[0][query.IDField])

	both, err := provider.List(ctx, query.ListOptions{
		Fields: []string{query.IDField}, Filters: map[string]any{query.IDField: []any{lined, empty}},
	})
	require.NoError(t, err)
	assert.Len(t, both, 2)

	for name, filters := range map[string]map[string]any{
		"combined":     {query.IDField: lined, service.FieldCompleted: false},
		"not text":     {query.IDField: 7},
		"a list of 7s": {query.IDField: []any{7}},
	} {
		_, err := provider.List(ctx, query.ListOptions{Filters: filters})
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalid(err), name)
	}
}

// TestTheProviderNamesACartsShippingAddressAndMethods: the shipping address
// keyed as an order's is, nil before one is written, and the chosen methods,
// an empty list before one is chosen (ADR 0291).
func TestTheProviderNamesACartsShippingAddressAndMethods(t *testing.T) {
	svc, _, lined, empty := linedService(t)
	ctx := context.Background()
	_, err := svc.SetShippingAddress(ctx, lined, service.AddressInput{
		FirstName: "Ada", LastName: "Lovelace", Address1: "12 Right St", City: "Ankara",
		PostalCode: "06000", CountryCode: "TR", Phone: "+90",
	})
	require.NoError(t, err)
	_, err = svc.SetBillingAddress(ctx, lined, service.AddressInput{
		FirstName: "Billed", Address1: "1 Ledger Rd", City: "Izmir", CountryCode: "TR",
	})
	require.NoError(t, err)
	_, err = svc.AddShippingMethod(ctx, lined, service.AddShippingMethodInput{
		Name: "Courier", ShippingOptionID: "so_courier", Amount: 2_500,
	})
	require.NoError(t, err)

	records, err := service.NewQueryProvider(svc).FetchByIDs(ctx, []string{lined, empty},
		[]string{query.IDField, service.FieldShippingAddress, service.FieldShippingMethods})
	require.NoError(t, err)
	require.Len(t, records, 2)
	byCart := map[string]query.Record{}
	for _, record := range records {
		id, ok := record[query.IDField].(string)
		require.True(t, ok)
		byCart[id] = record
	}

	assert.Equal(t, map[string]any{
		service.AddressFirstName: "Ada", service.AddressLastName: "Lovelace", service.AddressCompany: "",
		service.AddressLine1: "12 Right St", service.AddressLine2: "", service.AddressCity: "Ankara",
		service.AddressProvince: "", service.AddressPostalCode: "06000", service.AddressCountryCode: "TR",
		service.AddressPhone: "+90",
	}, byCart[lined][service.FieldShippingAddress], "the shipping address, not the billing one")
	methods, ok := byCart[lined][service.FieldShippingMethods].([]map[string]any)
	require.True(t, ok)
	require.Len(t, methods, 1)
	assert.Equal(t, "Courier", methods[0][service.MethodName])
	assert.Equal(t, "so_courier", methods[0][service.MethodOptionID])
	assert.Equal(t, int64(2_500), methods[0][service.MethodAmount])
	assert.NotEmpty(t, methods[0][service.MethodID])

	assert.Nil(t, byCart[empty][service.FieldShippingAddress])
	none, ok := byCart[empty][service.FieldShippingMethods].([]map[string]any)
	require.True(t, ok)
	assert.NotNil(t, none)
	assert.Empty(t, none)
}
