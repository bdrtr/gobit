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
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// txMarkerKey is the fake store's "we are inside a transaction" marker.
type txMarkerKey struct{}

// readSnapshotKey is the context key of the snapshot of a read-only
// transaction.
type readSnapshotKey struct{}

// fakeSnapshot is the complete state of the fake store at one moment.
//
// Both the view of a read-only transaction and the rollback point of a writing
// transaction are carried with this type; both are "the copy of the store at
// that moment".
type fakeSnapshot struct {
	orders    map[string]models.Order
	items     map[string]models.OrderLineItem
	summaries map[string]models.OrderSummary
	addresses map[string][]models.OrderAddress
	returns   map[string]models.Return
	outbox    map[string]outboxRow
	retItems  map[string]models.ReturnItem
	exchanges map[string]models.Exchange
	claims    map[string]models.Claim
}

// fakeStore is the in-memory counterpart of service.Store.
//
// # What it imitates and what it DOES NOT
//
// The fake only imitates the things THE DATABASE does:
//
//  1. display_id is produced by the STORE (the counterpart of the IDENTITY
//     column). Had the service tried to produce the number itself, the tests
//     would see it.
//  2. A method that takes a lock returns an error if it is called OUTSIDE a
//     transaction.
//  3. If the transaction ends with an error what was written is ROLLED BACK.
//  4. The uniqueness of idempotency_key: the counterpart of the partial unique
//     index in the migration; soft-deleted records are outside the constraint.
//  5. The WHERE condition of the status transition queries: on an order that is
//     not in the expected status no row is affected and a Conflict is returned.
//  6. The merging of the summary amounts with GREATEST: the query keeps the
//     LARGER of the recorded value and the reported value.
//  7. Foreign key: a child record cannot be attached to an order that does not
//     exist.
//  8. The SNAPSHOT of a read-only transaction (the counterpart of REPEATABLE
//     READ).
//
// No rule that is THE SERVICE's responsibility is REPEATED here: the fake does
// not validate the total identity, does not prevent a return record from being
// opened on a canceled order and does not silently count the "already canceled"
// case as a success. Otherwise the related tests would pass even if that check
// were deleted from the service — what the test proved would be the behavior of
// the fake.
type fakeStore struct {
	mu        sync.Mutex
	orders    map[string]models.Order
	items     map[string]models.OrderLineItem
	summaries map[string]models.OrderSummary
	addresses map[string][]models.OrderAddress
	returns   map[string]models.Return
	outbox    map[string]outboxRow
	retItems  map[string]models.ReturnItem
	exchanges map[string]models.Exchange
	claims    map[string]models.Claim

	// seq gives the added records an increasing timestamp; the listing order
	// being deterministic rests on this.
	seq int
	// displaySeq is the sequence of the order number and it starts at 1.
	displaySeq int64
	// forceDisplayID, when it is set, makes the produced number be this one. It
	// imitates a broken sequence (or a direct SQL intervention).
	forceDisplayID *int64

	// lockedOrders records the locked orders IN ORDER. Whether a lock was taken
	// is a concurrency contract and in a real database its violation only shows
	// up under a race; here it can be read directly.
	lockedOrders []string
	// lockedReturns records the return rows that were locked, in order.
	lockedReturns []string
	// lockedClaims records the claim rows that were locked, in order.
	lockedClaims []string

	// spendingLocks records the customers whose spending lock was taken IN
	// ORDER.
	spendingLocks []string
	// spendingSums counts how many times the spend sum was read. On a customer
	// without a limit it has to stay ZERO: loading an extra query onto an order
	// that has no rule is not free.
	spendingSums int

	// failCreateLineItem, when it is set, makes CreateLineItem return this
	// error; it is used to exercise the transaction rollback path.
	failCreateLineItem error
	// failCreateSummary, when it is set, makes CreateSummary return this error.
	failCreateSummary error

	// hookCreateOrder, when it is set, is called ONCE at the BEGINNING of
	// CreateOrder and is cleared afterwards.
	//
	// It exists to set up the RACE of idempotent calls: in a real database the
	// "both calls failed to find the key, both attempted to write" situation
	// depends on timing and cannot be produced deterministically in a test.
	hookCreateOrder func()
}

// newFakeStore produces an empty fake store.
func newFakeStore() *fakeStore {
	return &fakeStore{
		orders:    map[string]models.Order{},
		items:     map[string]models.OrderLineItem{},
		summaries: map[string]models.OrderSummary{},
		addresses: map[string][]models.OrderAddress{},
		returns:   map[string]models.Return{},
		outbox:    map[string]outboxRow{},
		retItems:  map[string]models.ReturnItem{},
		exchanges: map[string]models.Exchange{},
		claims:    map[string]models.Claim{},
	}
}

