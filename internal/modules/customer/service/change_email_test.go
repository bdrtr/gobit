package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// TestAnAccountMovesToAProvenAddress is ADR 0377's half on the customer
// side: the address is folded and written, and one another account holds is
// a conflict that writes nothing.
func TestAnAccountMovesToAProvenAddress(t *testing.T) {
	ctx := context.Background()
	svc := New(newMemRepo(), Options{})
	account := func(email string) string {
		t.Helper()
		id, err := svc.RegisterGuestCustomer(ctx, email, "", "", "")
		require.NoError(t, err)
		require.NoError(t, svc.ConvertGuestToAccount(ctx, id))

		return id
	}
	ada := account("ada@example.test")
	account("taken@example.test")

	require.NoError(t, svc.ChangeCustomerEmail(ctx, ada, " Ada.New@Example.test "))
	email, err := svc.CustomerEmail(ctx, ada)
	require.NoError(t, err)
	assert.Equal(t, "ada.new@example.test", email)

	err = svc.ChangeCustomerEmail(ctx, ada, "taken@example.test")
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "another account's address: %v", err)
	email, err = svc.CustomerEmail(ctx, ada)
	require.NoError(t, err)
	assert.Equal(t, "ada.new@example.test", email, "nothing was written")
}
