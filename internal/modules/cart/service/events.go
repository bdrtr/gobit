package service

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// The events this module publishes (ADR 0153).
//
// # Why two, and why these two
//
// A funnel is two numbers: how many carts a shop opened and how many of them
// became orders. The second number was already on the bus — "order.placed" has
// been published since the order module existed — and the first was nowhere at
// all: until this file the cart module published NOTHING, so the denominator of
// every conversion question a shop can ask did not exist.
//
// "cart.completed" is published beside it rather than left to "order.placed",
// because the two are not the same moment. A cart is completed by the checkout
// saga's last step; an order is placed by its second. Between them lie the
// payment and the compensation, so an order that fails after it was opened has a
// placed event and NO completed cart — and that difference is exactly what an
// operator asking "where do my carts die" needs to see.
//
// # Why not a per-line event
//
// Because every published topic of this repository is MANDATORILY forwarded to
// whatever endpoints an operator registered (plugins/webhookout, and its own gate
// fails the build in both directions). A "cart.line_added" topic would therefore
// multiply every installation's delivery table by the shopper's clicking, and the
// shop that wanted the funnel would pay for it in somebody else's queue.
const (
	// EventCartCreated says a cart was opened.
	EventCartCreated = "cart.created"
	// EventCartCompleted says a cart was completed and is now immutable.
	EventCartCompleted = "cart.completed"
)

// The fields an event of this module carries.
//
// # It carries NO money and NO identity, and both are decisions
//
// No amount: the payload would be on the wire to third parties (the forwarding
// above), and the cart's totals are a moving figure until the last calculation —
// an amount stamped at creation would be wrong for most carts and right for none
// in particular. A subscriber that needs the money reads the cart.
//
// No customer and no e-mail: the cart's contact address is the one field the
// module's own personal-data declaration names, and the order module already
// refuses to put an address on its event for the same reason. The region is a
// shop's own dimension and carries nothing about the shopper.
//
// The region IS carried rather than left to a read, and that is the one place
// this payload departs from the payment module's "name the record and stop": a
// cart is DELETABLE (soft delete, and the erasure flow removes it outright), so a
// subscriber told only the id may find nothing there by the time it looks. The
// region and the currency are facts of the cart's first moment and never change
// afterwards, so carrying them is safe under a redelivery in a way an amount
// would not be.
const (
	// EventFieldCartID is the cart the event is about.
	EventFieldCartID = "cart_id"
	// EventFieldRegionID is the region the cart was opened in.
	EventFieldRegionID = "region_id"
	// EventFieldCurrencyCode is the cart's ISO 4217 code, upper case.
	EventFieldCurrencyCode = "currency_code"
	// EventFieldOccurredAt is the moment, RFC 3339 with nanoseconds, UTC.
	EventFieldOccurredAt = "occurred_at"
)

// cartEventPayload is the body of both events.
//
// It is built in ONE place and used by both the outbox write and the direct
// publish. The order module builds its payload twice, by hand, with nothing
// comparing the two — a shape where one copy can gain a field and the other
// cannot, and where the same event id would then carry two different bodies. The
// payment module's own note says the same thing; this is the third module to
// follow it rather than the first to repeat the mistake.
func cartEventPayload(cart models.Cart, at time.Time) map[string]any {
	return map[string]any{
		EventFieldCartID:       cart.ID,
		EventFieldRegionID:     cart.RegionID,
		EventFieldCurrencyCode: cart.CurrencyCode,
		EventFieldOccurredAt:   at.UTC().Format(time.RFC3339Nano),
	}
}

// cartEventID derives an event's id from the topic and the cart.
//
// A cart is created once and completed once, so the cart's own id makes each
// topic's event unique without a second column to key on — and being DERIVED is
// what makes the outbox row and the direct publish ONE event rather than two: a
// subscriber that is idempotent on the id, which the bus's at-least-once contract
// already requires of it, cannot tell the two deliveries apart.
//
// The topic is part of the id because the same cart produces both events; keyed
// on the cart alone, the completion would look like a redelivery of the creation
// and the outbox's `ON CONFLICT (id) DO NOTHING` would drop it in silence.
func cartEventID(topic, cartID string) string {
	return topic + ":" + cartID
}

// publishCartEvent sends the event AFTER the transaction has committed.
//
// A failure is logged and swallowed: the cart is already written, so returning an
// error here would tell the caller that something did not happen when it did. The
// outbox row covers the loss — the relay publishes what this call missed — and
// this stays as the FAST path, so a subscriber usually hears in the same request
// rather than up to a minute later.
func (s *Service) publishCartEvent(ctx context.Context, topic string, cart models.Cart, at time.Time) {
	err := s.events.Publish(ctx, eventbus.Event{
		ID:   cartEventID(topic, cart.ID),
		Name: topic,
		Data: cartEventPayload(cart, at),
	})
	if err != nil {
		s.log.ErrorContext(ctx, "the cart event could not be published; the CART IS ALREADY WRITTEN",
			"event", topic,
			"cart_id", cart.ID,
			"error", err)
	}
}

// recordCartEvent writes the event into the outbox INSIDE the caller's
// transaction.
//
// A failure here fails the whole write, which is the stance the order, payment
// and fulfillment modules already take: a cart recorded without its promised
// event is exactly the state the outbox exists to prevent, and accepting it
// quietly would leave the guarantee looking present while it was not.
func (s *Service) recordCartEvent(
	ctx context.Context, topic string, cart models.Cart, at time.Time,
) error {
	return s.store.WriteOutboxEvent(ctx,
		cartEventID(topic, cart.ID), topic, cartEventPayload(cart, at))
}
