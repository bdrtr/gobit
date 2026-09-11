package service

import (
	"context"
	"strconv"
	"time"

	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// EventOrderLineCanceled says units of one line will not be delivered.
//
// # Why this module publishes rather than acting
//
// Units written off after the checkout deducted their stock are units nobody
// will ever send and nobody counts as stock either. Putting them back is an
// inventory write, and this module may not make one (ADR 0006) — nor may it ask
// the fulfillment module how many of the line already shipped, which is what
// decides how many can come back. A flow above all three does both, and it hears
// about the cancellation here (ADR 0134).
//
// The name is a CROSS-MODULE CONTRACT, like [EventOrderPlaced]: on the Redis
// backend it is also the stream name, so changing it stops every subscriber
// silently.
const EventOrderLineCanceled = "order.line_canceled"

// The keys this event carries.
//
// # Why it carries FIGURES, where the payment events carry none
//
// ADR 0121 refused to put an amount into a payment event, and the reason was
// specific: a refund is not idempotent, so a figure would be an INCREMENT of an
// unknown base, and a redelivered increment reports a total that never existed.
//
// A cancellation row is the opposite shape. It is written once, never rewritten,
// and the numbers here are FACTS OF THAT ROW at the moment it was created — not
// increments of anything. A subscriber that read them back off the order later
// would get the same answer, which is exactly why they are safe to carry and why
// carrying them is worth one less read.
//
// What the subscriber CANNOT be given is how many units already shipped: that
// belongs to another module and changes after this event is written. It asks.
//
// Every value is a STRING, including the counts, for [EventOrderPlaced]'s
// reason: JSON has one number type and the Redis backend would hand a subscriber
// a float64 where the in-memory one hands an int64.
const (
	// EventFieldCancellationID is the cancellation row, and it is what makes
	// putting the units back IDEMPOTENT: the bus delivers at least once and
	// adding stock is deliberately not, so the inventory ledger holds this id
	// unique per cancellation movement.
	EventFieldCancellationID = "cancellation_id"
	// EventFieldOrderLineItemID is the line whose units were written off.
	EventFieldOrderLineItemID = "order_line_item_id"
	// EventFieldVariantID is the variant that line sells, which is how a
	// subscriber reaches the inventory item without reading the order.
	EventFieldVariantID = "variant_id"
	// EventFieldCanceledQuantity is how many units this row wrote off.
	EventFieldCanceledQuantity = "canceled_quantity"
	// EventFieldCanceledBefore is how many units of that line were ALREADY
	// spoken for — returned or canceled — before this row.
	//
	// It is here because the units that may come back are the ones that were
	// deducted and will not leave, and that window is the line's bought quantity
	// minus what shipped. A second cancellation must not return units the first
	// already returned, so a subscriber needs where in that window this row sits
	// rather than only its size.
	EventFieldCanceledBefore = "canceled_before"
	// EventFieldBoughtQuantity is how many units the line sold.
	EventFieldBoughtQuantity = "bought_quantity"
	// EventFieldCanceledAt is the moment, RFC 3339 with nanoseconds, UTC.
	EventFieldCanceledAt = "canceled_at"
)

// lineCanceledEventID derives the event's id from the CANCELLATION row.
//
// Not from the order and not from the line: a line can be written off many times
// and the outbox writes with `ON CONFLICT (id) DO NOTHING`, so an id keyed on
// either would make the second cancellation look like a redelivery of the first
// and drop it without a word (the payment module records the same trap).
func lineCanceledEventID(cancellationID string) string {
	return EventOrderLineCanceled + ":" + cancellationID
}

// lineCanceledPayload is the body of the event, built in ONE place.
//
// Both the outbox row and the direct publish use it, which is what makes them one
// event rather than two that can drift apart.
func lineCanceledPayload(
	orderID string, c models.OrderLineCancellation, variantID string, before, bought int64,
) map[string]any {
	return map[string]any{
		EventFieldOrderID:          orderID,
		EventFieldCancellationID:   c.ID,
		EventFieldOrderLineItemID:  c.OrderLineItemID,
		EventFieldVariantID:        variantID,
		EventFieldCanceledQuantity: strconv.FormatInt(c.Quantity, 10),
		EventFieldCanceledBefore:   strconv.FormatInt(before, 10),
		EventFieldBoughtQuantity:   strconv.FormatInt(bought, 10),
		EventFieldCanceledAt:       c.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

// recordLineCanceled writes the event into the outbox INSIDE the caller's
// transaction.
//
// A failure here fails the cancellation, and that is the right trade for this
// act: a write-off recorded without its promised event is stock that stays
// deducted forever with nothing anywhere saying it should not be. The outbox
// exists to prevent exactly that state, and accepting it quietly would leave the
// guarantee looking present while it was not.
func (s *Service) recordLineCanceled(
	ctx context.Context,
	orderID string, c models.OrderLineCancellation, variantID string, before, bought int64,
) error {
	return s.store.WriteOutboxEvent(ctx,
		lineCanceledEventID(c.ID), EventOrderLineCanceled,
		lineCanceledPayload(orderID, c, variantID, before, bought))
}

// publishLineCanceled sends the event AFTER the transaction has committed.
//
// A failure is logged and swallowed: the write-off is already recorded and the
// outbox row covers the loss — the relay publishes what this call missed. This
// stays as the FAST path so the stock is usually back in the same request rather
// than up to a minute later.
func (s *Service) publishLineCanceled(
	ctx context.Context,
	orderID string, c models.OrderLineCancellation, variantID string, before, bought int64,
) {
	err := s.events.Publish(ctx, eventbus.Event{
		ID:   lineCanceledEventID(c.ID),
		Name: EventOrderLineCanceled,
		Data: lineCanceledPayload(orderID, c, variantID, before, bought),
	})
	if err != nil {
		s.log.ErrorContext(ctx,
			"the cancellation event could not be published; the units are written off and "+
				"their STOCK IS STILL DEDUCTED until the outbox relay catches up",
			"event", EventOrderLineCanceled,
			"order_id", orderID,
			"cancellation_id", c.ID,
			"error", err)
	}
}
