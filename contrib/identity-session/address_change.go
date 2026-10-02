package identitysession

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The codes an address change answers with (ADR 0377).
const (
	// CodeAddressChangeInvalid is a new address this module cannot act on: not
	// an address, or the one the account already has.
	CodeAddressChangeInvalid = "identity_session_address_change_invalid"
	// CodeAddressChangeNotUsable is a link that is unknown, already used or
	// expired.
	CodeAddressChangeNotUsable = "identity_session_address_change_not_usable"
	// CodeAddressTaken is a proven address another account took meanwhile.
	CodeAddressTaken = "identity_session_address_taken"
)

// DefaultAddressChangeTTL is how long an address change link works when
// [Options.AddressChangeTTL] is not set: an hour, as a sign-up link.
const DefaultAddressChangeTTL = time.Hour

// AddressChanges is the OPTIONAL capability an [Accounts] offers to move an
// account to another address (ADR 0377). It is not a method of [Accounts],
// which is published, for [PasswordReset]'s reason.
type AddressChanges interface {
	// ChangeAccountEmail writes the address onto the customer's record. It is
	// called only after the address has been proven, and answers an error when
	// another account holds it.
	ChangeAccountEmail(ctx context.Context, customerID, email string) error
}

// AddressProof is how the proof of a new address reaches it (ADR 0377).
type AddressProof interface {
	// SendAddressProof carries a token to the address an account asked to
	// move to. Whoever holds the token moves the account there, so an
	// implementation puts it in the message and nowhere else.
	SendAddressProof(ctx context.Context, email, token string) error
}

// PendingAddresses is the OPTIONAL capability a store offers to hold an
// address change waiting for its link.
type PendingAddresses interface {
	// PutAddressChange writes a pending change for a customer who has a
	// credential, REPLACING any the customer already had.
	PutAddressChange(ctx context.Context, tokenHash, customerID, email string, expiresAt time.Time) error
	// TakeAddressChange removes a pending change and answers its customer and
	// address, in one statement, so a token is single-use whatever the timing.
	// It answers [ErrNoAddressChange] for a token that is unknown, already used
	// or expired.
	TakeAddressChange(ctx context.Context, tokenHash string) (customerID, email string, err error)
}

// ErrNoAddressChange is a token that is not a usable pending address change.
var ErrNoAddressChange = errors.New("identity-session: that token is not a pending address change")

// addressChangeRequest is the body that asks to move an account.
type addressChangeRequest struct {
	NewEmail        string `json:"new_email"`
	CurrentPassword string `json:"current_password"`
}

// addressChangeConfirmation is the body that follows the link.
type addressChangeConfirmation struct {
	Token string `json:"token"`
}

// addressChangeMounted says whether the flow has what it needs: somebody to
// move the account's record, somebody to carry the link, a store that holds a
// pending change, and one that reads a credential by its customer.
func (m *Module) addressChangeMounted() bool {
	if isNil(m.opts.Accounts) || isNil(m.opts.AddressProof) || m.store == nil {
		return false
	}
	_, moves := m.opts.Accounts.(AddressChanges)
	_, holds := m.store.(PendingAddresses)

	return moves && holds && m.passwordChangeMounted()
}

