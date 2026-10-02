package identitysession

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The codes a signed-in password change answers with (ADR 0375).
const (
	// CodePasswordChangeInvalid is a new password the hash refuses, or a body
	// this module cannot read.
	CodePasswordChangeInvalid = "identity_session_password_change_invalid"
	// CodeCurrentPasswordWrong is a current password that does not match.
	CodeCurrentPasswordWrong = "identity_session_current_password_wrong"
	// CodeNoPasswordHere is a customer who signs in some other way than a
	// password kept here, so there is none to change.
	CodeNoPasswordHere = "identity_session_no_password_here"
)

// CustomerCredentials is the OPTIONAL capability a store offers to read a
// credential by its customer (ADR 0375): a session proves a customer, not an
// address. It is not a method of [Credentials], which is published, for
// [PasswordReset]'s reason.
type CustomerCredentials interface {
	// CredentialOf returns the address and the stored hash of the customer's
	// credential, or [ErrNoCredential] when they have none here.
	CredentialOf(ctx context.Context, customerID string) (email, passwordHash string, err error)
}

// passwordChange is the body of POST /store/v1/auth/password.
type passwordChange struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// passwordChangeMounted says whether the bound store can read a credential by
// its customer.
func (m *Module) passwordChangeMounted() bool {
	_, ok := m.store.(CustomerCredentials)

	return ok
}

// changePassword replaces the signed-in customer's password with a new one,
// given the current one (ADR 0375).
//
// The current password is asked for because the session alone is not the
// person: a cookie left in a shared browser, or copied out of one, would
// otherwise be enough to lock its owner out. The new one is checked before the
// current one is, so a refused new password costs one hash and says nothing
// about the current one.
//
// Then it is a replacement like a reset's (ADR 0374): every session issued
// before it ends, the anchor moving before the password does, and this browser
// gets a session issued after it.
func (m *Module) changePassword(w http.ResponseWriter, r *http.Request) {
	customerID, err := m.sessions.CustomerID(r)
	if err != nil {
		m.refuseSession(w, r, err)

		return
	}

	var body passwordChange
	if !decode(w, r, &body) {
		return
	}
	newHash, err := HashPassword(body.NewPassword)
	if err != nil {
		corehttp.WriteError(r.Context(), w, coreerrors.Invalid(CodePasswordChangeInvalid, "%s", err.Error()))

		return
	}

	store, _ := m.store.(CustomerCredentials)
	email, currentHash, err := store.CredentialOf(r.Context(), customerID)
	switch {
	case errors.Is(err, ErrNoCredential):
		corehttp.WriteError(r.Context(), w, coreerrors.Conflict(CodeNoPasswordHere,
			"this account does not sign in with a password kept here, so there is none to change"))

		return
	case err != nil:
		m.unavailable(w, r, "the credential could not be read", err)

		return
	}

	// Every failure VerifyPassword has is a mismatch, a stored hash it cannot
	// read included, as a sign-in's is.
	if err := VerifyPassword(currentHash, body.CurrentPassword); err != nil {
		corehttp.WriteError(r.Context(), w, coreerrors.Forbidden(CodeCurrentPasswordWrong,
			"the current password does not match"))

		return
	}

	if err := m.endSessions(r.Context(), customerID); err != nil {
		m.unavailable(w, r, "the sessions before the change could not be ended", err)

		return
	}
	if err := m.store.Put(r.Context(), customerID, email, newHash); err != nil {
		m.unavailable(w, r, "the new password could not be written", err)

		return
	}

	m.log.InfoContext(r.Context(), "identity-session replaced a password its owner proved",
		"customer_id", customerID)

	m.sessions.Issue(w, customerID)
	corehttp.WriteJSON(r.Context(), w, http.StatusNoContent, nil)
}

// The store reads a credential by its customer (ADR 0375).
var _ CustomerCredentials = pgCredentials{}

// CredentialOf reads by the primary key.
func (s pgCredentials) CredentialOf(ctx context.Context, customerID string) (email, passwordHash string, err error) {
	switch err := s.pool.QueryRow(ctx,
		`SELECT email, password_hash FROM customer_credentials WHERE customer_id = $1`,
		customerID).Scan(&email, &passwordHash); {
	case errors.Is(err, pgx.ErrNoRows):
		return "", "", ErrNoCredential
	case err != nil:
		return "", "", fmt.Errorf("identity-session: the credential could not be read: %w", err)
	}

	return email, passwordHash, nil
}
