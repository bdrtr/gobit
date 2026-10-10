package service

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// EventOrderReturnOpened says a return was opened on an order (ADR 0437).
//
// [Service.CreateReturnRecord] publishes it, and the storefront, the admin API
// and the panel all open a return through that method. It carries the order,
// the return and the moment the database stamped the record with, and not the
// shopper's reason or note: those are the shopper's own words, and the event
// reaches a shop's third-party endpoints. A reader that wants the lines reads
// the return (ADR 0433).
//
// Nothing inside gobit subscribes to it. A consumer running beside gobit
// reported that it could not hear a return it did not open itself, and ADR
// 0437 names it in the arch gate's subscriberlessPublications.
//
// The name is a CROSS-MODULE CONTRACT, like [EventOrderPlaced].
const EventOrderReturnOpened = "order.return_opened"

// EventOrderReturnCanceled says a return was withdrawn (ADR 0437).
//
// [Service.CancelReturn] publishes it, for the admin API and the panel alike,
// so a reader that heard [EventOrderReturnOpened] hears how a return that
// brought nothing back ended. It carries the order, the return and the moment
// the database stamped the withdrawal with.
//
// The name is a CROSS-MODULE CONTRACT, like [EventOrderPlaced].
const EventOrderReturnCanceled = "order.return_canceled"

// The keys the return events carry beside [EventFieldOrderID] and
// [EventFieldCanceledAt].
const (
	// EventFieldReturnID is the return record.
	EventFieldReturnID = "return_id"
	// EventFieldOpenedAt is the moment the return was opened, RFC 3339 with
	// nanoseconds, UTC: the record's created_at.
	EventFieldOpenedAt = "opened_at"
)

// returnOpenedEventID derives the event's id from the RETURN: a return is
// opened once.
func returnOpenedEventID(returnID string) string {
	return EventOrderReturnOpened + ":" + returnID
}

// returnOpenedPayload is the body of the event, built in ONE place from the row
// the write returned, so the moment is the database's.
func returnOpenedPayload(ret models.Return) map[string]any {
	return map[string]any{
		EventFieldOrderID:  ret.OrderID,
		EventFieldReturnID: ret.ID,
		EventFieldOpenedAt: ret.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

// recordReturnOpened writes the event into the outbox INSIDE the opening's
// transaction; a failure fails the opening, so a return is never opened
// without the event that announces it.
func (s *Service) recordReturnOpened(ctx context.Context, ret models.Return) error {
	return s.store.WriteOutboxEvent(ctx,
		returnOpenedEventID(ret.ID), EventOrderReturnOpened, returnOpenedPayload(ret))
}

// publishReturnOpened sends the event AFTER the transaction has committed. A
// failure is logged and swallowed: the return is open and the outbox row covers
// the loss.
func (s *Service) publishReturnOpened(ctx context.Context, ret models.Return) {
	err := s.events.Publish(ctx, eventbus.Event{
		ID:   returnOpenedEventID(ret.ID),
		Name: EventOrderReturnOpened,
		Data: returnOpenedPayload(ret),
	})
	if err != nil {
		s.log.ErrorContext(ctx,
			"the return's opening event could not be published; the return IS open and the "+
				"outbox relay will send it",
			"event", EventOrderReturnOpened,
			"return_id", ret.ID,
			"error", err)
	}
}

// returnCanceledEventID derives the event's id from the RETURN: a withdrawal is
// terminal, and a second one finds the return canceled and writes nothing.
func returnCanceledEventID(returnID string) string {
	return EventOrderReturnCanceled + ":" + returnID
}

// returnCanceledPayload is the body of the event, built in ONE place from the
// row the withdrawal returned.
func returnCanceledPayload(ret models.Return) map[string]any {
	canceledAt := ""
	if ret.CanceledAt != nil {
		canceledAt = ret.CanceledAt.UTC().Format(time.RFC3339Nano)
	}

	return map[string]any{
		EventFieldOrderID:    ret.OrderID,
		EventFieldReturnID:   ret.ID,
		EventFieldCanceledAt: canceledAt,
	}
}

// recordReturnCanceled writes the event into the outbox INSIDE the
// withdrawal's transaction; a failure fails the withdrawal.
func (s *Service) recordReturnCanceled(ctx context.Context, ret models.Return) error {
	return s.store.WriteOutboxEvent(ctx,
		returnCanceledEventID(ret.ID), EventOrderReturnCanceled, returnCanceledPayload(ret))
}

// publishReturnCanceled sends the event AFTER the transaction has committed. A
// failure is logged and swallowed: the return is withdrawn and the outbox row
// covers the loss.
func (s *Service) publishReturnCanceled(ctx context.Context, ret models.Return) {
	err := s.events.Publish(ctx, eventbus.Event{
		ID:   returnCanceledEventID(ret.ID),
		Name: EventOrderReturnCanceled,
		Data: returnCanceledPayload(ret),
	})
	if err != nil {
		s.log.ErrorContext(ctx,
			"the return's withdrawal event could not be published; the return IS withdrawn and "+
				"the outbox relay will send it",
			"event", EventOrderReturnCanceled,
			"return_id", ret.ID,
			"error", err)
	}
}
