// Package inventory is the stock module: inventory items, locations, levels and
// reservations (plan Section 6, Phase 4).
//
// The module owns its own tables and imports NO other module (Principle
// 2.1/2.4, ADR 0001). It exposes five surfaces:
//
//   - Service: in the container under [ServiceName]. The complete_cart saga of
//     Phase 6 calls the stock step and its compensation from here.
//   - The cross-module PRIMITIVE surface: in the container under [InteropName]
//     (ADR 0006). It is registered SEPARATELY from the service, because it
//     speaks only in primitive types; the checkout and returns flows touch
//     stock through it.
//   - Query provider: in the container under "inventory_item.query" (ADR 0004).
//     Records come back together with their total sellable quantity; product's
//     storefront listing sees the product and its stock in one call.
//   - The ADMIN WRITE surface: in the container under [AdminName] (ADR 0013).
//     It is the name the admin panel (internal/adminui) resolves.
//   - Admin API: /admin/v1/stock-locations and /admin/v1/inventory-items.
//
// It DOES NOT declare the link definition: the "product_variant_inventory" link
// between a variant and an inventory item is declared by the product module,
// which owns the relationship.
package inventory

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
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/inventory/api"
	"github.com/bdrtr/gobit/internal/modules/inventory/repository"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// Container names.
const (
	// ModuleName is the module's name; it is also the prefix of the migration
	// version table.
	ModuleName = "inventory"
	// ServiceName is the module service's name in the container. Other modules
	// resolve the service by this name and use it through a narrow interface
	// they define in their OWN packages (ADR 0001).
	ServiceName = ModuleName + ".service"
	// InteropName is the cross-module primitive surface's name in the container
	// (ADR 0006).
	//
	// It is registered SEPARATELY from the service: the service speaks in
	// inventory's rich types, this surface only in primitive and stdlib types.
	// Sagas resolve it through a narrow interface they define themselves.
	InteropName = ModuleName + ".interop"
	// ProviderName is the query provider's name in the container (ADR 0004).
	ProviderName = service.EntityName + query.ProviderSuffix
	// AdminName is the name of the module's ADMIN WRITE surface in the container
	// (ADR 0013). Besides writing it also carries a READ: the query provider
	// does not offer the per-location breakdown, and an operator cannot adjust
	// stock from a total.
	AdminName = ModuleName + ".admin"
	// dbServiceName is the core database pool's name in the container.
	dbServiceName = "core.db"
	// linkServiceName is the core link service's name in the container.
	linkServiceName = "core.link"
)

// The table names the personal data declaration mentions.
//
// The constants bring a typo down to one place; they do not prove correctness:
// that the declaration really names columns in the schema is shown by the
// audit that reads the migrations (erasure_test.go).
const (
	tableStockLocations = "stock_locations"
	tableInventoryItems = "inventory_items"
	tableReservations   = "inventory_reservations"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationsRoot is the root directory of the migration files.
//
// The golang-migrate source looks at the root (iofs.New(src, ".")), whereas the
// embed.FS keeps the files under "migrations/"; that is why the subtree is
// opened once, here.
var migrationsRoot = mustSub(migrationsFS, "migrations")

// Module implements the inventory module's core contract.
type Module struct {
	svc *service.Service
	// links is the core service that writes the warehouse↔channel binding; it
	// is resolved in Register.
	links api.ChannelBindings
}

// That the module satisfies the core contract is verified at compile time.
var _ module.Module = (*Module)(nil)

// That it can describe itself for the document is pinned down at compile time
// too.
//
// [openapi.Describer] is an OPTIONAL interface and the composition root looks
// for it with a TYPE ASSERTION; should the method name or signature drift,
// nothing would break at compile time — only the stock endpoints would
// silently fall out of the document. This line closes that silence.
var _ openapi.Describer = (*Module)(nil)

// That it can declare personal data is pinned down for the same reason.
//
// [personaldata.Declarer] is optional too and the sweeper looks for it with a
// TYPE ASSERTION; that is why the pin sits next to [openapi.Describer]'s. The
// module does NOT implement [personaldata.Eraser], and that is not unfinished
// work: an erasure subject carries a customer ID and an email address, no
// column of this module holds either of them, so the module cannot find a
// person. The coordinator lists a holder that declares but cannot erase as
// RETAINED in every report — the declaration is the only way the module becomes
// visible in the answer given to a data subject.
var _ personaldata.Declarer = (*Module)(nil)

// New produces an inventory module ready to be registered.
func New() *Module {
	return &Module{}
}

// Name returns the module's name.
func (m *Module) Name() string {
	return ModuleName
}

// Register registers the service and the query provider in the container.
//
// Only the CORE service (core.db) is resolved; no other module's service is
// resolved here, because at this stage it may not be registered yet (see the
// module.Module contract). Resolving core.db here is safe because main.go
// registers it as a ready value before the modules come up, and its absence is
// a setup error under which the module could never work — it is not quietly
// postponed.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, dbServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), "inventory_db_unavailable",
			"the %s module could not resolve the %q service", ModuleName, dbServiceName)
	}

	links, err := container.Resolve[link.LinkService](c, linkServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), "inventory_link_unavailable",
			"the %s module could not resolve the %q service", ModuleName, linkServiceName)
	}

	svc := service.New(repository.New(pool.Pool()), slog.Default())

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}
	if err := c.Provide(InteropName, service.NewInterop(svc)); err != nil {
		return err
	}
	if err := c.Provide(ProviderName, service.NewQueryProvider(svc)); err != nil {
		return err
	}
	// The admin surface is registered under a SEPARATE name; the reason is at
	// [AdminName].
	if err := c.Provide(AdminName, service.NewAdminSurface(svc)); err != nil {
		return err
	}

	// The link definitions are declared HERE: the schema stays next to the
	// definition and is verified idempotently on every startup (ADR 0005). The
	// link's tables belong to core/link, which is why they have no place in the
	// module's migrations.
	for _, def := range service.Definitions() {
		if err := links.Define(ctx, def); err != nil {
			return errors.Wrap(err, errors.KindOf(err), "inventory_link_define_failed",
				"the %q link definition could not be declared", def.Name)
		}
	}

	m.svc = svc
	m.links = links
	slog.Default().DebugContext(ctx, "inventory module registered",
		"service", ServiceName, "provider", ProviderName)
	return nil
}

