package loyaltypoints_test

import (
	"context"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/loyaltypoints"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// txMarkerKey is the fake store's "we are inside a transaction" marker.
type txMarkerKey struct{}

// currencyFormat is the schema's CHECK on the ledger's currency column.
var currencyFormat = regexp.MustCompile(`^[A-Z]{3}$`)

// memStore is loyaltypoints.Store in memory.
//
// It imitates the store-credit fake's four behaviors, because the provider's
// correctness rests on them:
//
//  1. a lock taken OUTSIDE a transaction fails, so a flow that forgot WithTx is
//     caught by a unit test rather than by a deadlock in production;
//  2. a transaction that ends in an error ROLLS BACK both the sessions and the
//     ledger — "it returned an error and the points did not move" cannot be
//     asserted otherwise;
//  3. an insert with a used idempotency key writes nothing and is NOT an error;
//  4. the balance is the SUM of the rows rather than a number kept beside them,
//     so a hold written with the wrong sign shows up in the balance the way it
//     would in the database.
//
// And a fifth the credit fake does not have: the ledger's CHECK constraints.
// Migration 000006 pairs every kind with a sign and closes the vocabulary; the
// real repository turns those violations into ERRORS, and a fake that accepted a
// positive hold would let the provider's contribution — the sign — go untested
// at exactly the point where it contributes.
type memStore struct {
	mu       sync.Mutex
	sessions map[string]models.TenderSession
	entries  []models.LoyaltyEntry

	// sequence records the locks taken and the balance read, in order. The
	// order is a concurrency contract — the session first, the balance second,
	// the read last — and in a real database a violation shows up only under
	// load: as a deadlock when the two locks are swapped, as a balance spent
	// twice when the read comes before the lock.
	sequence []string
	// insertCalls counts the REAL session writes; a second CreateSession under
	// one key opening no row is proved with it.
	insertCalls int
}

// newMemStore builds an empty ledger.
func newMemStore() *memStore {
	return &memStore{sessions: map[string]models.TenderSession{}}
}

// That the fake satisfies the surface the provider expects is pinned at compile
// time.
var _ loyaltypoints.Store = (*memStore)(nil)

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

// InsertLoyaltySessionIfAbsent writes the session only if the key is free.
func (m *memStore) InsertLoyaltySessionIfAbsent(
	_ context.Context, session models.TenderSession,
) (models.TenderSession, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.sessions {
		if m.sessions[id].IdempotencyKey == session.IdempotencyKey {
			return models.TenderSession{}, false, nil
		}
	}

	now := time.Now().UTC()
	session.CreatedAt, session.UpdatedAt = now, now
	m.sessions[session.ID] = session
	m.insertCalls++

	return session, true, nil
}

// LoyaltySessionByIdempotencyKey returns the session by its key.
func (m *memStore) LoyaltySessionByIdempotencyKey(
	_ context.Context, key string,
) (models.TenderSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id := range m.sessions {
		if m.sessions[id].IdempotencyKey == key {
			return m.sessions[id], nil
		}
	}

	return models.TenderSession{}, errors.NotFound("fake_not_found", "no session: %s", key)
}

// LoyaltySession returns the session by its id.
func (m *memStore) LoyaltySession(
	_ context.Context, id string,
) (models.TenderSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[id]
	if !ok {
		return models.TenderSession{}, errors.NotFound("fake_not_found", "no session: %s", id)
	}

	return session, nil
}

// LockLoyaltySession refuses OUTSIDE a transaction, as the real one does.
func (m *memStore) LockLoyaltySession(
	ctx context.Context, id string,
) (models.TenderSession, error) {
	if ctx.Value(txMarkerKey{}) == nil {
		return models.TenderSession{}, errors.Internal("fake_no_tx",
			"a lock may only be taken inside a transaction: %s", id)
	}

	m.mu.Lock()
	m.sequence = append(m.sequence, "session")
	m.mu.Unlock()

	return m.LoyaltySession(ctx, id)
}

// UpdateLoyaltySessionState writes the status and the amounts.
func (m *memStore) UpdateLoyaltySessionState(
	_ context.Context,
	id string,
	status models.SessionStatus,
	authorized, captured, refunded int64,
	declineReason string,
) (models.TenderSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[id]
	if !ok {
		return models.TenderSession{}, errors.NotFound("fake_not_found", "no session: %s", id)
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

// AppendLoyaltyEntry appends one row, refusing what the schema refuses.
func (m *memStore) AppendLoyaltyEntry(
	_ context.Context, entry models.LoyaltyEntry,
) (models.LoyaltyEntry, error) {
	if err := checkEntry(entry); err != nil {
		return models.LoyaltyEntry{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	entry.CreatedAt = time.Now().UTC()
	m.entries = append(m.entries, entry)

	return entry, nil
}

// checkEntry is the ledger's six CHECK constraints, in the order 000005 declares
// them; 000006 replaced the last two with the five-kind vocabulary and its
// signs, and those are the two this fake reads.
func checkEntry(entry models.LoyaltyEntry) error {
	switch {
	case strings.TrimSpace(entry.CustomerID) == "":
		return errors.Internal("fake_check", "payment_loyalty_entries_customer_not_blank")
	case !currencyFormat.MatchString(entry.CurrencyCode):
		return errors.Invalid("fake_check", "payment_loyalty_entries_currency_format")
	case strings.TrimSpace(entry.Reference) == "":
		return errors.Internal("fake_check", "payment_loyalty_entries_reference_not_blank")
	case entry.Points == 0:
		return errors.Internal("fake_check", "payment_loyalty_entries_points_not_zero")
	case !entry.Kind.Valid():
		return errors.Internal("fake_check", "payment_loyalty_entries_kind_valid")
	}

	negative := entry.Kind == models.LoyaltyReverse || entry.Kind == models.LoyaltyHold
	if negative != (entry.Points < 0) {
		return errors.Internal("fake_check", "payment_loyalty_entries_sign_matches_kind")
	}

	return nil
}

// LoyaltyBalance sums the rows, the way the real query does.
func (m *memStore) LoyaltyBalance(
	_ context.Context, customerID, currencyCode string,
) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sequence = append(m.sequence, "balance read")

	var balance int64
	for i := range m.entries {
		if m.entries[i].CustomerID == customerID && m.entries[i].CurrencyCode == currencyCode {
			balance += m.entries[i].Points
		}
	}

	return balance, nil
}

// LockLoyaltyBalance refuses OUTSIDE a transaction and records the lock.
func (m *memStore) LockLoyaltyBalance(ctx context.Context, customerID, _ string) error {
	if ctx.Value(txMarkerKey{}) == nil {
		return errors.Internal("fake_no_tx",
			"the ledger may only be locked inside a transaction: %s", customerID)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.sequence = append(m.sequence, "balance lock")

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

// references returns the ledger's references in order.
func (m *memStore) references() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]string, 0, len(m.entries))
	for i := range m.entries {
		out = append(out, m.entries[i].Reference)
	}

	return out
}

// balance is the fake's own sum, for assertions.
func (m *memStore) balance(customerID, currencyCode string) int64 {
	value, _ := m.LoyaltyBalance(context.Background(), customerID, currencyCode)

	return value
}
