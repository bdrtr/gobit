package service

import (
	"context"
	"math"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
)

// SetInventoryLevel writes the PHYSICAL quantity of an item at a location.
//
// The level is created when there is none, and otherwise stocked_quantity is
// updated to the absolute value given; the reserved quantity DOES NOT CHANGE.
// A new physical quantity below the reserved one is errors.Conflict: promised
// stock cannot evaporate silently through a stock count — the reservations have
// to be released first.
//
// The whole of the work is done under the item lock, in one transaction. The
// lock also keeps two concurrent creations for the same (item, location) from
// colliding on the unique index: whichever is going to create the row wins the
// race at the lock, and the other waits, then sees the existing row and updates
// it.
//
// The first step of the lock order is the LOCATION
// ([Service.requireOpenLocation]): a closed location takes no stock, and the
// close locks that same row exclusively, so this write either finishes before
// the close or waits and then finds the location closed (ADR 0055).
func (s *Service) SetInventoryLevel(ctx context.Context, itemID, locationID string, stockedQty int64) (models.InventoryLevel, error) {
	return s.setInventoryLevel(ctx, itemID, locationID, stockedQty, nil)
}

// SetInventoryLevelFrom writes the physical quantity as [Service.SetInventoryLevel]
// does, and only when the level still holds read, the quantity the writer was
// shown (ADR 0280). A level that moved since — a sale took a unit, somebody else
// counted — is refused with [CodeStockMoved] and nothing is written: an
// absolute count written over a quantity it never saw would erase the movement
// in between. A location with no level yet holds zero.
func (s *Service) SetInventoryLevelFrom(
	ctx context.Context, itemID, locationID string, read, stockedQty int64,
) (models.InventoryLevel, error) {
	return s.setInventoryLevel(ctx, itemID, locationID, stockedQty, &read)
}

// setInventoryLevel is the write behind both; read, when given, is compared
// with the level under its lock.
func (s *Service) setInventoryLevel(
	ctx context.Context, itemID, locationID string, stockedQty int64, read *int64,
) (models.InventoryLevel, error) {
	if err := requireIDs(itemID, locationID); err != nil {
		return models.InventoryLevel{}, err
	}
	if stockedQty < 0 {
		return models.InventoryLevel{}, errors.Invalid(CodeInvalidInput,
			"the stocked quantity cannot be negative: %d", stockedQty)
	}

	var out models.InventoryLevel
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		if err := s.requireOpenLocation(ctx, locationID); err != nil {
			return err
		}
		if err := s.store.LockInventoryItem(ctx, itemID); err != nil {
			return err
		}

		level, err := s.store.LockInventoryLevel(ctx, itemID, locationID)
		if err != nil {
			// The item is locked at this point and its existence has been
			// verified; the only "not found" possible here is that the level
			// does not exist yet.
			if !errors.HasKind(err, errors.KindNotFound) {
				return err
			}
			if err := stockUnmoved(read, 0, locationID); err != nil {
				return err
			}
			created, createErr := s.openLevel(ctx, itemID, locationID, stockedQty,
				models.MovementStockCount)
			if createErr != nil {
				return createErr
			}
			out = created
			return nil
		}

		if err := stockUnmoved(read, level.StockedQuantity, locationID); err != nil {
			return err
		}
		if stockedQty < level.ReservedQuantity {
			return errors.Conflict(CodeInsufficientStock,
				"the physical quantity (%d) cannot be lowered below the reserved quantity (%d); release the reservations first",
				stockedQty, level.ReservedQuantity)
		}

		updated, err := s.writeQuantities(ctx, level, stockedQty, level.ReservedQuantity,
			models.MovementStockCount, "", "", "")
		if err != nil {
			return err
		}
		out = updated
		return nil
	})
	if err != nil {
		return models.InventoryLevel{}, err
	}
	return out, nil
}

// stockUnmoved refuses a count written over a quantity the writer was not
// shown: read is what they saw, current what the locked level holds.
func stockUnmoved(read *int64, current int64, locationID string) error {
	if read == nil || *read == current {
		return nil
	}

	return errors.Conflict(CodeStockMoved,
		"the physical count at %s is %d, not the %d the form was drawn with; nothing was saved, "+
			"so look again and count from what is there", locationID, current, *read)
}

