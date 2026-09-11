// Package ordercancel puts back the stock of units that were written off.
//
// # What was wrong
//
// The checkout's last step confirms the reservations, which DEDUCTS the stock. So
// an order that exists has already had its units taken off the sellable figure,
// and a line written off afterwards is a unit nobody will ever send and nobody
// counts as stock either. Nothing put it back — neither the whole-order
// cancellation nor the partial one, and the order module cannot: the units live in
// another module (Principle 2.1/2.4, ADR 0006). Gap D70.
//
// # Why it is a SUBSCRIBER and not an endpoint
//
// The record already has two published admin endpoints (ADR 0113). Moving them
// here would break a promise made to integrators (ADR 0026) for a reason that has
// nothing to do with them, and a second endpoint beside them would leave a shop
// with two ways to cancel, one of which quietly loses stock.
//
// So the order module publishes and this listens. It is the first flow in this
// repository to subscribe — modules did it before (the order module on the payment
// events, the notification module on order.placed) and a flow had not needed to,
// because until now no consequence of one module's write reached two others.
//
// # Three modules, and none of them could do this alone
//
// The order knows what was written off. The fulfillment module knows how many of
// that line are in a parcel and therefore gone. The inventory module knows which
// shelf the units left and holds the ledger they go back into. Deciding across
// them is this layer's job.
package ordercancel

import (
	"context"
	"log/slog"
	"strconv"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
)

// Service names in the container (ADR 0006). Nothing here is known at compile
// time; the concrete values are resolved by name.
const (
	// ServiceInventory is the inventory module's cross-module surface.
	ServiceInventory = "inventory.interop"
	// ServiceFulfillment is the fulfillment module's cross-module surface.
	ServiceFulfillment = "fulfillment.interop"
	// ServiceLink is the core's Module Links service.
	ServiceLink = "core.link"
	// ServiceEventBus is the bus this flow listens on.
	ServiceEventBus = "core.eventbus"
)

// topicLineCanceled is the event this flow listens to.
//
// The name is REPEATED as a literal rather than imported: this flow and the order
// module do not know each other's types, and reaching for a constant across that
// line would tie them together at compile time. It is the same repetition the
// order module makes for the payment topics, at the same price — a rename on one
// side is caught by nothing but a test that runs both.
const topicLineCanceled = "order.line_canceled"

// linkVariantInventory binds a product variant to the inventory item that tracks
// its stock. The name is repeated here for the topic's reason.
const linkVariantInventory = "product_variant_inventory"

// The payload keys this flow reads.
const (
	fieldCancellationID   = "cancellation_id"
	fieldOrderID          = "order_id"
	fieldOrderLineItemID  = "order_line_item_id"
	fieldVariantID        = "variant_id"
	fieldCanceledQuantity = "canceled_quantity"
	fieldCanceledBefore   = "canceled_before"
	fieldBoughtQuantity   = "bought_quantity"
)

// CodeEventUnusable reports an event this flow cannot act on.
const CodeEventUnusable = "order_cancel_event_unusable"

// CodeNotReady reports a flow that was not wired.
const CodeNotReady = "order_cancel_not_ready"

// Inventory is the slice of the inventory module this flow calls.
type Inventory interface {
	// SaleLocations answers where an order's units were deducted from, as
	// inventory item to location.
	SaleLocations(ctx context.Context, orderID string) (map[string]string, error)
	// ReturnCanceled puts units back. It is idempotent on the cancellation id,
	// and alreadyBack says the units were there before this call.
	ReturnCanceled(
		ctx context.Context,
		inventoryItemID, locationID string,
		quantity int64,
		cancellationID string,
	) (alreadyBack bool, err error)
}

// Fulfillment is the slice of the fulfillment module this flow calls.
type Fulfillment interface {
	// CommittedQuantities sums, per order line, the units a live parcel holds.
	CommittedQuantities(ctx context.Context, fulfillmentIDs []string) (map[string]int64, error)
}

