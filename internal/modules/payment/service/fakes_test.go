package service_test

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// txMarkerKey is the fake store's "we are inside a transaction" marker.
type txMarkerKey struct{}

// fakeStore is the in-memory counterpart of service.Store.
//
// It DELIBERATELY imitates three behaviors of the real store, because the
// service's correctness rests on them:
//
//  1. A method that takes a lock returns an error when it is called OUTSIDE a
//     transaction. If the service forgets WithTx in a flow, the unit test
//     catches it; on the real database this bug would only show under a race,
//     because of the unlocked read.
//  2. When a transaction ends in an error, what it wrote is ROLLED BACK. The
//     claim "an error came back and nothing was written" can only be tested
//     this way.
//  3. At most ONE capture comes out of a session; this is the counterpart of
//     the unique constraint, and Capture's idempotency rests on it.
type fakeStore struct {
	mu          sync.Mutex
	collections map[string]models.PaymentCollection
	sessions    map[string]models.PaymentSession
	payments    map[string]models.Payment
	refunds     map[string]models.Refund
	// outbox holds the events written inside a transaction IN ORDER. The order
	// matters: we want to see that the event is written AFTER the money movement.
	outbox []outboxRow

	// locks records the locks taken IN ORDER ("collection", "session",
	// "payment"). The lock order is a concurrency contract, and on the real
	// database a violation only shows under a race (as a deadlock); here the
	// order can be read directly.
	locks []string
	// collectionWrites counts how many times the collection row was written;
	// it is how the idempotent branches are proved not to touch the amounts A
	// SECOND TIME.
	collectionWrites int
	// sessionWrites counts how many times the session row was written.
	sessionWrites int

	// failCreatePayment, when set, makes CreatePayment return this error; it is
	// used to test the transaction rollback path.
	failCreatePayment error

	// credit is the credit ledger per customer and currency (ADR 0152).
	//
	// In the real table the balance is the SUM of the rows; the fake keeps it the
	// same way — had it kept a single number, a bug that writes the hold as a
	// negative would be invisible in the tests.
	credit map[string][]models.StoreCreditEntry
	// creditLocks are the balances LockStoreCreditBalance was asked for.
	creditLocks []string

	// loyalty is the points ledger, and it is kept per customer and currency
	// (ADR 0164).
	loyalty map[string][]models.LoyaltyEntry

	// journal is what JournalMovements returns, and journalCalls records how it
	// was asked (ADR 0186).
	journal      []models.JournalMovement
	journalCalls []journalCall
	// caused is what CausedRefunds returns (ADR 0189).
	caused []models.CausedRefund

	// giftCards, giftDigests and giftEntries are the gift cards, their codes'
	// digests and their ledger (ADR 0208); failGiftEntry makes the issue's
	// ledger write fail.
	giftCards     map[string]models.GiftCard
	giftDigests   map[string]string
	giftEntries   map[string][]models.GiftCardEntry
	failGiftEntry error
	// giftHolds is how many sessions hold part of each card (ADR 0213), and
	// giftLocks the cards whose balance was locked, in order.
	giftHolds map[string]int64
	giftLocks []string
	// giftValidity is the validity each card insert was given (ADR 0214).
	giftValidity []int32
}

// newFakeStore builds an empty fake store.
func newFakeStore() *fakeStore {
	return &fakeStore{
		collections: map[string]models.PaymentCollection{},
		sessions:    map[string]models.PaymentSession{},
		payments:    map[string]models.Payment{},
		refunds:     map[string]models.Refund{},
	}
}

// It is verified at compile time that the fake store meets the surface the
// service expects.
var _ service.Store = (*fakeStore)(nil)

