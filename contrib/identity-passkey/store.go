package identitypasskey

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrLastWayIn is a removal that would leave the account with no way in.
//
// It is this module's own error and not a store failure: the store refused
// because the rule refused, and a caller has to tell that from a database that
// is down — one is a sentence for the person and the other is a 500.
var ErrLastWayIn = errors.New("identity-passkey: that is the last way into the account")

// ErrNoCredential is what an unknown passkey answers.
//
// It is one error for "no such credential" and "the row could not be read",
// because a sign-in must not tell a caller which credential ids exist.
var ErrNoCredential = errors.New("identity-passkey: no such credential")

// Credentials is the store this module reads and writes.
//
// It is an interface for [identitysession.Credentials]'s reason: an installation
// keeping its keys somewhere else binds that and keeps the ceremonies.
//
// # A store speaks for ONE relying party, and that is part of the contract
//
// A passkey is scoped to an RP ID by the authenticator that minted it: a
// credential created for one relying party is not offered when the browser is
// asked for another. None of the methods here takes an RP ID, and that is
// deliberate — the alternative was six signatures carrying a value that never
// changes for the life of a store. So an implementation binds the relying party
// it is constructed with and must not answer with credentials of any other.
//
// Getting this wrong is not a cosmetic fault. The rule that refuses to remove
// somebody's last way in counts what this store reports, so a store that reports
// credentials of an abandoned relying party reports ways in that are not, and the
// guard permits the removal it exists to refuse (gap D68).
//
// The stored credential cannot be filtered after the fact: the library writes no
// attestation bytes into it, so the relying party is not recoverable from the
// value. Whatever stores it has to store that too.
type Credentials interface {
	// ForCustomer returns every credential a customer registered.
	ForCustomer(ctx context.Context, customerID string) ([]webauthn.Credential, error)
	// ByCredentialID returns one credential and the customer who owns it.
	ByCredentialID(ctx context.Context, credentialID []byte) (customerID string, credential webauthn.Credential, err error)
	// Put stores a credential against a customer.
	Put(ctx context.Context, customerID string, credential webauthn.Credential) error
	// ListForCustomer returns what a person may be shown about their own keys.
	//
	// It returns a NARROW row and never a webauthn.Credential, so a handler
	// cannot publish what it was never handed: the public key, the sign
	// counter, the attestation and the AAGUID are all in the credential and
	// none of them is a person's business.
	ListForCustomer(ctx context.Context, customerID string) ([]Key, error)
	// Remove removes a credential, refusing the customer's LAST way in unless the
	// caller says another way in exists. A way in is a row that is not suspended:
	// a suspended row signs nobody in (ADR 0382).
	//
	// allowLast is a decided bool and not a question this store asks, and that
	// placement is deliberate: the caller asks whatever it has to ask BEFORE the
	// transaction opens, because a cross-module query made while this
	// transaction holds a row lock would be a second connection taken from the
	// same pool — and enough concurrent removals would then all hold one and all
	// wait for another.
	//
	// The guard is still applied under the lock, so a stale bool cannot widen it:
	// a suspended target is removed whatever the bool says; otherwise, with two
	// unsuspended rows held the count permits the removal, and with fewer it is
	// the bool that decides.
	//
	// It answers how many of that customer's rows SURVIVE, suspended ones
	// included, so a caller can tell "removed" from "refused" without a second
	// query.
	Remove(ctx context.Context, customerID string, credentialID []byte, allowLast bool) (surviving int, err error)
	// SignedIn records a sign-in the library accepted, and is the check that the
	// key was not copied (ADR 0382). The session is issued only when it answers nil.
	//
	// Under a lock on the key's row it compares the assertion's count with the
	// stored one. A higher count, or zero over zero (a key that does not count),
	// is recorded with the backup state, the latched user verification and the
	// moment. A key whose stored credential is backup-eligible is never refused
	// over its count, since it may live on several devices by design, and its
	// stored count only moves up.
	//
	// Otherwise the same count, recorded less than twice [CeremonyTTL] ago, answers
	// [ErrCountRepeated] and writes nothing; anything else marks the key suspended
	// and answers [ErrKeyCopied]. A suspended key answers [ErrKeySuspended], and a
	// key this store does not hold for its relying party under that public key
	// answers [ErrNoCredential].
	//
	// Any other error refuses the sign-in: a count that could not be checked was
	// not checked.
	SignedIn(ctx context.Context, assertion Assertion) error
}

