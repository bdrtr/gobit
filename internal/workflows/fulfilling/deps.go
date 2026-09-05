// Package fulfilling is the flow that opens a SHIPMENT for an order.
//
// # Why it is a flow and not a module method
//
// The shipment is the fulfillment module's record and the sale is the order
// module's, and a module does not know another module (Principle 2.1/2.4).
// Deciding across the two is this layer's job (ADR 0006), exactly as it is for
// the checkout saga, the after-sales flow and invoicing.
//
// The fulfillment module is deliberately ignorant of orders. Its create
// endpoint takes a free-text Reference it never validates, and its own godoc
// says where the association belongs: "Principle 2.2 — the link is established
// through Module Links". So the module could open a shipment all along and
// nothing could answer "which order is this parcel for" — the question the
// support desk asks first.
//
// # What was actually missing
//
// Two halves, and each was useless without the other. The link definition
// "order_fulfillment" was named in the order module's package doc as somebody
// else's job and declared by nobody. And the fulfillment module's
// CreateFulfillment interop — documented down to the consumer-side interface a
// caller should declare — had no caller at all. A capability with no consumer
// is this repository's named second error class; this flow is the consumer, and
// the fulfillment module now declares the definition.
//
// # Opening a shipment is a DECISION, not a consequence of the sale
//
// Nothing here runs when an order is placed. When a shop ships is a policy
// question with real answers on both sides — on payment, after picking, in one
// parcel or in three — and a framework that opened a shipment at checkout would
// be deciding it. The same reasoning invoicing uses for the document and
// after-sales uses for the refund: the record is created when someone decides.
package fulfilling

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
)

// Service names in the container (ADR 0006). The concrete types are resolved by
// these names; none of them is known at compile time.
const (
	// ServiceOrder is the order module's cross-module surface.
	ServiceOrder = "order.interop"
	// ServiceFulfillment is the fulfillment module's cross-module surface.
	//
	// It is the PRIMITIVE surface and not the module's rich service: a workflow
	// may not import a module in either direction (ADR 0006), so the types on
	// this side have to be its own.
	ServiceFulfillment = "fulfillment.interop"
	// ServiceLink is the core's Module Links service.
	ServiceLink = "core.link"
)

// Error codes.
const (
	// CodeInvalidInput reports that the request could not be accepted.
	CodeInvalidInput = "fulfilling_invalid_input"
	// CodeOrderUnreadable reports that the order could not be read.
	CodeOrderUnreadable = "fulfilling_order_unreadable"
	// CodeCreateFailed reports that the shipment could not be opened.
	CodeCreateFailed = "fulfilling_create_failed"
	// CodeLinkFailed reports that the shipment was opened and the binding to
	// the order was NOT written.
	//
	// It is its own code because the consequence is specific: the parcel
	// exists, the customer may already have it, and nothing can answer which
	// order it belongs to.
	CodeLinkFailed = "fulfilling_link_failed"
	// CodeLinkUnreadable reports that the order's shipments could not be read.
	CodeLinkUnreadable = "fulfilling_link_unreadable"
	// CodeSetupFailed reports a dependency that could not be resolved.
	CodeSetupFailed = "fulfilling_setup_failed"
)

// Orders is the part of the order module this flow reads.
//
// It is declared HERE, on the consumer's side, and carries only primitives and
// JSON so that resolving it by name needs no import of the order module
// (ADR 0001/0006).
type Orders interface {
	// OrderContactJSON returns the order's contact block.
	//
	// The flow does not need the contact; it needs the REFUSAL. An unknown id
	// comes back as a not-found, and that is what keeps a typo from opening a
	// parcel bound to an order that does not exist — an orphan the operator
	// would find only when the customer asked where it was.
	OrderContactJSON(ctx context.Context, orderID string) (json.RawMessage, error)
}

// Fulfillments is the part of the fulfillment module this flow uses.
type Fulfillments interface {
	// CreateFulfillment opens a shipment and returns its identifier.
	//
	// A second call with the same idempotency key returns the EXISTING
	// shipment rather than opening a second one; that is what keeps a retry
	// from printing a second label.
	CreateFulfillment(ctx context.Context, reference, optionID, idempotencyKey string) (string, error)
	// FulfillmentStatus returns the shipment's status.
	FulfillmentStatus(ctx context.Context, fulfillmentID string) (string, error)
}

// Links is the part of the core's link service this flow uses.
type Links interface {
	// Create binds the two records to each other. Binding the same pair twice
	// is a NO-OP.
	Create(ctx context.Context, definition, fromID, toID string) error
	// ListMany returns, for each of the given ids, the ids bound to it.
	ListMany(ctx context.Context, definition string, fromIDs []string) (map[string][]string, error)
}

// Deps are the flow's dependencies.
type Deps struct {
	// Orders is the order module's primitive surface.
	Orders Orders
	// Fulfillments is the fulfillment module's primitive surface.
	Fulfillments Fulfillments
	// Links is the core's Module Links service.
	Links Links
	// Logger falls back to slog.Default when nil.
	Logger *slog.Logger
}

// Workflows is the flow.
type Workflows struct {
	orders       Orders
	fulfillments Fulfillments
	links        Links
	log          *slog.Logger
}

// New builds the flow and refuses a missing dependency.
//
// A flow that came up with a nil dependency would fail on the first request
// with a panic instead of a message; the absence is a setup fault and belongs
// at startup.
func New(deps Deps) (*Workflows, error) {
	switch {
	case deps.Orders == nil:
		return nil, errors.Internal(CodeSetupFailed, "the fulfilling flow needs the order surface")
	case deps.Fulfillments == nil:
		return nil, errors.Internal(CodeSetupFailed,
			"the fulfilling flow needs the fulfillment surface")
	case deps.Links == nil:
		return nil, errors.Internal(CodeSetupFailed, "the fulfilling flow needs the link service")
	}

	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}

	return &Workflows{
		orders:       deps.Orders,
		fulfillments: deps.Fulfillments,
		links:        deps.Links,
		log:          deps.Logger,
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
			"the fulfilling flow could not resolve %q", ServiceOrder)
	}

	fulfillments, err := container.Resolve[Fulfillments](c, ServiceFulfillment)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeSetupFailed,
			"the fulfilling flow could not resolve %q", ServiceFulfillment)
	}

	links, err := container.Resolve[Links](c, ServiceLink)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeSetupFailed,
			"the fulfilling flow could not resolve %q", ServiceLink)
	}

	return New(Deps{Orders: orders, Fulfillments: fulfillments, Links: links})
}
