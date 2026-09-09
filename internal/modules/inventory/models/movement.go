package models

// This file is English because ADR 0012 makes language a property of the FILE
// and every new file is English; models.go next to it is already English too.

import (
	"time"

	"github.com/bdrtr/gobit/internal/core/page"
)

// MovementReason is why the physical count changed.
//
// # It is also the answer to "who"
//
// The ledger carries no actor column, and this is the field that stands in its
// place. Two of the reasons come from an admin request, which `audit_log`
// already records together with the caller; the rest come from a flow with no
// person behind it. So a reader who wants a name knows from the reason
// whether there is one to look for and where — which is more than a nullable
// actor column could say, because half its rows would hold an identifier nobody
// issued (ADR 0056) and the other half nothing at all.
//
// The set is CLOSED, in this type and in the schema's CHECK. An open string
// would let a caller write its own vocabulary into the one column an operator
// reads the table by.
type MovementReason string

// The reasons a physical count changes. Each names both what happened to the
// goods and, by that, the surface it came from.
const (
	// MovementStockCount is an operator writing the count they measured, which
	// the service does in SetInventoryLevel. The delta is the difference their
	// number made, in either direction.
	MovementStockCount MovementReason = "stock_count"
	// MovementAdjustment is an operator moving the count by a delta they chose,
	// which is AdjustInventory reached from the admin API: breakage, a
	// correction, a transfer recorded by hand.
	MovementAdjustment MovementReason = "adjustment"
	// MovementSale is a confirmed reservation — ConfirmReservation, called by
	// the checkout saga: the promised units left the count for good. It is the
	// one reason that names a reservation, and its delta is always negative.
	MovementSale MovementReason = "sale"
	// MovementReturnRestock is goods arriving back at a location, which the
	// cross-module surface calls Restock. It is an addition rather than the
	// undoing of a hold — a confirmed reservation cannot be released — and its
	// delta is always positive.
	MovementReturnRestock MovementReason = "return_restock"
	// MovementReplacement is a confirmed reservation whose goods were sent to
	// settle a claim: the units left the count and nobody paid for them. It
	// names a reservation and deducts, exactly like a sale, and it is a reason
	// of its own because an operator reading the ledger to explain a month's
	// stock is asking which of the two it was.
	MovementReplacement MovementReason = "replacement"
)

// Valid reports whether the reason is a defined value.
func (r MovementReason) Valid() bool {
	switch r {
	case MovementStockCount, MovementAdjustment, MovementSale, MovementReturnRestock,
		MovementReplacement:
		return true
	default:
		return false
	}
}

// String returns the text representation of the reason.
func (r MovementReason) String() string { return string(r) }

// LeavesAgainstAPromise reports whether units of this reason left the warehouse
// against a reservation, which is to say whether the movement NAMES one.
//
// The schema states the same thing as an equivalence, and this is the Go side
// of it. It is a method rather than a comparison written twice: the set grew
// once already — a replacement leaves against a promise exactly as a sale does
// — and the next reason that joins it would otherwise have to be remembered in
// two places.
func (r MovementReason) LeavesAgainstAPromise() bool {
	return r == MovementSale || r == MovementReplacement
}

// FromAdminRequest reports whether a movement of this reason was produced by an
// admin request, which is to say whether `audit_log` holds a caller for it.
//
// It exists so the mapping is written ONCE. The reader publishes it as a field,
// and an operator reading a movement should not have to remember which of four
// reasons has a person behind it.
func (r MovementReason) FromAdminRequest() bool {
	return r == MovementStockCount || r == MovementAdjustment
}

// Movement is one change to the physical count of an item at a location.
//
// # What it explains, and what it does not
//
// It explains [InventoryLevel.StockedQuantity] and nothing else. A RESERVATION
// IS NOT A MOVEMENT: reserving and releasing change what is AVAILABLE, not what
// is present, and [Reservation] is already that record with its own lifecycle.
// Only the confirm produces a Movement, because only the confirm takes units
// out of the physical count.
//
// # The ledger does not begin where the stock did
//
// It begins where the table did. No opening balance was written for the stock
// that existed before the ledger, so the deltas DO NOT SUM to
// [InventoryLevel.StockedQuantity] — [Movement.StockedAfter] is what makes that
// harmless, since the oldest movement of an item names the balance the ledger
// inherited as StockedAfter minus Delta.
type Movement struct {
	// ID is the "invmov_" prefixed, time-sortable id.
	ID string
	// InventoryItemID and LocationID are the item and the place whose count
	// changed.
	InventoryItemID string
	LocationID      string
	// ReservationID is the promise the units left against. It is set on a
	// [MovementSale] and a [MovementReplacement] and empty on every other
	// reason, which the schema states as an equivalence rather than as an
	// option: units that leave against a promise NAME the promise.
	ReservationID string
	// Reason is why the count changed.
	Reason MovementReason
	// Delta is the signed change; it is never zero.
	Delta int64
	// StockedAfter is the physical count the change produced.
	//
	// It is stored rather than derived, and it is what makes a drift between the
	// ledger and the column visible from ONE row: the newest movement of a
	// (item, location) pair carries what stocked_quantity should read.
	StockedAfter int64
	// CreatedAt is when it happened, from the DATABASE clock — the same moment
	// as the updated_at of the level it explains, because both are that
	// transaction's start (ADR 0053).
	CreatedAt time.Time
}

// MovementFilter narrows and positions a movement listing.
//
// The type is in models for the reason [InventoryItemFilter] is: the service's
// own store interface can carry the criteria without importing the repository.
type MovementFilter struct {
	// InventoryItemID is the item whose movements are wanted; it is required,
	// and it is what the listing's index leads on.
	InventoryItemID string
	// LocationID narrows the listing to one place. Empty means every place.
	//
	// The filter has NO index of its own on purpose: it runs inside the rows the
	// item has already narrowed to, and an item has as many levels as the shop
	// has warehouses.
	LocationID string
	// After is the keyset position the page starts BELOW, in the listing's own
	// order (newest first). The zero value is the first page.
	//
	// Both halves of it carry weight: created_at alone is not unique — two
	// movements committed in one transaction share it exactly — which is why
	// the index carries the id as its last column.
	After page.Cursor
	// Limit is how many rows to return.
	Limit int64
}
