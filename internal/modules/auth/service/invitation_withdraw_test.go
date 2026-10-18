package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// TestAPendingInvitationIsWithdrawn is ADR 0453: withdrawing removes the
// user's row so its token is no longer accepted, and a user holding none, or
// holding only an expired one, is refused as not pending.
func TestAPendingInvitationIsWithdrawn(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{}
	svc := service.New(repo, service.Options{JWTSecret: "test-signing-secret-long-enough"})
	ctx := context.Background()

	_, err := repo.PutInvitation(ctx, "hash-1", "user_1", "user_admin", time.Now().UTC().Add(time.Hour))
	require.NoError(t, err)
	require.NoError(t, svc.WithdrawInvitation(ctx, "user_1", "user_admin"))
	_, err = repo.TakeInvitation(ctx, "hash-1")
	require.Error(t, err, "a withdrawn invitation's token is not accepted")

	err = svc.WithdrawInvitation(ctx, "user_1", "user_admin")
	assert.Equal(t, service.CodeInvitationNotPending, errors.CodeOf(err), "%v", err)
	assert.True(t, errors.IsNotFound(err))

	repo.invites.expired = true
	_, err = repo.PutInvitation(ctx, "hash-2", "user_2", "user_admin", time.Now().UTC().Add(time.Hour))
	require.NoError(t, err)
	err = svc.WithdrawInvitation(ctx, "user_2", "user_admin")
	assert.Equal(t, service.CodeInvitationNotPending, errors.CodeOf(err), "an expired invitation is not pending: %v", err)
	assert.Empty(t, repo.invites.byHash, "the expired row is removed all the same")

	err = svc.WithdrawInvitation(ctx, "apikey_1", "user_admin")
	assert.True(t, errors.IsInvalid(err), "an id that is not a user's: %v", err)
}

// keyRevisingRepo answers one key and writes its scopes as scripted.
type keyRevisingRepo struct {
	*fakeRepo
	key        models.APIKey
	written    bool
	read, next []string
	gets       int
}

func (r *keyRevisingRepo) GetAPIKey(_ context.Context, id string) (models.APIKey, error) {
	r.gets++
	if r.key.ID != id {
		return models.APIKey{}, errors.NotFound("auth_api_key_not_found", "api key not found: %s", id)
	}
	return r.key, nil
}

func (r *keyRevisingRepo) ReviseAPIKeyScopes(
	_ context.Context, id string, read, next []string, _ time.Time,
) (models.APIKey, bool, error) {
	r.read, r.next = read, next
	if !r.written {
		return models.APIKey{}, false, nil
	}
	return models.APIKey{ID: id, Type: models.APIKeySecret, Scopes: next}, true, nil
}

// TestASecretKeysPrivilegesAreRevisedFromWhatWasRead is ADR 0455: the read
// and the new privileges reach the store trimmed and without repeats; a
// privilege the caller lacks, none at all, a publishable key and a revoked
// one are refused before the store; nothing written is a refusal naming that
// the privileges changed since.
func TestASecretKeysPrivilegesAreRevisedFromWhatWasRead(t *testing.T) {
	t.Parallel()

	repo := &keyRevisingRepo{
		fakeRepo: &fakeRepo{}, written: true,
		key: models.APIKey{ID: "apikey_1", Type: models.APIKeySecret, Scopes: []string{"order:read"}},
	}
	svc := service.New(repo, service.Options{JWTSecret: "test-signing-secret-long-enough"})
	admin := scopedCtx(models.ScopeAdmin)

	key, err := svc.ReviseAPIKeyScopes(admin, "apikey_1", []string{" order:read "},
		[]string{"order:read", " product:read ", "product:read"})
	require.NoError(t, err)
	assert.Equal(t, []string{"order:read", "product:read"}, key.Scopes)
	assert.Equal(t, []string{"order:read"}, repo.read)

	repo.next = nil
	_, err = svc.ReviseAPIKeyScopes(scopedCtx(narrowScope), "apikey_1", nil, []string{models.ScopeAdmin})
	requireEscalationError(t, err)
	_, err = svc.ReviseAPIKeyScopes(admin, "apikey_1", []string{"order:read"}, []string{})
	assert.True(t, errors.IsInvalid(err), "a secret key with no privilege: %v", err)
	assert.Nil(t, repo.next, "a refused revision never reaches the store")

	repo.key.Type = models.APIKeyPublishable
	_, err = svc.ReviseAPIKeyScopes(admin, "apikey_1", nil, []string{"order:read"})
	assert.Equal(t, service.CodeAPIKeyTypeMismatch, errors.CodeOf(err), "%v", err)
	repo.key.Type = models.APIKeySecret
	revoked := time.Now()
	repo.key.RevokedAt = &revoked
	_, err = svc.ReviseAPIKeyScopes(admin, "apikey_1", nil, []string{"order:read"})
	assert.Equal(t, service.CodeAPIKeyRevoked, errors.CodeOf(err), "%v", err)
	assert.True(t, errors.IsConflict(err))
	assert.Nil(t, repo.next)

	repo.key.RevokedAt = nil
	repo.written = false
	_, err = svc.ReviseAPIKeyScopes(admin, "apikey_1", []string{"product:read"}, []string{"order:read"})
	assert.Equal(t, service.CodeAPIKeyScopesRevised, errors.CodeOf(err), "%v", err)
	_, err = svc.ReviseAPIKeyScopes(admin, "apikey_2", nil, []string{"order:read"})
	assert.True(t, errors.IsNotFound(err), "a key that is not there: %v", err)
}
