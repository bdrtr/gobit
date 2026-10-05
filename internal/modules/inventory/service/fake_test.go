package service_test

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// txMarkerKey is the fake store's "we are inside a transaction" marker.
type txMarkerKey struct{}

// fakeStore is the in-memory counterpart of service.Store.
//
// It DELIBERATELY imitates two behaviors of the real store, because the
// service's correctness rests on them:
//
//  1. The methods that take a lock return an error when called OUTSIDE a
//     transaction. If the service forgets WithTx in some flow, the unit test
//     catches it; against the real database the mistake would show only under
//     a race, as an unlocked read.
//  2. When the transaction ends in an error, what was written is ROLLED BACK.
//     That is the only way the claim "an error came back and nothing was
//     written" can be tested.
type fakeStore struct {
	mu           sync.Mutex
	items        map[string]models.InventoryItem
	locations    map[string]models.StockLocation
	levels       map[string]models.InventoryLevel
	reservations map[string]models.Reservation
	// movements is the ledger, in append order. It is a SLICE and not a map
	// because order is the thing being tested: the listing comes back newest
	// first, and a map would hand the assertion whatever it liked.
	movements []models.Movement

	// updateLevelCalls counts how many times a stock level was written; it is
	// how the idempotent flows are proven not to touch the stock a SECOND TIME.
	updateLevelCalls int
	// locks records the locks taken, IN ORDER ("item", "level",
	// "reservation"). The lock order is a concurrency contract, and against the
	// real database a violation shows only under a race (as a deadlock); here
	// the order can be read directly.
	locks []string
	// availableCalls is the number of calls to the batched availability query.
	availableCalls int

	// failCreateReservation, when set, is the error CreateReservation returns;
	// it is used to test the transaction's rollback path.
	failCreateReservation error
	// backorders are the claims (ADR 0392); backorderSeq hands out the queue
	// positions the identity column would.
	backorders   map[string]models.Backorder
	backorderSeq int64
	// queueReads records the available argument of every LockWaitingBackordersAt
	// call, in order: how many times a write read the queue, and with what.
	queueReads []queueRead

	// receipts are the expected supplier receipts (ADR 0399).
	receipts map[string]models.SupplierReceipt
	// deletedItems are the items soft deleted: gone for every read, and still
	// rows a foreign key accepts, as the table's are.
	deletedItems map[string]bool
	// forecastReads counts the forecast's reads of the receipts, and
	// byLocationReads the breakdown's: how the provider is proven not to
	// compute a per-warehouse field nobody named.
	forecastReads, byLocationReads int
	// now is the fake's clock for the forecast's "not yet due"; zero is the
	// wall clock.
	now time.Time

	// failSetReservationStatus makes the status write fail, which is the LAST
	// step of a confirm — after the level and the movement have been written.
	// It is what proves the ledger rolls back with the count rather than
	// surviving a failed transaction (ADR 0068).
	failSetReservationStatus error
}

// newFakeStore builds an empty fake store.
func newFakeStore() *fakeStore {
	return &fakeStore{
		items:        map[string]models.InventoryItem{},
		locations:    map[string]models.StockLocation{},
		levels:       map[string]models.InventoryLevel{},
		reservations: map[string]models.Reservation{},
		backorders:   map[string]models.Backorder{},
		receipts:     map[string]models.SupplierReceipt{},
		deletedItems: map[string]bool{},
	}
}

// queueRead is one read of a level's waiting claims.
type queueRead struct {
	itemID, locationID string
	available          int64
}

// That the fake store satisfies the surface the service expects is checked at
// compile time.
var _ service.Store = (*fakeStore)(nil)

// levelKey is the map key of an (item, location) pair.
func levelKey(itemID, locationID string) string {
	return itemID + "\x00" + locationID
}

// WithTx runs fn inside a "transaction"; when it returns an error the state is
// rolled back.
func (f *fakeStore) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if ctx.Value(txMarkerKey{}) != nil {
		return fn(ctx)
	}

	f.mu.Lock()
	snapshot := struct {
		items        map[string]models.InventoryItem
		locations    map[string]models.StockLocation
		levels       map[string]models.InventoryLevel
		reservations map[string]models.Reservation
		movements    []models.Movement
		backorders   map[string]models.Backorder
		receipts     map[string]models.SupplierReceipt
	}{
		items:        maps.Clone(f.items),
		locations:    maps.Clone(f.locations),
		levels:       maps.Clone(f.levels),
		reservations: maps.Clone(f.reservations),
		// The ledger is rolled back WITH the level, and that is the whole point
		// of it being in the snapshot: a movement that survived a failed
		// transaction would claim units moved that never did (ADR 0068).
		movements:  slices.Clone(f.movements),
		backorders: maps.Clone(f.backorders),
		receipts:   maps.Clone(f.receipts),
	}
	f.mu.Unlock()

	if err := fn(context.WithValue(ctx, txMarkerKey{}, true)); err != nil {
		f.mu.Lock()
		f.items, f.locations = snapshot.items, snapshot.locations
		f.levels, f.reservations = snapshot.levels, snapshot.reservations
		f.movements, f.backorders = snapshot.movements, snapshot.backorders
		f.receipts = snapshot.receipts
		f.mu.Unlock()
		return err
	}
	return nil
}

