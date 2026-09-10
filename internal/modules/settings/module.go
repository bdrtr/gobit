// Package settings is the module that answers who the shop IS.
//
// Its responsibility in one sentence: hold the installation's own identity — the
// legal name, tax number, tax office, e-mail and address a document is issued
// under — as a record an operator can edit rather than a value compiled into the
// embedding application.
//
// # Why the module exists at all
//
// The invoicing flow used to take the seller from its CALLER, and its own godoc
// said why: "the seller's legal details are the shop's own configuration, which
// lives in no module here". That made two documents from one shop able to name
// two different sellers, and it made the identity un-editable by the operator who
// edits everything else.
//
// # One record, and only what is printed
//
// There is ONE profile per installation. ADR 0009 puts multi-tenancy at the
// installation boundary, so a table that could hold two would be a second answer
// to "who is selling" and every reader would need a rule for choosing.
//
// Every field has a reader today. A settings module is one unread key away from
// being a bag, and a bag is a table nobody can answer a question about; when the
// second kind of setting arrives it gets its own table and its own reader.
//
// # What it does not know
//
// It imports no module and knows of no order, invoice or cart. The invoicing flow
// reads it through a primitive surface (ADR 0001, ADR 0006), and nothing here is
// aware that documents exist.
//
// # The surfaces it publishes
//
//   - "settings.interop" — the cross-module read, as JSON.
//   - GET/PUT /admin/v1/store-profile — the admin surface.
package settings

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
	"github.com/bdrtr/gobit/internal/modules/settings/api"
	"github.com/bdrtr/gobit/internal/modules/settings/repository"
	"github.com/bdrtr/gobit/internal/modules/settings/service"
)

// The names this module registers under.
const (
	// ModuleName is the module's unique name and the prefix of its migration
	// version table.
	ModuleName = "settings"
	// ServiceName is the concrete service's name in the container.
	ServiceName = ModuleName + ".service"
	// InteropName is the primitive cross-module surface's name.
	//
	// A consumer resolves it under this name and declares its own narrow
	// interface, which this type satisfies structurally (ADR 0001).
	InteropName = ModuleName + ".interop"
	// dbServiceName is the core database pool's name in the container.
	dbServiceName = "core.db"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationsRoot is the root of the migration files.
//
// golang-migrate looks at the root of the source and embed.FS keeps the files
// under "migrations/", so the subtree is opened once here.
var migrationsRoot = mustSub(migrationsFS, "migrations")

// Module is the settings module's [module.Module] implementation.
type Module struct {
	svc *service.Service
	api *api.API
	log *slog.Logger
}

var _ module.Module = (*Module)(nil)

// That it can describe itself is fixed at compile time as well.
//
// [openapi.Describer] is OPTIONAL and the composition root looks for it with a
// type assertion; if the method's name or signature drifted nothing would fail
// to build and the endpoints would quietly leave the document.
var _ openapi.Describer = (*Module)(nil)

// New builds an unregistered settings module; the service is set up in
// [Module.Register]. A nil log discards.
func New(log *slog.Logger) *Module {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Module{log: log}
}

// Name returns the module's name.
func (m *Module) Name() string { return ModuleName }

// Register builds the service and publishes it.
//
// It resolves no other MODULE's service — the identity depends on nothing — so
// this module has no place in the registration order.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, dbServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindUnavailable, "settings_db_unavailable",
			"the settings module could not resolve the %q service", dbServiceName)
	}

	m.svc = service.New(repository.New(pool.Pool()), service.Options{Logger: m.log})
	m.api = api.New(m.svc)

	if err := c.Provide(ServiceName, m.svc); err != nil {
		return err
	}
	if err := c.Provide(InteropName, service.NewInterop(m.svc)); err != nil {
		return err
	}

	m.log.InfoContext(ctx, "the settings module is registered",
		slog.String("service", ServiceName),
		slog.String("interop", InteropName),
	)

	return nil
}

// Migrations returns the module's migration files.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Routes binds the module's admin routes.
//
// It is called AFTER Register, so the API is built. The nil check is there for
// the hand-wired case: if Register failed, Bootstrap never calls this, but a
// module used directly is better served by a quiet no-op than by a panic.
func (m *Module) Routes(r chi.Router) {
	if m.api == nil {
		m.log.Warn("the settings module's Routes was called before Register; nothing was bound")

		return
	}
	m.api.Routes(r)
}

// Describe writes the module's endpoints into the OpenAPI document.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// PersonalData declares where this module keeps something about a person.
//
// # Why an identity of the SHOP is declared at all
//
// A shop is usually a company and a company is not a data subject. But a sole
// trader's shop is a person, and then the legal name IS a name, the tax number
// IS a national identification number and the e-mail IS that person's address.
// The invoice module reached the same conclusion about the seller it prints and
// declares those columns for the same reason; declaring here keeps the two
// records of one fact saying the same thing.
//
// # What erasure does with it: NOTHING, and that is the point
//
// This module implements no eraser. A data subject who asks to be forgotten is a
// CUSTOMER, and erasing the controller's own identity would erase the shop from
// its own installation — while the documents already issued go on printing it,
// because they copied it at the moment of issue. Reaching a seller here is the
// controller's own job, which is the sentence the invoice module's declaration
// already carries about its own seller columns.
//
// It lives on the module rather than on the service and takes no database,
// because a declaration is a property of the CODE: the same sentence on an empty
// database and a full one.
func (m *Module) PersonalData() personaldata.Declaration {
	const table = "store_profile"

	return personaldata.Declaration{
		Holder: ModuleName,
		Holdings: []personaldata.Holding{
			{
				Table: table, Column: "legal_name", Kind: personaldata.Named,
				Why: "the name the shop issues documents under; for a sole trader that is a person's name. No erasure searches it: it identifies the controller rather than a subject",
			},
			{
				Table: table, Column: "tax_number", Kind: personaldata.Named,
				Why: "the shop's tax or national identification number, which for a sole trader identifies a person. No erasure searches it",
			},
			{
				Table: table, Column: "tax_office", Kind: personaldata.Named,
				Why: "the tax office the shop is registered with, which locates the controller administratively. No erasure searches it",
			},
			{
				Table: table, Column: "email", Kind: personaldata.Named,
				Why: "the shop's own address as printed on its documents. It is NOT folded and nothing compares it, because nothing looks a shop up by it — the address is printed, not matched (see emailFoldExemptions)",
			},
			{
				Table: table, Column: "address", Kind: personaldata.Named,
				Why: "the shop's postal address as printed; for a sole trader working from home that is a person's home address",
			},
		},
	}
}

// Service returns the built service; nil before Register.
func (m *Module) Service() *service.Service { return m.svc }

// mustSub opens the subtree and panics if it cannot.
//
// //go:embed guarantees the directory exists at compile time, so the error path
// is unreachable. Returning nil quietly would leave the module migration-less —
// that is, with no table — and the failure would appear as a missing relation at
// the first read.
func mustSub(fsys embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("settings: the migrations subtree could not be opened: " + err.Error())
	}

	return sub
}
