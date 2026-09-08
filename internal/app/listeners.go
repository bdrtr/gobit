package app

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/bdrtr/gobit/internal/core/config"
)

// startOperatorListeners opens the two listeners an operator may ask for — the
// pprof endpoint and the metrics scrape — and returns the ONE function that
// closes both and waits for them to be gone.
//
// # Why they close on a context of their own
//
// Because [serve] can return while the process context is still LIVE. That
// context is canceled by SIGTERM and by nothing else, and
// [github.com/bdrtr/gobit/core/http.Server.Run] hands back a listen failure the
// moment it happens without canceling anything — so an API port that is already
// bound ends serve with an error while both of these listeners are still
// selecting on a signal that will never arrive. A wait on them at that point
// would block forever: no exit, and no report of the failure serve had already
// diagnosed. The listeners therefore run under a child context, and the
// returned function cancels it BEFORE it waits.
//
// This is not a hypothetical. A busy API port is the commonest way a boot fails,
// and both of these listeners are opened before the API server is.
//
// # Why the two are closed together, and before the providers behind them
//
// One cancel closes both, so the order between them decides nothing. The order
// against the caller's other deferred work does: the caller registers this
// AFTER the telemetry shutdown, and defers run last-registered first, so both
// listeners are gone before the meter provider they read from is closed. A
// scrape arriving at a provider that has already been shut down answers 500,
// which reads to an operator as a broken target rather than as a process on its
// way out.
func startOperatorListeners(
	ctx context.Context, cfg config.Config, metrics http.Handler, log *slog.Logger,
) func() {
	listenerCtx, closeListeners := context.WithCancel(ctx)

	waitForProfiling := startProfiling(listenerCtx, cfg, log)
	waitForMetrics := startMetrics(listenerCtx, cfg, metrics, log)

	return func() {
		closeListeners()
		waitForMetrics()
		waitForProfiling()
	}
}
