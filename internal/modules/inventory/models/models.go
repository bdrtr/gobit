// Package models holds the domain models of the inventory module.
//
// The types here are independent of the database driver: pgtype and sqlc
// generated types DO NOT LEAK in here. The translation is done in the repository
// layer; the service, the API and the tests see only these types.
//
// Quantities are whole numbers everywhere (BIGINT -> int64). There is no money
// in this module; what the "minor unit" rule of the money-carrying modules
// amounts to here is that a quantity is never held as a fraction anywhere.
package models

import "time"

// StockLocation is the place where stock physically sits (a warehouse, a store).
type StockLocation struct {
	// ID is the "sloc_" prefixed, time-sortable id.
	ID string
	// Name is the display name of the location.
	Name string
	// Address1, Address2, City, Province and PostalCode are location details;
	// all of them are optional (an empty string means "no value").
	Address1   string
	Address2   string
	City       string
	Province   string
	PostalCode string
	// CountryCode is the ISO 3166-1 alpha-2 country code (e.g. "TR").
	CountryCode string
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// InventoryItem is the item whose stock is tracked.
//
// The information that the item belongs to a product variant IS NOT HELD IN THIS
// MODULE; the tie is established over the "product_variant_inventory" link
// (Principle 2.2). The inventory module neither imports the product module nor
// references its table.
type InventoryItem struct {
	// ID is the "invitem_" prefixed id.
	ID string
	// SKU is the stock keeping code; it is unique among the living items.
	SKU string
	// Title and Description are optional descriptive fields.
	Title       string
	Description string
	// RequiresShipping reports whether the item has to be shipped physically;
	// it is false for digital products.
	RequiresShipping bool
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// InventoryLevel is the stock situation of one item at one location.
//
// The sellable quantity IS NOT STORED, it is derived with
// [InventoryLevel.Available].
type InventoryLevel struct {
	// ID is the "invlevel_" prefixed id.
	ID string
	// InventoryItemID is the id of the item the level belongs to.
	InventoryItemID string
	// LocationID is the id of the location the level belongs to.
	LocationID string
	// StockedQuantity is the quantity physically present at the location.
	StockedQuantity int64
	// ReservedQuantity is the part of the physical stock that is reserved, that
	// is to say promised to another sale.
	ReservedQuantity int64
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Available returns the sellable quantity: stocked - reserved.
//
// The value is derived, not stored. Were it stored, the two columns could drift
// apart from each other and the stock would silently look wrong.
func (l InventoryLevel) Available() int64 {
	return l.StockedQuantity - l.ReservedQuantity
}

// ReservationStatus is the state of a reservation in its lifecycle.
type ReservationStatus string

// Reservation states. Transitions: active -> released | confirmed.
// A finished reservation does not become active again.
const (
	// ReservationActive reports that the stock is set aside and has not finished yet.
	ReservationActive ReservationStatus = "active"
	// ReservationReleased reports that the reservation was taken back; the set
	// aside quantity has become sellable again. The saga compensation moves it
	// into this state.
	ReservationReleased ReservationStatus = "released"
	// ReservationConfirmed reports that the reservation was deducted from the
	// physical stock; this is the quantity that was shipped.
	ReservationConfirmed ReservationStatus = "confirmed"
)

// Valid reports whether the state is a defined value.
func (s ReservationStatus) Valid() bool {
	switch s {
	case ReservationActive, ReservationReleased, ReservationConfirmed:
		return true
	default:
		return false
	}
}

// String returns the text representation of the state.
func (s ReservationStatus) String() string {
	return string(s)
}

// Reservation is a quantity set aside from the sellable stock.
//
// The complete_cart saga in Phase 6 first creates it with Reserve, the
// compensation step takes it back with ReleaseReservation if the flow fails, and
// deducts it from the physical stock with ConfirmReservation if it succeeds.
type Reservation struct {
	// ID is the "invres_" prefixed id.
	ID string
	// InventoryItemID is the item the reservation is set aside from.
	InventoryItemID string
	// LocationID is the location the stock is set aside at.
	LocationID string
	// Quantity is the quantity set aside; it is always positive.
	Quantity int64
	// LineItemID is the id of the cart/order line that asked for the reservation.
	// It belongs to the cart module and IS NOT A FOREIGN KEY HERE (Principle 2.2).
	// It may be empty: not every reservation has to be born from a line.
	LineItemID string
	// Description is an optional free-form explanation.
	Description string
	// Status is the state of the reservation.
	Status ReservationStatus
	// CreatedAt and UpdatedAt are UTC.
	CreatedAt time.Time
	UpdatedAt time.Time
}
