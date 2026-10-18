package stockalert

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
)

// EventBackInStock says a variant a customer marked is back in stock and
// their mail was handed to the notification module (ADR 0448). It is published
// before the mark is cleared, so a pass that stops between the two publishes
// it again under the same id; nothing inside gobit subscribes to it.
//
// It is spelled as the mail's template is: the mail and the event are one
// fact.
const EventBackInStock = "wishlist.back_in_stock"

// The fields of [EventBackInStock]. Every value is a string; the moment is
// the mark's armed_at, in UTC, in time.RFC3339Nano, which with the customer
// and the variant names the alert.
const (
	EventFieldCustomerID = "customer_id"
	EventFieldVariantID  = "variant_id"
	EventFieldProductID  = "product_id"
	EventFieldArmedAt    = "armed_at"
)

// ServiceEvents is the bus the flow publishes on.
const ServiceEvents = "core.eventbus"

// Publisher is the slice of the bus this flow calls.
type Publisher interface {
	Publish(ctx context.Context, event eventbus.Event) error
}

// WithEvents gives the flow the bus its back-in-stock event goes out on; a
// flow without one publishes nothing.
func (w *Workflow) WithEvents(events Publisher) *Workflow {
	w.events = events

	return w
}

// backInStockEventID names the alert by a digest of its mail's reference, so
// the event's two publishes are one event and its id carries no customer id
// to a receiver the field is withheld from.
func backInStockEventID(reference string) string {
	sum := sha256.Sum256([]byte(reference))

	return EventBackInStock + ":" + hex.EncodeToString(sum[:16])
}

// publishBackInStock announces one answered mark. A failure is returned, and
// the mark stays armed for the next pass, whose mail is the notification
// module's repeat and whose event is this one again.
func (w *Workflow) publishBackInStock(ctx context.Context, a *alert, productID, reference string) error {
	if w.events == nil {
		return nil
	}
	err := w.events.Publish(ctx, eventbus.Event{
		ID:   backInStockEventID(reference),
		Name: EventBackInStock,
		Data: backInStockPayload(a, productID),
	})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), CodeNotReady,
			"the back-in-stock event for variant %s could not be published", a.VariantID)
	}

	return nil
}

// backInStockPayload is the body of [EventBackInStock].
func backInStockPayload(a *alert, productID string) map[string]any {
	return map[string]any{
		EventFieldCustomerID: a.CustomerID,
		EventFieldVariantID:  a.VariantID,
		EventFieldProductID:  productID,
		EventFieldArmedAt:    a.ArmedAt.UTC().Format(time.RFC3339Nano),
	}
}
