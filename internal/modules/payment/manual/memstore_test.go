package manual_test

import (
	"context"
	"maps"
	"sync"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// txMarkerKey is the fake store's "we are inside a transaction" marker.
type txMarkerKey struct{}

// memStore is the in-memory counterpart of manual.Store.
//
// It DELIBERATELY imitates three behaviors of the real store, because the
// provider's correctness rests on them:
//
//  1. LockManualSession returns an error if it is called OUTSIDE a
//     transaction. If the provider forgets WithTx in a flow, the unit test
//     catches it.
//  2. If the transaction ends with an error, what was written is ROLLED BACK;
//     the claim "an error was returned and the ledger did not change" can
//     only be exercised this way.
//  3. InsertManualSessionIfAbsent does not write a second time with the same
//     key and does NOT RETURN AN ERROR; that is the ground the idempotency
//     contract stands on.
type memStore struct {
	mu       sync.Mutex
	sessions map[string]models.ManualSession

	// insertCalls counts how many REAL writes were made; it is what proves
	// that a second CreateSession with the same key opens no new row.
	insertCalls int
	// updateCalls counts how many times a state was written; it is what proves
	// that the idempotent branches do not touch the ledger A SECOND TIME.
	updateCalls int
}

// newMemStore builds an empty in-memory ledger.
func newMemStore() *memStore {
	return &memStore{sessions: map[string]models.ManualSession{}}
}

// That the fake store satisfies the surface the provider expects is verified
// at compile time.
var _ manual.Store = (*memStore)(nil)

// WithTx runs fn inside a "transaction"; if it returns an error, the ledger is
// rolled back.
func (m *memStore) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if ctx.Value(txMarkerKey{}) != nil {
		return fn(ctx)
	}

	m.mu.Lock()
	snapshot := maps.Clone(m.sessions)
	m.mu.Unlock()

	if err := fn(context.WithValue(ctx, txMarkerKey{}, true)); err != nil {
		m.mu.Lock()
		m.sessions = snapshot
		m.mu.Unlock()
		return err
	}
	return nil
}

// InsertManualSessionIfAbsent writes the session only if the key is free.
func (m *memStore) InsertManualSessionIfAbsent(
	_ context.Context,
	ses models.ManualSession,
) (models.ManualSession, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.sessions {
		if m.sessions[id].IdempotencyKey == ses.IdempotencyKey {
			return models.ManualSession{}, false, nil
		}
	}

	now := time.Now().UTC()
	ses.CreatedAt, ses.UpdatedAt = now, now
	m.sessions[ses.ID] = ses
	m.insertCalls++
	return ses, true, nil
}

// ManualSessionByIdempotencyKey returns the session by its key.
func (m *memStore) ManualSessionByIdempotencyKey(_ context.Context, key string) (models.ManualSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.sessions {
		if m.sessions[id].IdempotencyKey == key {
			return m.sessions[id], nil
		}
	}
	return models.ManualSession{}, errors.NotFound("fake_not_found", "no session: %s", key)
}

// ManualSession returns the session by its identifier.
func (m *memStore) ManualSession(_ context.Context, id string) (models.ManualSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ses, ok := m.sessions[id]
	if !ok {
		return models.ManualSession{}, errors.NotFound("fake_not_found", "no session: %s", id)
	}
	return ses, nil
}

// LockManualSession returns the session; it returns an error if it is called
// outside a transaction.
func (m *memStore) LockManualSession(ctx context.Context, id string) (models.ManualSession, error) {
	if ctx.Value(txMarkerKey{}) == nil {
		return models.ManualSession{}, errors.Internal("fake_tx_required",
			"LockManualSession was called outside a transaction")
	}
	return m.ManualSession(ctx, id)
}

// UpdateManualSessionState writes the status and the amounts as absolute
// values.
func (m *memStore) UpdateManualSessionState(
	_ context.Context,
	id string,
	status models.SessionStatus,
	authorized, captured, refunded int64,
	declineReason string,
) (models.ManualSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ses, ok := m.sessions[id]
	if !ok {
		return models.ManualSession{}, errors.NotFound("fake_not_found", "no session: %s", id)
	}
	ses.Status = status
	ses.AuthorizedAmount = authorized
	ses.CapturedAmount = captured
	ses.RefundedAmount = refunded
	ses.DeclineReason = declineReason
	ses.UpdatedAt = time.Now().UTC()
	m.sessions[id] = ses
	m.updateCalls++
	return ses, nil
}

// counts returns the write counters together.
func (m *memStore) counts() (inserts, updates int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.insertCalls, m.updateCalls
}
