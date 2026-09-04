// Package cart is the cart module (plan Section 6, Phase 5).
//
// Its responsibility in one sentence: to know WHAT a cart holds — in which
// region, on whose behalf, with which lines, with which address and shipping
// method. The module is the SOLE writer of Cart, LineItem, CartAddress and
// ShippingMethod data (Principle 2.3).
//
// # What it does not know
//
// It does not compute HOW MUCH the cart COMES TO. The price is pricing's data,
// the tax region/tax's; the flow that brings the two together is the
// calculate_totals WORKFLOW (plan Section 2.5, ADR 0006). This module calls no
// price/tax source; it only receives the totals through
// [service.Service.SetTotals], VALIDATES them and stores them.
//
// The module imports NO other module (Principle 2.1/2.4, ADR 0001; the rule is
// enforced by depguard in .golangci.yml and by the internal/arch tests).
// region_id, customer_id and variant_id are other modules' ids; they are stored
// as free text and no foreign key is given (Principle 2.2).
//
// # The surfaces it exposes
//
//   - "cart.service" — the service for cross-module calls and workflows.
//   - "cart.interop" — the PRIMITIVE surface the flows use (ADR 0006). The
//     complete_cart saga closes the cart through it.
//   - "cart.query" — the read provider opened to the Query layer (ADR 0004).
//   - /store/v1/carts … — the customer API (the surface that builds the cart,
//     changes it and TURNS IT INTO AN ORDER).
//   - /admin/v1/carts — the admin API (READ ONLY).
//
// # The flows it uses
//
// All of the storefront's WRITING endpoints — opening a cart, adding a line,
// updating a line's quantity and completing the cart — have been delegated to
// cross-module FLOWS; the module resolves them from the container under the
// names [CartFlowsName] and [CartCompletionName], through narrow interfaces it
// defines in ITS OWN package (ADR 0001/0006). The rationale: the cart's region
// is region's data, the line's price pricing's, its title the catalog's, and
// the order order + payment + inventory's, and this module knows none of them.
//
// All three paths fail CLOSED: if the flow cannot be resolved, no cart is
// opened, no line is added, no order is created (see [cartOpening],
// [linePricing] and [cartCompletion]).
//
// # The module surface it uses
//
// There is none. There was one — the REGION — and the cart read its currency
// from it; today the cart-opening flow makes that derivation, so the single
// place where the module resolved another module by name is closed too.
//
// # The links it declares
//
// There are none. The cart's region and its customer stand in THEIR OWN
// COLUMNS and every read is made from those columns; holding the same relation
// in a link table as well would write a row, would create maintenance cost and
// would serve no read at all (see the CHANGELOG, "cart_customer/cart_region
// removed").
package cart

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/module"
	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/cart/api"
	"github.com/bdrtr/gobit/internal/modules/cart/repository"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// ModuleName is the module's name; it is the prefix of the container names and
// of the migration version ledger.
const ModuleName = "cart"

// ServiceName is the module service's name in the container.
//
// Other modules and workflows reach the cart service under this name (WITHOUT
// importing this package, as ADR 0001/0006 requires) and use it through a
// narrow interface they define in THEIR OWN packages.
const ServiceName = ModuleName + ".service"

// InteropName is the cross-module primitive surface's name in the container
// (ADR 0006).
//
// It is registered SEPARATELY from the service itself: the service speaks in
// cart's rich types, this surface only in primitive and stdlib types. The cart
// flows resolve it with the narrow interface they define themselves.
const InteropName = ModuleName + ".interop"

// ProviderName is the Query provider's name in the container (ADR 0004).
const ProviderName = service.EntityName + query.ProviderSuffix

// CartFlowsName is the cart flows' name in the container (ADR 0001/0006).
//
// ONE name feeds TWO narrow interfaces ([api.CartOpening] and
// [api.LinePricing]): both are satisfied by the same registration, the cart
// flows' cross-module surface. The constant's name is therefore the FLOW's
// name, not that of one of the interfaces — calling it "LinePricingName" while
// "opening a cart" is resolved from that same registration would separate what
// the code says from what it does.
//
// The name belongs to the internal/workflows/cart package and is repeated here
// as a STRING; modules cannot import workflow packages (ADR 0006, in both
// directions) and the price of the repetition is the accepted price of
// isolation. The same pattern is used in the order module's SpendingPolicyName
// constant.
//
// A typo does NOT STAY SILENT and, unlike b2b's, does not lead to a degradation
// either: if the name cannot be resolved, the cart-opening and line-adding
// endpoints fail closed (see [cartOpening] and [linePricing]). The single
// source of truth for the name is the cart flows' InteropName constant.
const CartFlowsName = "workflows.cart.interop"

