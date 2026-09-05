// Package errorotlp reports gobit's failures to an OpenTelemetry collector.
//
// # Why a SECOND reporter exists
//
// [ADR 0014] set the shape of error reporting and named the one thing that
// would test it: "reopen when a second reporter is written. One implementation
// cannot show whether ErrorEvent is the right shape or merely the shape Sentry
// wanted." This is that second implementation, and it was chosen for the model
// furthest from the first one.
//
// Sentry has issues: a report carries a fingerprint and the collector groups by
// it. The OpenTelemetry log model has no issue, no grouping key and no
// deduplication — a log record is a timestamp, a severity, a body and
// attributes. Everything Sentry gets a dedicated field for has to survive as an
// attribute here or not at all.
//
// What that showed is written in the ADR, not repeated here.
//
// # Why not the OTel SDK
//
// The same reason errorsentry does not use Sentry's: the OTLP/HTTP JSON body is
// a nested object with four levels and a documented encoding, and writing it
// costs less than another dependency in go.mod does. The parts of the SDK worth
// the dependency — batching, retry, a logs pipeline — are the parts ADR 0014
// refuses anyway (no retries, one sender, drop when full).
//
// # Use
//
//	PLUGINS=error-otlp
//	OTLP_LOGS_ENDPOINT=http://collector:4318/v1/logs
//
// and optionally OTLP_LOGS_HEADERS ("key=value,key=value") for a collector
// behind an API key. The service name and environment are read from the ones
// gobit already has (SERVICE_NAME, APP_ENV) so an operator does not say them
// twice.
//
// Without the endpoint the plugin refuses to start rather than running as a
// no-op: an installation that believes it has error reporting and does not is
// worse off than one that knows it has none.
//
// [ADR 0014]: https://github.com/bdrtr/gobit/blob/main/docs/adr/0014-error-reporting.md
package errorotlp

import (
	"context"

	coreplugin "github.com/bdrtr/gobit/core/plugin"
)

// Name is the plugin's name in the catalog.
const Name = "error-otlp"

// ProviderID identifies the reporter in the startup log.
const ProviderID = "otlp"

// The setting names. These are the NAMES of environment variables, not values.
const (
	// endpointSetting holds the full URL of the collector's logs endpoint,
	// including the /v1/logs path. It is given in full rather than assembled
	// from a host: collectors are put behind gateways, paths and ports that no
	// convention predicts, and a client that guessed the path would POST into a
	// 404 for as long as the process ran.
	endpointSetting = "OTLP_LOGS_ENDPOINT"
	// headersSetting carries extra request headers as "key=value,key=value".
	// Hosted collectors authenticate this way and there is no other place in
	// gobit's configuration for a header.
	headersSetting = "OTLP_LOGS_HEADERS"
	// serviceNameSetting names the service in the resource. gobit's own
	// SERVICE_NAME is used so the reports land next to the traces and metrics
	// the same process already exports.
	serviceNameSetting = "SERVICE_NAME"
	// appEnvSetting fills deployment.environment.
	appEnvSetting = "APP_ENV"
	// releaseSetting fills service.version when the deployment sets it.
	releaseSetting = "OTLP_SERVICE_VERSION"
)

// defaultServiceName is used when SERVICE_NAME is unset; it matches the default
// gobit's own configuration carries, so an unconfigured installation does not
// appear in the collector under two different names.
const defaultServiceName = "gobit"

// Plugin is the OTLP error-reporting plugin.
type Plugin struct {
	// reporter is kept so the tests can reach it; the core holds its own
	// reference through the container.
	reporter *Reporter
}

// New builds the plugin.
func New() *Plugin { return &Plugin{} }

// Name returns the plugin's name.
func (p *Plugin) Name() string { return Name }

// Setup validates the endpoint and registers the reporter.
//
// The registration is NOT queued to Start, for the reason ADR 0014 gives: the
// modules come up between Install and Start, and a reporter that waited would
// watch every migration failure go by unreported in the one phase where a
// failure means the process is about to exit.
func (p *Plugin) Setup(_ context.Context, h *coreplugin.Host) error {
	raw, _ := h.Setting(endpointSetting)

	target, err := parseEndpoint(raw)
	if err != nil {
		return err
	}

	headers, err := parseHeaders(mustSetting(h, headersSetting))
	if err != nil {
		return err
	}

	service, ok := h.Setting(serviceNameSetting)
	if !ok || service == "" {
		service = defaultServiceName
	}
	environment, _ := h.Setting(appEnvSetting)
	release, _ := h.Setting(releaseSetting)

	log := h.Logger()
	// contextcheck sees Setup's context reaching a sender that builds its own.
	// That is the design: the sender outlives every context there is here, and
	// the reason is written on [Reporter.Report].
	//nolint:contextcheck // the sender has no live context to inherit
	p.reporter = newReporter(target, headers, resource{
		service:     service,
		environment: environment,
		release:     release,
	}, func(err error) {
		// The send failure is logged at WARN, and the level is load-bearing
		// (ADR 0014, decision 9). At ERROR it would come straight back through
		// the reporting handler, be reported as a failure, fail to send, and
		// log again — a collector outage turning into a loop that only ends
		// when the process does.
		log.Warn("an error report could not be delivered", "error", err)
	})

	h.RegisterErrorReporter(p.reporter)
	log.Info("otlp error reporting is configured",
		"endpoint", target, "service", service, "environment", environment)

	return nil
}

// mustSetting reads a setting and treats "not set" as empty.
func mustSetting(h *coreplugin.Host, name string) string {
	value, _ := h.Setting(name)

	return value
}
