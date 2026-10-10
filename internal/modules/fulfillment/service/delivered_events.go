package service

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/eventbus"
)

// EventFulfillmentDelivered says a parcel reached its recipient (ADR 0437).
//
// # Who it is for
//
// Nothing inside gobit subscribes to it. A consumer running beside gobit — a
// marketplace that settles a seller's payout a week after delivery — reported
// polling every pending payout's parcels for this moment, and hears it from
// `webhook-out` instead; ADR 0437 names it in the arch gate's
// subscriberlessPublications. The shopper reads the moment from the storefront
// timeline.
//
// The keys are [EventFulfillmentCanceled]'s, for the same reasons, and the
// tracking number is not among them: it is a lookup key to the recipient's name
// and address at the carrier, and a reader that wants it asks for the parcel.
// A parcel bringing a return back is delivered too, to the warehouse, and its
// return_id says so.
//
// The name is a CROSS-MODULE CONTRACT: on the Redis backend it is also the stream
// name, so changing it stops every subscriber silently.
const EventFulfillmentDelivered = "fulfillment.delivered"

// EventFieldDeliveredAt is the moment the parcel was marked delivered, RFC 3339
// with nanoseconds, UTC: the delivered_at the row was written with.
const EventFieldDeliveredAt = "delivered_at"

// fulfillmentDeliveredEventID derives the event's id from the PARCEL: a parcel
// is delivered once, and a second report finds it delivered and records
// nothing.
func fulfillmentDeliveredEventID(fulfillmentID string) string {
	return EventFulfillmentDelivered + ":" + fulfillmentID
}

// fulfillmentDeliveredPayload is the body of the event, built in ONE place for
// the outbox row and the direct publish.
func fulfillmentDeliveredPayload(fulfillmentID, reference, returnID string, at time.Time) map[string]any {
	return map[string]any{
		EventFieldFulfillmentID: fulfillmentID,
		EventFieldReference:     reference,
		EventFieldReturnID:      returnID,
		EventFieldDeliveredAt:   at.UTC().Format(time.RFC3339Nano),
	}
}

// recordFulfillmentDelivered writes the event into the outbox INSIDE the
// caller's transaction; a failure fails the report, so a delivery is never
// recorded without the event that announces it.
func (s *Service) recordFulfillmentDelivered(
	ctx context.Context, fulfillmentID, reference, returnID string, at time.Time,
) error {
	if s.events == nil {
		// A service assembled by hand, with no bus and so no relay to keep the
		// promise a row would make.
		return nil
	}

	return s.store.WriteOutboxEvent(ctx,
		fulfillmentDeliveredEventID(fulfillmentID), EventFulfillmentDelivered,
		fulfillmentDeliveredPayload(fulfillmentID, reference, returnID, at))
}

// publishFulfillmentDelivered sends the event AFTER the transaction has
// committed. A failure is logged and swallowed: the delivery is recorded and
// the outbox row covers the loss.
func (s *Service) publishFulfillmentDelivered(
	ctx context.Context, fulfillmentID, reference, returnID string, at time.Time,
) {
	if s.events == nil {
		return
	}

	err := s.events.Publish(ctx, eventbus.Event{
		ID:   fulfillmentDeliveredEventID(fulfillmentID),
		Name: EventFulfillmentDelivered,
		Data: fulfillmentDeliveredPayload(fulfillmentID, reference, returnID, at),
	})
	if err != nil {
		s.log.ErrorContext(ctx,
			"the parcel delivery event could not be published; the parcel IS delivered and "+
				"the outbox relay will send it",
			"event", EventFulfillmentDelivered,
			"fulfillment_id", fulfillmentID,
			"error", err)
	}
}
