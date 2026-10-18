package plugin_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreplugin "github.com/bdrtr/gobit/core/plugin"
)

// TestARouteQuotaIsCollectedAtSetupWithItsPlugin is ADR 0452: a quota a
// plugin declares in Setup reaches the composition root with the plugin's
// name, and is handed out as a copy, its routes included.
func TestARouteQuotaIsCollectedAtSetupWithItsPlugin(t *testing.T) {
	t.Parallel()

	_, reg, h := newHost(t, nil)
	routes := []string{"POST /admin/v1/auth/login"}
	reg.Add(testPlugin{name: "dtm", setup: func(_ context.Context, h *coreplugin.Host) error {
		h.RegisterRouteQuota(coreplugin.RouteQuota{Name: "dtm.auth-burst", Routes: routes, Limit: 10, Window: time.Minute})

		return nil
	}})
	require.NoError(t, reg.Install(t.Context(), h))
	routes[0] = "POST /rewritten"

	quotas := h.RouteQuotas()
	require.Len(t, quotas, 1)
	assert.Equal(t, "dtm.auth-burst", quotas[0].Name)
	assert.Equal(t, "dtm", quotas[0].PluginName(), "a refusal at startup names the plugin")
	assert.Equal(t, []string{"POST /admin/v1/auth/login"}, quotas[0].Routes, "the declaration is a copy")
	assert.Equal(t, 10, quotas[0].Limit)

	quotas[0].Routes[0] = "POST /smuggled"
	assert.Equal(t, "POST /admin/v1/auth/login", h.RouteQuotas()[0].Routes[0], "and so is the read")
}
