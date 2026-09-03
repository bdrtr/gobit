package observability

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// sampleTraceID is a fixed trace ID that makes the sampling decision
// deterministic.
//
// Not generating it randomly is deliberate: because TraceIDRatioBased derives
// the decision from the trace ID itself, a random id would make the test fail
// intermittently.
var sampleTraceID = trace.TraceID{
	0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
	0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
}

// sampleSpanID is the id needed for the parent span context to count as valid.
var sampleSpanID = trace.SpanID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

// parentContext builds a parent span context with the given flags.
func parentContext(remote, sampled bool) context.Context {
	var flags trace.TraceFlags
	if sampled {
		flags = trace.FlagsSampled
	}

	return trace.ContextWithSpanContext(context.Background(),
		trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    sampleTraceID,
			SpanID:     sampleSpanID,
			TraceFlags: flags,
			Remote:     remote,
		}))
}

// TestRemoteParentCannotOverrideTheSampleRatio proves the traceparent sent by
// the client cannot switch the sampling ratio off.
//
// Regression: ParentBased's DEFAULT is AlwaysSample when the remote parent is
// marked "sampled". On a public endpoint the traceparent is entirely under the
// client's control; an attacker marking every request sampled could make the
// OTEL_TRACES_SAMPLER_ARG=0.01 setting meaningless and inflate the telemetry
// cost.
func TestRemoteParentCannotOverrideTheSampleRatio(t *testing.T) {
	tests := map[string]struct {
		ratio  float64
		remote bool
		parent bool
		want   sdktrace.SamplingDecision
	}{
		"remote parent sampled but the ratio is zero": {
			ratio: 0, remote: true, parent: true, want: sdktrace.Drop,
		},
		"remote parent sampled and the ratio is one": {
			ratio: 1, remote: true, parent: true, want: sdktrace.RecordAndSample,
		},
		"remote parent not sampled": {
			ratio: 1, remote: true, parent: false, want: sdktrace.Drop,
		},
		// A child span in the same process follows the parent span; otherwise a
		// single request's span tree would be riddled with holes.
		"local parent sampled and the ratio is zero": {
			ratio: 0, remote: false, parent: true, want: sdktrace.RecordAndSample,
		},
		"local parent not sampled": {
			ratio: 1, remote: false, parent: false, want: sdktrace.Drop,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			result := sampler(tt.ratio).ShouldSample(sdktrace.SamplingParameters{
				ParentContext: parentContext(tt.remote, tt.parent),
				TraceID:       sampleTraceID,
				Name:          "GET /public",
			})

			assert.Equal(t, tt.want, result.Decision)
		})
	}
}

// TestParentlessRequestFollowsTheRatio proves spans with no parent context
// (root spans) are sampled at the configured ratio.
func TestParentlessRequestFollowsTheRatio(t *testing.T) {
	params := sdktrace.SamplingParameters{
		ParentContext: context.Background(),
		TraceID:       sampleTraceID,
		Name:          "GET /public",
	}

	assert.Equal(t, sdktrace.Drop, sampler(0).ShouldSample(params).Decision)
	assert.Equal(t, sdktrace.RecordAndSample, sampler(1).ShouldSample(params).Decision)
}

// fakeProvider is an exporter stand-in recording the shutdown call and the
// budget it was given.
type fakeProvider struct {
	// block true makes Shutdown block until the context ends; that is how an
	// unreachable collector eating the budget is imitated.
	block bool
	// err is the error Shutdown will return.
	err error

	// remaining is the time LEFT on the context when Shutdown was entered.
	remaining atomic.Int64
	// called reports whether Shutdown was called.
	called atomic.Bool
}

// Shutdown satisfies the closable interface.
func (s *fakeProvider) Shutdown(ctx context.Context) error {
	s.called.Store(true)

	if deadline, ok := ctx.Deadline(); ok {
		s.remaining.Store(int64(time.Until(deadline)))
	}

	if s.block {
		<-ctx.Done()
	}

	return s.err
}

