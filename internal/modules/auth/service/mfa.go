package service

import (
	"context"
	"errors"
	"strings"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/repository"
)

// The codes a second factor answers with.
const (
	// CodeMFAUnavailable reports an installation that cannot hold a TOTP secret.
	//
	// It is a 500 and not a 422, because nothing the caller sent is wrong: the
	// deployment has no MFA_SECRET_KEY, and a person pressing "enable two-factor"
	// has no way to supply one.
	CodeMFAUnavailable = "auth_mfa_unavailable"
	// CodeMFASecretUnreadable reports a stored secret that will not open.
	//
	// In practice it means the key changed. It is separate from
	// [CodeMFAUnavailable] because the repair is different: there the operator
	// sets a variable, here they have to decide between restoring the old key and
	// asking everybody to enroll again.
	//nolint:gosec // G101 reads the NAME: this is an error code, not a credential.
	CodeMFASecretUnreadable = "auth_mfa_secret_unreadable"
	// CodeMFANotEnrolled is a confirmation for a user who has no credential
	// waiting, which includes one already confirmed.
	CodeMFANotEnrolled = "auth_mfa_not_enrolled"
	// CodeMFACodeWrong is a code that is not one of the ones valid around now.
	//
	// ONE code for "wrong digits", "too late" and "not a number". Telling them
	// apart would tell somebody guessing whether they are close, and the person
	// holding the phone sees the same six digits either way.
	CodeMFACodeWrong = "auth_mfa_code_wrong"
)

// MFAEnrollment is what an enrollment hands back, once.
//
// # The secret crosses the boundary exactly here
//
// An authenticator app needs the secret, and there is no way to give it one
// without showing it: the QR code IS the secret. So this is the only value in the
// module that carries a readable secret outward, it is produced by a request the
// person made about themselves, and it is never readable again — a second call
// draws a NEW secret rather than repeating the old one.
type MFAEnrollment struct {
	// Secret is the base32 value, for somebody typing it in by hand.
	Secret string
	// URI is the same secret as an `otpauth://` link, for a QR code.
	URI string
}

// EnrolMFA draws a second factor for the caller and stores it UNCONFIRMED.
//
// # Why it takes the caller and not a user id
//
// A second factor is a thing a person HAS. An administrator who could enroll one
// for somebody else would hold the secret of that person's phone, which is the
// whole of what the factor is worth — the same reason ADR 0137 keeps an
// invitation token out of the response its issuer reads.
//
// So the endpoint above this acts on whoever the request proves, and this method
// takes the id that proof produced.
//
// # Why re-enrolling is allowed and replaces
//
// It is what somebody with a lost phone does. The write replaces the row and
// clears the confirmation, so the old secret stops working the moment the new one
// is proven — and until then the person still has whatever they had before.
func (s *Service) EnrolMFA(ctx context.Context, userID, issuer string) (MFAEnrollment, error) {
	if strings.TrimSpace(userID) == "" {
		return MFAEnrollment{}, coreerrors.Invalid(CodeMFANotEnrolled,
			"a second factor belongs to a person, and this request names none")
	}
	if !s.secrets.ready() {
		return MFAEnrollment{}, coreerrors.Internal(CodeMFAUnavailable,
			"this installation cannot store a TOTP secret safely: MFA_SECRET_KEY is not "+
				"set, and enrolling without it would keep the secret in plaintext")
	}

	user, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return MFAEnrollment{}, err
	}

	secret, err := newTOTPSecret()
	if err != nil {
		return MFAEnrollment{}, err
	}

	sealed, err := s.secrets.seal([]byte(secret))
	if err != nil {
		return MFAEnrollment{}, err
	}

	if _, err := s.repo.PutMFACredential(ctx, userID, sealed); err != nil {
		return MFAEnrollment{}, err
	}

	return MFAEnrollment{
		Secret: secret,
		URI:    otpauthURI(mfaIssuer(issuer), user.Email, secret),
	}, nil
}

// ConfirmMFA proves that the app holds the secret this module stored.
//
// # Why a credential does not count until this runs
//
// Between the enrollment and the first correct code, nobody has shown that the
// scan worked. A credential that counted from the moment it was written would
// lock out every person whose phone died half way through, and that is the
// ordinary failure of enrolling rather than an exotic one.
func (s *Service) ConfirmMFA(ctx context.Context, userID, code string) error {
	if !s.secrets.ready() {
		return coreerrors.Internal(CodeMFAUnavailable,
			"this installation has no MFA_SECRET_KEY, so no stored secret can be read")
	}

	credential, err := s.repo.GetMFACredential(ctx, userID)
	if errors.Is(err, repository.ErrNoMFACredential) {
		return coreerrors.Conflict(CodeMFANotEnrolled,
			"there is no enrollment waiting to be confirmed; start one and scan the code")
	}
	if err != nil {
		return err
	}
	if credential.Confirmed() {
		return coreerrors.Conflict(CodeMFANotEnrolled,
			"this second factor is already confirmed; enroll again to replace it")
	}

	secret, err := s.secrets.open(credential.Secret)
	if err != nil {
		return err
	}

	matches, err := totpMatches(string(secret), code, s.clock())
	if err != nil {
		return err
	}
	if !matches {
		return coreerrors.Invalid(CodeMFACodeWrong,
			"that code is not the one this authenticator produces right now")
	}

	if _, err := s.repo.ConfirmMFACredential(ctx, userID); err != nil {
		// The row was confirmed between the read and the write, which is two
		// correct codes racing. Nothing is wrong with either of them.
		if errors.Is(err, repository.ErrNoMFACredential) {
			return nil
		}

		return err
	}

	return nil
}

// HasConfirmedMFA reports whether a user has proven a second factor.
//
// It is the question a login flow will ask, and it is written now because the
// storage shape has to answer it: an enrollment that was never confirmed must read
// as NO. Nothing calls it yet, and the endpoint that will is a separate decision
// — requiring a factor is a different act from being able to hold one.
func (s *Service) HasConfirmedMFA(ctx context.Context, userID string) (bool, error) {
	credential, err := s.repo.GetMFACredential(ctx, userID)
	if errors.Is(err, repository.ErrNoMFACredential) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	return credential.Confirmed(), nil
}

// defaultMFAIssuer is what an authenticator shows when the caller names nothing.
const defaultMFAIssuer = "gobit"

// mfaIssuer is the name the person sees in their app.
//
// It is trimmed of the characters that would break the label: an otpauth label is
// `issuer:account` and a colon inside the issuer makes the app read the rest as
// the account.
func mfaIssuer(issuer string) string {
	cleaned := strings.TrimSpace(strings.ReplaceAll(issuer, ":", " "))
	if cleaned == "" {
		return defaultMFAIssuer
	}

	return cleaned
}
