package identitypasskey

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoCredential is what an unknown passkey answers.
//
// It is one error for "no such credential" and "the row could not be read",
// because a sign-in must not tell a caller which credential ids exist.
var ErrNoCredential = errors.New("identity-passkey: no such credential")

// Credentials is the store this module reads and writes.
//
// It is an interface for [identitysession.Credentials]'s reason: an installation
// keeping its keys somewhere else binds that and keeps the ceremonies.
type Credentials interface {
	// ForCustomer returns every credential a customer registered.
	ForCustomer(ctx context.Context, customerID string) ([]webauthn.Credential, error)
	// ByCredentialID returns one credential and the customer who owns it.
	ByCredentialID(ctx context.Context, credentialID []byte) (customerID string, credential webauthn.Credential, err error)
	// Put stores a credential against a customer.
	Put(ctx context.Context, customerID string, credential webauthn.Credential) error
	// Used stamps the moment a credential signed in.
	//
	// A failure here is NOT a failed sign-in: the stamp is for a person choosing
	// which of their keys to remove, and refusing the session over it would
	// trade an account for a timestamp. The caller logs and carries on.
	Used(ctx context.Context, credentialID []byte) error
}

// pgCredentials is the store on this module's own table.
type pgCredentials struct{ pool *pgxpool.Pool }

// ForCustomer reads every credential the customer registered.
func (s pgCredentials) ForCustomer(
	ctx context.Context, customerID string,
) ([]webauthn.Credential, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT credential FROM passkey_credentials WHERE customer_id = $1 ORDER BY created_at`,
		customerID)
	if err != nil {
		return nil, fmt.Errorf("identity-passkey: the credentials could not be read: %w", err)
	}
	defer rows.Close()

	var out []webauthn.Credential
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("identity-passkey: a credential could not be scanned: %w", err)
		}

		var credential webauthn.Credential
		if err := json.Unmarshal(raw, &credential); err != nil {
			return nil, fmt.Errorf("identity-passkey: a credential could not be decoded: %w", err)
		}
		out = append(out, credential)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity-passkey: the credentials could not be read: %w", err)
	}

	return out, nil
}

// ByCredentialID reads the credential an authenticator presented.
func (s pgCredentials) ByCredentialID(
	ctx context.Context, credentialID []byte,
) (customerID string, credential webauthn.Credential, err error) {
	var raw []byte
	row := s.pool.QueryRow(ctx,
		`SELECT customer_id, credential FROM passkey_credentials WHERE credential_id = $1`,
		encodeCredentialID(credentialID))

	switch err := row.Scan(&customerID, &raw); {
	case errors.Is(err, pgx.ErrNoRows):
		return "", webauthn.Credential{}, ErrNoCredential
	case err != nil:
		return "", webauthn.Credential{}, fmt.Errorf(
			"identity-passkey: the credential could not be read: %w", err)
	}

	if err := json.Unmarshal(raw, &credential); err != nil {
		return "", webauthn.Credential{}, fmt.Errorf(
			"identity-passkey: the credential could not be decoded: %w", err)
	}

	return customerID, credential, nil
}

// Put stores a credential, replacing one with the same id.
//
// The conflict target is the CREDENTIAL: a key registered twice is the same key,
// and a person re-registering one they already had should not end with two rows
// that sign the same challenge.
func (s pgCredentials) Put(
	ctx context.Context, customerID string, credential webauthn.Credential,
) error {
	raw, err := json.Marshal(credential)
	if err != nil {
		return fmt.Errorf("identity-passkey: the credential could not be encoded: %w", err)
	}

	if _, err := s.pool.Exec(ctx,
		`INSERT INTO passkey_credentials (credential_id, customer_id, credential)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (credential_id) DO UPDATE
		 SET customer_id = EXCLUDED.customer_id,
		     credential = EXCLUDED.credential`,
		encodeCredentialID(credential.ID), customerID, raw); err != nil {
		return fmt.Errorf("identity-passkey: the credential could not be written: %w", err)
	}

	return nil
}

// Used stamps the moment.
func (s pgCredentials) Used(ctx context.Context, credentialID []byte) error {
	if _, err := s.pool.Exec(ctx,
		`UPDATE passkey_credentials SET last_used_at = now() WHERE credential_id = $1`,
		encodeCredentialID(credentialID)); err != nil {
		return fmt.Errorf("identity-passkey: the credential's use could not be stamped: %w", err)
	}

	return nil
}

// encodeCredentialID is the one place a credential's raw bytes become a column
// value.
//
// base64url without padding, which is the spelling the browser's own API uses
// for the same bytes — so a value in this column and a value in a WebAuthn JSON
// payload are the same string, and an operator comparing them by eye is not
// comparing two encodings.
func encodeCredentialID(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}
