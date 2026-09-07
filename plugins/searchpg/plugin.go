// Package searchpg is the plugin that adds product search to gobit.
//
// # The search engine is NOT an external service
//
// The index and the query are PostgreSQL's own full text search: a document
// lives in a tsvector column, a match is found through a GIN index, and the
// ranking is done with ts_rank (why not ts_rank_cd: see [searchSQL], with the
// measurement). Meilisearch and OpenSearch were deliberately NOT chosen — each
// would bring a new external dependency, a new compose service, a new health
// check and a new failure class of "the index and the database have parted
// company". The PostgreSQL that is already there gives a REAL search before the
// scale grows.
//
// The decision is reversible, and that is exactly what the plugin boundary is
// worth: changing the engine changes ONLY this package. Neither the core, nor
// the product module, nor any other module knows this plugin exists; its name
// appears nowhere outside one line in the installation file and the PLUGINS
// environment variable.
//
// # The three extension points it uses
//
//  1. [coreplugin.Host.AddModule] — the plugin brings its OWN module: its own
//     table, its own migration, its own version ledger ("searchpg") and its own
//     routes. The module goes through the SAME lifecycle as the core modules.
//  2. [coreplugin.Host.Subscribe] — it listens to "product.created",
//     "product.updated" and "product.deleted" to keep the index fresh.
//  3. The module's Routes — it opens GET /store/v1/search and
//     POST /admin/v1/search/reindex.
//
// # It imports NO module
//
// The plugin cannot import product (internal/arch TestPluginsDoNotImportModules).
// It reaches the catalog record through the narrow [StoreProductReader]
// interface defined in this package and BY NAME from the container
// ("product.interop"); the resolution is LAZY, because at Setup time no module
// has come up yet (see [catalog.resolve]).
//
// # Channel filtering is NOT repeated here
//
// Which product appears in which sales channel is the CATALOG's rule. The
// plugin produces only RELEVANCE-ORDERED IDS from the index; it asks for the
// records to show through "product.interop" and adds the channel ids to the
// request. Rewriting the filter here would produce a second definition of the
// rule, and the day the two definitions parted company, search would become a
// BYPASS of channel filtering.
//
// # The text search configuration: 'simple'
//
// Documents and queries are produced with the 'simple' dictionary, which means
// there is NO stemming and NO stop-word removal. The alternative, 'english',
// reduces words to the wrong stems in a Turkish catalog — "kalemler" is not cut
// back to "kalem" but pruned by English rules — and PostgreSQL ships no built-in
// Turkish dictionary. In a framework that does not know its installation's
// language, no stemming at all is more predictable than stemming in the wrong
// one. The price is plain and it is accepted: a search for "kalem" does NOT find
// a product that says "kalemler". When that limit is reached, the right step is
// to change the engine rather than to leak a dictionary setting in here.
//
// Case folding depends on PostgreSQL's ctype setting: on a cluster created with
// the C locale, NON-ASCII letters are not folded and a search for "Gömlek" does
// not find a product that says "gömlek". That the cluster was created with a
// UTF-8 locale is therefore a precondition of search.
//
// # Use
//
//	PLUGINS=search-pg
//
// It asks for no configuration of its own; the index table is created at startup
// by the migration. An empty index returns nothing, so on an existing catalog
// the first step is to call POST /admin/v1/search/reindex.
package searchpg

import (
	"context"

	coreplugin "github.com/bdrtr/gobit/core/plugin"
)

// Name is the plugin's name in the registry; this is what goes in the PLUGINS list.
const Name = "search-pg"

// ModuleName is the name of the module this plugin brings.
//
// It DIFFERS from the plugin's name and it has to: the module name turns
// directly into an SQL table name ("searchpg_schema_migrations") and core/db's
// ownership pattern does not allow a hyphen. The name is also the prefix of the
// scope the admin endpoint requires (see [ScopeWrite]).
const ModuleName = "searchpg"

// This block is a CONTRACT between modules and its values are repeated BY HAND.
//
// Because a plugin cannot import any module (ADR 0001), these names cannot be
// bound to product's constants — exactly as the core repeats
// coreplugin.PaymentProvidersName by hand. Every hand-repeated constant is open
// to silent drift, and the cost of drift here is concrete: if an event name
// changes the plugin receives no events at all, and if the interop name changes
// the search endpoint answers 503 on every request. No compiler catches either —
// the arch tests do not audit a plugin from this direction (see the note in the
// package's tests).
const (
	// catalogInteropName is the container name of the product module's PRIMITIVE
	// read surface (product.InteropName).
	catalogInteropName = "product.interop"
	// catalogEntity is the products' entity name in the Query layer; the reindex
	// pages ids under this name (product service.EntityProduct).
	catalogEntity = "product"
	// catalogStatusFilter is the Query provider's publication status filter.
	catalogStatusFilter = "status"
	// catalogStatusPublished is the only publication status visible in the storefront.
	catalogStatusPublished = "published"
	// eventProductCreated is the new-product event (product service.EventProductCreated).
	eventProductCreated = "product.created"
	// eventProductUpdated is the product update event.
	eventProductUpdated = "product.updated"
	// eventProductDeleted is the product deletion event.
	eventProductDeleted = "product.deleted"
	// eventFieldProductID is the product id key in the event payload.
	eventFieldProductID = "product_id"
)

// The names of the CORE services resolved from the container.
const (
	svcDB    = "core.db"
	svcQuery = "core.query"
)

// Plugin is the PostgreSQL-backed search plugin.
type Plugin struct {
	// mod is the module the plugin brings; the subscriptions are its methods too.
	// It is built in Setup and completed by [searchModule.Register].
	mod *searchModule
}

// That the plugin satisfies the core contract is fixed at compile time.
var _ coreplugin.Plugin = (*Plugin)(nil)

// New builds the plugin.
func New() *Plugin { return &Plugin{} }

// Name returns the plugin's name.
func (p *Plugin) Name() string { return Name }

// Setup adds the module to the registry and subscribes to the catalog events.
//
// It asks for NO configuration and therefore validates none; unlike
// paymentstripe there is no setting here to say "stop startup if it is missing"
// about (see the package documentation).
//
// # NOTHING is resolved from the container here
//
// Setup runs BEFORE the modules come up: "product.interop" is not in the
// container at this moment, and trying to resolve it would bring startup down
// with an error where nothing is actually missing. The registration only
// DECLARES the module and the subscriptions; catalog access is resolved on
// first use (see [catalog.resolve]).
//
// # Why the subscription goes THROUGH the Host
//
// Had the bus been taken directly and Subscribe called on it, the subscription
// would be set up without the product module having registered, and the first
// event could arrive while the index table had not been migrated yet.
// [coreplugin.Host.Subscribe] QUEUES the registration and applies it after the
// modules have come up.
func (p *Plugin) Setup(_ context.Context, h *coreplugin.Host) error {
	p.mod = newSearchModule(h.Container(), h.Logger())

	// Extension point 1: the plugin brings its OWN module.
	h.AddModule(p.mod)

	// Extension point 2: the catalog events. A write and an update go to the SAME
	// handler: in both cases the right behavior is "read the record, write it to
	// the index", and two separate handlers would be a second copy of one body.
	h.Subscribe(eventProductCreated, p.mod.productWritten)
	h.Subscribe(eventProductUpdated, p.mod.productWritten)
	h.Subscribe(eventProductDeleted, p.mod.productDeleted)

	h.Logger().Info("the search plugin was set up",
		"module", ModuleName,
		"search_endpoint", SearchPath,
		"reindex_endpoint", ReindexPath)

	return nil
}
