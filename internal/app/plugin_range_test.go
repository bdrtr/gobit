package app

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/module"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	"github.com/bdrtr/gobit/internal/core/config"
)

// unreadableRange is an embedder's plugin naming its core releases wrongly.
type unreadableRange struct{ setUp *bool }

func (unreadableRange) Name() string         { return "unreadable-range" }
func (unreadableRange) RequiresCore() string { return "0.9 or later" }
func (p unreadableRange) Setup(context.Context, *coreplugin.Host) error {
	*p.setUp = true
	return nil
}

// TestTheCompositionRootRefusesAPluginsUnreadableRange is ADR 0224 on the
// production path: a plugin the embedding program hands in goes through the
// same Install, and a range that cannot be read stops the start before its
// Setup, whatever the build's release is.
func TestTheCompositionRootRefusesAPluginsUnreadableRange(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	c := container.New(log)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	setUp := false

	_, _, err := installPlugins(t.Context(), config.Config{}, c, module.NewRegistry(log, nil), nil, log,
		[]coreplugin.Plugin{unreadableRange{setUp: &setUp}})

	require.Error(t, err)
	assert.Equal(t, "plugin_core_range_invalid", coreerrors.CodeOf(err))
	assert.False(t, setUp)
}
