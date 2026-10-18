//go:build integration

package repository_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository"
)

// TestAPasswordSetElsewhereSpendsTheInvitation is ADR 0453 on PostgreSQL: a
// password written for the user removes their pending invitation in the same
// transaction, so the mailed link no longer sets a password; withdrawing one
// answers it, and a user holding none is the sentinel.
func TestAPasswordSetElsewhereSpendsTheInvitation(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	user := newUser(ctx, t, repo)
	deadline := time.Now().UTC().Add(time.Hour)

	mailed := tokenHashFor(user.ID, "mailed")
	_, err := repo.PutInvitation(ctx, mailed, user.ID, user.ID, deadline)
	require.NoError(t, err)
	_, err = repo.SetPasswordHash(ctx, user.ID, models.ProviderEmailPass, "$2a$04$hash", time.Now().UTC())
	require.NoError(t, err)
	_, err = repo.TakeInvitation(ctx, mailed)
	require.ErrorIs(t, err, repository.ErrNoInvitation, "the mailed link is spent by the password")

	resent := tokenHashFor(user.ID, "resent")
	_, err = repo.PutInvitation(ctx, resent, user.ID, user.ID, deadline)
	require.NoError(t, err)
	withdrawn, err := repo.WithdrawInvitation(ctx, user.ID)
	require.NoError(t, err)
	assert.Equal(t, resent, withdrawn.TokenHash)
	_, err = repo.TakeInvitation(ctx, resent)
	require.ErrorIs(t, err, repository.ErrNoInvitation, "a withdrawn link is not accepted")
	_, err = repo.WithdrawInvitation(ctx, user.ID)
	require.ErrorIs(t, err, repository.ErrNoInvitation, "nothing left to withdraw")

	other := newUser(ctx, t, repo)
	kept := tokenHashFor(other.ID, "kept")
	_, err = repo.PutInvitation(ctx, kept, other.ID, user.ID, deadline)
	require.NoError(t, err)
	_, err = repo.SetPasswordHash(ctx, user.ID, models.ProviderEmailPass, "$2a$04$again", time.Now().UTC())
	require.NoError(t, err)
	_, err = repo.TakeInvitation(ctx, kept)
	require.NoError(t, err, "another user's invitation is untouched")
}

// TestAKeysScopesAreWrittenOnlyAsTheyWereRead is ADR 0455 on PostgreSQL: a
// live secret key's scopes are written while they are, as a set, the ones
// read; another set, a publishable key and a revoked key write nothing.
func TestAKeysScopesAreWrittenOnlyAsTheyWereRead(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)

	record := newKeyRecord(t)
	record.Type, record.Scopes = models.APIKeySecret, []string{"order:read", "product:read"}
	secret, err := repo.CreateAPIKey(ctx, record)
	require.NoError(t, err)

	revised, written, err := repo.ReviseAPIKeyScopes(ctx, secret.ID,
		[]string{"product:read", "order:read", "order:read"}, []string{"order:read", "order:write"}, time.Now())
	require.NoError(t, err)
	require.True(t, written, "the set read, in another order")
	assert.Equal(t, []string{"order:read", "order:write"}, revised.Scopes)
	stored, err := repo.GetAPIKey(ctx, secret.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"order:read", "order:write"}, stored.Scopes)

	_, written, err = repo.ReviseAPIKeyScopes(ctx, secret.ID, []string{"order:read"}, []string{"admin"}, time.Now())
	require.NoError(t, err)
	assert.False(t, written, "a set that differs by one")

	publishable, err := repo.CreateAPIKey(ctx, newKeyRecord(t))
	require.NoError(t, err)
	_, written, err = repo.ReviseAPIKeyScopes(ctx, publishable.ID, nil, []string{"order:read"}, time.Now())
	require.NoError(t, err)
	assert.False(t, written, "a publishable key holds no privilege")

	_, err = repo.RevokeAPIKey(ctx, secret.ID, "user_x", time.Now())
	require.NoError(t, err)
	_, written, err = repo.ReviseAPIKeyScopes(ctx, secret.ID, stored.Scopes, []string{"order:read"}, time.Now())
	require.NoError(t, err)
	assert.False(t, written, "a revoked key")
}

// tokenHashFor is an invitation digest no other test writes.
func tokenHashFor(userID, label string) string {
	sum := sha256.Sum256([]byte(userID + "/" + label))

	return hex.EncodeToString(sum[:])
}
