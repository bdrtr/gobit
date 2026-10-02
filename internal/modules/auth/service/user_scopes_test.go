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

// revisingRepo writes a user's scopes as scripted over the shared fake,
// recording what it was asked to write.
type revisingRepo struct {
	*fakeRepo
	written    bool
	missing    bool
	read, next []string
}

func (r *revisingRepo) GetUser(ctx context.Context, id string) (models.User, error) {
	if r.missing {
		return models.User{}, errors.NotFound("auth_user_not_found", "user not found: %s", id)
	}
	return r.fakeRepo.GetUser(ctx, id)
}

func (r *revisingRepo) ReviseUserScopes(
	_ context.Context, id string, read, next []string, _ time.Time,
) (models.User, bool, error) {
	r.read, r.next = read, next
	if !r.written {
		return models.User{}, false, nil
	}
	return models.User{ID: id, Scopes: next}, true, nil
}

// TestAUsersPrivilegesAreChangedFromWhatWasRead is ADR 0347: the read and the
// new privileges reach the store trimmed and without repeats; a privilege the
// caller does not hold is not granted; and nothing written is a refusal
// naming that they were changed since, the user being there.
func TestAUsersPrivilegesAreChangedFromWhatWasRead(t *testing.T) {
	t.Parallel()

	repo := &revisingRepo{fakeRepo: &fakeRepo{}, written: true}
	svc := service.New(repo, service.Options{JWTSecret: "test-signing-secret-long-enough"})
	admin := scopedCtx(models.ScopeAdmin)

	user, err := svc.ReviseUserScopes(admin, "user_1", []string{" order:read ", "order:read"},
		[]string{"order:read", " order:write ", "order:write"})
	require.NoError(t, err)
	assert.Equal(t, []string{"order:read", "order:write"}, user.Scopes)
	assert.Equal(t, []string{"order:read"}, repo.read)
	assert.Equal(t, []string{"order:read", "order:write"}, repo.next)

	repo.next = nil
	_, err = svc.ReviseUserScopes(scopedCtx(narrowScope), "user_1", nil, []string{models.ScopeAdmin})
	requireEscalationError(t, err)
	assert.Nil(t, repo.next, "a privilege the caller does not hold never reaches the store")
	_, err = svc.ReviseUserScopes(admin, "user_1", nil, []string{" "})
	assert.True(t, errors.IsInvalid(err), "an empty privilege: %v", err)
	_, err = svc.ReviseUserScopes(admin, "apikey_1", nil, nil)
	assert.True(t, errors.IsInvalid(err), "an id that is not a user's: %v", err)

	repo.written = false
	_, err = svc.ReviseUserScopes(admin, "user_1", []string{"order:read"}, []string{})
	require.Error(t, err)
	assert.Equal(t, service.CodeUserScopesRevised, errors.CodeOf(err), "%v", err)
	assert.Equal(t, []string{}, repo.next, "no privilege at all is a set, not nothing")
	repo.missing = true
	_, err = svc.ReviseUserScopes(admin, "user_1", []string{"order:read"}, []string{})
	assert.True(t, errors.IsNotFound(err), "a user who is not there: %v", err)
}

// TestThePanelReadsAUser is ADR 0347: the surface returns the user as the
// Users screen lists them, saying whether they have proven an authenticator.
func TestThePanelReadsAUser(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{userEmail: "ada@example.test", mfa: map[string]models.MFACredential{}}
	confirmed := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	repo.mfa["user_1"] = models.MFACredential{UserID: "user_1", ConfirmedAt: &confirmed}
	surface := service.NewAccountSurface(service.New(repo, service.Options{JWTSecret: "test-signing-secret-long-enough"}), "gobit")

	raw, err := surface.UserJSON(context.Background(), "user_1")
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"user_1","email":"ada@example.test","first_name":"","last_name":"","scopes":[],
		"second_factor":true,"created_at":"0001-01-01T00:00:00Z"}`, string(raw))
	raw, err = surface.UserJSON(context.Background(), "user_2")
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"second_factor":false`)
}
