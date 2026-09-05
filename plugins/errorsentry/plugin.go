// Package errorsentry reports gobit's failures to Sentry.
//
// # What it shows
//
// It is the third shape a plugin can take. paymentstripe adds a PROVIDER to a
// module's registry and searchpg brings a MODULE of its own; this one fills a
// slot the CORE owns. It imports no commerce module, takes its contract from
// [coreprovider] and its registration point from [coreplugin.Host], and the
// core's code does not mention Sentry anywhere.
//
// # What it does not decide
//
// It does not decide what may be sent. The event it receives has already been
// through the core's allow list (core/errorreport), which is
// deliberate: a policy enforced by the plugin would be a policy each plugin
// could get wrong, and this one runs in a process holding customer data and
// talks to a service in somebody else's datacenter.
//
// # Use
//
//	PLUGINS=error-sentry
//	SENTRY_DSN=https://<key>@<host>/<project id>
//
// and optionally SENTRY_ENVIRONMENT and SENTRY_RELEASE. Without the DSN the
// plugin refuses to start rather than running as a no-op: an installation that
// believes it has error reporting and does not is worse off than one that knows
// it has none.
package errorsentry

import (
	"context"

	coreplugin "github.com/bdrtr/gobit/core/plugin"
)

// Name is the plugin's name in the catalog.
const Name = "error-sentry"

// ProviderID identifies the reporter in the startup log.
const ProviderID = "sentry"

// The setting names. These are the NAMES of environment variables, not values.
const (
	// dsnSetting holds the Sentry DSN.
	dsnSetting = "SENTRY_DSN"
	// environmentSetting names the deployment ("production", "staging"). Left
	// unset, APP_ENV is used: an operator who already told gobit which
	// environment this is should not have to say it twice.
	environmentSetting = "SENTRY_ENVIRONMENT"
	// appEnvSetting is the fallback for environmentSetting.
	appEnvSetting = "APP_ENV"
	// releaseSetting names the build. Sentry's regression tracking and its
	// suspect-commit feature both key off it.
	releaseSetting = "SENTRY_RELEASE"
)

// Plugin is the Sentry error-reporting plugin.
type Plugin struct {
	// reporter is kept so the tests can reach it; the core holds its own
	// reference through the container.
	reporter *Reporter
}

// New builds the plugin.
func New() *Plugin { return &Plugin{} }

// Name returns the plugin's name.
func (p *Plugin) Name() string { return Name }

// Setup validates the DSN and registers the reporter.
//
// The registration is NOT queued to Start, unlike a payment provider's: there is
// no module to wait for, and the modules come up between Install and Start. A
// reporter that waited would watch every migration failure and every provider
// check go by unreported, in the one phase where a failure means the process is
// about to exit.
func (p *Plugin) Setup(_ context.Context, h *coreplugin.Host) error {
	raw, _ := h.Setting(dsnSetting)

	parsed, err := parseDSN(raw)
	if err != nil {
		return err
	}

	environment, ok := h.Setting(environmentSetting)
	if !ok {
		environment, _ = h.Setting(appEnvSetting)
	}
	release, _ := h.Setting(releaseSetting)

	log := h.Logger()
	// contextcheck sees Setup's context reaching a sender that builds its own.
	// That is the design: the sender outlives every context there is here, and
	// the reason is written on [Reporter.Report].
	//nolint:contextcheck // the sender has no live context to inherit
	p.reporter = newReporter(parsed, environment, release, func(err error) {
		// The send failure is logged at WARN, and the level is load-bearing.
		// At ERROR it would come straight back through the reporting handler,
		// be reported as a failure, fail to send, and log again — a collector
		// outage turning into a loop that only ends when the process does.
		log.Warn("an error report could not be delivered", "error", err)
	})

	h.RegisterErrorReporter(p.reporter)
	log.Info("sentry error reporting is configured",
		"project", parsed.projectID, "environment", environment, "release", release)

	return nil
}
