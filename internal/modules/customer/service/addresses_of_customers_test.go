package service

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// AddressesOfCustomers answers the customers' living addresses in one call,
// which it counts, in the order they were written, as the real read does (ADR
// 0304, ADR 0308).
func (m *memRepo) AddressesOfCustomers(_ context.Context, customerIDs []string) ([]models.CustomerAddress, error) {
	m.record("AddressesOfCustomers")

	wanted := map[string]bool{}
	for _, id := range customerIDs {
		wanted[id] = true
	}
	out := []models.CustomerAddress{}
	for id := range m.addresses {
		if a := m.addresses[id]; wanted[a.CustomerID] && a.DeletedAt == nil {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(x, y models.CustomerAddress) int {
		if c := cmpString(x.CustomerID, y.CustomerID); c != 0 {
			return c
		}
		if c := x.CreatedAt.Compare(y.CreatedAt); c != 0 {
			return c
		}

		return cmpString(x.ID, y.ID)
	})

	return out, nil
}

// TestTheDefaultShippingAddressIsReadWhenAskedFor is ADR 0304's field: a
// customer's default shipping address under the order's address keys, nil
// for one with none, one read for the page, and none when not asked for.
func TestTheDefaultShippingAddressIsReadWhenAskedFor(t *testing.T) {
	ctx := context.Background()
	repo := newMemRepo()
	svc := New(repo, Options{})
	provider := NewQueryProvider(svc)

	housed, err := svc.CreateCustomer(ctx, CustomerInput{Email: "housed@example.test"})
	require.NoError(t, err)
	_, err = svc.CreateAddress(ctx, housed.ID, AddressInput{
		FirstName: "Ada", Company: "Engines Ltd", Address1: "12 Right St", City: "Ankara",
		PostalCode: "06000", CountryCode: "TR", Phone: "+90", IsDefaultShipping: true,
	})
	require.NoError(t, err)
	_, err = svc.CreateAddress(ctx, housed.ID, AddressInput{Address1: "Not the default", City: "Izmir", CountryCode: "TR"})
	require.NoError(t, err)
	homeless, err := svc.CreateCustomer(ctx, CustomerInput{Email: "homeless@example.test"})
	require.NoError(t, err)

	records, err := provider.FetchByIDs(ctx, []string{housed.ID, homeless.ID},
		[]string{fieldID, fieldDefaultShippingAddress})
	require.NoError(t, err)
	require.Len(t, records, 2)
	byID := map[any]query.Record{}
	for _, record := range records {
		byID[record[fieldID]] = record
	}
	assert.Equal(t, map[string]any{
		"first_name": "Ada", "last_name": "", "company": "Engines Ltd", "address_1": "12 Right St",
		"address_2": "", "city": "Ankara", "postal_code": "06000", "country_code": "TR", "phone": "+90",
	}, byID[housed.ID][fieldDefaultShippingAddress])
	assert.Nil(t, byID[homeless.ID][fieldDefaultShippingAddress])
	assert.Equal(t, 1, repo.calls["AddressesOfCustomers"], "two customers, one read")

	_, err = provider.FetchByIDs(ctx, []string{housed.ID}, []string{fieldID, fieldEmail})
	require.NoError(t, err)
	assert.Equal(t, 1, repo.calls["AddressesOfCustomers"], "not asked for, not read")
}

// TestTheAddressesAreListedWhenAskedFor is ADR 0308's field: every living
// address of a customer in the order it was written, with its id and its
// default flags; an empty list for a customer with none; and the default and
// the list from one read when both are asked for.
func TestTheAddressesAreListedWhenAskedFor(t *testing.T) {
	ctx := context.Background()
	repo := newMemRepo()
	svc := New(repo, Options{})
	provider := NewQueryProvider(svc)

	housed, err := svc.CreateCustomer(ctx, CustomerInput{Email: "listed@example.test"})
	require.NoError(t, err)
	home, err := svc.CreateAddress(ctx, housed.ID, AddressInput{
		Address1: "Home St 1", City: "Ankara", CountryCode: "TR", IsDefaultShipping: true,
	})
	require.NoError(t, err)
	office, err := svc.CreateAddress(ctx, housed.ID, AddressInput{
		Company: "Engines Ltd", Address1: "Office St 2", City: "Izmir", CountryCode: "TR", IsDefaultBilling: true,
	})
	require.NoError(t, err)
	gone, err := svc.CreateAddress(ctx, housed.ID, AddressInput{Address1: "Old St 3", City: "Bursa", CountryCode: "TR"})
	require.NoError(t, err)
	require.NoError(t, svc.DeleteAddress(ctx, housed.ID, gone.ID))
	homeless, err := svc.CreateCustomer(ctx, CustomerInput{Email: "unlisted@example.test"})
	require.NoError(t, err)

	records, err := provider.FetchByIDs(ctx, []string{housed.ID, homeless.ID},
		[]string{fieldID, fieldAddresses, fieldDefaultShippingAddress})
	require.NoError(t, err)
	byID := map[any]query.Record{}
	for _, record := range records {
		byID[record[fieldID]] = record
	}

	list, ok := byID[housed.ID][fieldAddresses].([]map[string]any)
	require.True(t, ok)
	require.Len(t, list, 2, "a deleted address is not listed")
	assert.Equal(t, home.ID, list[0]["id"])
	assert.Equal(t, "Home St 1", list[0]["address_1"])
	assert.Equal(t, true, list[0]["is_default_shipping"])
	assert.Equal(t, false, list[0]["is_default_billing"])
	assert.Equal(t, office.ID, list[1]["id"])
	assert.Equal(t, "Engines Ltd", list[1]["company"])
	assert.Equal(t, true, list[1]["is_default_billing"])
	empty, ok := byID[homeless.ID][fieldAddresses].([]map[string]any)
	require.True(t, ok)
	assert.NotNil(t, empty)
	assert.Empty(t, empty)
	assert.Equal(t, 1, repo.calls["AddressesOfCustomers"], "both fields from one read")
}