// WithTx runs fn inside a "transaction"; if fn returns an error, it rolls the
// state back.
func (f *fakeStore) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if ctx.Value(txMarkerKey{}) != nil {
		return fn(ctx)
	}

	f.mu.Lock()
	// BOTH LEDGERS go into the snapshot as well. Had they not, a test saying "the
	// transaction was rolled back, so the row was not written" would pass GREEN
	// while the opposite of what it proves was true: a real transaction rolls the
	// row back, a fake map does not.
	snapshot := struct {
		collections map[string]models.PaymentCollection
		sessions    map[string]models.PaymentSession
		payments    map[string]models.Payment
		refunds     map[string]models.Refund
		credit      map[string][]models.StoreCreditEntry
		loyalty     map[string][]models.LoyaltyEntry
		giftCards   map[string]models.GiftCard
		giftDigests map[string]string
		giftEntries map[string][]models.GiftCardEntry
	}{
		collections: maps.Clone(f.collections),
		sessions:    maps.Clone(f.sessions),
		payments:    maps.Clone(f.payments),
		refunds:     maps.Clone(f.refunds),
		credit:      maps.Clone(f.credit),
		loyalty:     maps.Clone(f.loyalty),
		giftCards:   maps.Clone(f.giftCards),
		giftDigests: maps.Clone(f.giftDigests),
		giftEntries: maps.Clone(f.giftEntries),
	}
	f.mu.Unlock()

	if err := fn(context.WithValue(ctx, txMarkerKey{}, true)); err != nil {
		f.mu.Lock()
		f.collections, f.sessions = snapshot.collections, snapshot.sessions
		f.payments, f.refunds = snapshot.payments, snapshot.refunds
		f.credit, f.loyalty = snapshot.credit, snapshot.loyalty
		f.giftCards, f.giftDigests, f.giftEntries = snapshot.giftCards, snapshot.giftDigests, snapshot.giftEntries
		f.mu.Unlock()
		return err
	}
	return nil
}

// WriteOutboxEvent records the event and REFUSES outside a transaction.
//
// Imitating the refusal matters: the real store refuses to write outside a
// transaction, and had the fake accepted, code that writes the event in the
// wrong place would pass green in the unit test.
func (f *fakeStore) WriteOutboxEvent(ctx context.Context, id, name string, data map[string]any) error {
	if ctx.Value(txMarkerKey{}) == nil {
		return errors.Internal("payment_query_failed",
			"an outbox event can only be written inside a transaction: %s", name)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.outbox = append(f.outbox, outboxRow{ID: id, Name: name, Data: data})

	return nil
}

// outboxRow is an outbox row as the fake keeps it.
type outboxRow struct {
	ID   string
	Name string
	Data map[string]any
}

// requireTx verifies that a method taking a lock is called inside a
// transaction.
func requireTx(ctx context.Context, op string) error {
	if ctx.Value(txMarkerKey{}) == nil {
		return errors.Internal("fake_tx_required", "%s was called outside a transaction", op)
	}
	return nil
}

// recordLock records a lock taken, in order.
func (f *fakeStore) recordLock(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.locks = append(f.locks, name)
}

// lockOrder returns the recorded lock order.
func (f *fakeStore) lockOrder() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.locks)
}

// writes returns the collection and session write counters.
func (f *fakeStore) writes() (collections, sessions int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.collectionWrites, f.sessionWrites
}

// --- collections -------------------------------------------------------------

// CreatePaymentCollection records the collection.
func (f *fakeStore) CreatePaymentCollection(
	_ context.Context,
	col models.PaymentCollection,
) (models.PaymentCollection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := time.Now().UTC()
	col.CreatedAt, col.UpdatedAt = now, now
	f.collections[col.ID] = col
	return col, nil
}

// GetPaymentCollection returns the collection.
func (f *fakeStore) GetPaymentCollection(_ context.Context, id string) (models.PaymentCollection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	col, ok := f.collections[id]
	if !ok {
		return models.PaymentCollection{}, errors.NotFound("fake_collection_not_found",
			"collection not found: %s", id)
	}
	return col, nil
}

// LockPaymentCollection locks the collection.
func (f *fakeStore) LockPaymentCollection(ctx context.Context, id string) (models.PaymentCollection, error) {
	if err := requireTx(ctx, "LockPaymentCollection"); err != nil {
		return models.PaymentCollection{}, err
	}
	f.recordLock("collection")
	return f.GetPaymentCollection(ctx, id)
}

// ListPaymentCollections filters and pages the collections.
func (f *fakeStore) ListPaymentCollections(
	_ context.Context,
	filter models.CollectionFilter,
) ([]models.PaymentCollection, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var matched []models.PaymentCollection
	for _, id := range slices.Sorted(maps.Keys(f.collections)) {
		col := f.collections[id]
		if filter.Reference != nil && col.Reference != *filter.Reference {
			continue
		}
		if filter.Status != nil && col.Status.String() != *filter.Status {
			continue
		}
		matched = append(matched, col)
	}

	total := int64(len(matched))
	if filter.Offset >= total {
		return []models.PaymentCollection{}, total, nil
	}
	end := min(filter.Offset+filter.Limit, total)
	return slices.Clone(matched[filter.Offset:end]), total, nil
}

