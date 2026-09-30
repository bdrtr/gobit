package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/repository"
)

// AccountSurface is a person's own account as the admin panel reaches it:
// their second factor (ADR 0266) and their sessions (ADR 0268).
//
// It speaks in PRIMITIVES because the panel may import no module (ADR 0013):
// the enrolment comes back as the secret and its link rather than as
// [MFAEnrollment], the sessions as JSON, and every act names the person the
// panel's session proved. It adds no rule of its own; each method is the
// service's, so the panel and the API act under the same conditions.
type AccountSurface struct {
	svc    *Service
	issuer string
}

// NewAccountSurface builds the surface over the service, with the name an
// authenticator app shows beside the account.
func NewAccountSurface(svc *Service, issuer string) *AccountSurface {
	return &AccountSurface{svc: svc, issuer: issuer}
}

// SecondFactorStatus reports whether the person has proven an authenticator,
// whether an enrolment is waiting to be proven, and whether the installation
// requires one they have not proven (ADR 0265).
func (s *AccountSurface) SecondFactorStatus(ctx context.Context, userID string) (proven, waiting, owed bool, err error) {
	credential, err := s.svc.repo.GetMFACredential(ctx, userID)
	switch {
	case errors.Is(err, repository.ErrNoMFACredential):
	case err != nil:
		return false, false, false, err
	default:
		proven = credential.Confirmed()
		waiting = !proven || credential.Waiting()
	}

	owed, err = s.svc.SecondFactorOwed(ctx, userID)

	return proven, waiting, owed, err
}

// EnrollSecondFactor draws a secret for the person and returns it with its
// otpauth link, once; replacing a proven factor takes its current code.
func (s *AccountSurface) EnrollSecondFactor(ctx context.Context, userID, currentCode string) (secret, uri string, err error) {
	enrollment, err := s.svc.EnrolMFA(ctx, userID, s.issuer, currentCode)
	if err != nil {
		return "", "", err
	}

	return enrollment.Secret, enrollment.URI, nil
}

// ConfirmSecondFactor proves the waiting enrolment with the code its app shows.
func (s *AccountSurface) ConfirmSecondFactor(ctx context.Context, userID, code string) error {
	return s.svc.ConfirmMFA(ctx, userID, code)
}

// RemoveSecondFactor takes the person's factor off, given its current code.
func (s *AccountSurface) RemoveSecondFactor(ctx context.Context, userID, code string) error {
	_, err := s.svc.RemoveOwnMFA(ctx, userID, code)

	return err
}

// panelSession is one open session as the panel reads it; the json tags are
// the contract, because the panel cannot import this package.
type panelSession struct {
	ID        string    `json:"session_id"`
	CreatedAt time.Time `json:"signed_in_at"`
	ExpiresAt time.Time `json:"ends_at"`
	Current   bool      `json:"current"`
}

// SessionsJSON returns the person's open sessions, newest first, with the one
// named current marked (ADR 0268).
func (s *AccountSurface) SessionsJSON(ctx context.Context, userID, currentSessionID string) (json.RawMessage, error) {
	sessions, err := s.svc.ListSessions(ctx, corehttp.Principal{
		ID: userID, Kind: PrincipalKindUser, SessionID: currentSessionID,
	})
	if err != nil {
		return nil, err
	}

	out := make([]panelSession, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, panelSession(session))
	}

	return json.Marshal(out)
}

// CloseSession closes one of the person's own sessions.
func (s *AccountSurface) CloseSession(ctx context.Context, userID, sessionID string) error {
	return s.svc.CloseSession(ctx, userID, sessionID)
}

// CloseOtherSessions closes every session of the person but the current one.
func (s *AccountSurface) CloseOtherSessions(ctx context.Context, userID, currentSessionID string) (int64, error) {
	return s.svc.CloseOtherSessions(ctx, corehttp.Principal{
		ID: userID, Kind: PrincipalKindUser, SessionID: currentSessionID,
	})
}