// requireTx verifies that the methods that take a lock are called inside a
// transaction.
func requireTx(ctx context.Context, op string) error {
	if ctx.Value(txMarkerKey{}) == nil {
		return errors.Internal("fake_tx_required", "%s called outside a transaction", op)
	}
	return nil
}

// CreateStockLocation records the location.
func (f *fakeStore) CreateStockLocation(_ context.Context, loc models.StockLocation) (models.StockLocation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	loc.CreatedAt, loc.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	f.locations[loc.ID] = loc
	return loc, nil
}

// GetStockLocation returns the location.
func (f *fakeStore) GetStockLocation(_ context.Context, id string) (models.StockLocation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	loc, ok := f.locations[id]
	if !ok {
		return models.StockLocation{}, errors.NotFound("inventory_location_not_found", "there is no such location: %s", id)
	}
	return loc, nil
}

// ListStockLocations pages the locations.
func (f *fakeStore) ListStockLocations(_ context.Context, limit, offset int64, includeClosed bool) ([]models.StockLocation, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	all := sortedValues(f.locations, func(loc models.StockLocation) string { return loc.ID })
	matched := make([]models.StockLocation, 0, len(all))
	for i := range all {
		if all[i].Closed() && !includeClosed {
			continue
		}
		matched = append(matched, all[i])
	}
	return paginate(matched, limit, offset), int64(len(matched)), nil
}

// LockStockLocation "exclusively locks" the location and returns it.
func (f *fakeStore) LockStockLocation(ctx context.Context, id string) (models.StockLocation, error) {
	if err := requireTx(ctx, "LockStockLocation"); err != nil {
		return models.StockLocation{}, err
	}
	f.recordLock("location")
	return f.GetStockLocation(ctx, id)
}

// LockStockLocationShared "locks the location in shared mode" and returns it.
func (f *fakeStore) LockStockLocationShared(ctx context.Context, id string) (models.StockLocation, error) {
	if err := requireTx(ctx, "LockStockLocationShared"); err != nil {
		return models.StockLocation{}, err
	}
	f.recordLock("location")
	return f.GetStockLocation(ctx, id)
}

// CloseStockLocation stamps the location closed.
func (f *fakeStore) CloseStockLocation(_ context.Context, id string) (models.StockLocation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	loc, ok := f.locations[id]
	if !ok {
		return models.StockLocation{}, errors.NotFound("inventory_location_not_found",
			"there is no such location: %s", id)
	}
	now := time.Now().UTC()
	loc.ClosedAt, loc.UpdatedAt = &now, now
	f.locations[id] = loc
	return loc, nil
}

// StockHeldAtLocation returns the physical and the reserved totals at the
// location.
func (f *fakeStore) StockHeldAtLocation(_ context.Context, locationID string) (stocked, reserved int64, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, level := range f.levels {
		if level.LocationID != locationID {
			continue
		}
		stocked += level.StockedQuantity
		reserved += level.ReservedQuantity
	}
	return stocked, reserved, nil
}

// CountActiveReservationsAtLocation counts the active reservations at the
// location.
func (f *fakeStore) CountActiveReservationsAtLocation(_ context.Context, locationID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var count int64
	for id := range f.reservations {
		res := f.reservations[id]
		if res.LocationID == locationID && res.Status == models.ReservationActive {
			count++
		}
	}
	return count, nil
}

// CreateInventoryItem records the item.
func (f *fakeStore) CreateInventoryItem(_ context.Context, item models.InventoryItem) (models.InventoryItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, existing := range f.items {
		if existing.SKU == item.SKU {
			return models.InventoryItem{}, errors.Conflict("inventory_sku_exists", "sku already in use: %s", item.SKU)
		}
	}
	item.CreatedAt, item.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	f.items[item.ID] = item
	return item, nil
}

// GetInventoryItem returns the item.
func (f *fakeStore) GetInventoryItem(_ context.Context, id string) (models.InventoryItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	item, ok := f.items[id]
	if !ok {
		return models.InventoryItem{}, errors.NotFound("inventory_item_not_found", "there is no such item: %s", id)
	}
	return item, nil
}

// LockInventoryItem "locks" the item.
func (f *fakeStore) LockInventoryItem(ctx context.Context, id string) error {
	if err := requireTx(ctx, "LockInventoryItem"); err != nil {
		return err
	}
	f.recordLock("item")
	_, err := f.GetInventoryItem(ctx, id)
	return err
}

// LockInventoryItemShared "locks" the item in shared mode.
//
// In the in-memory store the locks are not real; the behavior imitated is that
// the lock is taken inside a transaction, that the item's EXISTENCE is
// verified, and that the lock ORDER is recorded.
func (f *fakeStore) LockInventoryItemShared(ctx context.Context, id string) error {
	if err := requireTx(ctx, "LockInventoryItemShared"); err != nil {
		return err
	}
	f.recordLock("item")
	_, err := f.GetInventoryItem(ctx, id)
	return err
}

// recordLock appends the lock taken to the order.
func (f *fakeStore) recordLock(kind string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.locks = append(f.locks, kind)
}

// lockOrder returns the locks taken, in order.
func (f *fakeStore) lockOrder() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.locks)
}

