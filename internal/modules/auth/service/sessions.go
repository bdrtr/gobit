package service

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// CodeSessionNotOpen is a session the caller asked to close that is not one
// of their open sessions (ADR 0267).
const CodeSessionNotOpen = "auth_session_not_open"

// SessionView is one of a person's open sessions, as they are shown it.
type SessionView struct {
	ID        string
	CreatedAt time.Time
	ExpiresAt time.Time
	// Current reports that this is the session the request was made with.
	Current bool
	// UserAgent is what the browser said it was at the sign-in; empty for a
	// session opened before it was kept (ADR 0276).
	UserAgent string
}

// recordSession writes the row a sign-in's token names (ADR 0267). The token
// is signed by the sign-in itself, after the second factor was demanded; this
// only records it.
//
// The person's expired sessions are forgotten first, so the table holds what
// can still be used and no job has to sweep it; a failure to forget is logged
// and does not refuse the sign-in. A failure to record is: a token naming a
// row that does not exist would be refused on its first request.
func (s *Service) recordSession(
	ctx context.Context, userID, sessionID string, now, expiresAt time.Time, userAgent string,
) error {
	if err := s.repo.PruneSessions(ctx, userID, now); err != nil {
		s.log.WarnContext(ctx, "the expired sessions could not be forgotten",
			slog.String("user_id", userID), slog.Any("error", err))
	}

	return s.repo.InsertSession(ctx, models.Session{
		ID: sessionID, UserID: userID, CreatedAt: now, ExpiresAt: expiresAt,
		UserAgent: boundedUserAgent(userAgent),
	})
}

// boundedUserAgent keeps at most [models.MaxUserAgent] bytes of what the
// browser said, cut at a character rather than inside one, with its control
// characters dropped so the label prints on one line (ADR 0276).
func boundedUserAgent(userAgent string) string {
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, strings.TrimSpace(userAgent))
	if len(cleaned) <= models.MaxUserAgent {
		return cleaned
	}
	cut := models.MaxUserAgent
	for cut > 0 && !utf8.RuneStart(cleaned[cut]) {
		cut--
	}

	return cleaned[:cut]
}

// checkSession holds a token that names a session to that session: it has to
// exist, belong to the token's subject and not have been closed by itself.
// A token that names none was signed before sessions were recorded and is
// judged by the anchor alone.
func (s *Service) checkSession(ctx context.Context, parsed parsedToken) error {
	if parsed.SessionID == "" {
		return nil
	}

	session, err := s.repo.GetSession(ctx, parsed.SessionID)
	if errors.IsNotFound(err) {
		return errors.Unauthorized(CodeTokenInvalid, "the session the token names does not exist")
	}
	if err != nil {
		return err
	}
	if session.UserID != parsed.Subject {
		return errors.Unauthorized(CodeTokenInvalid, "the session the token names belongs to somebody else")
	}
	if session.RevokedAt != nil {
		return errors.Unauthorized(CodeTokenInvalid, "the session was closed")
	}

	return nil
}

// ListSessions returns the person's open sessions, newest first: neither
// closed by themselves, nor expired, nor signed before the anchor that
// logging out everywhere or changing the password moved (ADR 0267).
func (s *Service) ListSessions(ctx context.Context, principal corehttp.Principal) ([]SessionView, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}

	now := s.clock()
	anchor, err := s.repo.SessionAnchor(ctx, principal.ID)
	if err != nil {
		return nil, err
	}
	sessions, err := s.repo.ListUnclosedSessions(ctx, principal.ID, now)
	if err != nil {
		return nil, err
	}

	out := make([]SessionView, 0, len(sessions))
	for i := range sessions {
		if signedBefore(sessions[i].CreatedAt, anchor) {
			continue
		}
		out = append(out, SessionView{
			ID:        sessions[i].ID,
			CreatedAt: sessions[i].CreatedAt,
			ExpiresAt: sessions[i].ExpiresAt,
			Current:   sessions[i].ID == principal.SessionID,
			UserAgent: sessions[i].UserAgent,
		})
	}

	return out, nil
}

// CloseSession closes one of the person's own sessions; a session that is not
// open and theirs is a 404 rather than a success, so a mistyped id is not
// mistaken for a closed one.
func (s *Service) CloseSession(ctx context.Context, userID, sessionID string) error {
	if err := s.ready(); err != nil {
		return err
	}

	closed, err := s.repo.CloseSession(ctx, userID, sessionID, s.clock())
	if err != nil {
		return err
	}
	if !closed {
		return errors.NotFound(CodeSessionNotOpen, "there is no open session %s of this account", sessionID)
	}

	s.log.InfoContext(ctx, "admin session closed",
		slog.String("user_id", userID), slog.String("session_id", sessionID))

	return nil
}

// CloseOtherSessions closes every open session of the person but the one the
// request was made with, and reports how many it closed.
func (s *Service) CloseOtherSessions(ctx context.Context, principal corehttp.Principal) (int64, error) {
	if err := s.ready(); err != nil {
		return 0, err
	}

	closed, err := s.repo.CloseOtherSessions(ctx, principal.ID, principal.SessionID, s.clock())
	if err != nil {
		return 0, err
	}

	s.log.InfoContext(ctx, "admin sessions closed but one",
		slog.String("user_id", principal.ID), slog.Int64("closed", closed))

	return closed, nil
}