// Links reads the Module Links this flow needs.
type Links interface {
	// ListMany returns the links of the given source ids in a SINGLE query.
	ListMany(ctx context.Context, name string, fromIDs []string) (map[string][]string, error)
}

// Subscriber is the narrow surface this flow needs to LISTEN.
type Subscriber interface {
	// Subscribe registers the handler for the named event.
	Subscribe(eventName string, h eventbus.Handler) error
}

// Workflow is the wired flow.
type Workflow struct {
	inventory   Inventory
	fulfillment Fulfillment
	links       Links
	log         *slog.Logger
}

// New builds the flow from already-resolved dependencies.
func New(
	inventory Inventory, fulfillment Fulfillment, links Links, log *slog.Logger,
) *Workflow {
	if log == nil {
		log = slog.Default()
	}

	return &Workflow{
		inventory: inventory, fulfillment: fulfillment, links: links, log: log,
	}
}

// HandleLineCanceled puts back the stock of units that were written off.
//
// # Why a missing piece is not an error
//
// The handler returns nil for everything that will never become actionable: a
// variant that tracks no stock, an order whose checkout never deducted anything, a
// cancellation whose units had all shipped. Returning an error would put the bus
// into a retry loop over an event that cannot succeed, and the retries would bury
// the failures that are real.
//
// What DOES return an error is a fault that may pass: the fulfillment module being
// unreachable, the inventory write failing. Those the bus should try again.
func (w *Workflow) HandleLineCanceled(ctx context.Context, e eventbus.Event) error {
	in, err := readCanceledEvent(e)
	if err != nil {
		return err
	}

	// The window of units that can come back: bought minus those a live parcel
	// holds. A parcel that was canceled never left, so its units are still here.
	committed, err := w.committedQuantity(ctx, in.orderID, in.lineItemID)
	if err != nil {
		return err
	}

	quantity := returnableUnits(in.bought, committed, in.before, in.quantity)
	if quantity == 0 {
		w.log.DebugContext(ctx, "a canceled line has no units to put back",
			"cancellation_id", in.cancellationID, "order_line_item_id", in.lineItemID,
			"bought", in.bought, "committed", committed,
			"canceled_before", in.before, "canceled_now", in.quantity)

		return nil
	}

	itemID, locationID, found, err := w.shelf(ctx, in.orderID, in.variantID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	alreadyBack, err := w.inventory.ReturnCanceled(
		ctx, itemID, locationID, quantity, in.cancellationID)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), CodeEventUnusable,
			"the stock of canceled line %s could not be put back", in.lineItemID)
	}
	if alreadyBack {
		// The second delivery of one event. The ledger refused to add the units
		// twice, which is the whole reason the cancellation's id is the movement's
		// reference — the bus delivers at least once and adding stock is deliberately
		// not idempotent anywhere else.
		w.log.DebugContext(ctx, "the canceled units were already put back",
			"cancellation_id", in.cancellationID)

		return nil
	}

	w.log.InfoContext(ctx, "the stock of a canceled line went back on the shelf",
		"cancellation_id", in.cancellationID, "order_line_item_id", in.lineItemID,
		"inventory_item_id", itemID, "location_id", locationID, "quantity", quantity)

	return nil
}

// returnableUnits is how many of THIS cancellation's units go back on the shelf.
//
// # The arithmetic, and why it is not simply the canceled quantity
//
// Stock was deducted for every unit bought. Units a live parcel holds have left.
// So the units that were deducted and will not leave are `bought - committed`, and
// that window is shared by every cancellation of the line: a second one must not
// put back what the first already did.
//
//	before = min(canceledBefore, window)
//	after  = min(canceledBefore + now, window)
//	return = after - before
//
// It is monotone and needs no record of what was returned before, because the
// total put back is always `min(canceledTotal, window)` however the cancellations
// were split. A line whose units have all shipped gives a window of zero and
// therefore returns nothing, which is the answer: those goods are with the
// customer and a write-off of them is a money act, not a stock one.
func returnableUnits(bought, committed, canceledBefore, now int64) int64 {
	window := bought - committed
	if window <= 0 {
		return 0
	}

	before := min(canceledBefore, window)
	after := min(canceledBefore+now, window)
	if after <= before {
		return 0
	}

	return after - before
}