// AdjustInventory raises or lowers the physical quantity by delta, as an
// OPERATOR'S correction: breakage, a recount, a transfer recorded by hand.
//
// The result CANNOT GO NEGATIVE and cannot fall below the reserved quantity; in
// either case errors.Conflict comes back and nothing is written. The read is
// made under the row lock, so two concurrent corrections do not overwrite each
// other.
//
// The locks are taken location -> item -> level (see the lock order section on
// [Store]); a missing location or item is errors.NotFound, and a closed
// location is errors.Conflict (ADR 0055).
//
// The movement it leaves in the ledger says "adjustment". Goods coming BACK
// from a customer are the same arithmetic and a different fact, so they have
// their own entry point ([Service.RestockInventory]) rather than a reason
// parameter on this one: a reason a caller passes is a reason a caller can get
// wrong, and the admin endpoint would have to invent one for every request.
func (s *Service) AdjustInventory(ctx context.Context, itemID, locationID string, delta int64) (models.InventoryLevel, error) {
	if delta == 0 {
		return models.InventoryLevel{}, errors.Invalid(CodeInvalidInput, "delta cannot be zero")
	}

	return s.adjust(ctx, itemID, locationID, delta, models.MovementAdjustment)
}

// RestockInventory puts returned goods BACK at a location.
//
// It is [Service.AdjustInventory]'s arithmetic under a different name, and the
// name is the point: the ledger has to be able to tell a warehouse correction
// from goods a customer sent back, and nothing in a positive delta says which
// one it was.
//
// It is NOT the undoing of a reservation. A confirmed reservation cannot be
// released — the units left the count for good — so a return is an ARRIVAL, and
// two calls add the stock twice because two calls mean two physical arrivals.
// The caller is responsible for calling it once per receipt; the return record
// is what makes that possible, since a return can only be received once.
//
// The quantity has to be POSITIVE. The check lives here rather than in the
// cross-module surface that calls it, because that surface's own rule is that
// it translates signatures and holds no rules of its own.
func (s *Service) RestockInventory(ctx context.Context, itemID, locationID string, quantity int64) (models.InventoryLevel, error) {
	if quantity <= 0 {
		return models.InventoryLevel{}, errors.Invalid(CodeInvalidInput,
			"the restocked quantity has to be positive: %d (item %s)", quantity, itemID)
	}

	return s.adjust(ctx, itemID, locationID, quantity, models.MovementReturnRestock)
}

// adjust is the shared body of [Service.AdjustInventory] and
// [Service.RestockInventory]; the reason is what the two disagree about.
func (s *Service) adjust(
	ctx context.Context,
	itemID, locationID string,
	delta int64,
	reason models.MovementReason,
) (models.InventoryLevel, error) {
	if err := requireIDs(itemID, locationID); err != nil {
		return models.InventoryLevel{}, err
	}

	var out models.InventoryLevel
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		if err := s.requireOpenLocation(ctx, locationID); err != nil {
			return err
		}
		if err := s.store.LockInventoryItemShared(ctx, itemID); err != nil {
			return err
		}

		level, err := s.store.LockInventoryLevel(ctx, itemID, locationID)
		if err != nil {
			return err
		}

		newStocked, err := addQuantity(level.StockedQuantity, delta)
		if err != nil {
			return err
		}
		// One check covers both rules: since the reserved quantity can never
		// be negative, by the database constraint, the condition
		// "newStocked >= reserved" also covers "newStocked >= 0". Writing a
		// second if would leave a dead branch that no input could reach.
		if newStocked < level.ReservedQuantity {
			return errors.Conflict(CodeInsufficientStock,
				"the stock cannot be lowered that far: the result would be %d (current %d, reserved %d); the sellable quantity cannot go negative",
				newStocked, level.StockedQuantity, level.ReservedQuantity)
		}

		updated, err := s.writeQuantities(ctx, level, newStocked, level.ReservedQuantity, reason, "", "", "")
		if err != nil {
			return err
		}
		out = updated
		return nil
	})
	if err != nil {
		return models.InventoryLevel{}, err
	}
	return out, nil
}

