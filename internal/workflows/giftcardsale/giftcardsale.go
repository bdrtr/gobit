// Package giftcardsale issues the gift cards an order sold, once its money is
// in, and mails each card's code to the order's address (ADR 0210).
//
// It is a workflow because it combines three modules (ADR 0006): the payment
// module says the money is in and keeps the cards, the order module holds the
// lines, which say which of them sold gift cards (ADR 0211), and the address,
// and the notification module sends the code. None of them imports another,
// and this package imports none of them: each is reached by name through the
// narrow interface declared here.
//
// # The line, not the catalog
//
// The order's books count a line as a gift card by its own flag, copied from
// the product when the order was placed (ADR 0211). The flow reads the same
// flag, so a line booked as a card is the line that issues one, whatever the
// catalog says about the product afterwards.
//
// # At least once, and once
//
// The flow runs on payment.captured, which the outbox publishes at least once. A
// card is issued under the name of the sale — the order line and the unit — so
// a second delivery finds the card the first one made and gets no code back.
// Only the call that made a card holds its code, and only that call mails it.
//
// A handler's error is logged and the event is not delivered again
// (docs/extending.md), and a process that stops while a handler runs loses the
// event too. [Workflow.Sweep] is the second way in: a scheduled job walks the
// gift card lines of recent orders and issues what no delivery did (ADR 0212).
// Both ways issue through the same door, so a card is made once whichever
// arrives first, and neither issues for a canceled order.
//
// # A code that did not arrive
//
// The code is mailed once and kept nowhere (ADR 0208). If the mail fails, or
// the process stops between the card and the mail, the code is lost and the
// card is not: an operator gives the card a new code (ADR 0210), and the
// notification log shows which mail did not go. The sweep cannot help there,
// since the card exists.
package giftcardsale

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/query"
)

// The container names this flow resolves, spelled by hand because it imports
// no module; internal/arch pins the surfaces behind them.
const (
	ServicePayments      = "payment.interop"
	ServiceOrders        = "order.interop"
	ServiceNotifications = "notification.interop"
	ServiceQuery         = "core.query"
	ServiceLink          = "core.link"
	ServiceEventBus      = "core.eventbus"
)

// The cross-module names the flow reads, spelled by hand for the same reason.
const (
	// TopicPaymentCaptured is the payment module's capture event, and
	// FieldCollectionID the one field of it the flow reads.
	TopicPaymentCaptured = "payment.captured"
	FieldCollectionID    = "payment_collection_id"
	// LinkOrderPayment binds an order to the collection its checkout opened.
	LinkOrderPayment = "order_payment"
	// EntityLineItem is the read layer's order line, FieldIsGiftcard its flag,
	// FieldOrderID its order and FilterPlacedFrom the lower bound of its
	// order's placement.
	EntityLineItem   = "order_line_item"
	FieldIsGiftcard  = "is_giftcard"
	FieldOrderID     = "order_id"
	FilterPlacedFrom = "placed_from"
	// EntityOrder is the read layer's order, FieldStatus its status and
	// StatusCanceled the status of a canceled one.
	EntityOrder    = "order"
	FieldStatus    = "status"
	StatusCanceled = "canceled"
	// TemplateIssued is the notification a sold card's code is mailed with.
	TemplateIssued = "gift_card.issued"
	// ChannelEmail is the notification channel.
	ChannelEmail = "email"
)

// The template's data keys. Every value is a string (core/provider's rule).
const (
	DataCode         = "code"
	DataAmount       = "amount"
	DataCurrencyCode = "currency_code"
	DataOrderID      = "order_id"
)

// linePage is how many lines are read at a time.
const linePage = 100

// referenceBatch is how many sales one read of the issued cards names.
const referenceBatch = 500

// Error codes.
const (
	// CodeNotReady reports a flow that could not be wired or a record it could
	// not read.
	CodeNotReady = "gift_card_sale_not_ready"
	// CodeSweepIncomplete reports a sweep that could not issue for every order
	// it found.
	CodeSweepIncomplete = "gift_card_sale_sweep_incomplete"
)

// Payments is the slice of the payment module this flow calls.
type Payments interface {
	// Collection reads a collection's amounts.
	Collection(ctx context.Context, collectionID string) (
		status string, amount, authorized, captured, refunded int64, err error)
	// IssueSoldGiftCard issues the card a sale made; the code is empty when
	// the card already existed.
	IssueSoldGiftCard(ctx context.Context, reference, orderID, currencyCode string, amount int64) (
		cardID, code string, err error)
	// SoldGiftCardReferences returns which of the sales already made their
	// card.
	SoldGiftCardReferences(ctx context.Context, references []string) ([]string, error)
}

