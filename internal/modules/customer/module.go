// Package customer is the customer module (plan Section 6, Phase 5).
//
// Its responsibility in one sentence: to know who is shopping — a guest and a
// registered account alike. The module is the SOLE writer of Customer,
// CustomerGroup and CustomerAddress data (Principle 2.3).
//
// # Guest or account
//
// The module's central decision is that e-mail uniqueness is enforced only on
// REGISTERED accounts; guest records may share the same e-mail. The rule lives
// in a partial unique index in the database and its rationale is written in
// internal/modules/customer/models, in the Customer godoc.
//
// # What it does not know
//
// customer imports no module and is unaware that carts and orders exist. The
// cart ↔ customer and order ↔ customer bonds are set up with Module Links by
// those modules, which own the relation; customer never sees those links
// (Principle 2.2: there is no cross-module FK). Country codes are validated
// too but NOT LISTED: the owner of the country list is the region module.
//
// # The surfaces it exposes
//
//   - "customer.service" — the service for cross-module calls (see
//     internal/modules/customer/service, interop.go).
//   - "customer.query" — the read provider opened to the Query layer
//     (ADR 0004). Records come back WITH THEIR GROUP IDS so that the rule
//     context of the price calculation can be set up in one call.
//   - /admin/v1/customers, /admin/v1/customer-groups … — the admin API.
//   - /store/v1/customers … — the storefront API. Verifying a customer
//     identity is still the embedding application's job (ADR 0008) and gobit
//     issues none; what changed with ADR 0043 is that the storefront routes
//     naming a customer now REQUIRE the embedder's verifier and refuse when
//     none is bound. The contract is corehttp.Identity, resolved from the
//     container under corehttp.IdentityName by [storefrontIdentity]; see the
//     internal/modules/customer/api package documentation.
//
// # A note for the side that declares the link
//
// Query finds an expansion's target provider FROM THE MODULE NAME AT THE END
// of the link definition (the target name + ".query" is looked up). The
// customer end has to be written WITH THE ENTITY NAME:
//
//	link.LinkDefinition{
//	    Name:        "b2b_employee_customer",
//	    From:        link.LinkSide{Module: "b2b", Field: "employee_id"},
//	    To:          link.LinkSide{Module: "customer", Field: "customer_id"},
//	    Cardinality: link.OneToOne,
//	}
//
// The example IS REAL: the b2b module declares this link and reads the
// employee's customer record THROUGH IT. Rather than declaring a link that is
// never read, a column has to be used; for why that is so, see internal/arch
// TestTheLinkDefinitionsAreTraversed.
//
// Here the entity name and the module name are the same ("customer"), but that
// is a coincidence and the provider name has to be read from the
// [ProviderName] constant.
package customer

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
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/customer/api"
	"github.com/bdrtr/gobit/internal/modules/customer/repository"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// The names in the container.
const (
	// ModuleName is the module's unique name; it is also the prefix of the
	// migration version table.
	ModuleName = "customer"
	// ServiceName is the service's name in the container. Consumer modules
	// resolve it by this name and through a narrow interface of their OWN
	// (ADR 0001).
	ServiceName = ModuleName + ".service"
	// ProviderName is the query provider's name in the container (ADR 0004).
	ProviderName = service.Entity + query.ProviderSuffix
	// GroupProviderName is the customer groups' query provider's name in the
	// container (ADR 0321).
	GroupProviderName = service.EntityGroup + query.ProviderSuffix
	// AdminName is the module's panel surface's name in the container (ADR
	// 0322): a customer's membership of the groups.
	AdminName = ModuleName + ".admin"
	// dbServiceName is the core database pool's name in the container.
	dbServiceName = "core.db"
)

// codeSetupFailed reports that the module's setup failed.
const codeSetupFailed = "customer_module_setup_failed"

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationsRoot is the embedded files with the "migrations/" prefix stripped:
// the golang-migrate source reads FROM THE ROOT, and embed.FS would carry the
// files together with the folder name.
var migrationsRoot = mustSub(migrationsFS, "migrations")

// Module is the customer module's [module.Module] implementation.
type Module struct {
	svc     *service.Service
	handler *api.Handler
	log     *slog.Logger
}

var _ module.Module = (*Module)(nil)

// That it can describe the document is pinned down at compile time as well.
//
// [openapi.Describer] is an OPTIONAL interface and the composition root looks
// for it with a TYPE ASSERTION; if the method name or its signature slips,
// nothing breaks at compile time — only the customer endpoints would silently
// drop out of the document. This line closes that silence.
var _ openapi.Describer = (*Module)(nil)

