package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository"
)

// The codes an invitation answers with.
const (
	// CodeInvitationNotUsable is a token that is not a usable invitation.
	//
	// ONE code for never existed, already used and expired. Telling them apart
	// would answer, for any token somebody cares to try, whether it was ever real
	// — and the person holding a real one is about to be signed in anyway.
	CodeInvitationNotUsable = "auth_invitation_not_usable"
	// CodeInvitationNotSendable reports that the invitation could not be carried
	// to the person it names.
	CodeInvitationNotSendable = "auth_invitation_not_sendable"
)

// DefaultInvitationTTL is how long an invitation link works for.
//
// Seventy-two hours, and the number is not the storefront's. A shopper who asks to
// register is at the keyboard and uses the link within minutes (ADR 0133 gives them
// one hour); an administrator invites a colleague who may be away for the weekend,
// and a link that expired before they read it turns a one-step flow into a support
// request. Three days is long enough to cross one, and short enough that a message
// sitting unread in a mailbox is not a permanent open door.
const DefaultInvitationTTL = 72 * time.Hour

// invitationTokenBytes is how many random bytes an invitation link carries.
//
// Thirty-two, which is the api_key table's floor one file over and the session
// secret's: whoever guesses it becomes an administrator.
const invitationTokenBytes = 32

// InvitationSender carries an invitation to the person it names.
//
// # Why it is a seam and not a call
//
// This module does not know the notification module (Principle 2.1/2.4), and the
// message cannot be an EVENT: an event is durable in a stream and forwarded to an
// operator's third-party endpoints by a gate that fails in both directions, and the
// token in it is the account (ADR 0137).
//
// The signature carries only primitive types, so the composition root can bind the
// notification module's surface without this package naming it.
type InvitationSender interface {
	// SendInvitation carries a token to an address.
	//
	// The reference is the caller's own record for the message, and it is half the
	// notification module's idempotency key.
	SendInvitation(ctx context.Context, email, token, reference string) error
}

// InviteUser opens an invitation for a user and has it carried to them.
//
// # What it does NOT do
//
// It writes no password and creates no identity. The account it names may have no
// auth_identity row at all — which is exactly the state [Service.CreateUser] leaves
// when it is called without one — and that state is the point rather than a
// problem: until the person accepts, nobody can log in as them, including the
// administrator who invited them.
//
// # Why the token is returned to nobody
//
// It goes to the sender and is never in the response. An invitation handed back
// over the admin API would be an administrator holding a colleague's first-password
// link, which is the thing this flow exists to stop.
func (s *Service) InviteUser(ctx context.Context, userID, invitedBy string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(userID, models.UserIDPrefix, "the user identifier"); err != nil {
		return err
	}
	if err := requireID(invitedBy, models.UserIDPrefix, "the inviting user's identifier"); err != nil {
		return err
	}
	if s.inviteSender == nil {
		return coreerrors.Internal(CodeInvitationNotSendable,
			"nothing is bound to carry an invitation, so one cannot be opened; an "+
				"invitation nobody receives is a row and not an invitation")
	}

	// The user is read FIRST, for the address and for the refusal: an invitation to
	// an id nobody has is a row the foreign key would refuse anyway, and refusing
	// here names the user rather than a constraint.
	user, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return err
	}

	token, err := newInvitationToken()
	if err != nil {
		return err
	}

	invitation, err := s.repo.PutInvitation(ctx,
		hashInvitationToken(token), userID, invitedBy, s.now().Add(DefaultInvitationTTL))
	if err != nil {
		return err
	}

	// Recorded first, then sent. The other order hands somebody a link that cannot
	// work; this order can leave a row nobody uses, and that row expires and is
	// replaced the moment an administrator asks again.
	if err := s.inviteSender.SendInvitation(ctx, user.Email, token, invitation.TokenHash); err != nil {
		s.log.ErrorContext(ctx, "the invitation could not be sent; the row is written",
			"user_id", userID, "error", err)

		return coreerrors.Wrap(err, coreerrors.KindOf(err), CodeInvitationNotSendable,
			"the invitation could not be carried to the address on the account")
	}

	s.log.InfoContext(ctx, "an invitation was sent", "user_id", userID, "invited_by", invitedBy)

	return nil
}

// AcceptInvitation spends an invitation and sets the user's first password.
//
// # The token is consumed BEFORE the password is written
//
// [repository.Repo.TakeInvitation] removes the row and returns it in one statement,
// so the token stops working the instant it is used even if everything after fails.
// The cost is chosen: a failure in the write loses the invitation and an
// administrator sends another. The other order would leave a replayable link, and a
// link that can be replayed is a link that can reset a password somebody has since
// changed.
//
// # Why it does not sign them in
//
// The storefront's registration does (ADR 0133), because proving an address there
// IS the account. Here the person now has a password and an ordinary login to make
// with it, and handing back an admin session from an unauthenticated endpoint would
// be a second way to get one.
func (s *Service) AcceptInvitation(ctx context.Context, token, password string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := validatePassword(password); err != nil {
		return err
	}

	invitation, err := s.repo.TakeInvitation(ctx, hashInvitationToken(token))
	if errors.Is(err, repository.ErrNoInvitation) {
		return coreerrors.Invalid(CodeInvitationNotUsable,
			"that invitation is not usable; it may have been used already or expired, "+
				"so ask an administrator to send another")
	}
	if err != nil {
		return err
	}

	if err := s.SetPassword(ctx, invitation.UserID, password); err != nil {
		return err
	}

	s.log.InfoContext(ctx, "an invitation was accepted", "user_id", invitation.UserID)

	return nil
}

// newInvitationToken mints a token.
func newInvitationToken() (string, error) {
	raw := make([]byte, invitationTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", coreerrors.Internal(CodeInvitationNotSendable,
			"the invitation token could not be read: %v", err)
	}

	return hex.EncodeToString(raw), nil
}

// hashInvitationToken is what the table holds instead of the token.
//
// SHA-256 and not bcrypt, and the difference is the input rather than laziness: a
// password is chosen by a person and has to survive an offline attack on a leaked
// hash, while this is thirty-two bytes from crypto/rand with nothing to guess. What
// the hash buys is that a leaked TABLE is not a list of working links — the same
// reasoning, and the same digest, the api_key table uses.
func hashInvitationToken(token string) string {
	sum := sha256.Sum256([]byte(token))

	return hex.EncodeToString(sum[:])
}
