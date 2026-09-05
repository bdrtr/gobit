// Package invoice is the invoice module.
//
// Its responsibility in one sentence: giving a finished document a number that
// no other document in its series will ever have, storing it, and never letting
// it change again. The module is the SOLE writer of the invoices,
// invoice_lines and invoice_series tables (Principle 2.3).
//
// # The one guarantee
//
// Within a series the numbers run 1, 2, 3 with nothing missing and nothing
// repeated. The reason is legal rather than technical: a tax authority reading
// a series that jumps from 41 to 43 sees a document that was issued and then
// made to disappear. Everything about the design follows from it — the number
// is taken inside the transaction that writes the document, there is no draft
// state to hand a number to and abandon, and a canceled document keeps its
// number and stays in the table.
//
// That is also why the module does NOT use a database sequence, which the order
// module does use for its order numbers: a sequence advances outside the
// transaction, so a rollback burns its value. For an order number that hole is
// harmless; here it is the thing being prevented.
//
// # What it does not know
//
// It does not know what an order is, what a customer is, or what an e-invoice
// regime is. It is handed a finished document — two parties, a list of lines, a
// set of totals — and it checks that the document adds up, numbers it and
// stores it. Assembling a document FROM an order is a workflow's job
// (ADR 0001/0006), and transmitting one to a tax authority is a plugin's.
//
// # What it deliberately does not do
//
// It does not render a PDF and it does not speak to a tax authority. A
// framework cannot file an invoice on a merchant's behalf: that needs the
// merchant's own certificate and a contract with an integrator. What a
// framework owes is the document, its numbering and a place for the
// transmission to plug in.
//
// # The surfaces it exposes
//
//   - "invoice.service" — the rich in-module surface.
//   - POST/GET /admin/v1/invoices, GET /admin/v1/invoices/{id},
//     POST /admin/v1/invoices/{id}/status, GET /admin/v1/invoice-series.
//
// There is no storefront surface (see the api package) and no cross-module
// interop surface: nothing else reads invoices today, and a contract opened
// without a consumer can never be narrowed again.
package invoice

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/module"
	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/invoice/api"
	"github.com/bdrtr/gobit/internal/modules/invoice/repository"
	"github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// ModuleName is the name of the module; it is the prefix of the container names
// and of the migration version ledger.
const ModuleName = "invoice"

// ServiceName is the name of the module's service in the container.
const ServiceName = ModuleName + ".service"

// svcDB is the name of the core database pool in the container.
const svcDB = "core.db"

// codeSetupFailed is returned when the module cannot be set up.
const codeSetupFailed = "invoice_module_setup_failed"

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot is the embedded files with the "migrations/" prefix stripped:
// db.Migrate reads the source from the root.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// Options are the module's setup settings.
type Options struct {
	// Logger falls back to slog.Default when nil.
	Logger *slog.Logger
}

// Module is the implementation the invoice module offers to the core.
type Module struct {
	opts    Options
	svc     *service.Service
	handler *api.Handler
}

// That the core's contract is satisfied is pinned down at compile time.
var _ module.Module = (*Module)(nil)

// That it can describe the document is pinned down at compile time as well.
//
// [openapi.Describer] is an OPTIONAL interface the composition root looks for
// with a type assertion; if the method name or signature slipped, nothing would
// break at compile time and this module's endpoints would simply drop out of
// the document.
var _ openapi.Describer = (*Module)(nil)

// New produces an invoice module ready to be registered.
func New(opts Options) *Module {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	return &Module{opts: opts}
}

// Name returns the module's unique name.
func (m *Module) Name() string { return ModuleName }

// Migrations returns the module's migration files.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Register builds the service and puts it into the container.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, svcDB)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the database pool (%q)", ModuleName, svcDB)
	}

	log := m.opts.Logger.With("module", ModuleName)

	svc := service.New(repository.New(pool.Pool()), service.Options{Logger: log})

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}

	m.svc = svc
	m.handler = api.New(svc)

	log.DebugContext(ctx, "invoice module registered", "service", ServiceName)

	return nil
}

// Routes mounts the module's endpoints on the router.
//
// If Register did not run, nothing is mounted: an endpoint that does not exist
// is better than a handler without a service panicking on the first request.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
		slog.Default().Warn("Routes was called on the invoice module without Register, no route was mounted")

		return
	}

	m.handler.Routes(r)
}

// Describe writes the module's endpoints into the OpenAPI document.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// mustSub returns the sub-filesystem or panics.
//
// It runs at package initialization and its failure would mean the embedded
// directory is missing — a build-time mistake rather than a runtime condition.
func mustSub(f embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}

	return sub
}
