package service_test

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// txMarkerKey is the fake store's "we are inside a transaction" marker.
type txMarkerKey struct{}

// readSnapshotKey is the context key of the read-only transaction's snapshot.
type readSnapshotKey struct{}

// fakeSnapshot is the fake store's complete state at one moment.
//
// Both the read-only transaction's view and the writing transaction's rollback
// point are carried with this type; both of them are "the copy of the store at
// that moment".
type fakeSnapshot struct {
	carts     map[string]models.Cart
	items     map[string]models.LineItem
	addresses map[string]models.CartAddress
	methods   map[string]models.ShippingMethod
}

// fakeStore is the in-memory counterpart of service.Store.
//
// # What it imitates and what it DOES NOT
//
// The fake imitates only the things THE DATABASE does:
//
//  1. A method that takes the lock returns an error if it is called OUTSIDE a
//     transaction. If the service forgets WithTx in a flow, the unit test
//     catches it; in a real database that mistake would only show up under a
//     race.
//  2. If the transaction ends with an error, what was written IS ROLLED BACK.
//     The claim "it returned an error and nothing was written" can only be
//     tested this way.
//  3. The (cart_id, variant_id) uniqueness: the counterpart of the partial
//     unique index in the migration.
//  4. The read-only transaction's SNAPSHOT: the reads inside
//     [fakeStore.WithReadTx] see the state at the start of the transaction, they
//     do not see intervening writes — the counterpart of PostgreSQL's REPEATABLE
//     READ level.
//
// No rule that is THE SERVICE's responsibility IS REPEATED here: the fake does
// not prevent writing to a completed cart and it does not validate the totals
// identity. Otherwise the "the service rejects a completed cart" test would pass
// even if that check were deleted from the service — what the test proved would
// be the fake's behavior.
type fakeStore struct {
	mu        sync.Mutex
	carts     map[string]models.Cart
	items     map[string]models.LineItem
	addresses map[string]models.CartAddress
	methods   map[string]models.ShippingMethod

	// seq gives the added child records an increasing timestamp; the listing
	// order being deterministic rests on it.
	seq int

	// lockedCarts records the locked carts IN ORDER. Whether the lock was taken
	// is a concurrency contract and in a real database its violation only shows
	// up under a race; here it can be read directly.
	lockedCarts []string
	// bumpCalls counts how many times the shape counter was incremented.
	bumpCalls int

	// setLineTotalsCalls counts the line amount WRITE calls.
	//
	// The number is the contract itself: however many lines a calculation round
	// carries, it is a SINGLE write call. The number is the only indicator of how
	// long the cart's lock is held that is visible in a unit test; a change that
	// goes back to a loop per line is caught here.
	setLineTotalsCalls int
	// setLineTotalsRows is the total number of lines written by those calls.
	setLineTotalsRows int

	// failCreateLineItem, when it is set, makes CreateLineItem return this
	// error; it is used to test the transaction rollback path.
	failCreateLineItem error
	// failSetLineItemTotals, when it is set, makes SetLineItemTotals return this
	// error.
	failSetLineItemTotals error

	// hookListLineItems, when it is set, is called ONCE AT THE START of
	// ListLineItems and is then cleared.
	//
	// It exists to slip a write INTO THE MIDDLE of a multi-query read: in a real
	// database an intervening write depends on timing and cannot be produced
	// deterministically in a test.
	hookListLineItems func()
}

// newFakeStore produces an empty fake store.
func newFakeStore() *fakeStore {
	return &fakeStore{
		carts:     map[string]models.Cart{},
		items:     map[string]models.LineItem{},
		addresses: map[string]models.CartAddress{},
		methods:   map[string]models.ShippingMethod{},
	}
}

// That the fake store satisfies the surface the service expects is verified at
// compile time.
var _ service.Store = (*fakeStore)(nil)

// addressKey is the map key of the (cart, type) pair.
func addressKey(cartID string, kind models.AddressType) string {
	return cartID + "\x00" + kind.String()
}

// nextStamp produces the next increasing timestamp. The caller must hold the
// lock.
func (f *fakeStore) nextStamp() time.Time {
	f.seq++
	return time.Unix(0, 0).UTC().Add(time.Duration(f.seq) * time.Millisecond)
}

