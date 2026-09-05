// Package order is the order module (plan Section 6, Phase 6).
//
// Its responsibility in one sentence: to know permanently WHAT an order is —
// under which number, in which region, on whose behalf, with which lines and at
// which amount it was placed. The module is the SOLE writer of Order,
// OrderLineItem, OrderSummary, Return, Exchange and Claim data (Principle 2.3).
//
// # What it does not know
//
// It does not know the cart. The input given to [service.Service.CreateOrder]
// is the cart's SNAPSHOT: the lines and the totals arrive already computed. The
// flow that builds the snapshot is the complete_cart WORKFLOW (plan Section
// 2.5, ADR 0006); this module does not call cart and does not import it.
//
// It does not know payment either: the captured and the refunded amount are
// written over OrderSummary, by the flow that knows the payment result.
//
// It does not know the spending LIMIT either: the limit is the b2b module's
// data and it reaches this module over a narrow surface resolved under the name
// [SpendingPolicyName]. What it does know is SPENDING itself — the total of the
// orders that were placed — and this is why the side that applies the limit to
// that total is here; the rule closes against the race when it is applied
// INSIDE the transaction the order is written in (see [service.SpendingPolicy]).
// The dependency is OPTIONAL: if b2b is not installed, no limit is applied.
//
// It does not know WHO PLACED THE ORDER either, and it cannot find out.
// customer_id reaches this module as the storefront's CLAIM; the identity of a
// store surface is a sales channel, not a customer. Because the spending limit
// hangs on that claim, the rule is NOT APPLIED to a purchase that does not
// declare its customer — not a hole, but a RESPONSIBILITY the framework leaves
// to the embedding application, and it is settled in ADR 0008.
//
// The module imports NO other module (Principle 2.1/2.4, ADR 0001; the rule is
// enforced by depguard in .golangci.yml and by the internal/arch tests).
// region_id, customer_id, cart_id and variant_id are other modules' ids; they
// are stored as free text and are given no foreign key (Principle 2.2).
//
// # The surfaces it exposes
//
//   - "order.service" — in-module use and reading with rich types.
//   - "order.interop" — the PRIMITIVE surface the saga and the "order.placed"
//     subscribers use (ADR 0006). complete_cart opens the order here and
//     cancels it here in compensation; the notification side reads the e-mail
//     that is NOT in the event here, with [service.Interop.OrderContactJSON].
//   - "order.query" — the read provider opened to the Query layer (ADR 0004).
//   - /admin/v1/orders … — the admin API (reads + status transitions).
//   - /store/v1/orders/{id} — the customer API (READ only).
//
// # The events it publishes
//
// "order.placed" — when an order is created (plan Phase 6 DoD). For its payload
// and its publication policy see [service.EventOrderPlaced] and
// service/events.go.
//
// # The links it declares
//
// None. An order's region and customer stand IN THEIR OWN COLUMNS and every
// read is made from those columns (see queries/orders.sql); holding the same
// relation in a link table as well would have written a row, would have caused
// maintenance cost and would have served no read (see the CHANGELOG,
// "order_customer/order_region removed"). The "order_payment" and
// "order_fulfillment" bindings are not owned by this module either: a link
// definition can be declared only ONCE (ADR 0005), and the definition is
// declared by the side that writes the record the binding carries — payment,
// fulfillment.
package order

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/order/api"
	"github.com/bdrtr/gobit/internal/modules/order/repository"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// ModuleName is the module's name; it is the prefix of the container names and
// of the migration version ledger.
const ModuleName = "order"

// ServiceName is the module service's name in the container.
//
// Other modules and workflows reach the order service under this name (WITHOUT
// importing this package, as ADR 0001/0006 requires) and use it through a
// narrow interface they define in THEIR OWN packages.
const ServiceName = ModuleName + ".service"

// InteropName is the cross-module primitive surface's name in the container
// (ADR 0006).
//
// It is registered SEPARATELY from the service itself: the service speaks in
// order's rich types, this surface only in primitive and stdlib types. The
// complete_cart saga resolves it with the narrow interface it defines itself.
const InteropName = ModuleName + ".interop"