// Orders is the slice of the order module this flow calls.
type Orders interface {
	// OrderContactJSON returns the order's address and currency, as JSON.
	OrderContactJSON(ctx context.Context, orderID string) (json.RawMessage, error)
}

// Notifier is the slice of the notification module this flow calls.
type Notifier interface {
	// Send delivers one message; (template, reference) is its idempotency key.
	Send(ctx context.Context, template, channel, reference, to string, data map[string]string) error
}

// Reader is the read layer.
type Reader interface {
	// Graph reads records of an entity.
	Graph(ctx context.Context, spec query.GraphSpec) ([]query.Record, error)
}

// Links reads the Module Links this flow needs.
type Links interface {
	// ListMany returns the links of the given source ids.
	ListMany(ctx context.Context, name string, fromIDs []string) (map[string][]string, error)
	// ListManyByTo returns the links of the given target ids.
	ListManyByTo(ctx context.Context, name string, toIDs []string) (map[string][]string, error)
}

// Subscriber is the narrow surface this flow needs to listen.
type Subscriber interface {
	// Subscribe registers the handler for the named event.
	Subscribe(eventName string, h eventbus.Handler) error
}

// Workflow is the wired flow.
type Workflow struct {
	payments Payments
	orders   Orders
	notifier Notifier
	reader   Reader
	links    Links
	log      *slog.Logger
}

// New builds the flow from resolved dependencies.
func New(payments Payments, orders Orders, notifier Notifier, reader Reader, links Links, log *slog.Logger) *Workflow {
	if log == nil {
		log = slog.Default()
	}

	return &Workflow{payments: payments, orders: orders, notifier: notifier, reader: reader, links: links, log: log}
}

// HandleCaptured issues the cards an order sold once its collection is paid.
//
// A collection captured in part is left alone: a split payment captures twice
// (ADR 0209), and the capture that completes it announces again. A collection no
// order was placed through — a delivery change, an exchange's difference — is
// left alone too, since the link names only a checkout's.
func (w *Workflow) HandleCaptured(ctx context.Context, e eventbus.Event) error {
	collectionID, _ := e.Data[FieldCollectionID].(string)
	if collectionID == "" {
		w.log.WarnContext(ctx, "a capture event without a collection was ignored", "event", e.ID)

		return nil
	}

	paid, err := w.paid(ctx, []string{collectionID})
	if err != nil || !paid {
		return err
	}

	orders, err := w.links.ListManyByTo(ctx, LinkOrderPayment, []string{collectionID})
	if err != nil {
		return err
	}
	for _, orderID := range orders[collectionID] {
		lines, err := w.giftCardLines(ctx, orderID)
		if err != nil {
			return err
		}
		if _, err := w.issue(ctx, orderID, lines); err != nil {
			return err
		}
	}

	return nil
}

// Sweep issues the cards that the orders placed since the given moment sold
// and that no delivery issued (ADR 0212).
//
// It reads the gift card lines of those orders, asks the payment module which
// of their sales already made a card, and takes only the orders missing one
// through the capture's checks: a collection captured in full, and an order
// that is not canceled. It reports how many orders it issued for, how many
// cards it made, and how many orders still wait for their capture.
//
// An order that fails is reported and does not stop the others.
func (w *Workflow) Sweep(ctx context.Context, since time.Time) (orders, issued, waiting int, err error) {
	sold, orderIDs, err := w.soldSince(ctx, since)
	if err != nil {
		return 0, 0, 0, err
	}
	missing, err := w.missingCards(ctx, orderIDs, sold)
	if err != nil || len(missing) == 0 {
		return 0, 0, 0, err
	}
	collections, err := w.links.ListMany(ctx, LinkOrderPayment, missing)
	if err != nil {
		return 0, 0, 0, err
	}

	var first error
	failed := 0
	for _, orderID := range missing {
		paid, err := w.paid(ctx, collections[orderID])
		if err == nil && !paid {
			waiting++

			continue
		}
		made := 0
		if err == nil {
			made, err = w.issue(ctx, orderID, sold[orderID])
		}
		issued += made
		if err != nil {
			failed++
			if first == nil {
				first = err
			}
			w.log.ErrorContext(ctx, "a sold gift card could not be issued by the sweep",
				"order_id", orderID, "error", err)

			continue
		}
		if made > 0 {
			orders++
		}
	}
	if first != nil {
		return orders, issued, waiting, errors.Wrap(first, errors.KindOf(first), CodeSweepIncomplete,
			"%d of %d orders missing a gift card could not be swept", failed, len(missing))
	}

	return orders, issued, waiting, nil
}

// soldLine is one of an order's lines that sold gift cards.
type soldLine struct {
	id        string
	quantity  int64
	unitPrice int64
}