// CartCompletionName is the cart completion flow's name in the container
// (ADR 0001/0006).
//
// The name belongs to the internal/workflows/checkout package and is repeated
// here for the same reason; the single source of truth for the name is that
// package's InteropName constant.
const CartCompletionName = "workflows.checkout.interop"

// svcDB is the database pool's name in the container.
const svcDB = "core.db"

// codeSetupFailed is the error code reporting that the module could not be
// wired.
const codeSetupFailed = "cart_module_setup_failed"

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot is the embedded files with the "migrations/" prefix stripped:
// db.Migrate reads the source from the root.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// Module is the application the cart module offers the core.
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
// nothing would break at compile time — only the cart's endpoints would
// silently fall out of the document. This line closes that silence.
var _ openapi.Describer = (*Module)(nil)

// New produces a cart module ready to be registered.
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

// Register registers the service and the Query provider with the container.
//
// Only CORE services are resolved; other modules' services may not be
// registered yet at this stage (see the module.Module documentation). Because
// core.db is registered in main.go as a ready value before the modules come up,
// resolving it here is safe, and its absence is a setup error that makes the
// module unable to run at all — it is not silently deferred.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, svcDB)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the database pool (%q)", ModuleName, svcDB)
	}

	// The application configures the logger with slog.SetDefault at startup;
	// the module does not look for a separate logger registration.
	log := slog.Default().With("module", ModuleName)

	svc, err := service.New(service.Options{
		Repo:   repository.New(pool.Pool()),
		Logger: log,
	})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s service could not be set up", ModuleName)
	}

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}
	// The provider name has the form "<entity>.query"; Query looks it up under
	// that name and verifies with Entity() that the name matches (ADR 0004).
	// The cross-module surface is registered under a SEPARATE name: the service
	// itself speaks in cart's rich types, this surface only in primitive types
	// (ADR 0006). The cart flows resolve it with their own narrow interfaces.
	if err := c.Provide(InteropName, service.NewInterop(svc)); err != nil {
		return err
	}
	if err := c.Provide(ProviderName, service.NewQueryProvider(svc)); err != nil {
		return err
	}

	m.svc = svc
	// The flows are NOT REGISTERED YET at this stage, and they cannot be: a flow
	// is set up by resolving all modules' services from the container, that is,
	// it is born after the WHOLE Register loop has finished. The handler, on the
	// other hand, needs the flow. The dependency circle is broken by deferring
	// the resolution to REQUEST TIME (see [cartOpening], [linePricing] and
	// [cartCompletion]); the order module applies the same pattern for its
	// spending limit rule.
	//
	m.handler = api.New(svc, api.Flows{
		Opening:  &cartOpening{c: c, log: log},
		Pricing:  &linePricing{c: c, log: log},
		Checkout: &cartCompletion{c: c, log: log},
		Shipping: &shippingPricing{c: c, log: log},
	})
	slog.Default().DebugContext(ctx, "cart module registered",
		"service", ServiceName, "provider", ProviderName)
	return nil
}

// Routes mounts the module's store and admin endpoints on the router.
//
// If Register did not run, no endpoint is mounted: rather than a handler
// without a service panicking on the first request, it is better for the
// endpoint not to exist at all.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
		slog.Default().Warn("Routes was called on the cart module without Register, no route was mounted")
		return
	}
	m.handler.Routes(r)
}

// Describe writes the module's storefront and admin endpoints into the OpenAPI
// document.
//
// The description itself lives in [api.Describe]: the body schemas are derived
// from that package's unexported DTOs, and exporting those types only for the
// sake of the document would widen the module's surface.
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
		panic("cart: could not open the embedded migrations directory: " + err.Error())
	}
	return sub
}

