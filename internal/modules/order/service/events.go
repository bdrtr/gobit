package service

import (
	"context"
	"strconv"
	"time"

	"github.com/bdrtr/gobit/internal/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// EventOrderPlaced is the name of the event published when an order is created
// (plan Phase 6 DoD).
//
// The name is a CROSS-MODULE CONTRACT: the subscribers (notification delivery,
// accounting export, search index) listen for exactly this name and on the
// Redis backend the name is at the same time the stream name. Changing it means
// that every subscriber silently stops receiving events.
const EventOrderPlaced = "order.placed"

// The keys in the event's Data field.
//
// The keys are a contract too and they have to change together with the
// consumers. The payload is deliberately kept NARROW: putting every field a
// subscriber might need into the event turns the event into a second copy of
// the order and leads to the two representations diverging. For the detail a
// subscriber needs it holds the order_id and it can read the order.
//
// # Every field is a STRING — the numeric ones too
//
// ALL the values of the payload are of the string type; amounts and counters
// travel as decimal strings without a fraction (e.g. "6100"). The rule follows
// from the transport form of the data bus: the Redis Streams backend of
// [eventbus.EventBus] in production writes Data with json.Marshal and decodes
// it into a map[string]any when reading. JSON has a single number type, so a
// field put in as an int64 reaches the subscriber as a float64 — whereas the
// same field stays an int64 on the InMemory backend. The consequence was
// twofold:
//
//  1. A subscriber written according to the contract (e.Data["total"].(int64))
//     would work in development and FALL OVER IN PRODUCTION.
//  2. Amounts above 2^53 minor units would be silently rounded in a float64;
//     that is, money would travel over a float (plan Section 8: float NEVER).
//
// A string gives the SAME Go type and the EXACT value on both backends. The
// subscriber side reads it with strconv.ParseInt; the conversion being able to
// return an error is preferable to silent rounding.
//
// The e-mail is DELIBERATELY absent: events are written to Redis and are
// durable there; putting personal data into a durable stream is an unnecessary
// spread for information that already sits on the order itself (plan Section 8:
// sensitive data is not carried).
const (
	// EventFieldOrderID is the identifier of the order.
	EventFieldOrderID = "order_id"
	// EventFieldDisplayID is the human readable number of the order; a string
	// without a fraction.
	EventFieldDisplayID = "display_id"
	// EventFieldStatus is the status of the order.
	EventFieldStatus = "status"
	// EventFieldRegionID is the region of the order.
	EventFieldRegionID = "region_id"
	// EventFieldCustomerID is the customer of the order; empty on a guest order.
	EventFieldCustomerID = "customer_id"
	// EventFieldCurrencyCode is the currency of the order.
	EventFieldCurrencyCode = "currency_code"
	// EventFieldTotal is the total amount of the order: a minor unit INTEGER,
	// carried as a string without a fraction (e.g. "6100").
	EventFieldTotal = "total"
	// EventFieldItemCount is the number of lines on the order; a string without
	// a fraction.
	EventFieldItemCount = "item_count"
	// EventFieldPlacedAt is the moment the order was placed (RFC 3339, UTC).
	EventFieldPlacedAt = "placed_at"
)

// publishOrderPlaced publishes the "order.placed" event AFTER the order is
// written.
//
// # Why AFTER the write
//
// Had the event been published inside the transaction, a subscriber could
// receive the event before the transaction was even committed, would try to
// read an order that is not in the database and would see NotFound. When the
// publish is done after the commit, everyone who receives the event can find
// the order.
//
// # A publishing failure DOES NOT DROP the order
//
// The decision is deliberate and it has three reasons:
//
//  1. The order is a RECORD, whereas the event is the announcement of a fact
//     that happened. Rolling back an order whose payment was taken because of a
//     one-second unavailability of Redis would be a far more expensive loss
//     than the thing it tries to protect.
//  2. The publish returning SUCCESS does not guarantee delivery anyway: the
//     [eventbus.EventBus] contract says that Publish does not wait for the
//     handlers, that the InMemory backend delivers AT MOST ONCE and that the
//     event is lost if the process dies. Tying the write to this call would be
//     risking real data in exchange for a guarantee that does not exist.
//  3. Because the publish is done AFTER the commit the order is already
//     written; returning an error would be telling the caller "the order was
//     not created" and the saga would run a compensation (a cancellation)
//     needlessly.
//
// The price of this is that a lost event falls behind the record. The price is
// made VISIBLE: the failure is logged at the ERROR level with the identifier
// and the number of the order, so that the escaped event can be republished by
// hand or by a scanning job.
//
// For why the numeric fields are put in as STRINGS see [EventFieldTotal] and
// the block above it.
func (s *Service) publishOrderPlaced(ctx context.Context, order models.Order, itemCount int) {
	event := eventbus.Event{
		Name: EventOrderPlaced,
		Data: map[string]any{
			EventFieldOrderID:      order.ID,
			EventFieldDisplayID:    strconv.FormatInt(order.DisplayID, 10),
			EventFieldStatus:       order.Status.String(),
			EventFieldRegionID:     order.RegionID,
			EventFieldCustomerID:   order.CustomerID,
			EventFieldCurrencyCode: order.CurrencyCode,
			EventFieldTotal:        strconv.FormatInt(order.Total, 10),
			EventFieldItemCount:    strconv.Itoa(itemCount),
			EventFieldPlacedAt:     order.PlacedAt.UTC().Format(time.RFC3339Nano),
		},
	}

	if err := s.events.Publish(ctx, event); err != nil {
		s.log.ErrorContext(ctx, "the order event could not be published; the order WAS WRITTEN",
			"event", EventOrderPlaced,
			"order_id", order.ID,
			"display_id", order.DisplayID,
			"error", err)
	}
}