// ListInventoryLevels returns the item's stock levels across every location.
// When the item does not exist it returns errors.NotFound; for an item with no
// level, an empty slice.
func (s *Service) ListInventoryLevels(ctx context.Context, itemID string) ([]models.InventoryLevel, error) {
	if err := requireText("inventory_item_id", itemID); err != nil {
		return nil, err
	}
	// The existence check makes a missing item answer "no such item" rather
	// than "no stock"; the two mean different things to the caller.
	if _, err := s.store.GetInventoryItem(ctx, itemID); err != nil {
		return nil, err
	}
	return s.store.ListInventoryLevels(ctx, itemID)
}

// AvailableQuantity returns the item's sellable total across ALL locations.
//
// The total is derived from each level's stocked - reserved difference. When
// the item does not exist it returns errors.NotFound; for an item with no level
// at all, 0.
func (s *Service) AvailableQuantity(ctx context.Context, itemID string) (int64, error) {
	levels, err := s.ListInventoryLevels(ctx, itemID)
	if err != nil {
		return 0, err
	}

	var total int64
	for _, level := range levels {
		total += level.Available()
	}
	return total, nil
}

// AvailableQuantitiesByLocation returns the sellable quantity broken down by
// item and LOCATION.
//
// # Why a breakdown and not a filtered total
//
// The read that asks for this is a storefront read passing through the Query
// layer (ADR 0004), and an EXPANSION carries no filter: the provider is given
// IDs and field names, not "which locations you may count". So all of them come
// back, and the caller sums the ones the sales channel ships from (ADR 0092).
//
// A location left empty is ABSENT from the map; a missing location contributes
// zero.
func (s *Service) AvailableQuantitiesByLocation(
	ctx context.Context, itemIDs []string,
) (map[string]map[string]int64, error) {
	if len(itemIDs) == 0 {
		return map[string]map[string]int64{}, nil
	}

	return s.store.AvailableByItemLocation(ctx, itemIDs)
}

// AvailableQuantities returns the sellable totals of the given items in ONE
// query. An item with no level at all appears in the result with zero.
//
// The Query provider uses it: product's storefront listing makes a single round
// trip for stock however many products there are (no N+1).
func (s *Service) AvailableQuantities(ctx context.Context, itemIDs []string) (map[string]int64, error) {
	if len(itemIDs) == 0 {
		return map[string]int64{}, nil
	}

	found, err := s.store.AvailableByItemIDs(ctx, itemIDs)
	if err != nil {
		return nil, err
	}

	out := make(map[string]int64, len(itemIDs))
	for _, id := range itemIDs {
		out[id] = found[id]
	}
	return out, nil
}

// LocationsWithStock returns the IDs of the locations from which AT LEAST
// quantity units of the item can be reserved.
//
// "Reservable" is defined THE SAME way as in [Service.Reserve]: each level's
// [models.InventoryLevel.Available] value, that is stocked - reserved. The list
// is filtered from the very levels [Service.AvailableQuantity] sums; writing a
// second definition of "available" (a separate query that looks only at the
// physical quantity, for example) would produce locations that show up in the
// list but get errors.Conflict from Reserve.
//
// # The order is a FACT, not a policy
//
// The result is in ascending order of LOCATION ID, and that order is
// DETERMINISTIC. An order such as "most stocked first" looks tempting but is
// wrong: which warehouse ships is a SHIPPING DECISION and belongs to
// fulfillment; this method returns only a stock fact. Hiding policy in the
// order would make the decision in a place nobody looks — the stock module's
// sorting.
//
// # The result is a list of CANDIDATES
//
// The list is read without locks, so it can be stale the moment it is
// returned: a cart that comes in between can take the last unit. The ONE
// authority on sufficiency is [Service.Reserve], which decides inside a
// transaction and under the row lock. This method does not replace it; it only
// narrows down the locations where Reserve CAN BE TRIED.
//
// When no location is enough it returns an EMPTY slice, not an error: "not
// enough stock" is an answer, not a fault, and the caller chooses to turn it
// into a Conflict in its own context (in the saga step). When the item does not
// exist it returns errors.NotFound; "it has no stock" and "it does not exist"
// are different situations for the caller.
//
// quantity has to be POSITIVE, otherwise errors.Invalid is returned. A zero or
// negative threshold would list locations for a quantity Reserve refuses
// outright: every location returned would blow up at the reservation. Silently
// returning an empty list, on the other hand, would hide the caller's mistake
// by making it look like "no stock".
func (s *Service) LocationsWithStock(ctx context.Context, itemID string, quantity int64) ([]string, error) {
	if quantity <= 0 {
		return nil, errors.Invalid(CodeInvalidInput,
			"the requested quantity has to be positive: %d", quantity)
	}

	// The item ID is validated and its existence checked in
	// ListInventoryLevels; repeating that here would let the same rule drift
	// apart in two places.
	levels, err := s.ListInventoryLevels(ctx, itemID)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(levels))
	for _, level := range levels {
		if level.Available() >= quantity {
			out = append(out, level.LocationID)
		}
	}
	// Levels are unique per (item, location) pair, so the list holds no
	// duplicates; sorting alone is enough.
	slices.Sort(out)
	return out, nil
}