// The personal data capabilities are pinned down at compile time too.
//
// The rationale is the same as the [openapi.Describer] pin's, and here the cost
// is heavier: all three interfaces are looked up with a TYPE ASSERTION
// (ADR 0029, ADR 0034), so when a method name or signature slips nothing
// breaks — the module silently drops out of the scan. In the document's case
// the cost of that is a missing path; here it is telling a person "your data
// has been erased" while their e-mail, name and address stay where they were.
//
// The three interfaces are pinned SEPARATELY because they are separate
// capabilities: there is a holder that can declare but cannot erase (see the
// personaldata.Declarer documentation), and a single pin would tie the three
// together. The cost of [personaldata.Discloser] dropping out differs from the
// others' and is more insidious: the coordinator writes a holder that declares
// but cannot disclose into the dossier as UNRESOLVABLE (see
// internal/workflows/datasubject, undisclosedHolder). So if the signature
// slips, no test breaks and the build does not break — the document that goes
// to the person only says "we cannot tell what is here", whereas the module
// could have told exactly that.
var (
	_ personaldata.Eraser    = (*Module)(nil)
	_ personaldata.Declarer  = (*Module)(nil)
	_ personaldata.Discloser = (*Module)(nil)
)

// New produces a customer module that is not set up yet; the service is set up
// inside [Module.Register]. If log is nil, the logs are discarded.
func New(log *slog.Logger) *Module {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Module{log: log}
}

// Name returns the module's name.
func (m *Module) Name() string { return ModuleName }

// Migrations returns the module's migration files.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Register registers the service and the two query providers, the customers'
// and the groups', into the container.
//
// customer needs no MODULE's service; it resolves only the core pool. Because
// the pool is registered BEFORE Bootstrap, resolving it directly here is safe —
// the only thing that would create a dependency on the module order would be
// resolving another MODULE's service, and that is not done.
//
// That sentence survives ADR 0043 unchanged, and [storefrontIdentity] is why. The
// customer identity comes from a module the embedder adds LAST, so resolving it
// here would make registration order part of the contract; the wrapper handed
// to the handler resolves it on the first storefront request instead. The
// container is captured, not read.
//
// No link definition is DECLARED: customer is the end of the links that point
// at it, not their owner. Today the only owner is the b2b module
// ("b2b_employee_customer").
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, dbServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the %q service", ModuleName, dbServiceName)
	}

	repo := repository.New(pool.Pool())
	m.svc = service.New(repo, service.Options{Logger: m.log})
	m.handler = api.New(m.svc, storefrontIdentity(c, m.log)).
		WithPreview(&segmentPreview{c: c, log: m.log})

	if err := c.Provide(ServiceName, m.svc); err != nil {
		return err
	}
	if err := c.Provide(ProviderName, service.NewQueryProvider(m.svc)); err != nil {
		return err
	}
	if err := c.Provide(GroupProviderName, service.NewGroupProvider(m.svc)); err != nil {
		return err
	}
	if err := c.Provide(AdminName, service.NewAdminSurface(m.svc)); err != nil {
		return err
	}

	m.log.InfoContext(ctx, "customer module registered",
		slog.String("service", ServiceName),
		slog.String("provider", ProviderName),
		slog.String("group_provider", GroupProviderName),
	)
	return nil
}

// Routes mounts the module's admin and store routes on the router.
//
// It is called AFTER Register (see module.Registry.Bootstrap); the handler is
// therefore set up. There is still a nil check: if Register fails and Bootstrap
// is cut short, Routes is never called, but if the module is used by hand, a
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
// from that package's unexported DTOs, and exporting the types only for the
// document's sake would widen the module's surface.
//
// Unlike [Module.Routes] there is NO handler check and none is needed: the
// schema comes from the types, not from the service. Putting a check there
// would also silently empty the document of a module that is not set up.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// Erase ANONYMIZES the person's customer records; it deletes none of them.
//
// The decision itself and its rationale are in the service (see
// service.Service.Erase): the row cannot go, because the cart and the order
// carry the customer id in a plain TEXT column without a foreign key
// (Principle 2.2), and deleting the row would silently leave them ownerless.
// The module only DELEGATES here — the work is the service's, because the
// transaction and the repository are visible only from there.
//
// If Register was not called the service is nil, and this DOES NOT TURN INTO a
// panic: the service's ready check covers a nil receiver too and returns a
// typed Unavailable error. A silent "0 rows anonymized" answer would have been
// unacceptable — a module that is not set up would look like a module that
// holds no data at all.
func (m *Module) Erase(ctx context.Context, s personaldata.Subject) (personaldata.Result, error) {
	return m.svc.Erase(ctx, s)
}

