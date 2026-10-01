// Package promotion is the promotion module (plan Section 6, Phase 7).
//
// Its responsibility in one sentence: to compute which discounts a cart is
// entitled to and to count how many times a coupon was used. The module is the
// ONLY writer of the Campaign, Promotion, ApplicationMethod, PromotionRule and
// usage ledger data (Principle 2.3).
//
// # The job it took over
//
// In Phase 5 the discount field of the cart total was ALWAYS ZERO and
// internal/workflows/cart left it with the note "promotion takes over in Phase
// 7". The takeover is this module's [service.Service.ComputeDiscounts]; the
// cart flow resolves it by the name "promotion.interop" and calls it through
// ComputeDiscountsJSON.
//
// The tax base was defined in Phase 5 as AFTER the discount from the start
// (the internal/workflows/cart package comment, "Tax contract"), so when the
// discount comes into play the tax settles on the right base by itself.
//
// # The computation and the usage are SEPARATE
//
// [service.Service.ComputeDiscounts] HAS NO SIDE EFFECTS: it is called every
// time the cart changes and consumes no counter. The one that actually spends
// a coupon is [service.Service.RedeemPromotion], and it is idempotent; its
// compensation [service.Service.ReleasePromotion] is too (plan Section 5.5).
//
// # What it does not know
//
// promotion imports no module. Which order a usage belongs to is a free
// "reference" text, NOT a foreign key (Principle 2.2), and its existence is
// not verified here; the bond is made by the link the order declares. That is
// why this module declares NO link definition: the bond's owner is not the
// promotion but the side that needs the promotion.
//
// # The surfaces it opens
//
//   - "promotion.service" — the rich in-module surface (with domain types).
//   - "promotion.interop" — the cross-module PRIMITIVE surface (ADR
//     0001/0006); the cart flow and the order saga compute the discount here.
//   - "promotion.query" — the read provider opened to the Query layer (ADR
//     0004); it returns ONLY active promotions and a narrow field set.
//   - "promotion.admin" — the panel's surface (ADR 0311): the promotions in a
//     status with their usage, which the read provider keeps out because it
//     cannot tell a storefront from an operator.
//   - /admin/v1/promotions, /admin/v1/campaigns … — the admin API.
//   - /store/v1/promotions/{code} — coupon validation; it does NOT LEAK a
//     draft/inactive promotion or the rule conditions.
package promotion

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/promotion/api"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// The names in the container.
const (
	// ModuleName is the module's unique name; it is also the prefix of the
	// migration version table.
	ModuleName = "promotion"
	// ServiceName is the service's name in the container.
	//
	// Other modules and workflows (WITHOUT importing this package, as ADR
	// 0001/0006 require) reach the service by this name and use it through a
	// narrow interface they define in THEIR OWN package.
	ServiceName = ModuleName + ".service"
	// InteropName is the container name of the cross-module primitive surface
	// (ADR 0006).
	//
	// It is registered SEPARATELY from the service itself: the service speaks
	// in promotion's rich types, this surface only in primitive and stdlib
	// types.
	InteropName = ModuleName + ".interop"
	// ProviderName is the container name of the Query provider (ADR 0004).
	ProviderName = service.Entity + query.ProviderSuffix
	// AdminName is the container name of the panel's surface (ADR 0311).
	AdminName = ModuleName + ".admin"
	// dbServiceName is the container name of the core database pool.
	dbServiceName = "core.db"
)

// codeSetupFailed reports that the module's setup failed.
const codeSetupFailed = "promotion_module_setup_failed"

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Module is the promotion module's [module.Module] implementation.
type Module struct {
	svc     *service.Service
	handler *api.API
	log     *slog.Logger
}

// That the core contract is satisfied is pinned at compile time.
var _ module.Module = (*Module)(nil)

// That it can describe the document is pinned at compile time too.
//
// [openapi.Describer] is an OPTIONAL interface and the composition root looks
// for it with a TYPE ASSERTION; were the method's name or signature to drift,
// nothing would break at compile time, the promotion endpoints would only drop
// silently out of the document. This line closes that silence.
var _ openapi.Describer = (*Module)(nil)

// New produces a promotion module ready to be registered; the service is built
// in [Module.Register]. If log is nil the logs are discarded.
//
// Dependencies are resolved during Register, not here: the container may not
// have set up the core services by this moment.
func New(log *slog.Logger) *Module {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Module{log: log}
}

// Name returns the module's name.
func (m *Module) Name() string { return ModuleName }

// Migrations returns the module's migration files.
//
// The root is moved down into the "migrations" subfolder; golang-migrate looks
// for the files at the ROOT of the source, and embed.FS would carry them with
// the folder's name.
func (m *Module) Migrations() fs.FS {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		// The embed path is fixed at compile time; landing here means the
		// migrations folder was not embedded, and that cannot pass silently.
		panic("promotion: the migration source could not be opened: " + err.Error())
	}
	return sub
}

// Register registers the service, the cross-module surface, the Query
// provider and the panel's surface in the container.
//
// promotion needs no MODULE's service; it resolves only the core pool. The pool
// is registered BEFORE Bootstrap, so resolving it directly here is safe — the
// only thing that would create a dependency on the module order is resolving
// another MODULE's service, and that is not done.
//
// No link definition is declared: the owner of the bond of which promotion an
// order used is the order side (see the package comment).
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, dbServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the database pool (%q)", ModuleName, dbServiceName)
	}

	svc := service.New(repository.New(pool.Pool()), service.Options{Logger: m.log})

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}
	if err := c.Provide(InteropName, service.NewInterop(svc)); err != nil {
		return err
	}
	// The provider's name is "<entity>.query"; Query looks for it by this name
	// and verifies through Entity() that the name matches (ADR 0004).
	if err := c.Provide(ProviderName, service.NewQueryProvider(svc)); err != nil {
		return err
	}
	if err := c.Provide(AdminName, NewAdminSurface(svc)); err != nil {
		return err
	}

	m.svc = svc
	m.handler = api.New(svc).WithTrial(&promotionTrial{c: c, log: m.log})

	m.log.InfoContext(ctx, "promotion module registered",
		slog.String("service", ServiceName),
		slog.String("interop", InteropName),
		slog.String("provider", ProviderName),
		slog.String("admin", AdminName),
	)
	return nil
}

// Routes binds the module's admin and store routes to the router.
//
// It is called AFTER Register (see module.Registry.Bootstrap), so the handler
// is built. There is a nil check all the same: if Register fails and Bootstrap
// is cut short Routes is never called, but if the module is used by hand a
// silent no-op is safer than a panic.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
		return
	}
	m.handler.Routes(r)
}

// Describe writes the module's endpoints into the OpenAPI document.
//
// The description itself is in [api.Describe]: the body schemas are derived
// from that package's unexported DTOs, and exporting the types for the
// document's sake alone would widen the module's surface.
//
// Unlike [Module.Routes] there is NO handler check, and none is needed: the
// schema comes from the types, not from the service. A check would also
// silently empty the document of a module that was not set up.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// Service returns the built service; nil if Register was not called.
//
// It is for the tests and the embedding applications that use the module
// directly; in the normal flow the service is resolved from the container by
// the name [ServiceName].
func (m *Module) Service() *service.Service { return m.svc }
