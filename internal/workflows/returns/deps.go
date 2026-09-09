// Package returns is the flow that ACTS on a return.
//
// # Why it is a flow and not a module method
//
// The order module records that goods came back; putting their stock back
// reaches the INVENTORY module, and a module does not know another module
// (Principle 2.1/2.4). Deciding across modules is this layer's job (ADR 0006),
// exactly as it is for the checkout saga and the cart's pricing.
//
// Until this package existed the after-sales records could be created and read
// and nothing else: `order/service/aftersales.go` said so in writing and
// deferred acting to "the next phases". The roadmap that contained those phases
// closed.
//
// # Receiving goods is NOT refunding money
//
// This flow puts stock back and stops. A refund is a separate decision an
// operator makes after looking at what arrived — goods can come back damaged,
// incomplete, or outside the return window — and a framework that refunded
// automatically on receipt would be deciding something the shop has to decide.
//
// The two are also asymmetric in cost: the stock is a physical fact the moment
// the parcel is opened, while the money is reversible until someone sends it.
package returns

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
	// ServiceInventory is the inventory module's cross-module surface.
	ServiceInventory = "inventory.interop"
	// ServicePayment is the payment module's cross-module surface.
	ServicePayment = "payment.interop"
	// ServiceLink is the core's Module Links service.
	ServiceLink = "core.link"
	// ServiceShipping is the fulfilling FLOW's cross-flow surface. The value is
	// repeated here for [LinkVariantInventory]'s reason.
	ServiceShipping = "workflows.fulfilling.interop"
)

// LinkOrderPayment binds an order to the payment collection opened for it.
//
// The payment module declares it and the value is REPEATED here for the reason
// [LinkVariantInventory] gives.
const LinkOrderPayment = "order_payment"

// LinkVariantInventory binds a product variant to its inventory item.
//
// The product module declares it and the value is REPEATED here: this package
// cannot import that module (ADR 0006) and the repetition is the accepted price
// of isolation (ADR 0001). A typo does not stay silent — the link service
// returns NotFound for an undeclared name.
const LinkVariantInventory = "product_variant_inventory"

// Error codes.
const (
	// CodeNotReady reports that the flow was wired with a missing dependency.
	CodeNotReady = "returns_workflow_not_ready"
	// CodeInvalidInput reports that the input did not pass validation.
	CodeInvalidInput = "returns_workflow_invalid_input"
	// CodeReturnUnreadable reports that the return could not be read.
	CodeReturnUnreadable = "returns_workflow_return_unreadable"
	// CodeNoInventoryItem reports that a returned variant has no inventory item.
	CodeNoInventoryItem = "returns_workflow_no_inventory_item"
	// CodeNoPayment reports that the order has no payment collection bound.
	CodeNoPayment = "returns_workflow_no_payment"
	// CodeRefundFailed reports that the money could not be sent back.
	CodeRefundFailed = "returns_workflow_refund_failed"
	// CodeReplacementUnreadable reports that the replacement could not be read.
	CodeReplacementUnreadable = "returns_workflow_replacement_unreadable"
	// CodeReplacementNotOpen reports a dispatch of a replacement that is not
	// waiting to be sent.
	CodeReplacementNotOpen = "returns_workflow_replacement_not_open"
	// CodeStockNotHeld reports that the units could not be set aside.
	CodeStockNotHeld = "returns_workflow_stock_not_held"
	// CodeParcelNotOpened reports that no parcel could be opened for the goods.
	CodeParcelNotOpened = "returns_workflow_parcel_not_opened"
	// CodeStockNotTaken reports that the promised units could not be deducted.
	CodeStockNotTaken = "returns_workflow_stock_not_taken"
)

// Orders is the surface of the order module used by this flow.
type Orders interface {
	// ReturnDetailJSON returns a return with its lines and their variants.
	ReturnDetailJSON(ctx context.Context, returnID string) (json.RawMessage, error)
	// ReceiveReturn stamps the return as received at the given location.
	ReceiveReturn(ctx context.Context, returnID, locationID string) error
	// SetOrderSummaryTotals records on the order how much was collected and how
	// much was refunded; the write is a MERGE and cannot shrink a total.
	SetOrderSummaryTotals(ctx context.Context, orderID string, paidTotal, refundedTotal int64) error

	// ClaimDetailJSON returns what a flow needs to settle a claim.
	ClaimDetailJSON(ctx context.Context, claimID string) (json.RawMessage, error)
	// CompleteClaim records that the claim was settled.
	CompleteClaim(ctx context.Context, claimID string) error

	// ReplacementDetailJSON returns what a flow needs to send a replacement.
	ReplacementDetailJSON(ctx context.Context, replacementID string) (json.RawMessage, error)
	// RecordReplacementReservation writes the promise a line's units are held
	// under; repeating it with the same promise is a no-op.
	RecordReplacementReservation(ctx context.Context, replacementID, itemID, reservationID string) error
	// MarkReplacementDispatched records that the goods left, in the parcel named.
	MarkReplacementDispatched(ctx context.Context, replacementID, fulfillmentID string) error
}

// Payments is the surface of the payment module used by this flow.
type Payments interface {
	// RefundCollection refunds an amount against a collection and returns what
	// actually went back. A zero amount refunds everything left.
	RefundCollection(ctx context.Context, collectionID string, amount int64, reason string) (int64, error)
	// Collection returns the collection's status and amounts.
	Collection(ctx context.Context, collectionID string) (
		status string,
		amount, authorized, captured, refunded int64,
		err error,
	)
}