// That the fake store satisfies the surface the service expects is verified at
// compile time.
var _ service.Store = (*fakeStore)(nil)

// nextStamp produces the next increasing timestamp. The caller has to hold the
// lock.
func (f *fakeStore) nextStamp() time.Time {
	f.seq++
	return time.Unix(0, 0).UTC().Add(time.Duration(f.seq) * time.Millisecond)
}

// snapshot takes the copy of the store at that moment.
func (f *fakeStore) snapshot() fakeSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeSnapshot{
		orders:    maps.Clone(f.orders),
		items:     maps.Clone(f.items),
		summaries: maps.Clone(f.summaries),
		addresses: maps.Clone(f.addresses),
		returns:   maps.Clone(f.returns),
		outbox:    maps.Clone(f.outbox),
		retItems:  maps.Clone(f.retItems),
		exchanges: maps.Clone(f.exchanges),
		claims:    maps.Clone(f.claims),
	}
}

// WithTx runs fn inside a "transaction"; if it returns an error what THE
// TRANSACTION ITSELF wrote is rolled back.
//
// The rollback is done NOT by reverting the whole store to a copy but by
// running, in reverse order, the undo record kept for every write made during
// the transaction. The difference matters: a wholesale copy would also erase
// what ANOTHER transaction wrote while this one was running, and concurrent
// scenarios (the race of idempotent calls, for instance) would break in a way
// that does not happen in a real database.
//
// [fakeStore.displaySeq] is DELIBERATELY not rolled back: in PostgreSQL a
// sequence is not rewound together with the transaction either, a rolled back
// INSERT consumes the number.
func (f *fakeStore) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txMarkerKey{}).(*txState); ok {
		return fn(ctx)
	}

	state := &txState{}
	if err := fn(context.WithValue(ctx, txMarkerKey{}, state)); err != nil {
		f.mu.Lock()
		for i := len(state.undos) - 1; i >= 0; i-- {
			state.undos[i]()
		}
		f.mu.Unlock()
		return err
	}
	return nil
}

// txState is the undo record of a transaction.
type txState struct {
	// undos are the undoers of the writes that were made; they are run in
	// reverse order.
	undos []func()
}

// recordUndo records the undoer of a write into the transaction.
//
// Writes outside a transaction cannot be undone; in a real database an INSERT
// without a transaction is durable immediately too.
func (f *fakeStore) recordUndo(ctx context.Context, undo func()) {
	if state, ok := ctx.Value(txMarkerKey{}).(*txState); ok {
		state.undos = append(state.undos, undo)
	}
}

// undoEntry produces the closure that restores the state of a map entry AS IT
// WAS BEFORE THE WRITE.
func undoEntry[V any](m map[string]V, key string) func() {
	prev, existed := m[key]
	return func() {
		if existed {
			m[key] = prev
			return
		}
		delete(m, key)
	}
}

// WithReadTx runs fn inside a read with a single snapshot.
func (f *fakeStore) WithReadTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, inTx := ctx.Value(txMarkerKey{}).(*txState); inTx || ctx.Value(readSnapshotKey{}) != nil {
		return fn(ctx)
	}
	snapshot := f.snapshot()
	return fn(context.WithValue(ctx, readSnapshotKey{}, &snapshot))
}

// view returns the state the read will see: the snapshot when it is inside a
// read-only transaction, the live state otherwise.
func (f *fakeStore) view(ctx context.Context) fakeSnapshot {
	if snapshot, ok := ctx.Value(readSnapshotKey{}).(*fakeSnapshot); ok {
		return *snapshot
	}
	return f.snapshot()
}

// requireTx verifies that the methods taking a lock are called inside a
// transaction.
func requireTx(ctx context.Context, op string) error {
	if _, ok := ctx.Value(txMarkerKey{}).(*txState); !ok {
		return errors.Internal("fake_tx_required", "%s has to be called inside a transaction", op)
	}
	return nil
}

// notFound is the error of a missing order.
func notFound(id string) error {
	return errors.NotFound("order_not_found", "the order was not found: %s", id)
}

// --- orders ------------------------------------------------------------------

