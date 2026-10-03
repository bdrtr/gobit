// Package repository is the database access of the inventory module.
//
// It touches ONLY this module's tables (plan Section 4). The sqlc-generated code
// lives under repository/inventorydb and is not edited by hand; this package
// adds two things on top of it:
//
//   - Conversion: pgtype and the generated row types DO NOT LEAVE THIS PACKAGE,
//     they are converted to models types.
//   - Classification: driver errors are converted into core/errors typed errors;
//     a missing row becomes NotFound, a uniqueness violation becomes Conflict.
//
// # Carrying the transaction
//
// [Repository.WithTx] opens a transaction and puts it into the CONTEXT; every
// repository method called during the transaction runs in that same transaction
// as long as it receives that context. The alternative was to put a separate
// interface type carrying the transaction handle into the method signatures; in
// that case the service could not have matched this package STRUCTURALLY with
// the narrow interface it declares in its own package — in Go the named types in
// a signature have to be identical one for one, meaning the service would have
// been forced to import the repository. Carrying it in the context reduces the
// signatures to the types both sides share (context.Context, models.*).
//
// Locking methods (Lock...) return an error if they are called OUTSIDE a
// transaction: because a FOR UPDATE lock is released once the transaction ends,
// a lock without a transaction would silently protect nothing.
package repository

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/repository/inventorydb"
)

// Error codes. The caller can look at them with errors.CodeOf; the API layer
// passes the same codes on to the client.
const (
	codeItemNotFound        = "inventory_item_not_found"
	codeLocationNotFound    = "inventory_location_not_found"
	codeLevelNotFound       = "inventory_level_not_found"
	codeReservationNotFound = "inventory_reservation_not_found"
	codeSKUExists           = "inventory_sku_exists"
	codeLevelExists         = "inventory_level_exists"
	codeInsufficientStock   = "inventory_insufficient_stock"
	codeTxRequired          = "inventory_tx_required"
	codeQueryFailed         = "inventory_query_failed"
	codeConcurrentUpdate    = "inventory_concurrent_update"
)

// Constraint names; they are used to turn a driver error into a meaningful
// typed error. The names are identical to the ones in the migration.
const (
	constraintSKUUniq       = "inventory_items_sku_uniq"
	constraintLevelUniq     = "inventory_levels_item_location_uniq"
	constraintAvailable     = "inventory_levels_available_nonneg"
	constraintStockedNonneg = "inventory_levels_stocked_nonneg"
)

// PostgreSQL SQLSTATE codes.
const (
	sqlStateUniqueViolation     = "23505"
	sqlStateForeignKeyViolation = "23503"
	sqlStateCheckViolation      = "23514"
	sqlStateDeadlockDetected    = "40P01"
)

// rollbackTimeout is the time granted to a rollback on a canceled context.
// The rollback must be attempted even if the caller's ctx has expired;
// otherwise the transaction would stay open until the connection returned to
// the pool.
const rollbackTimeout = 5 * time.Second

// txKeyType is the type of the context key; it is not exported so that it
// cannot be produced from the outside.
type txKeyType struct{}

// txKey is the key of the transaction handle in the context.
var txKey = txKeyType{}

// Repository is the access to the inventory tables. It is safe for concurrent
// use.
type Repository struct {
	pool *pgxpool.Pool
}

// New produces a Repository working on the given pool.
func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// WithTx runs fn in a single database transaction.
//
// The context given to fn carries the transaction; every repository method
// called with that context runs in the same transaction. If fn returns an error
// or panics, the transaction is rolled back and the error (on a panic, the
// panic) is passed upwards.
//
// If the call nests, a new transaction is NOT opened, the existing one is used:
// opening a nested transaction means a savepoint in PostgreSQL and would give a
// misleading confidence about the atomicity of the outer transaction.
func (r *Repository) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := txFromContext(ctx); ok {
		return fn(ctx)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return classify(err, "inventory_tx_begin_failed", "could not begin transaction")
	}

	committed := false
	defer func() {
		if committed {
			return
		}
		// A short-lived context independent of the caller's is used: if the
		// caller's ctx has been canceled, a rollback made with it would drop
		// instantly too.
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()

	if err := fn(context.WithValue(ctx, txKey, tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return classify(err, "inventory_tx_commit_failed", "could not commit transaction")
	}
	committed = true
	return nil
}

// txFromContext returns the transaction handle in the context.
func txFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey).(pgx.Tx)
	return tx, ok
}