// Inventory is the surface of the inventory module used by this flow.
type Inventory interface {
	// Restock puts quantity units of the item back at the location.
	Restock(ctx context.Context, inventoryItemID, locationID string, quantity int64) error
	// ReserveForReplacement sets units aside for goods being SENT to settle a
	// claim and returns the promise's id. Insufficient stock is a Conflict.
	//
	// It is the replacement's own entry point rather than the sale's, because
	// the confirm behind it writes what the ledger will say a month from now:
	// units that left as a sale, or units nobody paid for.
	ReserveForReplacement(
		ctx context.Context,
		inventoryItemID, locationID string,
		quantity int64,
		orderLineItemID string,
	) (reservationID string, err error)
	// ConfirmReservation turns the promise into deducted stock. It is
	// idempotent: confirming a confirmed promise does nothing.
	ConfirmReservation(ctx context.Context, reservationID string) error
	// ReleaseReservation gives the units back. It is idempotent.
	ReleaseReservation(ctx context.Context, reservationID string) error
}

// Shipping is the surface of the FULFILLING FLOW used by this flow.
//
// It is a flow rather than a module, and that is deliberate: opening a parcel
// means creating a shipment AND binding it to its order, and the binding is
// this layer's fact. Reaching the fulfillment module directly would open
// parcels nothing could find again — and would miss the refusal that flow
// makes when an idempotency key names a shipment somebody canceled (ADR 0088).
type Shipping interface {
	// OpenForOrder opens a shipment for an order and binds the two. The
	// request carries the shipping option and the idempotency key.
	OpenForOrder(
		ctx context.Context, orderID string, request json.RawMessage,
	) (fulfillmentID string, alreadyOpen bool, err error)
}

// Links is the surface of the core's Module Links service used by this flow.
//
// There is only a BATCH read: a return's lines are resolved in ONE query, so
// the number of queries does not grow with the number of lines.
type Links interface {
	// ListMany returns the links of the given source ids in a SINGLE query.
	ListMany(ctx context.Context, name string, fromIDs []string) (map[string][]string, error)
}

// Deps are the flow's dependencies.
type Deps struct {
	// Orders is the order surface; it is mandatory.
	Orders Orders
	// Inventory is the inventory surface; it is mandatory.
	//
	// It is NOT optional, unlike the tax and promotion surfaces the cart flow
	// treats that way. Those have a correct fallback and the answer stays a
	// real number; a missing inventory surface has none — receiving goods
	// without putting their stock back is the defect this flow exists to close.
	Inventory Inventory
	// Payments is the payment surface; it is mandatory.
	//
	// Like [Deps.Inventory] it has no correct fallback: refunding without it
	// would mean telling a customer their money is on the way while nothing
	// asked a provider to send it.
	Payments Payments
	// Links is the Module Links surface; it is mandatory.
	Links Links
	// Shipping is the fulfilling flow's surface; it is mandatory.
	//
	// Like the two above it has no correct fallback: a replacement whose parcel
	// was never opened is a promise the shop cannot keep, and dispatching
	// without one would deduct stock for goods nobody is carrying.
	Shipping Shipping
	// Logger discards the logs when nil.
	Logger *slog.Logger
}

// Workflows is the flow.
type Workflows struct {
	orders    Orders
	inventory Inventory
	payments  Payments
	links     Links
	shipping  Shipping
	log       *slog.Logger
}

// New builds the flow with the given dependencies.
func New(deps Deps) (*Workflows, error) {
	missing := []struct {
		name  string
		empty bool
	}{
		{ServiceOrder, deps.Orders == nil},
		{ServiceInventory, deps.Inventory == nil},
		{ServicePayment, deps.Payments == nil},
		{ServiceLink, deps.Links == nil},
		{ServiceShipping, deps.Shipping == nil},
	}
	for _, dep := range missing {
		if dep.empty {
			return nil, errors.Internal(CodeNotReady,
				"the return flow cannot be built without %q", dep.name)
		}
	}

	log := deps.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Workflows{
		orders:    deps.Orders,
		inventory: deps.Inventory,
		payments:  deps.Payments,
		links:     deps.Links,
		shipping:  deps.Shipping,
		log:       log,
	}, nil
}

// FromContainer wires the flow by resolving every surface BY NAME.
//
// The error names the surface that could not be resolved, so a wiring mistake
// says which registration is missing rather than "something is nil".
func FromContainer(c *container.Container) (*Workflows, error) {
	if c == nil {
		return nil, errors.Internal(CodeNotReady,
			"the return flow cannot be wired without a container")
	}

	orders, err := resolve[Orders](c, ServiceOrder)
	if err != nil {
		return nil, err
	}
	inventory, err := resolve[Inventory](c, ServiceInventory)
	if err != nil {
		return nil, err
	}
	payments, err := resolve[Payments](c, ServicePayment)
	if err != nil {
		return nil, err
	}
	links, err := resolve[Links](c, ServiceLink)
	if err != nil {
		return nil, err
	}
	shipping, err := resolve[Shipping](c, ServiceShipping)
	if err != nil {
		return nil, err
	}

	return New(Deps{
		Orders:    orders,
		Inventory: inventory,
		Payments:  payments,
		Links:     links,
		Shipping:  shipping,
	})
}

// resolve reads one surface from the container and wraps the failure with its
// name.
func resolve[T any](c *container.Container, name string) (T, error) {
	svc, err := container.Resolve[T](c, name)
	if err != nil {
		var zero T

		return zero, errors.Wrap(err, errors.KindOf(err), CodeNotReady,
			"the return flow could not resolve the %q service", name)
	}

	return svc, nil
}