// CreateOrder writes a new order and its number is produced by the STORE.
func (f *fakeStore) CreateOrder(ctx context.Context, order models.Order) (models.Order, error) {
	if hook := f.takeCreateHook(); hook != nil {
		hook()
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if order.IdempotencyKey != "" {
		// The loop is walked BY KEY: the order structure is large and copying it
		// by value would carry a few hundred bytes for nothing on every turn.
		for id := range f.orders {
			if f.orders[id].DeletedAt == nil && f.orders[id].IdempotencyKey == order.IdempotencyKey {
				return models.Order{}, errors.Conflict("order_idempotency_key_taken",
					"an order with this idempotency key already exists")
			}
		}
	}

	f.displaySeq++
	order.DisplayID = f.displaySeq
	if f.forceDisplayID != nil {
		order.DisplayID = *f.forceDisplayID
	}
	stamp := f.nextStamp()
	order.PlacedAt = stamp
	order.CreatedAt = stamp
	order.UpdatedAt = stamp
	f.recordUndo(ctx, undoEntry(f.orders, order.ID))
	f.orders[order.ID] = order
	return order, nil
}

// takeCreateHook takes the hook if there is one and clears it.
func (f *fakeStore) takeCreateHook() func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	hook := f.hookCreateOrder
	f.hookCreateOrder = nil
	return hook
}

// GetOrder returns the order by its identifier.
func (f *fakeStore) GetOrder(ctx context.Context, id string) (models.Order, error) {
	order, ok := f.view(ctx).orders[id]
	if !ok || order.DeletedAt != nil {
		return models.Order{}, notFound(id)
	}
	return order, nil
}

// GetOrderByDisplayID returns the order by its number.
func (f *fakeStore) GetOrderByDisplayID(ctx context.Context, displayID int64) (models.Order, error) {
	snapshot := f.view(ctx)
	for id := range snapshot.orders {
		if snapshot.orders[id].DeletedAt == nil && snapshot.orders[id].DisplayID == displayID {
			return snapshot.orders[id], nil
		}
	}
	return models.Order{}, errors.NotFound("order_not_found", "the order was not found: #%d", displayID)
}

// GetOrderByIdempotencyKey returns the order opened with the key.
func (f *fakeStore) GetOrderByIdempotencyKey(ctx context.Context, key string) (models.Order, error) {
	snapshot := f.view(ctx)
	for id := range snapshot.orders {
		order := snapshot.orders[id]
		if order.DeletedAt == nil && order.IdempotencyKey != "" && order.IdempotencyKey == key {
			return order, nil
		}
	}
	return models.Order{}, errors.NotFound("order_not_found",
		"no order was found with this idempotency key")
}

// LockOrder locks the order; it can only be called inside a transaction.
func (f *fakeStore) LockOrder(ctx context.Context, id string) (models.Order, error) {
	if err := requireTx(ctx, "LockOrder"); err != nil {
		return models.Order{}, err
	}

	f.mu.Lock()
	f.lockedOrders = append(f.lockedOrders, id)
	order, ok := f.orders[id]
	f.mu.Unlock()

	if !ok || order.DeletedAt != nil {
		return models.Order{}, notFound(id)
	}
	return order, nil
}

// ListOrders filters and pages the orders.
func (f *fakeStore) ListOrders(ctx context.Context, filter models.OrderFilter) ([]models.Order, int64, error) {
	snapshot := f.view(ctx)
	matched := make([]models.Order, 0, len(snapshot.orders))
	for id := range snapshot.orders {
		if snapshot.orders[id].DeletedAt != nil {
			continue
		}
		if filter.CustomerID != nil && snapshot.orders[id].CustomerID != *filter.CustomerID {
			continue
		}
		if filter.RegionID != nil && snapshot.orders[id].RegionID != *filter.RegionID {
			continue
		}
		if filter.Status != nil && snapshot.orders[id].Status != *filter.Status {
			continue
		}
		matched = append(matched, snapshot.orders[id])
	}
	// created_at DESC, id DESC — the same ordering as in the query.
	slices.SortFunc(matched, func(a, b models.Order) int {
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return b.CreatedAt.Compare(a.CreatedAt)
		}
		return strings.Compare(b.ID, a.ID)
	})

	total := int64(len(matched))
	if filter.Offset >= total {
		return []models.Order{}, total, nil
	}
	end := min(filter.Offset+filter.Limit, total)
	return slices.Clone(matched[filter.Offset:end]), total, nil
}

// OrdersByIDs returns the set of identifiers.
func (f *fakeStore) OrdersByIDs(ctx context.Context, ids []string) ([]models.Order, error) {
	snapshot := f.view(ctx)
	out := make([]models.Order, 0, len(ids))
	for _, id := range slices.Sorted(slices.Values(ids)) {
		if order, ok := snapshot.orders[id]; ok && order.DeletedAt == nil {
			out = append(out, order)
		}
	}
	return out, nil
}

// CancelOrder cancels the order; it only takes effect in the 'pending' status.
func (f *fakeStore) CancelOrder(ctx context.Context, id, reason string) (models.Order, error) {
	return f.applyStatus(ctx, id, models.OrderPending, models.OrderCanceled, reason)
}

// CompleteOrder completes the order; it only takes effect in the 'pending'
// status.
func (f *fakeStore) CompleteOrder(ctx context.Context, id string) (models.Order, error) {
	return f.applyStatus(ctx, id, models.OrderPending, models.OrderCompleted, "")
}

