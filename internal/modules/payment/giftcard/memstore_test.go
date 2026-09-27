package giftcard_test

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/giftcard"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// txMarkerKey is the fake store's "inside a transaction" marker.
type txMarkerKey struct{}

// memStore is giftcard.Store in memory. It keeps store credit's fake's four
// behaviors: a lock outside a transaction fails, a failed transaction rolls the
// sessions and the ledger back, a used idempotency key writes nothing, and the
// balance is the sum of the rows.
type memStore struct {
	mu       sync.Mutex
	cards    map[string]models.GiftCard
	digests  map[string]string
	sessions map[string]models.TenderSession
	entries  []models.GiftCardEntry
	// locked records the cards whose balance was locked, in order.
	locked []string
}

func newMemStore() *memStore {
	return &memStore{
		cards: map[string]models.GiftCard{}, digests: map[string]string{},
		sessions: map[string]models.TenderSession{},
	}
}

var _ giftcard.Store = (*memStore)(nil)

// issue puts a card with its code and balance in the store.
func (m *memStore) issue(id, code, currency string, amount int64) {
	normalized, ok := models.NormalizeGiftCardCode(code)
	if !ok {
		panic("not a code: " + code)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cards[id] = models.GiftCard{ID: id, CodeTail: models.GiftCardCodeTail(normalized), CurrencyCode: currency}
	m.digests[models.GiftCardCodeDigest(normalized)] = id
	m.entries = append(m.entries, models.GiftCardEntry{
		ID: models.NewGiftCardEntryID(), GiftCardID: id, Amount: amount, Kind: models.GiftCardIssue,
	})
}

func (m *memStore) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if ctx.Value(txMarkerKey{}) != nil {
		return fn(ctx)
	}
	m.mu.Lock()
	sessions, entries := maps.Clone(m.sessions), slices.Clone(m.entries)
	m.mu.Unlock()

	if err := fn(context.WithValue(ctx, txMarkerKey{}, true)); err != nil {
		m.mu.Lock()
		m.sessions, m.entries = sessions, entries
		m.mu.Unlock()

		return err
	}

	return nil
}

func (m *memStore) GiftCardByDigest(_ context.Context, digest string) (models.GiftCard, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.digests[digest]
	if !ok {
		return models.GiftCard{}, errors.NotFound("payment_gift_card_not_found", "no gift card has this code")
	}

	return m.cards[id], nil
}

func (m *memStore) GiftCard(_ context.Context, id string) (models.GiftCard, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	card, ok := m.cards[id]
	if !ok {
		return models.GiftCard{}, errors.NotFound("payment_gift_card_not_found", "no such gift card: %s", id)
	}

	return card, nil
}

// close marks a card closed, as an operator's close leaves it (ADR 0213).
func (m *memStore) close(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	card := m.cards[id]
	closed := time.Unix(1_000, 0).UTC()
	card.DisabledAt, card.DisableReason = &closed, "a test"
	m.cards[id] = card
}

func (m *memStore) InsertGiftCardSessionIfAbsent(
	_ context.Context, session models.TenderSession,
) (models.TenderSession, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.sessions {
		if m.sessions[id].IdempotencyKey == session.IdempotencyKey {
			return models.TenderSession{}, false, nil
		}
	}
	m.sessions[session.ID] = session

	return session, true, nil
}

func (m *memStore) GiftCardSessionByIdempotencyKey(_ context.Context, key string) (models.TenderSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.sessions {
		if m.sessions[id].IdempotencyKey == key {
			return m.sessions[id], nil
		}
	}

	return models.TenderSession{}, errors.NotFound("payment_gift_card_session_not_found", "no session: %s", key)
}

func (m *memStore) GiftCardSession(_ context.Context, id string) (models.TenderSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok {
		return models.TenderSession{}, errors.NotFound("payment_gift_card_session_not_found", "no session: %s", id)
	}

	return session, nil
}

func (m *memStore) LockGiftCardSession(ctx context.Context, id string) (models.TenderSession, error) {
	if ctx.Value(txMarkerKey{}) == nil {
		return models.TenderSession{}, errors.Internal("payment_tx_required", "a lock outside a transaction")
	}

	return m.GiftCardSession(ctx, id)
}

func (m *memStore) UpdateGiftCardSessionState(
	_ context.Context, id string, status models.SessionStatus, authorized, captured, refunded int64, reason string,
) (models.TenderSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session := m.sessions[id]
	session.Status, session.AuthorizedAmount = status, authorized
	session.CapturedAmount, session.RefundedAmount, session.DeclineReason = captured, refunded, reason
	m.sessions[id] = session

	return session, nil
}

func (m *memStore) AppendGiftCardEntry(_ context.Context, entry models.GiftCardEntry) (models.GiftCardEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if (entry.Kind == models.GiftCardHold) != (entry.Amount < 0) {
		return models.GiftCardEntry{}, errors.Invalid("payment_query_failed", "the sign does not match the kind")
	}
	m.entries = append(m.entries, entry)

	return entry, nil
}

func (m *memStore) GiftCardBalance(_ context.Context, cardID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var balance int64
	for _, entry := range m.entries {
		if entry.GiftCardID == cardID {
			balance += entry.Amount
		}
	}

	return balance, nil
}

func (m *memStore) LockGiftCardBalance(ctx context.Context, cardID, _ string) error {
	if ctx.Value(txMarkerKey{}) == nil {
		return errors.Internal("payment_tx_required", "a lock outside a transaction")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.locked = append(m.locked, cardID)

	return nil
}
