// Package repository is the promotion module's database access layer.
//
// The promotiondb package that sqlc generates stays INSIDE this package: only
// the [models] domain types are handed out, and pgtype appears in no
// signature. The boundary is deliberate — the service and API layers do not
// bind to storage details, and when the generated code is regenerated only
// this package is affected.
//
// Raw errors do not cross the boundary either: pgx.ErrNoRows and PostgreSQL
// constraint violations are translated here into core/errors' typed errors,
// so the HTTP layer picks the right status code (plan Section 2.7).
//
// # Lock order
//
// The redemption flow (Redeem/Release) takes row locks, and the order is THE
// SAME EVERYWHERE: FIRST the promotion, THEN the campaign. When two promotions
// tied to the same campaign are redeemed concurrently, both want the same
// campaign row; only an order fixed this way keeps a deadlock from forming.
package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository/promotiondb"
)

// Error codes; the caller can look at them with errors.CodeOf.
const (
	// CodeCampaignNotFound reports that the requested campaign was not found.
	CodeCampaignNotFound = "campaign_not_found"
	// CodePromotionNotFound reports that the requested promotion was not found.
	CodePromotionNotFound = "promotion_not_found"
	// CodeApplicationMethodNotFound reports that the promotion has no
	// application method.
	CodeApplicationMethodNotFound = "promotion_application_method_not_found"
	// CodePromotionRuleNotFound reports that the requested promotion rule was
	// not found.
	CodePromotionRuleNotFound = "promotion_rule_not_found"
	// CodeUsageLimitReached reports that the promotion's uses have run out.
	CodeUsageLimitReached = "promotion_usage_limit_reached"
	// CodePromotionNotActive reports that the promotion is NOT live; a draft or
	// inactive promotion cannot be redeemed (see [Repo.Redeem]).
	CodePromotionNotActive = "promotion_not_active"
	// CodeCampaignWindowClosed reports that the campaign's date window does NOT
	// COVER the moment of redemption (see [Repo.Redeem]).
	CodeCampaignWindowClosed = "campaign_window_closed"
	// CodeBudgetUnitLocked reports that the budget's UNIT (its type or its
	// currency) cannot be changed while the budget counter is not zero (see
	// [Repo.UpdateCampaign]).
	CodeBudgetUnitLocked = "campaign_budget_unit_locked"
	// CodeBudgetExceeded reports that the campaign budget does not suffice.
	CodeBudgetExceeded = "campaign_budget_exceeded"
	// CodeBudgetCurrencyMismatch reports that the redemption's currency does not
	// match the campaign budget's.
	CodeBudgetCurrencyMismatch = "campaign_budget_currency_mismatch"
	// CodeRedemptionRaced reports that another call got in between during a
	// release; under the row lock it is EXPECTED never to happen (see
	// [Repo.Release]).
	CodeRedemptionRaced = "promotion_redemption_raced"
	// CodeConstraintViolation reports that a database constraint was violated.
	CodeConstraintViolation = "promotion_constraint_violation"
	// CodeDuplicate reports a uniqueness violation.
	CodeDuplicate = "promotion_duplicate"
	// CodeQueryFailed reports an unexpected database error.
	CodeQueryFailed = "promotion_query_failed"
	// CodeCanceled reports a context cancellation.
	CodeCanceled = "promotion_canceled"
	// CodeTxFailed reports a failure of transaction management.
	CodeTxFailed = "promotion_tx_failed"
)

// PostgreSQL SQLSTATE codes (the ones needed).
const (
	sqlstateCheckViolation       = "23514"
	sqlstateUniqueViolation      = "23505"
	sqlstateForeignKeyViolation  = "23503"
	sqlstateNotNullViolation     = "23502"
	sqlstateStringDataRightTrunc = "22001"
)

// emptyJSONObject is an empty metadata body; nil is never written to the NOT
// NULL column.
var emptyJSONObject = []byte(`{}`)

// Repo provides access to the promotion tables. It is safe for concurrent use.
type Repo struct {
	pool *pgxpool.Pool
	q    *promotiondb.Queries
}

// New builds a repository that runs on the given pool.
//
// If pool is nil, that is reported as a typed error on the first call, not at
// setup; the setup path does not panic.
func New(pool *pgxpool.Pool) *Repo {
	r := &Repo{pool: pool}
	if pool != nil {
		r.q = promotiondb.New(pool)
	}
	return r
}

// ready verifies that the pool can be used.
func (r *Repo) ready() error {
	if r == nil || r.pool == nil || r.q == nil {
		return errors.Unavailable(CodeQueryFailed, "the promotion database pool is not set up")
	}
	return nil
}

// inTx runs fn in a single transaction; if fn returns an error the transaction
// is ROLLED BACK.
//
// It is MANDATORY for the redemption flow: a row lock is held only for the
// length of a transaction, and the counter and the ledger (promotion_redemption)
// are either written TOGETHER or not at all. Otherwise a promotion whose
// counter went up but which has no ledger entry would leave behind a
// redemption that cannot be released.
func (r *Repo) inTx(ctx context.Context, fn func(q *promotiondb.Queries) error) error {
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

// deref turns a string pointer into its value; nil becomes the empty string.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// nilIfEmpty turns the empty string into SQL NULL.
//
// The difference between the empty string and NULL is meaningful in this
// schema: a budget's currency exists only for the "spend" type and MUST BE
// NULL for the other types (see campaign_budget_currency_check in the
// migration). Had the empty string been written, the constraint would have
// refused it.
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// copyInt64 returns an integer pointer by COPYING it.
//
// The copy is required: had the generated row's pointer been handed straight
// to the domain model, a caller changing the model would also have affected
// the buffer on the repository's side.
func copyInt64(v *int64) *int64 {
	if v == nil {
		return nil
	}
	out := *v
	return &out
}

// encodeMetadata turns the metadata map into a JSONB body.
//
// An empty or nil map writes `{}`: the column is NOT NULL, and a JSON null
// would have hit the jsonb_typeof constraint.
func encodeMetadata(md map[string]string) ([]byte, error) {
	if len(md) == 0 {
		return emptyJSONObject, nil
	}
	raw, err := json.Marshal(md)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, CodeConstraintViolation,
			"the promotion metadata could not be encoded as JSON")
	}
	return raw, nil
}

// decodeMetadata turns a JSONB body into the metadata map.
//
// A body that cannot be decoded returns an EMPTY map, NOT an error: metadata
// takes no part in a business rule, and the whole promotion becoming
// unreadable because of one hand-written broken record would have brought the
// computation down entirely.
func decodeMetadata(raw []byte) map[string]string {
	if len(raw) == 0 {
		return map[string]string{}
	}
	out := map[string]string{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]string{}
	}
	return out
}
