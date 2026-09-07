// Provider verification: confirming at startup that the notification and file
// providers an installation selected are really registered, and warning when
// the local file root will not survive a redeploy. It is its own file because
// both checks exist for the same reason and share one shape — config validates
// the FORM of a name, and only the composition root, after the plugins have
// registered, can validate its MEANING.

package app

import (
	"log/slog"
	"strings"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/modules/file"
	fileservice "github.com/bdrtr/gobit/internal/modules/file/service"
	"github.com/bdrtr/gobit/internal/modules/notification"
	notificationservice "github.com/bdrtr/gobit/internal/modules/notification/service"
)

// codeUnknownNotificationProvider reports that NOTIFICATION_PROVIDER does not
// name a registered provider.
const codeUnknownNotificationProvider = "notification_provider_unknown"

// codeUnknownFileProvider reports that FILE_PROVIDER does not name a
// registered provider.
const codeUnknownFileProvider = "file_provider_unknown"

// notificationProviders is the NARROW surface the setup wants from the
// notification provider registry.
//
// A two-method interface is used instead of the concrete registry type: the
// setup depends not on the notification module's service surface but only on
// the two calls listed here, and the check can be exercised with a fake
// registry without bringing the whole module up.
type notificationProviders interface {
	Get(id string) (coreprovider.NotificationProvider, error)
	IDs() []string
}

// That the real registry satisfies this narrow surface is pinned at COMPILE
// time.
//
// Conformance is checked at runtime by container.Resolve's type assertion, and
// a drift there means startup stopping with "the registry does not satisfy the
// expected surface" — that is, at the latest possible moment. This line asks
// the compiler the same question; importing the notification module is already
// allowed for the composition root (see [adminUsers]).
var _ notificationProviders = (*notificationservice.ProviderRegistry)(nil)

// verifyNotificationProvider confirms that the selected provider is REALLY
// registered.
//
// # Why here and not in config
//
// config cannot know the valid names: providers come from plugins and the
// plugin list is decided at compile time. The same split applies to PLUGINS
// (see [selectPlugins]) — config validates the FORM, the composition root
// validates the MEANING.
//
// # Why startup STOPS
//
// The alternative — ignoring the unknown name and falling back to the default
// "log" provider — is exactly what must be avoided: the installation opens, no
// error appears, and order confirmations are only written to the log. The
// failure is noticed while customers are waiting for confirmations, usually
// days later. Stopping at startup moves the failure to the moment the
// configuration is still in hand.
//
// # Why this step comes AFTER Start
//
// The plugins' provider registrations are applied during
// [coreplugin.Registry.Start]; an earlier check would reject a valid name
// coming from a plugin as "unknown".
func verifyNotificationProvider(c *container.Container, id string) error {
	registry, err := container.Resolve[notificationProviders](c, notification.ProvidersName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeUnknownNotificationProvider,
			"the notification provider registry %q could not be resolved", notification.ProvidersName)
	}

	if _, err := registry.Get(id); err != nil {
		return errors.Wrap(err, errors.KindInvalid, codeUnknownNotificationProvider,
			"NOTIFICATION_PROVIDER=%q is not a registered notification provider (registered: %s); "+
				"if a plugin brings it, has it been added to the PLUGINS list?",
			id, strings.Join(registry.IDs(), ", "))
	}

	return nil
}

// fileProviders is the NARROW surface the setup wants from the file provider
// registry.
//
// The rationale is exactly the same as [notificationProviders]'s and is not
// repeated. Merging the two interfaces into a single generic type was not
// attempted because the gain is only a two-line declaration; in exchange the
// container.Resolve call would be written with a generic interface type and a
// type mismatch error would become harder to read — while the one job of this
// code is to produce a diagnosable error.
type fileProviders interface {
	Get(id string) (coreprovider.FileProvider, error)
	IDs() []string
}

// That the real registry satisfies this narrow surface is pinned at COMPILE
// time.
var _ fileProviders = (*fileservice.ProviderRegistry)(nil)

// verifyFileProvider confirms that the selected file provider is REALLY
// registered.
//
// Why the check is here rather than in config, and why it comes AFTER
// [coreplugin.Registry.Start], is written in the [verifyNotificationProvider]
// godoc.
//
// # Why startup STOPS
//
// The cost is DIFFERENT from the notification one and shows up earlier: had an
// unknown name been ignored and the default used, the installation would open
// with the "local" provider and an installation that believes it is writing to
// object storage would write files TO THE LOCAL DISK. When the container
// restarts those files are gone; the records and the product image URLs stay
// where they are. The error would surface in its most expensive form — as data
// loss.
//
// The reverse direction is just as bad: in an installation where "local" was
// NOT REGISTERED because no root directory was given (see file.Options.Root),
// this check says at startup that the upload endpoint will reject every
// request.
func verifyFileProvider(c *container.Container, id string) error {
	registry, err := container.Resolve[fileProviders](c, file.ProvidersName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeUnknownFileProvider,
			"the file provider registry %q could not be resolved", file.ProvidersName)
	}

	if _, err := registry.Get(id); err != nil {
		return errors.Wrap(err, errors.KindInvalid, codeUnknownFileProvider,
			"FILE_PROVIDER=%q is not a registered file provider (registered: %s); "+
				"if a plugin brings it, has it been added to the PLUGINS list, and for the local "+
				"provider has FILE_ROOT been given?",
			id, strings.Join(registry.IDs(), ", "))
	}

	return nil
}

// warnAboutFileRoot logs a warning for a local root directory that is not
// durable.
//
// Why the warning does NOT stop startup, and what the "durable" criterion is,
// are in the [config.Config.LocalFileRootIsDurable] godoc. The only job here is
// to keep the risk visible: a production deployment that goes out with a
// relative or temporary root leaves no trace without this line, and the failure
// is only noticed after the first redeploy — when the images are gone.
//
// The message names BOTH reasons because the criterion covers both; the root
// itself is logged, so the operator sees at a glance which one they hit.
//
// In local development it is SILENT: there a relative root is the right thing,
// and printing a warning on every startup would drown a real warning in noise.
func warnAboutFileRoot(cfg config.Config, log *slog.Logger) {
	if !cfg.IsShared() || cfg.LocalFileRootIsDurable() {
		return
	}

	log.Warn("the file root directory is NOT DURABLE",
		"root", cfg.FileRoot,
		"warning", "a relative path is resolved against the process's WORKING DIRECTORY, and a "+
			"temporary root (/tmp, /var/tmp, /dev/shm or TMPDIR) is cleaned by the operating system; "+
			"either way the uploaded files are lost on redeploy (the URLs stay in the records)",
		"remedy", "give the absolute path of a mounted DURABLE volume as FILE_ROOT")
}