// PaymentCollectionsByIDs returns the collections of a set of ids.
func (f *fakeStore) PaymentCollectionsByIDs(_ context.Context, ids []string) ([]models.PaymentCollection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]models.PaymentCollection, 0, len(ids))
	for _, id := range slices.Sorted(slices.Values(ids)) {
		if col, ok := f.collections[id]; ok {
			out = append(out, col)
		}
	}
	return out, nil
}

// PaymentMomentsByCollectionIDs computes the SAME two moments out of the fake's
// own rows: the first capture and the last refund.
//
// Applying the same rule is compulsory — a fake that behaves differently lets a
// test pass over behavior the database does not have. A moment that never
// happened stays nil.
func (f *fakeStore) PaymentMomentsByCollectionIDs(
	_ context.Context, ids []string,
) ([]models.PaymentMoments, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]models.PaymentMoments, 0, len(ids))
	for _, id := range slices.Sorted(slices.Values(ids)) {
		if _, ok := f.collections[id]; !ok {
			continue
		}

		moment := models.PaymentMoments{CollectionID: id}
		for paymentID := range f.payments {
			payment := f.payments[paymentID]
			if payment.PaymentCollectionID != id {
				continue
			}
			if moment.FirstCapturedAt == nil || payment.CapturedAt.Before(*moment.FirstCapturedAt) {
				captured := payment.CapturedAt
				moment.FirstCapturedAt = &captured
			}
			for refundID := range f.refunds {
				refund := f.refunds[refundID]
				if refund.PaymentID != paymentID {
					continue
				}
				if moment.LastRefundedAt == nil || refund.CreatedAt.After(*moment.LastRefundedAt) {
					created := refund.CreatedAt
					moment.LastRefundedAt = &created
				}
			}
		}
		out = append(out, moment)
	}

	return out, nil
}

// PaymentMovementsByCollectionIDs reports every capture and refund, ordered as
// the real query orders them: by collection, then by moment, then by id.
func (f *fakeStore) PaymentMovementsByCollectionIDs(
	_ context.Context, ids []string,
) ([]models.PaymentMovement, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []models.PaymentMovement
	for paymentID := range f.payments {
		payment := f.payments[paymentID]
		if !slices.Contains(ids, payment.PaymentCollectionID) {
			continue
		}
		out = append(out, models.PaymentMovement{
			CollectionID: payment.PaymentCollectionID, ID: paymentID, PaymentID: paymentID,
			Kind: models.MovementCapture, Amount: payment.Amount, At: payment.CapturedAt,
		})
		for refundID := range f.refunds {
			refund := f.refunds[refundID]
			if refund.PaymentID != paymentID {
				continue
			}
			out = append(out, models.PaymentMovement{
				CollectionID: payment.PaymentCollectionID, ID: refundID, PaymentID: paymentID,
				Kind: models.MovementRefund, Amount: refund.Amount, At: refund.CreatedAt,
				Reference: refund.Reference,
			})
		}
	}
	slices.SortFunc(out, func(a, b models.PaymentMovement) int {
		if c := strings.Compare(a.CollectionID, b.CollectionID); c != 0 {
			return c
		}
		if c := a.At.Compare(b.At); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})

	return out, nil
}

// UpdatePaymentCollectionTotals writes the amounts and the status.
func (f *fakeStore) UpdatePaymentCollectionTotals(
	_ context.Context,
	id string,
	status models.CollectionStatus,
	authorized, captured, refunded int64,
) (models.PaymentCollection, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	col, ok := f.collections[id]
	if !ok {
		return models.PaymentCollection{}, errors.NotFound("fake_collection_not_found",
			"collection not found: %s", id)
	}
	col.Status = status
	col.AuthorizedAmount = authorized
	col.CapturedAmount = captured
	col.RefundedAmount = refunded
	col.UpdatedAt = time.Now().UTC()
	f.collections[id] = col
	f.collectionWrites++
	return col, nil
}

// --- sessions ----------------------------------------------------------------

// CreatePaymentSession records the session.
func (f *fakeStore) CreatePaymentSession(
	_ context.Context,
	ses models.PaymentSession,
) (models.PaymentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for id := range f.sessions {
		if f.sessions[id].ProviderID == ses.ProviderID &&
			f.sessions[id].IdempotencyKey == ses.IdempotencyKey {
			return models.PaymentSession{}, errors.Conflict("fake_session_exists",
				"a session with this key exists: %s", ses.IdempotencyKey)
		}
	}

	now := time.Now().UTC()
	ses.CreatedAt, ses.UpdatedAt = now, now
	f.sessions[ses.ID] = ses
	f.sessionWrites++
	return ses, nil
}

