package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/inventory/models"
)

// Store is the persistence surface the service needs.
//
// The interface is declared on the CONSUMING side, which is here (ADR 0001's
// pattern). The service does NOT import the repository package; the concrete
// store satisfies these signatures structurally and the binding is made in
// module.go. That is what lets a unit test be written against a few lines of
// fake store rather than a real database.
//
// # The transaction boundary
//
// [Store.WithTx] runs the given function inside a single database transaction
// and carries the transaction on the context the function receives. Every call
// inside the transaction must therefore use THE ctx THE FUNCTION WAS GIVEN;
// with the outer ctx the call falls outside the transaction and atomicity is
// silently lost.
//
// The methods beginning with Lock hold the row until the end of the
// transaction and may only be called inside [Store.WithTx]. Every flow that
// changes a quantity reads through them: a quantity read without the lock can
// be stale by the time of the write, and two concurrent reservations could take
// the same last unit.
//
// # Lock order
//
// The order is LOCATION, then ITEM, then LEVEL — [Store.LockStockLocation] or
// [Store.LockStockLocationShared], then [Store.LockInventoryItem] or
// [Store.LockInventoryItemShared], then [Store.LockInventoryLevel]. A flow that
// took two of them in the reverse order would wait on a flow waiting on it, and
// the database would detect the deadlock and kill one transaction. The item
// lock is not merely an "does the item exist" check; it IS the order, and it
// takes in advance, in the right place, the implicit item lock that the
// reservation INSERT is going to ask for through its foreign key anyway.
//
// WHICH FLOWS TAKE WHICH. [Service.SetInventoryLevel] and
// [Service.AdjustInventory] take all three. [Service.CloseStockLocation] takes
// the location alone, exclusively. [Service.Reserve],
// [Service.ReleaseReservation] and [Service.ConfirmReservation] take the item
// and the level and NOT the location.
//
// That last omission is safe, and it is not the same thing as taking the three
// out of order. A reservation cannot put stock into a location, so it has
// nothing to keep out of a close. It does touch the location row — the
// reservation INSERT fires the foreign key, which asks for FOR KEY SHARE, and
// asks LAST. Nothing can deadlock on it: FOR KEY SHARE does not conflict with
// the FOR SHARE the stock writes hold, and the close, which does conflict with
// it, holds no item or level lock for a reservation to be waiting on. Only one
// of the two ever holds something the other wants.
//
// The location joined the order on 2026-09-08, with the close (ADR 0055), and
// it is the same kind of step rather than a new idea: a closed location is one
// that holds nothing and takes nothing, and the ONLY way to keep that true is
// for the flows that put stock in to meet the close on one row. They take it
// SHARED and the close takes it EXCLUSIVELY, so stocking two items at the same
// warehouse does not serialize while a close waits for both.
type Store interface {
	// WithTx runs fn in a single transaction; the transaction is rolled back if
	// fn returns an error.
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error

	// CreateStockLocation yeni bir stok lokasyonu kaydeder.
	CreateStockLocation(ctx context.Context, loc models.StockLocation) (models.StockLocation, error)
	// GetStockLocation returns the location by its id; NotFound when there is
	// none. A CLOSED location is returned too: the levels and reservations that
	// name it are still read (ADR 0055).
	GetStockLocation(ctx context.Context, id string) (models.StockLocation, error)
	// ListStockLocations pages the locations; the second value is the total
	// number of matching rows. With includeClosed false only the open ones are
	// returned.
	ListStockLocations(ctx context.Context, limit, offset int64, includeClosed bool) ([]models.StockLocation, int64, error)
	// LockStockLocation locks the location EXCLUSIVELY for the transaction and
	// returns it; NotFound when there is none. The close takes it.
	LockStockLocation(ctx context.Context, id string) (models.StockLocation, error)
	// LockStockLocationShared locks the location in SHARED mode and returns it;
	// NotFound when there is none. Every flow that writes stock takes it first.
	LockStockLocationShared(ctx context.Context, id string) (models.StockLocation, error)
	// CloseStockLocation stamps the location closed and returns it.
	CloseStockLocation(ctx context.Context, id string) (models.StockLocation, error)
	// StockHeldAtLocation returns what the location's living levels hold: the
	// physical total and the promised part of it.
	StockHeldAtLocation(ctx context.Context, locationID string) (stocked, reserved int64, err error)
	// CountActiveReservationsAtLocation returns how many promises still stand
	// at the location.
	CountActiveReservationsAtLocation(ctx context.Context, locationID string) (int64, error)

	// CreateInventoryItem yeni bir stok kalemi kaydeder.
	CreateInventoryItem(ctx context.Context, item models.InventoryItem) (models.InventoryItem, error)
	// GetInventoryItem returns the item by its id, or NotFound.
	GetInventoryItem(ctx context.Context, id string) (models.InventoryItem, error)
	// LockInventoryItem takes an EXCLUSIVE lock on the item for the transaction
	// and proves it exists. The flows that change the item's structure (opening
	// a level, deleting) use it.
	LockInventoryItem(ctx context.Context, id string) error
	// LockInventoryItemShared takes a SHARED lock on the item for the
	// transaction and proves it exists. The flows that touch only quantities use
	// it: they do not wait on each other, but they do conflict with the
	// exclusive lock.
	LockInventoryItemShared(ctx context.Context, id string) error
	// ListInventoryItems filters and pages the items; the second value is the
	// total count.
	ListInventoryItems(ctx context.Context, filter models.InventoryItemFilter) ([]models.InventoryItem, int64, error)
	// InventoryItemsByIDs fetches a set of ids in ONE query (no N+1).
	InventoryItemsByIDs(ctx context.Context, ids []string) ([]models.InventoryItem, error)
	// SoftDeleteInventoryItem soft-deletes the item.
	SoftDeleteInventoryItem(ctx context.Context, id string) error
	// SoftDeleteInventoryLevelsByItem soft-deletes every level of the item.
	SoftDeleteInventoryLevelsByItem(ctx context.Context, itemID string) error

	// LockInventoryLevel locks the level and returns its current state, or
	// NotFound.
	LockInventoryLevel(ctx context.Context, itemID, locationID string) (models.InventoryLevel, error)
	// CreateInventoryLevel yeni bir seviye kaydeder.
	CreateInventoryLevel(ctx context.Context, level models.InventoryLevel) (models.InventoryLevel, error)
	// UpdateInventoryLevelQuantities writes the quantities as ABSOLUTE values.
	UpdateInventoryLevelQuantities(ctx context.Context, levelID string, stocked, reserved int64) (models.InventoryLevel, error)
	// ListInventoryLevels returns the item's levels across every location.
	ListInventoryLevels(ctx context.Context, itemID string) ([]models.InventoryLevel, error)
	// AvailableByItemIDs returns the sellable total per item in ONE query.
	// An item with no level at all is absent from the result.
	AvailableByItemIDs(ctx context.Context, ids []string) (map[string]int64, error)

	// CreateReservation yeni bir rezervasyon kaydeder.
	CreateReservation(ctx context.Context, res models.Reservation) (models.Reservation, error)
	// LockReservation locks the reservation and returns its current state, or
	// NotFound.
	LockReservation(ctx context.Context, id string) (models.Reservation, error)
	// GetReservation returns the reservation without locking it, or NotFound.
	GetReservation(ctx context.Context, id string) (models.Reservation, error)
	// SetReservationStatus rezervasyonun durumunu yazar.
	SetReservationStatus(ctx context.Context, id string, status models.ReservationStatus) error
	// CountActiveReservations returns how many active reservations the item has.
	CountActiveReservations(ctx context.Context, itemID string) (int64, error)
}
