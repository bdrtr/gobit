package repository

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// WithdrawInvitation removes the user's invitation, whatever its token, and
// answers what it held; a user with none is [ErrNoInvitation] (ADR 0453). An
// expired row is removed and answered too: whether it was still pending is
// the caller's question.
func (r *Repo) WithdrawInvitation(ctx context.Context, userID string) (models.UserInvitation, error) {
	if err := r.ready(); err != nil {
		return models.UserInvitation{}, err
	}

	row, err := r.q.WithdrawInvitation(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.UserInvitation{}, ErrNoInvitation
	}
	if err != nil {
		return models.UserInvitation{}, wrapDB(err, "could not withdraw the invitation")
	}

	return toInvitation(row), nil
}
