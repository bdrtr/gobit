// Package repository is the b2b module's database access layer.
//
// The b2bdb package sqlc generates stays INSIDE this package: only the [models]
// domain types are handed out, and pgtype appears in no signature. The boundary
// is deliberate — the service and API layers do not bind to storage details,
// and when the generated code is regenerated only this package is affected.
//
// Raw errors do not cross the boundary either: pgx.ErrNoRows and PostgreSQL
// constraint violations are turned into core/errors' typed errors here, so the
// HTTP layer picks the right status code (plan Section 2.7).
//
// # The employee's customer bond is NOT here
//
// [models.CompanyEmployee.CustomerID] comes back EMPTY from this layer: the
// bond is owned by core/link and has no corresponding column in the schema.
// The service layer is what fills the field (see
// internal/modules/b2b/service, links.go).
package repository

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/b2b/repository/b2bdb"
)

// Error codes; the calling side can look at these with errors.CodeOf.
const (
	// CodeCompanyNotFound reports that the requested company could not be
	// found.
	CodeCompanyNotFound = "b2b_company_not_found"
	// CodeEmployeeNotFound reports that the requested employee could not be
	// found.
	CodeEmployeeNotFound = "b2b_employee_not_found"
	// CodeConstraintViolation reports that a database constraint was violated.
	CodeConstraintViolation = "b2b_constraint_violation"
	// CodeDuplicate reports a uniqueness violation.
	CodeDuplicate = "b2b_duplicate"
	// CodeQueryFailed reports an unexpected database error.
	CodeQueryFailed = "b2b_query_failed"
	// CodeCanceled reports a context cancellation.
	CodeCanceled = "b2b_canceled"
	// CodeTxFailed reports a failure of transaction management.
	CodeTxFailed = "b2b_tx_failed"
)

// PostgreSQL SQLSTATE codes (the ones this package needs).
const (
	sqlstateCheckViolation       = "23514"
	sqlstateUniqueViolation      = "23505"
	sqlstateForeignKeyViolation  = "23503"
	sqlstateNotNullViolation     = "23502"
	sqlstateStringDataRightTrunc = "22001"
)

// Repo gives access to the b2b tables. It is safe for concurrent use.
type Repo struct {
	pool *pgxpool.Pool
	q    *b2bdb.Queries
}

// New builds a repository that works over the given pool.
//
// A nil pool is reported as a typed error on the first call, not at setup; the
// setup path does not panic.
func New(pool *pgxpool.Pool) *Repo {
	r := &Repo{pool: pool}
	if pool != nil {
		r.q = b2bdb.New(pool)
	}
	return r
}

// ready verifies that the pool can be used.
func (r *Repo) ready() error {
	if r == nil || r.pool == nil || r.q == nil {
		return errors.Unavailable(CodeQueryFailed, "the b2b database pool is not set up")
	}
	return nil
}

// inTx runs fn in a single transaction; if fn returns an error the transaction
// is ROLLED BACK.
//
// Atomicity is required when deleting a company: had an error struck between
// soft-deleting the company and deleting its employees, LIVE employee records
// bound to a deleted company would be left behind, and the storefront would
// show those employees a company that can no longer be read.
func (r *Repo) inTx(ctx context.Context, fn func(q *b2bdb.Queries) error) error {
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

// notFoundOr turns pgx.ErrNoRows into NotFound and everything else into
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

// sprintf formats the error message once.
//
// In calls without arguments the format comes back UNCHANGED; otherwise a
// percent sign in the message would reach the user as garbled text (e.g.
// "%!d(MISSING)").
func sprintf(format string, a ...any) string {
	if len(a) == 0 {
		return format
	}
	return fmt.Sprintf(format, a...)
}

// toInt32 narrows a paging value SAFELY to the int32 the query expects.
//
// A negative value is pulled up to zero and a value above int32 down to the
// upper bound: otherwise the narrowing would silently flip the sign and produce
// a query like "LIMIT -2147483648". The bounds check is not left to the
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
// An invalid (NULL) timestamp returns the zero time: on NOT NULL columns this
// cannot happen, and if it does, the zero time is a value that does not panic
// and stands out in a test.
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

// fromTime turns a time into a NOT NULL timestamp; it is always written as
// UTC.
func fromTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}