// queries returns the query set appropriate for the context: the one bound to
// the transaction if there is one, otherwise the one bound to the pool.
func (r *Repository) queries(ctx context.Context) *inventorydb.Queries {
	if tx, ok := txFromContext(ctx); ok {
		return inventorydb.New(tx)
	}
	return inventorydb.New(r.pool)
}

// requireTx verifies that locking methods are called inside a transaction.
func requireTx(ctx context.Context, op string) error {
	if _, ok := txFromContext(ctx); !ok {
		return errors.Internal(codeTxRequired,
			"%s must be called inside a transaction; a FOR UPDATE lock without a transaction protects nothing", op)
	}
	return nil
}

// --- stock locations ---------------------------------------------------------

// CreateStockLocation records a new stock location.
func (r *Repository) CreateStockLocation(ctx context.Context, loc models.StockLocation) (models.StockLocation, error) {
	row, err := r.queries(ctx).CreateStockLocation(ctx, inventorydb.CreateStockLocationParams{
		ID:          loc.ID,
		Name:        loc.Name,
		Address1:    nullString(loc.Address1),
		Address2:    nullString(loc.Address2),
		City:        nullString(loc.City),
		Province:    nullString(loc.Province),
		PostalCode:  nullString(loc.PostalCode),
		CountryCode: nullString(loc.CountryCode),
	})
	if err != nil {
		return models.StockLocation{}, classify(err, codeQueryFailed, "the stock location could not be created")
	}
	return toStockLocation(row), nil
}

// GetStockLocation returns the location by its id, or NotFound.
func (r *Repository) GetStockLocation(ctx context.Context, id string) (models.StockLocation, error) {
	row, err := r.queries(ctx).GetStockLocation(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.StockLocation{}, errors.NotFound(codeLocationNotFound,
				"the stock location was not found: %s", id)
		}
		return models.StockLocation{}, classify(err, codeQueryFailed, "the stock location could not be read")
	}
	return toStockLocation(row), nil
}

// LockStockLocation locks the location EXCLUSIVELY for the transaction and
// returns it; NotFound when there is none.
//
// It is the first lock of the module's order and the close is what takes it
// (see the lock order section on the service's Store). Calling it outside a
// transaction is an error: a FOR UPDATE taken without one is released before
// anything can be decided under it.
func (r *Repository) LockStockLocation(ctx context.Context, id string) (models.StockLocation, error) {
	if err := requireTx(ctx, "LockStockLocation"); err != nil {
		return models.StockLocation{}, err
	}
	row, err := r.queries(ctx).LockStockLocation(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.StockLocation{}, errors.NotFound(codeLocationNotFound,
				"the stock location was not found: %s", id)
		}
		return models.StockLocation{}, classify(err, codeQueryFailed, "the location could not be locked")
	}
	return toStockLocation(row), nil
}

// LockStockLocationShared locks the location in SHARED mode and returns it;
// NotFound when there is none.
//
// Every flow that writes stock takes it first. Shared, so those flows do not
// serialize against each other; it collides only with the close.
func (r *Repository) LockStockLocationShared(ctx context.Context, id string) (models.StockLocation, error) {
	if err := requireTx(ctx, "LockStockLocationShared"); err != nil {
		return models.StockLocation{}, err
	}
	row, err := r.queries(ctx).LockStockLocationShared(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.StockLocation{}, errors.NotFound(codeLocationNotFound,
				"the stock location was not found: %s", id)
		}
		return models.StockLocation{}, classify(err, codeQueryFailed, "the location could not be locked")
	}
	return toStockLocation(row), nil
}

// CloseStockLocation stamps the location closed and returns it.
func (r *Repository) CloseStockLocation(ctx context.Context, id string) (models.StockLocation, error) {
	row, err := r.queries(ctx).CloseStockLocation(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.StockLocation{}, errors.NotFound(codeLocationNotFound,
				"the stock location was not found: %s", id)
		}
		return models.StockLocation{}, classify(err, codeQueryFailed, "the location could not be closed")
	}
	return toStockLocation(row), nil
}