// committedQuantity answers how many units of the line a live parcel holds.
//
// The parcels are found through the "order_fulfillment" LINK, which is the only
// binding between an order and its shipments that can be trusted: the shipment
// also carries a free-text reference the module never validates, and reading that
// as the order would be reading a convention (the fulfillment module says so).
func (w *Workflow) committedQuantity(ctx context.Context, orderID, lineItemID string) (int64, error) {
	if w.links == nil || w.fulfillment == nil {
		return 0, errors.Internal(CodeNotReady,
			"the cancellation flow is not wired, so a canceled line cannot be acted on")
	}

	byOrder, err := w.links.ListMany(ctx, "order_fulfillment", []string{orderID})
	if err != nil {
		return 0, errors.Wrap(err, errors.KindOf(err), CodeEventUnusable,
			"the parcels of order %s could not be read", orderID)
	}

	parcels := byOrder[orderID]
	if len(parcels) == 0 {
		return 0, nil
	}

	committed, err := w.fulfillment.CommittedQuantities(ctx, parcels)
	if err != nil {
		return 0, errors.Wrap(err, errors.KindOf(err), CodeEventUnusable,
			"the shipped units of order %s could not be read", orderID)
	}

	return committed[lineItemID], nil
}

// shelf answers which inventory item the variant tracks and which location its
// units left from.
//
// # Why the location comes from the LEDGER
//
// The units have to go back where they came from, and the reservation that knew
// the location is keyed to the CART's line item, which an order does not carry. So
// the sale movement is what remembers: the checkout writes the order onto it, and
// this reads it back (ADR 0134).
//
// An order with no sale movement is not an error. A checkout that failed before
// its last step leaves reservations and no sale, and its compensation released
// them — so there is nothing deducted and nothing to put back.
func (w *Workflow) shelf(
	ctx context.Context, orderID, variantID string,
) (itemID, locationID string, found bool, err error) {
	if w.links == nil || w.inventory == nil {
		return "", "", false, errors.Internal(CodeNotReady,
			"the cancellation flow is not wired, so a canceled line cannot be acted on")
	}

	// The variant reaches its inventory item through the "variant_inventory" LINK,
	// which is how the return flow does it one directory over: the binding is a
	// link rather than a column, so neither module names the other.
	linked, err := w.links.ListMany(ctx, linkVariantInventory, []string{variantID})
	if err != nil {
		return "", "", false, errors.Wrap(err, errors.KindOf(err), CodeEventUnusable,
			"the inventory item of variant %s could not be read", variantID)
	}

	tracked := len(linked[variantID]) > 0
	if tracked {
		itemID = linked[variantID][0]
	}
	if !tracked {
		// A variant that tracks no stock has none to put back. It is an ordinary
		// shape — a service, a digital good — rather than a fault.
		w.log.DebugContext(ctx, "a canceled line sells a variant that tracks no stock",
			"variant_id", variantID)

		return "", "", false, nil
	}

	locations, err := w.inventory.SaleLocations(ctx, orderID)
	if err != nil {
		return "", "", false, errors.Wrap(err, errors.KindOf(err), CodeEventUnusable,
			"the shelf the units of order %s left from could not be read", orderID)
	}

	locationID, deducted := locations[itemID]
	if !deducted {
		w.log.WarnContext(ctx,
			"a canceled line's stock cannot be put back: the ledger holds no sale for it, "+
				"so either the checkout never deducted it or the movement was written "+
				"without an order",
			"order_id", orderID, "variant_id", variantID, "inventory_item_id", itemID)

		return "", "", false, nil
	}

	return itemID, locationID, true, nil
}

// canceledEvent is the payload, read.
type canceledEvent struct {
	cancellationID string
	orderID        string
	lineItemID     string
	variantID      string
	quantity       int64
	before         int64
	bought         int64
}

