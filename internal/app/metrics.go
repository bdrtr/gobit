package app

import (
	"context"
	"log/slog"
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/core/observability"
)

// startMetrics opens the metrics listener when one has been configured, and
// returns a function that waits for it to close.
//
// # Where the "no address, no metrics" rule lives
//
// Here, and only here. observability.Setup is told YES or NO and never sees an
// address, because it builds a handler and binds nothing; if the two ends could
// disagree, an installation could end up paying for an aggregation store that
// nobody can read, or opening a port that answers 404 to every scrape. The
// handler being nil is therefore treated as the same answer as the address
// being empty, and both mean no listener.
//
// # Why a failure here does not stop the boot
//
// The same trade [startProfiling] makes, for the same reason (ADR 0007):
// telemetry serves the product's visibility and not its correctness, so a
// metrics port that cannot be bound must not close the shop. It is logged at
// ERROR rather than swallowed, because the operator would otherwise find out by
// pointing a scraper at nothing.
func startMetrics(
	ctx context.Context, cfg config.Config, handler http.Handler, log *slog.Logger,
) func() {
	if cfg.MetricsAddr == "" || handler == nil {
		return func() {}
	}

	log = log.With("component", "metrics")
	log.Warn("the metrics listener is OPEN; it is unauthenticated and its series name every "+
		"route this API serves", "addr", cfg.MetricsAddr, "path", observability.MetricsPath)

	srv := corehttp.NewServer(corehttp.ServerOptions{
		Addr:              cfg.MetricsAddr,
		Handler:           handler,
		Logger:            log,
		ShutdownTimeout:   cfg.ShutdownTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		// The ordinary write budget applies, and this is where the profiling
		// listener's shape is NOT copied. A profile takes as long as it was
		// asked to take, which is why that server leaves WriteTimeout at zero;
		// a scrape is a bounded response gathered from an in-memory store, so a
		// scrape that outlasts the budget is a fault worth cutting off rather
		// than a request worth waiting for.
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	})

	done := make(chan struct{})

	go func() {
		defer close(done)

		if err := srv.Run(ctx); err != nil {
			log.Error("the metrics listener could not be started; the metrics are NOT scrapable",
				"addr", cfg.MetricsAddr, "error", err)
		}
	}()

	return func() { <-done }
}
