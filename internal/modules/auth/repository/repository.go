// Package repository is the database access layer of the auth module.
//
// The authdb package that sqlc generates stays INSIDE this package: only the
// [models] domain types are handed outward, and pgtype appears in no signature.
// This boundary is deliberate — the service and API layers do not bind to a
// storage detail, and when the generated code is regenerated only this package
// is affected.
//
// Raw errors do not cross the boundary either: pgx.ErrNoRows and PostgreSQL
// constraint violations are converted here into the typed errors of
// core/errors, so that the HTTP layer picks the status code correctly
// (plan Section 2.7).
//
// # Secrets
//
// This package reads two secret columns: auth_identity.password_hash and
// api_key.token_hash. Both are HASHES, not plain text; even so, neither is EVER
// put into an error message, a log line or a constraint description. The only
// identifier used in error messages is the record id.
package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/repository/authdb"
)

// Error codes; the caller can look them up with errors.CodeOf.
const (
	// CodeUserNotFound reports that the requested user was not found.
	CodeUserNotFound = "auth_user_not_found"
	// CodeIdentityNotFound reports that the requested identity record was not found.
	CodeIdentityNotFound = "auth_identity_not_found"
	// CodeAPIKeyNotFound reports that the requested API key was not found.
	CodeAPIKeyNotFound = "auth_api_key_not_found" //nolint:gosec // G101: not a credential, a constant error CODE
	// CodeSalesChannelNotFound reports that the requested sales channel was not found.
	CodeSalesChannelNotFound = "auth_sales_channel_not_found"
	// CodeAlreadyRevoked reports that the key has already been revoked.
	CodeAlreadyRevoked = "auth_api_key_already_revoked"
	// CodeConstraintViolation reports that a database constraint was violated.
	CodeConstraintViolation = "auth_constraint_violation"
	// CodeDuplicate reports a uniqueness violation (e.g. a registered email).
	CodeDuplicate = "auth_duplicate"
	// CodeEmailTaken reports that the email belongs to another user.
	CodeEmailTaken = "auth_email_taken"
	// CodeChannelNameTaken reports that the channel name is in use.
	CodeChannelNameTaken = "auth_sales_channel_name_taken"
	// CodeMetadataInvalid reports that the metadata field could not be parsed.
	CodeMetadataInvalid = "auth_metadata_invalid"
	// CodeQueryFailed reports an unexpected database error.
	CodeQueryFailed = "auth_query_failed"
	// CodeCanceled reports a context cancellation.
	CodeCanceled = "auth_canceled"
	// CodeTxFailed reports a failure of transaction management.
	CodeTxFailed = "auth_tx_failed"
)

// Names of the unique indexes.
//
// The names are used in error classification: which rule a uniqueness violation
// came from can only be read off the constraint name, and only that way can the
// caller tell "email taken" from "channel name taken".
const (
	// IndexUserEmail is the email uniqueness of live users.
	IndexUserEmail = "auth_user_email_uniq"
	// IndexIdentityProvider is the uniqueness of an identity within a provider.
	IndexIdentityProvider = "auth_identity_provider_uniq"
	// IndexIdentityUserProvider is the ONE identity per user per provider
	// rule.
	IndexIdentityUserProvider = "auth_identity_user_provider_uniq"
	// IndexChannelName is the uniqueness of sales channel names.
	IndexChannelName = "sales_channel_name_uniq"
	// IndexTokenHash is the uniqueness of the API key hash.
	IndexTokenHash = "api_key_token_hash_uniq" //nolint:gosec // G101: not a credential, a database INDEX name
)

// PostgreSQL SQLSTATE codes (the ones we need).
const (
	sqlstateCheckViolation      = "23514"
	sqlstateUniqueViolation     = "23505"
	sqlstateForeignKeyViolation = "23503"
	sqlstateNotNullViolation    = "23502"
)

// sqlstateDataException is the prefix of the "data exception" CLASS (22xxx).
//
// The class is recognized by its PREFIX rather than by individual codes, and
// that is deliberate: the whole class is born from a value the CLIENT sent —
// the text did not fit the column (22001), the value could not be converted to
// the target type (22P02), a NUL escape was put into jsonb (22P05), the text
// carries a byte with no counterpart in the server encoding (22021). Had the
// codes been counted by hand, the list would sooner or later fall short, the
// missing code would land in KindInternal, and a malformed field written by the
// client would produce a 500; the right answer is 422.
const sqlstateDataException = "22"

// Repo provides access to the auth tables. It is safe for concurrent use.
type Repo struct {
	pool *pgxpool.Pool
	q    *authdb.Queries
}

// New produces a repository working on the given pool.
//
// If pool is nil, this is reported as a typed error on the first call rather
// than at construction time; the construction path produces no panic.
func New(pool *pgxpool.Pool) *Repo {
	r := &Repo{pool: pool}
	if pool != nil {
		r.q = authdb.New(pool)
	}
	return r
}

// ready verifies that the pool is usable.
func (r *Repo) ready() error {
	if r == nil || r.pool == nil || r.q == nil {
		return errors.Unavailable(CodeQueryFailed, "auth database pool is not set up")
	}
	return nil
}

