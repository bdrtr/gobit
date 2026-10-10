//go:build integration

package app

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errorreport"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	"github.com/bdrtr/gobit/internal/core/config"
)

// TestServeStartsAPluginWorkerAndWaitsForItOnTheWayOut is ADR 0436 at the
// composition root: the jobs serve starts include a plugin's worker, and the
// stop serve defers returns only after the worker has, so the pool that closes
// next closes under nothing.
func TestServeStartsAPluginWorkerAndWaitsForItOnTheWayOut(t *testing.T) {
	dsn := migrateDSN(t)
	ctx := context.Background()
	jobsEnv(t, dsn)

	cfg, err := config.Load()
	require.NoError(t, err)
	log := slog.New(slog.DiscardHandler)

	started, returned := make(chan struct{}), make(chan struct{})
	loop := func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(returned)

		return nil
	}
	plugins := []coreplugin.Plugin{workerPlugin{name: "looper", workers: []coreplugin.Worker{
		{Name: "looper-loop", Run: loop},
	}}}

	app, closeApp, err := openApplication(ctx, cfg, log, errorreport.NewSink(), Options{Plugins: plugins}, consumesEvents)
	require.NoError(t, err)
	defer closeApp()

	_, stopJobs, err := startJobs(ctx, app.container, app.host, cfg, log)
	require.NoError(t, err)

	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("serve did not start the plugin's worker")
	}

	stopJobs()

	select {
	case <-returned:
	default:
		t.Fatal("the stop serve defers returned before the plugin's worker did")
	}
}