// reference names one card a line sold: the line and the unit.
func (l soldLine) reference(unit int64) string { return fmt.Sprintf("%s:%d", l.id, unit) }

// paid reports whether one of the collections is captured in full.
func (w *Workflow) paid(ctx context.Context, collectionIDs []string) (bool, error) {
	for _, collectionID := range collectionIDs {
		_, amount, _, captured, _, err := w.payments.Collection(ctx, collectionID)
		if err != nil {
			return false, err
		}
		if captured >= amount {
			return true, nil
		}
	}

	return false, nil
}

// issue issues and mails the cards one order sold, and reports how many it
// made. It is the door both the capture and the sweep go through.
func (w *Workflow) issue(ctx context.Context, orderID string, lines []soldLine) (int, error) {
	if len(lines) == 0 {
		return 0, nil
	}
	canceled, err := w.canceled(ctx, orderID)
	if err != nil {
		return 0, err
	}
	if canceled {
		w.log.InfoContext(ctx, "a canceled order's gift cards are not issued", "order_id", orderID)

		return 0, nil
	}

	raw, err := w.orders.OrderContactJSON(ctx, orderID)
	if err != nil {
		return 0, err
	}
	var contact struct {
		Email        string `json:"email"`
		CurrencyCode string `json:"currency_code"`
	}
	if err := json.Unmarshal(raw, &contact); err != nil {
		return 0, errors.Wrap(err, errors.KindInternal, CodeNotReady, "the contact of order %s could not be read", orderID)
	}

	made := 0
	for _, line := range lines {
		for unit := int64(1); unit <= line.quantity; unit++ {
			// The card's value is the line's unit price, before any discount
			// (ADR 0210).
			cardID, code, err := w.payments.IssueSoldGiftCard(ctx,
				line.reference(unit), orderID, contact.CurrencyCode, line.unitPrice)
			if err != nil {
				return made, err
			}
			if code == "" {
				// An earlier delivery made this card and held its code.
				continue
			}
			made++
			if err := w.notifier.Send(ctx, TemplateIssued, ChannelEmail, cardID, contact.Email, map[string]string{
				DataCode: code, DataAmount: fmt.Sprint(line.unitPrice),
				DataCurrencyCode: contact.CurrencyCode, DataOrderID: orderID,
			}); err != nil {
				w.log.ErrorContext(ctx, "a sold gift card's code could not be mailed; give the card a new code",
					"gift_card", cardID, "order_id", orderID, "error", err)

				return made, err
			}
		}
	}

	return made, nil
}

// canceled reports whether the order is canceled.
func (w *Workflow) canceled(ctx context.Context, orderID string) (bool, error) {
	records, err := w.reader.Graph(ctx, query.GraphSpec{
		Entity: EntityOrder, Fields: []string{query.IDField, FieldStatus},
		Filters: map[string]any{query.IDField: orderID}, Limit: 1,
	})
	if err != nil {
		return false, err
	}
	if len(records) == 0 {
		return false, errors.NotFound(CodeNotReady, "order %s could not be read", orderID)
	}
	status, _ := records[0][FieldStatus].(string)

	return status == StatusCanceled, nil
}

// giftCardLines reads an order's lines and keeps the ones that sold gift
// cards.
func (w *Workflow) giftCardLines(ctx context.Context, orderID string) ([]soldLine, error) {
	var out []soldLine
	for offset := 0; ; offset += linePage {
		page, err := w.reader.Graph(ctx, query.GraphSpec{
			Entity:  EntityLineItem,
			Fields:  []string{query.IDField, "quantity", "unit_price", FieldIsGiftcard},
			Filters: map[string]any{FieldOrderID: orderID},
			Limit:   linePage, Offset: offset,
		})
		if err != nil {
			return nil, err
		}
		for _, record := range page {
			giftcard, ok := record[FieldIsGiftcard].(bool)
			if !ok {
				return nil, errors.Internal(CodeNotReady, "a line of order %s could not be read", orderID)
			}
			if !giftcard {
				continue
			}
			line, err := readSoldLine(record, orderID)
			if err != nil {
				return nil, err
			}
			out = append(out, line)
		}
		if len(page) < linePage {
			return out, nil
		}
	}
}

