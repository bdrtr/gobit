// Package ordercancel puts back the stock of units that were written off.
//
// # What was wrong
//
// The checkout's last step confirms the reservations, which DEDUCTS the stock. So
// an order that exists has already had its units taken off the sellable figure
// (a line the checkout let through without stock loses its units only when its
// claim is filled, ADR 0392), and a line written off afterwards is a unit nobody
// will ever send and nobody counts as stock either. Nothing put it back — neither
// the whole-order cancellation nor the partial one, and the order module cannot:
// the units live in another module (Principle 2.1/2.4, ADR 0006). Gap D70.
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
//
// # THREE events, because the window has two edges and one moves twice
//
// How many of a line's units belong on the shelf is
// `min(canceled, bought − in a live parcel)`, and both sides of that move. A write
// off grows the left side and this flow hears it as "order.line_canceled"; a
// parcel being canceled shrinks the right side and it hears that as
// "fulfillment.canceled" (ADR 0139). Since ADR 0423 a parcel coming back
// undelivered shrinks it too, as far as no return or replacement speaks for its
// units, and the flow hears that as "fulfillment.returned" and recounts as for
// a canceled parcel. A return or a replacement withdrawn later moves it as well,
// and no event says so (docs/known-limits.md).
//
// The second one was missing for a while and the gap had teeth, because the
// missing half was the resolution the first half's record RECOMMENDED: ADR 0135
// declined to withdraw a shipment under a customer and told the shop to cancel the
// parcel instead. Doing that released units the write-off had already counted as
// gone, and nothing recomputed anything — the goods were neither sold, nor
// shipped, nor stock (gap D75).
package ordercancel

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"sync"
	"time"

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
	// ServiceOrder is the order module's cross-module surface.
	ServiceOrder = "order.interop"
	// ServiceLink is the core's Module Links service.
	ServiceLink = "core.link"
	// ServiceEventBus is the bus this flow listens on.
	ServiceEventBus = "core.eventbus"
	// ServiceWorkflow is the name this flow registers itself under, so that
	// the container shuts it down before the bus (ADR 0420). Nothing resolves
	// it; the container's shutdown is its only reader.
	ServiceWorkflow = "ordercancel.shutdown"
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
	// inventory item to location. A backordered line's fill is not among them;
	// [Inventory.SettleBackorder] names its shelf.
	SaleLocations(ctx context.Context, orderID string) (map[string]string, error)
	// SettleBackorder withdraws from the line's waiting claims the units that
	// will not leave and answers, per item, the units of the line the module
	// never deducted and where a filled claim deducted the rest (ADR 0392). An
	// error comes with the answers when only the fill that follows failed.
	SettleBackorder(
		ctx context.Context, orderLineItemID string, bought, window int64,
	) (undeducted map[string]int64, filledAt map[string]string, err error)
	// ReturnCanceled brings a LINE's returned units up to a target and reports
	// whether the target was already met. It takes a target rather than a
	// quantity because two acts put a line's units back and neither may compute a
	// delta from a state the other has not yet changed (ADR 0142).
	ReturnCanceled(
		ctx context.Context,
		inventoryItemID, locationID, lineItemID string,
		target int64,
		reference string,
	) (alreadyBack bool, err error)
}

// Fulfillment is the slice of the fulfillment module this flow calls.
type Fulfillment interface {
	// HeldForReferenceLocked sums, per order line, the units the order's
	// outgoing parcels hold, counted by the reference the module stores on
	// each item, an addition's units in its parent's parcel among them (ADR
	// 0428), and under the order's dispatch lock (ADR 0420). spoken is, per line, how many
	// units a return or a replacement speaks for, which a parcel that came back
	// undelivered holds its units to (ADR 0423).
	HeldForReferenceLocked(ctx context.Context, reference string, spoken map[string]int64) (map[string]int64, error)
	// QuantitiesOfFulfillment sums, per order line, the units ONE parcel holds —
	// a CANCELED one included, which is the whole reason it is separate from the
	// method above.
	QuantitiesOfFulfillment(ctx context.Context, fulfillmentID string) (map[string]int64, error)
}

