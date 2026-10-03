// Package payment is the payment module (plan Section 6, Phase 6).
//
// Its responsibility in one sentence: to know which stage the MONEY for a cart
// or an order is at — held, taken, or refunded. The module is the SOLE writer
// of PaymentCollection, PaymentSession, Payment and Refund data (Principle
// 2.3).
//
// # The provider abstraction
//
// The side that talks to a payment institution is not the module but a
// PROVIDER that satisfies core/provider's PaymentProvider contract. The module
// keeps providers by id in a registry ([service.ProviderRegistry]) and resolves
// them BY NAME during a flow.
//
// The providers in the box are the ones [Module.Register] registers: the gift
// card in every installation; the manual provider
// (internal/modules/payment/manual), which authorizes whatever the caller
// names, only where [Options.ManualProvider] asks for it, which the composition
// root does everywhere but production (ADR 0283); and the two tenders that
// spend a customer's own balance (storecredit, loyaltypoints) only where the
// customer claim is proven ([Options.PersonBoundTenders]). The plugin system
// adds its own provider to the registry in the container without touching the
// core or this module — plugins/paymentpaytr does exactly that.
//
// # Saga compensation
//
// Phase 6's complete_cart saga undoes the payment step with
// [service.Service.CancelPayment], and that method is IDEMPOTENT: called twice,
// the second call returns no error. That compensation can be run again is not a
// preference but a condition for the saga to work (plan Section 5.5).
//
// # What it does not know
//
// The module imports no module and does not know WHICH cart or order a payment
// belongs to. reference is free text, NOT a foreign key (Principle 2.2), and
// its existence is not verified here; the connection is made with a link.
// ~~That is why this module declares NO link definition: the owner of the
// connection is not payment but the side that needs the payment.~~
//
// **2026-09-07: THIS module declares the definition.** The connection is still
// made with a link rather than a foreign key, but a definition can be declared
// ONLY ONCE, and the side that declares it is the side that WRITES the record
// the connection carries — the payment capture. That is why "order_payment" is
// declared here (see [service.LinkOrderPayment]) and the order module declares
// no definition. Declaring the definition is not knowing the order: the
// definition carries only the names of the two entities, and this module still
// resolves no order and verifies no reference.
//
// # The surfaces it exposes
//
//   - "payment.service" — the rich in-module surface (with domain types).
//   - "payment.interop" — the PRIMITIVE cross-module surface (ADR 0001/0006);
//     the Phase 6 saga runs the payment steps through it.
//   - "payment.providers" — the provider registry; plugins add providers here.
//   - "payment_collection.query" — the read provider opened to the Query layer
//     (ADR 0004).
//   - /admin/v1/payment-collections … — the admin API.
//   - /store/v1/payment-collections/{id} … — the customer's payment flow.
package payment

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/payment/api"
	"github.com/bdrtr/gobit/internal/modules/payment/giftcard"
	"github.com/bdrtr/gobit/internal/modules/payment/loyaltypoints"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/offline"
	"github.com/bdrtr/gobit/internal/modules/payment/repository"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
	"github.com/bdrtr/gobit/internal/modules/payment/storecredit"
)

// ModuleName is the module's name; it is the prefix of the container names and
// of the migration version ledger.
const ModuleName = "payment"

// ServiceName is the module service's name in the container.
//
// Other modules and workflows reach the service under this name (WITHOUT
// importing this package, as ADR 0001/0006 requires) and use it through a
// narrow interface they define in THEIR OWN packages.
const ServiceName = ModuleName + ".service"

// InteropName is the cross-module primitive surface's name in the container
// (ADR 0006).
//
// It is registered SEPARATELY from the service itself: the service speaks in
// payment's rich types, this surface only in primitive and stdlib types. The
// order completion saga resolves it with its own narrow interface.
const InteropName = ModuleName + ".interop"

// ProvidersName is the provider registry's name in the container.
//
// A plugin adds its own PaymentProvider by resolving this registry and does not
// have to change the module's code; plugins/paymentpaytr is the working example
// of this.
const ProvidersName = ModuleName + ".providers"

