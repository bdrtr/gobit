package service

import (
	"context"
	"errors"

	"github.com/bdrtr/gobit/internal/modules/inventory/models"
)

// This file is the CROSS-MODULE surface of the inventory module (ADR 0001,
// ADR 0006).
//
// The sagas under internal/workflows have to set stock aside and release it
// again, but neither can those packages import this module nor can this module
// import them. The solution is the same as in the region/cart/payment/order
// modules: publishing a surface that uses only PRIMITIVE and stdlib types. The
// consumer defines its own narrow interface, this type satisfies it STRUCTURALLY
// and it is resolved from the container by name.
//
// The surface is DELIBERATELY narrow and was picked according to what the flows
// need: set the stock aside ([Interop.Reserve] and, for goods being sent to
// settle a claim, [Interop.ReserveForReplacement]), release what was set aside
// ([Interop.ReleaseReservation]), turn it into deducted stock
// ([Interop.ConfirmReservation]), ask for the sellable total
// ([Interop.AvailableQuantity]) and list the locations that have enough stock
// ([Interop.LocationsWithStock]). Every method added here is a CONTRACT: the
// consumer writes it in its own package with exactly the same signature and the
// match can be checked not by the compiler but only by tests.
//
// LocationsWithStock DOES NOT CARRY the "which warehouse should we ship from"
// question onto the surface, that one is a shipping decision and belongs to
// fulfillment. This surface reports only the stock fact — at which locations
// there is enough quantity; the module that will make the decision takes its
// candidates from here. Merging the two into a single method would make the
// stock query depend on shipping policy.

// Interop turns the stock service into the primitive cross-module surface.
//
// It makes no decisions: it only translates the signature. Concurrency, lock
// order and insufficient-stock rules stay on [Service]; adding a rule here would
// mean the same rule drifting apart in two places.
//
// It is registered in the container under the name "inventory.interop".
type Interop struct {
	svc *Service
}

// NewInterop sets up the cross-module surface for the given service.
func NewInterop(svc *Service) *Interop { return &Interop{svc: svc} }

// Reserve sets the stock aside and returns the reservation id.
//
// If there is not enough stock it returns errors.Conflict; the saga reads that
// as "the order cannot be placed". Setting aside is serialized at the DATABASE
// level, so two concurrent calls CANNOT get the same last quantity.
func (i *Interop) Reserve(
	ctx context.Context,
	inventoryItemID, locationID string,
	quantity int64,
	lineItemID string,
) (reservationID string, err error) {
	res, err := i.svc.Reserve(ctx, ReserveInput{
		InventoryItemID: inventoryItemID,
		LocationID:      locationID,
		Quantity:        quantity,
		LineItemID:      lineItemID,
	})
	if err != nil {
		return "", err
	}
	return res.ID, nil
}

// ReserveForReplacement sets stock aside for goods that will be SENT to settle
// a claim, and returns the reservation id.
//
// # Why it is not Reserve with an extra argument
//
// The two calls differ in one word and that word is the point: the confirm
// writes a movement whose reason comes from the promise, so what is chosen here
// is what the ledger will say a month from now — units that left as a sale, or
// units nobody paid for. Widening [Interop.Reserve] would put that choice in a
// positional parameter next to three identifiers, where the checkout saga would
// have to pass a value it never varies.
//
// Everything else is [Interop.Reserve]'s: insufficient stock is a Conflict the
// caller reads as "this cannot be sent from here", and the set-aside is
// serialized at the database level.
func (i *Interop) ReserveForReplacement(
	ctx context.Context,
	inventoryItemID, locationID string,
	quantity int64,
	orderLineItemID string,
) (reservationID string, err error) {
	res, err := i.svc.Reserve(ctx, ReserveInput{
		InventoryItemID: inventoryItemID,
		LocationID:      locationID,
		Quantity:        quantity,
		LineItemID:      orderLineItemID,
		Purpose:         models.PurposeReplacement,
	})
	if err != nil {
		return "", err
	}

	return res.ID, nil
}

// ReleaseReservation releases the stock that was set aside.
//
// IT IS THE SAGA COMPENSATION and IT IS IDEMPOTENT: for an already released
// reservation the second call DOES NOT return an error. A compensation chain may
// rerun a step; the second call blowing up would mean the compensation stays
// half done.
func (i *Interop) ReleaseReservation(ctx context.Context, reservationID string) error {
	return i.svc.ReleaseReservation(ctx, reservationID)
}

