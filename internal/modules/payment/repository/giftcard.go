package repository

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/repository/paymentdb"
)

// The three tables a gift card is kept in (ADR 0208): the cards, their ledger,
// and the gift-card provider's own sessions, which the service never touches.

// Error codes of the gift card tables.
const (
	// codeGiftCardNotFound reports an id or a digest no card has.
	codeGiftCardNotFound = "payment_gift_card_not_found"
	// codeGiftCardSessionNotFound reports a session the gift-card provider
	// never opened.
	codeGiftCardSessionNotFound = "payment_gift_card_session_not_found"
)

// InsertGiftCard writes a card and reports whether it did. A sold card whose
// sale already made one is not written: the second return is false and the
// caller reads the existing card by its source reference (ADR 0210).
func (r *Repository) InsertGiftCard(
	ctx context.Context, card models.GiftCard, digest string,
) (models.GiftCard, bool, error) {
	var reference *string
	if card.SourceReference != "" {
		reference = &card.SourceReference
	}
	row, err := r.queries(ctx).InsertGiftCard(ctx, paymentdb.InsertGiftCardParams{
		ID:              card.ID,
		CodeDigest:      digest,
		CodeTail:        card.CodeTail,
		CurrencyCode:    card.CurrencyCode,
		Reason:          card.Reason,
		Source:          string(card.Source),
		SourceReference: reference,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.GiftCard{}, false, nil
	}
	if err != nil {
		return models.GiftCard{}, false, classify(err, codeQueryFailed, "the gift card could not be written")
	}

	return toGiftCard(row), true, nil
}

// GiftCardBySourceReference returns the card a sale made, or NotFound.
func (r *Repository) GiftCardBySourceReference(ctx context.Context, reference string) (models.GiftCard, error) {
	row, err := r.queries(ctx).GetGiftCardBySourceReference(ctx, &reference)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.GiftCard{}, errors.NotFound(codeGiftCardNotFound, "no gift card was sold as %s", reference)
	}
	if err != nil {
		return models.GiftCard{}, classify(err, codeQueryFailed, "the gift card could not be read")
	}

	return toGiftCard(row), nil
}

// ReplaceGiftCardCode gives a card a new code's digest and tail.
func (r *Repository) ReplaceGiftCardCode(ctx context.Context, id, digest, tail string) (models.GiftCard, error) {
	row, err := r.queries(ctx).ReplaceGiftCardCode(ctx, paymentdb.ReplaceGiftCardCodeParams{
		ID: id, CodeDigest: digest, CodeTail: tail,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.GiftCard{}, errors.NotFound(codeGiftCardNotFound, "no such gift card: %s", id)
	}
	if err != nil {
		return models.GiftCard{}, classify(err, codeQueryFailed, "the gift card's code could not be replaced")
	}

	return toGiftCard(row), nil
}

// GiftCard returns a card by its id, or NotFound.
func (r *Repository) GiftCard(ctx context.Context, id string) (models.GiftCard, error) {
	row, err := r.queries(ctx).GetGiftCard(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.GiftCard{}, errors.NotFound(codeGiftCardNotFound, "no such gift card: %s", id)
	}
	if err != nil {
		return models.GiftCard{}, classify(err, codeQueryFailed, "the gift card could not be read")
	}

	return toGiftCard(row), nil
}

// GiftCardByDigest returns the card a code's digest opens, or NotFound. The
// error names neither the digest nor the code.
func (r *Repository) GiftCardByDigest(ctx context.Context, digest string) (models.GiftCard, error) {
	row, err := r.queries(ctx).GetGiftCardByDigest(ctx, digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.GiftCard{}, errors.NotFound(codeGiftCardNotFound, "no gift card has this code")
	}
	if err != nil {
		return models.GiftCard{}, classify(err, codeQueryFailed, "the gift card could not be read")
	}

	return toGiftCard(row), nil
}

// ListGiftCards returns a page of cards, newest first, and how many there are.
func (r *Repository) ListGiftCards(ctx context.Context, limit, offset int64) ([]models.GiftCard, int64, error) {
	rows, err := r.queries(ctx).ListGiftCards(ctx, paymentdb.ListGiftCardsParams{RowLimit: limit, RowOffset: offset})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "the gift cards could not be read")
	}
	total, err := r.queries(ctx).CountGiftCards(ctx)
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "the gift cards could not be counted")
	}

	out := make([]models.GiftCard, 0, len(rows))
	for i := range rows {
		out = append(out, toGiftCard(rows[i]))
	}

	return out, total, nil
}

