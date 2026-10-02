package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// identityRepo records the login identity a user is opened with over the
// shared fake.
type identityRepo struct {
	*fakeRepo
	opened   int
	identity *models.AuthIdentity
}

func (r *identityRepo) CreateUser(
	ctx context.Context, u models.User, identity *models.AuthIdentity,
) (models.User, error) {
	r.opened++
	r.identity = identity
	return r.fakeRepo.CreateUser(ctx, u, identity)
}

// TestThePanelOpensAUserWithoutAPassword is ADR 0348: the user the panel
// invites is opened with no login identity, so nobody can sign in as them
// until they accept, the operator included.
func TestThePanelOpensAUserWithoutAPassword(t *testing.T) {
	t.Parallel()

	repo := &identityRepo{fakeRepo: &fakeRepo{}}
	svc := service.New(repo, service.Options{
		JWTSecret: "test-signing-secret-long-enough", InvitationSender: &recordingSender{repo: repo.fakeRepo},
	})
	_, err := service.NewAccountSurface(svc, "gobit").InviteUser(
		scopedCtx(models.ScopeAdmin), "user_inviter", "new@example.test", "", "", nil)
	require.NoError(t, err)
	assert.Equal(t, 1, repo.opened)
	assert.Nil(t, repo.identity, "no password, so no identity")
}

// TestThePanelInvitesAUser is ADR 0348: the surface opens a user without a
// password, with the privileges given and none when none is given, never an
// administrator by omission, and sends them an invitation; an invitation
// that cannot be sent still names the user opened; a privilege the operator
// lacks opens nobody; and an invitation is sent again on its own.
func TestThePanelInvitesAUser(t *testing.T) {
	t.Parallel()

	svc, sender := invitingService(t)
	surface := service.NewAccountSurface(svc, "gobit")
	admin := scopedCtx(models.ScopeAdmin)

	id, err := surface.InviteUser(admin, "user_inviter", "New@Example.test", "Ada", "Lovelace", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, id)
	opened := sender.repo.lastUser
	assert.Equal(t, id, opened.ID)
	assert.Equal(t, "new@example.test|Ada|Lovelace", opened.Email+"|"+opened.FirstName+"|"+opened.LastName)
	assert.Equal(t, []string{}, opened.Scopes, "no privilege given is none, not admin")
	require.Len(t, sender.sent, 1, "an invitation is sent")

	_, err = surface.InviteUser(admin, "user_inviter", "clerk@example.test", "", "", []string{"order:read"})
	require.NoError(t, err)
	assert.Equal(t, []string{"order:read"}, sender.repo.lastUser.Scopes)

	writes := sender.repo.writeCount
	_, err = surface.InviteUser(scopedCtx(narrowScope), "user_inviter", "boss@example.test", "", "",
		[]string{models.ScopeAdmin})
	requireEscalationError(t, err)
	assert.Equal(t, writes, sender.repo.writeCount, "nobody is opened")

	sender.err = errors.Unavailable("notification_down", "no answer")
	id, err = surface.InviteUser(admin, "user_inviter", "late@example.test", "", "", nil)
	require.Error(t, err)
	assert.NotEmpty(t, id, "the user opened is named although the invitation was not sent")

	sender.err = nil
	sent := len(sender.sent)
	require.NoError(t, surface.ResendInvitation(admin, id, "user_inviter"))
	assert.Len(t, sender.sent, sent+1, "an invitation is sent again")
	err = surface.ResendInvitation(admin, id, "apikey_1")
	assert.True(t, errors.IsInvalid(err), "an inviter who is not a user: %v", err)
}

// TestThePanelRemovesAUser is ADR 0349: the surface deletes the user through
// the service, which refuses an id that is not a user's.
func TestThePanelRemovesAUser(t *testing.T) {
	t.Parallel()

	svc, repo := newService(t)
	surface := service.NewAccountSurface(svc, "gobit")

	require.NoError(t, surface.RemoveUser(context.Background(), "user_1"))
	assert.Equal(t, 1, repo.writeCount, "the delete reached the store")
	err := surface.RemoveUser(context.Background(), "apikey_1")
	assert.True(t, errors.IsInvalid(err), "%v", err)
	assert.Equal(t, 1, repo.writeCount)
}
