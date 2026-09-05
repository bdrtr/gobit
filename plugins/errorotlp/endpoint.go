package errorotlp

import (
	"net/url"
	"strings"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// The error codes.
const (
	codeEndpointMissing = "otlp_endpoint_missing"
	codeEndpointInvalid = "otlp_endpoint_invalid"
	codeHeadersInvalid  = "otlp_headers_invalid"
)

// parseEndpoint validates the collector's logs endpoint.
//
// It refuses anything it does not fully understand, for the reason the Sentry
// plugin's DSN parser gives: a monitoring integration's failure mode is
// SILENCE. A nearly-right endpoint produces a process that starts, logs that
// reporting is on, and posts every failure into a 404 for as long as it runs.
// The mistake has to be caught in the one place that can still be loud.
//
// The path is NOT appended here. OTLP's own convention is /v1/logs, but
// collectors sit behind gateways that rewrite paths, and a client that helpfully
// completed the URL would be guessing about somebody else's routing. The
// operator gives the full URL and what they wrote is what is used.
func parseEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", coreerrors.Invalid(codeEndpointMissing,
			"%s is not set; the errorotlp plugin has nowhere to report to", endpointSetting)
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", coreerrors.Wrap(err, coreerrors.KindInvalid, codeEndpointInvalid,
			"%s could not be parsed as a URL", endpointSetting)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", coreerrors.Invalid(codeEndpointInvalid,
			"%s has to be an http or https URL, got %q", endpointSetting, parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", coreerrors.Invalid(codeEndpointInvalid,
			"%s has no host", endpointSetting)
	}

	return parsed.String(), nil
}

// parseHeaders turns "key=value,key=value" into request headers.
//
// The format is the one OTEL_EXPORTER_OTLP_HEADERS already uses, so an operator
// who configured the collector for another exporter can paste the same string.
//
// An entry that is not a pair is REFUSED rather than skipped: a mistyped API key
// header silently dropped would leave the process posting unauthenticated
// reports into a 401, which is the same silence a missing endpoint produces.
func parseHeaders(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	headers := map[string]string{}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		name, value, ok := strings.Cut(entry, "=")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || name == "" {
			return nil, coreerrors.Invalid(codeHeadersInvalid,
				"%s expects key=value pairs separated by commas; %q is not one", headersSetting, entry)
		}

		headers[name] = value
	}

	return headers, nil
}