// ReserveInput is a reservation request.
type ReserveInput struct {
	// InventoryItemID is the item to reserve; required.
	InventoryItemID string
	// LocationID is the location the stock is set aside at; required.
	LocationID string
	// Quantity is the number of units to set aside; it has to be positive.
	Quantity int64
	// LineItemID is the cart/order line asking for the reservation; optional.
	// It is an ID belonging to the cart module and is not a foreign key here.
	LineItemID string
	// Description is an optional free-text description.
	Description string
	// Purpose is WHY the stock is set aside; left empty, it reads as
	// [models.PurposeSale].
	//
	// This is the field that decides the movement reason the confirmation will
	// write: when the goods leave the warehouse, the ledger learns from this
	// promise whether it was a "sale" or a "replacement". Passing the reason to
	// the confirmation as a parameter was rejected — the confirmation is called
	// from the saga, from a retry and from the recovery path, and in the third
	// the caller does not have that information.
	Purpose models.ReservationPurpose
}

// Reserve sets the requested quantity aside from the sellable stock.
//
// When there is not enough stock it returns errors.Conflict (code:
// [CodeInsufficientStock]) and nothing is written. The level row is locked for
// the whole transaction, so of two calls racing for the last unit EXACTLY ONE
// wins.
//
// The locks are taken item -> level (see "Lock order" on [Store]). The item
// lock is SHARED: it does not serialize concurrent reservations, and conflicts
// only with the flows that change the item structurally (SetInventoryLevel,
// DeleteInventoryItem). When the item does not exist it returns
// errors.NotFound.
//
// This is the stock step of Phase 6's complete_cart saga; its compensation is
// [Service.ReleaseReservation].
func (s *Service) Reserve(ctx context.Context, in ReserveInput) (models.Reservation, error) {
	if err := requireIDs(in.InventoryItemID, in.LocationID); err != nil {
		return models.Reservation{}, err
	}
	if in.Quantity <= 0 {
		return models.Reservation{}, errors.Invalid(CodeInvalidInput,
			"the reservation quantity has to be positive: %d", in.Quantity)
	}
	if err := checkTextLen("line_item_id", in.LineItemID); err != nil {
		return models.Reservation{}, err
	}
	if err := checkTextLen("description", in.Description); err != nil {
		return models.Reservation{}, err
	}
	purpose := in.Purpose
	if purpose == "" {
		purpose = models.PurposeSale
	}
	if !purpose.Valid() {
		return models.Reservation{}, errors.Invalid(CodeInvalidInput,
			"unknown reservation purpose: %q", in.Purpose)
	}

	var out models.Reservation
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		if err := s.store.LockInventoryItemShared(ctx, in.InventoryItemID); err != nil {
			return err
		}

		level, err := s.store.LockInventoryLevel(ctx, in.InventoryItemID, in.LocationID)
		if err != nil {
			return err
		}

		available := level.Available()
		if available < in.Quantity {
			return errors.Conflict(CodeInsufficientStock,
				"insufficient stock: %d sellable, %d requested (item: %s, location: %s)",
				available, in.Quantity, in.InventoryItemID, in.LocationID)
		}

		newReserved, err := addQuantity(level.ReservedQuantity, in.Quantity)
		if err != nil {
			return err
		}
		// No movement: a reservation promises stock, it does not move it. The
		// physical count is unchanged, and inventory_reservations is already
		// this fact's record (ADR 0068).
		if _, err := s.writeQuantities(ctx, level, level.StockedQuantity, newReserved, "", "", "", ""); err != nil {
			return err
		}

		created, err := s.store.CreateReservation(ctx, models.Reservation{
			ID:              models.NewReservationID(),
			InventoryItemID: in.InventoryItemID,
			LocationID:      in.LocationID,
			Quantity:        in.Quantity,
			LineItemID:      strings.TrimSpace(in.LineItemID),
			Description:     strings.TrimSpace(in.Description),
			Purpose:         purpose,
			Status:          models.ReservationActive,
		})
		if err != nil {
			return err
		}
		out = created
		return nil
	})
	if err != nil {
		return models.Reservation{}, err
	}
	return out, nil
}