// linePricing is the wrapper that resolves the line pricing flow ON FIRST USE.
//
// # Why lazy
//
// The [module.Module] contract says that during Register other modules'
// services may not be registered yet. The dependency here is born even later:
// the flow is set up by resolving ALL modules' surfaces from the container,
// that is, after the Register loop has finished — whereas the handler is set up
// during Register. The circle is broken by deferring the resolution to the
// first request. Registration order thus becomes irrelevant and the composition
// root can register the flows AFTER the modules.
//
// # Why it fails CLOSED
//
// This is the OPPOSITE of the order module's spending limit wrapper
// (spendingPolicy), and the difference is deliberate. There, if the name is not
// registered the right answer is "no limit": in a store where b2b is not
// installed there is no such concept as a spending limit. Here, if the name is
// not registered the right answer is NOT "no price" — writing a line without a
// price into the cart (neither with the amount the client sent, nor with zero)
// would be selling goods for free, silently. The only right outcome is for the
// line NOT TO BE ADDED AT ALL; this is why an unresolvable name, and equally a
// registered type that does not satisfy the surface, returns an ERROR and the
// endpoint closes.
//
// The decision is made ONCE and stored. The flows are registered at startup,
// before the first request; a name that was not found at the first resolution
// will not be found on the later requests either, and retrying on every request
// would do nothing but reproduce the same error forever.
type linePricing struct {
	c    *container.Container
	log  *slog.Logger
	once sync.Once
	svc  api.LinePricing
	err  error
}

// That the wrapper satisfies the surface the handler expects is pinned down at
// compile time.
var _ api.LinePricing = (*linePricing)(nil)

// AddPricedLineItem adds a line to the cart and returns the line's id.
func (p *linePricing) AddPricedLineItem(
	ctx context.Context,
	cartID, variantID string,
	quantity int64,
	metadata json.RawMessage,
) (string, error) {
	p.once.Do(func() { p.resolve(ctx) })
	if p.err != nil {
		return "", p.err
	}
	return p.svc.AddPricedLineItem(ctx, cartID, variantID, quantity, metadata)
}

// SetLineItemQuantity writes the line's quantity and reports whether the line
// was removed.
func (p *linePricing) SetLineItemQuantity(
	ctx context.Context,
	cartID, lineItemID string,
	quantity int64,
) (bool, error) {
	p.once.Do(func() { p.resolve(ctx) })
	if p.err != nil {
		return false, p.err
	}
	return p.svc.SetLineItemQuantity(ctx, cartID, lineItemID, quantity)
}

// resolve resolves the flow from the container; it stores the result once.
//
// # The error kind is NOT INHERITED from the container
//
// The wrapping kind is the constant [errors.KindInternal]; the container's own
// kind (unregistered name → KindNotFound, registration of the wrong type →
// KindInvalid) is not passed through as is. The reason is that those kinds
// point at the wrong PARTY for the error: neither is something the client could
// fix, both are a SERVER CONFIGURATION failure.
//
// Had it been inherited, the line-adding endpoint would return 404. That has
// three separate costs: it tells the client "there is no such endpoint" and
// pushes the storefront into looking for a path that does not exist, the alert
// chain built on 5xx never rings, and intermediate layers can cache the 404 and
// keep the failure alive even AFTER the setup is fixed. A registration of the
// wrong type would say "your body is invalid" with 422; even when the body is
// flawless.
//
// The text the operator needs (which name could not be resolved, what became
// impossible) is PRESERVED in the error and does not leak to the client: the
// transport layer masks KindInternal bodies and writes the real chain only to
// the log (see corehttp.WriteError). The only machine-readable field the client
// sees remains [codeSetupFailed].
func (p *linePricing) resolve(ctx context.Context) {
	svc, err := container.Resolve[api.LinePricing](p.c, CartFlowsName)
	if err != nil {
		p.err = errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"the %s module could not resolve the line pricing flow (%q); a line "+
				"cannot be added without the server determining the price",
			ModuleName, CartFlowsName)
		return
	}
	p.svc = svc
	p.log.InfoContext(ctx, "line pricing flow bound", "flow", CartFlowsName)
}

// shippingPricing is the wrapper that resolves the shipping pricing flow ON
// FIRST USE.
//
// The laziness and the failing closed are [linePricing]'s, about the cart's
// other decided number. Until this flow existed the storefront handler took the
// shipping amount out of the request body, so a shopper could name their own
// delivery price; the flow is the only thing standing between that body and the
// cart, which is exactly why an unresolved flow must not degrade into using it.
type shippingPricing struct {
	c    *container.Container
	log  *slog.Logger
	once sync.Once
	svc  api.ShippingPricing
	err  error
}

// That the wrapper satisfies the surface the handler expects is pinned down at
// compile time.
var _ api.ShippingPricing = (*shippingPricing)(nil)