// ProviderName is the Query provider's name in the container (ADR 0004).
const ProviderName = service.EntityName + query.ProviderSuffix

// dbServiceName is the core database pool's name in the container.
const dbServiceName = "core.db"

// eventBusServiceName is the event bus's name in the container.
const eventBusServiceName = "core.eventbus"

// linkServiceName is the Module Links service's name in the container.
const linkServiceName = "core.link"

// codeLinkDefine reports that a link definition could not be declared at
// startup.
const codeLinkDefine = "payment_module_link_define_failed"

// Error codes.
const (
	codeSetupFailed      = "payment_module_setup_failed"
	codeProviderRegister = "payment_module_provider_register_failed"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot is the embedded files with the "migrations/" prefix stripped:
// db.Migrate reads the source from the root.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// Module is the payment module as the core sees it.
type Module struct {
	// opts are the installation's settings; the zero value picks the safe side.
	opts      Options
	svc       *service.Service
	providers *service.ProviderRegistry
	handler   *api.Handler
	// personal answers what the module keeps about a person (ADR 0277); nil
	// until Register.
	personal *service.PersonalData
}

// That the core's contract is satisfied is pinned down at compile time.
var _ module.Module = (*Module)(nil)

// That it can describe itself in the document is pinned down at compile time
// too.
//
// [openapi.Describer] is an OPTIONAL interface and the composition root looks
// for it with a TYPE ASSERTION; if the method's name or signature drifted,
// nothing would break at compile time, the payment endpoints would only drop
// out of the document silently. This line closes that silence.
var _ openapi.Describer = (*Module)(nil)

// New produces a payment module ready to be registered.
//
// Dependencies are resolved during Register, not here: until that moment the
// container may not have set up the core services.
func New(opts ...Options) *Module {
	m := &Module{}
	if len(opts) > 0 {
		m.opts = opts[0]
	}

	return m
}

// Options are the module's settings, given by the installation.
//
// The zero value picks the SAFE side: neither the manual provider nor the
// person-bound tenders are registered. An embedder who builds the module by
// hand without having heard of either setting has opened no tender that places
// a paid order without payment, and none that spends a customer's money or
// points.
type Options struct {
	// ManualProvider is whether the manual provider is registered.
	//
	// It authorizes and captures whatever the caller names, so where a shopper
	// can choose it an order is placed paid with nothing paid (ADR 0283). It is
	// what an installation without a provider account takes an order end to end
	// with, and the composition root registers it everywhere but production.
	ManualProvider bool

	// OfflineMethods are the names of the offline payment methods — a bank
	// transfer, cash on delivery — each registered as a provider of its own
	// whose money arrives after the order is placed (ADR 0284). None by
	// default: a method places orders that owe their total, which a shop
	// offers only by naming it.
	OfflineMethods []string

	// OfflineWaitDays is how many days each named offline method waits for its
	// money before the offline expiry job cancels its order (ADR 0289). A
	// method left out never expires, which is every method by default; a named
	// one has to be in OfflineMethods.
	OfflineWaitDays map[string]int

	// PersonBoundTenders is whether the two providers that spend a PERSON's
	// balance — store credit (ADR 0152) and loyalty points (ADR 0165) — are
	// registered.
	//
	// # Why it is something that can be turned off, and why it is ONE setting
	//
	// Because the balance being spent is A PERSON's, and that person's identity
	// comes from the cart's customer field. Since ADR 0125 a cart body that names
	// a customer has to be PROVEN — but an installation can return to the old
	// behavior (STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM), and there the claim
	// is not questioned. In that installation these tenders would mean that
	// anyone who writes a customer's name could spend that customer's balance.
	//
	// That is why the combination CANNOT BE CONFIGURED: the composition root
	// registers NEITHER of them in an installation that trusts the claim.
	// Because the reason belongs to the PERSON, not to the credit or the points,
	// the setting is a single one; two settings would be two copies of the same
	// security decision. Keeping the setting here leaves that decision in one
	// place without requiring the module to read configuration (Principle
	// 2.4).
	PersonBoundTenders bool

	// LoyaltyEarnBasisPoints is how many points each minor unit of captured
	// money earns, in basis points (ADR 0164).
	//
	// ZERO TURNS earning OFF, and it is the default: the ledger exists, nothing
	// is written into it, and a read returns zero. That is the safe side,
	// because a points program nobody asked for is a promise the shop did not
	// make.
	//
	// The ceiling is one point per minor unit, and a value above it is REJECTED
	// at setup (see [service.MaxLoyaltyEarnBasisPoints]). Keeping the setting
	// here leaves the rate in one place without requiring the module to read
	// configuration (Principle 2.4).
	LoyaltyEarnBasisPoints int64

	// GiftCardValidityDays is how many days a gift card pays for when nobody
	// names its moment; zero, the default, is never (ADR 0214). It is held here
	// for the loyalty rate's reason: the module does not read configuration.
	GiftCardValidityDays int
}

// Name returns the module's unique name.
func (m *Module) Name() string { return ModuleName }

// Migrations returns the module's migration files.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Register registers the service, the cross-module surface, the provider
// registry and the Query provider with the container.
//
// Only CORE services are resolved; other modules' services may not be
// registered yet at this stage (see the module.Module documentation). Because
// core.db is registered in main.go as a ready value before the modules come
// up, resolving it here is safe, and its absence is a setup error that makes
// the module unable to run at all — it is not silently deferred.
//
// The manual provider ([manual.Provider]) is registered here when
// [Options.ManualProvider] asks for it. It uses the same repository but writes
// to a SEPARATE table; the service's [service.Store] has none of that table's
// methods, so the module cannot reach the provider's ledger at the type level.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, dbServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the database pool (%q)", ModuleName, dbServiceName)
	}

	links, err := container.Resolve[link.LinkService](c, linkServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the link service (%q)", ModuleName, linkServiceName)
	}

	// The link definitions are declared HERE: the schema stays next to the
	// definition and is verified idempotently at every startup (ADR 0005). A
	// definition can be declared ONLY ONCE, so order_payment is declared by this
	// module rather than the order module — this is the side that writes the
	// record the connection carries (see [service.LinkOrderPayment]).
	for _, def := range service.Definitions() {
		if err := links.Define(ctx, def); err != nil {
			return errors.Wrap(err, errors.KindOf(err), codeLinkDefine,
				"the %q link definition could not be declared", def.Name)
		}
	}

	// Resolved through narrow interfaces: the service publishes, the
	// registration below subscribes to one order event (ADR 0288), and nothing
	// here closes the bus (see service.EventPublisher).
	//
	// REQUIRED, and this is the order module's stance: a lost money event has
	// no compensation. An installation without events is one where the order
	// learns neither what was captured nor what was refunded.
	bus, err := container.Resolve[service.EventPublisher](c, eventBusServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the event bus (%q)", ModuleName, eventBusServiceName)
	}
	subscriber, err := container.Resolve[service.EventSubscriber](c, eventBusServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the event bus as a subscriber (%q)", ModuleName, eventBusServiceName)
	}

	log := slog.Default().With("module", ModuleName)
	repo := repository.New(pool.Pool())

	providers := service.NewProviderRegistry()
	if m.opts.ManualProvider {
		if err := providers.Register(manual.New(repo, log)); err != nil {
			return errors.Wrap(err, errors.KindOf(err), codeProviderRegister,
				"the %s module could not register the manual provider", ModuleName)
		}
	}
	// Store credit and loyalty points are payment methods too, and they come in
	// the box (ADR 0152, ADR 0165): they need no plugin, because the balance they
	// spend is in this module's own ledgers. In an installation with no credit
	// or points nothing changes — the provider is registered, but nobody whose
	// balance is zero can pay with it.
	//
	// The case where they are NOT REGISTERED is a security decision, and its
	// reason is on [Options.PersonBoundTenders]: in an installation that trusts
	// the customer claim without proof these providers would let someone spend
	// another person's balance, so the combination cannot be configured.
	// A gift card is registered in every installation (ADR 0208). It is not
	// person-bound: its owner is whoever presents the code, so the claim the two
	// tenders above depend on plays no part in it.
	if err := providers.Register(giftcard.New(repo, log)); err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeProviderRegister,
			"the %s module could not register the gift card provider", ModuleName)
	}
	if m.opts.PersonBoundTenders {
		if err := providers.Register(storecredit.New(repo, log)); err != nil {
			return errors.Wrap(err, errors.KindOf(err), codeProviderRegister,
				"the %s module could not register the store credit provider", ModuleName)
		}
		if err := providers.Register(loyaltypoints.New(repo, log)); err != nil {
			return errors.Wrap(err, errors.KindOf(err), codeProviderRegister,
				"the %s module could not register the loyalty points provider", ModuleName)
		}
	}
	// The offline methods come after the providers in the box, so a method
	// named like one of them is the registration that fails, naming the method
	// (ADR 0284).
	for _, method := range m.opts.OfflineMethods {
		provider, err := offline.New(method)
		if err != nil {
			return errors.Wrap(err, errors.KindOf(err), codeProviderRegister,
				"the %s module could not register the offline method %q", ModuleName, method)
		}
		if err := providers.Register(provider); err != nil {
			return errors.Wrap(err, errors.KindOf(err), codeProviderRegister,
				"the %s module could not register the offline method %q", ModuleName, method)
		}
	}

	svc, err := service.New(service.Options{
		Store:                  repo,
		Providers:              providers,
		Events:                 bus,
		Logger:                 log,
		LoyaltyEarnBasisPoints: m.opts.LoyaltyEarnBasisPoints,
		GiftCardValidityDays:   m.opts.GiftCardValidityDays,
		Links:                  links,
		OfflineWaitDays:        m.opts.OfflineWaitDays,
	})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s service could not be set up", ModuleName)
	}

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}
	if err := c.Provide(InteropName, service.NewInterop(svc)); err != nil {
		return err
	}
	// The panel's surface (ADR 0287).
	if err := c.Provide(AdminName, &ReceivingSurface{svc: svc}); err != nil {
		return err
	}
	// A canceled order's authorized sessions are closed here (ADR 0288). A
	// failure STOPS THE STARTUP, for the order module's reason: a module that
	// hears no cancel leaves every unpaid order's promise open and looks wired.
	if err := subscriber.Subscribe(service.TopicOrderCanceled, svc.HandleOrderCanceled); err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not subscribe to the %q event", ModuleName, service.TopicOrderCanceled)
	}
	if err := c.Provide(ProvidersName, providers); err != nil {
		return err
	}
	// The provider's name has the form "<entity>.query"; Query looks it up under
	// that name and verifies with Entity() that the name matches (ADR 0004).
	if err := c.Provide(ProviderName, service.NewQueryProvider(svc)); err != nil {
		return err
	}

	m.svc = svc
	m.providers = providers
	m.personal = service.NewPersonalData(repo, log)
	m.handler = api.New(svc).WithIdentity(storefrontIdentity(c, log))

	log.DebugContext(ctx, "payment module registered",
		"service", ServiceName,
		"interop", InteropName,
		"providers", providers.IDs(),
		"query", ProviderName,
	)
	return nil
}

// Routes mounts the module's store and admin endpoints on the router.
//
// If Register did not run, no endpoint is mounted: rather than a handler
// without a service panicking on the first request, it is better for the
// endpoint not to exist at all.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
		slog.Default().Warn("Routes was called on the payment module without Register, no route was mounted")
		return
	}
	m.handler.Routes(r)
}

// Describe writes the module's store and admin endpoints into the OpenAPI
// document.
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

// Providers returns the module's provider registry; it is nil if Register was
// not called.
//
// The embedding application can add its own provider here; in the normal flow
// the registry is resolved from the container under the name [ProvidersName].
func (m *Module) Providers() *service.ProviderRegistry { return m.providers }

// mustSub opens the subdirectory; it panics if it cannot be opened.
//
// The panic is safe here: the directory name is constant at compile time and
// the go:embed directive has already verified at compile time that the files
// exist. Returning nil silently would nevertheless mean the module coming up
// without migrations (that is, without tables); a setup error must blow up
// openly.
func mustSub(files embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic("payment: the embedded migration directory could not be opened: " + err.Error())
	}
	return sub
}
