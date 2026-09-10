package service

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/eventbus"
)

// The events this module publishes.
//
// # Why two and not three
//
// Money moves at two moments and the module's own read surface says so: a
// capture and a refund. An authorization moves nothing — it puts a hold on the
// customer's card — and ADR 0054 fixed the money-moment vocabulary at two for
// exactly that reason. A third topic would be a name for a reader nobody has
// named.
//
// The two are also the only writes an order can care about: of the six
// production callers that write a collection's totals, four move the AUTHORIZED
// figure alone, and an order's summary has no column for a hold.
const (
	// EventPaymentCaptured says money was collected against a collection.
	EventPaymentCaptured = "payment.captured"
	// EventPaymentRefunded says money was sent back against a collection.
	EventPaymentRefunded = "payment.refunded"
)

// The fields an event of this module carries.
//
// # The event carries NO amount, and that is the decision rather than an omission
//
// It names the collection and stops. A subscriber reads the amounts from the
// collection itself, at the moment it is told, which is the discipline the
// order module's own events already follow: carry an identifier, read the
// record.
//
// Three things make it the only workable shape here.
//
// A refund is deliberately NOT idempotent ([Service.RefundPayment]): two calls
// for ten units are a real refund of twenty. So an amount in the payload would
// be an INCREMENT, and the bus delivers at least once — a redelivered increment
// reports a number that never happened. What a subscriber needs is the
// CUMULATIVE total, and the only place that is always current is the collection.
//
// The order summary's merge keeps the LARGER of what it holds and what it is
// told, which is idempotent and order-independent for a cumulative figure and
// silently wrong for an incremental one. A payload without an amount makes the
// wrong shape unwritable.
//
// And the events of this module are forwarded to whatever endpoints an operator
// registered ([plugins/webhookout]). An amount here would put money figures on
// the wire to third parties; a collection identifier does not, and no new
// redaction rule is needed.
const (
	// EventFieldCollectionID is the payment collection the money moved against.
	EventFieldCollectionID = "payment_collection_id"
	// EventFieldOccurredAt is the moment, RFC 3339 with nanoseconds, UTC.
	EventFieldOccurredAt = "occurred_at"
)

// moneyMovedPayload is the body of both events.
//
// It is built in ONE place and used by both the outbox write and the direct
// publish. The order module builds its payload twice, by hand, with nothing
// comparing the two — a shape where one copy can gain a field and the other
// cannot, and where the same event id would then carry two different bodies.
func moneyMovedPayload(collectionID string, at time.Time) map[string]any {
	return map[string]any{
		EventFieldCollectionID: collectionID,
		EventFieldOccurredAt:   at.UTC().Format(time.RFC3339Nano),
	}
}

// moneyMovedEventID derives an event's id from the ROW that moved the money.
//
// It is derived from the capture or the refund and never from the collection:
// a collection moves money many times, and an id keyed on it would make the
// second refund look like a redelivery of the first. The outbox row is written
// with `ON CONFLICT (id) DO NOTHING`, so that collision would not fail — it
// would silently drop a real event.
//
// Being derived rather than random is what makes the outbox row and the direct
// publish ONE event: both carry this id, and a subscriber that is idempotent on
// it — which the bus's at-least-once contract already requires — cannot tell
// the two deliveries apart.
func moneyMovedEventID(topic, rowID string) string {
	return topic + ":" + rowID
}

// publishMoneyMoved sends the event AFTER the transaction has committed.
//
// A failure is logged and swallowed, for [Service.CreateSession]'s reason one
// module over: the money has already moved and the record is already written,
// so returning an error here would tell the caller that something did not
// happen when it did. The outbox row covers the loss — the relay publishes what
// this call missed — and this stays as the FAST path, so a subscriber usually
// hears in the same request rather than up to a minute later.
func (s *Service) publishMoneyMoved(ctx context.Context, topic, rowID, collectionID string, at time.Time) {
	err := s.events.Publish(ctx, eventbus.Event{
		ID:   moneyMovedEventID(topic, rowID),
		Name: topic,
		Data: moneyMovedPayload(collectionID, at),
	})
	if err != nil {
		s.log.ErrorContext(ctx, "the payment event could not be published; the MONEY ALREADY MOVED",
			"event", topic,
			"payment_collection_id", collectionID,
			"row_id", rowID,
			"error", err)
	}
}

// recordMoneyMoved writes the event into the outbox INSIDE the caller's
// transaction.
//
// A failure here fails the whole write, which is the order module's stance and
// the right one for money: a capture recorded without its promised event is
// exactly the state the outbox exists to prevent, and accepting it quietly
// would leave the guarantee looking present while it was not.
func (s *Service) recordMoneyMoved(
	ctx context.Context, topic, rowID, collectionID string, at time.Time,
) error {
	return s.store.WriteOutboxEvent(ctx,
		moneyMovedEventID(topic, rowID), topic, moneyMovedPayload(collectionID, at))
}