// AddQuotedShippingMethod attaches a shipping option at its quoted price.
func (p *shippingPricing) AddQuotedShippingMethod(
	ctx context.Context,
	cartID, shippingOptionID string,
	data json.RawMessage,
) (string, error) {
	p.once.Do(func() { p.resolve(ctx) })
	if p.err != nil {
		return "", p.err
	}

	return p.svc.AddQuotedShippingMethod(ctx, cartID, shippingOptionID, data)
}

// resolve looks the flow up in the container and remembers the outcome.
func (p *shippingPricing) resolve(ctx context.Context) {
	svc, err := container.Resolve[api.ShippingPricing](p.c, CartFlowsName)
	if err != nil {
		p.err = errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"the %s module could not resolve the shipping pricing flow (%q); a shipping "+
				"method cannot be added without the server determining the price",
			ModuleName, CartFlowsName)

		return
	}
	p.svc = svc
	p.log.InfoContext(ctx, "shipping pricing flow bound", "flow", CartFlowsName)
}

// cartCompletion is the wrapper that resolves the cart completion flow ON FIRST
// USE.
//
// The rationale for the laziness and for failing closed is the same as
// [linePricing]'s; here it is even plainer: if there is no flow there is no
// order, no payment and no stock reservation either, and there can be no
// shortcut called "consider the cart completed".
type cartCompletion struct {
	c    *container.Container
	log  *slog.Logger
	once sync.Once
	svc  api.CartCompletion
	err  error
}

// That the wrapper satisfies the surface the handler expects is pinned down at
// compile time.
var _ api.CartCompletion = (*cartCompletion)(nil)

// CompleteCartJSON turns the cart into an order.
func (p *cartCompletion) CompleteCartJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error) {
	p.once.Do(func() { p.resolve(ctx) })
	if p.err != nil {
		return nil, p.err
	}
	return p.svc.CompleteCartJSON(ctx, request)
}

// resolve resolves the flow from the container; it stores the result once.
//
// The rationale for not inheriting the error kind from the container is in the
// [linePricing.resolve] godoc and holds here word for word.
func (p *cartCompletion) resolve(ctx context.Context) {
	svc, err := container.Resolve[api.CartCompletion](p.c, CartCompletionName)
	if err != nil {
		p.err = errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"the %s module could not resolve the cart completion flow (%q)",
			ModuleName, CartCompletionName)
		return
	}
	p.svc = svc
	p.log.InfoContext(ctx, "cart completion flow bound", "flow", CartCompletionName)
}

// cartOpening is the wrapper that resolves the cart opening flow ON FIRST USE.
//
// The rationale for the laziness is the same as [linePricing]'s and the one for
// failing closed points the same way: if there is no flow, the right answer is
// NOT "a cart without a region" or "the region the client named". The cart's
// region selects the tax rate, and the currency derived from it selects which
// price list is applied; dropping either to a default would reopen the
// authorization gate that was closed. The cart is NOT OPENED AT ALL.
//
// The wrapper resolves the SAME name as [linePricing] ([CartFlowsName]) but is
// a separate type: the two endpoints see two different narrow interfaces, and
// had they been reduced to a single wrapper the handler would see both line
// pricing and cart opening behind one surface — whereas an interface being
// narrow is exactly the answer to the question "which capability does this
// endpoint use".
type cartOpening struct {
	c    *container.Container
	log  *slog.Logger
	once sync.Once
	svc  api.CartOpening
	err  error
}

// That the wrapper satisfies the surface the handler expects is pinned down at
// compile time.
var _ api.CartOpening = (*cartOpening)(nil)

// OpenCartForCountry opens the cart and returns its id.
func (p *cartOpening) OpenCartForCountry(
	ctx context.Context,
	countryCode, customerID, email string,
	metadata json.RawMessage,
) (string, error) {
	p.once.Do(func() { p.resolve(ctx) })
	if p.err != nil {
		return "", p.err
	}
	return p.svc.OpenCartForCountry(ctx, countryCode, customerID, email, metadata)
}

// resolve resolves the flow from the container; it stores the result once.
//
// The rationale for not inheriting the error kind from the container is in the
// [linePricing.resolve] godoc and holds here word for word.
func (p *cartOpening) resolve(ctx context.Context) {
	svc, err := container.Resolve[api.CartOpening](p.c, CartFlowsName)
	if err != nil {
		p.err = errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"the %s module could not resolve the cart opening flow (%q); a cart "+
				"cannot be opened without the region being derived from the country",
			ModuleName, CartFlowsName)
		return
	}
	p.svc = svc
	p.log.InfoContext(ctx, "cart opening flow bound", "flow", CartFlowsName)
}
