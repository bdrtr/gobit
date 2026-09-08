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
// # The fault it catches
//
// THE FORMAT OF THE ADDRESS. The OpenTelemetry specification defines
// OTEL_EXPORTER_OTLP_ENDPOINT as a URL ("http://collector:4317"); the Go SDK's
// WithEndpoint option, on the other hand, expects a SCHEMELESS "host:port".
// When the two are mixed up no error surfaces at all: gRPC connects lazily,
// the application logs "telemetry is set up", and not a single span goes out.
// The silent loss is only noticed while a fault is being investigated — that
// is, at the worst possible moment.
//
// # The second fault this scenario used to catch, and why it cannot come back
//
// It also gave OTEL_METRIC_EXPORT_INTERVAL and METRIC_EXPORT_INTERVAL each the
// value its own specification asks for, because the two names differed by a
// prefix and meant different things — a millisecond integer against a Go
// duration. There is no interval left to clash over: ADR 0046 retired the
// periodic metric reader along with the setting that configured it, and
// metrics now leave by a scrape endpoint that has no interval at all. The
// paragraph is kept rather than deleted so that a reader who finds
// METRIC_EXPORT_INTERVAL in the history knows what happened to it.
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

			assert.False(t, s.happened(),
				"the application must NOT go DOWN while the tracing collector is unreachable (ADR 0007)\n%s", s.logBuf())
		})
	}
}
