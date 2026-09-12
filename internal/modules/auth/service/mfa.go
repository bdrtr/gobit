package service

import (
	"context"
	"errors"
	"log/slog"
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
	// CodeMFARequired is a correct password from somebody who holds a factor and
	// sent no code.
	//
	// It is SEPARATE from [CodeInvalidCredentials] on purpose, and the separation
	// costs nothing that doctrine protects: it is only ever returned AFTER the
	// password matched, so it tells a caller who already knows the password that
	// the account has a second factor. What it buys is the second step — a client
	// that cannot tell this apart from a wrong password has no way to ask for the
	// six digits.
	CodeMFARequired = "auth_mfa_required"
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
// # What re-enrolling does, and what it must not do
//
// Somebody moving to a new phone asks again. If nothing is confirmed yet the new
// secret simply REPLACES the half-written one: there is nothing to keep alive.
//
// If a factor IS confirmed, the new secret is PARKED beside it (ADR 0147) and the
// old phone keeps signing the person in until they prove the new one. The reason
// is the demand: since a login refuses without the code, an enrollment that cleared
// the confirmation and was then abandoned would turn the demand off — a way out of
// the second factor that needs no secret at all.
//
// A lost phone therefore cannot be fixed from here any more: its owner cannot sign
// in to ask. That case belongs to the operator (`gobit mfa-reset`).
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

	if err := s.storeEnrolment(ctx, userID, sealed); err != nil {
		return MFAEnrollment{}, err
	}

	return MFAEnrollment{
		Secret: secret,
		URI:    otpauthURI(mfaIssuer(issuer), user.Email, secret),
	}, nil
}

// storeEnrolment writes the sealed secret where it belongs for this account.
//
// The order is the unconfirmed case FIRST, and it is the cheap one to get wrong:
// the write itself refuses to touch a confirmed credential, so a sentinel here
// means "this person has a proven factor" and the secret goes beside it. Asking
// first and writing second would be the same two statements with a race between
// them — two enrollments by the same person, which is harmless, and a confirmation
// landing in between, which is not.
func (s *Service) storeEnrolment(ctx context.Context, userID string, sealed []byte) error {
	_, err := s.repo.PutMFACredential(ctx, userID, sealed)
	if err == nil {
		return nil
	}
	if !errors.Is(err, repository.ErrNoMFACredential) {
		return err
	}

	if _, err := s.repo.PutPendingMFASecret(ctx, userID, sealed); err != nil {
		if errors.Is(err, repository.ErrNoMFACredential) {
			// The confirmation was undone between the two statements, which is the
			// owner removing their factor mid-enrollment. Nothing is wrong with
			// either request and the person can ask again.
			return coreerrors.Conflict(CodeMFANotEnrolled,
				"the second factor on this account changed while the enrollment was being "+
					"written; start it again")
		}

		return err
	}

	return nil
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
	if credential.Confirmed() && !credential.Waiting() {
		return coreerrors.Conflict(CodeMFANotEnrolled,
			"this second factor is already confirmed; enroll again to replace it")
	}

	// The WAITING secret is the one being proven when there is one. Matching
	// against the confirmed secret instead would let the old phone confirm the new
	// enrollment, and the person would walk away believing the new app works.
	sealed := credential.Secret
	if credential.Waiting() {
		sealed = credential.PendingSecret
	}

	secret, err := s.secrets.open(sealed)
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

	if credential.Waiting() {
		return s.promoteWaiting(ctx, userID)
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

// promoteWaiting makes the proven replacement the secret that signs in.
//
// A sentinel means the promotion already happened, which is two correct codes
// racing: the second one is right about the same fact and has nothing to add.
func (s *Service) promoteWaiting(ctx context.Context, userID string) error {
	if _, err := s.repo.PromotePendingMFASecret(ctx, userID); err != nil {
		if errors.Is(err, repository.ErrNoMFACredential) {
			return nil
		}

		return err
	}

	return nil
}

// RemoveMFA takes the second factor off an account and reports whether there was
// one.
//
// # Who may call it
//
// The owner, through the endpoint above this — which acts on whoever the request
// proved, like the rest of this file — and the operator at the machine, through
// `gobit mfa-reset`. There is deliberately NO endpoint that removes a colleague's
// factor: an administrator who could would be one stolen session away from turning
// off somebody else's second factor, and the point of the factor is that a stolen
// session is not enough.
//
// So the answer for a lost phone is machine access, which is the privilege level
// that case deserves and the one an installation can audit separately.
func (s *Service) RemoveMFA(ctx context.Context, userID string) (bool, error) {
	if strings.TrimSpace(userID) == "" {
		return false, coreerrors.Invalid(CodeMFANotEnrolled,
			"a second factor belongs to a person, and this request names none")
	}

	removed, err := s.repo.DeleteMFACredential(ctx, userID)
	if err != nil {
		return false, err
	}

	s.log.InfoContext(ctx, "second factor removed",
		slog.String("user_id", userID), slog.Bool("had_one", removed))

	return removed, nil
}

// HasConfirmedMFA reports whether a user has proven a second factor.
//
// It is the question [Service.Login] asks before it issues a token, and the shape
// of the storage is what makes the answer safe: an enrollment that was never
// confirmed reads as NO, so a scan that failed half way locks nobody out, and a
// secret WAITING beside a confirmed one reads as YES, because the person still has
// the phone they proved.
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
