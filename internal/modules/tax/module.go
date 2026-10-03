// Package tax is the tax module (plan Section 6, Phase 7).
//
// Its responsibility in one sentence: to know in which geography and at which
// rate a sale is taxed, and to compute the tax of a list of line items. The
// module is the SOLE writer of TaxRegion, TaxRate, TaxRateRule, TaxClass and
// TaxClassMember data (Principle 2.3).
//
// # The work inherited from region
//
// In Phase 5 tax lived TEMPORARILY in the region module: the region table had a
// single tax_rate (basis points) and an automatic_taxes flag, and the cart flow
// read them. region's godoc marked this explicitly as "the tax module will take
// over in Phase 7". This module provides that takeover.
//
// The takeover is done in this round WITHOUT TOUCHING region (ADR 0001: modules
// do not import one another and do not see one another's tables). tax sets up
// its own schema and surface, and the cart flow computes the tax through
// "tax.interop"; it falls back to "region.service"'s rate only when tax is not
// registered or the cart's region does not resolve to a single country. The
// two surfaces correspond one to one:
//
//	region: RegionTax(ctx, regionID)   -> (rateBps int32, automatic bool, err error)
//	tax:    RateForCountry(ctx, code)  -> (rateBps int32, found bool, err error)
//
// The meaning of the second return value has CHANGED, and deliberately so: the
// flag in region was an "apply/do not apply tax" preference, while here it is
// the fact "is there a configuration". The preference is now expressed in the
// data itself — if tax is not wanted, no region is opened for that country or
// the default rate is written as zero.
//
// # The provider abstraction is NOT IN THE CORE
//
// The plan says "TaxProvider", but core/provider has NO tax provider (it
// defines the payment, shipping, notification, file and classification
// contracts and ErrorReporter), and this module cannot touch the core. The
// contract therefore lives in the module's own package ([service.TaxProvider]),
// and the implementation that ships in the box is the local calculation
// ([service.LocalProvider]). The decision is EXPLICITLY temporary; the
// condition and the path for moving it are written in the service package's
// godoc.
//
// # What it does not know
//
// tax imports no module. Country codes are region's data, but this module does
// not read them: it validates the FORMAT of an ISO 3166-1 code and does not ask
// whether it is DEFINED. The product/product type/shipping option ids in rule
// records are free text too, and they are NOT foreign keys (Principle 2.2).
//
// That is why the module declares NO link definition: the owner of the bond is
// not tax but the side that needs tax.
//
// # The surfaces it exposes
//
//   - "tax.service" — the rich in-module surface (with domain types).
//   - "tax.interop" — the PRIMITIVE cross-module surface (ADR 0001/0006); the
//     cart flow has the tax computed through it.
//   - "tax.providers" — the provider registry; a plugin can add a provider to
//     it, but core/plugin publishes no constant for this name (see
//     ProvidersName below).
//   - "tax_region.query" — the read provider opened to the Query layer
//     (ADR 0004).
//   - /admin/v1/tax-regions, /admin/v1/tax-rates (+ rules),
//     /admin/v1/tax-classes (+ products) — the admin API.
//
// There is NO Store API; the reasoning is in the internal/modules/tax/api
// package comment.
package tax

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
	"github.com/bdrtr/gobit/internal/modules/tax/api"
	"github.com/bdrtr/gobit/internal/modules/tax/repository"
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

// The names in the container.
const (
	// ModuleName is the module's unique name; it is also the prefix of the
	// migration version table.
	ModuleName = "tax"
	// ServiceName is the service's name in the container. Consumer modules
	// resolve it by this name and through the narrow interface they define
	// THEMSELVES (ADR 0001).
	ServiceName = ModuleName + ".service"
	// InteropName is the container name of the primitive cross-module surface
	// (ADR 0006).
	//
	// It is registered SEPARATELY from the service itself: the service speaks
	// in tax's rich types, this surface only in primitive and stdlib types.
	// The cart flow resolves it through its own narrow interface.
	InteropName = ModuleName + ".interop"
	// ProvidersName is the provider registry's name in the container.
	//
	// A plugin adds its own tax provider by resolving this registry, and it
	// does not have to change the module's code. The mechanism is ready and
	// proven in the payment slot (plugins/paymentpaytr); no plugin fills this
	// slot yet.
	//
	// BUT it has ONE DIFFERENCE from the other four families, and it was
	// written down on 2026-09-09: core/plugin does NOT PUBLISH a constant for
	// this name. The payment, shipping, notification and file registries each
	// have a constant there, and internal/arch asserts that the two are equal;
	// since there is no such constant here, a plugin can reach this registry
	// only by writing the string "tax.providers" BY HAND (through
	// Host.Container), so it stays open to the drift the constants prevent.
	//
	// This is not a gap but the result of the same reasoning: a published name
	// is a promise kept until 1.0.0 (ADR 0026), and a capability with no
	// consumer is not published (ADR 0063). The day a tax plugin is written,
	// the constant and the plugin come in the SAME change. Until then the
	// situation is written down in internal/arch in
	// providerFamiliesWithoutAPublishedName — not silence, a written decision.
	ProvidersName = ModuleName + ".providers"
	// ProviderName is the query provider's name in the container (ADR 0004).
	ProviderName = service.Entity + query.ProviderSuffix
	// dbServiceName is the core database pool's name in the container.
	dbServiceName = "core.db"
)