// StockHeldAtLocation returns what the location's living levels hold: the
// physical total and the promised part of it.
func (r *Repository) StockHeldAtLocation(ctx context.Context, locationID string) (stocked, reserved int64, err error) {
	row, err := r.queries(ctx).StockHeldAtLocation(ctx, locationID)
	if err != nil {
		return 0, 0, classify(err, codeQueryFailed, "the stock held at the location could not be read")
	}
	return row.StockedQuantity, row.ReservedQuantity, nil
}

// CountActiveReservationsAtLocation returns how many promises still stand at
// the location.
func (r *Repository) CountActiveReservationsAtLocation(ctx context.Context, locationID string) (int64, error) {
	count, err := r.queries(ctx).CountActiveReservationsByLocation(ctx, locationID)
	if err != nil {
		return 0, classify(err, codeQueryFailed, "the active reservations of the location could not be counted")
	}
	return count, nil
}

// ListStockLocations returns the locations page by page. The second return
// value belongs not to the page but to ALL the rows matching the filter.
//
// With includeClosed false only the OPEN locations come back; the closed ones
// enter the listing when they are asked for (ADR 0055).
//
// The total comes from a SEPARATE query, so it is right even on a page that is
// out of range and returns no row at all (see queries/stock_locations.sql).
func (r *Repository) ListStockLocations(ctx context.Context, limit, offset int64, includeClosed bool) ([]models.StockLocation, int64, error) {
	rows, err := r.queries(ctx).ListStockLocations(ctx, inventorydb.ListStockLocationsParams{
		RowLimit:      limit,
		RowOffset:     offset,
		IncludeClosed: includeClosed,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "the stock locations could not be listed")
	}

	total, err := r.queries(ctx).CountStockLocations(ctx, includeClosed)
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "the stock locations could not be counted")
	}

	out := make([]models.StockLocation, 0, len(rows))
	for i := range rows {
		out = append(out, toStockLocation(rows[i]))
	}
	return out, total, nil
}

// --- inventory items ---------------------------------------------------------

// CreateInventoryItem records a new inventory item.
// It returns Conflict if a living item already carries the same SKU.
func (r *Repository) CreateInventoryItem(ctx context.Context, item models.InventoryItem) (models.InventoryItem, error) {
	row, err := r.queries(ctx).CreateInventoryItem(ctx, inventorydb.CreateInventoryItemParams{
		ID:               item.ID,
		Sku:              item.SKU,
		Title:            nullString(item.Title),
		Description:      nullString(item.Description),
		RequiresShipping: item.RequiresShipping,
	})
	if err != nil {
		return models.InventoryItem{}, classify(err, codeQueryFailed, "the inventory item could not be created")
	}
	return toInventoryItem(row), nil
}

// GetInventoryItem returns the item by its id, or NotFound.
func (r *Repository) GetInventoryItem(ctx context.Context, id string) (models.InventoryItem, error) {
	row, err := r.queries(ctx).GetInventoryItem(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.InventoryItem{}, errors.NotFound(codeItemNotFound, "the inventory item was not found: %s", id)
		}
		return models.InventoryItem{}, classify(err, codeQueryFailed, "the inventory item could not be read")
	}
	return toInventoryItem(row), nil
}

// LockInventoryItem locks the item for the transaction and verifies that it
// exists. It returns an error if called outside a transaction.
func (r *Repository) LockInventoryItem(ctx context.Context, id string) error {
	if err := requireTx(ctx, "LockInventoryItem"); err != nil {
		return err
	}
	if _, err := r.queries(ctx).LockInventoryItem(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.NotFound(codeItemNotFound, "the inventory item was not found: %s", id)
		}
		return classify(err, codeQueryFailed, "the inventory item could not be locked")
	}
	return nil
}

