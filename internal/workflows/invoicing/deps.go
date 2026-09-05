// Package invoicing is the flow that turns an ORDER into an invoice.
//
// # Why it is a flow and not a module method
//
// The document is the invoice module's record and the sale is the order
// module's, and a module does not know another module (Principle 2.1/2.4).
// Deciding across the two is this layer's job (ADR 0006), exactly as it is for
// the checkout saga and the after-sales flow.
//
// The invoice module is deliberately ignorant of orders: it is handed a
// finished document, checks that it adds up, numbers it and stores it. That is
// what lets a shop invoice something that is not an order at all — a service, a
// repair, a manual sale — and it is why the assembling lives here.
//
// # Issuing is a DECISION, not a consequence of the sale
//
// Nothing here runs automatically when an order is placed. When a shop invoices
// is a policy question with real answers on both sides — on payment, on
// dispatch, monthly for a corporate buyer — and a framework that issued on
// checkout would be deciding it. The same reasoning the after-sales flow uses
// for refunds: the record is created when someone decides, not when something
// happens.
//
// # Issuing twice must not cost two numbers
//
// A number is spent for good once it is taken (ADR 0024), so a double-click has
// to be answered with the document that already exists rather than with a
// second one. The order-to-invoice link is what makes that possible, and
// reading it before doing anything else is the first thing this flow does.
package invoicing

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
)

// Service names in the container (ADR 0006). The concrete types are resolved by
// these names; none of them is known at compile time.
const (
	// ServiceOrder is the order module's cross-module surface.
	ServiceOrder = "order.interop"
	// ServiceInvoice is the invoice module's cross-module surface.
	//
	// It is the PRIMITIVE surface and not the module's rich service: a workflow
	// may not import a module in either direction (ADR 0006), so the types on
	// this side have to be its own.
	ServiceInvoice = "invoice.interop"
	// ServiceLink is the core's Module Links service.
	ServiceLink = "core.link"
)

// Error codes.
const (
	// CodeInvalidInput reports that the request could not be accepted.
	CodeInvalidInput = "invoicing_invalid_input"
	// CodeOrderUnreadable reports that the order could not be read.
	CodeOrderUnreadable = "invoicing_order_unreadable"
	// CodeLinkFailed reports that the document was issued and the binding to
	// the order was not written.
	CodeLinkFailed = "invoicing_link_failed"
	// CodeSetupFailed reports a dependency that could not be resolved.
	CodeSetupFailed = "invoicing_setup_failed"
)

// Orders is the part of the order module this flow reads.
//
// It is declared HERE, on the consumer's side, and carries only primitives and
// JSON so that resolving it by name needs no import of the order module
// (ADR 0001/0006).
type Orders interface {
	// OrderInvoiceJSON returns everything a document has to print about an
	// order: its lines with their tax rates, its totals and its contact.
	OrderInvoiceJSON(ctx context.Context, orderID string) (json.RawMessage, error)
}

// Links is the part of the core's link service this flow uses.
type Links interface {
	// Create binds the two records to each other.
	//
	// Binding the same pair twice is a NO-OP; binding an end that is already
	// bound to another record is a CONFLICT, because the definition is one to
	// one.
	Create(ctx context.Context, definition, fromID, toID string) error
	// ListMany returns, for each of the given ids, the ids bound to it.
	ListMany(ctx context.Context, definition string, fromIDs []string) (map[string][]string, error)
}

// Deps are the flow's dependencies.
type Deps struct {
	// Orders is the order module's primitive surface.
	Orders Orders
	// Invoices is the invoice module's service.
	Invoices Invoices
	// Links is the core's Module Links service.
	Links Links
	// Logger falls back to slog.Default when nil.
	Logger *slog.Logger
}

// Workflows is the flow.
type Workflows struct {
	orders   Orders
	invoices Invoices
	links    Links
	log      *slog.Logger
}

// New builds the flow and refuses a missing dependency.
//
// A flow that came up with a nil dependency would fail on the first request
// with a panic instead of a message; the absence is a setup fault and belongs
// at startup.
func New(deps Deps) (*Workflows, error) {
	switch {
	case deps.Orders == nil:
		return nil, errors.Internal(CodeSetupFailed, "the invoicing flow needs the order surface")
	case deps.Invoices == nil:
		return nil, errors.Internal(CodeSetupFailed, "the invoicing flow needs the invoice service")
	case deps.Links == nil:
		return nil, errors.Internal(CodeSetupFailed, "the invoicing flow needs the link service")
	}

	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}

	return &Workflows{
		orders:   deps.Orders,
		invoices: deps.Invoices,
		links:    deps.Links,
		log:      deps.Logger,
	}, nil
}

// FromContainer builds the flow by resolving its dependencies BY NAME.
//
// It is called from the composition root after every module has registered:
// the flow needs surfaces that do not exist while the modules are coming up.
func FromContainer(c *container.Container) (*Workflows, error) {
	orders, err := container.Resolve[Orders](c, ServiceOrder)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeSetupFailed,
			"the invoicing flow could not resolve %q", ServiceOrder)
	}

	invoices, err := container.Resolve[Invoices](c, ServiceInvoice)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeSetupFailed,
			"the invoicing flow could not resolve %q", ServiceInvoice)
	}

	links, err := container.Resolve[Links](c, ServiceLink)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeSetupFailed,
			"the invoicing flow could not resolve %q", ServiceLink)
	}

	return New(Deps{Orders: orders, Invoices: invoices, Links: links})
}
