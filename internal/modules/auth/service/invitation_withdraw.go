package service

import (
	"context"
	stderrors "errors"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository"
)

// CodeInvitationNotPending reports that the user holds no invitation that
// could still be accepted, so there is none to withdraw (ADR 0453).
const CodeInvitationNotPending = "auth_invitation_not_pending"

// WithdrawInvitation voids the user's pending invitation, so the link that
// was sent stops working at once (ADR 0453).
//
// An administrator withdraws one sent to the wrong address, or one a first
// password set some other way has made moot; a password written through
// [Service.SetPassword] spends it in the same transaction already. A user
// with none pending is refused with [CodeInvitationNotPending]: the caller
// asked to close a door that is not open, and saying so tells them the link
// they worried about cannot be used. An expired row is removed all the same.
func (s *Service) WithdrawInvitation(ctx context.Context, userID, withdrawnBy string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(userID, models.UserIDPrefix, "the user identifier"); err != nil {
		return err
	}
	// The user is read first so an id nobody has is the user's 404, not a
	// missing invitation's.
	if _, err := s.repo.GetUser(ctx, userID); err != nil {
		return err
	}

	invitation, err := s.repo.WithdrawInvitation(ctx, userID)
	if stderrors.Is(err, repository.ErrNoInvitation) || (err == nil && !invitation.ExpiresAt.After(s.clock())) {
		return coreerrors.NotFound(CodeInvitationNotPending,
			"user %s holds no invitation that could still be accepted", userID)
	}
	if err != nil {
		return err
	}

	s.log.InfoContext(ctx, "an invitation was withdrawn", "user_id", userID, "withdrawn_by", withdrawnBy)

	return nil
}
