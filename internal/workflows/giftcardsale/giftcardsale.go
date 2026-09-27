// Package giftcardsale issues the gift cards an order sold, once its money is
// in, and mails each card's code to the order's address (ADR 0210).
//
// It is a workflow because it combines four modules (ADR 0006): the payment
// module says the money is in and keeps the cards, the order module holds the
// lines and the address, the catalog says which products are gift cards, and
// the notification module sends the code. None of them imports another, and
// this package imports none of them: each is reached by name through the
// narrow interface declared here.
//
// # At least once, and once
//
// The flow runs on payment.captured, which the outbox publishes at least once. A
// card is issued under the name of the sale — the order line and the unit — so
// a second delivery finds the card the first one made and gets no code back.
// Only the call that made a card holds its code, and only that call mails it.
//
// A handler's error is logged and the event is not delivered again
// (docs/extending.md). An order whose flow failed before its cards were made
// has no cards, and nothing issues them later yet; ADR 0210 names it.
//
// # A code that did not arrive
//
// The code is mailed once and kept nowhere (ADR 0208). If the mail fails, or
// the process stops between the card and the mail, the code is lost and the
// card is not: an operator gives the card a new code (ADR 0210), and the
// notification log shows which mail did not go.
package giftcardsale

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

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
	// EntityLineItem, EntityVariant and EntityProduct are the read layer's
	// entities the flow walks from an order's lines to their products.
	EntityLineItem = "order_line_item"
	EntityVariant  = "variant"
	EntityProduct  = "product"
	// FieldIsGiftcard is the product's flag.
	FieldIsGiftcard = "is_giftcard"
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

// linePage is how many of an order's lines are read at a time.
const linePage = 100

// CodeNotReady reports a flow that could not be wired.
const CodeNotReady = "gift_card_sale_not_ready"