// LockInventoryItemShared locks the item in SHARED mode for the transaction and
// verifies that it exists. It returns an error if called outside a
// transaction.
//
// The flows that touch a level or reservation row take it as the first step of
// the lock order; because it is shared, it does not make concurrent
// reservations wait for each other, but it collides with the exclusive lock of
// [Repository.LockInventoryItem] (see queries/inventory_items.sql).
func (r *Repository) LockInventoryItemShared(ctx context.Context, id string) error {
	if err := requireTx(ctx, "LockInventoryItemShared"); err != nil {
		return err
	}
	if _, err := r.queries(ctx).LockInventoryItemShared(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.NotFound(codeItemNotFound, "the inventory item was not found: %s", id)
		}
		return classify(err, codeQueryFailed, "the inventory item could not be locked")
	}
	return nil
}

// ListInventoryItems returns the items filtered and paginated.
// The second return value is the count of ALL the rows matching the filter.
//
// The total comes from a SEPARATE query and applies the same filters as the
// list; it is right even when the page is out of range and returns no row at
// all. A row written between the two queries can change the total by one: the
// total is an informational field of the pagination envelope, and no
// transactional decision is based on it.
func (r *Repository) ListInventoryItems(ctx context.Context, filter models.InventoryItemFilter) ([]models.InventoryItem, int64, error) {
	rows, err := r.queries(ctx).ListInventoryItems(ctx, inventorydb.ListInventoryItemsParams{
		Sku:              filter.SKU,
		RequiresShipping: filter.RequiresShipping,
		RowLimit:         filter.Limit,
		RowOffset:        filter.Offset,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "the inventory items could not be listed")
	}

	total, err := r.queries(ctx).CountInventoryItems(ctx, inventorydb.CountInventoryItemsParams{
		Sku:              filter.SKU,
		RequiresShipping: filter.RequiresShipping,
	})
	if err != nil {
		return nil, 0, classify(err, codeQueryFailed, "the inventory items could not be counted")
	}

	out := make([]models.InventoryItem, 0, len(rows))
	for i := range rows {
		out = append(out, toInventoryItem(rows[i]))
	}
	return out, total, nil
}

// InventoryItemsByIDs returns the items of the given ids in a SINGLE query.
// No row comes back for an id that is not found; that is not an error.
func (r *Repository) InventoryItemsByIDs(ctx context.Context, ids []string) ([]models.InventoryItem, error) {
	if len(ids) == 0 {
		return []models.InventoryItem{}, nil
	}
	rows, err := r.queries(ctx).GetInventoryItemsByIDs(ctx, ids)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the inventory items could not be read")
	}

	out := make([]models.InventoryItem, 0, len(rows))
	for i := range rows {
		out = append(out, toInventoryItem(rows[i]))
	}
	return out, nil
}

// SoftDeleteInventoryItem soft-deletes the item (deleted_at). It returns
// NotFound if the item does not exist or has already been deleted.
func (r *Repository) SoftDeleteInventoryItem(ctx context.Context, id string) error {
	affected, err := r.queries(ctx).SoftDeleteInventoryItem(ctx, id)
	if err != nil {
		return classify(err, codeQueryFailed, "the inventory item could not be deleted")
	}
	if affected == 0 {
		return errors.NotFound(codeItemNotFound, "the inventory item was not found: %s", id)
	}
	return nil
}

// SoftDeleteInventoryLevelsByItem soft-deletes all of the item's inventory
// levels.
func (r *Repository) SoftDeleteInventoryLevelsByItem(ctx context.Context, itemID string) error {
	if err := r.queries(ctx).SoftDeleteInventoryLevelsByItem(ctx, itemID); err != nil {
		return classify(err, codeQueryFailed, "the inventory levels could not be deleted")
	}
	return nil
}

// --- inventory levels --------------------------------------------------------

// LockInventoryLevel locks the (item, location) level for the transaction and
// returns its current state. NotFound if there is no level; an error if called
// outside a transaction.
//
// Every flow that changes a stock quantity does its read through THIS method: a
// quantity read without taking the lock could be stale by the time it is
// written.
func (r *Repository) LockInventoryLevel(ctx context.Context, itemID, locationID string) (models.InventoryLevel, error) {
	if err := requireTx(ctx, "LockInventoryLevel"); err != nil {
		return models.InventoryLevel{}, err
	}
	row, err := r.queries(ctx).LockInventoryLevel(ctx, inventorydb.LockInventoryLevelParams{
		InventoryItemID: itemID,
		LocationID:      locationID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.InventoryLevel{}, levelNotFound(itemID, locationID)
		}
		return models.InventoryLevel{}, classify(err, codeQueryFailed, "the inventory level could not be locked")
	}
	return toInventoryLevel(row), nil
}