// ProviderName is the Query provider's name in the container (ADR 0004).
const ProviderName = service.EntityName + query.ProviderSuffix

// The names of the core services resolved from the container.
const (
	svcDB       = "core.db"
	svcEventBus = "core.eventbus"
	// svcQuery is the core cross-module read layer's name in the container
	// (ADR 0004). The order reads its payment through it and only through it.
	svcQuery = "core.query"
	// returnFlowName is the return flow's name in the container.
	//
	// The value is declared by internal/workflows/returns and REPEATED here:
	// this module cannot import that package (ADR 0006 holds in both
	// directions).
	returnFlowName = "workflows.returns.interop"
	// invoicingFlowName is the invoicing flow's name in the container.
	//
	// The value is declared by internal/workflows/invoicing and REPEATED here
	// for the same reason: this module cannot import that package (ADR 0006
	// holds in both directions).
	invoicingFlowName = "workflows.invoicing.interop"
	// fulfillingFlowName is the fulfilling flow's name in the container.
	fulfillingFlowName = "workflows.fulfilling.interop"
)

// SpendingPolicyName is the container name of the service that publishes the
// spending limit rule (ADR 0001).
//
// The name belongs to the b2b module and is repeated here as a STRING; modules
// cannot import each other (Principle 2.4) and the price of the repetition is
// the accepted price of isolation. A typo does not stay silent: if the name
// cannot be resolved, the module concludes that b2b was never installed and no
// limit is applied — which is why, should the name change, the "not installed"
// branch in the [spendingPolicy] documentation would fire wrongly. The single
// source of truth for the name is the b2b module's InteropName constant.
const SpendingPolicyName = "b2b.interop"

// codeSetupFailed is the error code reporting that the module could not be
// wired.
const codeSetupFailed = "order_module_setup_failed"

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot is the embedded files with the "migrations/" prefix stripped:
// db.Migrate reads the source from the root.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// Module is the application the order module offers the core.
type Module struct {
	svc     *service.Service
	handler *api.Handler
}

// That the core's contract is satisfied is pinned down at compile time.
var _ module.Module = (*Module)(nil)

// That it can describe itself for the document is pinned down at compile time
// too.
//
// [openapi.Describer] is an OPTIONAL interface and the composition root looks
// for it with a TYPE ASSERTION; should the method name or signature drift,
// nothing would break at compile time — only the order endpoints would silently
// fall out of the document. This line closes that silence.
var _ openapi.Describer = (*Module)(nil)

// New produces an order module ready to be registered.
//
// Dependencies are resolved during Register, not here: until that moment the
// container may not have set up the core services.
func New() *Module {
	return &Module{}
}

// Name returns the module's unique name.
func (m *Module) Name() string { return ModuleName }

// Migrations returns the module's migration files.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Register registers the service, the interop surface and the Query provider
// with the container.
//
// Only CORE services are resolved; other modules' services may not be
// registered yet at this stage (see the module.Module documentation). Because
// core.db and core.eventbus are registered in main.go as ready values before
// the modules come up, resolving them here is safe, and their absence is a
// setup error that makes the module unable to run at all — it is not silently
// deferred.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, svcDB)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the database pool (%q)", ModuleName, svcDB)
	}
	// It is resolved through a narrow interface: the module only PUBLISHES, it
	// does not subscribe and does not close the bus (see service.EventPublisher).
	bus, err := container.Resolve[service.EventPublisher](c, svcEventBus)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the event bus (%q)", ModuleName, svcEventBus)
	}

	// At startup the application sets up the logger configured with
	// slog.SetDefault; the module does not look for a separate logger
	// registration.
	log := slog.Default().With("module", ModuleName)

	queryLayer, err := container.Resolve[query.Query](c, svcQuery)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the query layer (%q)", ModuleName, svcQuery)
	}

	svc, err := service.New(service.Options{
		Repo:   repository.New(pool.Pool()),
		Events: bus,
		// The spending rule's provider is ANOTHER module and may not be
		// registered yet at this stage; the resolution is deferred to first use
		// (see [spendingPolicy] and the module.Module documentation).
		Spending: &spendingPolicy{c: c, log: log},
		// The Query layer is a CORE service, so it can be resolved right here:
		// the deferral the spending rule needs is about another MODULE not
		// being registered yet, and that does not apply to core.
		Catalog: queryLayer,
		Logger:  log,
	})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s service could not be set up", ModuleName)
	}

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}
	// The cross-module surface is registered under a SEPARATE name: the service
	// itself speaks in order's rich types, this surface only in primitive types
	// (ADR 0006).
	if err := c.Provide(InteropName, service.NewInterop(svc)); err != nil {
		return err
	}
	// The provider name has the form "<entity>.query"; Query looks it up under
	// that name and verifies with Entity() that the name matches (ADR 0004).
	if err := c.Provide(ProviderName, service.NewQueryProvider(svc)); err != nil {
		return err
	}

	m.svc = svc
	// The return flow is resolved at REQUEST TIME for the reason the spending
	// rule is: a flow is born after the whole Register loop has finished, while
	// the handler is built inside it.
	m.handler = api.New(svc,
		&returnReceiving{c: c, log: log},
		&invoicingFlow{c: c, log: log},
		&fulfillingFlow{c: c, log: log})
	slog.Default().DebugContext(ctx, "order module registered",
		"service", ServiceName, "interop", InteropName, "provider", ProviderName)
	return nil
}