// Payments is the slice of the payment module this flow calls.
type Payments interface {
	// Collection reads a collection's amounts.
	Collection(ctx context.Context, collectionID string) (
		status string, amount, authorized, captured, refunded int64, err error)
	// IssueSoldGiftCard issues the card a sale made; the code is empty when
	// the card already existed.
	IssueSoldGiftCard(ctx context.Context, reference, orderID, currencyCode string, amount int64) (
		cardID, code string, err error)
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

// Catalog is the read layer.
type Catalog interface {
	// Graph reads records of an entity.
	Graph(ctx context.Context, spec query.GraphSpec) ([]query.Record, error)
}

// Links reads the Module Links this flow needs.
type Links interface {
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
	catalog  Catalog
	links    Links
	log      *slog.Logger
}

// New builds the flow from resolved dependencies.
func New(payments Payments, orders Orders, notifier Notifier, catalog Catalog, links Links, log *slog.Logger) *Workflow {
	if log == nil {
		log = slog.Default()
	}

	return &Workflow{payments: payments, orders: orders, notifier: notifier, catalog: catalog, links: links, log: log}
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

	_, amount, _, captured, _, err := w.payments.Collection(ctx, collectionID)
	if err != nil {
		return err
	}
	if captured < amount {
		return nil
	}

	orders, err := w.links.ListManyByTo(ctx, LinkOrderPayment, []string{collectionID})
	if err != nil {
		return err
	}
	for _, orderID := range orders[collectionID] {
		if err := w.issueFor(ctx, orderID); err != nil {
			return err
		}
	}

	return nil
}

// soldLine is one of an order's lines that sold gift cards.
type soldLine struct {
	id        string
	quantity  int64
	unitPrice int64
}

// issueFor issues and mails the cards one order sold.
func (w *Workflow) issueFor(ctx context.Context, orderID string) error {
	lines, err := w.giftCardLines(ctx, orderID)
	if err != nil || len(lines) == 0 {
		return err
	}

	raw, err := w.orders.OrderContactJSON(ctx, orderID)
	if err != nil {
		return err
	}
	var contact struct {
		Email        string `json:"email"`
		CurrencyCode string `json:"currency_code"`
	}
	if err := json.Unmarshal(raw, &contact); err != nil {
		return errors.Wrap(err, errors.KindInternal, CodeNotReady, "the contact of order %s could not be read", orderID)
	}

	for _, line := range lines {
		for unit := int64(1); unit <= line.quantity; unit++ {
			// The card's value is the line's unit price, before any discount
			// (ADR 0210).
			cardID, code, err := w.payments.IssueSoldGiftCard(ctx,
				fmt.Sprintf("%s:%d", line.id, unit), orderID, contact.CurrencyCode, line.unitPrice)
			if err != nil {
				return err
			}
			if code == "" {
				// An earlier delivery made this card and held its code.
				continue
			}
			if err := w.notifier.Send(ctx, TemplateIssued, ChannelEmail, cardID, contact.Email, map[string]string{
				DataCode: code, DataAmount: fmt.Sprint(line.unitPrice),
				DataCurrencyCode: contact.CurrencyCode, DataOrderID: orderID,
			}); err != nil {
				w.log.ErrorContext(ctx, "a sold gift card's code could not be mailed; give the card a new code",
					"gift_card", cardID, "order_id", orderID, "error", err)

				return err
			}
		}
	}

	return nil
}

// giftCardLines reads an order's lines and keeps the ones whose product is a
// gift card.
func (w *Workflow) giftCardLines(ctx context.Context, orderID string) ([]soldLine, error) {
	var lines []query.Record
	for offset := 0; ; offset += linePage {
		page, err := w.catalog.Graph(ctx, query.GraphSpec{
			Entity:  EntityLineItem,
			Fields:  []string{query.IDField, "variant_id", "quantity", "unit_price"},
			Filters: map[string]any{"order_id": orderID},
			Limit:   linePage, Offset: offset,
		})
		if err != nil {
			return nil, err
		}
		lines = append(lines, page...)
		if len(page) < linePage {
			break
		}
	}

	variantIDs := make([]string, 0, len(lines))
	for _, line := range lines {
		if id, _ := line["variant_id"].(string); id != "" {
			variantIDs = append(variantIDs, id)
		}
	}
	productOf, err := w.productsOf(ctx, variantIDs)
	if err != nil {
		return nil, err
	}
	giftcards, err := w.giftCardProducts(ctx, productOf)
	if err != nil {
		return nil, err
	}

	var out []soldLine
	for _, line := range lines {
		variantID, _ := line["variant_id"].(string)
		if !giftcards[productOf[variantID]] {
			continue
		}
		id, _ := line[query.IDField].(string)
		quantity, quantityOK := number(line["quantity"])
		price, priceOK := number(line["unit_price"])
		if id == "" || !quantityOK || !priceOK {
			return nil, errors.Internal(CodeNotReady, "a gift card line of order %s could not be read", orderID)
		}
		out = append(out, soldLine{id: id, quantity: quantity, unitPrice: price})
	}

	return out, nil
}

// productsOf maps variants to their products.
func (w *Workflow) productsOf(ctx context.Context, variantIDs []string) (map[string]string, error) {
	out := map[string]string{}
	if len(variantIDs) == 0 {
		return out, nil
	}
	records, err := w.catalog.Graph(ctx, query.GraphSpec{
		Entity: EntityVariant, Fields: []string{query.IDField, "product_id"},
		Filters: map[string]any{"ids": variantIDs}, Limit: len(variantIDs),
	})
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		id, _ := record[query.IDField].(string)
		product, _ := record["product_id"].(string)
		out[id] = product
	}

	return out, nil
}

// giftCardProducts reports which of the products are gift cards.
func (w *Workflow) giftCardProducts(ctx context.Context, productOf map[string]string) (map[string]bool, error) {
	ids := make([]string, 0, len(productOf))
	seen := map[string]bool{}
	for _, product := range productOf {
		if product != "" && !seen[product] {
			seen[product] = true
			ids = append(ids, product)
		}
	}
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	records, err := w.catalog.Graph(ctx, query.GraphSpec{
		Entity: EntityProduct, Fields: []string{query.IDField, FieldIsGiftcard},
		Filters: map[string]any{"ids": ids}, Limit: len(ids),
	})
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		id, _ := record[query.IDField].(string)
		giftcard, _ := record[FieldIsGiftcard].(bool)
		out[id] = giftcard
	}

	return out, nil
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

// FromContainer resolves the flow's dependencies and subscribes it to the
// capture event.
func FromContainer(c *container.Container, log *slog.Logger) (*Workflow, error) {
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
	catalog, err := resolve[Catalog](c, ServiceQuery)
	if err != nil {
		return nil, err
	}
	links, err := resolve[Links](c, ServiceLink)
	if err != nil {
		return nil, err
	}
	bus, err := resolve[Subscriber](c, ServiceEventBus)
	if err != nil {
		return nil, err
	}

	w := New(payments, orders, notifier, catalog, links, log)
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