// ArchiveOrder archives the order; it only takes effect in the 'completed'
// status.
func (f *fakeStore) ArchiveOrder(ctx context.Context, id string) (models.Order, error) {
	return f.applyStatus(ctx, id, models.OrderCompleted, models.OrderArchived, "")
}

// applyStatus imitates the WHERE condition of the status transition queries.
func (f *fakeStore) applyStatus(ctx context.Context, id string, required, next models.OrderStatus, reason string) (models.Order, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	order, ok := f.orders[id]
	if !ok || order.DeletedAt != nil || order.Status != required {
		return models.Order{}, errors.Conflict("order_state_changed",
			"the transition could not be applied: the status of the order differs from the expected one (%s)", id)
	}

	stamp := f.nextStamp()
	order.Status = next
	order.UpdatedAt = stamp
	switch next {
	case models.OrderCanceled:
		order.CanceledAt = &stamp
		order.CancelReason = reason
	case models.OrderCompleted:
		order.CompletedAt = &stamp
	case models.OrderArchived, models.OrderPending:
		// The stamp does not change.
	}
	f.recordUndo(ctx, undoEntry(f.orders, id))
	f.orders[id] = order
	return order, nil
}

// --- lines -------------------------------------------------------------------

// CreateLineItem writes a new order line.
func (f *fakeStore) CreateLineItem(ctx context.Context, item models.OrderLineItem) (models.OrderLineItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failCreateLineItem != nil {
		return models.OrderLineItem{}, f.failCreateLineItem
	}
	if order, ok := f.orders[item.OrderID]; !ok || order.DeletedAt != nil {
		return models.OrderLineItem{}, notFound(item.OrderID)
	}
	stamp := f.nextStamp()
	item.CreatedAt = stamp
	item.UpdatedAt = stamp
	f.recordUndo(ctx, undoEntry(f.items, item.ID))
	f.items[item.ID] = item
	return item, nil
}

// ListLineItems returns the lines of the order in creation order.
func (f *fakeStore) ListLineItems(ctx context.Context, orderID string) ([]models.OrderLineItem, error) {
	snapshot := f.view(ctx)
	out := make([]models.OrderLineItem, 0)
	for id := range snapshot.items {
		if snapshot.items[id].OrderID == orderID {
			out = append(out, snapshot.items[id])
		}
	}
	slices.SortFunc(out, func(a, b models.OrderLineItem) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	return out, nil
}

// --- summary -----------------------------------------------------------------

// CreateOrderAddress writes one address of the order.
//
// It refuses a second address of the same type, the way the schema's unique
// index on (order_id, address_type) does: a fake that accepted it would let a
// test pass over an order with two destinations, which the database will not
// hold.
func (f *fakeStore) CreateOrderAddress(
	ctx context.Context, address models.OrderAddress,
) (models.OrderAddress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	existing := f.addresses[address.OrderID]
	for i := range existing {
		if existing[i].Type == address.Type {
			return models.OrderAddress{}, errors.Conflict("order_address_duplicate",
				"order %s already has a %s address", address.OrderID, address.Type)
		}
	}

	stamp := f.nextStamp()
	address.CreatedAt = stamp
	address.UpdatedAt = stamp
	f.recordUndo(ctx, undoEntry(f.addresses, address.OrderID))
	f.addresses[address.OrderID] = append(existing, address)

	return address, nil
}

// OrderAddressesByOrderIDs reads the addresses of several orders at once.
func (f *fakeStore) OrderAddressesByOrderIDs(
	ctx context.Context, orderIDs []string,
) (map[string][]models.OrderAddress, error) {
	// Through the view, not the live map: inside a read transaction every other
	// reader sees the snapshot, and one reader that did not would give the
	// caller a detail whose header and addresses came from two different
	// instants — the thing the read transaction exists to prevent.
	stored := f.view(ctx).addresses

	out := make(map[string][]models.OrderAddress, len(orderIDs))
	for _, id := range orderIDs {
		if list := stored[id]; len(list) > 0 {
			out[id] = append([]models.OrderAddress(nil), list...)
		}
	}

	return out, nil
}

// CreateSummary opens the summary of the order.
func (f *fakeStore) CreateSummary(ctx context.Context, summary models.OrderSummary) (models.OrderSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failCreateSummary != nil {
		return models.OrderSummary{}, f.failCreateSummary
	}
	if order, ok := f.orders[summary.OrderID]; !ok || order.DeletedAt != nil {
		return models.OrderSummary{}, notFound(summary.OrderID)
	}
	if _, exists := f.summaries[summary.OrderID]; exists {
		return models.OrderSummary{}, errors.Conflict("order_summary_exists",
			"the summary of the order already exists")
	}
	stamp := f.nextStamp()
	summary.CreatedAt = stamp
	summary.UpdatedAt = stamp
	f.recordUndo(ctx, undoEntry(f.summaries, summary.OrderID))
	f.summaries[summary.OrderID] = summary
	return summary, nil
}

// addressCount is how many address rows the fake holds, whatever order they
// belong to.
//
// The tests ask it rather than asking by order id, and the difference is the
// whole point on the rollback path: a rolled-back order is not in the store, so
// a lookup BY ITS ID would come back empty whether the addresses were rolled
// back or left behind. The count cannot be fooled that way.
func (f *fakeStore) addressCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	total := 0
	for id := range f.addresses {
		total += len(f.addresses[id])
	}

	return total
}