// GetPaymentSession returns the session.
func (f *fakeStore) GetPaymentSession(_ context.Context, id string) (models.PaymentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	ses, ok := f.sessions[id]
	if !ok {
		return models.PaymentSession{}, errors.NotFound("fake_session_not_found", "session not found: %s", id)
	}
	return ses, nil
}

// LockPaymentSession locks the session.
func (f *fakeStore) LockPaymentSession(ctx context.Context, id string) (models.PaymentSession, error) {
	if err := requireTx(ctx, "LockPaymentSession"); err != nil {
		return models.PaymentSession{}, err
	}
	f.recordLock("session")
	return f.GetPaymentSession(ctx, id)
}

// PaymentSessionByIdempotencyKey returns the session by its key.
func (f *fakeStore) PaymentSessionByIdempotencyKey(
	_ context.Context,
	providerID, key string,
) (models.PaymentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, id := range slices.Sorted(maps.Keys(f.sessions)) {
		ses := f.sessions[id]
		if ses.ProviderID == providerID && ses.IdempotencyKey == key {
			return ses, nil
		}
	}
	return models.PaymentSession{}, errors.NotFound("fake_session_not_found",
		"no session with key: %s", key)
}

// ListPaymentSessionsByCollection returns the collection's sessions.
func (f *fakeStore) ListPaymentSessionsByCollection(
	_ context.Context,
	collectionID string,
) ([]models.PaymentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := []models.PaymentSession{}
	for _, id := range slices.Sorted(maps.Keys(f.sessions)) {
		if f.sessions[id].PaymentCollectionID == collectionID {
			out = append(out, f.sessions[id])
		}
	}
	return out, nil
}

// SessionsOfCollections returns the sessions of the given collections.
func (f *fakeStore) SessionsOfCollections(_ context.Context, ids []string) ([]models.PaymentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := []models.PaymentSession{}
	for _, id := range slices.Sorted(maps.Keys(f.sessions)) {
		if slices.Contains(ids, f.sessions[id].PaymentCollectionID) {
			out = append(out, f.sessions[id])
		}
	}
	return out, nil
}

// ListOverdueOfflineSessions applies the real query's conditions: authorized,
// of a named provider, opened before that provider's cutoff, after the key, in
// a collection that captured nothing; oldest first, then by id.
func (f *fakeStore) ListOverdueOfflineSessions(
	_ context.Context, providerIDs []string, cutoffs []time.Time,
	afterCreatedAt time.Time, afterID string, limit int32,
) ([]models.PaymentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := []models.PaymentSession{}
	for id := range f.sessions {
		ses := f.sessions[id]
		at := slices.Index(providerIDs, ses.ProviderID)
		if ses.Status != models.SessionAuthorized || at < 0 || !ses.CreatedAt.Before(cutoffs[at]) {
			continue
		}
		if ses.CreatedAt.Before(afterCreatedAt) || (ses.CreatedAt.Equal(afterCreatedAt) && ses.ID <= afterID) {
			continue
		}
		if f.collections[ses.PaymentCollectionID].CapturedAmount != 0 {
			continue
		}
		out = append(out, ses)
	}
	slices.SortFunc(out, func(a, b models.PaymentSession) int {
		if order := a.CreatedAt.Compare(b.CreatedAt); order != 0 {
			return order
		}
		return strings.Compare(a.ID, b.ID)
	})
	if len(out) > int(limit) {
		out = out[:limit]
	}

	return out, nil
}

// ListSessionsForReconciliation returns the suspect set for reconciliation.
//
// It applies BOTH of the real query's conditions — authorized, and last written
// before the given instant — and orders the result by updated_at. Without the
// fake imitating them, the service's claim to narrow the set could not be
// falsified by any test.
//
// There used to be a third condition, "not deleted". The column is gone
// (ADR 0054) and so is the branch that mirrored it. The excluded providers are
// the query's third condition since ADR 0284.
func (f *fakeStore) ListSessionsForReconciliation(
	_ context.Context,
	unchangedSince time.Time,
	excluded []string,
	limit int32,
) ([]models.PaymentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := []models.PaymentSession{}
	for _, id := range slices.Sorted(maps.Keys(f.sessions)) {
		ses := f.sessions[id]
		if ses.Status != models.SessionAuthorized || slices.Contains(excluded, ses.ProviderID) {
			continue
		}
		if !ses.UpdatedAt.Before(unchangedSince) {
			continue
		}
		out = append(out, ses)
	}

	slices.SortStableFunc(out, func(a, b models.PaymentSession) int {
		return a.UpdatedAt.Compare(b.UpdatedAt)
	})

	if int32(len(out)) > limit {
		out = out[:limit]
	}
	return out, nil
}