// Routes mounts the module's store and admin endpoints on the router.
//
// If Register did not run, no endpoint is mounted: rather than a handler
// without a service panicking on the first request, it is better for the
// endpoint not to exist at all.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
		slog.Default().Warn("Routes was called on the order module without Register, no route was mounted")
		return
	}
	m.handler.Routes(r)
}

// Describe writes the module's admin endpoints into the OpenAPI document.
//
// The description itself lives in [api.Describe]: the body schemas are derived
// from that package's unexported DTOs, and exporting those types only for the
// sake of the document would widen the module's surface. Which endpoints are
// not described and WHY they are not described is written there too.
//
// Unlike [Module.Routes] there is NO Register check, and none is needed: the
// schema comes from the types, not from the service. Putting a check there
// would silently empty the document of an unregistered module too.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// Service returns the module's service; it is nil if Register was not called.
//
// It is meant for tests and embedded use; in the normal flow the service is
// resolved from the container under the name [ServiceName].
func (m *Module) Service() *service.Service { return m.svc }

// mustSub opens the subdirectory; it panics if it cannot be opened.
//
// The panic is safe here: the directory name is constant at compile time and
// the embed directive has already verified at compile time that the files
// exist. Returning nil silently would nevertheless mean the module coming up
// without migrations (that is, without tables); a setup error must blow up
// openly.
func mustSub(files embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic("order: could not open the embedded migrations directory: " + err.Error())
	}
	return sub
}

// returnReceiving is the wrapper that resolves the return flow ON FIRST USE.
//
// It fails CLOSED: without the flow a return is not received at all. Recording
// the receipt and skipping the stock would put the goods in the warehouse and
// leave the count saying they are not there, with a record claiming success.
type returnReceiving struct {
	c    *container.Container
	log  *slog.Logger
	once sync.Once
	svc  api.ReturnReceiving
	err  error
}

// That the wrapper satisfies the surface the handler expects is pinned down at
// compile time.
var _ api.ReturnReceiving = (*returnReceiving)(nil)

// ReceiveReturn records the receipt and puts the stock back.
func (p *returnReceiving) ReceiveReturn(
	ctx context.Context, returnID, locationID string,
) (restockedLines int, restockedUnits int64, warnings []string, err error) {
	p.once.Do(func() { p.resolve(ctx) })
	if p.err != nil {
		return 0, 0, nil, p.err
	}

	return p.svc.ReceiveReturn(ctx, returnID, locationID)
}

// RefundReturn sends money back for a received return.
func (p *returnReceiving) RefundReturn(
	ctx context.Context, returnID string, amount int64, reason string,
) (refunded int64, summaryRecorded bool, warnings []string, err error) {
	p.once.Do(func() { p.resolve(ctx) })
	if p.err != nil {
		return 0, false, nil, p.err
	}

	return p.svc.RefundReturn(ctx, returnID, amount, reason)
}