// GetSummary returns the summary of the order.
func (f *fakeStore) GetSummary(ctx context.Context, orderID string) (models.OrderSummary, error) {
	summary, ok := f.view(ctx).summaries[orderID]
	if !ok {
		return models.OrderSummary{}, errors.NotFound("order_summary_not_found",
			"the summary of the order was not found: %s", orderID)
	}
	return summary, nil
}

// SetSummaryTotals merges the summary amounts with GREATEST.
//
// The merging is in the query itself (queries/order_summaries.sql), that is, it
// is the behavior of the database and the fake imitates it.
func (f *fakeStore) SetSummaryTotals(ctx context.Context, orderID string, paid, refunded int64) (models.OrderSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	summary, ok := f.summaries[orderID]
	if !ok {
		return models.OrderSummary{}, errors.NotFound("order_summary_not_found",
			"the summary of the order was not found: %s", orderID)
	}
	summary.PaidTotal = max(summary.PaidTotal, paid)
	summary.RefundedTotal = max(summary.RefundedTotal, refunded)
	summary.UpdatedAt = f.nextStamp()
	f.recordUndo(ctx, undoEntry(f.summaries, orderID))
	f.summaries[orderID] = summary
	return summary, nil
}

// --- return / exchange / claim -----------------------------------------------

// CreateReturn writes a return record.
func (f *fakeStore) CreateReturn(ctx context.Context, ret models.Return) (models.Return, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if order, ok := f.orders[ret.OrderID]; !ok || order.DeletedAt != nil {
		return models.Return{}, notFound(ret.OrderID)
	}
	stamp := f.nextStamp()
	ret.CreatedAt = stamp
	ret.UpdatedAt = stamp
	f.recordUndo(ctx, undoEntry(f.returns, ret.ID))
	f.returns[ret.ID] = ret
	return ret, nil
}

// LockReturn locks the return row and returns its current form.
func (f *fakeStore) LockReturn(ctx context.Context, id string) (models.Return, error) {
	if err := requireTx(ctx, "LockReturn"); err != nil {
		return models.Return{}, err
	}

	// The row is read DIRECTLY rather than through view(): a locking read sees
	// the live row, not the transaction's snapshot, which is what makes it a
	// lock. Going through view() here would also deadlock — it takes the same
	// mutex this method already holds.
	f.mu.Lock()
	f.lockedReturns = append(f.lockedReturns, id)
	ret, ok := f.returns[id]
	f.mu.Unlock()

	if !ok {
		return models.Return{}, errors.NotFound("order_return_not_found",
			"the return record was not found: %s", id)
	}

	return ret, nil
}

// ReceiveReturn stamps the return as received at the given location.
func (f *fakeStore) ReceiveReturn(
	ctx context.Context, id, locationID string,
) (models.Return, error) {
	return f.stampReturn(ctx, id, models.ReturnReceived, locationID)
}

// CancelReturn withdraws the return request.
func (f *fakeStore) CancelReturn(ctx context.Context, id string) (models.Return, error) {
	return f.stampReturn(ctx, id, models.ReturnCanceled, "")
}

// stampReturn writes the new status and the matching timestamp.
func (f *fakeStore) stampReturn(
	ctx context.Context, id string, status models.ReturnStatus, locationID string,
) (models.Return, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	ret, ok := f.returns[id]
	if !ok {
		return models.Return{}, errors.NotFound("order_return_not_found",
			"the return record was not found: %s", id)
	}

	stamp := f.nextStamp()
	ret.Status = status
	ret.UpdatedAt = stamp
	switch status {
	case models.ReturnReceived:
		ret.ReceivedAt = &stamp
		ret.ReceivedLocationID = locationID
	case models.ReturnCanceled:
		ret.CanceledAt = &stamp
	case models.ReturnRequested:
	}

	f.recordUndo(ctx, undoEntry(f.returns, id))
	f.returns[id] = ret

	return ret, nil
}

