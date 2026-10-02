//go:build integration

package identitysession_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identitysession "github.com/bdrtr/gobit/contrib/identity-session"
	"github.com/bdrtr/gobit/core/personaldata"
)

// moves is the real store's pending address change capability.
func moves(t *testing.T, m *identitysession.Module) identitysession.PendingAddresses {
	t.Helper()

	store, ok := m.Credentials().(identitysession.PendingAddresses)
	require.True(t, ok, "the module's own store keeps pending address changes (ADR 0377)")

	return store
}

// TestAPendingAddressChangeIsSingleUseAndReplaced is ADR 0377 against a real
// PostgreSQL: asking again replaces the pending change, a token is taken once
// and answers its customer and folded address, an expired one is not taken,
// and a customer with no credential cannot have one.
func TestAPendingAddressChangeIsSingleUseAndReplaced(t *testing.T) {
	m := registered(t)
	store := moves(t, m)
	ctx := t.Context()
	customer := "cust_06G8MOVESINGLEUSE00000"
	require.NoError(t, m.Credentials().Put(ctx, customer, "move@example.test", testHash))
	later := time.Now().UTC().Add(time.Hour)

	require.NoError(t, store.PutAddressChange(ctx, "move-first", customer, "first@example.test", later))
	require.NoError(t, store.PutAddressChange(ctx, "move-second", customer, " Second@Example.test ", later))
	_, _, err := store.TakeAddressChange(ctx, "move-first")
	require.ErrorIs(t, err, identitysession.ErrNoAddressChange, "asking again replaced the first")

	taken, email, err := store.TakeAddressChange(ctx, "move-second")
	require.NoError(t, err)
	assert.Equal(t, customer, taken)
	assert.Equal(t, "second@example.test", email, "the address is kept folded")
	_, _, err = store.TakeAddressChange(ctx, "move-second")
	require.ErrorIs(t, err, identitysession.ErrNoAddressChange, "a token is taken once")

	_, err = testPool.Exec(ctx,
		`INSERT INTO customer_address_changes (token_hash, customer_id, email, expires_at, created_at)
		 VALUES ('move-expired', $1, 'late@example.test', now() - interval '1 minute', now() - interval '1 hour')`,
		customer)
	require.NoError(t, err)
	_, _, err = store.TakeAddressChange(ctx, "move-expired")
	require.ErrorIs(t, err, identitysession.ErrNoAddressChange, "an expired token is not taken")

	require.Error(t, store.PutAddressChange(ctx, "move-orphan", "cust_06G8NOCREDENTIAL000000", "x@example.test", later),
		"a customer with no credential has no account to move")
}

// TestAnErasureTakesTheAddressChangesToTheAddress: the person's own pending
// change goes with their credential, and somebody else's change that would
// move an account to the person's address goes by the address; a dossier
// shows both before.
func TestAnErasureTakesTheAddressChangesToTheAddress(t *testing.T) {
	m := registered(t)
	store := moves(t, m)
	ctx := t.Context()
	later := time.Now().UTC().Add(time.Hour)
	person, other := "cust_06G8MOVEPERSON0000000", "cust_06G8MOVEOTHER00000000"
	require.NoError(t, m.Credentials().Put(ctx, person, "person@example.test", testHash))
	require.NoError(t, m.Credentials().Put(ctx, other, "other@example.test", testHash))
	require.NoError(t, store.PutAddressChange(ctx, "move-own", person, "person.new@example.test", later))
	require.NoError(t, store.PutAddressChange(ctx, "move-to-person", other, "person@example.test", later))

	dossier, err := m.PersonalDataOf(ctx, personaldata.Subject{CustomerID: person, Email: "person@example.test"})
	require.NoError(t, err)
	changes := 0
	for _, record := range dossier.Records {
		for _, field := range record.Fields {
			assert.NotContains(t, []any{"move-own", "move-to-person"}, field.Value, "a token's hash is never reproduced")
		}
		if record.Table == "customer_address_changes" {
			changes++
		}
	}
	assert.Equal(t, 2, changes, "the person's own change and the one to their address")

	_, err = m.Erase(ctx, personaldata.Subject{CustomerID: person, Email: "person@example.test"})
	require.NoError(t, err)
	var left int
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT count(*) FROM customer_address_changes WHERE token_hash IN ('move-own', 'move-to-person')`,
	).Scan(&left))
	assert.Zero(t, left)
	_, _, err = m.Credentials().Credential(ctx, "other@example.test")
	require.NoError(t, err, "the other account is untouched")
}
