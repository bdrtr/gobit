package service_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// newTestProvider builds a Query provider running over the fake store.
func newTestProvider(t *testing.T) (*service.QueryProvider, *fakeStore) {
	t.Helper()

	svc, store := newService(t)
	return service.NewQueryProvider(svc), store
}

// TestProviderEntity proves the provider is consistent with the name it will be
// registered in the container under; it compares Entity() with the prefix of
// the Query registration name.
func TestProviderEntity(t *testing.T) {
	provider, _ := newTestProvider(t)

	assert.Equal(t, "inventory_item", provider.Entity())
	assert.Equal(t, service.EntityName, provider.Entity())
}

// TestFetchByIDsCarriesTheSellableQuantity proves the provider returns the item
// with its TOTAL sellable quantity. product's storefront listing reads the stock
// from this field in a single call; if the field is missing or wrong, the
// product looks out of stock.
func TestFetchByIDsCarriesTheSellableQuantity(t *testing.T) {
	provider, store := newTestProvider(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 4)
	store.seedLevel(itemID, locB, 5, 1)

	records, err := provider.FetchByIDs(context.Background(), []string{itemID}, nil)

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, itemID, records[0][query.IDField])
	assert.Equal(t, "SKU-1", records[0][service.FieldSKU])
	assert.Equal(t, int64(10), records[0][service.FieldAvailableQuantity],
		"(10-4) + (5-1) has to be 10")
	assert.Equal(t, true, records[0][service.FieldRequiresShipping])
}

// TestFetchByIDsReturnsZeroForAnItemWithNoLevel proves an item with no stock
// level at all is NOT DROPPED from the records and its sellable quantity comes
// back as zero. Were it dropped, out-of-stock products would vanish from the
// storefront listing altogether.
func TestFetchByIDsReturnsZeroForAnItemWithNoLevel(t *testing.T) {
	provider, store := newTestProvider(t)
	store.seedItem(itemID, "SKU-1")

	records, err := provider.FetchByIDs(context.Background(), []string{itemID}, nil)

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, int64(0), records[0][service.FieldAvailableQuantity])
}

// TestFetchByIDsRunsInOneRoundTrip proves a SINGLE query is made for
// availability however many items are asked for (ADR 0004: N+1 is structurally
// forbidden).
func TestFetchByIDsRunsInOneRoundTrip(t *testing.T) {
	provider, store := newTestProvider(t)
	ids := []string{}
	for _, id := range []string{"invitem_1", "invitem_2", "invitem_3"} {
		store.seedItem(id, "SKU-"+id)
		store.seedLevel(id, locA, 5, 1)
		ids = append(ids, id)
	}

	records, err := provider.FetchByIDs(context.Background(), ids, nil)

	require.NoError(t, err)
	assert.Len(t, records, 3)
	assert.Equal(t, 1, store.availableCalls, "one batched call has to be made, not one per record")
}

// TestFetchByIDsMissingIDIsNotAnError proves no record comes back for an ID that
// is not found, and no error either (ADR 0004).
func TestFetchByIDsMissingIDIsNotAnError(t *testing.T) {
	provider, store := newTestProvider(t)
	store.seedItem(itemID, "SKU-1")

	records, err := provider.FetchByIDs(context.Background(), []string{itemID, unknown}, nil)

	require.NoError(t, err)
	assert.Len(t, records, 1)
}

// TestFetchByIDsEmptyIDList proves an empty ID list returns an empty result
// without ever reaching the store.
func TestFetchByIDsEmptyIDList(t *testing.T) {
	provider, store := newTestProvider(t)

	records, err := provider.FetchByIDs(context.Background(), nil, nil)

	require.NoError(t, err)
	assert.Empty(t, records)
	assert.Zero(t, store.availableCalls)
}

// TestFetchByIDsFieldSelection proves only the requested fields come back and
// that the sellable quantity is NOT COMPUTED AT ALL when it is not requested.
func TestFetchByIDsFieldSelection(t *testing.T) {
	provider, store := newTestProvider(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 4)

	records, err := provider.FetchByIDs(context.Background(), []string{itemID},
		[]string{query.IDField, service.FieldSKU})

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Len(t, records[0], 2)
	assert.Equal(t, "SKU-1", records[0][service.FieldSKU])
	assert.NotContains(t, records[0], service.FieldAvailableQuantity)
	assert.Zero(t, store.availableCalls, "no query may be made for a field nobody asked for")
}