// Assertion is what one sign-in the library accepted says about the key that
// signed it, read from that assertion's authenticator data (ADR 0382).
type Assertion struct {
	// CredentialID is the key that signed.
	CredentialID []byte
	// PublicKey is the key the signature was verified with. A store records the
	// assertion only on a row that still holds it, so a key replaced under its id
	// during the ceremony is not credited with the old key's count.
	PublicKey []byte
	// SignCount is the counter THIS assertion carried, not the library's record of
	// it: when the count does not advance the library keeps the stored one.
	SignCount uint32
	// BackupState is whether the key reported itself synced at this sign-in.
	BackupState bool
	// UserVerified is whether this ceremony verified the person. A store latches
	// it: a key that verified its person once stays recorded so.
	UserVerified bool
}

// ErrKeySuspended is a key that signs nobody in: its signature counter failed to
// advance, at this sign-in or an earlier one (ADR 0382). It stays so until the
// key is removed.
var ErrKeySuspended = errors.New("identity-passkey: that key is suspended")

// ErrKeyCopied is [ErrKeySuspended] answered by the sign-in that suspended it.
var ErrKeyCopied = fmt.Errorf("%w: its signature counter did not advance", ErrKeySuspended)

// ErrCountRepeated is the count last recorded for a key, sent again moments
// after: a finish whose answer was lost. Nothing is written.
var ErrCountRepeated = errors.New("identity-passkey: that signature count was just recorded")

// pgCredentials is the store on this module's own table.
type pgCredentials struct {
	pool *pgxpool.Pool
	// rpID is the relying party this store speaks about, and the ONLY one.
	//
	// A passkey is scoped to an RP ID by the authenticator that minted it, and the
	// stored credential does not carry that id — measured: the library writes no
	// attestation bytes into it, so there is nothing to read the relying party back
	// out of. It has to be a column, and it has to be applied by whatever holds
	// the configured value.
	//
	// So every read and every write here is scoped by it, and a row belonging to
	// another relying party is invisible to this module: not listed, not counted as
	// a way in, and not accepted at sign-in.
	rpID string
}

// rpScope is the SQL that limits a statement to this store's relying party.
//
// `rp_id IS NULL` is a row written before the column existed, and it is read as
// belonging to the configured relying party — which it does, unless the
// installation had already moved, and in that case nothing recorded what it was.
// Spelling it once means a query cannot be given the customer filter and miss
// this one.
const rpScope = ` AND (rp_id IS NULL OR rp_id = `

