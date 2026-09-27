//go:build integration

package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errorreport"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/modules/customer"
	customerapi "github.com/bdrtr/gobit/internal/modules/customer/api"
	customersvc "github.com/bdrtr/gobit/internal/modules/customer/service"
)

// TestTheSegmentPreviewReachesTheFlowTheRootWires is ADR 0217's preview on the
// production wiring: the customer module resolves the segment flow by name on
// its first request, so a composition root that built the flow and did not
// provide it would start green and answer the preview with a setup failure.
func TestTheSegmentPreviewReachesTheFlowTheRootWires(t *testing.T) {
	ctx := context.Background()
	dsn := migrateDSN(t)
	jobsEnv(t, dsn)
	cfg, err := config.Load()
	require.NoError(t, err)

	app, closeApp, err := openApplication(ctx, cfg, slog.New(slog.DiscardHandler), errorreport.NewSink(),
		Options{}, publishesOnly)
	require.NoError(t, err)
	defer closeApp()
	customers, err := container.Resolve[*customersvc.Service](app.container, customer.ServiceName)
	require.NoError(t, err)
	_, err = customers.CreateCustomer(ctx, customersvc.CustomerInput{Email: "segment@example.com"})
	require.NoError(t, err)

	flow, err := container.Resolve[customerapi.SegmentPreview](app.container, customer.SegmentFlowName)
	require.NoError(t, err, "the root provides the flow under the name the preview resolves")
	raw, err := flow.PreviewSegmentJSON(ctx,
		json.RawMessage(`{"conditions":[{"attribute":"has_account","operator":"eq","value":true}]}`))

	require.NoError(t, err)
	assert.JSONEq(t, `{"members":1,"customers":1}`, string(raw), "the account the installation holds")
}
