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

// foldEmail is the one place an address is normalised.
//
// The column has a CHECK requiring the folded form, so a caller that skipped
// this would be refused by the database rather than silently create a second
// account — but the refusal would reach an operator as a constraint name, so the
// folding is done here and the CHECK is the floor under it.
func foldEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }
