package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// The tests here are about who may know a colleague's first password.
//
// Before this flow the answer was "the administrator who created them", because
// CreateUser takes the password. That is the defect, and every rule below is one
// consequence of not wanting it back (ADR 0137).

const (
	testInviteUser    = "user_01INVITEDCOLLEAGUE00"
	testInviter       = "user_01INVITINGADMIN0000"
	testFirstPassword = "a first password long enough"
)

// TestTheTokenGoesToTHEADDRESSAndNowhereElse is the rule the flow exists for.
func TestTheTokenGoesToTHEADDRESSAndNowhereElse(t *testing.T) {
	svc, sender := invitingService(t)

	require.NoError(t, svc.InviteUser(context.Background(), testInviteUser, testInviter))

	require.Len(t, sender.sent, 1, "one invitation, one message")
	assert.NotEmpty(t, sender.sent[0].token, "the message carries a token")
	assert.Equal(t, 64, len(sender.sent[0].token),
		"thirty-two bytes of randomness, hex — whoever guesses it becomes an administrator")
}

// TestAnInvitationCanBeSpentONCE is what makes a link safe to leave in a mailbox.
func TestAnInvitationCanBeSpentONCE(t *testing.T) {
	svc, sender := invitingService(t)
	require.NoError(t, svc.InviteUser(context.Background(), testInviteUser, testInviter))
	token := sender.sent[0].token

	require.NoError(t, svc.AcceptInvitation(context.Background(), token, testFirstPassword))

	err := svc.AcceptInvitation(context.Background(), token, "another password entirely")
	require.Error(t, err)
	assert.Contains(t, err.Error(), service.CodeInvitationNotUsable,
		"a spent link must not be able to reset a password somebody has since changed")
}

// TestInvitingAgainREPLACESTheLink keeps one account to one live link.
func TestInvitingAgainREPLACESTheLink(t *testing.T) {
	svc, sender := invitingService(t)
	ctx := context.Background()

	require.NoError(t, svc.InviteUser(ctx, testInviteUser, testInviter))
	require.NoError(t, svc.InviteUser(ctx, testInviteUser, testInviter))
	require.Len(t, sender.sent, 2)

	first, second := sender.sent[0].token, sender.sent[1].token
	require.NotEqual(t, first, second, "each invitation mints its own token")

	require.Error(t, svc.AcceptInvitation(ctx, first, testFirstPassword),
		"the FIRST link stopped working when the second was sent; two live links to one "+
			"account is two chances for whoever finds one")
	require.NoError(t, svc.AcceptInvitation(ctx, second, testFirstPassword))
}

// TestOneAnswerForEveryTokenThatIsNotUsable keeps the three kinds of no apart from
// the caller.
func TestOneAnswerForEveryTokenThatIsNotUsable(t *testing.T) {
	svc, sender := invitingService(t)
	ctx := context.Background()

	unknown := svc.AcceptInvitation(ctx, "a token nobody ever minted", testFirstPassword)
	require.Error(t, unknown)

	expiredSvc, expiredSender := invitingService(t)
	expiredSender.repo.invites.expired = true
	require.NoError(t, expiredSvc.InviteUser(ctx, testInviteUser, testInviter))
	expired := expiredSvc.AcceptInvitation(ctx, expiredSender.sent[0].token, testFirstPassword)
	require.Error(t, expired)

	assert.Equal(t, unknown.Error(), expired.Error(),
		"an expired token and one that never existed are the same answer; telling them "+
			"apart says, for any token somebody tries, whether it was ever real")
	_ = sender
}

// TestAnInvitationNOBODYCanCarryIsNotOpened is the fail-closed direction.
//
// The row is written before the message is asked for, so an unbound sender would
// leave an invitation that exists and that its owner will never hear of. Refusing
// to open one at all is the honest answer.
func TestAnInvitationNOBODYCanCarryIsNotOpened(t *testing.T) {
	svc, _ := newService(t)

	err := svc.InviteUser(context.Background(), testInviteUser, testInviter)

	require.Error(t, err)
	assert.Contains(t, err.Error(), service.CodeInvitationNotSendable)
	assert.Equal(t, coreerrors.KindInternal, coreerrors.KindOf(err),
		"nothing bound to carry an invitation is a composition fault rather than a "+
			"caller's mistake")
}