// SessionCounts counts the collection's sessions by status.
func (f *fakeStore) SessionCounts(_ context.Context, collectionID string) (models.SessionCounts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var counts models.SessionCounts
	for id := range f.sessions {
		if f.sessions[id].PaymentCollectionID != collectionID {
			continue
		}
		counts.Total++
		switch f.sessions[id].Status {
		case models.SessionPending, models.SessionAuthorized:
			counts.Live++
		case models.SessionCanceled:
			counts.Canceled++
		case models.SessionFailed:
			counts.Failed++
		case models.SessionCaptured:
			// A captured session enters no count; the collection's status is
			// derived from the captured amount anyway.
		}
	}
	return counts, nil
}

// LiveSessionAmount sums the amount the live sessions reserve.
//
// The real query's rule is imitated EXACTLY: a pending session reserves its own
// amount, an authorized session the authorized amount. Had the rule been
// loosened here, the claim "two full-amount sessions cannot be opened" would
// hold in the unit test but not on the real database.
func (f *fakeStore) LiveSessionAmount(_ context.Context, collectionID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var reserved int64
	for id := range f.sessions {
		ses := f.sessions[id]
		if ses.PaymentCollectionID != collectionID {
			continue
		}
		switch ses.Status {
		case models.SessionPending:
			reserved += ses.Amount
		case models.SessionAuthorized:
			reserved += ses.AuthorizedAmount
		case models.SessionCaptured, models.SessionCanceled, models.SessionFailed:
			// A terminated session reserves no amount.
		}
	}
	return reserved, nil
}

// UpdatePaymentSessionState writes the session's state.
func (f *fakeStore) UpdatePaymentSessionState(
	_ context.Context,
	id string,
	status models.SessionStatus,
	authorizedAmount int64,
	data []byte,
	declineReason string,
) (models.PaymentSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	ses, ok := f.sessions[id]
	if !ok {
		return models.PaymentSession{}, errors.NotFound("fake_session_not_found", "session not found: %s", id)
	}
	ses.Status = status
	ses.AuthorizedAmount = authorizedAmount
	ses.Data = json.RawMessage(data)
	ses.DeclineReason = declineReason
	ses.UpdatedAt = time.Now().UTC()
	f.sessions[id] = ses
	f.sessionWrites++
	return ses, nil
}

// --- captures and refunds ----------------------------------------------------

