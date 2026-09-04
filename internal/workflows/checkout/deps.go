package checkout

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/core/workflow"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// Service names in the container (ADR 0006). Concrete types are resolved by
// these names; none of them is known at compile time.
const (
	// ServiceCart is the cross-module primitive surface of the cart module.
	ServiceCart = "cart.interop"
	// ServiceInventory is the cross-module primitive surface of the inventory
	// module.
	ServiceInventory = "inventory.interop"
	// ServiceOrder is the cross-module primitive surface of the order module.
	ServiceOrder = "order.interop"
	// ServicePayment is the cross-module primitive surface of the payment module.
	ServicePayment = "payment.interop"
	// ServiceFulfillment is the cross-module primitive surface of the fulfillment
	// module.
	//
	// The name was read from the fulfillment module's OWN wiring (the module's
	// InteropName constant); a guessed name would have shown up only at
	// resolution time, as a setup error.
	ServiceFulfillment = "fulfillment.interop"
	// LinkOrderPayment is the link binding an order to its payment collection.
	//
	// The name is REPEATED here rather than imported: this package cannot
	// import the payment module (ADR 0006), which declares the definition. A
	// typo does not stay silent — the link service returns NotFound for an
	// undeclared name.
	LinkOrderPayment = "order_payment"

	// ServiceLink is the core's Module Links service.
	ServiceLink = "core.link"
	// ServiceQuery is the core's cross-module read layer.
	ServiceQuery = "core.query"
	// ServiceWorkflow is the core's saga engine; execution state is written to
	// pgstore.
	ServiceWorkflow = "core.workflow"
)

// Cross-module CONTRACT constants.
//
// The values are defined in the product module as well and are REPEATED here:
// this package cannot import that module (ADR 0006) and the repetition is the
// accepted price of isolation (ADR 0001). A typo does not stay silent — if the
// link name is wrong core/link returns errors.NotFound, and if the entity or
// the field name is wrong Query returns errors.NotFound/errors.Invalid.
const (
	// LinkVariantInventory is the name of the link that binds a variant to an
	// inventory item; its definition is declared by the product module.
	LinkVariantInventory = "product_variant_inventory"
	// EntityVariant is the entity name of variants in the Query layer.
	EntityVariant = "variant"
	// FieldTitle is the name of the field that holds the title in a variant
	// record.
	FieldTitle = "title"
	// FilterIDs is the BATCH identifier filter of the variant provider; thanks to
	// this filter a separate query per line (N+1) is not needed.
	FilterIDs = "ids"
)