// requestAddressChange sends a link to the address a signed-in customer asks
// to move their account to (ADR 0377).
//
// The current password is asked for, as a password change asks for it: a
// session left in a shared browser must not be enough to move the account
// somewhere its owner cannot follow.
//
// # An address another account holds gets nothing
//
// The answer is 202 either way, and that address is not mailed: answering
// otherwise would tell any account holder which addresses have accounts, and
// mailing a stranger's address about somebody else's account is mail the shop
// should not send. The link is what moves the account, so nothing else changes
// until it is followed.
func (m *Module) requestAddressChange(w http.ResponseWriter, r *http.Request) {
	customerID, err := m.sessions.CustomerID(r)
	if err != nil {
		m.refuseSession(w, r, err)

		return
	}

	var body addressChangeRequest
	if !decode(w, r, &body) {
		return
	}
	email, err := registrationAddress(body.NewEmail)
	if err != nil {
		corehttp.WriteError(r.Context(), w, coreerrors.Invalid(CodeAddressChangeInvalid, "%s", err.Error()))

		return
	}

	credentials, _ := m.store.(CustomerCredentials)
	current, hash, err := credentials.CredentialOf(r.Context(), customerID)
	switch {
	case errors.Is(err, ErrNoCredential):
		corehttp.WriteError(r.Context(), w, coreerrors.Conflict(CodeNoPasswordHere,
			"this account does not sign in with a password kept here, so its address is not changed here"))

		return
	case err != nil:
		m.unavailable(w, r, "the credential could not be read", err)

		return
	}
	if err := VerifyPassword(hash, body.CurrentPassword); err != nil {
		corehttp.WriteError(r.Context(), w, coreerrors.Forbidden(CodeCurrentPasswordWrong,
			"the current password does not match"))

		return
	}
	if email == current {
		corehttp.WriteError(r.Context(), w, coreerrors.Invalid(CodeAddressChangeInvalid,
			"the account already signs in with that address"))

		return
	}

	owner, err := m.opts.Accounts.CustomerIDForEmail(r.Context(), email)
	if err != nil {
		m.unavailable(w, r, "whether that address has an account could not be read", err)

		return
	}
	if owner != "" {
		m.log.InfoContext(r.Context(), "identity-session sent no address change link: the address is another account's",
			"customer_id", customerID)
		corehttp.WriteJSON(r.Context(), w, http.StatusAccepted, nil)

		return
	}

	token, tokenHash, err := newRegistrationToken()
	if err != nil {
		m.unavailable(w, r, "the address change token could not be minted", err)

		return
	}
	pending, _ := m.store.(PendingAddresses)
	if err := pending.PutAddressChange(r.Context(), tokenHash, customerID, email, m.addressChangeDeadline()); err != nil {
		m.unavailable(w, r, "the address change could not be recorded", err)

		return
	}
	if err := m.opts.AddressProof.SendAddressProof(r.Context(), email, token); err != nil {
		m.unavailable(w, r, "the address change message could not be sent", err)

		return
	}

	corehttp.WriteJSON(r.Context(), w, http.StatusAccepted, nil)
}

// confirmAddressChange moves the account to the address the link was sent to
// (ADR 0377).
//
// It asks for no session: the link may be opened on another device, and
// following it is the proof the change waited for. The token is consumed
// first, in one statement. The customer's record moves before the credential
// does, so a failure between the two leaves the person signing in where they
// always did; another account that took the address meanwhile is told apart
// before anything is written.
//
// It signs nobody in and nobody out: the password did not change, and the
// sessions the person has are theirs at either address.
func (m *Module) confirmAddressChange(w http.ResponseWriter, r *http.Request) {
	var body addressChangeConfirmation
	if !decode(w, r, &body) {
		return
	}

	pending, _ := m.store.(PendingAddresses)
	customerID, email, err := pending.TakeAddressChange(r.Context(), hashRegistrationToken(strings.TrimSpace(body.Token)))
	switch {
	case errors.Is(err, ErrNoAddressChange):
		corehttp.WriteError(r.Context(), w, coreerrors.Invalid(CodeAddressChangeNotUsable,
			"that link is not usable; it may have been used already or expired, so ask again"))

		return
	case err != nil:
		m.unavailable(w, r, "the address change could not be read", err)

		return
	}

	owner, err := m.opts.Accounts.CustomerIDForEmail(r.Context(), email)
	if err != nil {
		m.unavailable(w, r, "whether that address has an account could not be read", err)

		return
	}
	if owner != "" && owner != customerID {
		corehttp.WriteError(r.Context(), w, coreerrors.Conflict(CodeAddressTaken,
			"another account took that address after the link was sent"))

		return
	}

	credentials, _ := m.store.(CustomerCredentials)
	_, hash, err := credentials.CredentialOf(r.Context(), customerID)
	if err != nil {
		m.unavailable(w, r, "the credential could not be read", err)

		return
	}
	accounts, _ := m.opts.Accounts.(AddressChanges)
	switch err := accounts.ChangeAccountEmail(r.Context(), customerID, email); {
	case coreerrors.IsConflict(err):
		// Another account took it between the check above and this write.
		corehttp.WriteError(r.Context(), w, coreerrors.Conflict(CodeAddressTaken,
			"another account took that address after the link was sent"))

		return
	case err != nil:
		m.unavailable(w, r, "the account's record could not be moved to the new address", err)

		return
	}
	if err := m.store.Put(r.Context(), customerID, email, hash); err != nil {
		m.unavailable(w, r, "the credential could not be moved to the new address", err)

		return
	}

	m.log.InfoContext(r.Context(), "identity-session moved an account to a proven address",
		"customer_id", customerID)

	corehttp.WriteJSON(r.Context(), w, http.StatusNoContent, nil)
}

