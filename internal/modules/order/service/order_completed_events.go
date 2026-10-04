package service

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// EventOrderCompleted says the shop finished with an order (ADR 0386).
//
// [Service.CompleteOrder] publishes it, and the admin API, the panel and the
// interop surface all complete through that method. It carries the order and
// the moment, as [EventOrderCanceled] does; a subscriber reads anything else
// from the order, as the notification module reads the address. Archiving
// publishes nothing (ADR 0386, ADR 0063).
//
// The name is a CROSS-MODULE CONTRACT, like [EventOrderPlaced].
const EventOrderCompleted = "order.completed"

// EventFieldCompletedAt is the moment the order was completed, in UTC, in
// time.RFC3339Nano.
const EventFieldCompletedAt = "completed_at"

// orderCompletedEventID derives the event's id from the order. Only a pending
// order is completed and a completed one never is again, so an order is
// completed once.
func orderCompletedEventID(orderID string) string {
	return EventOrderCompleted + ":" + orderID
}

// orderCompletedPayload is the body of the event, built in ONE place so the
// outbox row and the direct publish cannot disagree.
func orderCompletedPayload(order models.Order) map[string]any {
	completedAt := ""
	if order.CompletedAt != nil {
		completedAt = order.CompletedAt.UTC().Format(time.RFC3339Nano)
	}

	return map[string]any{
		EventFieldOrderID:     order.ID,
		EventFieldCompletedAt: completedAt,
	}
}

// recordOrderCompleted writes the event into the outbox INSIDE the
// completion's transaction; a failure fails the completion, for
// [Service.recordLineCanceled]'s reason.
func (s *Service) recordOrderCompleted(ctx context.Context, order models.Order) error {
	return s.store.WriteOutboxEvent(ctx,
		orderCompletedEventID(order.ID), EventOrderCompleted, orderCompletedPayload(order))
}

// publishOrderCompleted sends the event AFTER the transaction has committed. A
// failure is logged and swallowed: the completion is recorded and the outbox
// row covers the loss.
func (s *Service) publishOrderCompleted(ctx context.Context, order models.Order) {
	err := s.events.Publish(ctx, eventbus.Event{
		ID:   orderCompletedEventID(order.ID),
		Name: EventOrderCompleted,
		Data: orderCompletedPayload(order),
	})
	if err != nil {
		s.log.ErrorContext(ctx,
			"the order's completion event could not be published; the order IS completed and "+
				"the outbox relay will send it",
			"event", EventOrderCompleted,
			"order_id", order.ID,
			"error", err)
	}
}
