package service

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/eventbus"
)

// EventFulfillmentCanceled says a parcel will not be sent after all.
//
// # The first event this module has ever published
//
// Until ADR 0139 the fulfillment module published nothing. Its whole cross-module
// surface was READ or command, and a parcel being canceled was a row whose status
// changed and nothing else — no event, no subscriber, no trace outside this
// module's own table.
//
// That silence had a cost, and the cost was written down as the way out of a
// different problem. ADR 0135 refused to withdraw somebody's shipment when a line
// is canceled under it, on the ground that a framework must not decide what a shop
// has to, and it named the shop's own resolution: cancel the parcel. But canceling
// a parcel released units that a cancellation had already counted as GONE, and
// nothing recomputed anything — so the units were neither in a parcel, nor sold,
// nor on the shelf. The prescribed cure lost the goods (gap D75).
//
// # Why an event and not a call
//
// Putting the units back is an inventory write and this module may not make one
// (ADR 0006). It cannot ask the order module what was written off either. A flow
// above all three decides, and it hears about the cancellation here — the same
// shape the order module's line cancellation takes, for the same reason.
//
// The name is a CROSS-MODULE CONTRACT: on the Redis backend it is also the stream
// name, so changing it stops every subscriber silently.
const EventFulfillmentCanceled = "fulfillment.canceled"

// The keys this event carries.
//
// # Why it carries no quantities
//
// The order module's cancellation event carries figures because they are facts of
// an immutable row (ADR 0134). A parcel's items are the same kind of fact, but
// there can be many of them and an event is a flat map of strings: a list would
// have to travel as JSON inside one value, and a subscriber would be parsing a
// schema nothing checks.
//
// So this event carries IDENTITIES and the subscriber asks. The read it makes —
// [Interop.QuantitiesOfFulfillment] — answers about a canceled parcel on purpose,
// which is the one thing [Interop.CommittedQuantities] will not do.
const (
	// EventFieldFulfillmentID is the parcel that was canceled.
	EventFieldFulfillmentID = "fulfillment_id"
	// EventFieldReference is what the parcel was opened FOR, verbatim.
	//
	// It is the order identifier in every flow this repository ships, and it is
	// still free text this module never validates (Principle 2.2). A subscriber
	// that needs the order must read the "order_fulfillment" LINK rather than
	// this field; it is carried so an operator reading a forwarded webhook sees
	// what the parcel was for without a second lookup.
	EventFieldReference = "reference"
	// EventFieldCanceledAt is the moment, RFC 3339 with nanoseconds, UTC.
	EventFieldCanceledAt = "canceled_at"
)

// fulfillmentCanceledEventID derives the event's id from the PARCEL.
//
// A parcel can be canceled once: the second call finds the row already canceled
// and returns before reaching here, so keying on the fulfillment cannot make two
// distinct acts look like one. The outbox writes with `ON CONFLICT (id) DO
// NOTHING`, which is what makes a retried transaction write one row.
func fulfillmentCanceledEventID(fulfillmentID string) string {
	return EventFulfillmentCanceled + ":" + fulfillmentID
}

// fulfillmentCanceledPayload is the body of the event, built in ONE place.
//
// Both the outbox row and the direct publish use it, which is what makes them one
// event rather than two that can drift apart.
func fulfillmentCanceledPayload(fulfillmentID, reference string, at time.Time) map[string]any {
	return map[string]any{
		EventFieldFulfillmentID: fulfillmentID,
		EventFieldReference:     reference,
		EventFieldCanceledAt:    at.UTC().Format(time.RFC3339Nano),
	}
}

// recordFulfillmentCanceled writes the event into the outbox INSIDE the caller's
// transaction.
//
// A failure here fails the cancellation, and that is the right trade: a parcel
// canceled without its promised event is stock that stays deducted forever with
// nothing anywhere saying it should not be. The outbox exists to prevent exactly
// that, and accepting the loss quietly would leave the guarantee looking present
// while it was not.
func (s *Service) recordFulfillmentCanceled(
	ctx context.Context, fulfillmentID, reference string, at time.Time,
) error {
	if s.events == nil {
		// Unreachable through the module, which refuses to register without a bus.
		// A service assembled by hand can get here, and the safe reading of "no
		// publisher" is "write no row": with no bus there is no relay to keep the
		// promise a row would make.
		return nil
	}

	return s.store.WriteOutboxEvent(ctx,
		fulfillmentCanceledEventID(fulfillmentID), EventFulfillmentCanceled,
		fulfillmentCanceledPayload(fulfillmentID, reference, at))
}

// publishFulfillmentCanceled sends the event AFTER the transaction has committed.
//
// A failure is logged and swallowed: the cancellation is already recorded and the
// outbox row covers the loss — the relay publishes what this call missed. This
// stays as the FAST path so the units are usually back in the same request rather
// than up to a minute later.
func (s *Service) publishFulfillmentCanceled(
	ctx context.Context, fulfillmentID, reference string, at time.Time,
) {
	if s.events == nil {
		return
	}

	err := s.events.Publish(ctx, eventbus.Event{
		ID:   fulfillmentCanceledEventID(fulfillmentID),
		Name: EventFulfillmentCanceled,
		Data: fulfillmentCanceledPayload(fulfillmentID, reference, at),
	})
	if err != nil {
		s.log.ErrorContext(ctx,
			"the parcel cancellation event could not be published; the parcel is canceled "+
				"and the units it held ARE STILL DEDUCTED until the outbox relay catches up",
			"event", EventFulfillmentCanceled,
			"fulfillment_id", fulfillmentID,
			"error", err)
	}
}