// ForCustomer reads every credential the customer registered.
func (s pgCredentials) ForCustomer(
	ctx context.Context, customerID string,
) ([]webauthn.Credential, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT credential FROM passkey_credentials
		 WHERE customer_id = $1`+rpScope+`$2)
		 ORDER BY created_at`,
		customerID, s.rpID)
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
		`SELECT customer_id, credential FROM passkey_credentials
		 WHERE credential_id = $1`+rpScope+`$2)`,
		encodeCredentialID(credentialID), s.rpID)

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

// ErrCredentialBelongsToAnother is a registration whose credential id is already
// somebody else's.
var ErrCredentialBelongsToAnother = errors.New(
	"identity-passkey: that credential belongs to another customer")

// Put stores a credential, replacing one THAT CUSTOMER already had.
//
// The conflict target is the CREDENTIAL: a key registered twice is the same key,
// and a person re-registering one they already had should not end with two rows
// that sign the same challenge.
//
// # Why the update is scoped to the same owner
//
// Until this scope existed the update set customer_id from the incoming row, so
// a registration carrying an id that was already somebody else's MOVED their
// credential onto the registering account — silently, with a 204. A credential
// id comes from the client, so it is chosen by whoever controls the
// authenticator; a hostile one can present any id it likes.
//
// Scoped, the conflict matches nothing for a foreign id and the insert fails on
// the primary key instead, which this method turns into
// [ErrCredentialBelongsToAnother] rather than a 500 (gap D64).
func (s pgCredentials) Put(
	ctx context.Context, customerID string, credential webauthn.Credential,
) error {
	raw, err := json.Marshal(credential)
	if err != nil {
		return fmt.Errorf("identity-passkey: the credential could not be encoded: %w", err)
	}

	tag, err := s.pool.Exec(ctx,
		`INSERT INTO passkey_credentials (credential_id, customer_id, credential, rp_id)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (credential_id) DO UPDATE
		 SET credential = EXCLUDED.credential, rp_id = EXCLUDED.rp_id
		 WHERE passkey_credentials.customer_id = EXCLUDED.customer_id`,
		encodeCredentialID(credential.ID), customerID, raw, s.rpID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
			return ErrCredentialBelongsToAnother
		}

		return fmt.Errorf("identity-passkey: the credential could not be written: %w", err)
	}
	// A conflict whose WHERE did not match updates NOTHING and reports no error.
	// Inferring success from the absence of an error is what would let a foreign
	// id answer 204 having written nothing at all.
	if tag.RowsAffected() == 0 {
		return ErrCredentialBelongsToAnother
	}

	return nil
}

// uniqueViolation is PostgreSQL's code for a primary key collision.
const uniqueViolation = "23505"

// repeatWindow is how long after a count is recorded the same count again is a
// repeat and not a copy. A finish arrives at most [CeremonyTTL] after its begin
// on the application's clock; twice that absorbs a step of it, and both ends of
// the comparison are the database's.
const repeatWindow = 2 * CeremonyTTL

// SignedIn compares the count under the row's lock and records it, or suspends
// the key.
//
// The count stays inside the credential's JSON, where the library reads it, and
// it is merged in with `||` rather than `jsonb_set`, which returns the row
// unchanged when a parent key is missing. `signCount` is absent from the JSON
// at zero, so every read of it defaults. Every moment is `clock_timestamp()`,
// the write's own moment under the lock (ADR 0241). [pgCredentials.Remove]
// locks all of a customer's rows and this locks one and waits on nothing else,
// so the two cannot deadlock.
func (s pgCredentials) SignedIn(ctx context.Context, a Assertion) error {
	// The spelling Put stored: the library's JSON carries the key as base64.
	publicKey, err := json.Marshal(a.PublicKey)
	if err != nil {
		return fmt.Errorf("identity-passkey: the key could not be encoded: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("identity-passkey: the sign-in could not be started: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	id := encodeCredentialID(a.CredentialID)
	var stored int64
	var eligible, verified, suspended, recent bool
	err = tx.QueryRow(ctx,
		`SELECT COALESCE((credential #>> '{authenticator,signCount}')::bigint, 0),
		        COALESCE((credential #>> '{flags,backupEligible}')::boolean, false),
		        COALESCE((credential #>> '{flags,userVerified}')::boolean, false),
		        suspended_at IS NOT NULL,
		        COALESCE(last_used_at, created_at) > clock_timestamp() - make_interval(secs => $4)
		 FROM passkey_credentials
		 WHERE credential_id = $1`+rpScope+`$2) AND credential->'publicKey' = $3::jsonb
		 FOR UPDATE`,
		id, s.rpID, string(publicKey), repeatWindow.Seconds()).
		Scan(&stored, &eligible, &verified, &suspended, &recent)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNoCredential
	case err != nil:
		return fmt.Errorf("identity-passkey: the key could not be locked: %w", err)
	case suspended:
		return ErrKeySuspended
	}

	// The row is held from here to the commit, so each write below lands on it:
	// a removal or a Put of the same id waits for the lock.
	presented := int64(a.SignCount)
	switch {
	case presented > stored || (presented == 0 && stored == 0) || eligible:
		_, err := tx.Exec(ctx,
			`UPDATE passkey_credentials
			 SET credential = credential || jsonb_build_object(
			       'authenticator', COALESCE(credential->'authenticator', '{}'::jsonb)
			           || jsonb_build_object('signCount', $3::bigint),
			       'flags', COALESCE(credential->'flags', '{}'::jsonb)
			           || jsonb_build_object('backupState', $4::boolean, 'userVerified', $5::boolean)),
			     last_used_at = clock_timestamp()
			 WHERE credential_id = $1`+rpScope+`$2)`,
			id, s.rpID, max(stored, presented), a.BackupState, verified || a.UserVerified)
		if err != nil {
			return fmt.Errorf("identity-passkey: the sign-in could not be recorded: %w", err)
		}
	case presented == stored && recent:
		return ErrCountRepeated
	default:
		_, err := tx.Exec(ctx,
			`UPDATE passkey_credentials SET suspended_at = clock_timestamp()
			 WHERE credential_id = $1`+rpScope+`$2)`, id, s.rpID)
		if err != nil {
			return fmt.Errorf("identity-passkey: the key could not be suspended: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("identity-passkey: the suspension could not be committed: %w", err)
		}

		return ErrKeyCopied
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("identity-passkey: the sign-in could not be committed: %w", err)
	}

	return nil
}

// decodeCredentialID turns a path segment back into the bytes the store keys on.
//
// A segment that is not base64url is not a different answer from a segment that
// is somebody else's: both are ids this caller has no key for, and the handler
// treats them the same.
func decodeCredentialID(encoded string) ([]byte, error) {
	trimmed := strings.TrimSpace(encoded)

	raw, err := base64.RawURLEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("identity-passkey: the credential id is not base64url: %w", err)
	}

	// An encoding that does not come back as itself is REFUSED, and this is not
	// pedantry about spare bits.
	//
	// Go's base64 decoder does not require the trailing bits of an unpadded group
	// to be zero, so "b25sea" and "b25seQ" both decode to the same four bytes —
	// measured, not assumed. Accepting both gives one key two names, and the
	// listing only ever issues one of them. Nothing today keys on the string form,
	// which is exactly why this is worth closing now: an audit line, a cache key
	// or a rate limit written later would count two names as two subjects, and
	// that defect would be nowhere near this function.
	if base64.RawURLEncoding.EncodeToString(raw) != trimmed {
		return nil, fmt.Errorf(
			"identity-passkey: the credential id is not the canonical base64url of "+
				"any credential: %q", trimmed)
	}

	return raw, nil
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

// Key is what a person may be told about one of their own passkeys.
//
// # Why it is a type of its own
//
// Everything a credential carries beyond this is either theirs to know and
// useless (the public key), a signal about their hardware (the AAGUID, the
// attestation), or a clone-detection counter that means nothing out of context.
// A narrow row is how the handler is kept from publishing them: it cannot leak
// what the store never handed it (ADR 0130).
type Key struct {
	// ID is the credential's identifier, base64url — the only thing a removal
	// can name.
	ID string
	// CreatedAt is when the key was registered.
	CreatedAt time.Time
	// LastUsedAt is the last sign-in. A sign-in whose record cannot be written
	// is refused (ADR 0382), so it does not lag.
	LastUsedAt *time.Time
	// SuspendedAt is when the key signed with a signature counter that did not
	// advance, and nil for a key that signs in (ADR 0382). A suspended key signs
	// nobody in, is always removable, and is not a way into the account.
	SuspendedAt *time.Time
	// Transports is how the authenticator said it can be reached.
	//
	// It is frozen at registration, and that is why it is publishable while the
	// backup flags are not: how a credential was MADE does not change, and
	// whether it is currently synced does.
	Transports []string
}

// ListForCustomer reads the person's keys, oldest first.
//
// Oldest first because the list is read by somebody deciding which key to
// remove, and the order they registered them in is the only order they remember.
func (s pgCredentials) ListForCustomer(
	ctx context.Context, customerID string,
) ([]Key, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT credential_id, created_at, last_used_at, suspended_at,
		        COALESCE(credential->'transport', '[]'::jsonb)
		 FROM passkey_credentials
		 WHERE customer_id = $1`+rpScope+`$2)
		 ORDER BY created_at, credential_id`, customerID, s.rpID)
	if err != nil {
		return nil, fmt.Errorf("identity-passkey: the keys could not be listed: %w", err)
	}
	defer rows.Close()

	var out []Key
	for rows.Next() {
		var key Key
		var transports []byte
		if err := rows.Scan(&key.ID, &key.CreatedAt, &key.LastUsedAt, &key.SuspendedAt, &transports); err != nil {
			return nil, fmt.Errorf("identity-passkey: a key could not be scanned: %w", err)
		}
		if err := json.Unmarshal(transports, &key.Transports); err != nil {
			// A credential whose transports are unreadable is still a key the
			// person has, and hiding it would hide a way in. The field is
			// dropped; the row is not.
			key.Transports = nil
		}
		out = append(out, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity-passkey: the keys could not be listed: %w", err)
	}

	return out, nil
}

// Remove removes a credential, refusing the last way in unless the caller permits
// it.
//
// # Why the lock, and why it is this lock
//
// Measured on a real PostgreSQL: with the guard written inside the DELETE as a
// subquery, two concurrent removals of two DIFFERENT keys both see two rows and
// both delete, leaving the account with NONE — six runs out of six. Under READ
// COMMITTED each statement takes its own snapshot and the two DELETEs touch
// different rows, so nothing makes them wait for each other.
//
// `SELECT … WHERE customer_id = $1 FOR UPDATE` makes them wait: the second
// transaction blocks on rows the first holds and, when it proceeds, re-reads
// them — the row the first deleted is simply gone, so its count is one and its
// guard refuses. An advisory lock was measured to work identically and was
// refused: this repository keys those with a class number whose registry lives in
// the main module, and a contrib module claiming one would be coordinating across
// a module boundary for something a row lock already does.
//
// The count returned is of ROWS, which is not the same as ways in — a person can
// hold two credentials on one authenticator, because nothing stops an
// authenticator from minting a second one for a site it already has a key for
// once the exclusion list is satisfied by a different device. The record says so.
//
// The guard counts ways in all the same where it can: a suspended row signs
// nobody in, so it is not counted, and removing one is never refused (ADR 0382).
func (s pgCredentials) Remove(
	ctx context.Context, customerID string, credentialID []byte, allowLast bool,
) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("identity-passkey: the removal could not be started: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx,
		`SELECT credential_id, suspended_at IS NOT NULL FROM passkey_credentials
		 WHERE customer_id = $1`+rpScope+`$2)
		 FOR UPDATE`,
		customerID, s.rpID)
	if err != nil {
		return 0, fmt.Errorf("identity-passkey: the keys could not be locked: %w", err)
	}

	held, waysIn := 0, 0
	mine, targetSuspended := false, false
	target := encodeCredentialID(credentialID)
	for rows.Next() {
		var id string
		var suspended bool
		if err := rows.Scan(&id, &suspended); err != nil {
			rows.Close()

			return 0, fmt.Errorf("identity-passkey: a locked key could not be read: %w", err)
		}
		held++
		if !suspended {
			waysIn++
		}
		if id == target {
			mine, targetSuspended = true, suspended
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("identity-passkey: the keys could not be locked: %w", err)
	}

	// Not theirs, never existed, or already gone — one answer, because telling
	// them apart tells a caller which credential ids exist.
	if !mine {
		return held, ErrNoCredential
	}
	if !targetSuspended && waysIn < 2 && !allowLast {
		return held, ErrLastWayIn
	}

	tag, err := tx.Exec(ctx,
		`DELETE FROM passkey_credentials
		 WHERE credential_id = $1 AND customer_id = $2`+rpScope+`$3)`,
		target, customerID, s.rpID)
	if err != nil {
		return 0, fmt.Errorf("identity-passkey: the key could not be removed: %w", err)
	}
	// Success is not inferred from the absence of an error: a DELETE matching
	// nothing reports nothing, and the caller would tell somebody their stolen
	// key is gone while it still signs in.
	if tag.RowsAffected() == 0 {
		return held, ErrNoCredential
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("identity-passkey: the removal could not be committed: %w", err)
	}

	return held - 1, nil
}

// ErasePasskeysOf deletes every credential of a customer.
//
// # Not scoped by the relying party, on purpose
//
// Every other statement in this store is scoped by [pgCredentials.rpID], because
// every other question is "which keys can sign this person in". This one is "what
// is held about her", and a row left behind by an abandoned relying party is held
// about her. Scoping it would leave somebody who asked to be forgotten with rows
// on disk and a report saying they were deleted.
//
// It takes no row lock and applies no last-way-in rule. That guard defends a
// person keeping their account; an erasure is that person asking for the account
// to stop existing, and refusing it over the guard would refuse the erasure.
func (s pgCredentials) ErasePasskeysOf(ctx context.Context, customerID string) (int, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM passkey_credentials WHERE customer_id = $1`, customerID)
	if err != nil {
		return 0, fmt.Errorf("identity-passkey: the passkeys could not be erased: %w", err)
	}

	return int(tag.RowsAffected()), nil
}

// PasskeyRecordsOf reads every credential of a customer for a disclosure.
//
// Unscoped for [pgCredentials.ErasePasskeysOf]'s reason, and it reads the whole
// stored credential rather than the narrow row a listing takes: a dossier that
// withheld a column would be false about what is held.
func (s pgCredentials) PasskeyRecordsOf(
	ctx context.Context, customerID string,
) ([]StoredKey, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT credential_id, rp_id, created_at, last_used_at, suspended_at, credential::text
		 FROM passkey_credentials
		 WHERE customer_id = $1
		 ORDER BY created_at, credential_id`, customerID)
	if err != nil {
		return nil, fmt.Errorf("identity-passkey: the passkeys could not be read: %w", err)
	}
	defer rows.Close()

	var out []StoredKey
	for rows.Next() {
		var key StoredKey
		var rpID *string
		if err := rows.Scan(&key.CredentialID, &rpID, &key.CreatedAt,
			&key.LastUsedAt, &key.SuspendedAt, &key.Credential); err != nil {
			return nil, fmt.Errorf("identity-passkey: a passkey could not be scanned: %w", err)
		}
		if rpID != nil {
			key.RelyingPartyID = *rpID
		}
		out = append(out, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("identity-passkey: the passkeys could not be read: %w", err)
	}

	return out, nil
}