// ReleaseReservation takes the reservation back; the reserved quantity becomes
// sellable again.
//
// THIS IS THE SAGA COMPENSATION and it is IDEMPOTENT: an already released
// reservation returns no error and the stock is not touched a SECOND TIME. A
// compensation step has to be safe to run again — when a workflow is retried
// or triggered twice, the second call must not blow up the flow.
//
// An unknown ID returns errors.NotFound: idempotency does not mean "silently
// swallow everything"; a REAL reservation released twice and an ID that never
// existed are different situations, and the second is a mistake on the
// caller's side. The reservation record is never deleted (only its status
// changes), so the first situation can always be told apart.
//
// A confirmed reservation cannot be released (errors.Conflict): the stock has
// already been physically deducted, and taking it back would create stock.
//
// The locks are taken reservation -> item -> level (see "Lock order" on
// [Store]); the item lock comes BEFORE the level lock.
func (s *Service) ReleaseReservation(ctx context.Context, reservationID string) error {
	if err := requireText("reservation_id", reservationID); err != nil {
		return err
	}

	return s.store.WithTx(ctx, func(ctx context.Context) error {
		reservation, err := s.store.LockReservation(ctx, reservationID)
		if err != nil {
			return err
		}

		switch reservation.Status {
		case models.ReservationReleased:
			s.log.DebugContext(ctx, "reservation already released, nothing done",
				"reservation_id", reservationID)
			return nil
		case models.ReservationConfirmed:
			return errors.Conflict(CodeReservationNotActive,
				"a confirmed reservation cannot be released: %s", reservationID)
		case models.ReservationActive:
			// Handled below.
		default:
			return errors.Internal(CodeInconsistentState,
				"unknown reservation status %q (%s)", reservation.Status, reservationID)
		}

		if err := s.store.LockInventoryItemShared(ctx, reservation.InventoryItemID); err != nil {
			return err
		}

		level, err := s.store.LockInventoryLevel(ctx, reservation.InventoryItemID, reservation.LocationID)
		if err != nil {
			return err
		}
		newReserved := level.ReservedQuantity - reservation.Quantity
		if newReserved < 0 {
			return errors.Internal(CodeInconsistentState,
				"the reserved quantity (%d) is smaller than the reservation's quantity (%d) (%s)",
				level.ReservedQuantity, reservation.Quantity, reservationID)
		}

		// No movement, for the mirror of Reserve's reason: the promise is given
		// back and the goods never left.
		if _, err := s.writeQuantities(ctx, level, level.StockedQuantity, newReserved, "", "", "", ""); err != nil {
			return err
		}
		return s.store.SetReservationStatus(ctx, reservationID, models.ReservationReleased)
	})
}

