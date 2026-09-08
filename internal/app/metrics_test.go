package app

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	metricnoop "go.opentelemetry.io/otel/metric/noop"

	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/core/observability"
)

// freeAddr returns a loopback address nothing is listening on.
//
// The port is taken and released rather than guessed: a fixed port turns a
// developer's already-running process into a failing test, and the CI machine
// runs several packages at once.
func freeAddr(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "a free port could not be taken")

	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	return addr
}

// metricsConfig is a configuration whose only interesting field is the metrics
// address.
func metricsConfig(addr string) config.Config {
	cfg := baseConfig()
	cfg.MetricsAddr = addr
	cfg.ShutdownTimeout = 5 * time.Second
	cfg.ReadHeaderTimeout = time.Second
	cfg.ReadTimeout = 2 * time.Second
	cfg.WriteTimeout = 2 * time.Second
	cfg.IdleTimeout = 5 * time.Second

	return cfg
}

// restoreTheGlobalMeterProvider puts OTel's no-op meter provider back after a
// test that made observability.Setup install a real one.
//
// otel.SetMeterProvider writes a PROCESS-WIDE global, and shutting the provider
// down does not uninstall it: without this, every test running later in this
// binary would take its meters from a provider that has already been closed.
// The observability package keeps the same rule for its own tests with its
// restoreGlobals helper; this is the app side of it, and it exists because the
// leak crosses a package boundary that no single test can see.
//
// Only the METER is restored. Setup installs a tracer provider and a propagator
// only for an OTLP endpoint, and these tests give it none.
func restoreTheGlobalMeterProvider(t *testing.T) {
	t.Helper()

	t.Cleanup(func() { otel.SetMeterProvider(metricnoop.NewMeterProvider()) })
}

// waitedWithin reports whether the wait function returned inside the budget.
//
// It is a helper rather than a bare call because the failing shape of these
// tests is a wait that never returns: startMetrics blocks its caller until the
// listener closes, so a gate that wrongly opened one would HANG the test run
// instead of failing it.
func waitedWithin(wait func(), budget time.Duration) bool {
	done := make(chan struct{})

	go func() {
		defer close(done)
		wait()
	}()

	select {
	case <-done:
		return true
	case <-time.After(budget):
		return false
	}
}

// waitUntilBound blocks until something accepts a connection on addr.
//
// Binding a port is not instantaneous — startMetrics opens the listener in a
// goroutine — so a request sent the moment it returns would be refused for a
// reason that has nothing to do with what is under test.
func waitUntilBound(t *testing.T, addr string) {
	t.Helper()

	for range 100 {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			require.NoError(t, conn.Close())

			return
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("nothing came up on %s", addr)
}

// TestTheMetricsListenerNeedsBothAnAddressAndAHandler pins the gate that
// decides whether a port is opened at all.
//
// Both halves matter and they fail differently. Without the ADDRESS check the
// default installation would publish a port nobody asked for — the cost ADR
// 0046 promises is zero until an operator sets the address. Without the HANDLER
// check an installation could bind a port that answers 404 to every scrape,
// which reads to an operator like a broken deployment rather than like a switch
// left off; that is reachable whenever observability.Setup declined to build a
// meter provider.
func TestTheMetricsListenerNeedsBothAnAddressAndAHandler(t *testing.T) {
	addr := freeAddr(t)

	tests := map[string]struct {
		addr    string
		handler http.Handler
	}{
		"no address, so there is nothing to open": {
			addr:    "",
			handler: http.NotFoundHandler(),
		},
		"an address but no handler, so there is nothing to serve": {
			addr:    addr,
			handler: nil,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			// The context is deliberately NOT canceled: a listener that opened
			// would hold the wait function until it was.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			wait := startMetrics(ctx, metricsConfig(tt.addr), tt.handler, discardLogger())

			assert.True(t, waitedWithin(wait, 2*time.Second),
				"no listener must have been opened, so the wait must return at once")
		})
	}

	// The reserved port must still be free: the second case above must not have
	// bound it.
	ln, err := net.Listen("tcp", addr)
	require.NoError(t, err, "the metrics port must not have been bound")
	require.NoError(t, ln.Close())
}

