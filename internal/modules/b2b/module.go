// Package b2b is the module that makes buying on a company's behalf possible.
//
// Its responsibility in one sentence: knowing ON WHOSE BEHALF and UP TO HOW
// MUCH a buyer may shop. The module is the SOLE writer of the Company and
// CompanyEmployee data (Principle 2.3).
//
// # Why this module breaks the storefront flow's assumption
//
// In the B2C flow the buyer is an individual spending their own money; there
// is no such concept as spending authority. In B2B the buyer is an employee
// with a LIMITED SPENDING AUTHORITY: their identity is again a customer record
// (customer module), but how much they may spend is decided by the company
// they belong to. This module holds those two pieces of information; it does
// NOT hold the identity itself.
//
// # What it does not know
//
// It does not know the spending itself: carts, orders and amounts are other
// modules' data. That is why the side that ENFORCES the limit is NOT this
// module either — the module only publishes the rule (the limit, the company's
// currency and the start of the current window); the module that applies the
// rule to the spending is order, because the spending is its data and the rule
// is closed to races only when it is applied in the transaction that writes
// the order (see service/interop.go). For the definition of the window see
// internal/modules/b2b/models, SpendingResetPeriod.
//
// The module imports no other module (Principle 2.1/2.4, ADR 0001). The
// employee's bond to the customer record is established with Module Links and
// has no corresponding column in the schema (Principle 2.2).
//
// # The surfaces it opens to the outside
//
//   - "b2b.service" — the service that speaks in the module's rich types;
//     today only the module's own HTTP surface consumes it.
//   - "b2b.interop" — the primitive surface that publishes the spending RULE
//     (ADR 0001). The order module resolves it under this name and applies
//     the rule in the transaction that writes the order.
//   - /admin/v1/b2b/companies, /admin/v1/b2b/employees — the admin API.
//   - /store/v1/b2b/customers/{customer_id}/… — the storefront API.
//
// # Why there is NO Query provider
//
// The module does NOT REGISTER a provider such as "b2b_employee.query". A
// provider exists so that something can be a root or a target in the Query
// layer's expansions; today nothing consumes expanding b2b or expanding
// through b2b. Had it been registered, one more capability with no caller would
// have been added — that is exactly the error class that recurs in this
// repository. Adding it when the need arises is a few lines; removing it would
// mean taking back a published contract.
//
// # The link it declares
//
// THIS module declares the "b2b_employee_customer" definition (ADR 0005); the
// bond is owned by the employee record. For the reasoning behind the choice of
// cardinality see internal/modules/b2b/service, Definitions.
package b2b

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
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/b2b/api"
	"github.com/bdrtr/gobit/internal/modules/b2b/repository"
	"github.com/bdrtr/gobit/internal/modules/b2b/service"
)

// The names in the container.
const (
	// ModuleName is the module's unique name; it is also the prefix of the
	// migration version table.
	ModuleName = "b2b"
	// ServiceName is the service's name in the container. Consumer modules
	// resolve it under this name and with the narrow interface they define
	// THEMSELVES (ADR 0001).
	ServiceName = ModuleName + ".service"
	// InteropName is the container name of the cross-module PRIMITIVE surface.
	//
	// It is registered SEPARATELY from the service itself: the service speaks
	// in b2b's rich types, this surface only in primitive and stdlib types. The
	// order module, which enforces the spending limit, resolves it under this
	// name and with the narrow interface it defines itself (see
	// service/interop.go).
	InteropName = ModuleName + ".interop"
)

// The names of the core services resolved from the container.
const (
	svcDB   = "core.db"
	svcLink = "core.link"
)

// Error codes.
const (
	codeSetupFailed = "b2b_module_setup_failed"
	codeLinkDefine  = "b2b_link_define_failed"
)

