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
//   - "invoice.interop" — the PRIMITIVE surface the invoicing flow reads.
//   - POST/GET /admin/v1/invoices, GET /admin/v1/invoices/{id},
//     POST /admin/v1/invoices/{id}/status, GET /admin/v1/invoice-series.
//
// There is no storefront surface (see the api package): a document is a record
// between the shop and the tax authority, and what a customer receives is a
// copy the shop sends them.
package invoice

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/erasure"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/core/module"
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

// InteropName is the name of the module's PRIMITIVE cross-module surface.
//
// The invoicing flow resolves it by this name. It exists because a workflow may
// not import a module in either direction (ADR 0006) — the narrow surface is
// the mechanism, not a concession.
const InteropName = ModuleName + ".interop"

// svcDB is the name of the core database pool in the container.
const svcDB = "core.db"

// svcLink is the name of the core's Module Links service in the container.
const svcLink = "core.link"

// codeSetupFailed is returned when the module cannot be set up.
const codeSetupFailed = "invoice_module_setup_failed"

// codeLinkDefine is returned when a link definition cannot be declared.
const codeLinkDefine = "invoice_module_link_define_failed"

// codeNotRegistered is returned when a capability is used before Register ran.
const codeNotRegistered = "invoice_module_not_registered"

// The two tables the personal data declaration is read off.
//
// They are constants rather than fifteen repeated literals because a typo in
// one entry of a STATIC declaration produces a holding that points at a table
// which does not exist, and nothing about a static value would ever notice: it
// compiles, it serializes, and an embedder goes looking in the wrong place.
const (
	tableInvoices     = "invoices"
	tableInvoiceLines = "invoice_lines"
)

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

// That the module answers an erasure request, and can say what it keeps, is
// pinned down at compile time for the same reason.
//
// [erasure.Eraser] and [erasure.Declarer] are found by type assertion too, so a
// renamed method or a changed signature would not break the build — this module
// would simply stop being asked, and an erasure report would come back without
// it. A missing OpenAPI path costs a reader an endpoint; a holder missing from
// an erasure report costs a controller a true answer to a person who asked to
// be forgotten (ADR 0029).
var (
	_ erasure.Eraser   = (*Module)(nil)
	_ erasure.Declarer = (*Module)(nil)
)

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

	links, err := container.Resolve[link.LinkService](c, svcLink)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the link service (%q)", ModuleName, svcLink)
	}

	// The link definitions are verified idempotently at startup (ADR 0005). A
	// definition may be declared ONLY ONCE, so order_invoice is declared by this
	// module and not by the order module — this is the side that writes the
	// record the binding carries (see [service.LinkOrderInvoice]).
	for _, def := range service.Definitions() {
		if err := links.Define(ctx, def); err != nil {
			return errors.Wrap(err, errors.KindOf(err), codeLinkDefine,
				"the %q link definition could not be declared", def.Name)
		}
	}

	log := m.opts.Logger.With("module", ModuleName)

	svc := service.New(repository.New(pool.Pool()), service.Options{Logger: log})

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}
	if err := c.Provide(InteropName, service.NewInterop(svc)); err != nil {
		return err
	}

	m.svc = svc
	m.handler = api.New(svc)

	log.DebugContext(ctx, "invoice module registered",
		"service", ServiceName, "interop", InteropName)

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

// Erase answers an erasure request about a person, and the answer is that the
// documents are kept.
//
// The module delegates to the service rather than answering here, because the
// answer needs a row count and a count is a read of the module's own storage —
// the composition root has no business holding a repository. What the refusal
// MEANS, why it is the only available answer and why the count is taken at all
// are argued at [service.Service.Erase].
//
// Without Register there is no service to ask, and that is an error rather than
// a Retained with no count. A report that silently carried a zero would tell a
// controller that this person has no invoices, which is not something an
// unregistered module is in any position to say.
func (m *Module) Erase(ctx context.Context, s erasure.Subject) (erasure.Result, error) {
	if m.svc == nil {
		return erasure.Result{}, errors.Internal(codeNotRegistered,
			"the %s module was asked to erase before Register ran, so it cannot say what it holds",
			ModuleName)
	}

	return m.svc.Erase(ctx, s)
}