// CreateInventoryLevel records a new inventory level.
func (r *Repository) CreateInventoryLevel(ctx context.Context, level models.InventoryLevel) (models.InventoryLevel, error) {
	row, err := r.queries(ctx).CreateInventoryLevel(ctx, inventorydb.CreateInventoryLevelParams{
		ID:               level.ID,
		InventoryItemID:  level.InventoryItemID,
		LocationID:       level.LocationID,
		StockedQuantity:  level.StockedQuantity,
		ReservedQuantity: level.ReservedQuantity,
	})
	if err != nil {
		return models.InventoryLevel{}, classify(err, codeQueryFailed, "the inventory level could not be created")
	}
	return toInventoryLevel(row), nil
}

// UpdateInventoryLevelQuantities writes the level's quantities as ABSOLUTE
// values.
//
// An incremental update (quantity = quantity + n) is deliberately not used: the
// new value is computed from the value read under the lock, so the number the
// deciding code saw and the number written are the same.
func (r *Repository) UpdateInventoryLevelQuantities(ctx context.Context, levelID string, stocked, reserved int64) (models.InventoryLevel, error) {
	row, err := r.queries(ctx).UpdateInventoryLevelQuantities(ctx, inventorydb.UpdateInventoryLevelQuantitiesParams{
		ID:               levelID,
		StockedQuantity:  stocked,
		ReservedQuantity: reserved,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.InventoryLevel{}, errors.NotFound(codeLevelNotFound,
				"the inventory level was not found: %s", levelID)
		}
		return models.InventoryLevel{}, classify(err, codeQueryFailed, "the inventory level could not be updated")
	}
	return toInventoryLevel(row), nil
}

// ListInventoryLevels returns the item's levels at all locations.
func (r *Repository) ListInventoryLevels(ctx context.Context, itemID string) ([]models.InventoryLevel, error) {
	rows, err := r.queries(ctx).ListInventoryLevels(ctx, itemID)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the inventory levels could not be listed")
	}

	out := make([]models.InventoryLevel, 0, len(rows))
	for i := range rows {
		out = append(out, toInventoryLevel(rows[i]))
	}
	return out, nil
}

// AvailableByItemIDs returns, per item, the sellable total across ALL
// locations in a SINGLE query. An item with no level at all is absent from the
// result; the caller must count it as zero.
func (r *Repository) AvailableByItemIDs(ctx context.Context, ids []string) (map[string]int64, error) {
	if len(ids) == 0 {
		return map[string]int64{}, nil
	}
	rows, err := r.queries(ctx).AvailableQuantityByItemIDs(ctx, ids)
	if err != nil {
		return nil, classify(err, codeQueryFailed, "the available quantity could not be computed")
	}

	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.InventoryItemID] = row.AvailableQuantity
	}
	return out, nil
}