// WithTx runs fn inside a "transaction"; if it returns an error it rolls the
// state back.
func (f *fakeStore) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if ctx.Value(txMarkerKey{}) != nil {
		return fn(ctx)
	}

	snapshot := f.snapshot()
	if err := fn(context.WithValue(ctx, txMarkerKey{}, true)); err != nil {
		f.mu.Lock()
		f.carts, f.items = snapshot.carts, snapshot.items
		f.addresses, f.methods = snapshot.addresses, snapshot.methods
		f.mu.Unlock()
		return err
	}
	return nil
}

// WithReadTx runs fn inside a "transaction" with a SINGLE SNAPSHOT.
//
// The view freezes at the start of the transaction and the reads look at it; an
// intervening write IS NOT VISIBLE inside this transaction. Its real counterpart
// is REPEATABLE READ and imitating it is essential: the torn read is born
// exactly in the absence of that level.
func (f *fakeStore) WithReadTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if ctx.Value(readSnapshotKey{}) != nil || ctx.Value(txMarkerKey{}) != nil {
		return fn(ctx)
	}
	return fn(context.WithValue(ctx, readSnapshotKey{}, f.snapshot()))
}

// snapshot produces a complete copy of the store at that moment.
func (f *fakeStore) snapshot() fakeSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()

	return fakeSnapshot{
		carts:     cloneMap(f.carts),
		items:     cloneMap(f.items),
		addresses: cloneMap(f.addresses),
		methods:   cloneMap(f.methods),
	}
}

// readSnapshot returns the read-only transaction's view; nil if there is no
// transaction.
func readSnapshot(ctx context.Context) *fakeSnapshot {
	snap, ok := ctx.Value(readSnapshotKey{}).(fakeSnapshot)
	if !ok {
		return nil
	}
	return &snap
}

// storeView holds the maps a read will look at.
type storeView struct {
	fakeSnapshot
	// release is called when the read is done; it releases the lock on the live
	// maps.
	release func()
}

// view returns the maps the read will look at.
//
// If there is a read-only transaction, its FROZEN view is given and no lock is
// needed — the copy is not shared with anyone else. If there is none, the live
// maps are given UNDER the lock; the lock is held until [storeView.release] is
// called.
func (f *fakeStore) view(ctx context.Context) storeView {
	if snap := readSnapshot(ctx); snap != nil {
		return storeView{fakeSnapshot: *snap, release: func() {}}
	}

	f.mu.Lock()
	return storeView{
		fakeSnapshot: fakeSnapshot{
			carts:     f.carts,
			items:     f.items,
			addresses: f.addresses,
			methods:   f.methods,
		},
		release: f.mu.Unlock,
	}
}

// fireListLineItems runs the ListLineItems hook ONCE.
func (f *fakeStore) fireListLineItems() {
	f.mu.Lock()
	hook := f.hookListLineItems
	f.hookListLineItems = nil
	f.mu.Unlock()

	if hook != nil {
		hook()
	}
}

