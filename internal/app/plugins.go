// The plugin catalog: which plugins are COMPILED into this binary, which of
// them an installation selects, and the settings they are handed. It is its own
// file because it is the one place a plugin is named — adding or removing one
// touches nothing else — and because the migrate subcommands install the same
// set without ever building a server.

package app

import (
	"context"
	"log/slog"
	"os"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/module"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/plugins/errorotlp"
	"github.com/bdrtr/gobit/plugins/errorsentry"
	"github.com/bdrtr/gobit/plugins/files3"
	"github.com/bdrtr/gobit/plugins/notificationsmtp"
	"github.com/bdrtr/gobit/plugins/paymentpaytr"
	"github.com/bdrtr/gobit/plugins/paymentstripe"
	"github.com/bdrtr/gobit/plugins/searchpg"
	"github.com/bdrtr/gobit/plugins/webhookout"
	"github.com/bdrtr/gobit/plugins/webpush"
)

// codeUnknownPlugin reports an unrecognized name in the PLUGINS list.
const codeUnknownPlugin = "plugin_unknown"

// pluginCatalog holds the plugins COMPILED into this binary.
//
// The catalog lives here, at the composition root: adding a plugin changes
// neither the core nor any module, it only adds a line to this map (plan Phase
// 9 DoD). Which ones are installed is chosen by the PLUGINS environment
// variable.
//
// The catalog shows three different ways of extending. paymentstripe,
// notificationsmtp and files3 register a PROVIDER into a module's registry (the
// payment, notification and file modules' extension points); searchpg and
// webpush bring THEIR OWN MODULE —
// with its own table, its own migration and its own routes — and opens a new
// endpoint (GET /store/v1/sales-channels/{sales_channel_id}/search) without
// being named anywhere except the line below; errorsentry and errorotlp fill a slot the CORE owns, so they need no
// module to exist at all.
//
// webpush and paymentpaytr are the second and third of that middle kind, and
// both are there for the same reason: each looked like a plain provider and
// turned out to need durable state. A push destination is a set of devices the
// framework has to have STORED, and PayTR reports the outcome of a payment by
// posting BACK rather than answering when asked — so in both cases the provider
// slot alone cannot express the party (ADR 0018).
//
// The two provider plugins are not the same kind of thing, and the difference
// is worth naming: paymentstripe is a SKELETON that returns an error from every
// money-moving method, while notificationsmtp actually delivers. A provider
// slot with no working implementation is a promise the framework has not kept,
// and the notification slot was the one where that showed most — the only
// provider in the box writes a log line and sends nothing.
//
// The two reporters are the SAME slot filled twice, and that is deliberate:
// ADR 0014 said its shape could only be tested by a second implementation with
// a different model. Installing both is not supported — the core holds one
// reporter — and choosing between them is what the PLUGINS variable is for.
var pluginCatalog = map[string]func() coreplugin.Plugin{
	errorotlp.Name:        func() coreplugin.Plugin { return errorotlp.New() },
	errorsentry.Name:      func() coreplugin.Plugin { return errorsentry.New() },
	files3.Name:           func() coreplugin.Plugin { return files3.New() },
	notificationsmtp.Name: func() coreplugin.Plugin { return notificationsmtp.New() },
	paymentstripe.Name:    func() coreplugin.Plugin { return paymentstripe.New() },
	paymentpaytr.Name:     func() coreplugin.Plugin { return paymentpaytr.New() },
	searchpg.Name:         func() coreplugin.Plugin { return searchpg.New() },
	webhookout.Name:       func() coreplugin.Plugin { return webhookout.New() },
	webpush.Name:          func() coreplugin.Plugin { return webpush.New() },
}

// selectPlugins builds the names in the PLUGINS list from the catalog.
//
// An unknown name is an ERROR: skipping it silently would let a misspelled
// plugin be believed "installed", with the absence only noticed on first use.
// The local variable is deliberately NOT called "registry": the module
// registration invariant in internal/arch recognizes the receiver BY NAME, and
// a plugin registry sharing the name of the module registry in main.go makes
// this line look like a module registration to the check.
//
// log is a PARAMETER rather than the package default because the second caller
// is the migrate surface, which prints a table to stdout: it was measured that
// a nil logger here makes the registry fall back to slog's default handler and
// write "the plugins are installed" to stderr in the middle of an operator's
// report.
func selectPlugins(names []string, log *slog.Logger) (*coreplugin.Registry, error) {
	plugins := coreplugin.NewRegistry(log)

	for _, name := range names {
		name = strings.TrimSpace(name)

		constructor, ok := pluginCatalog[name]
		if !ok {
			return nil, errors.Invalid(codeUnknownPlugin,
				"unknown plugin %q (recognized: %s)", name, strings.Join(pluginNames(), ", "))
		}

		plugins.Add(constructor())
	}

	return plugins, nil
}

// pluginNames returns the catalog's names sorted; it is for the error message.
func pluginNames() []string {
	names := make([]string, 0, len(pluginCatalog))
	for name := range pluginCatalog {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

// pluginSettings builds the settings map handed to the plugins from the
// environment.
//
// Plugins ask for their settings by environment variable NAME (e.g.
// STRIPE_API_KEY); the core config CANNOT know those names because a plugin is
// added at compile time. Building the map here gives the same result without
// letting a plugin reach for the os package, and it makes passing fake settings
// possible in a test.
func pluginSettings() map[string]string {
	environ := os.Environ()
	settings := make(map[string]string, len(environ))

	for _, line := range environ {
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		settings[name] = value
	}

	return settings
}

// installPlugins selects the plugins named in the configuration and runs their
// Setup phase against a host bound to registry.
//
// It is three lines pulled into a function for the same reason
// [registerModules] is: a plugin may bring a MODULE OF ITS OWN, with its own
// table and its own migration — searchpg does — and that module exists nowhere
// but in the registry this call fills. The migrate subcommands have to see it
// too, or `migrate status` would silently omit an owner whose schema is in the
// database and `migrate down searchpg` would answer "unknown owner" about a
// module the server migrates on every boot.
//
// Setup neither connects nor migrates: the host QUEUES the provider and
// subscriber registrations for [github.com/bdrtr/gobit/core/plugin.Registry.Start],
// which the migrate path never calls. That is why bus may be nil there.
func installPlugins(
	ctx context.Context,
	cfg config.Config,
	c *container.Container,
	registry *module.Registry,
	bus eventbus.EventBus,
	log *slog.Logger,
	extra []coreplugin.Plugin,
) (*coreplugin.Registry, *coreplugin.Host, error) {
	plugins, err := selectPlugins(cfg.Plugins, log)
	if err != nil {
		return nil, nil, err
	}
	// A plugin handed in by the embedding program is not named in the
	// configuration and does not have to be: the program that compiled it in
	// has already made the selection that PLUGINS names for the ones in the box.
	for _, p := range extra {
		plugins.Add(p)
	}

	host := coreplugin.NewHost(c, registry, bus, log, pluginSettings())
	if err := plugins.Install(ctx, host); err != nil {
		return nil, nil, err
	}

	return plugins, host, nil
}
