package main

import (
	"context"
	"log/slog"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/core/config"
)

// startProfiling opens the pprof listener when one has been configured, and
// returns a function that waits for it to close.
//
// # Why a failure here does not stop the boot
//
// The address is already known to be loopback-only ([config.Config.Validate]),
// so what can still go wrong at this point is a port that is taken. Refusing to
// serve the shop over a busy debugging port would be the wrong trade — but so
// would coming up quietly, because the operator would find out by attaching to
// nothing. The failure is logged at ERROR and the shop keeps going.
func startProfiling(ctx context.Context, cfg config.Config, log *slog.Logger) func() {
	if cfg.ProfilingAddr == "" {
		return func() {}
	}

	log = log.With("component", "profiling")
	log.Warn("the profiling listener is OPEN; it is unauthenticated and its profiles carry "+
		"the contents of live memory", "addr", cfg.ProfilingAddr)

	srv := corehttp.NewServer(corehttp.ServerOptions{
		Addr:              cfg.ProfilingAddr,
		Handler:           corehttp.ProfilingHandler(),
		Logger:            log,
		ShutdownTimeout:   cfg.ShutdownTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		// WriteTimeout is left at ZERO on purpose. A profile takes as long as it
		// was asked to take — "?seconds=120" is an ordinary request — and any
		// budget here would cut off exactly the long profile the listener was
		// opened for. It is the reason these endpoints are not on the API
		// server, whose 30s write budget is there to protect the shop.
		IdleTimeout: cfg.IdleTimeout,
	})

	done := make(chan struct{})

	go func() {
		defer close(done)

		if err := srv.Run(ctx); err != nil {
			log.Error("the profiling listener could not be started; profiles are NOT available",
				"addr", cfg.ProfilingAddr, "error", err)
		}
	}()

	return func() { <-done }
}