// Error codes. Clients may branch on them; messages can change, codes do not.
const (
	// CodeInvalidInput reports that the input did not pass validation.
	CodeInvalidInput = "checkout_workflow_invalid_input"
	// CodeNotReady reports that the workflows were built with a missing
	// dependency.
	CodeNotReady = "checkout_workflow_not_ready"
	// CodeDependencyMissing reports that a service could not be resolved in the
	// container.
	CodeDependencyMissing = "checkout_workflow_dependency_missing"
	// CodeCartCompleted reports that an already completed cart was asked to be
	// ordered again.
	CodeCartCompleted = "checkout_workflow_cart_completed"
	// CodeCartEmpty reports that a cart with no lines was asked to be ordered.
	CodeCartEmpty = "checkout_workflow_cart_empty"
	// CodeCartChanged reports that the cart changed between the totals and the
	// snapshot.
	CodeCartChanged = "checkout_workflow_cart_changed"
	// CodeTotalMismatch reports that the amount the caller confirmed does not
	// match the computed amount.
	CodeTotalMismatch = "checkout_workflow_total_mismatch"
	// CodeSnapshotInvalid reports that the cart snapshot could not be read.
	CodeSnapshotInvalid = "checkout_workflow_snapshot_invalid"
	// CodeTotalsInvalid reports that the totals do not cover the lines of the
	// cart.
	CodeTotalsInvalid = "checkout_workflow_totals_invalid"
	// CodeAmountInvalid reports that the amount to be captured is invalid.
	CodeAmountInvalid = "checkout_workflow_amount_invalid"
	// CodeLinkReadFailed reports that the link layer COULD NOT BE READ; "there is
	// no inventory item" and "we could not find out whether there is one" are
	// different situations.
	CodeLinkReadFailed = "checkout_workflow_link_read_failed"
	// CodeCatalogReadFailed reports that the catalog read failed; it does NOT
	// mean that the variant does not exist.
	CodeCatalogReadFailed = "checkout_workflow_catalog_read_failed"
	// CodeVariantUnknown reports that a variant that is not in the catalog was
	// referenced.
	CodeVariantUnknown = "checkout_workflow_variant_unknown"
	// CodeVariantNotStocked reports that the variant is not linked to any
	// inventory item.
	CodeVariantNotStocked = "checkout_workflow_variant_not_stocked"
	// CodeVariantInventoryAmbiguous reports that the variant appears to be linked
	// to more than one inventory item.
	CodeVariantInventoryAmbiguous = "checkout_workflow_variant_inventory_ambiguous"
	// CodeReservationFailed reports that the inventory reservation step blew up.
	//
	// It is a FALLBACK code: if the error that made the step blow up carries its
	// own code THAT one is kept and this code never appears (see
	// [reserveInventoryStep.unwind]). What is left for it is only an error
	// without a code — and the contract violation errors this package produces
	// itself.
	CodeReservationFailed = "checkout_workflow_reservation_failed"
	// CodeReservationLeaked reports that the reserved inventory COULD NOT BE
	// RELEASED; manual intervention is required.
	CodeReservationLeaked = "checkout_workflow_reservation_leaked"
	// CodePaymentUnderauthorized reports that the authorized amount does not
	// cover the amount that must be collected (FULL PAYMENT RULE).
	CodePaymentUnderauthorized = "checkout_workflow_payment_underauthorized"
	// CodePaymentUndercaptured reports that the captured amount does not cover
	// the amount that must be collected.
	CodePaymentUndercaptured = "checkout_workflow_payment_undercaptured"
	// CodeCaptureIrreversible reports that a captured amount cannot be taken back
	// in this workflow; a refund is a SEPARATE workflow.
	CodeCaptureIrreversible = "checkout_workflow_capture_irreversible"
	// CodeCaptureAmbiguous reports that the money may have moved EVEN THOUGH the
	// capture call returned an error: the saga has NOT been rolled back and
	// manual reconciliation is required.
	CodeCaptureAmbiguous = "checkout_workflow_capture_ambiguous"
	// CodeEmptyIdentifier reports that a module returned an EMPTY identifier
	// without returning an error; the side effect is out in the world and we hold
	// no trace of it.
	CodeEmptyIdentifier = "checkout_workflow_empty_identifier"
	// CodeLinkFailed reports that a cross-module binding could not be written.
	CodeLinkFailed = "checkout_workflow_link_failed"
	// CodeSharedStateInvalid reports that the data carried between steps is
	// corrupt.
	CodeSharedStateInvalid = "checkout_workflow_shared_state_invalid"
)

// Carts is the surface of the cart module ("cart.interop") used by this
// package.
//
// The surface is limited to two methods: the saga READS the cart and at the end
// stamps it COMPLETED. Adding lines to or removing lines from the cart is not
// this workflow's business, and writing those methods here would have given
// checkout the right to modify the cart.
//
// The totals of the cart are not read through this surface; the totals are
// produced by [CartTotals] and the rationale is in the package comment.
type Carts interface {
	// CartSnapshotJSON returns, in a SINGLE read, the shape of the cart that goes
	// into the totals.
	//
	// The body is in the [Snapshot] schema. If the cart does not exist,
	// errors.NotFound.
	CartSnapshotJSON(ctx context.Context, cartID string) (json.RawMessage, error)

	// MarkCompleted stamps the cart as completed.
	//
	// It is NOT IDEMPOTENT: stamping an already completed cart a second time
	// returns errors.Conflict and that is deliberate (see MarkCompleted in the
	// cart module). The protection against repeats lives in the engine's
	// idempotency key. If the cart has no lines or its totals are STALE the call
	// returns errors.Conflict; that is why the totals are refreshed before the
	// saga.
	MarkCompleted(ctx context.Context, cartID string) error
}

