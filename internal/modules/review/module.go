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
