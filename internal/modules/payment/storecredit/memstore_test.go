package storecredit_test

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/storecredit"
)

// txMarkerKey is the fake store's "we are inside a transaction" marker.
type txMarkerKey struct{}

// memStore is storecredit.Store in memory.
//
// It imitates four behaviors of the real store DELIBERATELY, because the
// provider's correctness rests on them:
//
//  1. a lock taken OUTSIDE a transaction fails, so a flow that forgot WithTx is
//     caught by a unit test rather than by a deadlock in production;
//  2. a transaction that ends in an error ROLLS BACK both the sessions and the
//     ledger — "it returned an error and the money did not move" cannot be
//     asserted otherwise;
//  3. an insert with a used idempotency key writes nothing and is NOT an error;
//  4. the balance is the SUM of the rows rather than a number kept beside them,
//     so a hold written with the wrong sign shows up in the balance the way it
//     would in the database.
type memStore struct {
	mu       sync.Mutex
	sessions map[string]models.StoreCreditSession
	entries  []models.StoreCreditEntry

	// locks records the locks taken, in order. The order is a concurrency
	// contract — the session first, the ledger second — and in a real database a
	// violation shows up only under load, as a deadlock.
	locks []string
	// insertCalls counts the REAL session writes; a second CreateSession under
	// one key opening no row is proved with it.
	insertCalls int
}

// newMemStore builds an empty ledger.
func newMemStore() *memStore {
	return &memStore{sessions: map[string]models.StoreCreditSession{}}
}

// That the fake satisfies the surface the provider expects is pinned at compile
// time.
var _ storecredit.Store = (*memStore)(nil)

// WithTx runs fn in a "transaction"; on an error it rolls the ledger back.
func (m *memStore) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if ctx.Value(txMarkerKey{}) != nil {
		return fn(ctx)
	}

	m.mu.Lock()
	sessions := maps.Clone(m.sessions)
	entries := slices.Clone(m.entries)
	m.mu.Unlock()

	if err := fn(context.WithValue(ctx, txMarkerKey{}, true)); err != nil {
		m.mu.Lock()
		m.sessions, m.entries = sessions, entries
		m.mu.Unlock()

		return err
	}

	return nil
}

// InsertStoreCreditSessionIfAbsent writes the session only if the key is free.
func (m *memStore) InsertStoreCreditSessionIfAbsent(
	_ context.Context, session models.StoreCreditSession,
) (models.StoreCreditSession, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.sessions {
		if m.sessions[id].IdempotencyKey == session.IdempotencyKey {
			return models.StoreCreditSession{}, false, nil
		}
	}

	now := time.Now().UTC()
	session.CreatedAt, session.UpdatedAt = now, now
	m.sessions[session.ID] = session
	m.insertCalls++

	return session, true, nil
}

// StoreCreditSessionByIdempotencyKey returns the session by its key.
func (m *memStore) StoreCreditSessionByIdempotencyKey(
	_ context.Context, key string,
) (models.StoreCreditSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.sessions {
		if m.sessions[id].IdempotencyKey == key {
			return m.sessions[id], nil
		}
	}

	return models.StoreCreditSession{}, errors.NotFound("fake_not_found", "no session: %s", key)
}

// StoreCreditSession returns the session by its id.
func (m *memStore) StoreCreditSession(
	_ context.Context, id string,
) (models.StoreCreditSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[id]
	if !ok {
		return models.StoreCreditSession{}, errors.NotFound("fake_not_found", "no session: %s", id)
	}

	return session, nil
}

// LockStoreCreditSession refuses OUTSIDE a transaction, as the real one does.
func (m *memStore) LockStoreCreditSession(
	ctx context.Context, id string,
) (models.StoreCreditSession, error) {
	if ctx.Value(txMarkerKey{}) == nil {
		return models.StoreCreditSession{}, errors.Internal("fake_no_tx",
			"a lock may only be taken inside a transaction: %s", id)
	}

	m.mu.Lock()
	m.locks = append(m.locks, "session")
	m.mu.Unlock()

	return m.StoreCreditSession(ctx, id)
}

// UpdateStoreCreditSessionState writes the status and the amounts.
func (m *memStore) UpdateStoreCreditSessionState(
	_ context.Context,
	id string,
	status models.SessionStatus,
	authorized, captured, refunded int64,
	declineReason string,
) (models.StoreCreditSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[id]
	if !ok {
		return models.StoreCreditSession{}, errors.NotFound("fake_not_found", "no session: %s", id)
	}

	session.Status = status
	session.AuthorizedAmount = authorized
	session.CapturedAmount = captured
	session.RefundedAmount = refunded
	session.DeclineReason = declineReason
	session.UpdatedAt = time.Now().UTC()
	m.sessions[id] = session

	return session, nil
}

// AppendStoreCreditEntry appends one row; there is no update and no delete.
func (m *memStore) AppendStoreCreditEntry(
	_ context.Context, entry models.StoreCreditEntry,
) (models.StoreCreditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry.CreatedAt = time.Now().UTC()
	m.entries = append(m.entries, entry)

	return entry, nil
}

// StoreCreditBalance sums the rows, the way the real query does.
func (m *memStore) StoreCreditBalance(
	_ context.Context, customerID, currencyCode string,
) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var balance int64
	for i := range m.entries {
		if m.entries[i].CustomerID == customerID && m.entries[i].CurrencyCode == currencyCode {
			balance += m.entries[i].Amount
		}
	}

	return balance, nil
}

// LockStoreCreditEntries refuses OUTSIDE a transaction and records the lock.
func (m *memStore) LockStoreCreditEntries(ctx context.Context, customerID, _ string) error {
	if ctx.Value(txMarkerKey{}) == nil {
		return errors.Internal("fake_no_tx",
			"the ledger may only be locked inside a transaction: %s", customerID)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.locks = append(m.locks, "ledger")

	return nil
}

// kinds returns the ledger's kinds in order, which is what most assertions here
// are really about: which events the provider wrote, and in which sequence.
func (m *memStore) kinds() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]string, 0, len(m.entries))
	for i := range m.entries {
		out = append(out, m.entries[i].Kind.String())
	}

	return out
}

// balance is the fake's own sum, for assertions.
func (m *memStore) balance(customerID, currencyCode string) int64 {
	value, _ := m.StoreCreditBalance(context.Background(), customerID, currencyCode)

	return value
}