// CartTotals is the surface of the cart totals (internal/workflows/cart) used
// by this package.
//
// It is not resolved from the container by NAME; [FromContainer] builds it on
// the same container (see cartwf.FromContainer). It is defined as an interface
// for the sake of the tests: the real totals flow wants pricing, region and
// Query, whereas the unit tests of this package hand over the RESULT of the
// totals and do not re-exercise the totals themselves.
//
// Unlike the other surfaces of this package its signature is NOT primitive: the
// return type is a type of the sibling package and that package CAN be imported
// (ADR 0006 forbids only internal/modules). Turning the same data into JSON and
// decoding it again would have needlessly loosened a boundary the compiler is
// able to check.
type CartTotals interface {
	// CalculateTotals RECOMPUTES the totals of the cart and writes them to the
	// cart.
	//
	// On a completed cart it returns errors.Conflict. The result carries the unit
	// price, the subtotal, the discount and the tax per line; the lines of the
	// order are built from it.
	CalculateTotals(ctx context.Context, cartID string) (cartwf.Totals, error)
}

// Inventory is the surface of the inventory module ("inventory.interop") used
// by this package.
//
// AvailableQuantity is DELIBERATELY absent: an "is there enough" read performed
// before reserving is a race-prone copy of the check [Inventory.Reserve] does
// inside a transaction and under a lock. Between the read and the reservation
// another cart can take the last unit; in that case the pre-check only moves
// the error somewhere else, it does not prevent it.
//
// [Inventory.LocationsWithStock] is NOT an exception to that ban, because it
// asks a different question: the only authority on the "is there enough"
// question is still Reserve, and the question the list answers is "WHERE".
// Without its answer this workflow could not produce a single warehouse name
// and reporting the location would REMAIN mandatory for the caller (see
// [CompleteCartInput.LocationID]).
type Inventory interface {
	// LocationsWithStock returns the identifiers of the locations from which at
	// least quantity units of the item CAN BE RESERVED.
	//
	// What comes back is a CANDIDATE list, not a guarantee: the list is read
	// without a lock and between two calls another cart can take the last unit.
	// In that case the error is reported the way it is today — through Reserve's
	// errors.Conflict — that is, the list takes over no check at all.
	//
	// If no location suffices it returns an empty slice, NOT AN ERROR: "this
	// cannot be ordered" is not a decision of the inventory module and this
	// package is the one that draws that conclusion (see
	// [reserveInventoryStep.locationFor]).
	//
	// The order is deterministic but it is not a PREFERENCE order; the candidates
	// are put into preference order by [Fulfillment.RankLocations]. That
	// distinction is the reason this surface exists: hiding policy in an ordering
	// would make the decision in a place nobody looks at — in the ordering of the
	// inventory module.
	LocationsWithStock(ctx context.Context, inventoryItemID string, quantity int64) ([]string, error)

	// Reserve reserves the inventory and returns the reservation identifier.
	//
	// If there is not enough stock it returns errors.Conflict and the saga
	// interprets that as "this cannot be ordered".
	Reserve(
		ctx context.Context,
		inventoryItemID, locationID string,
		quantity int64,
		lineItemID string,
	) (reservationID string, err error)

	// ReleaseReservation releases the reserved inventory; it is the SAGA
	// COMPENSATION and it is IDEMPOTENT. An already released reservation does not
	// fail on a second call; an unknown identifier returns errors.NotFound.
	ReleaseReservation(ctx context.Context, reservationID string) error

	// ConfirmReservation turns the reservation into deducted inventory. After
	// this point the inventory cannot be released; a refund is a separate
	// workflow.
	ConfirmReservation(ctx context.Context, reservationID string) error
}

