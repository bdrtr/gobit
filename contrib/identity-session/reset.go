package identitysession

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The codes a password reset answers with (ADR 0373).
const (
	// CodePasswordResetInvalid is a reset request or confirmation this module
	// cannot act on: no address, or a password the hash refuses.
	CodePasswordResetInvalid = "identity_session_password_reset_invalid"
	// CodePasswordResetNotUsable is a reset link that is unknown, already used
	// or expired.
	CodePasswordResetNotUsable = "identity_session_password_reset_not_usable"
)

// DefaultPasswordResetTTL is how long a reset link works when
// [Options.PasswordResetTTL] is not set: an hour, as a sign-up link.
const DefaultPasswordResetTTL = time.Hour

// PasswordReset is how a reset link reaches the person (ADR 0373).
//
// It is a seam of its own rather than a third method on [Verification]: that
// interface is published, and a method added to it would compile-break every
// shop that implements it for a flow it may not want. A shop that sends sign-up
// mail implements this beside it in a few lines.
type PasswordReset interface {
	// SendPasswordReset carries a reset token to the address of an account.
	//
	// The token replaces the account's password for whoever holds it, so an
	// implementation puts it in the message and nowhere else.
	SendPasswordReset(ctx context.Context, email, token string) error
}

// PasswordResets is the OPTIONAL capability a store offers to hold a pending
// reset, for [Registrations]' reason: a store over LDAP or a shop's own users
// table has nowhere to put a row of gobit's, and the flow stays unmounted.
type PasswordResets interface {
	// PutPasswordReset writes a pending reset for a customer who has a
	// credential, REPLACING any the customer already had.
	PutPasswordReset(ctx context.Context, tokenHash, customerID string, expiresAt time.Time) error
	// TakePasswordReset removes a pending reset and answers the customer and
	// the address their credential signs in with, in one statement, so a token
	// is single-use whatever the timing. It answers [ErrNoPasswordReset] for a
	// token that is unknown, already used or expired.
	TakePasswordReset(ctx context.Context, tokenHash string) (customerID, email string, err error)
}

// ErrNoPasswordReset is a token that is not a usable pending reset.
var ErrNoPasswordReset = errors.New("identity-session: that token is not a pending password reset")

// passwordResetRequest is the body that asks for a reset link.
type passwordResetRequest struct {
	Email string `json:"email"`
}

// passwordResetConfirmation is the body that follows one.
type passwordResetConfirmation struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// passwordResetMounted says whether the flow has what it needs: somebody to
// carry the link, and a store that can hold a pending reset. Missing either,
// the endpoints are not mounted, so nobody is offered a reset that cannot
// arrive or cannot be completed.
func (m *Module) passwordResetMounted() bool {
	if isNil(m.opts.PasswordReset) || m.store == nil {
		return false
	}
	_, ok := m.store.(PasswordResets)

	return ok
}

// requestPasswordReset sends a reset link to the address of an account.
//
// # It answers the same thing whether the address has an account or not
//
// 202 either way, as the registration does, because anything else answers for
// any address whether that person shops here. An address with no credential
// gets no message at all: there is no password to reset, and sending a stranger
// mail about an address they typed would make the endpoint a way to mail
// anybody from this shop. The registration's own message tells somebody who
// forgot that they have an account; this endpoint is where that message sends
// them.
func (m *Module) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	var body passwordResetRequest
	if !decode(w, r, &body) {
		return
	}

	email, err := registrationAddress(body.Email)
	if err != nil {
		corehttp.WriteError(r.Context(), w, coreerrors.Invalid(CodePasswordResetInvalid, "%s", err.Error()))

		return
	}

	customerID, _, err := m.store.Credential(r.Context(), email)
	switch {
	case errors.Is(err, ErrPasswordMismatch):
		corehttp.WriteJSON(r.Context(), w, http.StatusAccepted, nil)

		return
	case err != nil:
		m.unavailable(w, r, "whether that address has an account could not be read", err)

		return
	}

	token, tokenHash, err := newRegistrationToken()
	if err != nil {
		m.unavailable(w, r, "the reset token could not be minted", err)

		return
	}

	// Recorded first, then sent, for the order the registration argues: a link
	// that cannot work is worse than a row nobody uses.
	store, _ := m.store.(PasswordResets)
	if err := store.PutPasswordReset(r.Context(), tokenHash, customerID, m.passwordResetDeadline()); err != nil {
		m.unavailable(w, r, "the reset could not be recorded", err)

		return
	}
	if err := m.opts.PasswordReset.SendPasswordReset(r.Context(), email, token); err != nil {
		m.unavailable(w, r, "the reset message could not be sent", err)

		return
	}

	corehttp.WriteJSON(r.Context(), w, http.StatusAccepted, nil)
}

// confirmPasswordReset replaces the password a reset link was sent for and
// signs the person in.
//
// The new password is hashed before the token is touched, so a password the
// hash refuses does not spend the link. The token is then consumed BEFORE the
// credential is written, for the registration's reason: the other order leaves
// a replayable link that sets the password back to this one.
//
// It ends every session issued before it, where the store keeps the moment
// sessions count from (ADR 0374): a reset is how somebody locks out whoever
// learned the old password. The anchor moves before the password does, so a
// failure between the two has signed people out rather than left a stranger
// signed in. Then it signs the person in, because following the link is the
// proof every password reset rests on.
func (m *Module) confirmPasswordReset(w http.ResponseWriter, r *http.Request) {
	var body passwordResetConfirmation
	if !decode(w, r, &body) {
		return
	}

	passwordHash, err := HashPassword(body.Password)
	if err != nil {
		corehttp.WriteError(r.Context(), w, coreerrors.Invalid(CodePasswordResetInvalid, "%s", err.Error()))

		return
	}

	store, _ := m.store.(PasswordResets)
	customerID, email, err := store.TakePasswordReset(
		r.Context(), hashRegistrationToken(strings.TrimSpace(body.Token)))
	switch {
	case errors.Is(err, ErrNoPasswordReset):
		corehttp.WriteError(r.Context(), w, coreerrors.Invalid(CodePasswordResetNotUsable,
			"that link is not usable; it may have been used already or expired, so ask for a new one"))

		return
	case err != nil:
		m.unavailable(w, r, "the reset could not be read", err)

		return
	}

	if err := m.endSessions(r.Context(), customerID); err != nil {
		m.unavailable(w, r, "the sessions before the reset could not be ended", err)

		return
	}
	if err := m.store.Put(r.Context(), customerID, email, passwordHash); err != nil {
		m.unavailable(w, r, "the new password could not be written", err)

		return
	}

	m.log.InfoContext(r.Context(), "identity-session replaced a password from a proven address",
		"customer_id", customerID)
	m.notifyPasswordChanged(r.Context(), customerID, email)

	m.sessions.Issue(w, customerID)
	corehttp.WriteJSON(r.Context(), w, http.StatusNoContent, nil)
}

// passwordResetDeadline is when a reset link minted now stops working.
func (m *Module) passwordResetDeadline() time.Time {
	ttl := m.opts.PasswordResetTTL
	if ttl <= 0 {
		ttl = DefaultPasswordResetTTL
	}

	return time.Now().UTC().Add(ttl)
}
