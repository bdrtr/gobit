//go:build integration

package customer_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// TestASavedAddressKeepsItsProvince is ADR 0369 against a real PostgreSQL: a
// saved address keeps the province it was written with, read back alone, in
// the customer's list and under the provider's address keys; a patch that
// names it moves it and one that does not leaves it; an address written
// without one holds it empty.
func TestASavedAddressKeepsItsProvince(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	customer := newAccount(ctx, t, svc)

	in := validAddress()
	in.City = "Konak"
	in.Province = "Izmir"
	in.IsDefaultShipping = true
	created, err := svc.CreateAddress(ctx, customer.ID, in)
	require.NoError(t, err)
	assert.Equal(t, "Izmir", created.Province, "the write answers what it wrote")
	read, err := svc.GetAddress(ctx, customer.ID, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "Izmir", read.Province)
	listed, err := svc.ListAddresses(ctx, customer.ID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, "Izmir", listed[0].Province)

	records, err := service.NewQueryProvider(svc).FetchByIDs(ctx, []string{customer.ID},
		[]string{query.IDField, "default_shipping_address", "addresses"})
	require.NoError(t, err)
	require.Len(t, records, 1)
	shipping, ok := records[0]["default_shipping_address"].(map[string]any)
	require.True(t, ok, "the default shipping address is read")
	assert.Equal(t, "Izmir", shipping["province"], "under the order's address key")
	entries, ok := records[0]["addresses"].([]map[string]any)
	require.True(t, ok, "the addresses are read")
	require.Len(t, entries, 1)
	assert.Equal(t, "Izmir", entries[0]["province"])

	moved := "Manisa"
	patched, err := svc.UpdateAddress(ctx, customer.ID, created.ID, service.UpdateAddressInput{Province: &moved})
	require.NoError(t, err)
	assert.Equal(t, "Manisa", patched.Province, "a patch naming the province moves it")
	assert.Equal(t, "Konak", patched.City)
	phone := "+90 555 000 0009"
	patched, err = svc.UpdateAddress(ctx, customer.ID, created.ID, service.UpdateAddressInput{Phone: &phone})
	require.NoError(t, err)
	assert.Equal(t, "Manisa", patched.Province, "a patch that does not name it leaves it")

	bare, err := svc.CreateAddress(ctx, customer.ID, validAddress())
	require.NoError(t, err)
	assert.Empty(t, bare.Province, "an address written without one holds it empty")
}

// TestAProvinceWrittenAfterAnErasureIsErasedToo is the erasure's guard for
// the new column: an anonymized address stays live, a province written onto
// it afterwards makes the row personal again, and a second erasure has to
// write it rather than find the row already anonymous.
func TestAProvinceWrittenAfterAnErasureIsErasedToo(t *testing.T) {
	ctx := context.Background()
	svc := erasureService(t)
	created := personalRecord(ctx, t, svc, true)

	first, err := svc.Erase(ctx, personaldata.Subject{CustomerID: created.ID})
	require.NoError(t, err)
	require.Equal(t, personaldata.Anonymized, first.Outcome)
	addresses, err := svc.ListAddresses(ctx, created.ID)
	require.NoError(t, err)
	require.Len(t, addresses, 1)
	province := "Izmir"
	_, err = svc.UpdateAddress(ctx, created.ID, addresses[0].ID, service.UpdateAddressInput{Province: &province})
	require.NoError(t, err, "an anonymized address stays live and writable, or this test proves nothing")
	require.Equal(t, "Izmir", readAddresses(ctx, t, created.ID)[0].Province)

	second, err := svc.Erase(ctx, personaldata.Subject{CustomerID: created.ID})
	require.NoError(t, err)
	assert.Equal(t, 1, second.Rows, "the address held a province again and had to be written")
	assert.Empty(t, readAddresses(ctx, t, created.ID)[0].Province)
}
