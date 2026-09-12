// Package analytics is the plugin that answers where a shop's carts go.
//
// # What it is for
//
// A shop's first question about its storefront is a ratio: of the carts that
// were opened, how many became orders. Until ADR 0153 the numerator was on the
// bus and the denominator did not exist anywhere — the cart module published
// nothing at all — so the question could not be asked of a running installation.
// This plugin is the consumer that makes the answer readable:
// GET /admin/v1/analytics/funnel.
//
// # Why a plugin and not a module
//
// Because a funnel is not commerce. Nothing in the checkout, the catalog or the
// ledger reads it, and an installation that does not want it should not carry its
// table. The plugin boundary is exactly that promise: this package is removable
// with `rm -rf` plus one line in the catalog, and no module knows it exists.
//
// # Why it stores the EVENTS and not counters
//
// The bus delivers at least once and guarantees no ordering, so a counter
// incremented per event is wrong by construction: one redelivery and the ratio is
// a number that never happened. Instead every event is written as a ROW keyed on
// the event's own id with `ON CONFLICT DO NOTHING`, and the funnel is a GROUP BY
// over those rows. A redelivery is dropped by the primary key rather than
// defended against by arithmetic, and the id it is dropped by is the DERIVED one
// the publishing modules put on both of their delivery paths.
//
// That is also why an event with no id is REFUSED rather than counted: an empty
// primary key would make the first such event the last one ever recorded.
//
// # The three topics
//
// "cart.created" and "cart.completed" come from the cart module (ADR 0153) and
// "order.placed" from the order module. The completion and the placement are kept
// APART deliberately: the checkout saga places the order at its second step and
// completes the cart at its last, so an order that fails in between leaves a
// placement with no completion — and that gap is the most useful thing this
// endpoint can show.
//
// # It imports NO module
//
// The topic names and the payload keys are repeated BY HAND, as every plugin in
// this tree repeats them (a plugin may not import a module — ADR 0001, and
// internal/arch refuses it). The cost of drift is named where the constants are.
//
// # Use
//
//	PLUGINS=analytics
//
// It asks for no configuration. The table is created at startup by the
// migration, and it starts counting from that moment: the funnel says nothing
// about the carts a shop opened before the plugin was installed.
package analytics

import (
	"context"
	"embed"

	coreplugin "github.com/bdrtr/gobit/core/plugin"
)

// Name is the plugin's name in the registry; this is what goes in the PLUGINS list.
const Name = "analytics"

// ModuleName is the name of the module this plugin brings.
//
// It is the same word as the plugin's name and it may be: the name turns into an
// SQL identifier ("analytics_schema_migrations") and carries no hyphen to lose.
const ModuleName = "analytics"

// The topics this plugin listens to.
//
// This block is a CONTRACT between modules and its values are repeated BY HAND,
// because a plugin cannot import a module (ADR 0001). Every hand-repeated
// constant is open to silent drift and the cost here is concrete and quiet: a
// renamed topic makes this plugin receive nothing, the table stays empty, and the
// funnel answers zeroes that look like a shop with no visitors. No compiler
// catches it; what does is the end-to-end proof that a completed cart reaches
// this table.
const (
	// eventCartCreated is the cart module's EventCartCreated.
	eventCartCreated = "cart.created"
	// eventCartCompleted is the cart module's EventCartCompleted.
	eventCartCompleted = "cart.completed"
	// eventOrderPlaced is the order module's EventOrderPlaced.
	eventOrderPlaced = "order.placed"
	// eventFieldRegionID is the region key, spelled the same in all three
	// payloads (cart service.EventFieldRegionID, order service.EventFieldRegionID).
	eventFieldRegionID = "region_id"
	// eventFieldOccurredAt is the cart events' moment key.
	eventFieldOccurredAt = "occurred_at"
	// eventFieldPlacedAt is the order event's moment key; the order module named
	// its own field rather than sharing the cart's word.
	eventFieldPlacedAt = "placed_at"
)

// svcDB is the database pool's name in the container.
const svcDB = "core.db"

// Plugin is the funnel plugin.
type Plugin struct {
	// mod is the module the plugin brings; the subscribers are its methods too.
	// It is built in Setup and completed by [funnelModule.Register].
	mod *funnelModule
}

// That the plugin satisfies the core contract is fixed at compile time.
var _ coreplugin.Plugin = (*Plugin)(nil)

// New builds the plugin.
func New() *Plugin { return &Plugin{} }

// Name returns the plugin's name.
func (p *Plugin) Name() string { return Name }

// Setup adds the module to the registry and subscribes to the three topics.
//
// Nothing is resolved from the container here: Setup runs BEFORE the modules come
// up, so the pool is taken in [funnelModule.Register]. The subscription goes
// through the Host rather than through a bus taken by hand, because the Host
// QUEUES it and applies it after the modules have come up — otherwise the first
// event could arrive before this plugin's own table had been migrated.
func (p *Plugin) Setup(_ context.Context, h *coreplugin.Host) error {
	p.mod = newFunnelModule(h.Container(), h.Logger())

	h.AddModule(p.mod)

	h.Subscribe(eventCartCreated, p.mod.cartCreated)
	h.Subscribe(eventCartCompleted, p.mod.cartCompleted)
	h.Subscribe(eventOrderPlaced, p.mod.orderPlaced)

	// The screen an operator actually reads (ADR 0155). Without it the funnel is
	// a table only somebody with a terminal can see, and the plugin's own point
	// is that a shop can look at its conversion rate.
	h.RegisterAdminPage(coreplugin.AdminPage{
		Label:  PageLabel,
		Path:   PagePath,
		Script: funnelScript,
	})

	h.Logger().Info("the analytics plugin was set up",
		"module", ModuleName,
		"funnel_endpoint", FunnelPath,
		"panel_screen", PagePath)

	return nil
}

// The panel screen this plugin registers (ADR 0155).
const (
	// PageLabel is what the panel's navigation shows.
	PageLabel = "Funnel"
	// PagePath is the panel path the screen answers on.
	//
	// It sits under the panel's prefix — the panel refuses one that does not,
	// because a path outside it would be bound where the panel's session ring
	// never runs. The prefix is repeated BY HAND for the reason every other
	// constant in this file is: a plugin cannot import the panel, which is
	// internal, and the published form it registers through carries the
	// description of a screen rather than the panel itself.
	PagePath = "/admin/ui/analytics/funnel"
)

// assetFiles holds the screen's client and is EMBEDDED IN THE BINARY.
//
// The panel serves these BYTES from its own origin, which is what lets its
// content policy stay `script-src 'self'`: a URL would have forced the policy
// open for every installation, including the ones that installed no plugin.
//
//go:embed assets/funnel.js
var assetFiles embed.FS

// funnelScript is the screen's client, read once at startup.
//
// Reading at init rather than per request means a missing asset fails the BUILD
// (the embed directive) rather than the first page load, in front of an operator
// — the panel's own reasoning for its assets.
var funnelScript = mustReadAsset("assets/funnel.js")

// mustReadAsset reads an embedded asset or panics.
//
// Its failure means the embed directive and the file have drifted apart, which
// is a build-time mistake rather than a runtime condition.
func mustReadAsset(name string) []byte {
	body, err := assetFiles.ReadFile(name)
	if err != nil {
		panic("analytics: the embedded asset could not be read: " + err.Error())
	}

	return body
}
