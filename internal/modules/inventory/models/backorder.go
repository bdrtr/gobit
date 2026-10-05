package models

import "time"

// BackorderStatus is where a claim stands.
type BackorderStatus string

// The three states of a claim. A claim starts waiting and ends filled or
// withdrawn; neither end is left.
const (
	// BackorderWaiting is a claim still owed units.
	BackorderWaiting BackorderStatus = "waiting"
	// BackorderFilled is a claim whose units were deducted for its order, as a
	// reservation confirmed in the write that made them sellable.
	BackorderFilled BackorderStatus = "filled"
	// BackorderWithdrawn is a claim the order will take none of: every unit was
	// written off before any arrived.
	BackorderWithdrawn BackorderStatus = "withdrawn"
)

// Valid reports whether the status is a defined value.
func (s BackorderStatus) Valid() bool {
	switch s {
	case BackorderWaiting, BackorderFilled, BackorderWithdrawn:
		return true
	default:
		return false
	}
}

// String returns the text representation of the status.
func (s BackorderStatus) String() string { return string(s) }

// Backorder is a claim: an order line the checkout let through without stock is
// owed Quantity units of the item, fillable at LocationIDs (ADR 0392).
type Backorder struct {
	// ID is the "invbo_" prefixed, time-sortable id.
	ID string
	// InventoryItemID is the item the line is owed.
	InventoryItemID string
	// OrderID and OrderLineItemID are the order module's; they are not foreign
	// keys (Principle 2.2).
	OrderID         string
	OrderLineItemID string
	// Quantity is the units the line was let through without; a bundle's part
	// is the line's units times the part's.
	Quantity int64
	// WithdrawnQuantity is how many of them a write-off took back before they
	// arrived; it never falls.
	WithdrawnQuantity int64
	// LocationIDs are the warehouses the claim may be filled at, in the order
	// fulfillment ranked them when the order was placed.
	LocationIDs []string
	// Status is where the claim stands.
	Status BackorderStatus
	// ReservationID is the reservation the fill confirmed, set exactly when the
	// claim is filled.
	ReservationID string
	// FilledLocationID is where the fill deducted, set with ReservationID: the
	// shelf a write-off of this line goes back to.
	FilledLocationID string
	// Seq is the queue position.
	Seq       int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Owed is how many units the claim still waits for.
func (b Backorder) Owed() int64 { return b.Quantity - b.WithdrawnQuantity }

// Undeducted is how many of the claim's units this module never took off a
// shelf for the line: the withdrawn ones once it is filled, every one before.
func (b Backorder) Undeducted() int64 {
	if b.Status == BackorderFilled {
		return b.WithdrawnQuantity
	}

	return b.Quantity
}

// BackorderFilter narrows and pages an item's claims.
type BackorderFilter struct {
	InventoryItemID string
	// Status is one status, or empty for every one.
	Status BackorderStatus
	Limit  int64
	Offset int64
}