// AppendGiftCardEntry adds one event to a card's ledger. There is no update and
// no delete: a correction is a new row.
func (r *Repository) AppendGiftCardEntry(ctx context.Context, entry models.GiftCardEntry) (models.GiftCardEntry, error) {
	row, err := r.queries(ctx).InsertGiftCardEntry(ctx, paymentdb.InsertGiftCardEntryParams{
		ID:         entry.ID,
		GiftCardID: entry.GiftCardID,
		Amount:     entry.Amount,
		Kind:       entry.Kind.String(),
		Reference:  entry.Reference,
	})
	if err != nil {
		return models.GiftCardEntry{}, classify(err, codeQueryFailed, "the gift card entry could not be written")
	}

	return toGiftCardEntry(row), nil
}

// GiftCardBalance returns a card's balance.
func (r *Repository) GiftCardBalance(ctx context.Context, cardID string) (int64, error) {
	balance, err := r.queries(ctx).GiftCardBalance(ctx, cardID)
	if err != nil {
		return 0, classify(err, codeQueryFailed, "the gift card balance could not be read")
	}

	return balance, nil
}

// GiftCardBalances returns the balances of several cards by id; a card with no
// rows is absent and holds zero.
func (r *Repository) GiftCardBalances(ctx context.Context, cardIDs []string) (map[string]int64, error) {
	rows, err := r.queries(ctx).GiftCardBalances(ctx, cardIDs)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the gift card balances could not be read")
	}

	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.GiftCardID] = row.Balance
	}

	return out, nil
}

// ListGiftCardEntries returns a card's history, newest first, and its length.
func (r *Repository) ListGiftCardEntries(
	ctx context.Context, cardID string, limit, offset int64,
) ([]models.GiftCardEntry, int64, error) {
	rows, err := r.queries(ctx).ListGiftCardEntries(ctx, paymentdb.ListGiftCardEntriesParams{
		GiftCardID: cardID, RowLimit: limit, RowOffset: offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "the gift card history could not be read")
	}
	total, err := r.queries(ctx).CountGiftCardEntries(ctx, cardID)
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "the gift card history could not be counted")
	}

	out := make([]models.GiftCardEntry, 0, len(rows))
	for i := range rows {
		out = append(out, toGiftCardEntry(rows[i]))
	}

	return out, total, nil
}

// LockGiftCardBalance locks a card's balance until the transaction ends.
//
// The lock is the card's own row. A customer's credit needs an advisory lock
// because a customer with no rows has nothing to lock (D118); a card is issued
// with its row, so the row is there before any balance is read. The currency is
// the card's own and is not part of the lock.
func (r *Repository) LockGiftCardBalance(ctx context.Context, cardID, _ string) error {
	if err := requireTx(ctx, "LockGiftCardBalance"); err != nil {
		return err
	}
	if _, err := r.queries(ctx).LockGiftCard(ctx, cardID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.NotFound(codeGiftCardNotFound, "no such gift card: %s", cardID)
		}

		return classify(err, codeQueryFailed, "the gift card could not be locked")
	}

	return nil
}