// The names of the tables that hold personal data (for the
// [Module.PersonalData] declaration).
//
// tableCompany STARTS with the same letters as [ModuleName] but is not derived
// from it: one is the name of a table in the database, the other the module's
// name in the container, and changing one does not change the other.
const (
	tableCompany  = "b2b_company"
	tableEmployee = "b2b_company_employee"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot is the embedded files with the "migrations/" prefix stripped:
// db.Migrate reads the source FROM THE ROOT, and embed.FS would carry the files
// along with the directory name.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// Module is the b2b module's implementation of [module.Module].
type Module struct {
	svc     *service.Service
	handler *api.Handler
	log     *slog.Logger
	opts    Options
}

// That the core contract is satisfied is pinned at compile time.
var _ module.Module = (*Module)(nil)

// That it can describe the document is pinned at compile time too.
//
// [openapi.Describer] is an OPTIONAL interface and the composition root looks
// for it WITH A TYPE ASSERTION; if the method's name or signature drifted,
// nothing would break at compile time, the module's endpoints would just
// silently drop out of the document. This line closes that silence.
var _ openapi.Describer = (*Module)(nil)

// That it can DECLARE personal data is pinned at compile time too.
//
// The reasoning is the same as for the [openapi.Describer] pin and the price
// is heavier: [personaldata.Declarer] is looked for WITH A TYPE ASSERTION too
// (ADR 0033), so when the method's name or signature drifts nothing breaks at
// compile time — the module silently drops out of the sweep. In the document's
// case the price of that is a missing path; here it is the company's name, its
// billing address and that person's spending limit NEVER APPEARING in the
// disclosure the embedding application publishes to a person.
//
// [personaldata.Eraser] is DELIBERATELY not pinned, because it is not
// implemented: this module has no person record it could erase (see
// [Module.PersonalData]). A holder that declares but does not erase shows up as
// a RETAINED row in every report of the coordinator; staying silent would not
// produce the row at all.
var _ personaldata.Declarer = (*Module)(nil)

// New builds a b2b module that is not set up yet; the service is set up in
// [Module.Register]. If log is nil the logs are discarded.
func New(log *slog.Logger, opts Options) *Module {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Module{log: log, opts: opts}
}

// Options are the module's startup choices.
//
// Its zero value is usable and the zero value of its one field is the CLOSED
// answer, which is why the field is named the way it is.
type Options struct {
	// TrustUnverifiedCustomerClaim serves the storefront's company and employee
	// reads on an unverified claim when no verifier is bound, instead of
	// refusing them (ADR 0125).
	//
	// The cart module takes the same choice under the same name and the
	// composition root feeds both from ONE field: this question has one answer
	// per installation, and two would be the divergence ADR 0057 built a shared
	// comparison to prevent.
	TrustUnverifiedCustomerClaim bool
}

// Name returns the module's name.
func (m *Module) Name() string { return ModuleName }

// Migrations returns the module's migration files.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Register registers the service in the container and declares the link
// definition.
//
// Only CORE services are resolved; other modules' services may not be
// registered yet at this stage (see the module.Module documentation). Since
// core.db and core.link are registered as ready values in the composition root
// before the modules come up, resolving them here is safe, and their absence is
// a setup error under which the module can never work — it is not silently
// deferred.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, svcDB)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the database pool (%q)", ModuleName, svcDB)
	}
	links, err := container.Resolve[link.LinkService](c, svcLink)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the link service (%q)", ModuleName, svcLink)
	}

	svc, err := service.New(service.Options{
		Repo:   repository.New(pool.Pool()),
		Links:  links,
		Logger: m.log,
	})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s service could not be set up", ModuleName)
	}

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}
	// The cross-module surface is registered under a SEPARATE name: the
	// service speaks in b2b's rich types, this surface only in primitive types
	// (ADR 0001).
	if err := c.Provide(InteropName, service.NewInterop(svc)); err != nil {
		return err
	}

	// The link definitions are declared HERE: the schema sits in the same place
	// as the definition itself and is verified idempotently at every startup
	// (ADR 0005).
	for _, def := range service.Definitions() {
		if err := links.Define(ctx, def); err != nil {
			return errors.Wrap(err, errors.KindOf(err), codeLinkDefine,
				"the %q link definition could not be declared", def.Name)
		}
	}

	m.svc = svc
	// The identity comes from the EMBEDDER's module, and that module may not be
	// registered yet at this point; that is why resolving it is deferred to the
	// FIRST REQUEST (see storefrontIdentity). The same pattern is used in
	// order's spending rule and in cart's flows.
	m.handler = api.New(svc, storefrontIdentity(c, m.log, m.opts.TrustUnverifiedCustomerClaim).Identity,
		m.opts.TrustUnverifiedCustomerClaim)
	m.log.InfoContext(ctx, "b2b module registered",
		slog.String("service", ServiceName),
		slog.String("interop", InteropName),
		slog.String("link", service.LinkEmployeeCustomer),
	)
	return nil
}

// Routes binds the module's admin and store routes to the router.
//
// If Register did not run, no endpoint is bound: an endpoint not existing at
// all is preferable to a handler without a service panicking on the first
// request.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
		m.log.Warn("Routes was called before the b2b module was registered; no route was bound")
		return
	}
	m.handler.Routes(r)
}