// Migrations returns the module's migration files.
func (m *Module) Migrations() fs.FS {
	return migrationsRoot
}

// Routes mounts the module's admin routes on the router.
//
// No route is mounted before Register is called: a handler without a service
// would meet every request with a panic; staying quiet (no route -> 404) is
// safer, and Bootstrap runs Register before Routes anyway.
func (m *Module) Routes(r chi.Router) {
	if m.svc == nil {
		slog.Default().Warn("Routes was called on the inventory module without Register, no route was mounted")
		return
	}
	api.NewHandler(m.svc, m.links).Routes(r)
}

// Describe writes the module's admin endpoints into the OpenAPI document.
//
// The description itself lives in [api.Describe]: the body schemas are derived
// from that package's unexported types, and exporting the types only for the
// sake of the document would widen the module's surface.
//
// Unlike [Module.Routes] there is NO service check, and none is needed: the
// schema comes from the types, not from the service. Adding a check would
// silently empty the document of a module that is not set up, too.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// PersonalData says where the module keeps personal data (ADR 0029).
//
// It takes no context and does not touch the database: the declaration is a
// property of the CODE, the same sentence on an empty install and on a full
// one, and an audit reads it without a connection. That is also why, unlike
// [Module.Routes], there is no service check; a module that has not been
// registered must still be able to say what it holds.
//
// Holder is left EMPTY. The coordinator fills it in with the name from the
// registry; writing the name here as well would be a copy the two places could
// silently drift apart on.
//
// # Whose address this is
//
// The weight of the declaration below is a WAREHOUSE address, and a warehouse
// is the OPERATOR's own premises, not the shopper's. In a small install it is
// often the operator's HOME address: personal data belonging to a real human
// being, but that human being is not a data subject shopping at the store, it
// is the trader themselves. The distinction has two consequences at once, and
// both were chosen deliberately.
//
// It IS DECLARED. The declaration answers the question "where does this
// install keep personal data", not "where is this person's data". Left
// undeclared, the operator's OWN access or erasure request would go
// unanswered, and on top of that the module would never appear in any report
// at all. The invoice module came to the same conclusion for seller_address: in
// a sole proprietorship the seller is a natural person.
//
// It IS NOT ERASED. A customer's erasure request cannot empty the store's
// warehouse. The first reason the module does not implement
// [personaldata.Eraser] is that it cannot resolve the subject in the first
// place; this is the second — in a shopper's sweep there is NO work to do in
// this table.
//
// # The contract cannot carry this distinction
//
// [personaldata.Holding] carries Table, Column, Kind and Why; it has no field
// to state the CLASS OF THE DATA SUBJECT. That is why the distinction was
// written into the first clause of every Why ("the operator's own premises
// rather than a shopper's"). That is the only place available today and it IS
// NOT ENOUGH: the sweep report does not carry Why — the path that turns a
// holder that declares but does not erase into RETAINED lists only
// "table.column" and writes the sentence itself — so the distinction is
// visible only in the GET /admin/v1/personal-data document. In a report given
// to a shopper, the warehouse's seven columns stand there without saying whose
// they are.
//
// # Named versus Open
//
// gobit DEFINED the columns of stock_locations: address_1 is a postal address
// line and the framework knows WHAT the field IS. That the operator wrote the
// value does not change that; the customer's name is written by the customer,
// too. The free-text fields (title, description) are Open: only the embedding
// application can decide what goes into them, and gobit does not read them
// (ADR 0029).
//
// # What is not declared
//
// inventory_levels does not appear AT ALL: it consists of two IDs and two
// numbers and carries not a single piece of text about a person.
// inventory_reservations.line_item_id does not appear either — it is a bare
// foreign ID pointing at a cart line, and once the columns beside it are
// anonymized it points at nobody. sku is a GOODS code. By contrast,
// inventory_reservations.description is the free text of the module's only
// PER-PERSON row — a reservation is born from a shopper's cart line — so it is
// the one place in this module where personal data belonging to a store
// customer can sit.
func (m *Module) PersonalData() personaldata.Declaration {
	return personaldata.Declaration{
		Holdings: []personaldata.Holding{
			{
				Table: tableStockLocations, Column: "name", Kind: personaldata.Named,
				Why:       "the operator's own premises rather than a shopper's — the warehouse or shop name, which in a one-person business is frequently the trader's own name",
				OnErasure: personaldata.Kept,
			},
			{
				Table: tableStockLocations, Column: "address_1", Kind: personaldata.Named,
				Why:       "the operator's own premises rather than a shopper's — the street line the goods sit at, which for a sole trader is frequently a home address",
				OnErasure: personaldata.Kept,
			},
			{
				Table: tableStockLocations, Column: "address_2", Kind: personaldata.Named,
				Why:       "the operator's own premises rather than a shopper's — flat, floor or door, which narrows the street line to a single household",
				OnErasure: personaldata.Kept,
			},
			{
				Table: tableStockLocations, Column: "city", Kind: personaldata.Named,
				Why:       "the operator's own premises rather than a shopper's — the town the warehouse is in",
				OnErasure: personaldata.Kept,
			},
			{
				Table: tableStockLocations, Column: "province", Kind: personaldata.Named,
				Why:       "the operator's own premises rather than a shopper's — the province or state of the warehouse address",
				OnErasure: personaldata.Kept,
			},
			{
				Table: tableStockLocations, Column: "postal_code", Kind: personaldata.Named,
				Why:       "the operator's own premises rather than a shopper's — the postal code of the warehouse, which in some countries reaches one building",
				OnErasure: personaldata.Kept,
			},
			{
				Table: tableStockLocations, Column: "country_code", Kind: personaldata.Named,
				Why:       "the operator's own premises rather than a shopper's — the country of the warehouse address, the least identifying part of it and declared because it is part of it",
				OnErasure: personaldata.Kept,
			},
			{
				Table: tableInventoryItems, Column: "title", Kind: personaldata.Open,
				Why:       "free text the shop types to name a stock item; it names goods rather than people, but a made-to-order item is routinely titled after the person it is being made for and gobit does not read it",
				OnErasure: personaldata.Kept,
			},
			{
				Table: tableInventoryItems, Column: "description", Kind: personaldata.Open,
				Why:       "free text the shop types about a stock item; gobit puts nothing in it and never reads it, so whether a person is described there is the controller's judgement",
				OnErasure: personaldata.Kept,
			},
			{
				Table: tableReservations, Column: "description", Kind: personaldata.Open,
				Why:       "free text on a reservation, and the only column in this module on a row born of one shopper's checkout, so a note naming that shopper lands here; gobit writes nothing into it",
				OnErasure: personaldata.Kept,
			},
		},
	}
}

// mustSub opens the sub file system; it panics if it cannot be opened.
//
// The error path is unreachable, because //go:embed guarantees at compile time
// that the directory exists. Returning nil silently would nevertheless mean the
// module coming up without migrations (that is, without tables); a setup error
// must blow up openly.
func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("inventory: the migration directory could not be opened: " + err.Error())
	}
	return sub
}
