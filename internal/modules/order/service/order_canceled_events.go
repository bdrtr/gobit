package service

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// EventOrderCanceled says an order was canceled (ADR 0288).
//
// Both cancels publish it: the shop's ([Service.CancelPlacedOrder]) and the
// checkout's compensation ([Service.CancelOrder]). The order's creation
// publishes [EventOrderPlaced] inside the checkout, so a subscriber that heard
// that one hears this one whichever way the order ended — an order the saga
// unwound is not left looking placed to a webhook receiver, and the payment
// module closes what the order still holds authorized.
//
// It carries the order and the moment, not the reason: the reason is text an
// operator typed, and the event reaches a shop's third-party endpoints with no
// redaction rule for it.
//
// The name is a CROSS-MODULE CONTRACT, like [EventOrderPlaced].
const EventOrderCanceled = "order.canceled"

// orderCanceledEventID derives the event's id from the order. A cancel is
// terminal and a second one writes nothing, so an order is canceled once.
func orderCanceledEventID(orderID string) string {
	return EventOrderCanceled + ":" + orderID
}

// orderCanceledPayload is the body of the event, built in ONE place for
// [lineCanceledPayload]'s reason.
func orderCanceledPayload(order models.Order) map[string]any {
	canceledAt := ""
	if order.CanceledAt != nil {
		canceledAt = order.CanceledAt.UTC().Format(time.RFC3339Nano)
	}

	return map[string]any{
		EventFieldOrderID:    order.ID,
		EventFieldCanceledAt: canceledAt,
	}
}

// recordOrderCanceled writes the event into the outbox INSIDE the cancel's
// transaction; a failure fails the cancel, for [Service.recordLineCanceled]'s
// reason.
func (s *Service) recordOrderCanceled(ctx context.Context, order models.Order) error {
	return s.store.WriteOutboxEvent(ctx,
		orderCanceledEventID(order.ID), EventOrderCanceled, orderCanceledPayload(order))
}

// publishOrderCanceled sends the event AFTER the transaction has committed. A
// failure is logged and swallowed: the cancel is recorded and the outbox row
// covers the loss.
func (s *Service) publishOrderCanceled(ctx context.Context, order models.Order) {
	err := s.events.Publish(ctx, eventbus.Event{
		ID:   orderCanceledEventID(order.ID),
		Name: EventOrderCanceled,
		Data: orderCanceledPayload(order),
	})
	if err != nil {
		s.log.ErrorContext(ctx,
			"the order's cancel event could not be published; the order IS canceled and "+
				"what its payment holds stays authorized until the outbox relay catches up",
			"event", EventOrderCanceled,
			"order_id", order.ID,
			"error", err)
	}
}