// Describe writes the module's endpoints into the OpenAPI document.
//
// The description itself is in [api.Describe]: the body schemas are derived
// from that package's unexported DTOs, and exporting the types just for the
// document's sake would widen the module's surface.
//
// Unlike [Module.Routes] there is NO handler check, and none is needed: the
// schema comes from the types, not from the service. Adding a check would
// silently empty the document of a module that is not set up, too.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// PersonalData declares EVERY column in which the module holds personal data.
//
// # Why in the module, why static
//
// The declaration is a property of the CODE, not of the data: it is the same
// sentence on an empty database and on a full one, and an audit reads it
// without opening a connection (ADR 0029). That is why it is bound to the
// module rather than the service, and answers correctly even when Register has
// not been called.
//
// Holder is left EMPTY: the coordinator fills it with the name in the
// REGISTRY (see internal/workflows/datasubject, Coordinator.PersonalData).
// Writing it here by hand would mean keeping the same name in two places and
// waiting for the two to diverge.
//
// # Why a company row is personal data
//
// The module's documentation says "the buyer is a LEGAL ENTITY", and that does
// NOT mean the company row carries no personal data: in a sole proprietorship
// the legal entity's name is that person's OWN name, and the billing address is
// often their home. A framework does not know which kind of company a row
// belongs to; because it does not know, it cannot tell the two apart, and not
// declaring where it cannot tell them apart would put the cost of being wrong
// on the person.
//
// # The employee table's edge case
//
// b2b_company_employee has not a single column that NAMES the person — no
// name, no e-mail address, not even a customer_id (why the column is absent is
// written in the migration's header). On the other hand EVERY ROW of the table
// is about a real person, and spending_limit is that person's spending
// authority. What is declared is therefore not an identity but FACTS about the
// person: the limit and whether they are an admin.
//
// The bond that carries the identity ("b2b_employee_customer") is declared in
// this module's service, but its table is created AT RUN TIME by core/link and
// appears in no migration; the coordinator declares that store SEPARATELY (see
// internal/workflows/datasubject, linkHoldings). Declaring it here as well
// would mean two holders taking on a single piece of data.
//
// id and company_id are not declared: a key names a row, not a person, and
// once the columns beside it are anonymized it points to nobody. Declaring it
// would put every foreign key in the installation on the list.
//
// # Why all of them are [personaldata.Named]
//
// This module has NO free-text column (metadata jsonb, description, note):
// gobit knows what each column carries, because it writes there itself after
// validating. That the declaration holds not a single [personaldata.Open] row
// is a measured property of this schema, not a possibility that was skipped.
//
// # Why it has to be complete
//
// The list is the only answer the embedding application can give to the
// question "what is held about this person, and where"; the embedder
// publishes it as a privacy notice. A missing row does not shorten the list,
// it makes it a LIE. That is why the list is derived one-to-one from the
// columns of the two tables in migrations/000001_b2b_init.up.sql and its
// completeness is pinned by a test (see
// TestPersonalDataCoversEveryPersonalColumn).
func (m *Module) PersonalData() personaldata.Declaration {
	return personaldata.Declaration{
		Holdings: []personaldata.Holding{
			{
				Table: tableCompany, Column: "name", Kind: personaldata.Named,
				Why:       "the company's name, which for a sole trader is that person's own name",
				OnErasure: personaldata.Kept,
			},
			{
				Table: tableCompany, Column: "email", Kind: personaldata.Named,
				Why:       "the e-mail address the company account is reached at, which for a one-person company is that person's own address",
				OnErasure: personaldata.Kept,
			},
			{
				Table: tableCompany, Column: "phone", Kind: personaldata.Named,
				Why:       "the telephone number left for the company, which for a one-person company is that person's own number",
				OnErasure: personaldata.Kept,
			},
			{
				Table: tableCompany, Column: "address", Kind: personaldata.Named,
				Why:       "the street line of the company's billing address, which is a person's home address whenever they trade from where they live",
				OnErasure: personaldata.Kept,
			},
			{
				Table: tableCompany, Column: "city", Kind: personaldata.Named,
				Why:       "the city of the company's billing address",
				OnErasure: personaldata.Kept,
			},
			{
				Table: tableCompany, Column: "postal_code", Kind: personaldata.Named,
				Why:       "the postal code of the company's billing address, which in some countries reaches a single building",
				OnErasure: personaldata.Kept,
			},
			{
				Table: tableCompany, Column: "country_code", Kind: personaldata.Named,
				Why:       "the country of the company's billing address, which is also the jurisdiction whose tax and retention rules apply",
				OnErasure: personaldata.Kept,
			},
			{
				Table: tableEmployee, Column: "spending_limit", Kind: personaldata.Named,
				Why:       "how much one employee may spend on their employer's account, a fact about that person although this table records no name, no address and no identifier of theirs",
				OnErasure: personaldata.Kept,
			},
			{
				Table: tableEmployee, Column: "is_company_admin", Kind: personaldata.Named,
				Why:       "whether that same employee may administer their company's account, which describes the person's authority rather than the company",
				OnErasure: personaldata.Kept,
			},
		},
	}
}

// Service returns the service that was set up; nil if Register was not
// called.
//
// It is for tests and embedding applications that use the module directly; in
// the normal flow the service is resolved from the container under the name
// [ServiceName].
func (m *Module) Service() *service.Service { return m.svc }

// mustSub opens a subtree of the embedded file system.
//
// The path is fixed at compile time; ending up here means the migrations
// directory was not embedded, and that cannot be passed over silently — a
// module that came up without migrations would start working without its
// tables.
func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("b2b: the migration source could not be opened: " + err.Error())
	}
	return sub
}