// TestFetchByIDsUnknownFieldIsInvalid proves asking for a field that is not
// offered returns errors.Invalid (ADR 0004).
func TestFetchByIDsUnknownFieldIsInvalid(t *testing.T) {
	provider, store := newTestProvider(t)
	store.seedItem(itemID, "SKU-1")

	_, err := provider.FetchByIDs(context.Background(), []string{itemID}, []string{"fiyat"})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Contains(t, err.Error(), "fiyat")
}

// TestListSKUFilter proves the sku filter is applied in the root listing.
func TestListSKUFilter(t *testing.T) {
	provider, store := newTestProvider(t)
	store.seedItem("invitem_1", "SKU-1")
	store.seedItem("invitem_2", "SKU-2")
	store.seedLevel("invitem_2", locA, 3, 0)

	records, err := provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{service.FieldSKU: "SKU-2"},
	})

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "invitem_2", records[0][query.IDField])
	assert.Equal(t, int64(3), records[0][service.FieldAvailableQuantity])
}

// TestListUnknownFilterIsInvalid proves an unsupported filter is not silently
// ignored; were it ignored, the caller would take an unfiltered list for a
// filtered one.
func TestListUnknownFilterIsInvalid(t *testing.T) {
	provider, _ := newTestProvider(t)

	_, err := provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{"renk": "kirmizi"},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Contains(t, err.Error(), "renk")
}

// TestListFilterTypeIsValidated proves the type of a filter value is checked.
func TestListFilterTypeIsValidated(t *testing.T) {
	provider, _ := newTestProvider(t)

	_, err := provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{service.FieldSKU: 42},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestListPages proves limit/offset are applied in the root listing.
func TestListPages(t *testing.T) {
	provider, store := newTestProvider(t)
	for _, id := range []string{"invitem_1", "invitem_2", "invitem_3"} {
		store.seedItem(id, "SKU-"+id)
	}

	records, err := provider.List(context.Background(), query.ListOptions{Limit: 2})
	require.NoError(t, err)
	assert.Len(t, records, 2)

	records, err = provider.List(context.Background(), query.ListOptions{Limit: 2, Offset: 2})
	require.NoError(t, err)
	assert.Len(t, records, 1)
}

// TestProviderSatisfiesTheQueryContract proves, at run time as well, that the
// provider satisfies the interface the core expects; resolving it by name from
// the container makes exactly this conversion.
func TestProviderSatisfiesTheQueryContract(t *testing.T) {
	provider, _ := newTestProvider(t)

	var asProvider any = provider
	_, ok := asProvider.(query.Provider)

	assert.True(t, ok, "QueryProvider has to satisfy the query.Provider interface")
}

// TestListLimitIsClippedToTheCeiling proves a limit coming from the core is
// clipped to the provider's page ceiling.
//
// In the core contract Limit=0 means "unbounded". This provider offers no
// unbounded listing; had it brought an unbounded request down to the default
// page size, the caller would get INCOMPLETE data without an error and believe
// it had all of it. A limit above the ceiling is not refused either: returning
// an error on the core path would mean returning no data at all because of a
// single number.
func TestListLimitIsClippedToTheCeiling(t *testing.T) {
	provider, store := newTestProvider(t)
	// The fixture is above both MaxLimit (100) and DefaultLimit (50); only a set
	// like this shows the difference between the two.
	const itemCount = 120
	for i := range itemCount {
		id := fmt.Sprintf("invitem_%03d", i)
		store.seedItem(id, "SKU-"+id)
	}
	ceiling := int(service.MaxLimit)

	cases := []struct {
		name     string
		limit    int
		expected int
	}{
		{"an unbounded request goes up to the ceiling", 0, ceiling},
		{"a limit above the ceiling is clipped", ceiling + 1, ceiling},
		{"the ceiling itself is valid", ceiling, ceiling},
		{"below the ceiling it is applied as is", 7, 7},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			records, err := provider.List(context.Background(), query.ListOptions{Limit: tc.limit})

			require.NoError(t, err, "the provider has to treat the limit as a reason to clip, not as an error")
			assert.Len(t, records, tc.expected)
		})
	}
}
