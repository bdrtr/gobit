//go:build integration

package app

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errorreport"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/core/config"
)

// TestTheInstallationProvidesItsClientKey is ADR 0368 on the production
// wiring: the opened installation holds, under corehttp.ClientKeyName, the
// key its own limit keys a client by, built from the hops it was told to
// trust.
func TestTheInstallationProvidesItsClientKey(t *testing.T) {
	ctx := context.Background()
	jobsEnv(t, migrateDSN(t))
	t.Setenv("TRUSTED_PROXY_HOPS", "1")
	cfg, err := config.Load()
	require.NoError(t, err)

	app, closeApp, err := openApplication(ctx, cfg, slog.New(slog.DiscardHandler), errorreport.NewSink(), Options{}, publishesOnly)
	require.NoError(t, err)
	defer closeApp()

	key, err := container.Resolve[corehttp.KeyFunc](app.container, corehttp.ClientKeyName)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/store/v1/auth/register", http.NoBody)
	req.RemoteAddr = "10.0.0.1:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	assert.Equal(t, "203.0.113.9", key(req), "one trusted hop: the forwarded address")
}