// ConfirmReservation turns the reservation into deducted stock: the reserved
// quantity comes off both the physical and the reserved quantity, and the
// sellable quantity DOES NOT CHANGE.
//
// Like [Service.ReleaseReservation] it is idempotent: an already confirmed
// reservation returns no error. A released reservation cannot be confirmed
// (errors.Conflict); the stock has been given back and no promise is left to
// deduct.
//
// The locks are taken reservation -> item -> level (see "Lock order" on
// [Store]); the item lock comes BEFORE the level lock.
func (s *Service) ConfirmReservation(ctx context.Context, reservationID, orderID string) error {
	if err := requireText("reservation_id", reservationID); err != nil {
		return err
	}

	return s.store.WithTx(ctx, func(ctx context.Context) error {
		reservation, err := s.store.LockReservation(ctx, reservationID)
		if err != nil {
			return err
		}

		switch reservation.Status {
		case models.ReservationConfirmed:
			s.log.DebugContext(ctx, "reservation already confirmed, nothing done",
				"reservation_id", reservationID)
			return nil
		case models.ReservationReleased:
			return errors.Conflict(CodeReservationNotActive,
				"a released reservation cannot be confirmed: %s", reservationID)
		case models.ReservationActive:
			// Handled below.
		default:
			return errors.Internal(CodeInconsistentState,
				"unknown reservation status %q (%s)", reservation.Status, reservationID)
		}

		if err := s.store.LockInventoryItemShared(ctx, reservation.InventoryItemID); err != nil {
			return err
		}

		level, err := s.store.LockInventoryLevel(ctx, reservation.InventoryItemID, reservation.LocationID)
		if err != nil {
			return err
		}
		newStocked := level.StockedQuantity - reservation.Quantity
		newReserved := level.ReservedQuantity - reservation.Quantity
		if newStocked < 0 || newReserved < 0 {
			return errors.Internal(CodeInconsistentState,
				"the confirmation would take the stock negative: physical %d, reserved %d, reservation %d (%s)",
				level.StockedQuantity, level.ReservedQuantity, reservation.Quantity, reservationID)
		}

		// This is the ONE reservation transition that moves goods, so it is the
		// one that leaves a movement: the units are gone from the warehouse and
		// the row names the promise they went out against (ADR 0068).
		//
		// The PROMISE ITSELF says what the movement's reason is: stock set aside
		// for a sale is deducted as a sale, stock set aside to settle a claim as
		// a replacement. The confirmation has no choice to make here, because
		// the choice was made when the reservation was written.
		//
		// The ORDER is written onto the movement, and it is the only thing on this
		// row that points outside the warehouse.
		//
		// It exists so that units written off later can go back to the shelf they
		// left. The reservation knew the location and is keyed to the CART's line
		// item, which an order does not carry — so without this the way back is a
		// chain through three modules, and with it it is one read.
		//
		// It may be empty: a replacement's goods leave against a claim rather than
		// an order, and a caller with no order to name says so by naming none.
		if _, err := s.writeQuantities(ctx, level, newStocked, newReserved,
			reservation.Purpose.MovementReason(), reservationID, orderID, ""); err != nil {
			return err
		}
		return s.store.SetReservationStatus(ctx, reservationID, models.ReservationConfirmed)
	})
}

// GetReservation returns the reservation by its ID; errors.NotFound when there
// is none.
func (s *Service) GetReservation(ctx context.Context, reservationID string) (models.Reservation, error) {
	if err := requireText("reservation_id", reservationID); err != nil {
		return models.Reservation{}, err
	}
	return s.store.GetReservation(ctx, reservationID)
}

// requireOpenLocation takes the SHARED lock on the location and refuses a
// closed one. It is the first step of every flow that writes stock.
//
// Both halves carry weight. The LOCK is what a close meets: the close holds the
// same row exclusively, so a stocking transaction either commits before the
// close counts what the location holds or waits and then finds it closed. The
// REFUSAL is what keeps a closed location empty after the close — without it,
// "closed" would be true for one moment, and the availability sums, which read
// inventory_levels with no join to stock_locations, would go on offering units
// out of a warehouse the operator has retired (ADR 0055).
//
// It also gives the location's absence a NAME: before this step, stocking a
// location that does not exist reached the database and came back as a foreign
// key violation.
func (s *Service) requireOpenLocation(ctx context.Context, locationID string) error {
	location, err := s.store.LockStockLocationShared(ctx, locationID)
	if err != nil {
		return err
	}
	if location.Closed() {
		return errors.Conflict(CodeLocationClosed,
			"the location is closed and takes no stock (%s)", locationID)
	}
	return nil
}

// requireIDs validates the item and location IDs together.
func requireIDs(itemID, locationID string) error {
	if err := requireText("inventory_item_id", itemID); err != nil {
		return err
	}
	return requireText("location_id", locationID)
}

