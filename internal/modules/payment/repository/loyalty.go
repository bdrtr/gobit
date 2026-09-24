package repository

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/repository/paymentdb"
)

// The two tables loyalty points are kept in (ADR 0164, ADR 0165).
//
// The LEDGER is the module's own record of what a customer earned from money
// that actually moved and of what they spent. It has two writers and an arch
// gate holds the pair: the service's earn path, reached from the ONE function
// that moves a collection's totals, and the loyalty-points provider's package.
// The SESSIONS belong to that provider, the way payment_store_credit_sessions
// belong to the store-credit one — the service never touches them.

// AppendLoyaltyEntry adds one row to a customer's point ledger.
//
// There is no update and no delete: because the balance is the sum of the rows, a
// correction is A NEW ROW — the module's rule since migration 000003, "a money
// record is kept", and the reason a refund appends a reverse rather than editing
// the earn it is undoing.
func (r *Repository) AppendLoyaltyEntry(
	ctx context.Context, entry models.LoyaltyEntry,
) (models.LoyaltyEntry, error) {
	row, err := r.queries(ctx).InsertLoyaltyEntry(ctx, paymentdb.InsertLoyaltyEntryParams{
		ID:           entry.ID,
		CustomerID:   entry.CustomerID,
		CurrencyCode: entry.CurrencyCode,
		Points:       entry.Points,
		Kind:         entry.Kind.String(),
		Reference:    entry.Reference,
	})
	if err != nil {
		return models.LoyaltyEntry{}, classify(err, codeQueryFailed,
			"the loyalty point entry could not be written")
	}

	return toLoyaltyEntry(row), nil
}

// LoyaltyPointsForReference sums what ONE collection has already been EARNED.
//
// It is the read that makes the write a target rather than an increment: the earn
// path computes where this collection's points should be and appends the
// difference, so being called twice for the same totals appends nothing. A
// collection with no rows has earned zero. Only earn and reverse rows are summed;
// a spend row references a session and never a collection, and the query says so
// rather than resting on that convention (ADR 0165).
func (r *Repository) LoyaltyPointsForReference(
	ctx context.Context, reference string,
) (int64, error) {
	points, err := r.queries(ctx).LoyaltyPointsForReference(ctx, reference)
	if err != nil {
		return 0, classify(err, codeQueryFailed,
			"the collection's loyalty points could not be read")
	}

	return points, nil
}

// LoyaltyBalance returns a customer's points in ONE currency.
//
// A customer with no rows gets ZERO rather than an error, for
// [Repository.StoreCreditBalance]'s reason: somebody who never earned and
// somebody whose points were all reversed hold the same number of points.
func (r *Repository) LoyaltyBalance(
	ctx context.Context, customerID, currencyCode string,
) (int64, error) {
	points, err := r.queries(ctx).LoyaltyBalance(ctx, paymentdb.LoyaltyBalanceParams{
		CustomerID:   customerID,
		CurrencyCode: currencyCode,
	})
	if err != nil {
		return 0, classify(err, codeQueryFailed, "the loyalty point balance could not be read")
	}

	return points, nil
}

// ListLoyaltyEntries pages one customer's point history, newest first; the second
// value is the count of ALL their rows in that currency.
func (r *Repository) ListLoyaltyEntries(
	ctx context.Context, customerID, currencyCode string, limit, offset int64,
) ([]models.LoyaltyEntry, int64, error) {
	rows, err := r.queries(ctx).ListLoyaltyEntries(ctx, paymentdb.ListLoyaltyEntriesParams{
		CustomerID:   customerID,
		CurrencyCode: currencyCode,
		RowLimit:     limit,
		RowOffset:    offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed,
			"the loyalty point history could not be read")
	}

	total, err := r.queries(ctx).CountLoyaltyEntries(ctx, paymentdb.CountLoyaltyEntriesParams{
		CustomerID:   customerID,
		CurrencyCode: currencyCode,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed,
			"the loyalty point history could not be counted")
	}

	out := make([]models.LoyaltyEntry, 0, len(rows))
	for i := range rows {
		out = append(out, toLoyaltyEntry(rows[i]))
	}

	return out, total, nil
}

// toLoyaltyEntry turns a database row into the domain model.
func toLoyaltyEntry(row paymentdb.PaymentLoyaltyEntry) models.LoyaltyEntry {
	return models.LoyaltyEntry{
		ID:           row.ID,
		CustomerID:   row.CustomerID,
		CurrencyCode: row.CurrencyCode,
		Points:       row.Points,
		Kind:         models.LoyaltyKind(row.Kind),
		Reference:    row.Reference,
		CreatedAt:    toTime(row.CreatedAt),
	}
}