// Orders is the slice of the order module this flow calls.
//
// It reads what each line sold and what was written off. The line cancellation
// gets those numbers on the event; a parcel cancellation has no such event to read
// them from, so it asks.
type Orders interface {
	// DispatchableLinesJSON answers, per line, the bought and canceled counts,
	// spoken_for (what a return or a replacement speaks for, ADR 0423) and the
	// variant, as a JSON array. The schema is repeated in [orderLine].
	DispatchableLinesJSON(ctx context.Context, orderID string) (json.RawMessage, error)
}

// Links reads the Module Links this flow needs.
type Links interface {
	// ListMany returns the links of the given source ids in a SINGLE query.
	ListMany(ctx context.Context, name string, fromIDs []string) (map[string][]string, error)
	// ListManyByTo returns the links of the given TARGET ids, which is how a
	// parcel finds the order it was opened for.
	ListManyByTo(ctx context.Context, name string, toIDs []string) (map[string][]string, error)
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
	orders      Orders
	links       Links
	log         *slog.Logger
	// busyWaits are the pauses between asks while a parcel of the order is
	// being opened ([Workflow.heldUnderLock]).
	busyWaits []time.Duration
	// draw answers a number in [0, n]; it spreads each pause ([jittered]).
	draw func(n int64) int64
	// stop is closed by [Workflow.Shutdown], which ends every pause at once.
	stop     chan struct{}
	stopOnce sync.Once
}

// busyBudget is how long one call of a handler may spend asking again while a
// parcel of the order is being opened (ADR 0420).
//
// The bus calls a failing handler once more than it has retry delays, waiting
// those delays between the calls (ADR 0240), and on the Redis backend a whole
// delivery has to end inside the takeover idle time, or another process takes
// the message over while this one still handles it. ADR 0240 refused waits of
// minutes in a handler for that reason, and the budget keeps to it: the calls
// share three quarters of [eventbus.DefaultClaimMinIdle] less the delays
// between them, and the last quarter is left for the asks themselves. The flow
// cannot see the bus's configured idle time, so the budget is derived from the
// default: an embedder who sets the takeover time below about 45 s brings the
// overlap back. The bound is per message, too: a read takes up to a batch of
// messages and each one's idle time starts at the read, so the later ones of a
// batch of busy messages can still pass it.
func busyBudget() time.Duration {
	delays := eventbus.HandlerRetryDelays()
	var between time.Duration
	for _, d := range delays {
		between += d
	}

	return (eventbus.DefaultClaimMinIdle*3/4 - between) / time.Duration(len(delays)+1)
}

// defaultBusyWaits are the pauses between asks for what an order's parcels
// hold while a parcel of it is being opened: short at first, then a second
// apart, until [busyBudget] is spent (ADR 0420).
//
// The bus's own retries are a second and a quarter, and an open's carrier call
// can take longer, so a busy answer handed straight to the bus would end the
// direct delivery within it. Waiting here holds no connection: the fulfillment
// module answers busy without waiting.
var defaultBusyWaits = func() []time.Duration {
	budget := busyBudget()
	var waits []time.Duration
	var spent time.Duration
	for _, wait := range []time.Duration{100 * time.Millisecond, 200 * time.Millisecond,
		400 * time.Millisecond, 800 * time.Millisecond} {
		waits, spent = append(waits, wait), spent+wait
	}
	for spent+time.Second <= budget {
		waits, spent = append(waits, time.Second), spent+time.Second
	}

	return waits
}()

// New builds the flow from already-resolved dependencies.
func New(
	inventory Inventory, fulfillment Fulfillment, orders Orders, links Links, log *slog.Logger,
) *Workflow {
	if log == nil {
		log = slog.Default()
	}

	return &Workflow{
		inventory: inventory, fulfillment: fulfillment, orders: orders,
		links: links, log: log, busyWaits: defaultBusyWaits,
		draw: drawUniform,
		stop: make(chan struct{}),
	}
}

// Shutdown ends every pause [Workflow.heldUnderLock] is in and every one it
// would start: a handler asking again while a parcel is being opened returns
// its busy fault at once (ADR 0420).
//
// The bus hands a handler a context its own shutdown does not cancel, so the
// flow is registered in the container after the bus and closed before it,
// which lets the bus's shutdown find no handler still pausing. On Redis the
// busy fault, failing once the bus's shutdown has begun, leaves the message
// pending and the next process takes it over; the in-memory bus has no pending
// list, and the event comes back only if the relay has not yet published its
// outbox row.
func (w *Workflow) Shutdown(context.Context) error {
	w.stopOnce.Do(func() { close(w.stop) })

	return nil
}