// Fulfillment is the surface of the fulfillment module ("fulfillment.interop")
// used by this package.
//
// The surface has a SINGLE method and that is deliberate: this workflow does
// NOT open a shipment. A shipment is a separate job that starts after an order
// whose payment has been taken; adding it to the saga would have meant carrying
// a call whose compensation is in the hands of the carrier beyond the pivot
// (see the package comment, "The point of no return").
//
// The only question asked here is this: from WHICH warehouse will the line's
// inventory be reserved. The answer to that question is a FULFILLMENT decision
// — today it looks at whether the warehouse serves the destination region and
// at the operator's preference order — whereas "which warehouses have enough
// stock" is an INVENTORY FACT and comes from [Inventory.LocationsWithStock].
// Gathering the two into a single surface would have made the inventory query
// depend on fulfillment policy, or fulfillment policy depend on the inventory
// schema.
type Fulfillment interface {
	// RankLocations puts the candidates into PREFERENCE ORDER: the shipment
	// leaves from the first one.
	//
	// destinationRegionID is the fulfillment region the shipment is going to and
	// it is MANDATORY; this package supplies it from the plan. The module knows
	// from its own records whether the warehouse serves that region, so this
	// package does not carry a POLICY, it carries the policy's QUESTION. If it is
	// given empty the module returns errors.Invalid.
	//
	// # Mandatory invariant: the returned slice is a SUBSET of the input
	//
	// The elements are EXACTLY the same strings as the elements of
	// candidateLocationIDs and none of them appears twice. Not a normalized copy
	// and not a counterpart read from a policy table: this package looks the
	// result up in its own candidate ledger and if it cannot find it, it fails
	// the workflow with errors.Internal (see [reserveInventoryStep.rankCandidates]).
	//
	// # The order is asked for ONCE
	//
	// After a depleted warehouse this surface is NOT called again; the caller
	// takes the next candidate from its own list (see
	// [reserveInventoryStep.reserveLine]). That is the reason an order is
	// returned rather than a single location: asking again on every depletion
	// would mean reading the same records over and over for the same order —
	// since the order is deterministic those reads would already produce the same
	// answer — and each one would widen the race window between reading the
	// candidates WITHOUT A LOCK and doing the reservation UNDER A LOCK.
	//
	// The order is DETERMINISTIC with the same candidates AND the same policy
	// records, and it is independent of the order in which the candidates arrive.
	// If the operator changes the policy between two calls the order changes too;
	// the determinism claim does NOT COVER that and does not need to, because
	// that is the expected outcome of a setting. It is also not possible for the
	// order to change WITHIN one execution: a single call is made per line.
	//
	// The candidate list is not given EMPTY by this package (see
	// [reserveInventoryStep.locationFor]); if it is, the module returns
	// errors.Conflict. It is also possible for the module to eliminate candidates
	// — today it eliminates the warehouses that do not serve the destination
	// region — and if none of them is left the error is again errors.Conflict:
	// the caller meets it in the SAME branch as insufficient stock ("this cannot
	// be ordered"). The importance of the kind is NOT in this package's
	// branching, it is in two other places: the HTTP counterpart of the error
	// derives from the kind, and the engine's default retry predicate does NOT
	// RETRY KindConflict. An eliminated candidate set does not change by trying
	// again.
	//
	// The module's own code is PRESERVED and it is that code which reaches the
	// client: while the step error is wrapped by [reserveInventoryStep.unwind]
	// the code is inherited. [CodeReservationFailed] is only a fallback for an
	// error without a code.
	RankLocations(
		ctx context.Context,
		destinationRegionID string,
		candidateLocationIDs []string,
	) (orderedLocationIDs []string, err error)
}

// Orders is the surface of the order module ("order.interop") used by this
// package.
//
// CompleteOrder is DELIBERATELY absent. There are two reasons: (1) a completed
// order CANNOT BE CANCELED, so the moment the saga called it, it would make its
// own compensation impossible; (2) the "completed" stamp of an order is the
// outcome of the delivery, not of the payment, and in the plan it belongs to
// fulfillment in Phase 7. The order leaves this workflow as "pending".
type Orders interface {
	// PlaceOrderJSON opens an order from the snapshot of the cart and returns its
	// identifier.
	//
	// The body is in the [orderSnapshot] schema. If "idempotency_key" in the
	// snapshot is non-empty the call is IDEMPOTENT: a second call with the same
	// key does not open a new order.
	PlaceOrderJSON(ctx context.Context, snapshot json.RawMessage) (orderID string, err error)

	// CancelOrder cancels the order; it is the SAGA COMPENSATION and it is
	// IDEMPOTENT. Canceling a completed order returns errors.Conflict.
	CancelOrder(ctx context.Context, orderID, reason string) error

	// SetOrderSummaryTotals records on the order how much was collected and how
	// much was refunded.
	//
	// The write is a MERGE — for each field the larger of the recorded and the
	// reported value is kept — so calling it twice cannot shrink a total and the
	// call is safe to repeat.
	SetOrderSummaryTotals(ctx context.Context, orderID string, paidTotal, refundedTotal int64) error
}

