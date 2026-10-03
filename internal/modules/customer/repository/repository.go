// Package repository is the customer module's database access layer.
//
// The customerdb package that sqlc generates stays INSIDE this package: only
// the [models] domain types are handed out, and pgtype appears in no
// signature. The boundary is deliberate — the service and API layers do not
// bind to storage details, and when the generated code is regenerated only
// this package is affected.
//
// Raw errors do not cross the boundary either: pgx.ErrNoRows and PostgreSQL
// constraint violations are translated here into core/errors' typed errors,
// so the HTTP layer picks the right status code (plan Section 2.7).
package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/repository/customerdb"
)

// Error codes; the caller can look them up with errors.CodeOf.
const (
	// CodeCustomerNotFound reports that the requested customer was not found.
	CodeCustomerNotFound = "customer_not_found"
	// CodeGroupNotFound reports that the requested customer group was not
	// found.
	CodeGroupNotFound = "customer_group_not_found"
	// CodeAddressNotFound reports that the requested address was not found.
	CodeAddressNotFound = "customer_address_not_found"
	// CodeMembershipNotFound reports that the requested group membership was
	// not found.
	CodeMembershipNotFound = "customer_group_membership_not_found"
	// CodeConstraintViolation reports that a database constraint was violated.
	CodeConstraintViolation = "customer_constraint_violation"
	// CodeDuplicate reports a uniqueness violation (e.g. an e-mail already on
	// an account, a second default address for one customer).
	CodeDuplicate = "customer_duplicate"
	// CodeMetadataInvalid reports that the metadata field could not be
	// decoded.
	CodeMetadataInvalid = "customer_metadata_invalid"
	// CodeQueryFailed reports an unexpected database error.
	CodeQueryFailed = "customer_query_failed"
	// CodeCanceled reports that the context was canceled.
	CodeCanceled = "customer_canceled"
	// CodeTxFailed reports that managing the transaction failed.
	CodeTxFailed = "customer_tx_failed"
)

// The names of the partial unique indexes.
//
// The names are used to classify errors: which rule a uniqueness violation
// came from can be read only from the constraint name, and only that way can
// the caller tell "the e-mail is already on an account" from "a second default
// address".
const (
	// IndexAccountEmail is the uniqueness of registered accounts' e-mails.
	IndexAccountEmail = "customer_account_email_uniq"
	// IndexDefaultShipping is the single default shipping address per
	// customer.
	IndexDefaultShipping = "customer_address_default_shipping_uniq"
	// IndexDefaultBilling is the single default billing address per customer.
	IndexDefaultBilling = "customer_address_default_billing_uniq"
	// IndexGroupName is the uniqueness of group names.
	IndexGroupName = "customer_group_name_uniq"
)

// PostgreSQL SQLSTATE codes (the ones this package needs).
const (
	sqlstateCheckViolation       = "23514"
	sqlstateUniqueViolation      = "23505"
	sqlstateForeignKeyViolation  = "23503"
	sqlstateNotNullViolation     = "23502"
	sqlstateStringDataRightTrunc = "22001"
)

// Repo gives access to the customer tables. It is safe for concurrent use.
type Repo struct {
	pool *pgxpool.Pool
	q    *customerdb.Queries
}

// New builds a repository that works over the given pool.
//
// A nil pool is reported as a typed error on the first call, not at setup; the
// setup path does not panic.
func New(pool *pgxpool.Pool) *Repo {
	r := &Repo{pool: pool}
	if pool != nil {
		r.q = customerdb.New(pool)
	}
	return r
}

// ready verifies that the pool can be used.
func (r *Repo) ready() error {
	if r == nil || r.pool == nil || r.q == nil {
		return errors.Unavailable(CodeQueryFailed, "the customer database pool is not set up")
	}
	return nil
}

