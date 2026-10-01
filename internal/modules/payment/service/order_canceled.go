package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// TopicOrderCanceled is the order module's event that an order was canceled
// (ADR 0288).
//
// The name is REPEATED as a literal for the reason the order module repeats
// this module's topics: neither module imports the other, and a rename on one
// side is caught by the arch gate that pairs every subscription with a
// publisher.
const TopicOrderCanceled = "order.canceled"

// eventFieldOrderID is the payload key naming the canceled order.
const eventFieldOrderID = "order_id"

// CodeOrderEventUnusable reports an order event this module cannot act on.
const CodeOrderEventUnusable = "payment_order_event_unusable"

// OrderLinks reads the Module Links this module answers an order's event
// through.
type OrderLinks interface {
	// List returns the ids the named link binds the source to.
	List(ctx context.Context, name, fromID string) ([]string, error)
}

// EventSubscriber is the narrow surface the module listens through. It is a
// separate interface from [EventPublisher] because only the module's
// registration subscribes; the service publishes.
type EventSubscriber interface {
	// Subscribe registers the handler for the named event.
	Subscribe(eventName string, h eventbus.Handler) error
}

// HandleOrderCanceled closes every session of the canceled order's
// collection that is still authorized (ADR 0288).
//
// A canceled order will capture nothing, so an authorization behind it is a
// promise the shop no longer waits for — an offline method's — or money held
// on a shopper's card. Both are closed through [Service.CancelPayment], under
// the locks a capture takes, so a capture and this close do not both happen.
//
// A capture that won that race leaves money on a canceled order; it is logged
// as an error and the other sessions are still closed, because retrying the
// event cannot undo a capture. An order bound to no collection — a saga that
// unwound before the binding — is nothing to do. A second delivery finds
// nothing authorized.
func (s *Service) HandleOrderCanceled(ctx context.Context, e eventbus.Event) error {
	orderID, _ := e.Data[eventFieldOrderID].(string)
	if orderID == "" {
		return errors.Invalid(CodeOrderEventUnusable,
			"the %q event carries no %s", e.Name, eventFieldOrderID)
	}
	if s.links == nil {
		return errors.Internal(CodeNotReady,
			"the links are not wired, so a canceled order's payment cannot be found: %s", orderID)
	}

	collections, err := s.links.List(ctx, LinkOrderPayment, orderID)
	if err != nil {
		return err
	}
	for _, collectionID := range collections {
		sessions, err := s.store.ListPaymentSessionsByCollection(ctx, collectionID)
		if err != nil {
			return err
		}
		for i := range sessions {
			if sessions[i].Status != models.SessionAuthorized {
				continue
			}
			err := s.CancelPayment(ctx, sessions[i].ID)
			if errors.IsConflict(err) {
				s.log.ErrorContext(ctx,
					"money was captured on an order that was canceled; it belongs to no live order "+
						"and has to be refunded",
					"order_id", orderID, "payment_session_id", sessions[i].ID, "error", err)
				continue
			}
			if err != nil {
				return err
			}
		}
	}

	return nil
}