// Payments is the surface of the payment module ("payment.interop") used by
// this package.
//
// Refund is DELIBERATELY absent: a refund is not the compensation of a capture,
// it is a separate workflow (see the package comment, "The point of no
// return"). SessionStatus is absent too; the saga does NOT ASK for the status
// of the session, it looks at the amounts (see [Payments.Collection]).
type Payments interface {
	// CreateCollection opens a payment collection for a reference and returns its
	// identifier. The amount must be POSITIVE.
	CreateCollection(ctx context.Context, reference, currencyCode string, amount int64) (collectionID string, err error)

	// OpenSessionWithData opens a payment session at a provider for the
	// collection and returns the identifier of the session.
	//
	// data is a free-form JSON object passed to the provider as is (card token,
	// return address, test behavior keys). A second call with the same
	// idempotencyKey does NOT open a NEW session.
	OpenSessionWithData(
		ctx context.Context,
		collectionID, providerID, idempotencyKey string,
		data json.RawMessage,
	) (sessionID string, err error)

	// Authorize authorizes the session; it returns the NEW status of the session
	// and the amount actually AUTHORIZED. If the provider declines it returns an
	// error.
	Authorize(ctx context.Context, sessionID string) (status string, authorized int64, err error)

	// Capture captures the authorized amount and returns the identifier of the
	// capture. If amount is zero the WHOLE authorized amount is drawn.
	Capture(ctx context.Context, sessionID string, amount int64) (paymentID string, err error)

	// Cancel cancels the session and releases the authorization hold; it is the
	// SAGA COMPENSATION and it is IDEMPOTENT. A session that has been captured
	// CANNOT be canceled (errors.Conflict).
	Cancel(ctx context.Context, sessionID string) error

	// Collection returns the current status and the AMOUNTS of the collection.
	//
	// The returned values are, in order, the status, the amount that must be
	// collected, the authorized, the captured and the refunded amount (all in
	// minor units).
	//
	Collection(ctx context.Context, collectionID string) (
		status string,
		amount, authorized, captured, refunded int64,
		err error,
	)
}

// Links is the surface of the core's Module Links service ("core.link") used by
// this package.
//
// There is only a BATCH read: the same path is used for a single row as well,
// and so the number of queries does not change as the number of lines grows
// (there is no N+1).
type Links interface {
	// ListMany returns the links of the given source identifiers in a SINGLE
	// query.
	ListMany(ctx context.Context, name string, fromIDs []string) (map[string][]string, error)

	// Create binds fromID to toID under the given definition.
	//
	// A link that already exists is NOT an error: the definition's cardinality
	// makes a duplicate impossible, so a repeated call reaches the same state
	// as the first. That matters here because a saga step can be attempted more
	// than once.
	Create(ctx context.Context, name, fromID, toID string) error
}

// Catalog is the surface of the core's Query layer ("core.query") used by this
// package (ADR 0004).
//
// The TITLE of the order line is read from here: the product module's service
// speaks in rich types and is therefore closed to cross-module calls, while
// Query exists for exactly that gap.
type Catalog interface {
	// Graph fetches the root records according to the spec and applies the
	// expansions.
	Graph(ctx context.Context, spec query.GraphSpec) ([]query.Record, error)
}

// Deps holds the dependencies of the workflow.
type Deps struct {
	// Carts is the cart surface; it is mandatory.
	Carts Carts
	// Totals is the cart totals surface; it is mandatory.
	Totals CartTotals
	// Inventory is the inventory surface; it is mandatory.
	Inventory Inventory
	// Fulfillment is the fulfillment surface; it is mandatory.
	//
	// On requests where the caller reports the location it is never called, but
	// it is MANDATORY nonetheless: if the dependency is not checked AT SETUP, a
	// mis-wired installation would show its absence only on the first request
	// without a location — that is, on the customer's checkout page.
	Fulfillment Fulfillment
	// Orders is the order surface; it is mandatory.
	Orders Orders
	// Payments is the payment surface; it is mandatory.
	Payments Payments
	// Links is the Module Links surface; it is mandatory.
	Links Links
	// Catalog is the Query surface; it is mandatory.
	Catalog Catalog
	// Executor is the saga engine; it is mandatory.
	//
	// An in-memory engine (workflow.NewInMemory) is only for tests: idempotency
	// protection then stays within the process boundary and two replicas can
	// order the same cart at the same time.
	Executor workflow.Executor
	// Logger discards the logs if it is given as nil.
	Logger *slog.Logger
}

// Workflows is the type that runs the order completion workflow. It is safe for
// concurrent use.
type Workflows struct {
	carts       Carts
	totals      CartTotals
	inventory   Inventory
	fulfillment Fulfillment
	orders      Orders
	payments    Payments
	links       Links
	catalog     Catalog
	executor    workflow.Executor
	log         *slog.Logger
}