// AvailableByItemLocation returns the sellable quantity broken down by item and
// LOCATION.
//
// A location left empty is ABSENT from the map: the query returns rows, not a
// count (see [Repository.AvailableByItemIDs]). The caller sums the locations
// its channel ships from; a missing location contributes zero and does not need
// to be written out separately.
func (r *Repository) AvailableByItemLocation(
	ctx context.Context, ids []string,
) (map[string]map[string]int64, error) {
	out := make(map[string]map[string]int64, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	rows, err := r.queries(ctx).AvailableByItemAndLocation(ctx, ids)
	if err != nil {
		return nil, classify(err, codeQueryFailed,
			"the available quantity per location could not be computed")
	}

	for _, row := range rows {
		byLocation, ok := out[row.InventoryItemID]
		if !ok {
			byLocation = map[string]int64{}
			out[row.InventoryItemID] = byLocation
		}
		byLocation[row.LocationID] = row.AvailableQuantity
	}

	return out, nil
}

// --- reservations ------------------------------------------------------------

// CreateReservation records a new reservation.
func (r *Repository) CreateReservation(ctx context.Context, res models.Reservation) (models.Reservation, error) {
	row, err := r.queries(ctx).CreateReservation(ctx, inventorydb.CreateReservationParams{
		ID:              res.ID,
		InventoryItemID: res.InventoryItemID,
		LocationID:      res.LocationID,
		Quantity:        res.Quantity,
		LineItemID:      nullString(res.LineItemID),
		Status:          res.Status.String(),
		Description:     nullString(res.Description),
		Purpose:         res.Purpose.String(),
	})
	if err != nil {
		return models.Reservation{}, classify(err, codeQueryFailed, "the reservation could not be created")
	}
	return toReservation(row), nil
}

// LockReservation locks the reservation for the transaction and returns its
// current state.
//
// Status transitions are made only under this lock: of two calls trying to
// release the same reservation at the same moment, the second sees the status
// the first one wrote and does not give the stock back a second time.
func (r *Repository) LockReservation(ctx context.Context, id string) (models.Reservation, error) {
	if err := requireTx(ctx, "LockReservation"); err != nil {
		return models.Reservation{}, err
	}
	row, err := r.queries(ctx).LockReservation(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Reservation{}, errors.NotFound(codeReservationNotFound,
				"the reservation was not found: %s", id)
		}
		return models.Reservation{}, classify(err, codeQueryFailed, "the reservation could not be locked")
	}
	return toReservation(row), nil
}

// GetReservation returns the reservation without locking it, or NotFound.
func (r *Repository) GetReservation(ctx context.Context, id string) (models.Reservation, error) {
	row, err := r.queries(ctx).GetReservation(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return models.Reservation{}, errors.NotFound(codeReservationNotFound,
				"the reservation was not found: %s", id)
		}
		return models.Reservation{}, classify(err, codeQueryFailed, "the reservation could not be read")
	}
	return toReservation(row), nil
}

// SetReservationStatus writes the reservation's status; NotFound if there is
// no such record.
func (r *Repository) SetReservationStatus(ctx context.Context, id string, status models.ReservationStatus) error {
	affected, err := r.queries(ctx).SetReservationStatus(ctx, inventorydb.SetReservationStatusParams{
		ID:     id,
		Status: status.String(),
	})
	if err != nil {
		return classify(err, codeQueryFailed, "the reservation status could not be updated")
	}
	if affected == 0 {
		return errors.NotFound(codeReservationNotFound, "the reservation was not found: %s", id)
	}
	return nil
}

// CountActiveReservations returns the number of the item's active
// reservations.
func (r *Repository) CountActiveReservations(ctx context.Context, itemID string) (int64, error) {
	count, err := r.queries(ctx).CountActiveReservationsByItem(ctx, itemID)
	if err != nil {
		return 0, classify(err, codeQueryFailed, "the active reservations could not be counted")
	}
	return count, nil
}

// --- conversion and error classification ------------------------------------

// levelNotFound produces the shared error for a missing level.
func levelNotFound(itemID, locationID string) error {
	return errors.NotFound(codeLevelNotFound,
		"the inventory level was not found (item: %s, location: %s)", itemID, locationID)
}

