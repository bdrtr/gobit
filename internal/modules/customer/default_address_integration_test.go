//go:build integration

package customer_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// TestTheDefaultShippingAddressFieldOnTheRealQuery is ADR 0304's read against
// a real PostgreSQL: a page of customers gets each one's default shipping
// address, not another of their addresses and not a deleted default, and nil
// for a customer with none.
func TestTheDefaultShippingAddressFieldOnTheRealQuery(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	// The default is written first and another address after it, so a read
	// that took any address would answer the later one.
	housed := newAccount(ctx, t, svc)
	home := validAddress()
	home.Address1 = "Default Street 7"
	home.IsDefaultShipping = true
	_, err := svc.CreateAddress(ctx, housed.ID, home)
	require.NoError(t, err)
	other := validAddress()
	other.Address1 = "Not The Default 1"
	_, err = svc.CreateAddress(ctx, housed.ID, other)
	require.NoError(t, err)

	moved := newAccount(ctx, t, svc)
	gone := validAddress()
	gone.IsDefaultShipping = true
	old, err := svc.CreateAddress(ctx, moved.ID, gone)
	require.NoError(t, err)
	require.NoError(t, svc.DeleteAddress(ctx, moved.ID, old.ID))

	homeless := newAccount(ctx, t, svc)

	records, err := service.NewQueryProvider(svc).FetchByIDs(ctx, []string{housed.ID, moved.ID, homeless.ID},
		[]string{query.IDField, "default_shipping_address"})
	require.NoError(t, err)
	byID := map[any]query.Record{}
	for _, record := range records {
		byID[record[query.IDField]] = record
	}
	require.Len(t, byID, 3)

	address, ok := byID[housed.ID]["default_shipping_address"].(map[string]any)
	require.True(t, ok, "the default shipping address is read")
	assert.Equal(t, "Default Street 7", address["address_1"])
	assert.Nil(t, byID[moved.ID]["default_shipping_address"], "a deleted default is no default")
	assert.Nil(t, byID[homeless.ID]["default_shipping_address"])
}