// CreateReturnItem writes one line of a return.
func (f *fakeStore) CreateReturnItem(
	ctx context.Context, item models.ReturnItem,
) (models.ReturnItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	stamp := f.nextStamp()
	item.CreatedAt = stamp
	item.UpdatedAt = stamp
	f.recordUndo(ctx, undoEntry(f.retItems, item.ID))
	f.retItems[item.ID] = item

	return item, nil
}

// ListReturnItems returns a return's lines.
func (f *fakeStore) ListReturnItems(ctx context.Context, returnID string) ([]models.ReturnItem, error) {
	view := f.view(ctx)

	out := []models.ReturnItem{}
	for _, id := range slices.Sorted(maps.Keys(view.retItems)) {
		if view.retItems[id].ReturnID == returnID {
			out = append(out, view.retItems[id])
		}
	}

	return out, nil
}

// ReturnedQuantities sums the units already asked back per order line.
//
// It applies the SAME exclusion the real query does — a canceled return
// releases its units — because that rule is the whole point of the sum, and a
// fake that skipped it would let the service's check pass on data the database
// would never produce.
func (f *fakeStore) ReturnedQuantities(
	ctx context.Context, lineItemIDs []string,
) (map[string]int64, error) {
	view := f.view(ctx)
	wanted := make(map[string]bool, len(lineItemIDs))
	for _, id := range lineItemIDs {
		wanted[id] = true
	}

	out := map[string]int64{}
	for _, id := range slices.Sorted(maps.Keys(view.retItems)) {
		item := view.retItems[id]
		if !wanted[item.OrderLineItemID] {
			continue
		}
		if ret, ok := view.returns[item.ReturnID]; !ok || ret.Status == models.ReturnCanceled {
			continue
		}
		out[item.OrderLineItemID] += item.Quantity
	}

	return out, nil
}

// WriteOutboxEvent records an event inside the "transaction".
//
// It enforces the same rule the real repository does — outside a transaction it
// REFUSES — because that rule is the entire guarantee: an outbox row written
// outside one promises an event for work that may never commit.
func (f *fakeStore) WriteOutboxEvent(
	ctx context.Context, id, name string, data map[string]any,
) error {
	if err := requireTx(ctx, "WriteOutboxEvent"); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.recordUndo(ctx, undoEntry(f.outbox, id))
	f.outbox[id] = outboxRow{ID: id, Name: name, Data: data}

	return nil
}

// outboxEvents returns the events written into the fake outbox, in id order.
func (f *fakeStore) outboxEvents() []outboxRow {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]outboxRow, 0, len(f.outbox))
	for _, id := range slices.Sorted(maps.Keys(f.outbox)) {
		out = append(out, f.outbox[id])
	}

	return out
}

// outboxRow is one event written into the fake outbox.
type outboxRow struct {
	ID   string
	Name string
	Data map[string]any
}

// LockClaim locks the claim row and returns its current form.
func (f *fakeStore) LockClaim(ctx context.Context, id string) (models.Claim, error) {
	if err := requireTx(ctx, "LockClaim"); err != nil {
		return models.Claim{}, err
	}

	f.mu.Lock()
	f.lockedClaims = append(f.lockedClaims, id)
	claim, ok := f.claims[id]
	f.mu.Unlock()

	if !ok {
		return models.Claim{}, errors.NotFound("order_claim_not_found",
			"the claim record was not found: %s", id)
	}

	return claim, nil
}

// CompleteClaim records that the claim was settled.
func (f *fakeStore) CompleteClaim(ctx context.Context, id string) (models.Claim, error) {
	return f.stampClaim(ctx, id, models.ClaimCompleted)
}

// CancelClaim withdraws the claim.
func (f *fakeStore) CancelClaim(ctx context.Context, id string) (models.Claim, error) {
	return f.stampClaim(ctx, id, models.ClaimCanceled)
}

// stampClaim writes the new status and the matching timestamp.
func (f *fakeStore) stampClaim(
	ctx context.Context, id string, status models.ClaimStatus,
) (models.Claim, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	claim, ok := f.claims[id]
	if !ok {
		return models.Claim{}, errors.NotFound("order_claim_not_found",
			"the claim record was not found: %s", id)
	}

	stamp := f.nextStamp()
	claim.Status = status
	claim.UpdatedAt = stamp
	switch status {
	case models.ClaimCompleted:
		claim.CompletedAt = &stamp
	case models.ClaimCanceled:
		claim.CanceledAt = &stamp
	case models.ClaimRequested:
	}

	f.recordUndo(ctx, undoEntry(f.claims, id))
	f.claims[id] = claim

	return claim, nil
}

// GetReturn returns the return record.
func (f *fakeStore) GetReturn(ctx context.Context, id string) (models.Return, error) {
	ret, ok := f.view(ctx).returns[id]
	if !ok {
		return models.Return{}, errors.NotFound("order_return_not_found",
			"the return record was not found: %s", id)
	}
	return ret, nil
}

