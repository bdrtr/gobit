package repository

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/repository/paymentdb"
)

// The two tables store credit is kept in (ADR 0152).
//
// The LEDGER is the module's own money record: the service writes credit into it
// and answers a balance out of it. The SESSIONS belong to the store-credit
// PROVIDER, exactly as payment_manual_sessions belong to the manual one — the
// service never touches them.

// AppendStoreCreditEntry adds one event to the ledger.
//
// There is no update and no delete: because the balance is the sum of the rows, a
// correction is A NEW ROW — the module's rule since migration 000003, "a money
// record is kept".
func (r *Repository) AppendStoreCreditEntry(
	ctx context.Context, entry models.StoreCreditEntry,
) (models.StoreCreditEntry, error) {
	row, err := r.queries(ctx).InsertStoreCreditEntry(ctx, paymentdb.InsertStoreCreditEntryParams{
		ID:           entry.ID,
		CustomerID:   entry.CustomerID,
		CurrencyCode: entry.CurrencyCode,
		Amount:       entry.Amount,
		Kind:         entry.Kind.String(),
		Reference:    entry.Reference,
		Reason:       entry.Reason,
	})
	if err != nil {
		return models.StoreCreditEntry{}, classify(err, codeQueryFailed,
			"the store credit entry could not be written")
	}

	return toStoreCreditEntry(row), nil
}

// StoreCreditBalance returns a customer's balance in ONE currency.
//
// A customer with no rows gets ZERO rather than an error: somebody who was never
// given credit and somebody who was given it and spent all of it hold the same
// amount of money.
func (r *Repository) StoreCreditBalance(
	ctx context.Context, customerID, currencyCode string,
) (int64, error) {
	balance, err := r.queries(ctx).StoreCreditBalance(ctx, paymentdb.StoreCreditBalanceParams{
		CustomerID:   customerID,
		CurrencyCode: currencyCode,
	})
	if err != nil {
		return 0, classify(err, codeQueryFailed, "the store credit balance could not be read")
	}

	return balance, nil
}

// LockStoreCreditEntries locks the customer's rows for the transaction.
//
// Every write that reads the balance and acts on it calls this FIRST: without it
// two concurrent authorizations both see enough money and both write a hold, which
// is the customer spending the same money twice. The lock puts them in a queue.
//
// A customer with no rows locks nothing, and that is not a hole: their balance is
// zero, so no authorization passes whatever order they run in.
func (r *Repository) LockStoreCreditEntries(
	ctx context.Context, customerID, currencyCode string,
) error {
	if _, err := r.queries(ctx).LockStoreCreditEntries(ctx, paymentdb.LockStoreCreditEntriesParams{
		CustomerID:   customerID,
		CurrencyCode: currencyCode,
	}); err != nil {
		return classify(err, codeQueryFailed, "the store credit entries could not be locked")
	}

	return nil
}

// ListStoreCreditEntries returns a customer's history, newest first.
func (r *Repository) ListStoreCreditEntries(
	ctx context.Context, customerID, currencyCode string, limit, offset int64,
) ([]models.StoreCreditEntry, int64, error) {
	rows, err := r.queries(ctx).ListStoreCreditEntries(ctx, paymentdb.ListStoreCreditEntriesParams{
		CustomerID:   customerID,
		CurrencyCode: currencyCode,
		RowLimit:     limit,
		RowOffset:    offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed,
			"the store credit history could not be read")
	}

	total, err := r.queries(ctx).CountStoreCreditEntries(ctx, paymentdb.CountStoreCreditEntriesParams{
		CustomerID:   customerID,
		CurrencyCode: currencyCode,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed,
			"the store credit history could not be counted")
	}

	out := make([]models.StoreCreditEntry, 0, len(rows))
	for i := range rows {
		out = append(out, toStoreCreditEntry(rows[i]))
	}

	return out, total, nil
}

