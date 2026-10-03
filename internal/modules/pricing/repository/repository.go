// Package repository is the pricing module's database access layer.
//
// The pricingdb package that sqlc generates stays INSIDE this package: only
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
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/repository/pricingdb"
)

// Error codes; the caller can look at them with errors.CodeOf.
const (
	// CodePriceSetNotFound reports that the requested price set was not found.
	CodePriceSetNotFound = "price_set_not_found"
	// CodePriceNotFound reports that the requested price was not found.
	CodePriceNotFound = "price_not_found"
	// CodePriceListNotFound reports that the requested price list was not
	// found.
	CodePriceListNotFound = "price_list_not_found"
	// CodePriceRuleNotFound reports that the requested price rule was not
	// found.
	CodePriceRuleNotFound = "price_rule_not_found"
	// CodeConstraintViolation reports that a database constraint was violated.
	CodeConstraintViolation = "pricing_constraint_violation"
	// CodeDuplicate reports a uniqueness violation.
	CodeDuplicate = "pricing_duplicate"
	// CodeQueryFailed reports an unexpected database error.
	CodeQueryFailed = "pricing_query_failed"
	// CodeCanceled reports a context cancellation.
	CodeCanceled = "pricing_canceled"
	// CodeTxFailed reports a failure of transaction management.
	CodeTxFailed = "pricing_tx_failed"
	// CodeHistoryUnreadable reports a price history snapshot that could not be
	// encoded or decoded (ADR 0167). The writer and the migration's seed share one
	// JSON shape, so this is a broken contract rather than bad input.
	CodeHistoryUnreadable = "pricing_history_unreadable"
)

// PostgreSQL SQLSTATE codes (the ones needed).
const (
	sqlstateCheckViolation       = "23514"
	sqlstateUniqueViolation      = "23505"
	sqlstateForeignKeyViolation  = "23503"
	sqlstateNotNullViolation     = "23502"
	sqlstateStringDataRightTrunc = "22001"
)

// Repo provides access to the pricing tables. It is safe for concurrent use.
type Repo struct {
	pool *pgxpool.Pool
	q    *pricingdb.Queries
}

// New builds a repository that runs on the given pool.
//
// If pool is nil, that is reported as a typed error on the first call, not at
// setup; the setup path does not panic.
func New(pool *pgxpool.Pool) *Repo {
	r := &Repo{pool: pool}
	if pool != nil {
		r.q = pricingdb.New(pool)
	}
	return r
}

// ready verifies that the pool can be used.
func (r *Repo) ready() error {
	if r == nil || r.pool == nil || r.q == nil {
		return errors.Unavailable(CodeQueryFailed, "the pricing database pool is not set up")
	}
	return nil
}

// inTx runs fn in a single transaction; if fn returns an error the transaction
// is ROLLED BACK.
//
// Atomicity is required for SetPrices: while a price set's prices are written
// in bulk, had an error struck between deleting the old prices and inserting
// the new ones, the container would be left without prices. The transaction
// guarantees that the container is seen with either the old price set or the
// new one.
func (r *Repo) inTx(ctx context.Context, fn func(q *pricingdb.Queries) error) error {
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

// fromTimePtr turns an optional time into a timestamp; nil becomes SQL NULL.
func fromTimePtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return fromTime(*t)
}
