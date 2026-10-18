package service

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// EventOrderTransferred says an order changed hands (ADR 0450).
//
// An accepted storefront request and an operator's move publish it, once for
// the order and once for every order that adds to it and moved with it. It
// carries the order, the transfer and the moment the transfer was closed, and
// no customer and no address: a subscriber reads who holds the order now from
// the order, which is the truth when the bus delivers two transfers out of
// order. A reader keeping a copy of an order's customer reads it again.
//
// Nothing inside gobit subscribes to it. A marketplace running beside gobit
// copies an order's customer into its own analytics, and ADR 0450 names it in
// the arch gate's subscriberlessPublications.
//
// The name is a CROSS-MODULE CONTRACT, like [EventOrderPlaced].
const EventOrderTransferred = "order.transferred"

// The keys the transfer event carries beside [EventFieldOrderID].
const (
	// EventFieldTransferID is the transfer that moved the order.
	EventFieldTransferID = "transfer_id"
	// EventFieldTransferredAt is the moment the transfer was accepted, RFC 3339
	// with nanoseconds, UTC: the transfer's closed_at.
	EventFieldTransferredAt = "transferred_at"
)

// orderTransferredEventID derives the event's id from the transfer and the
// order it moved: a transfer is accepted once and moves each order once.
func orderTransferredEventID(transferID, orderID string) string {
	return EventOrderTransferred + ":" + transferID + ":" + orderID
}

// orderTransferredPayload is the body of the event, built in ONE place from
// the row the acceptance returned, so the moment is the database's.
func orderTransferredPayload(transfer models.OrderTransfer, orderID string) map[string]any {
	transferredAt := ""
	if transfer.ClosedAt != nil {
		transferredAt = transfer.ClosedAt.UTC().Format(time.RFC3339Nano)
	}

	return map[string]any{
		EventFieldOrderID:       orderID,
		EventFieldTransferID:    transfer.ID,
		EventFieldTransferredAt: transferredAt,
	}
}

// recordOrderTransferred writes one event per moved order into the outbox
// INSIDE the move's transaction; a failure fails the move.
func (s *Service) recordOrderTransferred(ctx context.Context, transfer models.OrderTransfer, moved []string) error {
	for _, orderID := range moved {
		err := s.store.WriteOutboxEvent(ctx, orderTransferredEventID(transfer.ID, orderID),
			EventOrderTransferred, orderTransferredPayload(transfer, orderID))
		if err != nil {
			return err
		}
	}

	return nil
}

// publishOrderTransferred sends the events AFTER the transaction has
// committed. A failure is logged and swallowed: the order moved and the
// outbox rows cover the loss.
func (s *Service) publishOrderTransferred(ctx context.Context, transfer models.OrderTransfer, moved []string) {
	for _, orderID := range moved {
		err := s.events.Publish(ctx, eventbus.Event{
			ID:   orderTransferredEventID(transfer.ID, orderID),
			Name: EventOrderTransferred,
			Data: orderTransferredPayload(transfer, orderID),
		})
		if err != nil {
			s.log.ErrorContext(ctx,
				"the order's transfer event could not be published; the order DID move and the "+
					"outbox relay will send it",
				"event", EventOrderTransferred,
				"order_id", orderID,
				"transfer_id", transfer.ID,
				"error", err)
		}
	}
}