// Error codes.
const (
	codeSetupFailed      = "tax_module_setup_failed"
	codeProviderRegister = "tax_module_provider_register_failed"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationsRoot is the root directory of the migration files.
//
// The golang-migrate source looks at the root (iofs.New(src, ".")), while the
// embed.FS keeps the files under "migrations/"; the subtree is therefore
// opened once, here.
var migrationsRoot = mustSub(migrationsFS, "migrations")

// Module is the tax module's [module.Module] implementation.
type Module struct {
	svc *service.Service
	api *api.API
	log *slog.Logger
}

var _ module.Module = (*Module)(nil)

// That it can describe the document is pinned at compile time too.
//
// [openapi.Describer] is an OPTIONAL interface and the composition root looks
// for it with a TYPE ASSERTION; if the method's name or signature drifted,
// nothing would break at compile time, only the tax endpoints would silently
// drop out of the document. This line closes that silence.
var _ openapi.Describer = (*Module)(nil)

// New builds a tax module that is not set up yet; the service is set up in
// [Module.Register]. If log is nil, logs are discarded.
func New(log *slog.Logger) *Module {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Module{log: log}
}

// Name returns the module's name.
func (m *Module) Name() string { return ModuleName }

// Register registers the service, the cross-module surface, the provider
// registry and the query provider in the container.
//
// tax needs no MODULE's service; it resolves only the core pool. Since the
// pool is registered BEFORE Bootstrap, resolving it directly here is safe —
// the only thing that would create a dependency on module order is resolving
// another MODULE's service, and that is not done.
//
// The default provider ([service.LocalProvider]) is registered here and uses
// the same repository instance as its rate source. The registration is done
// EXPLICITLY (not left to the service's implicit default) so that the
// "tax.providers" value in the container and the registry the service uses are
// the SAME object; were there two separate registries, a provider added by a
// plugin would never show up in the calculation.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, dbServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindUnavailable, codeSetupFailed,
			"the tax module could not resolve the %q service", dbServiceName)
	}

	repo := repository.New(pool.Pool())

	providers := service.NewProviderRegistry()
	if err := providers.Register(service.NewLocalProvider(repo)); err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeProviderRegister,
			"the tax module could not register the default provider")
	}

	m.svc = service.New(repo, service.Options{Logger: m.log, Providers: providers})
	m.api = api.New(m.svc)

	if err := c.Provide(ServiceName, m.svc); err != nil {
		return err
	}
	if err := c.Provide(InteropName, service.NewInterop(m.svc)); err != nil {
		return err
	}
	if err := c.Provide(ProvidersName, providers); err != nil {
		return err
	}
	if err := c.Provide(ProviderName, service.NewQueryProvider(m.svc)); err != nil {
		return err
	}

	m.log.InfoContext(ctx, "tax module registered",
		slog.String("service", ServiceName),
		slog.String("interop", InteropName),
		slog.Any("providers", providers.IDs()),
		slog.String("query", ProviderName),
	)
	return nil
}

// Migrations returns the module's migration files.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Routes binds the module's admin routes to the router.
//
// It is called AFTER Register (see module.Registry.Bootstrap), so api is set up
// by then. There is a nil check all the same: if Register fails and Bootstrap
// is cut short, Routes is never called, but if the module is used by hand, a
// silent no-op is safer than a panic.
func (m *Module) Routes(r chi.Router) {
	if m.api == nil {
		m.log.Warn("Routes was called on the tax module without Register, no route was mounted")
		return
	}
	m.api.Routes(r)
}

// Describe writes the module's endpoints into the OpenAPI document.
//
// The description itself is in [api.Describe]: the body schemas are derived
// from that package's unexported DTOs, and exporting the types just for the
// sake of the document would widen the module's surface.
//
// Unlike [Module.Routes] there is NO api check, and none is needed: the schema
// comes from the types, not from the service. Adding a check would silently
// empty the document of a module that is not set up, too.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// Service returns the set-up service; nil if Register was not called.
//
// It is for tests that use the module directly and for applications that
// embed it; in the normal flow the service is resolved from the container by
// the name [ServiceName].
func (m *Module) Service() *service.Service { return m.svc }

// mustSub opens the sub file system; it panics if it cannot be opened.
//
// Since //go:embed guarantees the directory exists at compile time, the error
// path is unreachable. Still, silently returning nil would mean the module
// coming up without migrations (that is, without tables); a setup error has to
// blow up openly.
func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("tax: the migration directory could not be opened: " + err.Error())
	}
	return sub
}