// CollectionNetCapturedExcludingProvider returns what a collection has captured
// and not refunded through every tender but the named one.
//
// It is the earn path's base (ADR 0165): money that came out of the points
// ledger earns no points. A collection with no captures returns zero.
func (r *Repository) CollectionNetCapturedExcludingProvider(
	ctx context.Context, collectionID, excludedProviderID string,
) (int64, error) {
	net, err := r.queries(ctx).CollectionNetCapturedExcludingProvider(ctx,
		paymentdb.CollectionNetCapturedExcludingProviderParams{
			PaymentCollectionID: collectionID,
			ExcludedProviderID:  excludedProviderID,
		})
	if err != nil {
		return 0, classify(err, codeQueryFailed,
			"the collection's captures could not be summed by provider")
	}

	return net, nil
}

// InsertLoyaltySessionIfAbsent writes the provider's session only when that
// idempotency key is NOT YET IN USE; on a clash no row comes back (pgx.ErrNoRows)
// and the caller reads the existing session instead.
func (r *Repository) InsertLoyaltySessionIfAbsent(
	ctx context.Context, session models.TenderSession,
) (models.TenderSession, bool, error) {
	row, err := r.queries(ctx).InsertLoyaltySessionIfAbsent(ctx,
		paymentdb.InsertLoyaltySessionIfAbsentParams{
			ID:             session.ID,
			IdempotencyKey: session.IdempotencyKey,
			Reference:      session.Reference,
			CustomerID:     session.CustomerID,
			Amount:         session.Amount,
			CurrencyCode:   session.CurrencyCode,
			Status:         session.Status.String(),
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.TenderSession{}, false, nil
	}
	if err != nil {
		return models.TenderSession{}, false, classify(err, codeQueryFailed,
			"the loyalty session could not be written")
	}

	return toLoyaltySession(row), true, nil
}

// LoyaltySession returns the session by its id, or NotFound.
func (r *Repository) LoyaltySession(
	ctx context.Context, id string,
) (models.TenderSession, error) {
	row, err := r.queries(ctx).GetLoyaltySession(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.TenderSession{}, errors.NotFound(codeLoyaltySessionNotFound,
			"no such loyalty session: %s", id)
	}
	if err != nil {
		return models.TenderSession{}, classify(err, codeQueryFailed,
			"the loyalty session could not be read")
	}

	return toLoyaltySession(row), nil
}

// LoyaltySessionByIdempotencyKey returns the session by its key, or NotFound.
func (r *Repository) LoyaltySessionByIdempotencyKey(
	ctx context.Context, key string,
) (models.TenderSession, error) {
	row, err := r.queries(ctx).GetLoyaltySessionByIdempotencyKey(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.TenderSession{}, errors.NotFound(codeLoyaltySessionNotFound,
			"no loyalty session was opened with this key: %s", key)
	}
	if err != nil {
		return models.TenderSession{}, classify(err, codeQueryFailed,
			"the loyalty session could not be read")
	}

	return toLoyaltySession(row), nil
}

// LockLoyaltySession locks the session for the transaction and returns the
// state it is in at that moment.
func (r *Repository) LockLoyaltySession(
	ctx context.Context, id string,
) (models.TenderSession, error) {
	row, err := r.queries(ctx).LockLoyaltySession(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.TenderSession{}, errors.NotFound(codeLoyaltySessionNotFound,
			"no such loyalty session: %s", id)
	}
	if err != nil {
		return models.TenderSession{}, classify(err, codeQueryFailed,
			"the loyalty session could not be locked")
	}

	return toLoyaltySession(row), nil
}

// UpdateLoyaltySessionState writes the status and the three amounts as ABSOLUTE
// values.
func (r *Repository) UpdateLoyaltySessionState(
	ctx context.Context,
	id string,
	status models.SessionStatus,
	authorized, captured, refunded int64,
	declineReason string,
) (models.TenderSession, error) {
	row, err := r.queries(ctx).UpdateLoyaltySessionState(ctx,
		paymentdb.UpdateLoyaltySessionStateParams{
			ID:               id,
			Status:           status.String(),
			AuthorizedAmount: authorized,
			CapturedAmount:   captured,
			RefundedAmount:   refunded,
			DeclineReason:    nullText(declineReason),
		})
	if err != nil {
		return models.TenderSession{}, classify(err, codeQueryFailed,
			"the loyalty session could not be updated")
	}

	return toLoyaltySession(row), nil
}

// toLoyaltySession turns a database row into the domain model.
func toLoyaltySession(row paymentdb.PaymentLoyaltySession) models.TenderSession {
	return models.TenderSession{
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
