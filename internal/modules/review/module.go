// Package review is the review module.
//
// Its responsibility in one sentence: taking what a customer writes about a
// product, keeping it out of sight until a person has read it, and showing what
// that person approved. The module is the SOLE writer of the reviews table
// (Principle 2.3).
//
// # The one guarantee
//
// A review is INVISIBLE on the storefront until an operator approves it. Every
// storefront read reaches SQL that carries status = 'approved' as a literal,
// there is no method or endpoint that takes the status as an argument, and
// there is no storefront read of a single review by id at all.
//
// # Why that is the design and not a preference
//
// Decision A15 in docs/gaps.md asks whether the storefront may accept content
// from a party this framework cannot identify, and it carries one discriminator:
// does a human stand between the write and its effect? The repository's
// precedent for a yes is the order module's storefront return request — written
// by anyone holding the order id, moving no stock and no money, doing nothing
// until an operator acts. A review published on APPROVAL has that exact shape,
// so the return request's argument covers it unchanged. A review published on
// SUBMISSION does not, and would put an anonymous writer directly onto the
// shop's product page.
//
// # What it does not know
//
// It does not know what a product is. The subject of a review is a product
// identifier belonging to another module, stored and never validated
// (Principle 2.2) — the same rule the order module's line follows for the
// variant it sold. A review of a product that does not exist is handled by the
// thing that handles every other unwanted review: an operator does not approve
// it.
//
// It also does not know who wrote the review, and does not pretend to. There is
// no order id on the row, so "verified purchase" is not expressible here — an
// order id would prove that the writer holds one, which under ADR 0008 is not
// the same as being the buyer.
//
// # What it stores about a person
//
// One thing: a display name the author typed in order to have it printed. No
// email address, no phone number, no network address. The reasoning is in the
// migration, and it is the same one that keeps the recipient's address out of
// the notification module and out of every event payload.
//
// # The surfaces it exposes
//
//   - "review.service" — the in-module surface the handler is built on.
//   - POST/GET /store/v1/products/{product_id}/reviews,
//     GET /store/v1/products/{product_id}/review-summary.
//   - GET /admin/v1/reviews, GET /admin/v1/reviews/{id},
//     POST /admin/v1/reviews/{id}/status.
//
// There is deliberately NO primitive interop surface and NO read-layer Query
// provider. Both would be contracts with no consumer in this change, and a
// published capability nobody reads is this repository's most expensive
// recurring defect — the arch audits fail an interop surface no production file
// resolves, and they are right to. The day the AI subsystem reads reviews for
// summaries (C11 in docs/gaps.md), the provider arrives WITH that reader.
package review

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
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/review/api"
	"github.com/bdrtr/gobit/internal/modules/review/repository"
	"github.com/bdrtr/gobit/internal/modules/review/service"
)

// ModuleName is the name of the module; it is the prefix of the container names
// and of the migration version ledger.
const ModuleName = "review"

// ServiceName is the name of the module's service in the container.
const ServiceName = ModuleName + ".service"

// svcDB is the name of the core database pool in the container.
const svcDB = "core.db"

// tableReviews is the module's only table; the personal-data declaration names
// it and the audit test reads its columns out of the migration.
const tableReviews = "reviews"

// codeSetupFailed is returned when the module cannot be set up.
const codeSetupFailed = "review_module_setup_failed"

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

// Module is the implementation the review module offers to the core.
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

// That it can say what it holds about people is pinned down the same way.
//
// [personaldata.Declarer] is another OPTIONAL interface the composition root finds
// with a type assertion, so a slipped name or signature would produce no build
// error at all — it would produce a sweep in which this module never answers.
// The cost of that is worse than a missing path in a document: a controller
// answering a person would be told, in a report that looks complete, that
// nothing here holds anything about them.
var _ personaldata.Declarer = (*Module)(nil)

// New produces a review module ready to be registered.
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
//
// It declares no link definition. A review is bound to a product by a column
// this module owns and does not join on; a link would be the right shape for a
// record the binding CARRIES, and there is none — the review IS the record.
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

	log.DebugContext(ctx, "review module registered", "service", ServiceName)

	return nil
}

// Routes mounts the module's endpoints on the router.
//
// If Register did not run, nothing is mounted: an endpoint that does not exist
// is better than a handler without a service panicking on the first request.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
		slog.Default().Warn("Routes was called on the review module without Register, no route was mounted")

		return
	}

	m.handler.Routes(r)
}

// Describe writes the module's endpoints into the OpenAPI document.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// PersonalData declares every place this module keeps something about a person.
//
// It lives on the module and takes no database because a declaration is a
// property of the CODE: it reads the same on an empty installation as on a full
// one, an audit reads it without a connection, and a module whose Register
// failed can still say what its tables hold.
//
// # This module holds personal data and cannot say WHOSE
//
// That is the whole reason [personaldata.Declarer] and [personaldata.Eraser] are two
// interfaces instead of one, and this module is the case that forced them
// apart (ADR 0033).
//
// A review carries exactly ONE identifying field, author_name, and the
// migration header argues at length why it is the only one: a byline is data
// given TO BE PUBLISHED, while an e-mail address, a network address or an order
// id would be data taken for something else, so decision A15 refuses all three.
// The consequence is the part that has to be said out loud here. Nothing on the
// row points at a person this framework can look up — there is no customer id,
// no address, no order — so an [personaldata.Subject] resolves to no row in this
// table, and matching on the byline is not a resolution: two shoppers share a
// display name as easily as two people share a name, and erasing on that basis
// would erase strangers.
//
// So the module declares and offers no erasure. The two alternatives were to
// implement [personaldata.Eraser] and report zero rows for everybody, which is a
// well-formed lie, or to stay silent, which keeps this table out of every
// report a controller ever publishes. Declaring puts it in each of them as a
// Retained entry listing these columns, which is the true answer: the data is
// here, gobit cannot reach the person in it, and what to do about that is the
// embedder's decision with information gobit does not have.
//
// # What is not declared
//
// The row's id and product_id are identifiers and neither is a personal datum:
// product_id says what the review is ABOUT, and an id resolves to nobody once
// the columns beside it are gone. The rating, the status, the moderation
// timestamp and the row timestamps hold nothing about anyone — no column
// records WHICH operator decided. The audit that keeps this paragraph honest is
// in erasure_test.go, and it reads the migration rather than this list.
func (m *Module) PersonalData() personaldata.Declaration {
	// Holder is left empty deliberately: the coordinator fills it in from the
	// name the registry knows this module by, and a second copy here would be
	// free to drift from it.
	return personaldata.Declaration{
		Holdings: []personaldata.Holding{
			{
				Table: tableReviews, Column: "author_name", Kind: personaldata.Named,
				Why: "the byline a member of the public typed in order to have it printed under their review, and the only identifying thing stored about them; gobit cannot find this person's reviews from a customer id or an e-mail address, so acting on them is the embedder's decision with information gobit does not have",
			},
			{
				Table: tableReviews, Column: "title", Kind: personaldata.Open,
				Why: "the headline the author typed; it is free text nobody validates and gobit does not read it, so it can carry a name, an address or a third party",
			},
			{
				Table: tableReviews, Column: "body", Kind: personaldata.Open,
				Why: "the review itself, written by a member of the public about a product; it is their own words about their own purchase and gobit does not inspect them",
			},
			{
				Table: tableReviews, Column: "moderation_note", Kind: personaldata.Open,
				Why: "free text an operator typed when approving or rejecting the review, which may quote or describe the author; gobit does not read it",
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