// inTx runs fn in a single transaction; if fn returns an error the transaction
// is ROLLED BACK.
//
// Atomicity is required for assigning the default address: had an error
// struck between clearing the old default and marking the new one, the
// customer would be left with no default address at all. The transaction
// guarantees that the customer is seen with either the old default or the new
// one.
func (r *Repo) inTx(ctx context.Context, fn func(q *customerdb.Queries) error) error {
	if err := r.ready(); err != nil {
		return err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return wrapDB(err, "the transaction could not be started")
	}
	// Rollback called after Commit returns pgx.ErrTxClosed, which is ignored;
	// that is what lets the defer stay in place safely on the success path too.
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(r.q.WithTx(tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return wrapDB(err, "the transaction could not be completed")
	}
	return nil
}

// wrapDB turns a raw database error into a typed error.
//
// The classification is deliberate: a constraint violation is a CLIENT error
// (422), a uniqueness violation is a conflict (409), a cancellation is a
// temporary unavailability (503); everything else is a server error and its
// message is NOT LEAKED to the client (see core/http).
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
		switch pgErr.Code {
		case sqlstateUniqueViolation:
			return errors.Wrap(err, errors.KindConflict, CodeDuplicate,
				"%s (constraint: %s)", sprintf(format, a...), pgErr.ConstraintName)
		case sqlstateCheckViolation, sqlstateForeignKeyViolation,
			sqlstateNotNullViolation, sqlstateStringDataRightTrunc:
			return errors.Wrap(err, errors.KindInvalid, CodeConstraintViolation,
				"%s (constraint: %s)", sprintf(format, a...), pgErr.ConstraintName)
		}
	}

	return errors.Wrap(err, errors.KindInternal, CodeQueryFailed, format, a...)
}

// ConstraintName returns the database constraint the error came from, or an
// empty string when there is no constraint information.
//
// The service uses it to tell the REASON for a uniqueness violation apart:
// under the same SQLSTATE, "the e-mail is already registered" and "a second
// default address" differ from each other only by the constraint name.
func ConstraintName(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}

// notFoundOr turns pgx.ErrNoRows into NotFound and everything else into
// wrapDB's result.
func notFoundOr(err error, code, format string, a ...any) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.NotFound(code, format, a...)
	}
	return wrapDB(err, format, a...)
}

// sprintf formats the error message once.
//
// A call with no arguments returns the format UNCHANGED; otherwise a percent
// sign in the message would reach the user as garbled text (e.g.
// "%!d(MISSING)").
func sprintf(format string, a ...any) string {
	if len(a) == 0 {
		return format
	}
	return fmt.Sprintf(format, a...)
}

// toInt32 narrows a paging value SAFELY to the int32 the query expects.
//
// A negative value is clamped to zero and a value beyond int32 to the upper
// bound: otherwise the narrowing would silently flip the sign and produce a
// query such as "LIMIT -2147483648". The bounds check is not left to the
// caller's validation; this is the last line of defense.
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

// toTime turns a non-NULL timestamp into a UTC time.Time.
//
// An invalid (NULL) timestamp returns the zero time: on NOT NULL columns that
// cannot happen, and if it ever does, the zero time is a value that does not
// panic and that stands out in a test.
func toTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

// toTimePtr turns a nullable timestamp into a *time.Time.
func toTimePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

// fromTime turns a time into a NOT NULL timestamp; it is always written in
// UTC.
func fromTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

// toMetadata turns the jsonb column into a map.
//
// An empty or JSON null value returns a nil map, so the API response leaves
// the field out altogether instead of carrying "metadata": null (omitempty).
func toMetadata(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeMetadataInvalid,
			"the metadata field could not be decoded")
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// fromMetadata turns the map into the bytes written to the jsonb column.
//
// A nil map becomes the empty object ('{}'): the column is NOT NULL, and in
// storage there is no difference between "no metadata" and "empty metadata".
func fromMetadata(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte("{}"), nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, CodeMetadataInvalid,
			"the metadata field could not be encoded as JSON")
	}
	return raw, nil
}

// patchMetadata builds the metadata parameter for a partial update.
//
// A nil map becomes SQL NULL, and COALESCE, on seeing it, leaves the column AS
// IT IS. A non-empty map, by contrast, is a real write.
func patchMetadata(m map[string]any) ([]byte, error) {
	if m == nil {
		return nil, nil
	}
	return fromMetadata(m)
}
