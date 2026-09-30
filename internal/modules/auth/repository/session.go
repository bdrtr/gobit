package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository/authdb"
)

// CodeSessionNotFound is a session that does not exist.
const CodeSessionNotFound = "auth_session_not_found"

// InsertSession records a sign-in (ADR 0267).
func (r *Repo) InsertSession(ctx context.Context, session models.Session) error {
	if err := r.ready(); err != nil {
		return err
	}

	err := r.q.InsertSession(ctx, authdb.InsertSessionParams{
		ID:        session.ID,
		UserID:    session.UserID,
		CreatedAt: fromTime(session.CreatedAt),
		ExpiresAt: fromTime(session.ExpiresAt),
	})

	return wrapDB(err, "could not record the session of user %s", session.UserID)
}

// GetSession reads one session; errors.NotFound when there is none.
func (r *Repo) GetSession(ctx context.Context, id string) (models.Session, error) {
	if err := r.ready(); err != nil {
		return models.Session{}, err
	}

	row, err := r.q.GetSession(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.Session{}, coreerrors.NotFound(CodeSessionNotFound, "there is no session %s", id)
	}
	if err != nil {
		return models.Session{}, wrapDB(err, "could not read session %s", id)
	}

	return toSession(row), nil
}

// ListUnclosedSessions returns a person's sessions that are neither closed by
// themselves nor expired at now, newest first.
func (r *Repo) ListUnclosedSessions(ctx context.Context, userID string, now time.Time) ([]models.Session, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.q.ListUnclosedSessions(ctx, authdb.ListUnclosedSessionsParams{UserID: userID, Now: fromTime(now)})
	if err != nil {
		return nil, wrapDB(err, "could not read the sessions of user %s", userID)
	}
	out := make([]models.Session, 0, len(rows))
	for i := range rows {
		out = append(out, toSession(rows[i]))
	}

	return out, nil
}

// CloseSession closes one of the person's sessions and reports whether one was
// open.
func (r *Repo) CloseSession(ctx context.Context, userID, id string, now time.Time) (bool, error) {
	if err := r.ready(); err != nil {
		return false, err
	}

	closed, err := r.q.CloseSession(ctx, authdb.CloseSessionParams{ID: id, UserID: userID, Now: fromTime(now)})
	if err != nil {
		return false, wrapDB(err, "could not close session %s", id)
	}

	return closed > 0, nil
}

// CloseOtherSessions closes every open session of the person but keep, and
// reports how many it closed.
func (r *Repo) CloseOtherSessions(ctx context.Context, userID, keep string, now time.Time) (int64, error) {
	if err := r.ready(); err != nil {
		return 0, err
	}

	closed, err := r.q.CloseOtherSessions(ctx, authdb.CloseOtherSessionsParams{
		UserID: userID, KeepID: keep, Now: fromTime(now),
	})

	return closed, wrapDB(err, "could not close the other sessions of user %s", userID)
}

// PruneSessions forgets a person's expired sessions.
func (r *Repo) PruneSessions(ctx context.Context, userID string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	return wrapDB(r.q.PruneSessions(ctx, authdb.PruneSessionsParams{UserID: userID, Now: fromTime(now)}),
		"could not forget the expired sessions of user %s", userID)
}

// toSession converts a row.
func toSession(row authdb.AuthSession) models.Session {
	return models.Session{
		ID:        row.ID,
		UserID:    row.UserID,
		CreatedAt: toTime(row.CreatedAt),
		ExpiresAt: toTime(row.ExpiresAt),
		RevokedAt: toTimePtr(row.RevokedAt),
	}
}
