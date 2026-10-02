//go:build integration

package order_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order"
	"github.com/bdrtr/gobit/internal/modules/order/api"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// recordingOpener is the fulfilling flow as far as opening a parcel: it
// records each request and opens nothing.
type recordingOpener struct {
	api.Fulfilling
	requests []string
}

func (f *recordingOpener) OpenForOrder(
	_ context.Context, _ string, request json.RawMessage,
) (fulfillmentID string, alreadyOpen bool, err error) {
	f.requests = append(f.requests, string(request))
	return "ful_1", false, nil
}

// TestThePanelOpensAParcelOnTheDeliveryItNames is ADR 0332 on the real
// schema: the surface lists an order's deliveries as they stand, a changed
// one on its new option, in the order they were sold; a parcel opened on a
// delivery goes on the option that delivery stands on now, and one opened on
// none on the flow's default; a delivery the order does not have is not
// found and reaches no flow.
func TestThePanelOpensAParcelOnTheDeliveryItNames(t *testing.T) {
	ctx := context.Background()

	c := container.New(nil)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	bus := eventbus.NewInMemory(nil)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = bus.Shutdown(shutdownCtx)
	})
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.eventbus", bus))
	require.NoError(t, c.Provide("core.query", query.New(link.New(testPool, nil), c, nil)))
	require.NoError(t, order.New().Register(ctx, c))
	// The order module resolves the flow by this name on the first open.
	opener := &recordingOpener{}
	require.NoError(t, c.Provide("workflows.fulfilling.interop", api.Fulfilling(opener)))
	svc, err := container.Resolve[*service.Service](c, order.ServiceName)
	require.NoError(t, err)
	surface, err := container.Resolve[*order.AfterSalesSurface](c, order.AdminName)
	require.NoError(t, err)

	in := validInput()
	in.ShippingMethods = []service.CreateShippingMethodInput{
		{ShippingOptionID: "so_books", Name: "Books by post", Amount: 1000},
		{ShippingOptionID: "so_bulky", Name: "Bulky by van", Amount: 1500},
	}
	placed, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)
	detail, err := svc.GetOrder(ctx, placed.ID)
	require.NoError(t, err)
	require.Len(t, detail.ShippingMethods, 2)
	books, bulky := detail.ShippingMethods[0].ID, detail.ShippingMethods[1].ID
	if detail.ShippingMethods[0].ShippingOptionID != "so_books" {
		books, bulky = bulky, books
	}
	_, err = svc.ChangeDelivery(ctx, placed.ID, changeTo(bulky, "so_freight", 1200))
	require.NoError(t, err)

	raw, err := surface.DeliveriesJSON(ctx, placed.ID)
	require.NoError(t, err)
	var deliveries []struct {
		ID               string `json:"id"`
		ShippingOptionID string `json:"shipping_option_id"`
		Name             string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(raw, &deliveries))
	require.Len(t, deliveries, 2)
	got := map[string]string{}
	for _, delivery := range deliveries {
		got[delivery.ID] = delivery.ShippingOptionID + "|" + delivery.Name
	}
	assert.Equal(t, map[string]string{books: "so_books|Books by post", bulky: "so_freight|so_freight"}, got,
		"each delivery as it stands, the changed one on its new option")

	_, _, err = surface.OpenParcel(ctx, placed.ID, bulky, "panel-1")
	require.NoError(t, err)
	_, _, err = surface.OpenParcel(ctx, placed.ID, "", "panel-2")
	require.NoError(t, err)
	require.Len(t, opener.requests, 2)
	assert.JSONEq(t, `{"idempotency_key":"panel-1","shipping_option_id":"so_freight"}`, opener.requests[0],
		"the delivery's option as it stands now")
	assert.JSONEq(t, `{"idempotency_key":"panel-2"}`, opener.requests[1], "no delivery, the flow's default")

	_, _, err = surface.OpenParcel(ctx, placed.ID, "osm_missing", "panel-3")
	require.Error(t, err)
	assert.Equal(t, service.CodeDeliveryMissing, errors.CodeOf(err), "a delivery the order does not have: %v", err)
	assert.Len(t, opener.requests, 2, "and no flow is asked")

	_, err = surface.DeliveriesJSON(ctx, "order_missing")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "an unknown order: %v", err)
}
