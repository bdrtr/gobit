package service

// This file is English because ADR 0012 makes language a property of the FILE
// and every new file is English; stock.go beside it is still half Turkish.
//
// # The movement ledger (ADR 0068)
//
// inventory_levels.stocked_quantity stays AUTHORITATIVE and this ledger
// EXPLAINS it. The alternative — deriving the count by summing the ledger —
// would put an aggregate in front of every availability read, including the one
// the storefront listing takes, and would also make the module unable to answer
// anything about the stock that existed before the table did.
//
// The price of keeping both is that they can drift, and the two functions below
// are what stops them. Every write to a level's quantities in this module goes
// through [Service.writeQuantities] or [Service.openLevel]; those two are the
// only callers of the store's CreateInventoryLevel and
// UpdateInventoryLevelQuantities, so a physical change with no explanation is
// not something a caller can forget — there is nowhere to forget it. The
// repository refuses to append a movement outside a transaction, which is the
// other half: the level and its explanation commit together or neither does.
//
// TestEveryPhysicalStockWriteGoesThroughTheLedger in internal/arch holds the
// single-choke-point half from outside this package.

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
)

// openLevel opens a level at the given physical count and records the movement
// that put the stock there.
//
// A level opened EMPTY writes no movement, and that is not a shortcut: nothing
// arrived. The row is the statement that this item is now tracked at this
// location, which the level's own created_at already carries.
func (s *Service) openLevel(
	ctx context.Context,
	itemID, locationID string,
	stocked int64,
	reason models.MovementReason,
) (models.InventoryLevel, error) {
	level, err := s.store.CreateInventoryLevel(ctx, models.InventoryLevel{
		ID:              models.NewInventoryLevelID(),
		InventoryItemID: itemID,
		LocationID:      locationID,
		StockedQuantity: stocked,
	})
	if err != nil {
		return models.InventoryLevel{}, err
	}

	if err := s.recordMovement(ctx, level, stocked, reason, ""); err != nil {
		return models.InventoryLevel{}, err
	}

	return level, nil
}

// writeQuantities writes a level's quantities and, when the PHYSICAL count
// moved, appends the movement that explains the move.
//
// The reservation flows pass no reason and move no goods; every other caller
// passes one. A physical change with no reason comes back as errors.Internal —
// it is a programming error rather than a caller's mistake, and it would leave
// a hole in the ledger that nothing else would report.
func (s *Service) writeQuantities(
	ctx context.Context,
	level models.InventoryLevel,
	stocked, reserved int64,
	reason models.MovementReason,
	reservationID string,
) (models.InventoryLevel, error) {
	updated, err := s.store.UpdateInventoryLevelQuantities(ctx, level.ID, stocked, reserved)
	if err != nil {
		return models.InventoryLevel{}, err
	}

	if err := s.recordMovement(ctx, updated, stocked-level.StockedQuantity, reason, reservationID); err != nil {
		return models.InventoryLevel{}, err
	}

	return updated, nil
}

// recordMovement appends the ledger row for a physical change, or verifies that
// there was none to record.
//
// It is called with the level AFTER the write, so StockedAfter is read off the
// row the database returned rather than off the number this code hoped to
// write.
func (s *Service) recordMovement(
	ctx context.Context,
	level models.InventoryLevel,
	delta int64,
	reason models.MovementReason,
	reservationID string,
) error {
	// A write that moved nothing leaves nothing, whatever the caller meant by
	// it. This is not only the reservation flows: an operator who writes the
	// count they already had has done a real and useful thing — they confirmed
	// it — and it is not a MOVEMENT. The level's own updated_at is where that
	// visit is recorded, and a row here would be a movement of zero units, which
	// the schema refuses anyway.
	if delta == 0 {
		return nil
	}

	if !reason.Valid() {
		return errors.Internal(CodeInconsistentState,
			"the physical count of level %s changed by %d with no reason to record; "+
				"every change to stocked_quantity is explained by a movement (ADR 0068)",
			level.ID, delta)
	}
	if (reservationID != "") != (reason == models.MovementSale) {
		return errors.Internal(CodeInconsistentState,
			"a %q movement carries reservation %q; a reservation is named by a sale and by nothing else",
			reason, reservationID)
	}

	_, err := s.store.AppendMovement(ctx, models.Movement{
		ID:              models.NewMovementID(),
		InventoryItemID: level.InventoryItemID,
		LocationID:      level.LocationID,
		ReservationID:   reservationID,
		Reason:          reason,
		Delta:           delta,
		StockedAfter:    level.StockedQuantity,
	})

	return err
}

// ListMovementsInput is a request for one item's stock movements.
type ListMovementsInput struct {
	// InventoryItemID is the item; it is required.
	InventoryItemID string
	// LocationID narrows the listing to one place; empty means every place.
	LocationID string
	// After is the position the page starts BELOW, in the listing's own order.
	// The zero value is the first page.
	//
	// It is a MOMENT and an ID rather than an opaque string: encoding a position
	// into a cursor is an HTTP concern (ADR 0037's split), and the API layer is
	// what wraps and unwraps it.
	After page.Cursor
	// Limit is the page size; 0 means [DefaultLimit] and more than [MaxLimit] is
	// refused.
	Limit int64
}

// ListMovements returns a page of one item's stock movements, NEWEST FIRST.
//
// # What the ledger answers, and what it does not
//
// It answers "what happened to this item": every change to the physical count,
// with the reason, the location, the signed delta and the count the change
// produced. It does not answer "how much is here" — that is
// [Service.ListInventoryLevels], and the column it reads stays authoritative
// (ADR 0068).
//
// It also does not go back further than the table does. No opening balance was
// written when the ledger was created, so the deltas of an old item DO NOT SUM
// to its current count; the oldest movement's StockedAfter minus its Delta is
// the balance the ledger inherited, and everything before that is not recorded
// anywhere.
//
// # Paging is KEYSET
//
// A ledger is append-only and read newest-first, which is the shape offset is
// worst at: rows arrive while a reader walks, and under offset each arrival
// shifts every later page by one, so somebody following a stock discrepancy
// silently misses a row or sees it twice.
//
// A page SHORTER than the limit is the last page. There is no "has more" flag,
// because computing one means reading a row in order to throw it away.
//
// An item that does not exist is errors.NotFound rather than an empty page:
// "this item has no history" and "there is no such item" are different answers
// and a caller acts differently on them.
func (s *Service) ListMovements(ctx context.Context, in ListMovementsInput) ([]models.Movement, error) {
	if err := requireText("inventory_item_id", in.InventoryItemID); err != nil {
		return nil, err
	}
	if err := checkTextLen("location_id", in.LocationID); err != nil {
		return nil, err
	}
	if in.After.Time.IsZero() != (in.After.ID == "") {
		return nil, errors.Invalid(CodeInvalidInput,
			"a paging position needs both a moment and an id, or neither")
	}

	paging, err := Page{Limit: in.Limit}.normalize()
	if err != nil {
		return nil, err
	}

	// The existence check is what turns "no rows" into "no such item"; it is the
	// same step ListInventoryLevels takes and for the same reason.
	if _, err := s.store.GetInventoryItem(ctx, in.InventoryItemID); err != nil {
		return nil, err
	}

	return s.store.ListMovements(ctx, models.MovementFilter{
		InventoryItemID: in.InventoryItemID,
		LocationID:      in.LocationID,
		After:           in.After,
		Limit:           paging.Limit,
	})
}
