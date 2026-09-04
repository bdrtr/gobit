//go:build smoke

package smoke

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBothSpellingsOfTheTracingEndpointAreAccepted is scenario D: both
// spellings of the OTLP address are accepted, and a tracing fault does NOT
// bring the application DOWN (ADR 0007).
//
// # The two faults it catches
//
// The first is THE FORMAT OF THE ADDRESS. The OpenTelemetry specification
// defines OTEL_EXPORTER_OTLP_ENDPOINT as a URL ("http://collector:4317");
// the Go SDK's WithEndpoint option, on the other hand, expects a SCHEMELESS
// "host:port". When the two are mixed up no error surfaces at all: gRPC
// connects lazily, the application logs "telemetry is set up", and not a
// single span goes out. The silent loss is only noticed while a fault is
// being investigated — that is, at the worst possible moment.
//
// The second is THE NAME OF THE METRIC INTERVAL. In the application the
// interval is read under the name METRIC_EXPORT_INTERVAL and as a Go
// duration; the specification, meanwhile, has RESERVED the name
// OTEL_METRIC_EXPORT_INTERVAL and defines its value as an INTEGER NUMBER OF
// MILLISECONDS. Once the name is borrowed, the clash cuts in both directions
// at once: the value that follows the specification (60000) cannot be parsed
// as a Go duration and the application DOES NOT BOOT AT ALL; the value that
// follows the application (60s) makes the OTel SDK's own reader log a "parse
// duration" error on every boot.
//
// The scenario gives each variable the value that follows ITS OWN
// specification. If the name clash comes back, one of the two is bound to
// blow up: either the application cannot read 60000 as a duration and stops
// at startup, or the SDK takes 60s for a millisecond count and logs an error.
// Testing with a single variable would have seen only one direction of the
// fault.
//
// # Why no collector is set up, and the limit of this test
//
// What is under test is not the DELIVERY of spans but that the application
// boots and KEEPS RUNNING. Tracing exists for the product's visibility, not
// for its correctness, and an outage of the collector must not close the
// store (ADR 0007); standing a collector up would remove the very condition
// meant to be proven — that it works even when there is no collector.
//
// The limit must be stated plainly: without a collector this test cannot show
// that spans REALLY go out, only that both spellings get past startup. That
// the address is translated into the right SDK option (WithEndpoint /
// WithEndpointURL) is pinned by a unit test (see
// TestEndpointHasSchemeTellsBothFormsApart in the observability package). The
// division of labor is deliberate: the correctness of the option is tested
// there cheaply and precisely, while the question "does the application
// really boot with this address" can only be answered here.
func TestBothSpellingsOfTheTracingEndpointAreAccepted(t *testing.T) {
	endpoints := map[string]string{
		"schemeless host:port (Go SDK spelling)": "localhost:4317",
		"schemed URL (spec spelling)":            "http://localhost:4317",
	}

	for name, endpoint := range endpoints {
		t.Run(name, func(t *testing.T) {
			cfg := baseSettings(scenarioDatabase(t), freePort(t))
			cfg["OTEL_EXPORTER_OTLP_ENDPOINT"] = endpoint
			cfg["OTEL_EXPORTER_OTLP_INSECURE"] = "true"
			// Each variable is given the value that follows ITS OWN
			// specification; the rationale is in the test's godoc.
			cfg["METRIC_EXPORT_INTERVAL"] = "60s"
			cfg["OTEL_METRIC_EXPORT_INTERVAL"] = "60000"

			s := startServer(t, cfg)
			s.waitForReady(startupTimeout)

			code, body := s.request(http.MethodGet, "/ready", "")
			assert.Equal(t, http.StatusOK, code,
				"must be ready even while the collector is unreachable; body: %s", body)

			assert.True(t, s.logContains("telemetry is set up"),
				"successful setup must be logged; had the address been rejected we would see \"could not be set up\"\n%s",
				s.logBuf())
			assert.False(t, s.logContains("observability could not be set up"),
				"this spelling of the address must be accepted\n%s", s.logBuf())
			assert.True(t, s.logContains(endpoint),
				"the setup log must say which address was used\n%s", s.logBuf())

			// "parse duration" is the OTel SDK's own error text and it lands on
			// stderr (see sdk/metric env.go). It is the only visible trace of
			// the name clash: metrics are still sent, only the interval
			// silently falls back to the default.
			assert.NotContains(t, s.stderr.String(), "parse duration",
				"the OTel SDK could not parse the metric interval: the name clash may be back")

			assert.False(t, s.happened(),
				"the application must NOT go DOWN while the tracing collector is unreachable (ADR 0007)\n%s", s.logBuf())
		})
	}
}
