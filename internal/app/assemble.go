package app

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/bdrtr/gobit/core/errorreport"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/core/logger"
)

// InProcess brings the whole installation up WITHOUT listening and hands back
// the handler [serve] would have served (ADR 0150).
//
// # Who this is for
//
// A program that embeds gobit and wants to test its own module against a real
// one. Before this there was no way: the facade offered `Main(args, out)`, which
// binds a port and blocks, and everything behind it — the migrations, the
// registry, the router — is under internal/. So an embedder's only options were
// to run the binary and talk to it over a socket, or to reimplement the
// assembly, which is the copy this repository already has a gate against
// (TestTheEndToEndGroundWiresEveryFlowProductionDoes).
//
// # It is the SAME assembly
//
// Everything between the database pool and the HTTP server is [assemble], and
// both callers go through it. That is the whole point: a harness with an assembly
// of its own would be a second answer to "what is an installation", and the one
// the tests trust would be the one nobody deploys.
//
// # What it does not start, and why that is not a shortcut
//
// No HTTP server, no operator listener, no scheduled job. The first two need a
// PORT, which a test must not have to reserve. The jobs need a CLOCK: a relay
// ticking underneath a test's own assertions makes a failure depend on when the
// test looked, and the work a job does — publishing an outbox row, sweeping a
// stuck saga — is exactly what such a test is usually asserting about. An
// embedder that wants the relay drives it directly.
//
// The consequence is worth stating rather than discovering: an event reaches a
// subscriber through the DIRECT publish only. What the outbox row promises is
// kept by the relay, so in an in-process installation it is kept by nobody until
// somebody runs it.
//
// # The configuration is read the same way too
//
// From the environment, through [config.Load], because a second configuration
// path is a second set of defaults and the test would then be running under
// values no deployment has. A caller sets DATABASE_URL and whatever else its
// scenario needs, exactly as it would for the real binary.
//
// The returned function releases the pool and shuts the container down; a caller
// that forgets it leaks a connection pool per call.
func InProcess(ctx context.Context, opts Options) (http.Handler, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}

	// The sink stays empty unless a plugin fills it, exactly as in serve; what
	// differs is that nothing here touches slog's DEFAULT logger. A library call
	// that reset the process-wide logger would change the behavior of the
	// program that called it, which is not a thing a test helper may do.
	reportSink := errorreport.NewSink()

	log := logger.New(logger.Options{
		Level:      cfg.SlogLevel(),
		Format:     cfg.LogFormat,
		AddSource:  !cfg.IsProduction(),
		Middleware: errorreport.Middleware(reportSink, errorreport.Options{}),
	})

	app, closeApp, err := openApplication(ctx, cfg, log, reportSink, opts)
	if err != nil {
		return nil, nil, err
	}

	handler, err := assemble(ctx, cfg, log, app, opts)
	if err != nil {
		closeApp()

		return nil, nil, err
	}

	closeAll := func() {
		// The sink is flushed BEFORE the pool closes: a reporter that writes to
		// the database would otherwise lose the reports of the very failures the
		// caller is testing. The context is detached for the reason serve
		// detaches it — the caller's test context is usually already done by the
		// time a cleanup runs.
		if flushErr := reportSink.Close(context.WithoutCancel(ctx)); flushErr != nil {
			log.ErrorContext(ctx, "the error reporter could not be flushed", slog.Any("error", flushErr))
		}

		closeApp()
	}

	return handler, closeAll, nil
}