// ListInventoryItems filters and pages the items.
func (f *fakeStore) ListInventoryItems(_ context.Context, filter models.InventoryItemFilter) ([]models.InventoryItem, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	all := sortedValues(f.items, func(item models.InventoryItem) string { return item.ID })
	matched := make([]models.InventoryItem, 0, len(all))
	for _, item := range all {
		if filter.SKU != nil && item.SKU != *filter.SKU {
			continue
		}
		if filter.RequiresShipping != nil && item.RequiresShipping != *filter.RequiresShipping {
			continue
		}
		matched = append(matched, item)
	}
	return paginate(matched, filter.Limit, filter.Offset), int64(len(matched)), nil
}

// InventoryItemsByIDs returns the set of IDs.
func (f *fakeStore) InventoryItemsByIDs(_ context.Context, ids []string) ([]models.InventoryItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]models.InventoryItem, 0, len(ids))
	for _, id := range ids {
		if item, ok := f.items[id]; ok {
			out = append(out, item)
		}
	}
	return out, nil
}

// SoftDeleteInventoryItem deletes the item.
func (f *fakeStore) SoftDeleteInventoryItem(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.items[id]; !ok {
		return errors.NotFound("inventory_item_not_found", "there is no such item: %s", id)
	}
	delete(f.items, id)
	f.deletedItems[id] = true
	return nil
}

// SoftDeleteInventoryLevelsByItem deletes the item's levels.
func (f *fakeStore) SoftDeleteInventoryLevelsByItem(_ context.Context, itemID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for key, level := range f.levels {
		if level.InventoryItemID == itemID {
			delete(f.levels, key)
		}
	}
	return nil
}

// LockInventoryLevel "locks" the level and returns it.
func (f *fakeStore) LockInventoryLevel(ctx context.Context, itemID, locationID string) (models.InventoryLevel, error) {
	if err := requireTx(ctx, "LockInventoryLevel"); err != nil {
		return models.InventoryLevel{}, err
	}
	f.recordLock("level")
	return f.GetInventoryLevel(ctx, itemID, locationID)
}

// GetInventoryLevel returns the level.
func (f *fakeStore) GetInventoryLevel(_ context.Context, itemID, locationID string) (models.InventoryLevel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	level, ok := f.levels[levelKey(itemID, locationID)]
	if !ok {
		return models.InventoryLevel{}, errors.NotFound("inventory_level_not_found",
			"there is no such level (%s, %s)", itemID, locationID)
	}
	return level, nil
}

// CreateInventoryLevel records the level.
func (f *fakeStore) CreateInventoryLevel(_ context.Context, level models.InventoryLevel) (models.InventoryLevel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	key := levelKey(level.InventoryItemID, level.LocationID)
	if _, ok := f.levels[key]; ok {
		return models.InventoryLevel{}, errors.Conflict("inventory_level_exists", "the level already exists")
	}
	level.CreatedAt, level.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	f.levels[key] = level
	return level, nil
}

// UpdateInventoryLevelQuantities writes the quantities.
func (f *fakeStore) UpdateInventoryLevelQuantities(_ context.Context, levelID string, stocked, reserved int64) (models.InventoryLevel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.updateLevelCalls++
	for key, level := range f.levels {
		if level.ID != levelID {
			continue
		}
		level.StockedQuantity, level.ReservedQuantity = stocked, reserved
		level.UpdatedAt = time.Now().UTC()
		f.levels[key] = level
		return level, nil
	}
	return models.InventoryLevel{}, errors.NotFound("inventory_level_not_found", "there is no such level: %s", levelID)
}

// ListInventoryLevels returns the item's levels.
func (f *fakeStore) ListInventoryLevels(_ context.Context, itemID string) ([]models.InventoryLevel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	all := sortedValues(f.levels, func(level models.InventoryLevel) string { return level.ID })
	out := make([]models.InventoryLevel, 0, len(all))
	for _, level := range all {
		if level.InventoryItemID == itemID {
			out = append(out, level)
		}
	}
	return out, nil
}

// AvailableByItemIDs returns the sellable total per item.
func (f *fakeStore) AvailableByItemIDs(_ context.Context, ids []string) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.availableCalls++
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}

	out := map[string]int64{}
	for _, level := range f.levels {
		if _, ok := wanted[level.InventoryItemID]; ok {
			out[level.InventoryItemID] += level.Available()
		}
	}
	return out, nil
}