// inTx runs fn in a single transaction; if fn returns an error the transaction
// is ROLLED BACK.
//
// Atomicity is mandatory in two places: when the user and the identity record
// are created together (a user without an identity can never log in, and an
// identity without a user is left orphaned) and on an email change (if the user
// row shows the new address while the identity row shows the old one, login
// breaks).
func (r *Repo) inTx(ctx context.Context, fn func(q *authdb.Queries) error) error {
	if err := r.ready(); err != nil {
		return err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrapDB(err, "could not begin transaction")
	}
	// When Rollback is called after Commit it returns pgx.ErrTxClosed and is
	// ignored; this lets the defer stay safely in place on the success path too.
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(r.q.WithTx(tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return wrapDB(err, "could not commit transaction")
	}
	return nil
}

// wrapDB converts a raw database error into a typed error.
//
// The classification is deliberate: a constraint violation and a data exception
// are CLIENT errors (422), a uniqueness violation is a conflict (409), a
// cancellation is temporary unavailability (503); everything else is a server
// error and its message is NOT LEAKED to the client (see core/http).
func wrapDB(err error, format string, a ...any) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return errors.Wrap(err, errors.KindUnavailable, CodeCanceled, format, a...)
	case errors.Is(err, pgx.ErrTxClosed), errors.Is(err, pgx.ErrTxCommitRollback):
		return errors.Wrap(err, errors.KindInternal, CodeTxFailed, format, a...)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		message := withConstraint(sprintf(format, a...), pgErr.ConstraintName)
		switch {
		case pgErr.Code == sqlstateUniqueViolation:
			return errors.Wrap(err, errors.KindConflict, CodeDuplicate, "%s", message)
		case pgErr.Code == sqlstateCheckViolation,
			pgErr.Code == sqlstateForeignKeyViolation,
			pgErr.Code == sqlstateNotNullViolation,
			strings.HasPrefix(pgErr.Code, sqlstateDataException):
			return errors.Wrap(err, errors.KindInvalid, CodeConstraintViolation, "%s", message)
		}
	}

	return errors.Wrap(err, errors.KindInternal, CodeQueryFailed, format, a...)
}

// withConstraint appends the constraint name to the message ONLY when there is
// one.
//
// On data exceptions (22xxx) the constraint name is empty: the error is the
// rejection of a VALUE, not of a rule. Had the name been appended
// unconditionally, the message would end with "… (constraint: )" and whoever
// read the error would go looking for a constraint that does not exist.
func withConstraint(message, constraint string) string {
	if constraint == "" {
		return message
	}
	return fmt.Sprintf("%s (constraint: %s)", message, constraint)
}

// ConstraintName returns which database constraint the error came from; the
// empty string when there is no constraint information.
//
// The service uses this to tell apart the REASON for a uniqueness violation:
// under the same SQLSTATE, "email taken" and "channel name taken" are separated
// by nothing but the constraint name.
func ConstraintName(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}

// notFoundOr converts pgx.ErrNoRows into NotFound and everything else through
// wrapDB.
func notFoundOr(err error, code, format string, a ...any) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.NotFound(code, format, a...)
	}
	return wrapDB(err, format, a...)
}

// sprintf formats the error message exactly once.
//
// On calls without arguments the format is returned UNCHANGED; otherwise a
// percent sign in the message (e.g. "%!d(MISSING)") would reach the user as
// broken text.
func sprintf(format string, a ...any) string {
	if len(a) == 0 {
		return format
	}
	return fmt.Sprintf(format, a...)
}

// toInt32 narrows a pagination value SAFELY to the int32 the query expects.
//
// A negative value is clamped to zero and a value exceeding int32 to the upper
// bound: otherwise the narrowing would silently flip the sign and produce a
// query like "LIMIT -2147483648". The bound check is not left to the caller's
// validation; this is the last line of defense.
func toInt32(n int64) int32 {
	switch {
	case n < 0:
		return 0
	case n > math.MaxInt32:
		return math.MaxInt32
	default:
		return int32(n)
	}
}

// toTime converts a non-NULL timestamp into a UTC time.Time.
//
// An invalid (NULL) timestamp returns the zero time: on NOT NULL columns this
// cannot happen, and if it does, the zero time is a value that produces no
// panic and stands out in a test.
func toTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

// toTimePtr converts a nullable timestamp into a *time.Time.
func toTimePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

// fromTime converts a time into a NOT NULL timestamp; UTC is always written.
func fromTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

// toMetadata converts the jsonb column into a map.
//
// An empty or JSON null value returns a nil map; that way the API response
// shows no field at all instead of "metadata": null (omitempty).
func toMetadata(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeMetadataInvalid,
			"the metadata field could not be parsed")
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// fromMetadata converts the map into the bytes to be written to the jsonb
// column.
//
// A nil map is converted to the empty object ('{}'): the column is NOT NULL and
// in storage there is no difference between "no metadata" and "empty metadata".
func fromMetadata(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, CodeMetadataInvalid,
			"the metadata field could not be converted to JSON")
	}
	return raw, nil
}

// patchMetadata produces the metadata parameter for a partial update.
//
// A nil map is converted to SQL NULL; when COALESCE sees it, it leaves the
// column AS IT IS. A non-empty map, on the other hand, is a real write.
func patchMetadata(m map[string]any) ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	return fromMetadata(m)
}