// HandleLineCanceled puts back the stock of units that were written off.
//
// # Why a missing piece is not an error
//
// The handler returns nil for everything that will never become actionable: a
// variant that tracks no stock, an order whose checkout never deducted anything, a
// cancellation whose units had all shipped. Returning an error would have the bus
// call it again for an event that cannot succeed and log a failure that is none,
// burying the failures that are real.
//
// What DOES return an error is a fault that may pass: the fulfillment module being
// unreachable, the inventory write failing. Those the bus tries again, twice
// within a second and a quarter (ADR 0240); a longer outage is logged and lost.
func (w *Workflow) HandleLineCanceled(ctx context.Context, e eventbus.Event) error {
	in, err := readCanceledEvent(e)
	if err != nil {
		return err
	}

	// The window of units that can come back: bought minus those a live parcel
	// holds. A parcel that was canceled never left, so its units are still here.
	committed, lines, err := w.committedQuantity(ctx, in.orderID, in.lineItemID)
	if err != nil {
		return err
	}

	target := targetOnShelf(in.bought, in.before+in.quantity, committed)
	if target == 0 {
		w.log.DebugContext(ctx, "a canceled line has no units that belong on the shelf",
			"cancellation_id", in.cancellationID, "order_line_item_id", in.lineItemID,
			"bought", in.bought, "committed", committed,
			"canceled_total", in.before+in.quantity)

		return nil
	}

	parts := w.lineStock(ctx, lines, in.orderID, in.lineItemID, in.variantID)

	owed, settleErr := w.settle(ctx, in.orderID, in.lineItemID, in.bought, target)
	if owed == nil {
		return settleErr
	}

	return errors.Join(w.putBack(ctx, parts, in.lineItemID, target, in.cancellationID, owed), settleErr)
}

// lineOwed is what the inventory module answers about a line's stock, per item:
// the shelf the order's sale left from, the units of the line it never deducted
// and the shelf a filled claim deducted at (ADR 0392). The last two are empty
// for a line the checkout reserved.
type lineOwed struct {
	soldAt     map[string]string
	undeducted map[string]int64
	filledAt   map[string]string
}

// settle reads the shelves the order's sale left from, then withdraws from the
// line's waiting claims the units that will not leave — window, the same number
// the shelf's target is — and reads what the line's stock never lost.
//
// # Why the shelves are read FIRST
//
// The checkout records its claims before it confirms a reservation, so a sale of
// the order seen here means the line's claim exists and the settlement after it
// finds it. Read the other way round, a settlement that ran before the claim
// answers "no claim", and a sale confirmed in between credits the shelf with the
// whole window of a line whose stock lost nothing (D242).
//
// # Why the location comes from the LEDGER
//
// The units have to go back where they came from, and the reservation that knew
// the location is keyed to the CART's line item, which an order does not carry.
// So the sale movement is what remembers: the checkout writes the order onto it,
// and this reads it back (ADR 0134). A backordered line's fill is not among
// those sales; its claim names its own shelf.
//
// It returns no answer when a read or the withdrawal failed, and the act stops
// there: putting the window back without knowing what the line never lost is
// the fault D242 records. An error that comes WITH the answer is the fill that
// runs after the withdrawal; the answer stands, and the error is returned after
// the put-back so the bus tries the fill again.
func (w *Workflow) settle(
	ctx context.Context, orderID, lineItemID string, bought, window int64,
) (*lineOwed, error) {
	if w.inventory == nil {
		return nil, errors.Internal(CodeNotReady,
			"the cancellation flow is not wired, so a canceled line cannot be acted on")
	}

	soldAt, err := w.inventory.SaleLocations(ctx, orderID)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeEventUnusable,
			"the shelf the units of order %s left from could not be read", orderID)
	}

	undeducted, filledAt, err := w.inventory.SettleBackorder(ctx, lineItemID, bought, window)
	if err != nil {
		err = errors.Wrap(err, errors.KindOf(err), CodeEventUnusable,
			"the backorder of line %s could not be settled", lineItemID)
	}
	if err != nil && len(undeducted) == 0 {
		return nil, err
	}

	return &lineOwed{soldAt: soldAt, undeducted: undeducted, filledAt: filledAt}, err
}

// shelfTarget is how many of one part's units go back on the shelf: the line's
// window times the part's units per line unit, less the units the line's stock
// never lost, never below zero (ADR 0392).
func shelfTarget(window, perUnit, undeducted int64) int64 {
	return max(0, window*perUnit-undeducted)
}