// addQuantity adds delta to current and catches an upward overflow.
//
// current is NEVER negative (the database constraint guarantees it), so only
// an upward overflow is possible: downward, the smallest result is
// 0 + MinInt64, and that does not overflow. Left silent, an overflow would wrap
// the result to a negative number and could slip past every quantity check and
// the CHECK constraints.
func addQuantity(current, delta int64) (int64, error) {
	if delta > 0 && current > math.MaxInt64-delta {
		return 0, errors.Invalid(CodeInvalidInput,
			"quantity overflow: %d + %d exceeds the int64 limit", current, delta)
	}
	return current + delta, nil
}

// ReturnCanceledInventory brings a LINE's returned units up to a target.
//
// # Why it is neither an adjustment nor a restock
//
// The arithmetic is a restock's and the FACT is not. Nothing arrived: the units
// never left the building, and what changed is that the promise to send them was
// withdrawn. An operator reading the ledger to explain a month's stock is asking
// which of the three it was, and the ledger answers by reason (ADR 0068).
//
// # It takes a TARGET, not a quantity, and that is the whole correction
//
// It used to take a quantity and add it, with the cancellation's id held unique
// so a redelivered event wrote nothing. That was idempotent per ACT and the
// invariant is per LINE — and two different acts put a line's units back: a
// write-off, and the cancellation of the parcel that had been holding the rest.
// Each computed a delta from a state the other had not yet changed, so running
// them in the order nothing forbids credited the shelf with EIGHT units for a
// cancellation of five (D82).
//
// So the caller states where the total should BE, this reads where it is, and
// the difference is what moves. Whichever act arrives first does the work and the
// other finds the target already met; a redelivery finds it met too. The read and
// the write are in ONE transaction under the level's lock, so two acts arriving
// together cannot both see the old sum.
//
// A call that finds the target already met returns
// [models.ErrMovementAlreadyRecorded], which is not a failure: it says the units
// are already back.
func (s *Service) ReturnCanceledInventory(
	ctx context.Context, itemID, locationID, lineItemID string, target int64, reference string,
) (models.InventoryLevel, error) {
	if target <= 0 {
		return models.InventoryLevel{}, errors.Invalid(CodeInvalidInput,
			"the target on the shelf has to be positive: %d (item %s)", target, itemID)
	}
	if err := requireText("reference", reference); err != nil {
		return models.InventoryLevel{}, err
	}
	if err := requireText("line_item_id", lineItemID); err != nil {
		return models.InventoryLevel{}, err
	}
	if err := requireIDs(itemID, locationID); err != nil {
		return models.InventoryLevel{}, err
	}

	var out models.InventoryLevel
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		if err := s.requireOpenLocation(ctx, locationID); err != nil {
			return err
		}
		if err := s.store.LockInventoryItemShared(ctx, itemID); err != nil {
			return err
		}

		level, err := s.store.LockInventoryLevel(ctx, itemID, locationID)
		if err != nil {
			return err
		}

		// Read AFTER the locks. Before them it would be the stale sum two
		// concurrent acts would both act on.
		returned, err := s.store.ReturnedForLine(ctx, itemID, lineItemID)
		if err != nil {
			return err
		}

		quantity := target - returned
		if quantity <= 0 {
			return models.ErrMovementAlreadyRecorded
		}

		newStocked, err := addQuantity(level.StockedQuantity, quantity)
		if err != nil {
			return err
		}

		updated, err := s.writeQuantities(ctx, level, newStocked, level.ReservedQuantity,
			models.MovementCancellation, "", reference, lineItemID)
		if err != nil {
			return err
		}
		out = updated

		return nil
	})
	if err != nil {
		return models.InventoryLevel{}, err
	}

	return out, nil
}

// SaleLocations answers where an order's units were taken from, per inventory
// item.
//
// The map is empty rather than an error for an order whose stock was never
// deducted — a saga that failed before its last step leaves reservations and no
// sale — because "nothing left from anywhere" is a true answer and a caller that
// had to tell it apart from a fault would have nothing to do with the difference.
func (s *Service) SaleLocations(ctx context.Context, orderID string) (map[string]string, error) {
	if err := requireText("order_id", orderID); err != nil {
		return nil, err
	}

	return s.store.SaleLocations(ctx, orderID)
}