// addressChangeDeadline is when an address change link minted now stops
// working.
func (m *Module) addressChangeDeadline() time.Time {
	ttl := m.opts.AddressChangeTTL
	if ttl <= 0 {
		ttl = DefaultAddressChangeTTL
	}

	return time.Now().UTC().Add(ttl)
}

// The store holds pending address changes (ADR 0377).
var _ PendingAddresses = pgCredentials{}

// PutAddressChange writes a pending change, replacing the customer's own. The
// foreign key refuses a customer with no credential here.
func (s pgCredentials) PutAddressChange(
	ctx context.Context, tokenHash, customerID, email string, expiresAt time.Time,
) error {
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO customer_address_changes (token_hash, customer_id, email, expires_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (customer_id) DO UPDATE
		 SET token_hash = EXCLUDED.token_hash,
		     email = EXCLUDED.email,
		     expires_at = EXCLUDED.expires_at,
		     created_at = now()`,
		tokenHash, customerID, foldEmail(email), expiresAt); err != nil {
		return fmt.Errorf("identity-session: the address change could not be written: %w", err)
	}

	return nil
}

// TakeAddressChange removes a pending change and answers what it held, in
// one statement, for [pgCredentials.TakeRegistration]'s reason.
func (s pgCredentials) TakeAddressChange(
	ctx context.Context, tokenHash string,
) (customerID, email string, err error) {
	row := s.pool.QueryRow(ctx,
		`DELETE FROM customer_address_changes
		 WHERE token_hash = $1 AND expires_at > now()
		 RETURNING customer_id, email`, tokenHash)
	switch err := row.Scan(&customerID, &email); {
	case errors.Is(err, pgx.ErrNoRows):
		return "", "", ErrNoAddressChange
	case err != nil:
		return "", "", fmt.Errorf("identity-session: the address change could not be taken: %w", err)
	}

	return customerID, email, nil
}

// The store answers for pending address changes.
var _ AddressChangeRecords = pgCredentials{}

// EraseAddressChangesTo deletes the pending changes to an address.
func (s pgCredentials) EraseAddressChangesTo(ctx context.Context, email string) (int, error) {
	if email == "" {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM customer_address_changes WHERE email = $1`, email)
	if err != nil {
		return 0, fmt.Errorf("identity-session: the pending address changes could not be erased: %w", err)
	}

	return int(tag.RowsAffected()), nil
}

// PendingAddressChangesOf reads the pending changes a dossier reports. Neither
// the token nor its hash is selected.
func (s pgCredentials) PendingAddressChangesOf(
	ctx context.Context, customerID, email string,
) ([]StoredAddressChange, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT a.customer_id, a.email, a.created_at, a.expires_at
		 FROM customer_address_changes a
		 JOIN customer_credentials c ON c.customer_id = a.customer_id
		 WHERE ($1 <> '' AND a.customer_id = $1)
		    OR ($2 <> '' AND (c.email = $2 OR a.email = $2))
		 ORDER BY a.created_at, a.customer_id`, customerID, email)
	if err != nil {
		return nil, fmt.Errorf("identity-session: the pending address changes could not be read: %w", err)
	}
	defer rows.Close()

	var out []StoredAddressChange
	for rows.Next() {
		var row StoredAddressChange
		if err := rows.Scan(&row.CustomerID, &row.Email, &row.CreatedAt, &row.ExpiresAt); err != nil {
			return nil, fmt.Errorf("identity-session: a pending address change could not be scanned: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity-session: the pending address changes could not be read: %w", err)
	}

	return out, nil
}