// stockPart is one variant a line's units are made of, and how many of it ONE
// unit of the line holds: the line's own variant once, or each component of a
// line that sold a bundle (ADR 0235).
type stockPart struct {
	variantID string
	perUnit   int64
}

// lineStock answers what a written-off line's units are made of.
//
// The composition is the ORDER's, as the line was sold (ADR 0235), read from
// the answer the parcel act reads too, passed in by the caller, which read it
// for what a return or a replacement speaks for ([Workflow.committedQuantity]):
// the bundle may have been edited since, and the parts to put back are the ones
// the sale took. A line the answer does not carry is read as the event names
// it, its own variant once — the shape every line had before bundles, and the
// one a bundle line cannot have.
func (w *Workflow) lineStock(
	ctx context.Context, lines map[string]orderLine, orderID, lineItemID, variantID string,
) []stockPart {
	line, ok := lines[lineItemID]
	if !ok {
		w.log.WarnContext(ctx, "a canceled line is missing from its order's lines; its own variant is put back",
			"order_id", orderID, "order_line_item_id", lineItemID)

		return []stockPart{{variantID: variantID, perUnit: 1}}
	}

	return line.stockParts()
}

// targetOnShelf is how many of a line's written-off units will not leave.
//
// # One invariant, computed by both acts
//
// This is the number of the line's units that will not leave. Units a live
// parcel holds have left or are about to, so of `bought - committed` the ones
// that were actually written off are
//
//	min(canceledTotal, bought - committed)
//
// For a line the checkout reserved, every unit bought was deducted, so it is
// also what goes back on the shelf. For a backordered line, the inventory module
// withdraws these units from the claim and answers how many it never deducted;
// the shelf's target is this minus those, never below zero (ADR 0392,
// [shelfTarget]).
//
// Both sides move: a write-off grows `canceledTotal`, a parcel being canceled
// shrinks `committed`. Each act computes this same number from the state it finds
// and asks the inventory module to bring the line's total UP TO it.
//
// # Why a target and not an increment
//
// Because an increment needs to know what the other act has already done, and
// the previous design had each of them ASSUME it. The parcel act's "what was
// already owed" term assumed the write-offs had run; when the bus delivered them
// in the other order — which it may, being asynchronous and at-least-once — the
// parcel act put back what it expected the write-off to have left, and the
// write-off then found a full window and put back everything. Five canceled units
// became eight on the shelf (D82).
//
// A target has no such assumption. Whichever act arrives first does the work, the
// other finds the total already there, and a redelivery finds it too.
//
// Clamped at zero: an order whose parcels hold more than it sold is a state this
// flow did not create, and a negative window would ask for a negative target.
func targetOnShelf(bought, canceledTotal, committed int64) int64 {
	if canceledTotal <= 0 {
		return 0
	}

	window := bought - committed
	if window <= 0 {
		return 0
	}

	return min(canceledTotal, window)
}

// committedQuantity answers how many units of the line a live parcel holds.
//
// The fulfillment module counts them by the reference each item stores, under
// the order's dispatch lock (ADR 0420): the population it holds a new parcel
// to (ADR 0409), so a parcel whose link to the order was not written counts, so
// do the units an addition put into its parent's parcel (ADR 0428), and a
// parcel being opened while the units are written off is counted once it
// commits rather than missed and its units put back on the shelf.
//
// A parcel that came back undelivered holds its units only as far as a return
// or a replacement speaks for them (ADR 0423), so that figure is read first,
// from the order's lines as the parcel cancellation reads them, and the lines
// are answered with the count; a read that fails is a fault that may pass, and
// the bus calls the handler again.
//
// The figure is the one standing when it is read, once, before the count
// waits for a parcel being opened ([Workflow.heldUnderLock]), and nothing
// recounts when it later moves. A return written after the write-off cannot
// name the written-off units: returns and write-offs share one limit, what was
// bought. A replacement has no such limit, so the outcome depends on the order
// of the acts: one recorded after this count is not seen and the units that
// came back go on the shelf, and one withdrawn after it leaves the units it
// spoke for off the books (docs/known-limits.md).
func (w *Workflow) committedQuantity(
	ctx context.Context, orderID, lineItemID string,
) (committed int64, lines map[string]orderLine, err error) {
	if w.fulfillment == nil || w.orders == nil {
		return 0, nil, errors.Internal(CodeNotReady,
			"the cancellation flow is not wired, so a canceled line cannot be acted on")
	}

	if lines, err = w.orderLines(ctx, orderID); err != nil {
		return 0, nil, err
	}

	held, err := w.heldUnderLock(ctx, orderID, spokenOf(lines))
	if err != nil {
		return 0, nil, errors.Wrap(err, errors.KindOf(err), CodeEventUnusable,
			"the shipped units of order %s could not be read", orderID)
	}

	return held[lineItemID], lines, nil
}