// PersonalData declares EVERY column in which the module holds personal data.
//
// # Why in the module, why static
//
// The declaration is a property of the CODE, not of the data: it is the same
// sentence on an empty database and on a full one, and an audit reads it
// without opening a connection (ADR 0029). The DECLARER is still the module —
// this is the method the audit calls, this is the place for the rationale
// below, and it answers correctly even while Register has not been called.
//
// # Why the list itself is in the service
//
// [service.PersonalDataHoldings] holds the list, because DISCLOSURE has to read
// it: every record [Module.PersonalDataOf] produces DERIVES its fields from
// this list and carries no second column list (ADR 0034, fourth point). Two
// lists kept by hand agree on the day they are written and drift apart on the
// first day a column is added to one of them; when they drift apart, what comes
// out is a column that is either declared and never shown, or handed over after
// being called "not there". Both are a wrong answer given to a person about
// their own data. The service CANNOT import the module (it would be a cycle),
// so for the derivation to be able to exist the list had to stand on that
// side.
//
// # Why it has to be complete
//
// The list is the only answer the embedding application can give to the
// question "what is where about this person". A missing row does not shorten
// the list — it turns it into a LIE: a column that is not declared is looked
// for by no audit. The list is therefore derived one to one from the columns of
// the FOUR TABLES that migrations/000001_customer_init.up.sql CREATES, and its
// completeness is pinned down by a test (see
// TestPersonalDataCoversEveryPersonalColumn); the test reads every column of
// the four tables and asks for a written rationale for every column that is
// not declared.
//
// # Why only one column from the other two tables
//
// customer_group is a SEGMENT ("wholesalers"); its name belongs to the group,
// not to a person. Its metadata is something else: of the same kind as
// customer.metadata, it is a jsonb the embedding application writes FREELY and
// gobit does not look inside. Because it does not look, it cannot say "this is
// not personal data" either — ADR 0029 leaves that decision to the embedding
// application — and it is therefore DECLARED as [personaldata.Open], even
// though it is rewritten for no subject; Erase counts it under Kept in every
// answer (see service.Service.Erase).
//
// customer_group_customer is the FACT of membership and carries only two ids;
// once the customer row is anonymized, the id it carries points at nobody. The
// id columns themselves are declared in no table: what identifies the person
// is the columns next to the id, and that is exactly the fact anonymization
// relies on. Had the id been declared, we would have listed a key that has
// stopped pointing at the person as "personal data".
//
// The split between [personaldata.Named] and [personaldata.Open] is a split of
// responsibility: gobit wrote the named column there and knows what it is,
// whereas what is written into an open column only the embedding application
// can decide.
func (m *Module) PersonalData() personaldata.Declaration {
	return personaldata.Declaration{
		Holder:   ModuleName,
		Holdings: service.PersonalDataHoldings(),
	}
}

// PersonalDataOf returns the person's data in this module AS A DOSSIER.
//
// # Why the module owes this
//
// Because it can already FIND the person. [Module.Erase] resolves both handles,
// covers guest records knowingly and writes over what it finds. A module that
// can search in order to destroy but cannot search in order to show would have
// produced that asymmetry not from anyone's policy but from its own code
// (ADR 0034). This method is the other direction of the search Erase makes and
// resolves the subject in exactly the same way.
//
// # Why it only delegates
//
// The same rationale as [Module.Erase]: the repository and the queries are
// visible only from the service. The decision itself and the rationale for what
// goes into the dossier and what does not are in the service (see
// service.Service.PersonalDataOf) — in particular why customer_group.metadata
// and the membership row have no place in a PERSON's dossier.
//
// If Register was not called the service is nil, and a typed Unavailable error
// is returned instead of a panic. A silent "there is nothing about this person"
// answer would have been unacceptable, and here its cost is heavier than in
// erasure: the coordinator NAMES the error as missing in the dossier (see
// internal/app, dossierDTO.Incomplete), whereas an empty answer would have
// gone to the person as "we hold nothing about you".
func (m *Module) PersonalDataOf(ctx context.Context, s personaldata.Subject) (personaldata.Disclosure, error) {
	return m.svc.PersonalDataOf(ctx, s)
}

// Service returns the service that was set up; nil if Register was not called.
//
// It is for the tests and the embedding applications that use the module
// directly; in the normal flow the service is resolved from the container
// under the name [ServiceName].
func (m *Module) Service() *service.Service { return m.svc }

// mustSub opens the subtree of the embedded file system.
//
// The path is constant at compile time; landing here means the migrations
// folder was not embedded and it cannot be passed over silently — a module
// opened without migrations would start working without its tables.
func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("customer: could not open the migration source: " + err.Error())
	}
	return sub
}