// SettleClaim settles a claim by refunding it.
func (p *returnReceiving) SettleClaim(
	ctx context.Context, claimID string, amount int64, reason string,
) (refunded int64, summaryRecorded bool, warnings []string, err error) {
	p.once.Do(func() { p.resolve(ctx) })
	if p.err != nil {
		return 0, false, nil, p.err
	}

	return p.svc.SettleClaim(ctx, claimID, amount, reason)
}

// resolve looks the flow up in the container and remembers the outcome.
func (p *returnReceiving) resolve(ctx context.Context) {
	svc, err := container.Resolve[api.ReturnReceiving](p.c, returnFlowName)
	if err != nil {
		p.err = errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"the %s module could not resolve the return flow (%q); a return cannot be "+
				"received without the stock going back", ModuleName, returnFlowName)

		return
	}
	p.svc = svc
	p.log.InfoContext(ctx, "return receiving flow bound", "flow", returnFlowName)
}

// spendingPolicy is the wrapper that resolves the spending rule provider ON
// FIRST USE.
//
// # Why lazily
//
// The [module.Module] contract says that other modules' services may not be
// registered yet during Register, and it requires the resolution to be deferred
// to first use. This is also why the registration order does not matter: even
// if the b2b module was added after order, by the time the first order is
// opened it has long been registered.
//
// # Why OPTIONAL
//
// If [SpendingPolicyName] is not registered at all, the b2b module is not
// installed; in that setup there is no such concept as a "spending limit" and
// the right answer is a rule without a rule. This is given by the
// [emptySpendingRule] body — returning a constant answer instead of handing the
// service a nil keeps "there is no policy" from being a state the service has
// to branch on.
//
// # But it does NOT go SILENTLY out of service
//
// If the name IS registered but does not satisfy the expected surface, an error
// is returned and no order is opened. This distinction matters: "b2b is not
// installed" is a setup decision, whereas "b2b is installed but its surface is
// not recognized" is a wiring error, and silently turning that into unlimited
// purchasing would mean the rule shutting down in exactly the setup that needs
// it most. The decision is made ONCE and stored; resolving it again on every
// order would do nothing but reproduce the same error forever.
type spendingPolicy struct {
	c    *container.Container
	log  *slog.Logger
	once sync.Once
	svc  service.SpendingPolicy
	err  error
}

// emptySpendingRule is the body of the answer "this customer has no spending
// rule".
//
// The schema is defined in the service.SpendingPolicy documentation; when the
// "limited" field is false the other fields are not read.
var emptySpendingRule = json.RawMessage(`{"limited":false}`)

// That the wrapper satisfies the surface the service expects is pinned down at
// compile time.
var _ service.SpendingPolicy = (*spendingPolicy)(nil)

// SpendingLimitJSON returns the customer's spending rule.
func (p *spendingPolicy) SpendingLimitJSON(ctx context.Context, customerID string) (json.RawMessage, error) {
	p.once.Do(func() { p.resolve(ctx) })
	if p.err != nil {
		return nil, p.err
	}
	if p.svc == nil {
		return emptySpendingRule, nil
	}
	return p.svc.SpendingLimitJSON(ctx, customerID)
}

// resolve resolves the provider from the container; it stores the result once.
//
// The remaining branch (the name is registered but its surface is not
// recognized) is turned into KindInternal; the container's own kind — KindInvalid
// for a registration of the wrong type — is not passed through as it is. Were it
// inherited, the endpoint opening the order would say "your body is invalid"
// with a 422, whereas the request would get the same result even with a flawless
// body: the fault is in the SERVER CONFIGURATION. The same rationale is written
// in the cart module's flow wrappers too.
func (p *spendingPolicy) resolve(ctx context.Context) {
	svc, err := container.Resolve[service.SpendingPolicy](p.c, SpendingPolicyName)
	switch {
	case err == nil:
		p.svc = svc
		p.log.InfoContext(ctx, "spending limit rule bound",
			"provider", SpendingPolicyName)
	case errors.IsNotFound(err):
		// The b2b module is not in the setup: neither is the concept of a limit.
		p.log.DebugContext(ctx, "the spending limit provider is not registered, no limit will be applied",
			"provider", SpendingPolicyName)
	default:
		p.err = errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"the %s module could not resolve the spending rule provider (%q)", ModuleName, SpendingPolicyName)
	}
}