// cloneMap produces a shallow copy of the map.
func cloneMap[K comparable, V any](in map[K]V) map[K]V {
	out := make(map[K]V, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// requireTx validates that the methods that take the lock were called inside a
// transaction.
func requireTx(ctx context.Context, op string) error {
	if ctx.Value(txMarkerKey{}) == nil {
		return errors.Internal("fake_tx_required", "%s was called outside a transaction", op)
	}
	return nil
}

// CreateCart records the cart.
func (f *fakeStore) CreateCart(_ context.Context, cart models.Cart) (models.Cart, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	stamp := f.nextStamp()
	cart.CreatedAt, cart.UpdatedAt = stamp, stamp
	f.carts[cart.ID] = cart
	return cart, nil
}

// GetCart returns the cart.
func (f *fakeStore) GetCart(ctx context.Context, id string) (models.Cart, error) {
	view := f.view(ctx)
	defer view.release()

	cart, ok := view.carts[id]
	if !ok || cart.DeletedAt != nil {
		return models.Cart{}, errors.NotFound("cart_not_found", "cart not found: %s", id)
	}
	return cart, nil
}

// LockCart locks the cart; if it is called outside a transaction it returns an
// error.
func (f *fakeStore) LockCart(ctx context.Context, id string) (models.Cart, error) {
	if err := requireTx(ctx, "LockCart"); err != nil {
		return models.Cart{}, err
	}
	cart, err := f.GetCart(ctx, id)
	if err != nil {
		return models.Cart{}, err
	}

	f.mu.Lock()
	f.lockedCarts = append(f.lockedCarts, id)
	f.mu.Unlock()
	return cart, nil
}

// ListCarts filters and paginates the carts.
func (f *fakeStore) ListCarts(ctx context.Context, filter models.CartFilter) ([]models.Cart, int64, error) {
	view := f.view(ctx)
	defer view.release()

	matched := make([]models.Cart, 0, len(view.carts))
	// The loops are walked by index/key: the model structs are large and copying
	// them by value would carry a few hundred bytes for nothing on every turn.
	for id := range view.carts {
		cart := view.carts[id]
		if cart.DeletedAt != nil {
			continue
		}
		if filter.CustomerID != nil && cart.CustomerID != *filter.CustomerID {
			continue
		}
		if filter.RegionID != nil && cart.RegionID != *filter.RegionID {
			continue
		}
		if filter.Completed != nil && cart.Completed() != *filter.Completed {
			continue
		}
		matched = append(matched, cart)
	}
	slices.SortFunc(matched, func(a, b models.Cart) int {
		return cmpString(a.ID, b.ID)
	})

	total := int64(len(matched))
	if filter.Offset >= total {
		return []models.Cart{}, total, nil
	}
	end := min(filter.Offset+filter.Limit, total)
	return matched[filter.Offset:end], total, nil
}

// cmpString compares two strings.
func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// CartsByIDs returns the carts of the identifier set.
func (f *fakeStore) CartsByIDs(ctx context.Context, ids []string) ([]models.Cart, error) {
	view := f.view(ctx)
	defer view.release()

	out := make([]models.Cart, 0, len(ids))
	for _, id := range ids {
		if cart, ok := view.carts[id]; ok && cart.DeletedAt == nil {
			out = append(out, cart)
		}
	}
	slices.SortFunc(out, func(a, b models.Cart) int { return cmpString(a.ID, b.ID) })
	return out, nil
}

// UpdateCartContact writes the cart's email and customer fields.
//
// Who can be handed over to whom IS NOT checked HERE; that rule is the
// service's.
func (f *fakeStore) UpdateCartContact(_ context.Context, id string, contact models.CartContact) (models.Cart, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	cart, ok := f.carts[id]
	if !ok || cart.DeletedAt != nil {
		return models.Cart{}, errors.NotFound("cart_not_found", "cart not found: %s", id)
	}
	cart.Email = contact.Email
	cart.CustomerID = contact.CustomerID
	cart.UpdatedAt = f.nextStamp()
	f.carts[id] = cart
	return cart, nil
}

// UpdateCartTotals writes the totals.
//
// The totals identity IS NOT validated HERE; that rule is the service's (see the
// type's documentation).
func (f *fakeStore) UpdateCartTotals(_ context.Context, id string, totals models.CartTotals) (models.Cart, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	cart, ok := f.carts[id]
	if !ok || cart.DeletedAt != nil {
		return models.Cart{}, errors.NotFound("cart_not_found", "cart not found: %s", id)
	}
	cart.Subtotal = totals.Subtotal
	cart.DiscountTotal = totals.DiscountTotal
	cart.TaxTotal = totals.TaxTotal
	cart.ShippingTotal = totals.ShippingTotal
	cart.Total = totals.Total
	cart.TotalsRevision = totals.Revision
	cart.UpdatedAt = f.nextStamp()
	f.carts[id] = cart
	return cart, nil
}

// BumpCartRevision increments the shape counter.
func (f *fakeStore) BumpCartRevision(_ context.Context, id string) (models.Cart, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	cart, ok := f.carts[id]
	if !ok || cart.DeletedAt != nil {
		return models.Cart{}, errors.NotFound("cart_not_found", "cart not found: %s", id)
	}
	cart.Revision++
	cart.UpdatedAt = f.nextStamp()
	f.carts[id] = cart
	f.bumpCalls++
	return cart, nil
}

// MarkCartCompleted stamps the cart as completed.
//
// It does not prevent the second stamp HERE; that rule is the service's.
func (f *fakeStore) MarkCartCompleted(_ context.Context, id string) (models.Cart, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	cart, ok := f.carts[id]
	if !ok || cart.DeletedAt != nil {
		return models.Cart{}, errors.NotFound("cart_not_found", "cart not found: %s", id)
	}
	now := f.nextStamp()
	cart.CompletedAt = &now
	cart.UpdatedAt = now
	f.carts[id] = cart
	return cart, nil
}

// SoftDeleteCart soft deletes the cart.
func (f *fakeStore) SoftDeleteCart(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	cart, ok := f.carts[id]
	if !ok || cart.DeletedAt != nil {
		return errors.NotFound("cart_not_found", "cart not found: %s", id)
	}
	now := f.nextStamp()
	cart.DeletedAt = &now
	f.carts[id] = cart
	return nil
}

// CreateLineItem records the line.
func (f *fakeStore) CreateLineItem(_ context.Context, item models.LineItem) (models.LineItem, error) {
	if f.failCreateLineItem != nil {
		return models.LineItem{}, f.failCreateLineItem
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	// The (cart_id, variant_id) uniqueness: the counterpart of the partial index
	// in the migration.
	for id := range f.items {
		if f.items[id].CartID == item.CartID && f.items[id].VariantID == item.VariantID {
			return models.LineItem{}, errors.Conflict("cart_line_item_exists",
				"this variant is already in the cart")
		}
	}

	stamp := f.nextStamp()
	item.CreatedAt, item.UpdatedAt = stamp, stamp
	f.items[item.ID] = item
	return item, nil
}

// GetLineItem returns the line.
func (f *fakeStore) GetLineItem(ctx context.Context, cartID, lineID string) (models.LineItem, error) {
	view := f.view(ctx)
	defer view.release()

	item, ok := view.items[lineID]
	if !ok || item.CartID != cartID {
		return models.LineItem{}, lineNotFound(cartID, lineID)
	}
	return item, nil
}

// GetLineItemByVariant returns the line of the variant in the cart.
func (f *fakeStore) GetLineItemByVariant(ctx context.Context, cartID, variantID string) (models.LineItem, error) {
	view := f.view(ctx)
	defer view.release()

	for id := range view.items {
		if view.items[id].CartID == cartID && view.items[id].VariantID == variantID {
			return view.items[id], nil
		}
	}
	return models.LineItem{}, errors.NotFound("cart_line_item_not_found",
		"the cart has no line for this variant (%s / %s)", cartID, variantID)
}

// ListLineItems returns the cart's lines in creation order.
func (f *fakeStore) ListLineItems(ctx context.Context, cartID string) ([]models.LineItem, error) {
	f.fireListLineItems()

	view := f.view(ctx)
	defer view.release()

	out := make([]models.LineItem, 0, len(view.items))
	for id := range view.items {
		if view.items[id].CartID == cartID {
			out = append(out, view.items[id])
		}
	}
	slices.SortFunc(out, func(a, b models.LineItem) int {
		if a.CreatedAt.Equal(b.CreatedAt) {
			return cmpString(a.ID, b.ID)
		}
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	return out, nil
}

// SetLineItemQuantity writes the line's quantity.
func (f *fakeStore) SetLineItemQuantity(_ context.Context, cartID, lineID string, quantity int64) (models.LineItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	item, ok := f.items[lineID]
	if !ok || item.CartID != cartID {
		return models.LineItem{}, lineNotFound(cartID, lineID)
	}
	item.Quantity = quantity
	item.UpdatedAt = f.nextStamp()
	f.items[lineID] = item
	return item, nil
}

// SetLineItemTotals writes all of the line amounts of one round.
//
// Like the real store it behaves ALL OR NOTHING: when a missing line is found,
// no amount is written. In reality the rollback of the transaction provides
// this; in the fake store the same result is produced by matching first and
// writing afterwards, otherwise the unit test would see a half write that the
// real database would never give.
//
// It increments setLineTotalsCalls by one: the NUMBER of calls is part of the
// contract — one round is a SINGLE write call (see [Service.SetTotals]).
func (f *fakeStore) SetLineItemTotals(_ context.Context, cartID string, lines []models.LineItemTotals) error {
	if f.failSetLineItemTotals != nil {
		return f.failSetLineItemTotals
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.setLineTotalsCalls++
	f.setLineTotalsRows += len(lines)

	updated := make([]models.LineItem, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		if _, dup := seen[line.LineItemID]; dup {
			return errors.Invalid(service.CodeTotalsInconsistent,
				"more than one amount was given for the same line: %s", line.LineItemID)
		}
		seen[line.LineItemID] = struct{}{}

		item, ok := f.items[line.LineItemID]
		if !ok || item.CartID != cartID {
			return lineNotFound(cartID, line.LineItemID)
		}
		item.UnitPrice = line.Totals.UnitPrice
		item.Subtotal = line.Totals.Subtotal
		item.DiscountTotal = line.Totals.DiscountTotal
		item.TaxTotal = line.Totals.TaxTotal
		item.Total = line.Totals.Total
		item.UpdatedAt = f.nextStamp()
		updated = append(updated, item)
	}
	for i := range updated {
		f.items[updated[i].ID] = updated[i]
	}
	return nil
}

// SoftDeleteLineItem deletes the line.
func (f *fakeStore) SoftDeleteLineItem(_ context.Context, cartID, lineID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	item, ok := f.items[lineID]
	if !ok || item.CartID != cartID {
		return lineNotFound(cartID, lineID)
	}
	delete(f.items, lineID)
	return nil
}

// SoftDeleteLineItemsByCart deletes all of the cart's lines.
func (f *fakeStore) SoftDeleteLineItemsByCart(_ context.Context, cartID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for id := range f.items {
		if f.items[id].CartID == cartID {
			delete(f.items, id)
		}
	}
	return nil
}

// lineNotFound produces the missing line error.
func lineNotFound(cartID, lineID string) error {
	return errors.NotFound("cart_line_item_not_found",
		"cart line not found (%s / %s)", cartID, lineID)
}

// UpsertCartAddress writes the address; it PRESERVES the identifier of an
// existing one.
func (f *fakeStore) UpsertCartAddress(_ context.Context, addr models.CartAddress) (models.CartAddress, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	key := addressKey(addr.CartID, addr.Type)
	stamp := f.nextStamp()
	if existing, ok := f.addresses[key]; ok {
		addr.ID = existing.ID
		addr.CreatedAt = existing.CreatedAt
	} else {
		addr.CreatedAt = stamp
	}
	addr.UpdatedAt = stamp
	f.addresses[key] = addr
	return addr, nil
}

// ListCartAddresses returns the cart's addresses.
func (f *fakeStore) ListCartAddresses(ctx context.Context, cartID string) ([]models.CartAddress, error) {
	view := f.view(ctx)
	defer view.release()

	out := make([]models.CartAddress, 0, 2)
	for _, kind := range []models.AddressType{models.AddressBilling, models.AddressShipping} {
		if addr, ok := view.addresses[addressKey(cartID, kind)]; ok {
			out = append(out, addr)
		}
	}
	return out, nil
}

// SoftDeleteCartAddressesByCart deletes the cart's addresses.
func (f *fakeStore) SoftDeleteCartAddressesByCart(_ context.Context, cartID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, kind := range []models.AddressType{models.AddressBilling, models.AddressShipping} {
		delete(f.addresses, addressKey(cartID, kind))
	}
	return nil
}

// CreateShippingMethod adds a shipping method.
func (f *fakeStore) CreateShippingMethod(_ context.Context, method models.ShippingMethod) (models.ShippingMethod, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if method.ShippingOptionID != "" {
		for id := range f.methods {
			if f.methods[id].CartID == method.CartID && f.methods[id].ShippingOptionID == method.ShippingOptionID {
				return models.ShippingMethod{}, errors.Conflict("cart_shipping_option_already_added",
					"this shipping option has already been added to the cart")
			}
		}
	}

	stamp := f.nextStamp()
	method.CreatedAt, method.UpdatedAt = stamp, stamp
	f.methods[method.ID] = method
	return method, nil
}

// ListShippingMethods returns the cart's shipping methods.
func (f *fakeStore) ListShippingMethods(ctx context.Context, cartID string) ([]models.ShippingMethod, error) {
	view := f.view(ctx)
	defer view.release()

	out := make([]models.ShippingMethod, 0, len(view.methods))
	for id := range view.methods {
		if view.methods[id].CartID == cartID {
			out = append(out, view.methods[id])
		}
	}
	slices.SortFunc(out, func(a, b models.ShippingMethod) int { return cmpString(a.ID, b.ID) })
	return out, nil
}

// SoftDeleteShippingMethod deletes the shipping method.
func (f *fakeStore) SoftDeleteShippingMethod(_ context.Context, cartID, methodID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	method, ok := f.methods[methodID]
	if !ok || method.CartID != cartID {
		return errors.NotFound("cart_shipping_method_not_found",
			"shipping method not found (%s / %s)", cartID, methodID)
	}
	delete(f.methods, methodID)
	return nil
}

// SoftDeleteShippingMethodsByCart deletes all of the cart's shipping methods.
func (f *fakeStore) SoftDeleteShippingMethodsByCart(_ context.Context, cartID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for id := range f.methods {
		if f.methods[id].CartID == cartID {
			delete(f.methods, id)
		}
	}
	return nil
}
