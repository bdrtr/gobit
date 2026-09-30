package service

import (
	"context"
	"errors"

	"github.com/bdrtr/gobit/internal/modules/auth/repository"
)

// SecondFactorSurface is a person's own second factor as the admin panel
// reaches it (ADR 0266).
//
// It speaks in PRIMITIVES because the panel may import no module (ADR 0013):
// the enrolment comes back as the secret and its link rather than as
// [MFAEnrollment], and every act names the person the panel's session proved.
// It adds no rule of its own; each method is the service's, so the panel and
// the API change a factor under the same conditions (ADR 0264).
type SecondFactorSurface struct {
	svc    *Service
	issuer string
}

// NewSecondFactorSurface builds the surface over the service, with the name an
// authenticator app shows beside the account.
func NewSecondFactorSurface(svc *Service, issuer string) *SecondFactorSurface {
	return &SecondFactorSurface{svc: svc, issuer: issuer}
}

// SecondFactorStatus reports whether the person has proven an authenticator,
// whether an enrolment is waiting to be proven, and whether the installation
// requires one they have not proven (ADR 0265).
func (s *SecondFactorSurface) SecondFactorStatus(ctx context.Context, userID string) (proven, waiting, owed bool, err error) {
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
func (s *SecondFactorSurface) EnrollSecondFactor(ctx context.Context, userID, currentCode string) (secret, uri string, err error) {
	enrollment, err := s.svc.EnrolMFA(ctx, userID, s.issuer, currentCode)
	if err != nil {
		return "", "", err
	}

	return enrollment.Secret, enrollment.URI, nil
}

// ConfirmSecondFactor proves the waiting enrolment with the code its app shows.
func (s *SecondFactorSurface) ConfirmSecondFactor(ctx context.Context, userID, code string) error {
	return s.svc.ConfirmMFA(ctx, userID, code)
}

// RemoveSecondFactor takes the person's factor off, given its current code.
func (s *SecondFactorSurface) RemoveSecondFactor(ctx context.Context, userID, code string) error {
	_, err := s.svc.RemoveOwnMFA(ctx, userID, code)

	return err
}