// PersonalData declares every place this module keeps something about a person.
//
// It lives on the module rather than on the service and takes no database,
// because a declaration is a property of the CODE: it is the same sentence on
// an empty database and a full one, it is what an audit reads without a
// connection, and it has to be answerable by a module that failed to register.
//
// # What is declared, and why the list is longer than the refusal's
//
// Fifteen columns across two tables, and [Module.Erase] can reach nine of them.
// Twelve are the two PARTIES the document prints, copied in full at the moment
// it was issued (migration 000001) and never updated afterwards.
//
// The BUYER half is the reachable half. lower(buyer_email) is the only handle
// this module has on a person — invoices carries no customer_id and no order_id
// — so those six columns are what a Retained answer names as kept.
//
// The SELLER half is declared, is never searched and is never reported, and
// each of its six entries says that in its own Why rather than leaving a reader
// to work it out. The data is really there: the seller is the party that ISSUED
// the document, which for a sole trader is a natural person and on a
// marketplace is a different natural person on every invoice, and an audit that
// did not know would look in the wrong place. But an erasure request naming a
// sole trader's own address counts ZERO invoices and lists nothing, because
// nothing about these columns was looked at — Erase resolves through
// buyer_email alone. Declaring a column is not a promise to erase it or even to
// search it, and that is why [erasure.Declarer] is a separate interface from
// [erasure.Eraser]. What the declaration owes in exchange is to not let the two
// be mistaken for each other: a column declared as a place a person is, that
// the module silently never visits, reads as a reach this module does not have.
// Reaching a seller's own data is the controller's job and the declaration says
// so out loud. The invariant a test holds
// (internal/modules/invoice/erasure_test.go): a declared column that
// [service.RetainedColumns] does not list must SAY that Erase never searches
// it.
//
// The remaining three are [erasure.Open]: metadata is a jsonb the caller fills
// and nothing validates, invoice_lines.description is caller text, and
// status_reason is a sentence an operator typed. gobit does not read them and
// does not guess what is in them, and ADR 0032 states the consequence as a rule
// the embedder answers for: personal data written into those fields is retained
// under the same refusal as the named columns.
func (m *Module) PersonalData() erasure.Declaration {
	return erasure.Declaration{
		Holder: ModuleName,
		Holdings: []erasure.Holding{
			{
				Table: tableInvoices, Column: "buyer_name", Kind: erasure.Named,
				Why: "the name of the person or company the document was issued to, as it is printed on it",
			},
			{
				Table: tableInvoices, Column: "buyer_tax_number", Kind: erasure.Named,
				Why: "the buyer's tax or national identification number, when the document carries one",
			},
			{
				Table: tableInvoices, Column: "buyer_tax_office", Kind: erasure.Named,
				Why: "the tax office the buyer is registered with, which locates them administratively",
			},
			{
				Table: tableInvoices, Column: "buyer_email", Kind: erasure.Named,
				Why: "the buyer's e-mail address; it is also the ONLY column by which this module can find a person at all",
			},
			{
				Table: tableInvoices, Column: "buyer_address", Kind: erasure.Named,
				Why: "the buyer's postal address as printed on the document",
			},
			{
				Table: tableInvoices, Column: "buyer_country_code", Kind: erasure.Named,
				Why: "the buyer's country, which is part of the address and decides how the sale was taxed",
			},
			{
				Table: tableInvoices, Column: "seller_name", Kind: erasure.Named,
				Why: "the name of the party that ISSUED the document; for a sole trader that is a person's name. Erase never searches this column and never lists it as kept: a subject is resolved through the buyer address alone, so reaching a seller here is the controller's own job",
			},
			{
				Table: tableInvoices, Column: "seller_tax_number", Kind: erasure.Named,
				Why: "the seller's tax or national identification number, which for a sole trader identifies a person. Erase never searches this column and never lists it as kept; it identifies the issuer of the document",
			},
			{
				Table: tableInvoices, Column: "seller_tax_office", Kind: erasure.Named,
				Why: "the tax office the seller is registered with, which locates the issuer administratively. Erase never searches this column and never lists it as kept",
			},
			{
				Table: tableInvoices, Column: "seller_email", Kind: erasure.Named,
				Why: "the seller's e-mail address as printed on the document. Erase never searches this column and never lists it as kept: an erasure request naming this very address counts ZERO invoices, because only lower(buyer_email) is matched",
			},
			{
				Table: tableInvoices, Column: "seller_address", Kind: erasure.Named,
				Why: "the seller's postal address, which for a sole trader is frequently a home address. Erase never searches this column and never lists it as kept, so an audit of a sole trader's own data starts here rather than from an erasure report",
			},
			{
				Table: tableInvoices, Column: "seller_country_code", Kind: erasure.Named,
				Why: "the seller's country, part of the printed address. Erase never searches this column and never lists it as kept",
			},
			{
				Table: tableInvoices, Column: "status_reason", Kind: erasure.Open,
				Why: "free text an operator typed when the document was canceled or re-sent; it may name or describe anyone, and gobit does not read it",
			},
			{
				Table: tableInvoices, Column: "metadata", Kind: erasure.Open,
				Why: "a jsonb the caller fills and nothing validates; whatever the embedder put there about the buyer is here and gobit does not inspect it",
			},
			{
				Table: tableInvoiceLines, Column: "description", Kind: erasure.Open,
				Why: "the printed text of a line, supplied by the caller; an engraving, a delivery note or a customer's own words can be in it",
			},
		},
	}
}

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
