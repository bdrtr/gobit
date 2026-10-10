package app

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
)

// workerPlugin is a plugin whose whole purpose is to declare the workers it is
// given.
type workerPlugin struct {
	name    string
	workers []coreplugin.Worker
}

// Name returns the plugin's name.
func (p workerPlugin) Name() string { return p.name }

// Setup declares the workers through the host.
func (p workerPlugin) Setup(_ context.Context, h *coreplugin.Host) error {
	for _, w := range p.workers {
		h.RegisterWorker(w)
	}

	return nil
}

// idle is a worker's loop that waits for the end.
func idle(ctx context.Context) error {
	<-ctx.Done()

	return nil
}

// TestAPluginWorkerThatCannotRunStopsTheInstallation is ADR 0436's admission:
// a worker with no name, no Run, or a name another plugin's worker took is
// refused with the plugin's name, and one that can run is admitted.
func TestAPluginWorkerThatCannotRunStopsTheInstallation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		plugins []coreplugin.Plugin
		says    string
	}{
		{
			name:    "no name",
			plugins: []coreplugin.Plugin{workerPlugin{name: "nameless", workers: []coreplugin.Worker{{Run: idle}}}},
			says:    "nameless plugin has no name",
		},
		{
			name: "no run",
			plugins: []coreplugin.Plugin{workerPlugin{name: "lazy", workers: []coreplugin.Worker{
				{Name: "lazy-loop"},
			}}},
			says: `"lazy-loop" worker declared by the lazy plugin has no Run`,
		},
		{
			name: "a name taken",
			plugins: []coreplugin.Plugin{
				workerPlugin{name: "first", workers: []coreplugin.Worker{{Name: "shared-loop", Run: idle}}},
				workerPlugin{name: "second", workers: []coreplugin.Worker{{Name: "shared-loop", Run: idle}}},
			},
			says: `"shared-loop" worker declared by the second plugin was already declared by the first plugin`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			workers, err := pluginWorkers(installed(t, tc.plugins...))
			require.Error(t, err)
			assert.Nil(t, workers)
			assert.Equal(t, codeWorkerInvalid, coreerrors.CodeOf(err))
			assert.Contains(t, err.Error(), tc.says)
		})
	}

	workers, err := pluginWorkers(installed(t, workerPlugin{name: "webhook-out", workers: []coreplugin.Worker{
		{Name: "webhookout-send", Run: idle},
	}}))
	require.NoError(t, err)
	require.Len(t, workers, 1)
	assert.Equal(t, "webhook-out", workers[0].PluginName())

	none, err := pluginWorkers(nil)
	require.NoError(t, err, "no host is no plugins")
	assert.Empty(t, none)
}

// stoppedRunner is the job runner as stopWork sees it.
type stoppedRunner struct{ stopped bool }

// Stop records the call.
func (r *stoppedRunner) Stop() { r.stopped = true }

// TestAWorkerHasReturnedWhenTheServerStopsWaiting is the shutdown half: the
// stop serve defers cancels the worker and returns only once the worker's Run
// has, so the pool that closes after it closes under nothing; and a worker
// that outlasts the window is waited for exactly that long and named in the
// log.
func TestAWorkerHasReturnedWhenTheServerStopsWaiting(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var (
			mu       sync.Mutex
			returned bool
		)
		finishing := func(ctx context.Context) error {
			<-ctx.Done()
			// The attempt in flight, finishing after it was told to stop.
			time.Sleep(3 * time.Second)
			mu.Lock()
			returned = true
			mu.Unlock()

			return nil
		}

		running := startWorkers(t.Context(), []coreplugin.Worker{{Name: "finishing", Run: finishing}},
			slog.New(slog.DiscardHandler))
		runner := &stoppedRunner{}
		start := time.Now()
		stopWork(running, runner, 15*time.Second)

		mu.Lock()
		defer mu.Unlock()
		assert.True(t, returned, "stop returned while the worker was still running")
		assert.Equal(t, 3*time.Second, time.Since(start), "stop waited for the worker and no longer")
		assert.True(t, runner.stopped, "the runner was not stopped")
	})

	synctest.Test(t, func(t *testing.T) {
		var logged bytes.Buffer
		stuck := func(context.Context) error {
			time.Sleep(time.Hour)

			return nil
		}
		running := startWorkers(t.Context(), []coreplugin.Worker{{Name: "stuck", Run: stuck}},
			slog.New(slog.NewTextHandler(&logged, nil)))

		start := time.Now()
		stopWork(running, &stoppedRunner{}, 15*time.Second)

		assert.Equal(t, 15*time.Second, time.Since(start), "a stuck worker is waited for the window and no longer")
		assert.Contains(t, logged.String(), "did not stop within the shutdown timeout")
		time.Sleep(time.Hour)
	})
}

// TestAWorkerThatEndsOrPanicsIsLoggedAndNotStartedAgain: a loop that returns
// while the server serves, or panics, is an error in the log, once, and the
// process goes on.
func TestAWorkerThatEndsOrPanicsIsLoggedAndNotStartedAgain(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var (
			logged bytes.Buffer
			mu     sync.Mutex
			runs   int
		)
		count := func() {
			mu.Lock()
			runs++
			mu.Unlock()
		}
		running := startWorkers(t.Context(), []coreplugin.Worker{
			{Name: "quitter", Run: func(context.Context) error { count(); return errors.New("gave up") }},
			{Name: "panicker", Run: func(context.Context) error { count(); panic("boom") }},
		}, slog.New(slog.NewTextHandler(&logged, nil)))
		synctest.Wait()

		stopWork(running, &stoppedRunner{}, time.Second)

		mu.Lock()
		defer mu.Unlock()
		assert.Equal(t, 2, runs, "a worker that ended was started again")
		assert.Contains(t, logged.String(), "worker=quitter")
		assert.Contains(t, logged.String(), "gave up")
		assert.Contains(t, logged.String(), "worker=panicker")
		assert.Contains(t, logged.String(), codeWorkerPanicked)
	})
}