// classify converts a driver error into a typed error.
//
// Uniqueness, foreign key and CHECK violations are cases the client can correct;
// if they were not classified they would all appear as 500 and the real reason
// would remain only in the log. A deadlock is handled separately for the same
// reason: there is nothing wrong with the transaction itself, it CAN BE RETRIED.
func classify(err error, code, format string, a ...any) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return errors.Wrap(err, errors.KindInternal, code, format, a...)
	}

	switch pgErr.Code {
	case sqlStateUniqueViolation:
		switch pgErr.ConstraintName {
		case constraintSKUUniq:
			return errors.Wrap(err, errors.KindConflict, codeSKUExists,
				"this SKU is already in use")
		case constraintLevelUniq:
			return errors.Wrap(err, errors.KindConflict, codeLevelExists,
				"this item already has an inventory level at this location")
		}
	case sqlStateForeignKeyViolation:
		// A level/reservation row cannot be bound to an item or a location that
		// does not exist. The constraint name says which one it is.
		if strings.Contains(pgErr.ConstraintName, "location_id") {
			return errors.Wrap(err, errors.KindNotFound, codeLocationNotFound,
				"the stock location was not found")
		}
		return errors.Wrap(err, errors.KindNotFound, codeItemNotFound,
			"the inventory item was not found")
	case sqlStateCheckViolation:
		switch pgErr.ConstraintName {
		case constraintAvailable, constraintStockedNonneg:
			return errors.Wrap(err, errors.KindConflict, codeInsufficientStock,
				"insufficient stock: the available quantity cannot fall below zero")
		}
	case sqlStateDeadlockDetected:
		// Because the lock order has been made uniform this does not occur in
		// the normal flows; this is the last line of defense. The transaction
		// has been rolled back, the same request can be retried as it is —
		// which is why this is Conflict and not Internal (500).
		return errors.Wrap(err, errors.KindConflict, codeConcurrentUpdate,
			"conflicted with a concurrent transaction; the request can be retried")
	}
	return errors.Wrap(err, errors.KindInternal, code, format, a...)
}

// nullString turns an empty string into SQL NULL.
func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// stringValue turns SQL NULL into an empty string.
func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// timeValue turns a pgtype stamp into a UTC time.Time.
func timeValue(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

// timePointer turns a nullable stamp into a pointer.
//
// It is separate from [timeValue] because the two answer different questions. A
// zero time.Time is the right reading of a NULL created_at — there is no such
// row — while a NULL closed_at MEANS something: the location is open. Folding
// it into the zero value would make "open" and "closed at the zero instant"
// the same value.
func timePointer(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	at := ts.Time.UTC()
	return &at
}

// toStockLocation converts the generated row into the domain model.
func toStockLocation(row inventorydb.StockLocation) models.StockLocation {
	return models.StockLocation{
		ID:          row.ID,
		Name:        row.Name,
		Address1:    stringValue(row.Address1),
		Address2:    stringValue(row.Address2),
		City:        stringValue(row.City),
		Province:    stringValue(row.Province),
		PostalCode:  stringValue(row.PostalCode),
		CountryCode: stringValue(row.CountryCode),
		CreatedAt:   timeValue(row.CreatedAt),
		UpdatedAt:   timeValue(row.UpdatedAt),
		ClosedAt:    timePointer(row.ClosedAt),
	}
}

// toInventoryItem converts the generated row into the domain model.
func toInventoryItem(row inventorydb.InventoryItem) models.InventoryItem {
	return models.InventoryItem{
		ID:               row.ID,
		SKU:              row.Sku,
		Title:            stringValue(row.Title),
		Description:      stringValue(row.Description),
		RequiresShipping: row.RequiresShipping,
		CreatedAt:        timeValue(row.CreatedAt),
		UpdatedAt:        timeValue(row.UpdatedAt),
	}
}

// toInventoryLevel converts the generated row into the domain model.
func toInventoryLevel(row inventorydb.InventoryLevel) models.InventoryLevel {
	return models.InventoryLevel{
		ID:               row.ID,
		InventoryItemID:  row.InventoryItemID,
		LocationID:       row.LocationID,
		StockedQuantity:  row.StockedQuantity,
		ReservedQuantity: row.ReservedQuantity,
		CreatedAt:        timeValue(row.CreatedAt),
		UpdatedAt:        timeValue(row.UpdatedAt),
	}
}

// toReservation converts the generated row into the domain model.
func toReservation(row inventorydb.InventoryReservation) models.Reservation {
	return models.Reservation{
		ID:              row.ID,
		InventoryItemID: row.InventoryItemID,
		LocationID:      row.LocationID,
		Quantity:        row.Quantity,
		LineItemID:      stringValue(row.LineItemID),
		Description:     stringValue(row.Description),
		Purpose:         models.ReservationPurpose(row.Purpose),
		Status:          models.ReservationStatus(row.Status),
		CreatedAt:       timeValue(row.CreatedAt),
		UpdatedAt:       timeValue(row.UpdatedAt),
	}
}