// soldSince reads the gift card lines of the orders placed since the moment,
// by order, and the orders in the order they were first met.
//
// The read layer pages newest first, so an order placed while the sweep reads
// pushes the rest back a row: a line can be read twice and is kept once, and
// none is skipped.
func (w *Workflow) soldSince(
	ctx context.Context, since time.Time,
) (sold map[string][]soldLine, orderIDs []string, err error) {
	sold = map[string][]soldLine{}
	seen := map[string]bool{}
	for offset := 0; ; offset += linePage {
		page, err := w.reader.Graph(ctx, query.GraphSpec{
			Entity:  EntityLineItem,
			Fields:  []string{query.IDField, FieldOrderID, "quantity", "unit_price"},
			Filters: map[string]any{FieldIsGiftcard: true, FilterPlacedFrom: since},
			Limit:   linePage, Offset: offset,
		})
		if err != nil {
			return nil, nil, err
		}
		for _, record := range page {
			orderID, _ := record[FieldOrderID].(string)
			line, err := readSoldLine(record, orderID)
			if err != nil {
				return nil, nil, err
			}
			if orderID == "" || seen[line.id] {
				continue
			}
			seen[line.id] = true
			if _, known := sold[orderID]; !known {
				orderIDs = append(orderIDs, orderID)
			}
			sold[orderID] = append(sold[orderID], line)
		}
		if len(page) < linePage {
			return sold, orderIDs, nil
		}
	}
}

// missingCards returns the orders one of whose sales made no card yet.
func (w *Workflow) missingCards(
	ctx context.Context, orderIDs []string, sold map[string][]soldLine,
) ([]string, error) {
	var references []string
	for _, orderID := range orderIDs {
		for _, line := range sold[orderID] {
			for unit := int64(1); unit <= line.quantity; unit++ {
				references = append(references, line.reference(unit))
			}
		}
	}
	issued := map[string]bool{}
	for start := 0; start < len(references); start += referenceBatch {
		found, err := w.payments.SoldGiftCardReferences(ctx,
			references[start:min(start+referenceBatch, len(references))])
		if err != nil {
			return nil, err
		}
		for _, reference := range found {
			issued[reference] = true
		}
	}

	var missing []string
	for _, orderID := range orderIDs {
		complete := true
		for _, line := range sold[orderID] {
			for unit := int64(1); unit <= line.quantity && complete; unit++ {
				complete = issued[line.reference(unit)]
			}
		}
		if !complete {
			missing = append(missing, orderID)
		}
	}

	return missing, nil
}

// readSoldLine reads a gift card line's identity, quantity and unit price.
func readSoldLine(record query.Record, orderID string) (soldLine, error) {
	id, _ := record[query.IDField].(string)
	quantity, quantityOK := number(record["quantity"])
	price, priceOK := number(record["unit_price"])
	if id == "" || !quantityOK || !priceOK {
		return soldLine{}, errors.Internal(CodeNotReady, "a gift card line of order %s could not be read", orderID)
	}

	return soldLine{id: id, quantity: quantity, unitPrice: price}, nil
}

// number reads an integer a provider wrote as int, int32 or int64.
func number(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	default:
		return 0, false
	}
}

// SweeperFromContainer resolves the flow's dependencies without subscribing
// it; the sweep's job is built from it (ADR 0212).
func SweeperFromContainer(c *container.Container, log *slog.Logger) (*Workflow, error) {
	if c == nil {
		return nil, errors.Internal(CodeNotReady, "the gift card sale flow cannot be wired without a container")
	}
	payments, err := resolve[Payments](c, ServicePayments)
	if err != nil {
		return nil, err
	}
	orders, err := resolve[Orders](c, ServiceOrders)
	if err != nil {
		return nil, err
	}
	notifier, err := resolve[Notifier](c, ServiceNotifications)
	if err != nil {
		return nil, err
	}
	reader, err := resolve[Reader](c, ServiceQuery)
	if err != nil {
		return nil, err
	}
	links, err := resolve[Links](c, ServiceLink)
	if err != nil {
		return nil, err
	}

	return New(payments, orders, notifier, reader, links, log), nil
}

// FromContainer builds the flow and subscribes it to the capture event.
func FromContainer(c *container.Container, log *slog.Logger) (*Workflow, error) {
	w, err := SweeperFromContainer(c, log)
	if err != nil {
		return nil, err
	}
	bus, err := resolve[Subscriber](c, ServiceEventBus)
	if err != nil {
		return nil, err
	}
	if err := bus.Subscribe(TopicPaymentCaptured, w.HandleCaptured); err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeNotReady,
			"the gift card sale flow could not subscribe to %q", TopicPaymentCaptured)
	}

	return w, nil
}

// resolve reads one service and says which name failed.
func resolve[T any](c *container.Container, name string) (T, error) {
	value, err := container.Resolve[T](c, name)
	if err != nil {
		var zero T

		return zero, errors.Wrap(err, errors.KindOf(err), CodeNotReady,
			"the gift card sale flow needs %q and it could not be resolved", name)
	}

	return value, nil
}
