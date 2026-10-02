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

// resets is the real store's pending-reset capability.
func resets(t *testing.T, m *identitysession.Module) identitysession.PasswordResets {
	t.Helper()

	store, ok := m.Credentials().(identitysession.PasswordResets)
	require.True(t, ok, "the module's own store keeps pending resets (ADR 0373)")

	return store
}

// TestAPendingResetIsSingleUseAndReplaced is ADR 0373 against a real
// PostgreSQL: asking again replaces the pending reset, a token is taken once
// and answers the credential's address, an expired one is not taken, and a
// customer with no credential cannot have one.
func TestAPendingResetIsSingleUseAndReplaced(t *testing.T) {
	m := registered(t)
	store := resets(t, m)
	ctx := t.Context()
	customer := "cust_06G8RESETSINGLEUSE0000"
	require.NoError(t, m.Credentials().Put(ctx, customer, "reset@example.test", testHash))
	later := time.Now().UTC().Add(time.Hour)

	require.NoError(t, store.PutPasswordReset(ctx, "hash-first", customer, later))
	require.NoError(t, store.PutPasswordReset(ctx, "hash-second", customer, later))
	_, _, err := store.TakePasswordReset(ctx, "hash-first")
	require.ErrorIs(t, err, identitysession.ErrNoPasswordReset, "asking again replaced the first")

	taken, email, err := store.TakePasswordReset(ctx, "hash-second")
	require.NoError(t, err)
	assert.Equal(t, customer, taken)
	assert.Equal(t, "reset@example.test", email, "the address comes from the credential")
	_, _, err = store.TakePasswordReset(ctx, "hash-second")
	require.ErrorIs(t, err, identitysession.ErrNoPasswordReset, "a token is taken once")

	_, err = testPool.Exec(ctx,
		`INSERT INTO customer_password_resets (token_hash, customer_id, expires_at, created_at)
		 VALUES ('hash-expired', $1, now() - interval '1 minute', now() - interval '1 hour')`, customer)
	require.NoError(t, err)
	_, _, err = store.TakePasswordReset(ctx, "hash-expired")
	require.ErrorIs(t, err, identitysession.ErrNoPasswordReset, "an expired token is not taken")

	require.Error(t, store.PutPasswordReset(ctx, "hash-orphan", "cust_06G8NOCREDENTIAL000000", later),
		"a customer with no credential has no password to reset")
}

// TestAnErasureTakesThePendingResetWithTheCredential: the foreign key's
// cascade is what the erasure relies on, and the dossier shows a pending reset
// before it.
func TestAnErasureTakesThePendingResetWithTheCredential(t *testing.T) {
	m := registered(t)
	ctx := t.Context()
	customer := "cust_06G8RESETERASED000000"
	require.NoError(t, m.Credentials().Put(ctx, customer, "reset-erased@example.test", testHash))
	require.NoError(t, resets(t, m).PutPasswordReset(ctx, "hash-erased", customer, time.Now().UTC().Add(time.Hour)))

	dossier, err := m.PersonalDataOf(ctx, personaldata.Subject{Email: "reset-erased@example.test"})
	require.NoError(t, err)
	tables := map[string]bool{}
	for _, record := range dossier.Records {
		tables[record.Table] = true
		for _, field := range record.Fields {
			assert.NotEqual(t, "hash-erased", field.Value, "the token's hash is never reproduced")
		}
	}
	assert.True(t, tables["customer_password_resets"], "the pending reset is in the dossier")

	_, err = m.Erase(ctx, personaldata.Subject{CustomerID: customer})
	require.NoError(t, err)
	var left int
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT count(*) FROM customer_password_resets WHERE customer_id = $1`, customer).Scan(&left))
	assert.Zero(t, left, "the reset went with the credential")
}