// New builds the workflow with the given dependencies.
//
// A missing dependency returns an error at SETUP time; no nil check is done at
// run time. Leaving the absence to the first call would have meant that a
// mis-wired installation blows up only on the customer's checkout page.
func New(deps Deps) (*Workflows, error) {
	for _, dep := range []struct {
		name    string
		missing bool
	}{
		{ServiceCart, deps.Carts == nil},
		{serviceCartTotals, deps.Totals == nil},
		{ServiceInventory, deps.Inventory == nil},
		{ServiceFulfillment, deps.Fulfillment == nil},
		{ServiceOrder, deps.Orders == nil},
		{ServicePayment, deps.Payments == nil},
		{ServiceLink, deps.Links == nil},
		{ServiceQuery, deps.Catalog == nil},
		{ServiceWorkflow, deps.Executor == nil},
	} {
		if dep.missing {
			return nil, errors.Internal(CodeNotReady,
				"the order completion workflow cannot be built without the %q surface", dep.name)
		}
	}

	log := deps.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Workflows{
		carts:       deps.Carts,
		totals:      deps.Totals,
		inventory:   deps.Inventory,
		fulfillment: deps.Fulfillment,
		orders:      deps.Orders,
		payments:    deps.Payments,
		links:       deps.Links,
		catalog:     deps.Catalog,
		executor:    deps.Executor,
		log:         log,
	}, nil
}

// serviceCartTotals is the name of the totals surface in the error messages.
//
// There is NO such registration in the container and there should not be: the
// totals flow is not a module service but a sibling workflow package built on
// the same container (see [CartTotals]). The name exists only to give the
// missing dependency a name.
const serviceCartTotals = "workflows.cart"

// FromContainer resolves the dependencies from the container by NAME and builds
// the workflow (ADR 0006).
//
// The resolution order is FIXED by registration name: if more than one service
// is missing or incompatible the same error is returned on every run and the
// diagnosis becomes reproducible. The incompatibility error writes both the
// registered concrete type and the expected interface (see container.Resolve).
//
// The cart totals are not resolved by NAME, they are BUILT on the same
// container: the totals flow is not a service registered in the container but a
// sibling workflow package that resolves its own dependencies, again by their
// names.
func FromContainer(c *container.Container) (*Workflows, error) {
	if c == nil {
		return nil, errors.Internal(CodeNotReady,
			"the order completion workflow cannot be built without a container")
	}

	carts, err := resolve[Carts](c, ServiceCart)
	if err != nil {
		return nil, err
	}
	inventory, err := resolve[Inventory](c, ServiceInventory)
	if err != nil {
		return nil, err
	}
	fulfillment, err := resolve[Fulfillment](c, ServiceFulfillment)
	if err != nil {
		return nil, err
	}
	orders, err := resolve[Orders](c, ServiceOrder)
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
	catalog, err := resolve[Catalog](c, ServiceQuery)
	if err != nil {
		return nil, err
	}
	executor, err := resolve[workflow.Executor](c, ServiceWorkflow)
	if err != nil {
		return nil, err
	}

	totals, err := cartwf.FromContainer(c)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeDependencyMissing,
			"the order completion workflow could not build the cart totals")
	}

	return New(Deps{
		Carts:       carts,
		Totals:      totals,
		Inventory:   inventory,
		Fulfillment: fulfillment,
		Orders:      orders,
		Payments:    payments,
		Links:       links,
		Catalog:     catalog,
		Executor:    executor,
		// The application sets the logger up with slog.SetDefault at startup; the
		// workflow does not look for a separate logger registration.
		Logger: slog.Default().With("workflow", WorkflowName),
	})
}

// resolve resolves a single service and wraps its error PRESERVING ITS KIND.
//
// Preserving the kind is essential: an unregistered name must stay NotFound
// (404) and an incompatible type must stay Invalid (422). Turning them all into
// Internal would have made a fixable wiring error look like a server fault.
func resolve[T any](c *container.Container, name string) (T, error) {
	value, err := container.Resolve[T](c, name)
	if err != nil {
		var zero T
		return zero, errors.Wrap(err, errors.KindOf(err), CodeDependencyMissing,
			"the order completion workflow could not resolve the %q service", name)
	}
	return value, nil
}