// TestShutdownBudgetIsNotSharedBetweenProviders proves a slow provider does not
// consume the other one's budget.
//
// Regression: a single context was shared between the tracer and the meter.
// While the collector was unreachable the tracer ate the whole budget and the
// meter was called with an expired context; the pending metrics were dropped in
// silence.
func TestShutdownBudgetIsNotSharedBetweenProviders(t *testing.T) {
	const budget = 100 * time.Millisecond

	// Both block: with a sequential shutdown the second would only start AFTER
	// the first one's budget ran out.
	traces := &fakeProvider{block: true}
	metrics := &fakeProvider{block: true}

	start := time.Now()
	err := runShutdown(context.Background(), budget, traces, metrics)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.True(t, traces.called.Load(), "the trace provider must be closed")
	assert.True(t, metrics.called.Load(), "the metric provider must be closed")

	// Both must be called with the FULL budget; on a shared context the
	// second one's remaining time would be close to zero.
	assert.Greater(t, metrics.remaining.Load(), int64(budget/2),
		"the metric provider must get its own budget")
	assert.Greater(t, traces.remaining.Load(), int64(budget/2),
		"the trace provider must get its own budget")

	// A parallel shutdown: the total wait must stay bounded by a single budget.
	assert.Less(t, elapsed, 2*budget, "the providers must be closed in parallel")
}

// TestShutdownErrorsAreJoined proves one provider's error does NOT stop the
// other from being closed, and that the errors are joined.
func TestShutdownErrorsAreJoined(t *testing.T) {
	traceErr := errors.New("the traces could not be sent")
	metricErr := errors.New("the metrics could not be sent")

	traces := &fakeProvider{err: traceErr}
	metrics := &fakeProvider{err: metricErr}

	err := runShutdown(context.Background(), time.Second, traces, metrics)

	require.Error(t, err)
	assert.ErrorIs(t, err, traceErr)
	assert.ErrorIs(t, err, metricErr)
}

// TestEndpointHasSchemeTellsBothFormsApart proves both spellings of the
// collector address are recognized.
//
// # What this test does NOT prove
//
// It does not prove the right option actually sends spans — gRPC connects
// lazily, so the setup returns "successful" with the wrong option too. That is
// exactly why the silent loss was observed against a real collector: given a
// schemed address, the application logged "telemetry is set up" and Jaeger saw
// no span at all.
//
// What is pinned here is the DECISION: a schemed address goes to the URL
// option, a schemeless one to the host:port option. This test breaks when the
// decision breaks; the wiring itself is verified by hand with
// `make up-tracing`.
func TestEndpointHasSchemeTellsBothFormsApart(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		endpoint string
		want     bool
	}{
		"go sdk form":         {endpoint: "localhost:4317", want: false},
		"ipv4 host:port":      {endpoint: "10.0.0.5:4317", want: false},
		"spec form http":      {endpoint: "http://localhost:4317", want: true},
		"spec form https":     {endpoint: "https://collector.example.com:4317", want: true},
		"url carrying a path": {endpoint: "http://collector:4317/v1/traces", want: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := endpointHasScheme(tt.endpoint); got != tt.want {
				t.Errorf("endpointHasScheme(%q) = %v, want %v", tt.endpoint, got, tt.want)
			}
		})
	}
}

// TestEndpointOptionsAreNeverNil proves an option is produced for both forms; a
// nil option would panic while the exporter is being built.
func TestEndpointOptionsAreNeverNil(t *testing.T) {
	t.Parallel()

	for _, endpoint := range []string{"localhost:4317", "http://localhost:4317"} {
		if traceEndpoint(endpoint) == nil {
			t.Errorf("traceEndpoint(%q) returned nil", endpoint)
		}

		if metricEndpoint(endpoint) == nil {
			t.Errorf("metricEndpoint(%q) returned nil", endpoint)
		}
	}
}