// TestTheMetricsListenerServesTheScrapeEndpoint proves the endpoint is really
// reachable over its own port.
//
// This is the whole point of ADR 0046 stated as a request: one HTTP call to the
// metrics address returns the current value of the instruments, with no
// collector, no export interval and no second process in the path. Serving it
// on the API router instead would have been one line, and would have put an
// unauthenticated route on the address the ingress publishes.
func TestTheMetricsListenerServesTheScrapeEndpoint(t *testing.T) {
	// Registered BEFORE the shutdown below, so it runs after it: cleanups run
	// last-registered first, and the no-op belongs in the global only once the
	// real provider has been closed.
	restoreTheGlobalMeterProvider(t)

	telemetry, err := observability.Setup(context.Background(), observability.Options{
		ServiceName: "gobit-test",
		Metrics:     true,
		Logger:      discardLogger(),
	})
	require.NoError(t, err)
	require.NotNil(t, telemetry.MetricsHandler,
		"metrics were asked for, so a handler must have come back")

	t.Cleanup(func() { _ = telemetry.Shutdown(context.Background()) })

	addr := freeAddr(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wait := startMetrics(ctx, metricsConfig(addr), telemetry.MetricsHandler, discardLogger())

	// Retry at short intervals until the listener is up; binding a port is not
	// instantaneous and a single attempt would be flaky.
	var resp *http.Response

	for range 100 {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet,
			"http://"+addr+observability.MetricsPath, http.NoBody)
		require.NoError(t, reqErr)

		resp, err = http.DefaultClient.Do(req)
		if err == nil {
			break
		}

		time.Sleep(20 * time.Millisecond)
	}

	require.NoError(t, err, "the metrics endpoint never answered on %s", addr)

	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	cancel()
	assert.True(t, waitedWithin(wait, 10*time.Second),
		"the listener must close when the context is canceled")
}

// TestTheScrapeIsCutOffByTheWriteBudget pins the ONE place startMetrics
// deliberately does not copy startProfiling's shape.
//
// The profiling listener leaves WriteTimeout at zero because a profile takes as
// long as it was asked to take. A scrape is a bounded response gathered from an
// in-memory store, so one that outruns the ordinary write budget is a fault
// worth cutting off rather than a request worth waiting for. ADR 0046 and the
// comment in startMetrics both single that difference out, and until this test
// existed, deleting the WriteTimeout line from startMetrics left the package
// green: a stated decision that no test can fail is a sentence, not a
// constraint.
//
// The handler is a stand-in rather than the real scrape handler on purpose.
// What is under test is the SERVER's budget, and a real gather over two
// instruments is far too fast to outrun any budget worth configuring.
func TestTheScrapeIsCutOffByTheWriteBudget(t *testing.T) {
	addr := freeAddr(t)

	cfg := metricsConfig(addr)
	cfg.WriteTimeout = 150 * time.Millisecond
	// The read budgets have to OUTLAST the handler, or the request would be cut
	// off for the wrong reason and this test would stay green with no write
	// budget at all.
	cfg.ReadHeaderTimeout = 30 * time.Second
	cfg.ReadTimeout = 30 * time.Second
	cfg.IdleTimeout = 30 * time.Second

	answered := make(chan struct{})
	slow := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		defer close(answered)

		time.Sleep(10 * cfg.WriteTimeout)
		_, _ = w.Write([]byte("this answer is far too late"))
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wait := startMetrics(ctx, cfg, slow, discardLogger())

	waitUntilBound(t, addr)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+addr+observability.MetricsPath, http.NoBody)
	require.NoError(t, err)

	// A client with a budget far longer than the handler's sleep: if the SERVER
	// does not cut the response off, this request SUCCEEDS, which is the exact
	// regression the test is here for.
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}

	require.Error(t, err,
		"the write budget must have ended the request; it answered %v instead", resp)

	<-answered

	cancel()
	assert.True(t, waitedWithin(wait, 10*time.Second),
		"the listener must close when the context is canceled")
}
