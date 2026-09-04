package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// The names of the catalog domain events (plan Section 5.4).
//
// The names are a CROSS-MODULE CONTRACT: the subscribers (the search index, the
// storefront cache, an external catalog export) listen for exactly these names,
// and on the Redis backend the name is also the stream name. A name changing
// means every subscriber silently stops receiving events — nobody sees an
// error, only the index goes stale.
//
// The scope is the product's OWN fields. Variant, option and image writes do
// NOT produce these three events; if they need to, the right step is to add a
// separate name, not to publish "product.updated" from there too: one name
// having two different meanings leaves the subscriber unable to answer "what
// did this event change" from the payload.
const (
	// EventProductCreated is published when a new product is written.
	EventProductCreated = "product.created"
	// EventProductUpdated is published when the product's own fields are updated.
	EventProductUpdated = "product.updated"
	// EventProductDeleted is published when the product is SOFT deleted.
	EventProductDeleted = "product.deleted"
)

// The keys in the Data field of the events.
//
// The keys are a contract too and have to change together with the consumers.
// The payload is deliberately NARROW: putting every field a subscriber might
// want (title, handle, collection, variants) into the event turns the event
// into a second copy of the product and the two representations drift apart —
// an index that processes the event will one day show a title that is not in
// the record. The subscriber has the product_id and can read the record; for
// bulk access to the whole storefront representation the "product.interop"
// surface is here as well (see interop.go).
//
// # Every field is a STRING — the numeric ones too
//
// Both of today's fields are naturally text; the rule is written down HERE all
// the same, because the FIRST numeric field to be added to the payload (variant
// count, version number) is where it is most likely to be violated. The full
// rationale is written in internal/modules/order/service/events.go and is NOT
// REPEATED: in short, because the Redis backend turns Data into JSON, a field
// set as an int64 reaches the subscriber as a float64 — that is, a subscriber
// written to the contract works in development and FALLS OVER IN PRODUCTION.
//
// # Why status is in the payload
//
// It is the deliberate exception to the narrowness rule. The decision the
// subscriber makes with this event is mostly "index it, or DROP it from the
// index", and that decision depends only on the status. Without the field, a
// bulk update over draft products would force the subscriber into one read per
// event only to THROW the result away; the most frequent case would be the
// most expensive path.
//
// The price is that the value can go stale: the field tells the status AT THE
// MOMENT of the event, not the CURRENT status. A subscriber that makes a final
// decision still reads the record — status only gives it the right to discard
// cheaply the events that are not worth a read.
//
// # No personal data goes onto a durable stream
//
// Catalog data is not personal to begin with; the rule is written down all the
// same because the event is written onto a durable stream (a Redis stream) and
// a field put there cannot be taken back. Whoever wants to add "the creating
// user" to the payload one day should see this line.
const (
	// EventFieldProductID is the product's id; it is present in all THREE events.
	EventFieldProductID = "product_id"
	// EventFieldStatus is the product's publication status AT THE MOMENT of the
	// event ("draft", "published" or "archived"); it is ABSENT from the delete
	// event.
	EventFieldStatus = "status"
)

// publishProductEvent publishes the catalog event AFTER the write has been
// COMMITTED.
//
// If status is given empty it is not written into the event; the delete event
// goes down this path. A soft deleted record is returned by no read, so the
// value cannot be verified by the subscriber, and the "drop it from the index"
// action does not look at the status anyway. The delete event carries no handle
// either: putting it there would require an extra read BEFORE the delete, and a
// subscriber that caches by handle already knows that mapping from the
// created/updated events it received earlier.
//
// # Why after the write
//
// Were the event published inside the transaction, a subscriber could receive
// the event before the commit, try to read a product that is not yet in the
// database and see NotFound. When the publish happens after the commit,
// everyone who receives the event can find the record.
//
// # A publish failure does NOT FAIL the write
//
// A product update failing because "the event bus is unreachable" is WRONG:
//
//  1. The product is the RECORD, the event is the announcement of something
//     that happened. The admin interface being unable to edit the catalog
//     because Redis was unreachable for a second is a more expensive loss than
//     the thing it tries to protect.
//  2. The publish happens AFTER the commit; returning an error would tell the
//     caller "the change was not applied", whereas it was. The caller repeats
//     the request and in CreateProduct that produces either a second product or
//     a handle conflict (409) — a fault of the event bus would look to the user
//     like a catalog error.
//  3. A SUCCESSFUL publish does not guarantee delivery either: the
//     [eventbus.EventBus] contract says that Publish does not wait for the
//     handlers and that the InMemory backend delivers AT MOST ONCE. Tying the
//     write to this call would risk real data in exchange for a guarantee that
//     does not exist.
//
// The decision on the order side is the same, but one of its reasons (the saga
// running an unnecessary compensation) does NOT apply here: product CRUD is not
// inside a saga. What settles the outcome is not that one, it is the first two
// items.
//
// The price is that the missed event falls behind the record. The price is made
// VISIBLE: the failure is logged at ERROR level with the event name and the
// product id; a missed event can be republished by hand or by a sweeper job.
//
// # If the bus is not registered the event is skipped silently
//
// [Options.Events] may be nil and that is ONLY for embedded use and for tests:
// [github.com/bdrtr/gobit/internal/modules/product.Module.Register] resolves the
// bus from the container and fails startup if it cannot find it, so a nil bus
// does not occur in production. The same pattern exists in the Query layer as
// well (see [Service.enrichVariants]).
func (s *Service) publishProductEvent(ctx context.Context, name, productID string, status models.Status) {
	if s.events == nil {
		s.log.DebugContext(ctx, "the event bus is not registered; the catalog event was skipped",
			"event", name, "product_id", productID)
		return
	}

	data := map[string]any{EventFieldProductID: productID}
	if status != "" {
		data[EventFieldStatus] = status.String()
	}

	if err := s.events.Publish(ctx, eventbus.Event{Name: name, Data: data}); err != nil {
		s.log.ErrorContext(ctx, "the catalog event could not be published; the product WAS WRITTEN",
			"event", name,
			"product_id", productID,
			"error", err)
	}
}
