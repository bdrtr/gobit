package service

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	coreprovider "github.com/bdrtr/gobit/core/provider"
)

// This file holds the subscribers that TRIGGER the notifications.
//
// # Error policy: an error IS RETURNED, but that is NOT a "retry" request
//
// The decision and its reasoning are in the file documentation of
// plugins/searchpg/events.go; they are not repeated here. The summary:
// [eventbus.EventBus] DOES NOT REDELIVER a handler that returns an error, it
// counts the event as processed and logs the error at the ERROR level —
// therefore returning the error is not asking for a retry, it is making the
// fault VISIBLE.
//
// The handler does not retry inside itself either, and there is a second reason
// for that here: trying the same notification again can produce a SECOND e-mail
// because of the chance that the provider processed the first attempt (see the
// [Service.Notify] documentation).
//
// # The handler is IDEMPOTENT
//
// The contract does not guarantee ordering and the InMemory backend can call
// the same handler concurrently. The uniqueness here does not rest on the code
// but on the (template, reference) uniqueness in the delivery log: processing
// the same event twice and processing it once produce the SAME number of
// notifications.

// EventOrderPlaced is the name of the order event that is listened for (the
// SAME value as the EventOrderPlaced constant of the order service).
//
// The name is a CROSS-MODULE CONTRACT and it is repeated by hand; modules
// cannot import each other (Principle 2.4). The price of divergence is silent:
// if the name changes this subscriber receives no event and produces no error
// either — nobody notices that they are not receiving anything. The agreement
// is proven by the integration test.
const EventOrderPlaced = "order.placed"

// TemplateOrderPlaced is the name of the order confirmation template.
//
// Choosing it the SAME as the event name is deliberate (see the
// [coreprovider.Notification] documentation in the core): two separate names
// would make the question "which event triggers which template" answerable only
// by reading the code. The name is at the same time half of the idempotency
// key — its changing means the notification becoming sendable A SECOND TIME for
// all orders.
const TemplateOrderPlaced = EventOrderPlaced

// eventFieldOrderID is the ONLY field read from the event payload.
//
// Everything else the template needs is read from the RECORD the identifier
// points at (see [Service.OrderPlaced]).
const eventFieldOrderID = "order_id"

// The data keys passed to the template.
//
// The names are exactly the same as the field names of the order surface;
// translating them would have meant the same data traveling under two names
// and the template author not knowing which one is the right one.
const (
	dataKeyOrderID      = "order_id"
	dataKeyDisplayID    = "display_id"
	dataKeyCurrencyCode = "currency_code"
	dataKeyTotal        = "total"
	dataKeyItemCount    = "item_count"
)

// EventSubscriber is the NARROW surface the module wants from the event bus.
//
// The module only SUBSCRIBES: it does not publish and does not close the bus.
// Depending on the whole of [eventbus.EventBus] would have meant giving the
// module the authority to close it too; the lifetime of the bus is managed by
// the composition root.
type EventSubscriber interface {
	Subscribe(eventName string, h eventbus.Handler) error
}

// OrderPlaced handles the "order.placed" event: it reads the contact
// information of the order and sends the order confirmation notification.
//
// # The e-mail is read from the RECORD, not from the EVENT
//
// The e-mail is DELIBERATELY absent from the event payload: events are written
// to Redis and are durable there; putting personal data into a durable stream
// is an unnecessary spread for information that already sits on the order
// itself (the order module's event documentation). That is why this handler
// takes ONLY the order identifier from the event and reads the rest over
// "order.interop".
//
// The reading has a second benefit as well: the event payload can be STALE (the
// bus gives no ordering guarantee), whereas the record gives the truth at that
// moment.
func (s *Service) OrderPlaced(ctx context.Context, e eventbus.Event) error {
	orderID, err := eventOrderID(e)
	if err != nil {
		return err
	}

	raw, err := s.contacts.OrderContactJSON(ctx, orderID)
	if err != nil {
		// The KIND is PRESERVED: when the order cannot be found NotFound comes,
		// when the surface is absent Unavailable comes, and the two are
		// different faults.
		return errors.Wrap(err, errors.KindOf(err), CodeContactUnavailable,
			"the order contact information for the %q event could not be read: %s", e.Name, orderID)
	}

	contact, err := decodeContact(raw, orderID)
	if err != nil {
		return err
	}

	return s.Notify(ctx, NotifyInput{
		Template:  TemplateOrderPlaced,
		Channel:   coreprovider.ChannelEmail,
		Reference: orderID,
		To:        contact.Email,
		Data: map[string]string{
			dataKeyOrderID:      contact.OrderID,
			dataKeyDisplayID:    contact.DisplayID,
			dataKeyCurrencyCode: contact.CurrencyCode,
			dataKeyTotal:        contact.Total,
			dataKeyItemCount:    contact.ItemCount,
		},
	})
}

// eventOrderID reads the order identifier from the event payload.
//
// The value being a STRING is the contract (see order service/events.go):
// because the Redis backend turns the payload into JSON a numeric field reaches
// the subscriber as a float64, and the "every value is a string" rule exists
// precisely to prevent that. If the type does not match, silently carrying on
// with an empty identifier would produce a notification attempt for an order
// that never existed; returning an error makes the breaking of the contract
// visible in the log.
func eventOrderID(e eventbus.Event) (string, error) {
	raw, ok := e.Data[eventFieldOrderID]
	if !ok {
		return "", errors.Invalid(CodeEventInvalid,
			"the payload of the %q event has no %q field", e.Name, eventFieldOrderID)
	}

	id, ok := raw.(string)
	if !ok {
		return "", errors.Invalid(CodeEventInvalid,
			"the %q field in the %q event has to be a string (incoming type: %T)",
			e.Name, eventFieldOrderID, raw)
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.Invalid(CodeEventInvalid,
			"the %q field in the %q event is empty", e.Name, eventFieldOrderID)
	}
	return id, nil
}

// decodeContact decodes the response of the order surface.
//
// An empty order_id is the sign that the schema of the surface has changed and
// it produces an ERROR: carrying on with a body that has no identifier would
// have meant filling the template with empty fields and sending it to the
// customer. The e-mail being empty, on the other hand, is NOT an error; that
// decision is made by [Service.Notify].
func decodeContact(raw json.RawMessage, orderID string) (orderContact, error) {
	var contact orderContact
	if err := json.Unmarshal(raw, &contact); err != nil {
		return orderContact{}, errors.Wrap(err, errors.KindInternal, CodeContactInvalid,
			"the order contact response could not be decoded (%s); the schema of the %q surface may have changed",
			orderID, OrderInteropName)
	}
	if strings.TrimSpace(contact.OrderID) == "" {
		return orderContact{}, errors.Internal(CodeContactInvalid,
			"the order contact response has no %q field (%s); the schema of the %q surface may have changed",
			"order_id", orderID, OrderInteropName)
	}
	return contact, nil
}