// TestAFailedSENDDoesNotLeaveASilentRow holds the other half of the same care.
func TestAFailedSENDDoesNotLeaveASilentRow(t *testing.T) {
	svc, sender := invitingService(t)
	sender.err = errors.New("the notification module is unreachable")

	err := svc.InviteUser(context.Background(), testInviteUser, testInviter)

	require.Error(t, err, "an invitation nobody receives is a row and not an invitation")
	assert.Contains(t, err.Error(), service.CodeInvitationNotSendable)
}

// TestTheFIRSTPasswordObeysThePolicy proves acceptance is not a way around it.
func TestTheFIRSTPasswordObeysThePolicy(t *testing.T) {
	svc, sender := invitingService(t)
	ctx := context.Background()
	require.NoError(t, svc.InviteUser(ctx, testInviteUser, testInviter))

	err := svc.AcceptInvitation(ctx, sender.sent[0].token, "short")

	require.Error(t, err)
	assert.NotContains(t, err.Error(), service.CodeInvitationNotUsable,
		"a password that does not meet the policy is refused as a PASSWORD; folding it "+
			"into the token's answer would tell somebody their link is broken when it "+
			"is not")
}

// TestARefusedPasswordDoesNotSPENDTheInvitation is the order the checks run in.
//
// The policy is checked BEFORE the token is taken, so a colleague who typed
// something too short still holds a working link. The reverse order would spend the
// invitation on a request that wrote nothing.
func TestARefusedPasswordDoesNotSPENDTheInvitation(t *testing.T) {
	svc, sender := invitingService(t)
	ctx := context.Background()
	require.NoError(t, svc.InviteUser(ctx, testInviteUser, testInviter))
	token := sender.sent[0].token

	require.Error(t, svc.AcceptInvitation(ctx, token, "short"))

	require.NoError(t, svc.AcceptInvitation(ctx, token, testFirstPassword),
		"the link still works: a typo must not cost somebody their invitation")
}

// TestTheInviterIsRECORDED keeps the trail an operator reads afterwards.
func TestTheInviterIsRECORDED(t *testing.T) {
	svc, sender := invitingService(t)

	require.NoError(t, svc.InviteUser(context.Background(), testInviteUser, testInviter))

	for _, row := range sender.repo.invites.byHash {
		assert.Equal(t, testInviter, row.InvitedBy)
		assert.Equal(t, testInviteUser, row.UserID)
	}
}

// TestAnInvitationNamesARealUser refuses an id nobody has.
func TestAnInvitationNamesARealUser(t *testing.T) {
	svc, _ := invitingService(t)

	require.Error(t, svc.InviteUser(context.Background(), "not-a-user-id", testInviter),
		"the identifier has to look like one before a row is written")
}

// recordingSender is the seam, with what it was asked to carry.
type recordingSender struct {
	repo *fakeRepo
	sent []sentInvitation
	err  error
}

// sentInvitation is one message the sender was asked to carry.
type sentInvitation struct {
	email     string
	token     string
	reference string
}

// SendInvitation records the message.
func (s *recordingSender) SendInvitation(_ context.Context, email, token, reference string) error {
	if s.err != nil {
		return s.err
	}
	s.sent = append(s.sent, sentInvitation{email: email, token: token, reference: reference})

	return nil
}

// invitingService is a service with the invitation seam bound.
func invitingService(t *testing.T) (*service.Service, *recordingSender) {
	t.Helper()

	repo := &fakeRepo{}
	sender := &recordingSender{repo: repo}
	svc := service.New(repo, service.Options{
		Now:              func() time.Time { return time.Now().UTC() },
		JWTSecret:        "test-signing-secret-long-enough",
		InvitationSender: sender,
	})

	return svc, sender
}