// Restock puts stock BACK at a location.
//
// # Why it is not ReleaseReservation
//
// Releasing gives back stock that was only SET ASIDE. This is for stock that
// was already deducted: the checkout confirmed the reservation, so the units
// left the warehouse's count for good, and goods coming back are an addition
// rather than the undoing of a hold. The inventory module says as much —
// a confirmed reservation cannot be released and returns errors.Conflict.
//
// # Why it takes a LOCATION
//
// Stock lives at a location, and the returning goods arrive at one the caller
// names. It cannot be derived from the order: the order carries no location,
// and the warehouse that shipped is not necessarily the one the customer
// returned to.
//
// # It is NOT idempotent, and it must not be
//
// Two calls add the stock twice, because two calls mean two physical arrivals.
// The caller is responsible for calling it once per receipt; the return record
// is what makes that possible, since a return can only be received once.
//
// # Why it is not AdjustInventory with a positive delta
//
// It was, until the movement ledger arrived (ADR 0068). The arithmetic is the
// same and the FACT is not: a warehouse correction and goods a customer sent
// back are two entries an operator reads differently, and a positive delta says
// nothing about which one it was. The service therefore has an entry point per
// reason, and this surface picks the one that matches what it knows.
func (i *Interop) Restock(
	ctx context.Context,
	inventoryItemID, locationID string,
	quantity int64,
) error {
	_, err := i.svc.RestockInventory(ctx, inventoryItemID, locationID, quantity)

	return err
}

// ConfirmReservation turns the reservation into deducted stock.
//
// It is called once the order is final; from this point on the stock is not
// released again, a return is a separate flow.
//
// # Why it names the order
//
// The movement it writes carries the order, and that is the ONLY thing on the row
// pointing outside the warehouse. It is there so that units written off later can
// go back to the shelf they left: the reservation knew the location and is keyed
// to the CART's line item, which an order does not carry, so without this the way
// back is a chain through three modules (ADR 0134).
//
// An empty order is allowed and means "no order to name" — goods leaving against
// a claim rather than a sale. It is not a default to reach for: a caller that has
// an order and passes none has silently taken the way back away.
func (i *Interop) ConfirmReservation(ctx context.Context, reservationID, orderID string) error {
	return i.svc.ConfirmReservation(ctx, reservationID, orderID)
}

// ReturnCanceled brings a LINE's returned units up to a target.
//
// # Why it is not Restock
//
// [Interop.Restock] is goods a customer sent back, and its own record says two
// calls mean two physical arrivals. Nothing arrives here: the units never left,
// and what changed is that the promise to send them was withdrawn. The ledger has
// an entry point per reason because an operator reads the two differently.
//
// # Why it takes a TARGET and not a quantity
//
// Two different acts put a line's written-off units back — the write-off itself,
// and the cancellation of a parcel that had been holding the rest — and each of
// them used to compute a delta from a state the other had not yet changed. Run in
// the order nothing forbids, the pair credited the shelf with eight units for a
// cancellation of five (D82, ADR 0142).
//
// So the caller states where the total should BE. The module reads where it is,
// under the level's lock, and moves the difference. Order stops mattering and a
// redelivered event finds the target already met.
//
// # Why the second return value is a BOOL
//
// A target already met is not a failure and the caller may want to say so in a
// log line. The service reports it with a named error, and a named error cannot
// cross this boundary: a consumer that cannot import this module cannot match a
// sentinel it cannot name. A bool can be repeated verbatim in the consumer's own
// interface, which is the same reason every signature here uses primitive types.
//
// True means the line was ALREADY at or above the target and this call wrote
// nothing.
func (i *Interop) ReturnCanceled(
	ctx context.Context,
	inventoryItemID, locationID, lineItemID string,
	target int64,
	reference string,
) (alreadyBack bool, err error) {
	_, err = i.svc.ReturnCanceledInventory(
		ctx, inventoryItemID, locationID, lineItemID, target, reference)
	if errors.Is(err, models.ErrMovementAlreadyRecorded) {
		return true, nil
	}

	return false, err
}

// SaleLocations answers where an order's units were deducted from, per inventory
// item.
//
// An empty map is a true answer: an order whose checkout never reached its last
// step has reservations and no sale, so nothing left from anywhere.
func (i *Interop) SaleLocations(ctx context.Context, orderID string) (map[string]string, error) {
	return i.svc.SaleLocations(ctx, orderID)
}

// AvailableQuantity returns the item's available quantity across all locations.
func (i *Interop) AvailableQuantity(ctx context.Context, inventoryItemID string) (int64, error) {
	return i.svc.AvailableQuantity(ctx, inventoryItemID)
}

// LocationsWithStock returns, in ascending order, the ids of the locations from
// which at least quantity units of the item can be set aside.
//
// The returned order is the order of a FACT, IT IS NOT a preference order:
// fulfillment lines the candidates up in preference order and the cart flow uses
// the first warehouse in that line that works. If no location is enough it
// returns an empty slice, not an error; the saga turns that into a Conflict in
// its own context. For the detailed rationale see [Service.LocationsWithStock].
func (i *Interop) LocationsWithStock(
	ctx context.Context,
	inventoryItemID string,
	quantity int64,
) ([]string, error) {
	return i.svc.LocationsWithStock(ctx, inventoryItemID, quantity)
}