// CreatePayment records the capture; at most one per session.
func (f *fakeStore) CreatePayment(_ context.Context, pay models.Payment) (models.Payment, error) {
	if f.failCreatePayment != nil {
		return models.Payment{}, f.failCreatePayment
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	for id := range f.payments {
		if f.payments[id].PaymentSessionID == pay.PaymentSessionID {
			return models.Payment{}, errors.Conflict("fake_payment_exists",
				"a capture has already come out of this session: %s", pay.PaymentSessionID)
		}
	}

	now := time.Now().UTC()
	pay.CreatedAt, pay.UpdatedAt = now, now
	f.payments[pay.ID] = pay
	return pay, nil
}

// GetPayment returns the capture.
func (f *fakeStore) GetPayment(_ context.Context, id string) (models.Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	pay, ok := f.payments[id]
	if !ok {
		return models.Payment{}, errors.NotFound("fake_payment_not_found", "capture not found: %s", id)
	}
	return pay, nil
}

// LockPayment locks the capture.
func (f *fakeStore) LockPayment(ctx context.Context, id string) (models.Payment, error) {
	if err := requireTx(ctx, "LockPayment"); err != nil {
		return models.Payment{}, err
	}
	f.recordLock("payment")
	return f.GetPayment(ctx, id)
}

// PaymentBySession returns the capture born of the session.
func (f *fakeStore) PaymentBySession(_ context.Context, sessionID string) (models.Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, id := range slices.Sorted(maps.Keys(f.payments)) {
		if f.payments[id].PaymentSessionID == sessionID {
			return f.payments[id], nil
		}
	}
	return models.Payment{}, errors.NotFound("fake_payment_not_found",
		"no capture from session: %s", sessionID)
}

// ListPaymentsByCollection returns the collection's captures.
func (f *fakeStore) ListPaymentsByCollection(_ context.Context, collectionID string) ([]models.Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := []models.Payment{}
	for _, id := range slices.Sorted(maps.Keys(f.payments)) {
		if f.payments[id].PaymentCollectionID == collectionID {
			out = append(out, f.payments[id])
		}
	}
	return out, nil
}

// UpdatePaymentRefundedAmount writes the refunded amount.
func (f *fakeStore) UpdatePaymentRefundedAmount(
	_ context.Context,
	id string,
	refunded int64,
) (models.Payment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	pay, ok := f.payments[id]
	if !ok {
		return models.Payment{}, errors.NotFound("fake_payment_not_found", "capture not found: %s", id)
	}
	pay.RefundedAmount = refunded
	pay.UpdatedAt = time.Now().UTC()
	f.payments[id] = pay
	return pay, nil
}

// CreateRefund records the refund.
func (f *fakeStore) CreateRefund(_ context.Context, ref models.Refund) (models.Refund, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := time.Now().UTC()
	ref.CreatedAt, ref.UpdatedAt = now, now
	f.refunds[ref.ID] = ref
	return ref, nil
}

// ListRefundsByPayment returns the capture's refunds.
func (f *fakeStore) ListRefundsByPayment(_ context.Context, paymentID string) ([]models.Refund, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := []models.Refund{}
	for _, id := range slices.Sorted(maps.Keys(f.refunds)) {
		if f.refunds[id].PaymentID == paymentID {
			out = append(out, f.refunds[id])
		}
	}
	return out, nil
}

// --- fake provider -----------------------------------------------------------

// fakeProvider is a payment provider whose answers a test sets as a scenario.
//
// It is used instead of a real provider so that the service's DECISIONS can be
// tested independently of the provider's behavior: a decline, an error and a
// partial authorization can each be set up here in a single line.
type fakeProvider struct {
	mu sync.Mutex

	id string
	// nextStatus is the status the next Authorize returns.
	nextStatus coreprovider.SessionStatus
	// authorizedAmount, when non-zero, is the amount Authorize reports.
	authorizedAmount int64
	// declineReason is the reason for a decline.
	declineReason string
	// authorizeData is the raw body Authorize returns. Leaving it empty is
	// REALISTIC: most providers return no body in their authorization response,
	// and the module then has to KEEP the session's existing data.
	authorizeData json.RawMessage
	// authorizeErr, when set, makes Authorize return this error.
	authorizeErr error
	// captureErr, when set, makes Capture return this error.
	captureErr error
	// cancelErr, when set, makes Cancel return this error.
	cancelErr error
	// createErr, when set, makes CreateSession return this error.
	createErr error

	// createCalls, authorizeCalls, captureCalls, refundCalls and cancelCalls
	// count HOW MANY TIMES the provider was called. These counters are how the
	// idempotent branches are proved never to reach the provider.
	createCalls    int
	authorizeCalls int
	captureCalls   int
	refundCalls    int
	cancelCalls    int

	// sessions are the provider sessions opened (key -> id).
	sessions map[string]string
}

// newFakeProvider builds a provider whose default behavior is "authorize".
func newFakeProvider(id string) *fakeProvider {
	return &fakeProvider{
		id:         id,
		nextStatus: coreprovider.SessionAuthorized,
		sessions:   map[string]string{},
	}
}

// It is verified at compile time that the fake provider meets the core
// contract.
var _ coreprovider.PaymentProvider = (*fakeProvider)(nil)

// ID returns the provider's id.
func (p *fakeProvider) ID() string { return p.id }

// CreateSession opens a session on the provider's side.
func (p *fakeProvider) CreateSession(
	_ context.Context,
	in coreprovider.CreateSessionInput,
) (coreprovider.Session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.createErr != nil {
		return coreprovider.Session{}, p.createErr
	}
	p.createCalls++

	id, ok := p.sessions[in.IdempotencyKey]
	if !ok {
		id = "ext_" + in.IdempotencyKey
		p.sessions[in.IdempotencyKey] = id
	}
	return coreprovider.Session{
		ID:           id,
		Status:       coreprovider.SessionPending,
		Amount:       in.Amount,
		CurrencyCode: in.CurrencyCode,
		Data:         json.RawMessage(`{"fake":true}`),
	}, nil
}

// Authorize returns the result the scenario set.
func (p *fakeProvider) Authorize(_ context.Context, _ string) (coreprovider.AuthResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.authorizeErr != nil {
		return coreprovider.AuthResult{}, p.authorizeErr
	}
	p.authorizeCalls++
	return coreprovider.AuthResult{
		Status:           p.nextStatus,
		AuthorizedAmount: p.authorizedAmount,
		Data:             p.authorizeData,
		DeclineReason:    p.declineReason,
	}, nil
}

// Capture records the capture.
func (p *fakeProvider) Capture(_ context.Context, _ string, _ int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.captureErr != nil {
		return p.captureErr
	}
	p.captureCalls++
	return nil
}

// Refund records the refund.
func (p *fakeProvider) Refund(_ context.Context, _ string, _ int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refundCalls++
	return nil
}

// Cancel records the cancellation.
func (p *fakeProvider) Cancel(_ context.Context, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cancelErr != nil {
		return p.cancelErr
	}
	p.cancelCalls++
	return nil
}

// calls returns how many calls were made to the provider, per method.
func (p *fakeProvider) calls() (create, authorize, capture, refund, cancel int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.createCalls, p.authorizeCalls, p.captureCalls, p.refundCalls, p.cancelCalls
}

// scenario sets the provider's next answer.
func (p *fakeProvider) scenario(status coreprovider.SessionStatus, amount int64, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextStatus = status
	p.authorizedAmount = amount
	p.declineReason = reason
}

// setAuthorizeData sets the raw body Authorize returns.
func (p *fakeProvider) setAuthorizeData(data json.RawMessage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.authorizeData = data
}

// --- store credit ledger (ADR 0152) ------------------------------------------

// creditKey is the ledger's key: credit is kept separately per currency.
func creditKey(customerID, currencyCode string) string {
	return customerID + "\x00" + currencyCode
}

// AppendStoreCreditEntry appends a single event to the ledger.
func (f *fakeStore) AppendStoreCreditEntry(
	_ context.Context, entry models.StoreCreditEntry,
) (models.StoreCreditEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.credit == nil {
		f.credit = map[string][]models.StoreCreditEntry{}
	}
	key := creditKey(entry.CustomerID, entry.CurrencyCode)
	f.credit[key] = append(f.credit[key], entry)

	return entry, nil
}

// StoreCreditBalance answers what can be spent, the way the statement does
// (ADR 0258): the sum less what expired credit still holds.
func (f *fakeStore) StoreCreditBalance(
	_ context.Context, customerID, currencyCode string,
) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	figures := creditFigures(f.credit[creditKey(customerID, currencyCode)], time.Now())

	return figures.Balance - figures.Due(), nil
}

