package service

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/eventbus"
)

// EventFulfillmentReturned says a parcel came back to the sender undelivered
// (ADR 0423).
//
// # Why a parcel coming back is announced
//
// Since ADR 0423 a parcel that came back holds its units only as far as a return
// or a replacement speaks for them, so marking it come back lowers what the
// order's parcels hold: it is an input of the restock target a write-off sets
// (ADR 0142), as a parcel being canceled is. A line written off while its parcel
// was on the way put nothing back, since the units had left, and when the
// parcel comes back nothing else recomputes the shelf. The order cancellation
// flow hears this event and recounts as it does for a canceled parcel.
//
// The keys are [EventFulfillmentCanceled]'s, for the same reasons: identities a
// subscriber asks about rather than quantities, and the reference for a parcel
// whose link to its order was not written.
//
// The name is a CROSS-MODULE CONTRACT: on the Redis backend it is also the stream
// name, so changing it stops every subscriber silently.
const EventFulfillmentReturned = "fulfillment.returned"

// EventFieldReturnedAt is the moment the parcel was marked come back, RFC 3339
// with nanoseconds, UTC.
const EventFieldReturnedAt = "returned_at"

// fulfillmentReturnedEventID derives the event's id from the PARCEL: a parcel
// comes back once, and a second report finds it already returned and records
// nothing.
func fulfillmentReturnedEventID(fulfillmentID string) string {
	return EventFulfillmentReturned + ":" + fulfillmentID
}

// fulfillmentReturnedPayload is the body of the event, built in ONE place for the
// outbox row and the direct publish.
func fulfillmentReturnedPayload(fulfillmentID, reference, returnID string, at time.Time) map[string]any {
	return map[string]any{
		EventFieldFulfillmentID: fulfillmentID,
		EventFieldReference:     reference,
		EventFieldReturnID:      returnID,
		EventFieldReturnedAt:    at.UTC().Format(time.RFC3339Nano),
	}
}

// recordFulfillmentReturned writes the event into the outbox INSIDE the caller's
// transaction.
//
// A failure fails the report, for [Service.recordFulfillmentCanceled]'s reason:
// a parcel marked come back without its event is written-off units that stay
// off the shelf with nothing saying they should not.
func (s *Service) recordFulfillmentReturned(
	ctx context.Context, fulfillmentID, reference, returnID string, at time.Time,
) error {
	if s.events == nil {
		// A service assembled by hand, with no bus and so no relay to keep the
		// promise a row would make.
		return nil
	}

	return s.store.WriteOutboxEvent(ctx,
		fulfillmentReturnedEventID(fulfillmentID), EventFulfillmentReturned,
		fulfillmentReturnedPayload(fulfillmentID, reference, returnID, at))
}

// publishFulfillmentReturned sends the event AFTER the transaction has committed.
//
// A failure is logged and swallowed: the report is recorded and the outbox row
// covers the loss.
func (s *Service) publishFulfillmentReturned(
	ctx context.Context, fulfillmentID, reference, returnID string, at time.Time,
) {
	if s.events == nil {
		return
	}

	err := s.events.Publish(ctx, eventbus.Event{
		ID:   fulfillmentReturnedEventID(fulfillmentID),
		Name: EventFulfillmentReturned,
		Data: fulfillmentReturnedPayload(fulfillmentID, reference, returnID, at),
	})
	if err != nil {
		s.log.ErrorContext(ctx,
			"the parcel come-back event could not be published; the parcel is marked come "+
				"back and written-off units it held stay off the shelf until the outbox relay "+
				"catches up",
			"event", EventFulfillmentReturned,
			"fulfillment_id", fulfillmentID,
			"error", err)
	}
}
