package app

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errorreport"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/openapi"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
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
// assembly, which this repository did in internal/e2e until ADR 0398.
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
	installation, closeAll, err := Open(ctx, opts)
	if err != nil {
		return nil, nil, err
	}

	return installation.Router, closeAll, nil
}

// Installation is an installation brought up without listening, with the parts
// a test inside this repository reads.
//
// The container's names are not a contract (ADR 0001): this type is internal,
// and the facade hands an embedder the router alone (ADR 0150).
type Installation struct {
	// Router is the handler the server would serve.
	Router chi.Router
	// Container holds every service the installation resolves by name.
	Container *container.Container
	// Modules is the registry's module list, the ones plugins brought included.
	Modules []module.Module
	// Schema is the document /openapi.json serves.
	Schema *openapi.Doc
	// Jobs are the scheduled jobs the plugins registered. Nothing runs them:
	// an opened installation starts no clock.
	Jobs []coreplugin.Job

	describe func() *openapi.Doc
}

// Describe builds a fresh copy of the served document's descriptions, for a
// caller that generates it against a router of its own.
//
// It is the assembly's own describing step and not a copy of it, so a test that
// asks which endpoints are described asks the function the server runs.
func (i *Installation) Describe() *openapi.Doc { return i.describe() }

// Open brings the installation up without listening and returns it with the
// parts a test inside this repository reads (ADR 0398).
//
// The container's names are not a contract (ADR 0001); only the facade and
// internal/e2e may import this package, and
// TestOnlyTheFacadeAndTheGroundOpenTheCompositionRoot keeps it so. Everything
// [InProcess] says about the port, the clock and the configuration holds here,
// because InProcess is this function keeping only the router.
func Open(ctx context.Context, opts Options) (*Installation, func(), error) {
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

	app, closeApp, err := openApplication(ctx, cfg, log, reportSink, opts, consumesEvents)
	if err != nil {
		return nil, nil, err
	}

	router, err := assemble(ctx, cfg, log, app, opts)
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

	modules := app.registry.Modules()
	title, version := cfg.ServiceName+" API", opts.version()

	return &Installation{
		Router:    router,
		Container: app.container,
		Modules:   modules,
		Schema:    app.schema,
		Jobs:      app.host.Jobs(),
		describe: func() *openapi.Doc {
			return describeInstallation(title, version, modules)
		},
	}, closeAll, nil
}
