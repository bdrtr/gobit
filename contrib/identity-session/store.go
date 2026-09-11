package identitysession

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Credentials is the store a sign-in reads.
//
// It is an interface so an installation keeping its passwords somewhere else —
// an LDAP directory, an existing users table — binds that instead of this
// module's table without reimplementing the session half.
type Credentials interface {
	// Credential returns the customer and the stored hash for an e-mail.
	//
	// A missing e-mail returns [ErrPasswordMismatch] and NOT a not-found error:
	// the sign-in endpoint must answer the same way for an unknown address and a
	// wrong password, or it tells an attacker which addresses have accounts.
	Credential(ctx context.Context, email string) (customerID, passwordHash string, err error)
	// Put writes or replaces a customer's credential.
	Put(ctx context.Context, customerID, email, passwordHash string) error
}

// ErrPasswordUnknown is what [Module.HasPassword] answers when the bound store
// cannot say.
//
// It is a NAMED error and not a false, because the two mean opposite things to a
// caller deciding whether somebody has another way into their account: "no
// password" is an answer and "I could not look" is a question. A caller folding
// the second into the first removes a person's last passkey on the strength of a
// failed query.
var ErrPasswordUnknown = errors.New(
	"identity-session: whether that customer has a password could not be determined")

// PasswordLookup is the OPTIONAL capability a credential store may offer.
//
// It answers ONE question — does this customer have a password here — and it is
// optional for the reason [Credentials] is an interface at all: an installation
// binds an LDAP directory or an existing users table, and adding a third method
// to that interface would compile-break every one of them for a feature in a
// different Go module. A store that does not implement this is not broken; it is
// a store that cannot answer, and [Module.HasPassword] says so.
type PasswordLookup interface {
	// HasPassword reports whether the customer has a password in this store.
	HasPassword(ctx context.Context, customerID string) (bool, error)
}

// HasPassword answers whether a customer can sign in here with a password.
//
// # Why this module answers it and does not know who is asking
//
// A caller deciding something about a person's ways into their account needs
// this fact and cannot have this module's table. What it gets is the fact, with
// no opinion about what the fact means: whether a password COUNTS as a way in is
// the installation's judgment, made where the modules are assembled, and this
// module never learns the word the caller uses for the other ways.
//
// It returns [ErrPasswordUnknown] when the bound store does not implement
// [PasswordLookup], rather than false: see that error.
func (m *Module) HasPassword(ctx context.Context, customerID string) (bool, error) {
	if m.store == nil {
		return false, fmt.Errorf("%w: the module has not registered", ErrPasswordUnknown)
	}

	lookup, ok := m.store.(PasswordLookup)
	if !ok {
		return false, fmt.Errorf(
			"%w: the bound credential store does not implement identitysession.PasswordLookup",
			ErrPasswordUnknown)
	}

	return lookup.HasPassword(ctx, customerID)
}

// pgCredentials is the store on this module's own table.
type pgCredentials struct{ pool *pgxpool.Pool }

// Credential reads by folded e-mail.
func (s pgCredentials) Credential(
	ctx context.Context, email string,
) (customerID, passwordHash string, err error) {
	row := s.pool.QueryRow(ctx,
		`SELECT customer_id, password_hash FROM customer_credentials WHERE email = $1`,
		foldEmail(email))

	switch err := row.Scan(&customerID, &passwordHash); {
	case errors.Is(err, pgx.ErrNoRows):
		// The same answer a wrong password gets. See [Credentials.Credential].
		return "", "", ErrPasswordMismatch
	case err != nil:
		return "", "", fmt.Errorf("identity-session: the credential could not be read: %w", err)
	}

	return customerID, passwordHash, nil
}

// Put writes the credential, replacing whatever the customer had.
//
// The conflict target is the CUSTOMER and not the e-mail: a person changing
// their address keeps their account, and the unique index on the address is what
// refuses handing one address to two customers.
func (s pgCredentials) Put(ctx context.Context, customerID, email, passwordHash string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO customer_credentials (customer_id, email, password_hash)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (customer_id) DO UPDATE
		 SET email = EXCLUDED.email,
		     password_hash = EXCLUDED.password_hash,
		     updated_at = now()`,
		customerID, foldEmail(email), passwordHash)
	if err != nil {
		return fmt.Errorf("identity-session: the credential could not be written: %w", err)
	}

	return nil
}

// HasPassword reports whether the customer has a credential here.
//
// It is a primary key probe: customer_id is the table's PRIMARY KEY, so this is
// an index hit and needs no column, no index and no migration of its own. It
// reads no hash — what the caller asked is whether a password EXISTS.
func (s pgCredentials) HasPassword(ctx context.Context, customerID string) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM customer_credentials WHERE customer_id = $1)`,
		customerID).Scan(&exists); err != nil {
		// BOTH are wrapped. The sentinel is what the passkey module matches on with
		// errors.Is, and the database error underneath is what an operator needs to
		// tell a missing table from a refused connection — dropping it to %v would
		// print it once and make it unmatchable.
		return false, fmt.Errorf("%w: %w", ErrPasswordUnknown, err)
	}

	return exists, nil
}

// The optional capability is satisfied at compile time; a drifted signature would
// otherwise cost nothing in the build and turn every answer into
// [ErrPasswordUnknown] at run time.
var _ PasswordLookup = pgCredentials{}

// foldEmail is the one place an address is normalised.
//
// The column has a CHECK requiring the folded form, so a caller that skipped
// this would be refused by the database rather than silently create a second
// account — but the refusal would reach an operator as a constraint name, so the
// folding is done here and the CHECK is the floor under it.
func foldEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// EraseCredentialsOf deletes a person's credential row.
//
// Either handle finds it and BOTH are applied when both are given, because a
// subject carrying a customer id and an address is one person and this module
// keys on both: the id is the primary key and the address is unique. An OR is
// what makes a subject assembled from two sources — an admin's customer id and
// the address the person wrote in their request — erase the row either of them
// names rather than only the row both do.
func (s pgCredentials) EraseCredentialsOf(
	ctx context.Context, customerID, email string,
) (int, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM customer_credentials
		 WHERE ($1 <> '' AND customer_id = $1) OR ($2 <> '' AND email = $2)`,
		customerID, email)
	if err != nil {
		return 0, fmt.Errorf("identity-session: the credentials could not be erased: %w", err)
	}

	return int(tag.RowsAffected()), nil
}

// CredentialRecordsOf reads what is held about a customer or an address.
//
// The hash column is NOT selected. A value that is never going to be reported
// should not travel out of the database either — the dossier says a password is
// set and does not reproduce it, and reading the hash into memory to then drop it
// would be the same secret in one more place for no gain.
func (s pgCredentials) CredentialRecordsOf(
	ctx context.Context, customerID, email string,
) ([]StoredCredential, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT customer_id, email, created_at, updated_at
		 FROM customer_credentials
		 WHERE ($1 <> '' AND customer_id = $1) OR ($2 <> '' AND email = $2)
		 ORDER BY created_at, customer_id`, customerID, email)
	if err != nil {
		return nil, fmt.Errorf("identity-session: the credentials could not be read: %w", err)
	}
	defer rows.Close()

	var out []StoredCredential
	for rows.Next() {
		var row StoredCredential
		if err := rows.Scan(&row.CustomerID, &row.Email,
			&row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("identity-session: a credential could not be scanned: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity-session: the credentials could not be read: %w", err)
	}

	return out, nil
}