// ListReturns pages the return records of the order.
func (f *fakeStore) ListReturns(ctx context.Context, filter models.ChildFilter) ([]models.Return, int64, error) {
	snapshot := f.view(ctx)
	matched := make([]models.Return, 0)
	for id := range snapshot.returns {
		if snapshot.returns[id].OrderID == filter.OrderID {
			matched = append(matched, snapshot.returns[id])
		}
	}
	slices.SortFunc(matched, func(a, b models.Return) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	total := int64(len(matched))
	if filter.Offset >= total {
		return []models.Return{}, total, nil
	}
	return matched[filter.Offset:min(filter.Offset+filter.Limit, total)], total, nil
}

// CreateExchange writes an exchange record.
func (f *fakeStore) CreateExchange(ctx context.Context, exchange models.Exchange) (models.Exchange, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if order, ok := f.orders[exchange.OrderID]; !ok || order.DeletedAt != nil {
		return models.Exchange{}, notFound(exchange.OrderID)
	}
	stamp := f.nextStamp()
	exchange.CreatedAt = stamp
	exchange.UpdatedAt = stamp
	f.recordUndo(ctx, undoEntry(f.exchanges, exchange.ID))
	f.exchanges[exchange.ID] = exchange
	return exchange, nil
}

// GetExchange returns the exchange record.
func (f *fakeStore) GetExchange(ctx context.Context, id string) (models.Exchange, error) {
	exchange, ok := f.view(ctx).exchanges[id]
	if !ok {
		return models.Exchange{}, errors.NotFound("order_exchange_not_found",
			"the exchange record was not found: %s", id)
	}
	return exchange, nil
}

// ListExchanges pages the exchange records of the order.
func (f *fakeStore) ListExchanges(ctx context.Context, filter models.ChildFilter) ([]models.Exchange, int64, error) {
	snapshot := f.view(ctx)
	matched := make([]models.Exchange, 0)
	for id := range snapshot.exchanges {
		if snapshot.exchanges[id].OrderID == filter.OrderID {
			matched = append(matched, snapshot.exchanges[id])
		}
	}
	slices.SortFunc(matched, func(a, b models.Exchange) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	total := int64(len(matched))
	if filter.Offset >= total {
		return []models.Exchange{}, total, nil
	}
	return matched[filter.Offset:min(filter.Offset+filter.Limit, total)], total, nil
}

// CreateClaim writes a claim record.
func (f *fakeStore) CreateClaim(ctx context.Context, claim models.Claim) (models.Claim, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if order, ok := f.orders[claim.OrderID]; !ok || order.DeletedAt != nil {
		return models.Claim{}, notFound(claim.OrderID)
	}
	stamp := f.nextStamp()
	claim.CreatedAt = stamp
	claim.UpdatedAt = stamp
	f.recordUndo(ctx, undoEntry(f.claims, claim.ID))
	f.claims[claim.ID] = claim
	return claim, nil
}

// GetClaim returns the claim record.
func (f *fakeStore) GetClaim(ctx context.Context, id string) (models.Claim, error) {
	claim, ok := f.view(ctx).claims[id]
	if !ok {
		return models.Claim{}, errors.NotFound("order_claim_not_found",
			"the claim record was not found: %s", id)
	}
	return claim, nil
}

// ListClaims pages the claim records of the order.
func (f *fakeStore) ListClaims(ctx context.Context, filter models.ChildFilter) ([]models.Claim, int64, error) {
	snapshot := f.view(ctx)
	matched := make([]models.Claim, 0)
	for id := range snapshot.claims {
		if snapshot.claims[id].OrderID == filter.OrderID {
			matched = append(matched, snapshot.claims[id])
		}
	}
	slices.SortFunc(matched, func(a, b models.Claim) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	total := int64(len(matched))
	if filter.Offset >= total {
		return []models.Claim{}, total, nil
	}
	return matched[filter.Offset:min(filter.Offset+filter.Limit, total)], total, nil
}

// --- event bus ---------------------------------------------------------------

// fakeBus is the in-memory counterpart of service.EventPublisher.
type fakeBus struct {
	mu sync.Mutex
	// published holds the published events IN ORDER.
	published []eventbus.Event
	// failErr, when it is set, makes Publish return this error.
	failErr error
}

// That the fake bus satisfies the surface the service expects is verified at
// compile time.
var _ service.EventPublisher = (*fakeBus)(nil)

// newFakeBus produces an empty fake bus.
func newFakeBus() *fakeBus { return &fakeBus{} }

// Publish records the event.
func (b *fakeBus) Publish(_ context.Context, e eventbus.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.failErr != nil {
		return b.failErr
	}
	b.published = append(b.published, e)
	return nil
}

// events returns an instant copy of the published events.
func (b *fakeBus) events() []eventbus.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.published)
}

// --- spending ------------------------------------------------------------------

// LockCustomerSpending takes the spending lock of the customer.
//
// The fake imitates the ONLY observable contract of the real lock: it returns an
// error if it is called outside a transaction. That the lock REALLY serializes
// can only be proven with concurrent goroutines and a real database (see
// order_integration_test.go).
func (f *fakeStore) LockCustomerSpending(ctx context.Context, customerID string) error {
	if err := requireTx(ctx, "LockCustomerSpending"); err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.spendingLocks = append(f.spendingLocks, customerID)
	return nil
}

// SumCustomerSpend returns the customer's spend within the window.
//
// It imitates the rules of queries/spending.sql: canceled and soft-deleted
// orders do not enter the sum, the refunded amount is subtracted, the currency
// matches exactly and the lower end of the window IS INCLUDED.
func (f *fakeStore) SumCustomerSpend(
	ctx context.Context,
	customerID, currencyCode string,
	windowStart *time.Time,
) (int64, error) {
	snapshot := f.view(ctx)

	f.mu.Lock()
	f.spendingSums++
	f.mu.Unlock()

	var total int64
	for id := range snapshot.orders {
		order := snapshot.orders[id]
		switch {
		case order.DeletedAt != nil,
			order.Status == models.OrderCanceled,
			order.CustomerID != customerID,
			order.CurrencyCode != currencyCode:
			continue
		}
		if windowStart != nil && order.PlacedAt.Before(*windowStart) {
			continue
		}
		total += order.Total - snapshot.summaries[id].RefundedTotal
	}
	return total, nil
}

// seedOrder writes an order into the store directly and fixes its PLACED_AT to
// the moment the caller gives.
//
// [fakeStore.CreateOrder] stamps the time itself (the counterpart of the real
// database's now()) and that stamp is close to 1970; the tests that exercise the
// window boundaries, on the other hand, HAVE TO BE ABLE TO CHOOSE when the order
// was placed. No rule of the service is skipped: this path only sets up PAST
// data, the call under test is still the service.
func (f *fakeStore) seedOrder(order models.Order) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.displaySeq++
	order.DisplayID = f.displaySeq
	if order.Status == "" {
		order.Status = models.OrderPending
	}
	if order.CreatedAt.IsZero() {
		order.CreatedAt = order.PlacedAt
	}
	order.UpdatedAt = order.CreatedAt
	f.orders[order.ID] = order
}