// invoicingFlow is the wrapper that resolves the invoicing flow ON FIRST USE.
//
// It fails CLOSED for the reason the return wrapper does, minus the ambiguity:
// there is no second path to a document, so a missing flow can only mean the
// order is not invoiced.
type invoicingFlow struct {
	c    *container.Container
	log  *slog.Logger
	once sync.Once
	svc  api.Invoicing
	err  error
}

// That the wrapper satisfies the surface the handler expects is pinned down at
// compile time.
var _ api.Invoicing = (*invoicingFlow)(nil)

// IssueForOrder issues the document for the order, or returns the one it has.
func (p *invoicingFlow) IssueForOrder(
	ctx context.Context, orderID string, request json.RawMessage,
) (invoiceID, number string, alreadyIssued bool, err error) {
	p.once.Do(func() { p.resolve(ctx) })

	if p.err != nil {
		return "", "", false, p.err
	}

	return p.svc.IssueForOrder(ctx, orderID, request)
}

// InvoiceOfOrder returns the identity of the document bound to the order.
func (p *invoicingFlow) InvoiceOfOrder(
	ctx context.Context, orderID string,
) (invoiceID, number, status string, err error) {
	p.once.Do(func() { p.resolve(ctx) })

	if p.err != nil {
		return "", "", "", p.err
	}

	return p.svc.InvoiceOfOrder(ctx, orderID)
}

// resolve looks the flow up in the container and remembers the outcome.
func (p *invoicingFlow) resolve(ctx context.Context) {
	svc, err := container.Resolve[api.Invoicing](p.c, invoicingFlowName)
	if err != nil {
		p.err = errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"the %s module could not resolve the invoicing flow (%q); an order cannot be "+
				"invoiced without it", ModuleName, invoicingFlowName)

		return
	}

	p.svc = svc
	p.log.InfoContext(ctx, "invoicing flow bound", "flow", invoicingFlowName)
}

// fulfillingFlow resolves the fulfilling flow LAZILY, on the first request.
//
// The reason is the one the invoicing wrapper gives: a flow is born after the
// whole Register loop has finished, while the handler is built inside it. The
// outcome is remembered either way — a resolution that failed once will fail
// again, and retrying it per request would turn a setup fault into a load.
type fulfillingFlow struct {
	c    *container.Container
	log  *slog.Logger
	once sync.Once
	svc  api.Fulfilling
	err  error
}

// That the wrapper satisfies the surface the handler expects is pinned down at
// compile time.
var _ api.Fulfilling = (*fulfillingFlow)(nil)

// OpenForOrder opens a shipment for the order and binds the two.
func (p *fulfillingFlow) OpenForOrder(
	ctx context.Context, orderID string, request json.RawMessage,
) (fulfillmentID string, alreadyOpen bool, err error) {
	p.once.Do(func() { p.resolve(ctx) })

	if p.err != nil {
		return "", false, p.err
	}

	return p.svc.OpenForOrder(ctx, orderID, request)
}

// ShipmentsOfOrderJSON lists the shipments bound to the order.
func (p *fulfillingFlow) ShipmentsOfOrderJSON(
	ctx context.Context, orderID string,
) (json.RawMessage, error) {
	p.once.Do(func() { p.resolve(ctx) })

	if p.err != nil {
		return nil, p.err
	}

	return p.svc.ShipmentsOfOrderJSON(ctx, orderID)
}

// resolve looks the flow up in the container and remembers the outcome.
func (p *fulfillingFlow) resolve(ctx context.Context) {
	svc, err := container.Resolve[api.Fulfilling](p.c, fulfillingFlowName)
	if err != nil {
		p.err = errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"the %s module could not resolve the fulfilling flow (%q); a shipment cannot be "+
				"opened for an order without it", ModuleName, fulfillingFlowName)

		return
	}

	p.svc = svc
	p.log.InfoContext(ctx, "fulfilling flow bound", "flow", fulfillingFlowName)
}
