package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// DefaultShippingAddresses answers the customers' default shipping addresses
// in one call, which it counts, as the real read does (ADR 0304).
func (m *memRepo) DefaultShippingAddresses(_ context.Context, customerIDs []string) ([]models.CustomerAddress, error) {
	m.record("DefaultShippingAddresses")

	wanted := map[string]bool{}
	for _, id := range customerIDs {
		wanted[id] = true
	}
	out := []models.CustomerAddress{}
	for id := range m.addresses {
		if a := m.addresses[id]; wanted[a.CustomerID] && a.IsDefaultShipping && a.DeletedAt == nil {
			out = append(out, a)
		}
	}

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
	assert.Equal(t, 1, repo.calls["DefaultShippingAddresses"], "two customers, one read")

	_, err = provider.FetchByIDs(ctx, []string{housed.ID}, []string{fieldID, fieldEmail})
	require.NoError(t, err)
	assert.Equal(t, 1, repo.calls["DefaultShippingAddresses"], "not asked for, not read")
}
