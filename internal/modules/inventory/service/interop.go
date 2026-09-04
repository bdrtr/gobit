package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
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
// need: set the stock aside ([Interop.Reserve]), release what was set aside
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
func (i *Interop) Restock(
	ctx context.Context,
	inventoryItemID, locationID string,
	quantity int64,
) error {
	if quantity <= 0 {
		return errors.Invalid(CodeInvalidInput,
			"the restocked quantity has to be positive: %d (item %s)", quantity, inventoryItemID)
	}

	_, err := i.svc.AdjustInventory(ctx, inventoryItemID, locationID, quantity)

	return err
}

// ConfirmReservation turns the reservation into deducted stock.
//
// It is called once the order is final; from this point on the stock is not
// released again, a return is a separate flow.
func (i *Interop) ConfirmReservation(ctx context.Context, reservationID string) error {
	return i.svc.ConfirmReservation(ctx, reservationID)
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