// seedRefund writes the refunded amount of an order in the store.
func (f *fakeStore) seedRefund(orderID string, refunded int64) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.summaries[orderID] = models.OrderSummary{
		ID:            "ordsum_" + orderID,
		OrderID:       orderID,
		PaidTotal:     refunded,
		RefundedTotal: refunded,
	}
}

// spendingLockCount returns the number of spending locks that were taken.
func (f *fakeStore) spendingLockCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.spendingLocks)
}

// spendingSumCount returns the number of spend sum reads that were made.
func (f *fakeStore) spendingSumCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.spendingSums
}

// fakeSpendingPolicy is the in-memory counterpart of service.SpendingPolicy.
//
// The real provider is the b2b module and this package CANNOT import it; the
// fake only imitates the JSON schema of the boundary. That the schema is THE
// SAME on the two sides cannot be proven by these tests — that is the job of the
// integration test.
type fakeSpendingPolicy struct {
	mu sync.Mutex
	// payload is the body to be returned; when it is empty the "no rule" body is
	// returned.
	payload json.RawMessage
	// empty, when it is true, makes the call return an EMPTY body and the
	// payload is ignored.
	//
	// It is a separate flag because an empty slice and "not set" resolve to the
	// same thing in Go; without the distinction the case where the provider
	// returns no body at all could not be exercised.
	empty bool
	// err, when it is set, makes the call return this error.
	err error
	// asked records the customer identifiers that were asked about IN ORDER.
	asked []string
}

// That the fake satisfies the surface the service expects is verified at compile
// time.
var _ service.SpendingPolicy = (*fakeSpendingPolicy)(nil)

// SpendingLimitJSON returns the prepared answer and records the call.
func (p *fakeSpendingPolicy) SpendingLimitJSON(_ context.Context, customerID string) (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.asked = append(p.asked, customerID)
	if p.err != nil {
		return nil, p.err
	}
	if p.empty {
		return nil, nil
	}
	if len(p.payload) == 0 {
		return json.RawMessage(`{"limited":false}`), nil
	}
	return p.payload, nil
}

// calls returns a copy of the customer identifiers that were asked about.
func (p *fakeSpendingPolicy) calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.asked)
}