// InsertGiftCardSessionIfAbsent writes the provider's session only when that
// idempotency key is not yet in use; on a clash the caller reads the existing one.
func (r *Repository) InsertGiftCardSessionIfAbsent(
	ctx context.Context, session models.TenderSession,
) (models.TenderSession, bool, error) {
	row, err := r.queries(ctx).InsertGiftCardSessionIfAbsent(ctx, paymentdb.InsertGiftCardSessionIfAbsentParams{
		ID:             session.ID,
		IdempotencyKey: session.IdempotencyKey,
		Reference:      session.Reference,
		GiftCardID:     session.OwnerID,
		Amount:         session.Amount,
		CurrencyCode:   session.CurrencyCode,
		Status:         session.Status.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return models.TenderSession{}, false, nil
	}
	if err != nil {
		return models.TenderSession{}, false, classify(err, codeQueryFailed,
			"the gift card session could not be written")
	}

	return toGiftCardSession(row), true, nil
}

// GiftCardSession returns the session by its id, or NotFound.
func (r *Repository) GiftCardSession(ctx context.Context, id string) (models.TenderSession, error) {
	row, err := r.queries(ctx).GetGiftCardSession(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.TenderSession{}, errors.NotFound(codeGiftCardSessionNotFound,
			"no such gift card session: %s", id)
	}
	if err != nil {
		return models.TenderSession{}, classify(err, codeQueryFailed, "the gift card session could not be read")
	}

	return toGiftCardSession(row), nil
}

// GiftCardSessionByIdempotencyKey returns the session by its key, or NotFound.
func (r *Repository) GiftCardSessionByIdempotencyKey(ctx context.Context, key string) (models.TenderSession, error) {
	row, err := r.queries(ctx).GetGiftCardSessionByIdempotencyKey(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.TenderSession{}, errors.NotFound(codeGiftCardSessionNotFound,
			"no gift card session was opened with this key: %s", key)
	}
	if err != nil {
		return models.TenderSession{}, classify(err, codeQueryFailed, "the gift card session could not be read")
	}

	return toGiftCardSession(row), nil
}

// LockGiftCardSession locks the session for the transaction.
func (r *Repository) LockGiftCardSession(ctx context.Context, id string) (models.TenderSession, error) {
	row, err := r.queries(ctx).LockGiftCardSession(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return models.TenderSession{}, errors.NotFound(codeGiftCardSessionNotFound,
			"no such gift card session: %s", id)
	}
	if err != nil {
		return models.TenderSession{}, classify(err, codeQueryFailed, "the gift card session could not be locked")
	}

	return toGiftCardSession(row), nil
}

// UpdateGiftCardSessionState writes the status and the three amounts as
// ABSOLUTE values.
func (r *Repository) UpdateGiftCardSessionState(
	ctx context.Context,
	id string,
	status models.SessionStatus,
	authorized, captured, refunded int64,
	declineReason string,
) (models.TenderSession, error) {
	row, err := r.queries(ctx).UpdateGiftCardSessionState(ctx, paymentdb.UpdateGiftCardSessionStateParams{
		ID:               id,
		Status:           status.String(),
		AuthorizedAmount: authorized,
		CapturedAmount:   captured,
		RefundedAmount:   refunded,
		DeclineReason:    nullText(declineReason),
	})
	if err != nil {
		return models.TenderSession{}, classify(err, codeQueryFailed, "the gift card session could not be updated")
	}

	return toGiftCardSession(row), nil
}

// toGiftCard turns a database row into the domain model; the digest stays in
// the database.
func toGiftCard(row paymentdb.PaymentGiftCard) models.GiftCard {
	card := models.GiftCard{
		ID:           row.ID,
		CodeTail:     row.CodeTail,
		CurrencyCode: row.CurrencyCode,
		Reason:       row.Reason,
		Source:       models.GiftCardSource(row.Source),
		CreatedAt:    toTime(row.CreatedAt),
	}
	if row.SourceReference != nil {
		card.SourceReference = *row.SourceReference
	}
	card.CodeChangedAt = toTimePtr(row.CodeChangedAt)

	return card
}

// toGiftCardEntry turns a database row into the domain model.
func toGiftCardEntry(row paymentdb.PaymentGiftCardEntry) models.GiftCardEntry {
	return models.GiftCardEntry{
		ID:         row.ID,
		GiftCardID: row.GiftCardID,
		Amount:     row.Amount,
		Kind:       models.GiftCardKind(row.Kind),
		Reference:  row.Reference,
		CreatedAt:  toTime(row.CreatedAt),
	}
}

// toGiftCardSession turns a database row into the domain model.
func toGiftCardSession(row paymentdb.PaymentGiftCardSession) models.TenderSession {
	return models.TenderSession{
		ID:               row.ID,
		IdempotencyKey:   row.IdempotencyKey,
		Reference:        row.Reference,
		OwnerID:          row.GiftCardID,
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
