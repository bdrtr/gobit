package errorsentry

import (
	"net/url"
	"strings"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
)

// dsn is a parsed Sentry DSN.
//
// The DSN's shape is
//
//	{scheme}://{public key}@{host}{path}/{project id}
//
// and the two things a client needs from it are an endpoint and a key. Both are
// derived here rather than at the call site so a malformed DSN fails ONCE, at
// startup, instead of on every report.
type dsn struct {
	// endpoint is where an envelope is POSTed.
	endpoint string
	// publicKey goes into the authentication header.
	publicKey string
	// projectID is kept for the startup log; an operator with two projects
	// needs to see which one this process talks to.
	projectID string
}

// The error codes.
const (
	codeDSNMissing = "sentry_dsn_missing"
	codeDSNInvalid = "sentry_dsn_invalid"
)

// parseDSN turns the configured DSN into an endpoint and a key.
//
// It refuses anything it does not fully understand. The alternative — guessing
// at a nearly-right DSN — produces a process that starts, logs that reporting
// is on, and posts every failure into a 404 for as long as it runs. A
// monitoring integration's failure mode is silence, so the mistake has to be
// caught in the one place that can still be loud.
func parseDSN(raw string) (dsn, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return dsn{}, coreerrors.Invalid(codeDSNMissing,
			"%s is not set; the errorsentry plugin has nowhere to report to", dsnSetting)
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return dsn{}, coreerrors.Invalid(codeDSNInvalid, "%s is not a URL", dsnSetting)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return dsn{}, coreerrors.Invalid(codeDSNInvalid,
			"%s must be http or https, not %q", dsnSetting, parsed.Scheme)
	}
	if parsed.Host == "" {
		return dsn{}, coreerrors.Invalid(codeDSNInvalid, "%s has no host", dsnSetting)
	}

	if parsed.User == nil || parsed.User.Username() == "" {
		return dsn{}, coreerrors.Invalid(codeDSNInvalid,
			"%s has no public key; the shape is scheme://key@host/project_id", dsnSetting)
	}
	// A DSN carries the PUBLIC key and nothing else. A password component means
	// somebody pasted a secret DSN, a shape Sentry retired precisely because
	// the value ends up in configuration that gets shared.
	if _, hasSecret := parsed.User.Password(); hasSecret {
		return dsn{}, coreerrors.Invalid(codeDSNInvalid,
			"%s carries a secret; use the public DSN (scheme://key@host/project_id)", dsnSetting)
	}

	prefix, projectID := splitProject(parsed.Path)
	if projectID == "" {
		return dsn{}, coreerrors.Invalid(codeDSNInvalid,
			"%s has no project id; the shape is scheme://key@host/project_id", dsnSetting)
	}

	return dsn{
		endpoint:  parsed.Scheme + "://" + parsed.Host + prefix + "/api/" + projectID + "/envelope/",
		publicKey: parsed.User.Username(),
		projectID: projectID,
	}, nil
}

// splitProject cuts the project id off the end of the DSN's path and returns
// what came before it.
//
// The leading part is kept because a self-hosted Sentry can live under a
// sub-path, and dropping it would post every envelope to the wrong place on
// exactly the installations that are hardest to debug.
func splitProject(path string) (prefix, projectID string) {
	trimmed := strings.TrimSuffix(path, "/")
	index := strings.LastIndex(trimmed, "/")
	if index < 0 {
		return "", ""
	}

	return trimmed[:index], trimmed[index+1:]
}