// CreateReservation records the reservation.
func (f *fakeStore) CreateReservation(_ context.Context, res models.Reservation) (models.Reservation, error) {
	if f.failCreateReservation != nil {
		return models.Reservation{}, f.failCreateReservation
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	res.CreatedAt, res.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	f.reservations[res.ID] = res
	return res, nil
}

// LockReservation "locks" the reservation and returns it.
func (f *fakeStore) LockReservation(ctx context.Context, id string) (models.Reservation, error) {
	if err := requireTx(ctx, "LockReservation"); err != nil {
		return models.Reservation{}, err
	}
	f.recordLock("reservation")
	return f.GetReservation(ctx, id)
}

// GetReservation returns the reservation.
func (f *fakeStore) GetReservation(_ context.Context, id string) (models.Reservation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	res, ok := f.reservations[id]
	if !ok {
		return models.Reservation{}, errors.NotFound("inventory_reservation_not_found",
			"there is no such reservation: %s", id)
	}
	return res, nil
}

// SetReservationStatus writes the status.
func (f *fakeStore) SetReservationStatus(_ context.Context, id string, status models.ReservationStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.failSetReservationStatus != nil {
		return f.failSetReservationStatus
	}

	res, ok := f.reservations[id]
	if !ok {
		return errors.NotFound("inventory_reservation_not_found", "there is no such reservation: %s", id)
	}
	res.Status = status
	res.UpdatedAt = time.Now().UTC()
	f.reservations[id] = res
	return nil
}

// CountActiveReservations counts the active reservations.
func (f *fakeStore) CountActiveReservations(_ context.Context, itemID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var count int64
	for id := range f.reservations {
		res := f.reservations[id]
		if res.InventoryItemID == itemID && res.Status == models.ReservationActive {
			count++
		}
	}
	return count, nil
}

// --- test setup helpers -----------------------------------------------------

// seedItem puts an item into the fake store.
func (f *fakeStore) seedItem(id, sku string) models.InventoryItem {
	f.mu.Lock()
	defer f.mu.Unlock()

	item := models.InventoryItem{
		ID: id, SKU: sku, RequiresShipping: true,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	f.items[id] = item
	return item
}

// seedLocation puts an OPEN stock location into the fake store.
func (f *fakeStore) seedLocation(id string) models.StockLocation {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.ensureLocation(id)
}

// seedClosedLocation puts a CLOSED stock location into the fake store.
func (f *fakeStore) seedClosedLocation(id string) models.StockLocation {
	f.mu.Lock()
	defer f.mu.Unlock()

	loc := f.ensureLocation(id)
	closedAt := time.Now().UTC()
	loc.ClosedAt = &closedAt
	f.locations[id] = loc
	return loc
}

// ensureLocation opens the location when there is none; the CALLER must hold
// f.mu.
//
// The level and reservation fixtures call it too: in the real schema both rows
// are tied to stock_locations by a FOREIGN KEY, so a level without a location
// cannot exist at all. A fixture that skipped it would be testing a flow that
// reads the location against a world the database does not allow.
func (f *fakeStore) ensureLocation(id string) models.StockLocation {
	if loc, ok := f.locations[id]; ok {
		return loc
	}
	loc := models.StockLocation{
		ID: id, Name: id,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	f.locations[id] = loc
	return loc
}

// seedMovement puts a ledger row in at a CHOSEN moment.
//
// The moment is a parameter because the listing's order is the thing under
// test: rows written by the flows all land within the same instant, and a
// fixture built that way would let an implementation that returned insertion
// order pass.
func (f *fakeStore) seedMovement(
	id, itemID, locationID string,
	reason models.MovementReason,
	delta, after int64,
	at time.Time,
) models.Movement {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.ensureLocation(locationID)

	mv := models.Movement{
		ID: id, InventoryItemID: itemID, LocationID: locationID,
		Reason: reason, Delta: delta, StockedAfter: after, CreatedAt: at,
	}
	f.movements = append(f.movements, mv)

	return mv
}

// seedLevel puts a stock level into the fake store.
func (f *fakeStore) seedLevel(itemID, locationID string, stocked, reserved int64) models.InventoryLevel {
	return f.seedLevelWithID("invlevel_"+itemID+"_"+locationID, itemID, locationID, stocked, reserved)
}

// seedLevelWithID puts a stock level with a GIVEN ID into the fake store.
//
// [fakeStore.ListInventoryLevels] returns the levels sorted by ID, and the ID
// [fakeStore.seedLevel] produces contains the location ID; so the two orders
// agree on their own. A test of the sorting would, with that fixture, pass
// against an implementation that DOES NOT SORT AT ALL. This helper exists to
// deliberately SEPARATE the order the store returns from the expected order.
func (f *fakeStore) seedLevelWithID(levelID, itemID, locationID string, stocked, reserved int64) models.InventoryLevel {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.ensureLocation(locationID)

	level := models.InventoryLevel{
		ID:               levelID,
		InventoryItemID:  itemID,
		LocationID:       locationID,
		StockedQuantity:  stocked,
		ReservedQuantity: reserved,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	f.levels[levelKey(itemID, locationID)] = level
	return level
}

// seedReservation puts a reservation into the fake store.
func (f *fakeStore) seedReservation(id, itemID, locationID string, qty int64, status models.ReservationStatus) models.Reservation {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.ensureLocation(locationID)

	res := models.Reservation{
		ID: id, InventoryItemID: itemID, LocationID: locationID,
		Quantity: qty, Status: status,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	f.reservations[id] = res
	return res
}

// level returns the level, for the test's assertions.
func (f *fakeStore) level(itemID, locationID string) models.InventoryLevel {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.levels[levelKey(itemID, locationID)]
}

// reservation returns the reservation, for the test's assertions.
func (f *fakeStore) reservation(id string) models.Reservation {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.reservations[id]
}

// AppendMovement records one movement, and refuses to run outside a
// transaction exactly as the real repository does.
//
// The refusal is the half of ADR 0068 a unit test can prove: a movement written
// on the pool would commit while the level update it explains rolled back.
func (f *fakeStore) AppendMovement(ctx context.Context, mv models.Movement) (models.Movement, error) {
	if err := requireTx(ctx, "AppendMovement"); err != nil {
		return models.Movement{}, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if mv.Delta == 0 {
		return models.Movement{}, errors.Invalid("fake_movement_zero_delta",
			"a movement of zero units is refused by the schema's CHECK")
	}
	if !mv.Reason.Valid() {
		return models.Movement{}, errors.Invalid("fake_movement_bad_reason",
			"%q is not one of the reasons the schema's CHECK allows", mv.Reason)
	}
	if (mv.ReservationID != "") != mv.Reason.LeavesAgainstAPromise() {
		return models.Movement{}, errors.Invalid("fake_movement_reservation_mismatch",
			"the schema's CHECK ties a reservation to the reasons that take units out "+
				"against one, and to nothing else")
	}
	if mv.Reason == models.MovementReplacement && mv.Delta >= 0 {
		return models.Movement{}, errors.Invalid("fake_movement_replacement_sign",
			"the schema's CHECK makes a replacement deduct")
	}
	if mv.Reason == models.MovementCancellation && mv.Delta <= 0 {
		return models.Movement{}, errors.Invalid("fake_movement_cancellation_sign",
			"the schema's CHECK makes a cancellation add")
	}
	if mv.Reason == models.MovementSupplierReceipt {
		if mv.Delta <= 0 {
			return models.Movement{}, errors.Invalid("fake_movement_supplier_receipt_sign",
				"the schema's CHECK makes a supplier receipt add")
		}
		for i := range f.movements {
			if f.movements[i].Reason == models.MovementSupplierReceipt && f.movements[i].Reference == mv.Reference {
				return models.Movement{}, errors.Conflict("fake_movement_supplier_receipt_once",
					"the schema's unique index holds one movement per supplier receipt")
			}
		}
	}
	if (mv.Reference != "") != mv.Reason.CarriesAReference() {
		return models.Movement{}, errors.Invalid("fake_movement_reference_mismatch",
			"the schema's CHECKs tie a reference to the reasons that have something to "+
				"point at, and to nothing else")
	}
	// The uniqueness on the reference is GONE from the schema (migration 000007)
	// and so is its imitation here. The same act writes twice when its target
	// grows, and a fake that refused the second write would prove a rule the
	// database no longer has.
	//
	// What the schema DOES still tie to a cancellation is the line, one way:
	// inventory_movements_line_only_on_cancellation allows a line on a
	// cancellation and on nothing else, and a cancellation with none. This check
	// held both ways until a replacement's recall wrote the first such row
	// (ADR 0239), and a fake stricter than its schema refuses a write the
	// database takes.
	if mv.LineItemID != "" && mv.Reason != models.MovementCancellation {
		return models.Movement{}, errors.Invalid("fake_movement_line_mismatch",
			"the schema's CHECK sets line_item_id on a cancellation and on nothing else")
	}

	if mv.CreatedAt.IsZero() {
		mv.CreatedAt = time.Now().UTC()
	}
	f.movements = append(f.movements, mv)

	return mv, nil
}

// ReturnedForLine sums what a line's write-offs have already put back, the way
// the partial index does.
//
// It refuses OUTSIDE a transaction, because the real one does: the sum is only
// safe against two acts arriving at once when it is read under the level's lock,
// and a fake that answered anyway would prove a safety the database does not give.
func (f *fakeStore) ReturnedForLine(
	ctx context.Context, itemID, lineItemID string,
) (int64, error) {
	if err := requireTx(ctx, "ReturnedForLine"); err != nil {
		return 0, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	var returned int64
	for i := range f.movements {
		if f.movements[i].Reason == models.MovementCancellation &&
			f.movements[i].LineItemID == lineItemID &&
			f.movements[i].InventoryItemID == itemID {
			returned += f.movements[i].Delta
		}
	}

	return returned, nil
}

// CancellationRecorded reports whether a cancellation names the reference,
// refusing outside a transaction for [fakeStore.ReturnedForLine]'s reason.
func (f *fakeStore) CancellationRecorded(ctx context.Context, reference string) (bool, error) {
	if err := requireTx(ctx, "CancellationRecorded"); err != nil {
		return false, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	for i := range f.movements {
		if f.movements[i].Reason == models.MovementCancellation && f.movements[i].Reference == reference {
			return true, nil
		}
	}

	return false, nil
}

// SaleLocations answers where an order's units were deducted from, the way the
// partial index does: the FIRST sale movement per item wins.
func (f *fakeStore) SaleLocations(_ context.Context, reference string) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// A claim's fill is left out, as the real query's NOT EXISTS leaves it
	// out (ADR 0392).
	fills := map[string]bool{}
	for id := range f.backorders {
		if f.backorders[id].ReservationID != "" {
			fills[f.backorders[id].ReservationID] = true
		}
	}

	out := map[string]string{}
	for i := range f.movements {
		mv := f.movements[i]
		if mv.Reason != models.MovementSale || mv.Reference != reference || reference == "" {
			continue
		}
		if fills[mv.ReservationID] {
			continue
		}
		if _, seen := out[mv.InventoryItemID]; !seen {
			out[mv.InventoryItemID] = mv.LocationID
		}
	}

	return out, nil
}

// ListMovements pages the item's movements NEWEST FIRST, the way the real
// listing's index does.
func (f *fakeStore) ListMovements(_ context.Context, filter models.MovementFilter) ([]models.Movement, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	matching := make([]models.Movement, 0, len(f.movements))
	for i := range f.movements {
		mv := f.movements[i]
		if mv.InventoryItemID != filter.InventoryItemID {
			continue
		}
		if filter.LocationID != "" && mv.LocationID != filter.LocationID {
			continue
		}
		if !filter.After.Time.IsZero() && !before(mv, filter.After.Time, filter.After.ID) {
			continue
		}
		matching = append(matching, mv)
	}

	slices.SortFunc(matching, func(a, b models.Movement) int {
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return b.CreatedAt.Compare(a.CreatedAt)
		}
		return strings.Compare(b.ID, a.ID)
	})

	return paginate(matching, filter.Limit, 0), nil
}

// before reports whether the movement sits BELOW the given keyset position in
// the listing's own order, which is the row comparison the SQL makes.
func before(mv models.Movement, at time.Time, id string) bool {
	if mv.CreatedAt.Equal(at) {
		return mv.ID < id
	}
	return mv.CreatedAt.Before(at)
}

// movementsFor returns the ledger rows of one item, in append order.
func (f *fakeStore) movementsFor(itemID string) []models.Movement {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]models.Movement, 0, len(f.movements))
	for i := range f.movements {
		if f.movements[i].InventoryItemID == itemID {
			out = append(out, f.movements[i])
		}
	}
	return out
}

// sortedValues returns the map's values sorted by the key function. Without a
// fixed order the list tests would fail at random.
func sortedValues[T any](m map[string]T, key func(T) string) []T {
	out := make([]T, 0, len(m))
	for _, value := range m {
		out = append(out, value)
	}
	slices.SortFunc(out, func(a, b T) int { return strings.Compare(key(a), key(b)) })
	return out
}

// paginate applies limit/offset to the slice.
func paginate[T any](all []T, limit, offset int64) []T {
	if offset >= int64(len(all)) {
		return []T{}
	}
	rest := all[offset:]
	if limit > 0 && int64(len(rest)) > limit {
		rest = rest[:limit]
	}
	return rest
}

// AvailableByItemLocation returns the same numbers broken down by LOCATION.
//
// It counts its reads, for the provider's default field set.
//
// A level with nothing sellable left is ABSENT from the map; the real query does
// not return it as a row either. A fake that wrote a zero would leave the
// caller's "none at this location" branch untested.
func (f *fakeStore) AvailableByItemLocation(
	_ context.Context, ids []string,
) (map[string]map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.byLocationReads++
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}

	out := map[string]map[string]int64{}
	for _, level := range f.levels {
		if _, ok := wanted[level.InventoryItemID]; !ok {
			continue
		}
		if level.Available() <= 0 {
			continue
		}
		byLocation, ok := out[level.InventoryItemID]
		if !ok {
			byLocation = map[string]int64{}
			out[level.InventoryItemID] = byLocation
		}
		byLocation[level.LocationID] += level.Available()
	}

	return out, nil
}

// --- backorders (ADR 0392) ---------------------------------------------------

// CreateBackorder records a claim, or reports false when the line already has
// one for the item, as the unique index makes ON CONFLICT DO NOTHING answer.
func (f *fakeStore) CreateBackorder(_ context.Context, b models.Backorder) (models.Backorder, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for id := range f.backorders {
		if f.backorders[id].OrderLineItemID == b.OrderLineItemID &&
			f.backorders[id].InventoryItemID == b.InventoryItemID {
			return models.Backorder{}, false, nil
		}
	}
	if _, ok := f.items[b.InventoryItemID]; !ok {
		return models.Backorder{}, false, errors.NotFound("inventory_item_not_found",
			"there is no such item: %s", b.InventoryItemID)
	}
	f.backorderSeq++
	b.Seq = f.backorderSeq
	b.Status = models.BackorderWaiting
	b.CreatedAt, b.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	b.LocationIDs = slices.Clone(b.LocationIDs)
	f.backorders[b.ID] = b

	return b, true, nil
}

// GetBackorderOfLine returns the line's claim on the item.
func (f *fakeStore) GetBackorderOfLine(_ context.Context, orderLineItemID, itemID string) (models.Backorder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for id := range f.backorders {
		if f.backorders[id].OrderLineItemID == orderLineItemID && f.backorders[id].InventoryItemID == itemID {
			return f.backorders[id], nil
		}
	}

	return models.Backorder{}, errors.NotFound("inventory_backorder_not_found", "no claim")
}

// claimsInQueue returns the claims passing keep, in seq order; the caller holds
// f.mu.
func (f *fakeStore) claimsInQueue(keep func(models.Backorder) bool) []models.Backorder {
	out := make([]models.Backorder, 0, len(f.backorders))
	for id := range f.backorders {
		if keep(f.backorders[id]) {
			out = append(out, f.backorders[id])
		}
	}
	slices.SortFunc(out, func(a, b models.Backorder) int { return int(a.Seq - b.Seq) })

	return out
}

// LockBackordersOfLine "locks" the line's claims, in queue order.
func (f *fakeStore) LockBackordersOfLine(ctx context.Context, orderLineItemID string) ([]models.Backorder, error) {
	if err := requireTx(ctx, "LockBackordersOfLine"); err != nil {
		return nil, err
	}
	f.recordLock("backorder")

	f.mu.Lock()
	defer f.mu.Unlock()

	return f.claimsInQueue(func(c models.Backorder) bool { return c.OrderLineItemID == orderLineItemID }), nil
}

// LockWaitingBackordersAt "locks" the waiting claims that name the location and
// fit, in queue order, and records the read.
func (f *fakeStore) LockWaitingBackordersAt(
	ctx context.Context, itemID, locationID string, available int64,
) ([]models.Backorder, error) {
	if err := requireTx(ctx, "LockWaitingBackordersAt"); err != nil {
		return nil, err
	}
	f.recordLock("backorder")

	f.mu.Lock()
	defer f.mu.Unlock()

	f.queueReads = append(f.queueReads, queueRead{itemID: itemID, locationID: locationID, available: available})

	return f.claimsInQueue(func(c models.Backorder) bool {
		return c.InventoryItemID == itemID && c.Status == models.BackorderWaiting &&
			slices.Contains(c.LocationIDs, locationID) && c.Owed() <= available
	}), nil
}

// FillBackorder marks a waiting claim filled.
func (f *fakeStore) FillBackorder(_ context.Context, id, reservationID, locationID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	claim, ok := f.backorders[id]
	if !ok || claim.Status != models.BackorderWaiting {
		return 0, nil
	}
	claim.Status, claim.ReservationID, claim.FilledLocationID = models.BackorderFilled, reservationID, locationID
	claim.UpdatedAt = time.Now().UTC()
	f.backorders[id] = claim

	return 1, nil
}

// WithdrawBackorder writes a claim's withdrawn units and status, refusing what
// the schema's CHECKs refuse.
func (f *fakeStore) WithdrawBackorder(
	_ context.Context, id string, withdrawn int64, status models.BackorderStatus,
) (models.Backorder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	claim, ok := f.backorders[id]
	if !ok {
		return models.Backorder{}, errors.NotFound("inventory_backorder_not_found", "no claim %s", id)
	}
	if withdrawn < 0 || withdrawn > claim.Quantity ||
		(status == models.BackorderWithdrawn) != (withdrawn == claim.Quantity) {
		return models.Backorder{}, errors.Invalid("fake_backorder_check",
			"the schema's CHECKs refuse %d withdrawn of %d as %s", withdrawn, claim.Quantity, status)
	}
	claim.WithdrawnQuantity, claim.Status = withdrawn, status
	claim.UpdatedAt = time.Now().UTC()
	f.backorders[id] = claim

	return claim, nil
}

// CountWaitingBackorders counts the item's waiting claims.
func (f *fakeStore) CountWaitingBackorders(_ context.Context, itemID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return int64(len(f.claimsInQueue(func(c models.Backorder) bool {
		return c.InventoryItemID == itemID && c.Status == models.BackorderWaiting
	}))), nil
}

// ListBackorders pages the item's claims in queue order.
func (f *fakeStore) ListBackorders(_ context.Context, filter models.BackorderFilter) ([]models.Backorder, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	matched := f.claimsInQueue(func(c models.Backorder) bool {
		return c.InventoryItemID == filter.InventoryItemID && (filter.Status == "" || c.Status == filter.Status)
	})

	return paginate(matched, filter.Limit, filter.Offset), int64(len(matched)), nil
}

// OpenStockLocationIDs lists the open locations.
func (f *fakeStore) OpenStockLocationIDs(_ context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := []string{}
	for id := range f.locations {
		if loc := f.locations[id]; !loc.Closed() {
			out = append(out, id)
		}
	}
	slices.Sort(out)

	return out, nil
}

// backorder returns the claim, for the test's assertions.
func (f *fakeStore) backorder(id string) models.Backorder {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.backorders[id]
}

// queueReadCount returns how many times a write read the queue.
func (f *fakeStore) queueReadCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.queueReads)
}

// --- supplier receipts (ADR 0399) --------------------------------------------

// CreateSupplierReceipt records an expected receipt, refusing what the schema
// refuses.
func (f *fakeStore) CreateSupplierReceipt(_ context.Context, r models.SupplierReceipt) (models.SupplierReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// The foreign key, which a soft-deleted item still satisfies: only the
	// item's lock keeps a receipt off it.
	if _, ok := f.items[r.InventoryItemID]; !ok && !f.deletedItems[r.InventoryItemID] {
		return models.SupplierReceipt{}, errors.NotFound("inventory_item_not_found", "there is no such item: %s", r.InventoryItemID)
	}
	if r.Quantity <= 0 || (r.Reference != "" && strings.TrimSpace(r.Reference) == "") {
		return models.SupplierReceipt{}, errors.Invalid("fake_supplier_receipt_check", "the schema's CHECKs refuse this receipt")
	}
	r.Status = models.SupplierReceiptExpected
	r.CreatedAt, r.UpdatedAt = time.Now().UTC(), time.Now().UTC()
	f.receipts[r.ID] = r

	return r, nil
}

// GetSupplierReceipt returns the receipt.
func (f *fakeStore) GetSupplierReceipt(_ context.Context, id string) (models.SupplierReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, ok := f.receipts[id]
	if !ok {
		return models.SupplierReceipt{}, errors.NotFound("inventory_supplier_receipt_not_found", "no receipt %s", id)
	}

	return r, nil
}

// LockSupplierReceipt "locks" the receipt and returns it.
func (f *fakeStore) LockSupplierReceipt(ctx context.Context, id string) (models.SupplierReceipt, error) {
	if err := requireTx(ctx, "LockSupplierReceipt"); err != nil {
		return models.SupplierReceipt{}, err
	}
	f.recordLock("receipt")

	return f.GetSupplierReceipt(ctx, id)
}

// ReceiveSupplierReceipt closes an expected receipt with the count and the
// moment of the supplier_receipt movement naming it, as the query's join does.
func (f *fakeStore) ReceiveSupplierReceipt(_ context.Context, id string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, ok := f.receipts[id]
	if !ok || r.Status != models.SupplierReceiptExpected {
		return 0, nil
	}
	for i := range f.movements {
		mv := f.movements[i]
		if mv.Reason != models.MovementSupplierReceipt || mv.Reference != id {
			continue
		}
		at := mv.CreatedAt
		r.Status, r.ReceivedQuantity, r.ReceivedAt, r.UpdatedAt = models.SupplierReceiptReceived, mv.Delta, &at, at
		f.receipts[id] = r

		return 1, nil
	}

	return 0, nil
}

// CancelSupplierReceipt closes an expected receipt as canceled.
func (f *fakeStore) CancelSupplierReceipt(_ context.Context, id string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	r, ok := f.receipts[id]
	if !ok || r.Status != models.SupplierReceiptExpected {
		return 0, nil
	}
	at := time.Now().UTC()
	r.Status, r.CanceledAt, r.UpdatedAt = models.SupplierReceiptCanceled, &at, at
	f.receipts[id] = r

	return 1, nil
}

// receiptsInOrder returns the receipts passing keep by (expected_at, id); the
// caller holds f.mu.
func (f *fakeStore) receiptsInOrder(keep func(models.SupplierReceipt) bool) []models.SupplierReceipt {
	out := make([]models.SupplierReceipt, 0, len(f.receipts))
	for id := range f.receipts {
		if keep(f.receipts[id]) {
			out = append(out, f.receipts[id])
		}
	}
	slices.SortFunc(out, func(a, b models.SupplierReceipt) int {
		if c := a.ExpectedAt.Compare(b.ExpectedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})

	return out
}

// ListSupplierReceipts pages the item's receipts by expected moment.
func (f *fakeStore) ListSupplierReceipts(
	_ context.Context, filter models.SupplierReceiptFilter,
) ([]models.SupplierReceipt, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	matched := f.receiptsInOrder(func(r models.SupplierReceipt) bool {
		return r.InventoryItemID == filter.InventoryItemID && (filter.Status == "" || r.Status == filter.Status)
	})

	return paginate(matched, filter.Limit, filter.Offset), int64(len(matched)), nil
}

// CountExpectedSupplierReceipts counts the item's expected receipts.
func (f *fakeStore) CountExpectedSupplierReceipts(_ context.Context, itemID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return int64(len(f.receiptsInOrder(func(r models.SupplierReceipt) bool {
		return r.InventoryItemID == itemID && r.Status == models.SupplierReceiptExpected
	}))), nil
}

// CountExpectedSupplierReceiptsAtLocation counts the location's expected
// receipts.
func (f *fakeStore) CountExpectedSupplierReceiptsAtLocation(_ context.Context, locationID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return int64(len(f.receiptsInOrder(func(r models.SupplierReceipt) bool {
		return r.LocationID == locationID && r.Status == models.SupplierReceiptExpected
	}))), nil
}

// ExpectedSupplierReceiptsOfItems returns the items' expected receipts not yet
// due, in the REVERSE of the order they are expected: the forecast promises its
// own order rather than borrowing the store's, and a fake that handed it the
// right one would let a forecast that never ordered pass.
func (f *fakeStore) ExpectedSupplierReceiptsOfItems(_ context.Context, itemIDs []string) ([]models.SupplierReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.forecastReads++
	now := f.now
	if now.IsZero() {
		now = time.Now()
	}
	out := f.receiptsInOrder(func(r models.SupplierReceipt) bool {
		return slices.Contains(itemIDs, r.InventoryItemID) && r.Status == models.SupplierReceiptExpected &&
			r.ExpectedAt.After(now)
	})
	slices.Reverse(out)

	return out, nil
}

// WaitingBackordersOfItems returns the items' waiting claims in the REVERSE of
// queue order, for the reason the receipts above are.
func (f *fakeStore) WaitingBackordersOfItems(_ context.Context, itemIDs []string) ([]models.Backorder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := f.claimsInQueue(func(c models.Backorder) bool {
		return slices.Contains(itemIDs, c.InventoryItemID) && c.Status == models.BackorderWaiting
	})
	slices.Reverse(out)

	return out, nil
}

// seedReceipt puts an expected receipt into the fake store.
func (f *fakeStore) seedReceipt(id, itemID, locationID string, quantity int64, at time.Time) models.SupplierReceipt {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.ensureLocation(locationID)
	r := models.SupplierReceipt{
		ID: id, InventoryItemID: itemID, LocationID: locationID, Quantity: quantity, ExpectedAt: at,
		Status: models.SupplierReceiptExpected, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	f.receipts[id] = r

	return r
}

// markReceived closes a seeded receipt as received with its quantity, and
// writes nothing to the stock: a received receipt as the forecast's read finds
// it, whatever the shelf holds.
func (f *fakeStore) markReceived(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	r := f.receipts[id]
	at := time.Now().UTC()
	r.Status, r.ReceivedQuantity, r.ReceivedAt = models.SupplierReceiptReceived, r.Quantity, &at
	f.receipts[id] = r
}

// receipt returns the receipt, for the test's assertions.
func (f *fakeStore) receipt(id string) models.SupplierReceipt {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.receipts[id]
}

// supplierMovements returns the item's supplier_receipt movements.
func (f *fakeStore) supplierMovements(itemID string) []models.Movement {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []models.Movement
	for i := range f.movements {
		if f.movements[i].InventoryItemID == itemID && f.movements[i].Reason == models.MovementSupplierReceipt {
			out = append(out, f.movements[i])
		}
	}

	return out
}