// heldUnderLock asks the fulfillment module what the order's live outgoing
// parcels hold, and asks again while it answers that a parcel of the order is
// being opened (ADR 0420).
//
// That answer is [errors.KindUnavailable]: the module does not wait for the
// order's dispatch lock, which an open holds for its whole carrier call. This
// flow waits instead, holding nothing, by [Workflow.busyWaits], each pause
// spread by [jittered]; a parcel that commits in the meantime is counted on the
// next ask. Past the last wait, when the context ends or when the flow is shut
// down, the busy answer goes to the bus, which calls the handler twice more
// (ADR 0240), all of it inside [busyBudget]. The event is not stored after
// that, but all three topics are outbox topics, so the relay delivers it once more
// within about a minute, and the restock is a target the line's next act
// brings the shelf up to (ADR 0142).
func (w *Workflow) heldUnderLock(
	ctx context.Context, orderID string, spoken map[string]int64,
) (map[string]int64, error) {
	for attempt := 0; ; attempt++ {
		held, err := w.fulfillment.HeldForReferenceLocked(ctx, orderID, spoken)
		if err == nil || errors.KindOf(err) != errors.KindUnavailable || attempt >= len(w.busyWaits) {
			return held, err
		}

		timer := time.NewTimer(jittered(w.busyWaits[attempt], w.draw))
		select {
		case <-ctx.Done():
			timer.Stop()

			return nil, err
		case <-w.stop:
			timer.Stop()

			return nil, err
		case <-timer.C:
		}
	}
}

// itemOf answers which inventory item the variant tracks; false for a variant
// that tracks none.
func (w *Workflow) itemOf(ctx context.Context, variantID string) (itemID string, tracked bool, err error) {
	if w.links == nil || w.inventory == nil {
		return "", false, errors.Internal(CodeNotReady,
			"the cancellation flow is not wired, so a canceled line cannot be acted on")
	}

	// The variant reaches its inventory item through the "variant_inventory" LINK,
	// which is how the return flow does it one directory over: the binding is a
	// link rather than a column, so neither module names the other.
	linked, err := w.links.ListMany(ctx, linkVariantInventory, []string{variantID})
	if err != nil {
		return "", false, errors.Wrap(err, errors.KindOf(err), CodeEventUnusable,
			"the inventory item of variant %s could not be read", variantID)
	}
	if len(linked[variantID]) == 0 {
		// A variant that tracks no stock has none to put back. It is an ordinary
		// shape — a service, a digital good — rather than a fault.
		w.log.DebugContext(ctx, "a canceled line sells a variant that tracks no stock",
			"variant_id", variantID)

		return "", false, nil
	}

	return linked[variantID][0], true, nil
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
	orders, err := resolve[Orders](c, ServiceOrder)
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

	w := New(inventory, fulfillment, orders, links, log)

	if err := bus.Subscribe(topicLineCanceled, w.HandleLineCanceled); err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeNotReady,
			"the cancellation flow could not subscribe to %q", topicLineCanceled)
	}
	if err := bus.Subscribe(topicFulfillmentCanceled, w.HandleFulfillmentCanceled); err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeNotReady,
			"the cancellation flow could not subscribe to %q", topicFulfillmentCanceled)
	}
	if err := bus.Subscribe(topicFulfillmentReturned, w.HandleFulfillmentReturned); err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeNotReady,
			"the cancellation flow could not subscribe to %q", topicFulfillmentReturned)
	}

	// Registered so that the container closes it, and closes it BEFORE the bus,
	// which was registered earlier: [Workflow.Shutdown] ends the pauses of the
	// handlers the bus's own shutdown then waits for.
	if err := c.Provide(ServiceWorkflow, w); err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeNotReady,
			"the cancellation flow could not be registered for shutdown")
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