// InsertStoreCreditSessionIfAbsent writes the provider's session only when that
// idempotency key is NOT YET IN USE; on a clash no row comes back (pgx.ErrNoRows)
// and the caller reads the existing session instead.
func (r *Repository) InsertStoreCreditSessionIfAbsent(
	ctx context.Context, session models.StoreCreditSession,
) (models.StoreCreditSession, bool, error) {
	row, err := r.queries(ctx).InsertStoreCreditSessionIfAbsent(ctx,
		paymentdb.InsertStoreCreditSessionIfAbsentParams{
			ID:             session.ID,
			IdempotencyKey: session.IdempotencyKey,
			Reference:      session.Reference,
			CustomerID:     session.CustomerID,
			Amount:         session.Amount,
			CurrencyCode:   session.CurrencyCode,
			Status:         session.Status.String(),
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.StoreCreditSession{}, false, nil
	}
	if err != nil {
		return models.StoreCreditSession{}, false, classify(err, codeQueryFailed,
			"the store credit session could not be written")
	}

	return toStoreCreditSession(row), true, nil
}

// StoreCreditSession returns the session by its id, or NotFound.
func (r *Repository) StoreCreditSession(
	ctx context.Context, id string,
) (models.StoreCreditSession, error) {
	row, err := r.queries(ctx).GetStoreCreditSession(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.StoreCreditSession{}, errors.NotFound(codeStoreCreditSessionNotFound,
			"no such store credit session: %s", id)
	}
	if err != nil {
		return models.StoreCreditSession{}, classify(err, codeQueryFailed,
			"the store credit session could not be read")
	}

	return toStoreCreditSession(row), nil
}

// StoreCreditSessionByIdempotencyKey returns the session by its key, or NotFound.
func (r *Repository) StoreCreditSessionByIdempotencyKey(
	ctx context.Context, key string,
) (models.StoreCreditSession, error) {
	row, err := r.queries(ctx).GetStoreCreditSessionByIdempotencyKey(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.StoreCreditSession{}, errors.NotFound(codeStoreCreditSessionNotFound,
			"no store credit session was opened with this key: %s", key)
	}
	if err != nil {
		return models.StoreCreditSession{}, classify(err, codeQueryFailed,
			"the store credit session could not be read")
	}

	return toStoreCreditSession(row), nil
}

// LockStoreCreditSession locks the session for the transaction and returns the
// state it is in at that moment.
func (r *Repository) LockStoreCreditSession(
	ctx context.Context, id string,
) (models.StoreCreditSession, error) {
	row, err := r.queries(ctx).LockStoreCreditSession(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.StoreCreditSession{}, errors.NotFound(codeStoreCreditSessionNotFound,
			"no such store credit session: %s", id)
	}
	if err != nil {
		return models.StoreCreditSession{}, classify(err, codeQueryFailed,
			"the store credit session could not be locked")
	}

	return toStoreCreditSession(row), nil
}

// UpdateStoreCreditSessionState writes the status and the three amounts as
// ABSOLUTE values.
func (r *Repository) UpdateStoreCreditSessionState(
	ctx context.Context,
	id string,
	status models.SessionStatus,
	authorized, captured, refunded int64,
	declineReason string,
) (models.StoreCreditSession, error) {
	row, err := r.queries(ctx).UpdateStoreCreditSessionState(ctx,
		paymentdb.UpdateStoreCreditSessionStateParams{
			ID:               id,
			Status:           status.String(),
			AuthorizedAmount: authorized,
			CapturedAmount:   captured,
			RefundedAmount:   refunded,
			DeclineReason:    nullText(declineReason),
		})
	if err != nil {
		return models.StoreCreditSession{}, classify(err, codeQueryFailed,
			"the store credit session could not be updated")
	}

	return toStoreCreditSession(row), nil
}

// toStoreCreditEntry turns a database row into the domain model.
func toStoreCreditEntry(row paymentdb.PaymentStoreCreditEntry) models.StoreCreditEntry {
	return models.StoreCreditEntry{
		ID:           row.ID,
		CustomerID:   row.CustomerID,
		CurrencyCode: row.CurrencyCode,
		Amount:       row.Amount,
		Kind:         models.StoreCreditKind(row.Kind),
		Reference:    row.Reference,
		Reason:       row.Reason,
		CreatedAt:    toTime(row.CreatedAt),
	}
}

// toStoreCreditSession turns a database row into the domain model.
func toStoreCreditSession(row paymentdb.PaymentStoreCreditSession) models.StoreCreditSession {
	return models.StoreCreditSession{
		ID:               row.ID,
		IdempotencyKey:   row.IdempotencyKey,
		Reference:        row.Reference,
		CustomerID:       row.CustomerID,
		Amount:           row.Amount,
		CurrencyCode:     row.CurrencyCode,
		Status:           models.SessionStatus(row.Status),
		AuthorizedAmount: row.AuthorizedAmount,
		CapturedAmount:   row.CapturedAmount,
		RefundedAmount:   row.RefundedAmount,
		DeclineReason:    derefText(row.DeclineReason),
		CreatedAt:        toTime(row.CreatedAt),
		UpdatedAt:        toTime(row.UpdatedAt),
	}
}
