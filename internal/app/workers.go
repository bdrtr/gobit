package app

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
)

// The workers' error codes (ADR 0436).
const (
	// codeWorkerInvalid refuses a worker that cannot run as declared.
	codeWorkerInvalid = "plugin_worker_invalid"
	// codeWorkerPanicked reports a worker whose loop panicked.
	codeWorkerPanicked = "plugin_worker_panicked"
)

// pluginWorkers admits the workers the plugins declared during Setup
// (ADR 0436).
//
// It is [addPluginJobs]'s rule for the other shape: a worker with no name, a
// name another worker already took, or no Run stops the installation with the
// plugin's name in the message, rather than being skipped into a server that
// believes something is sending. A nil host is no plugins, as it is there.
func pluginWorkers(host *coreplugin.Host) ([]coreplugin.Worker, error) {
	if host == nil {
		return nil, nil
	}

	workers := host.Workers()
	taken := make(map[string]string, len(workers))
	for _, w := range workers {
		switch {
		case strings.TrimSpace(w.Name) == "":
			return nil, coreerrors.Invalid(codeWorkerInvalid,
				"a worker declared by the %s plugin has no name", w.PluginName())
		case w.Run == nil:
			return nil, coreerrors.Invalid(codeWorkerInvalid,
				"the %q worker declared by the %s plugin has no Run; a worker that does "+
					"nothing would leave the installation believing it runs", w.Name, w.PluginName())
		}
		if first, dup := taken[w.Name]; dup {
			return nil, coreerrors.Invalid(codeWorkerInvalid,
				"the %q worker declared by the %s plugin was already declared by the %s plugin",
				w.Name, w.PluginName(), first)
		}
		taken[w.Name] = w.PluginName()
	}

	return workers, nil
}

// workerSet is the plugins' workers while the server serves.
type workerSet struct {
	cancel context.CancelFunc
	done   chan struct{}
	log    *slog.Logger
}

// startWorkers runs every worker in a goroutine of its own, under a context
// that only [workerSet.stop] cancels.
//
// The context is detached from serve's for the runner's reason: SIGTERM
// cancels serve's context before the deferred stop runs, and a worker has to
// be told to stop by the same call that waits for it, or the wait has nothing
// to measure.
func startWorkers(ctx context.Context, workers []coreplugin.Worker, log *slog.Logger) *workerSet {
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	set := &workerSet{cancel: cancel, done: make(chan struct{}), log: log}

	var wg sync.WaitGroup
	names := make([]string, 0, len(workers))
	for _, w := range workers {
		names = append(names, w.Name)
		wg.Go(func() {
			err := runWorker(runCtx, w)
			if runCtx.Err() == nil {
				// Not restarted: a loop that returned on its own has a reason, and
				// running it again would turn one error line into one a second.
				log.ErrorContext(runCtx, "a plugin's worker stopped while the server serves; it is not started again",
					"worker", w.Name, "plugin", w.PluginName(), "error", err)

				return
			}
			if err != nil {
				log.WarnContext(runCtx, "a plugin's worker returned an error as it stopped",
					"worker", w.Name, "plugin", w.PluginName(), "error", err)
			}
		})
	}
	go func() {
		wg.Wait()
		close(set.done)
	}()

	if len(workers) > 0 {
		log.InfoContext(ctx, "the plugins' workers started", "workers", names)
	}

	return set
}

// stop cancels the workers and waits for them to return, for at most timeout.
//
// A worker still running when the window closes is logged, once, with the
// window: the pool closes after this returns, and a worker writing then fails
// against a closed pool rather than being waited for past the timeout the
// operator set.
func (s *workerSet) stop(timeout time.Duration) {
	s.cancel()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-s.done:
	case <-timer.C:
		s.log.Error("a plugin's worker did not stop within the shutdown timeout; the pool closes under it",
			"shutdown_timeout", timeout.String())
	}
}

// runWorker runs one worker's loop and turns a panic into an error, so that
// one plugin's bug is a log line rather than the end of the process.
func runWorker(ctx context.Context, w coreplugin.Worker) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = coreerrors.Internal(codeWorkerPanicked, "the %q worker panicked: %v", w.Name, v)
		}
	}()

	return w.Run(ctx)
}