// readCanceledEvent reads the payload and refuses one it cannot act on.
//
// Every count arrives as a STRING, which is the bus's own contract: JSON has one
// number type, so an int64 put in reaches a subscriber as a float64 on the Redis
// backend and as an int64 in memory. Parsing is what makes the two agree.
func readCanceledEvent(e eventbus.Event) (canceledEvent, error) {
	out := canceledEvent{
		cancellationID: text(e, fieldCancellationID),
		orderID:        text(e, fieldOrderID),
		lineItemID:     text(e, fieldOrderLineItemID),
		variantID:      text(e, fieldVariantID),
	}

	for name, value := range map[string]string{
		fieldCancellationID:  out.cancellationID,
		fieldOrderID:         out.orderID,
		fieldOrderLineItemID: out.lineItemID,
		fieldVariantID:       out.variantID,
	} {
		if value == "" {
			return canceledEvent{}, errors.Invalid(CodeEventUnusable,
				"the %q event carries no %s", e.Name, name)
		}
	}

	var err error
	if out.quantity, err = number(e, fieldCanceledQuantity); err != nil {
		return canceledEvent{}, err
	}
	if out.before, err = number(e, fieldCanceledBefore); err != nil {
		return canceledEvent{}, err
	}
	if out.bought, err = number(e, fieldBoughtQuantity); err != nil {
		return canceledEvent{}, err
	}
	if out.quantity <= 0 {
		return canceledEvent{}, errors.Invalid(CodeEventUnusable,
			"the %q event cancels %d units", e.Name, out.quantity)
	}

	return out, nil
}

// text reads a string field.
func text(e eventbus.Event, key string) string {
	value, _ := e.Data[key].(string)

	return value
}

// number reads a count that traveled as a decimal string.
func number(e eventbus.Event, key string) (int64, error) {
	raw, ok := e.Data[key].(string)
	if !ok {
		return 0, errors.Invalid(CodeEventUnusable,
			"the %q event carries no %s", e.Name, key)
	}

	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, errors.Invalid(CodeEventUnusable,
			"the %s of the %q event is not a number: %q", key, e.Name, raw)
	}
	if parsed < 0 {
		return 0, errors.Invalid(CodeEventUnusable,
			"the %s of the %q event is negative: %d", key, e.Name, parsed)
	}

	return parsed, nil
}

// FromContainer wires the flow and SUBSCRIBES it.
//
// The subscription is part of the wiring rather than a second call the composition
// root has to remember: a flow that is built and not subscribed is a flow that
// silently does nothing, which is the shape this whole slice exists to remove.
func FromContainer(c *container.Container, log *slog.Logger) (*Workflow, error) {
	if c == nil {
		return nil, errors.Internal(CodeNotReady,
			"the cancellation flow cannot be wired without a container")
	}

	inventory, err := resolve[Inventory](c, ServiceInventory)
	if err != nil {
		return nil, err
	}
	fulfillment, err := resolve[Fulfillment](c, ServiceFulfillment)
	if err != nil {
		return nil, err
	}
	links, err := resolve[Links](c, ServiceLink)
	if err != nil {
		return nil, err
	}
	bus, err := resolve[Subscriber](c, ServiceEventBus)
	if err != nil {
		return nil, err
	}

	w := New(inventory, fulfillment, links, log)

	if err := bus.Subscribe(topicLineCanceled, w.HandleLineCanceled); err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeNotReady,
			"the cancellation flow could not subscribe to %q", topicLineCanceled)
	}

	return w, nil
}

// resolve reads one service and says which name failed.
func resolve[T any](c *container.Container, name string) (T, error) {
	value, err := container.Resolve[T](c, name)
	if err != nil {
		var zero T

		return zero, errors.Wrap(err, errors.KindOf(err), CodeNotReady,
			"the cancellation flow needs %q and it could not be resolved", name)
	}

	return value, nil
}