// creditFigures computes a balance's expiry figures at a moment, by the rule
// StoreCreditExpiryFigures states in SQL.
func creditFigures(entries []models.StoreCreditEntry, at time.Time) models.StoreCreditExpiryFigures {
	var figures models.StoreCreditExpiryFigures
	for i := range entries {
		entry := &entries[i]
		figures.Balance += entry.Amount
		expired := entry.ExpiresAt != nil && !entry.ExpiresAt.After(at)
		switch {
		case entry.Kind == models.StoreCreditRefund,
			entry.Kind == models.StoreCreditIssue && !expired:
			figures.Unexpired += entry.Amount
		case entry.Kind == models.StoreCreditIssue:
			figures.Expired += entry.Amount
		case entry.Kind == models.StoreCreditExpire:
			figures.Written -= entry.Amount
		}
	}

	return figures
}

// LockStoreCreditBalance refuses outside a transaction and records the lock.
func (f *fakeStore) LockStoreCreditBalance(ctx context.Context, customerID, currencyCode string) error {
	if ctx.Value(txMarkerKey{}) == nil {
		return errors.New("LockStoreCreditBalance called outside a transaction")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creditLocks = append(f.creditLocks, creditKey(customerID, currencyCode))

	return nil
}

// StoreCreditExpiryDue lists the balances holding an expired issue whose
// figures leave something to take back, in key order.
func (f *fakeStore) StoreCreditExpiryDue(
	_ context.Context, at time.Time, limit int64,
) ([]models.StoreCreditBalanceRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []models.StoreCreditBalanceRef
	for _, key := range slices.Sorted(maps.Keys(f.credit)) {
		entries := f.credit[key]
		if len(entries) == 0 || creditFigures(entries, at).Due() == 0 {
			continue
		}
		out = append(out, models.StoreCreditBalanceRef{
			CustomerID: entries[0].CustomerID, CurrencyCode: entries[0].CurrencyCode,
		})
		if int64(len(out)) == limit {
			break
		}
	}

	return out, nil
}

// StoreCreditExpiryFigures computes one balance's figures.
func (f *fakeStore) StoreCreditExpiryFigures(
	_ context.Context, balance models.StoreCreditBalanceRef, at time.Time,
) (models.StoreCreditExpiryFigures, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return creditFigures(f.credit[creditKey(balance.CustomerID, balance.CurrencyCode)], at), nil
}

// ListStoreCreditEntries returns the history newest first, only the credits
// issued for orderID when it is given (ADR 0274).
func (f *fakeStore) ListStoreCreditEntries(
	_ context.Context, customerID, currencyCode, orderID string, limit, offset int64,
) ([]models.StoreCreditEntry, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	all := f.credit[creditKey(customerID, currencyCode)]
	out := make([]models.StoreCreditEntry, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		if orderID != "" && all[i].OrderID != orderID {
			continue
		}
		out = append(out, all[i])
	}
	total := int64(len(out))

	if int(offset) >= len(out) {
		return nil, total, nil
	}
	out = out[offset:]
	if int(limit) < len(out) {
		out = out[:limit]
	}

	return out, total, nil
}

// --- loyalty points ledger (ADR 0164) ----------------------------------------

// AppendLoyaltyEntry appends a single row to the points ledger.
func (f *fakeStore) AppendLoyaltyEntry(
	_ context.Context, entry models.LoyaltyEntry,
) (models.LoyaltyEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.loyalty == nil {
		f.loyalty = map[string][]models.LoyaltyEntry{}
	}
	key := creditKey(entry.CustomerID, entry.CurrencyCode)
	f.loyalty[key] = append(f.loyalty[key], entry)

	return entry, nil
}

// LoyaltyPointsForReference sums the points written for a collection.
//
// Because the ledger is kept under the customer's key, the total is searched
// across ALL keys: the target's subject is the collection, the storage's key is
// the customer, and mixing the two up would be exactly what the real query does
// not do.
func (f *fakeStore) LoyaltyPointsForReference(_ context.Context, reference string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var points int64
	for key := range f.loyalty {
		entries := f.loyalty[key]
		for i := range entries {
			// Only the earning rows, like the real query's kind filter: a spend
			// row does not enter the target even if it references the collection.
			if entries[i].Reference == reference &&
				(entries[i].Kind == models.LoyaltyEarn || entries[i].Kind == models.LoyaltyReverse) {
				points += entries[i].Points
			}
		}
	}

	return points, nil
}

// CollectionNetCapturedExcludingProviders does what the real query does: it
// filters the collection's captures by their sessions' providers and sums
// amount less refunds. That is the earn base, not the collection row's own
// totals, which do not know the provider (ADR 0165).
func (f *fakeStore) CollectionNetCapturedExcludingProviders(
	_ context.Context, collectionID string, excludedProviderIDs []string,
) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var net int64
	for id := range f.payments {
		payment := f.payments[id]
		if payment.PaymentCollectionID != collectionID {
			continue
		}
		if slices.Contains(excludedProviderIDs, f.sessions[payment.PaymentSessionID].ProviderID) {
			continue
		}
		net += payment.Amount - payment.RefundedAmount
	}

	return net, nil
}

// LoyaltyBalance returns the sum of the rows.
func (f *fakeStore) LoyaltyBalance(
	_ context.Context, customerID, currencyCode string,
) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var points int64
	entries := f.loyalty[creditKey(customerID, currencyCode)]
	for i := range entries {
		points += entries[i].Points
	}

	return points, nil
}

// ListLoyaltyEntries returns the history newest first.
func (f *fakeStore) ListLoyaltyEntries(
	_ context.Context, customerID, currencyCode string, limit, offset int64,
) ([]models.LoyaltyEntry, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	all := f.loyalty[creditKey(customerID, currencyCode)]
	total := int64(len(all))

	out := make([]models.LoyaltyEntry, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		out = append(out, all[i])
	}

	if int(offset) >= len(out) {
		return nil, total, nil
	}
	out = out[offset:]
	if int(limit) < len(out) {
		out = out[:limit]
	}

	return out, total, nil
}
