//go:build integration

package order_test

import (
	"context"
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
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// panelSurface registers the module on a container of its own and returns
// its service and the panel's surface over it.
func panelSurface(t *testing.T) (*service.Service, *order.AfterSalesSurface) {
	t.Helper()

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
	svc, err := container.Resolve[*service.Service](c, order.ServiceName)
	require.NoError(t, err)
	surface, err := container.Resolve[*order.AfterSalesSurface](c, order.AdminName)
	require.NoError(t, err)

	return svc, surface
}

// TestThePanelCancelsAPlacedOrder is ADR 0339 on the real schema: the
// surface cancels a pending order through the service the API's cancel
// calls, and a second cancel writes nothing; a completed order is refused.
// And ADR 0340: a pending order is completed once and not archived, a
// completed one is archived with its moment.
func TestThePanelCancelsAPlacedOrder(t *testing.T) {
	ctx := context.Background()
	svc, surface := panelSurface(t)

	pending, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	require.NoError(t, surface.CancelOrder(ctx, pending.ID, "never paid"))
	detail, err := svc.GetOrder(ctx, pending.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderCanceled, detail.Status)
	assert.Equal(t, "never paid", detail.CancelReason, "the reason is kept with the order")
	require.NoError(t, surface.CancelOrder(ctx, pending.ID, "again"), "a second cancel writes nothing")
	detail, err = svc.GetOrder(ctx, pending.ID)
	require.NoError(t, err)
	assert.Equal(t, "never paid", detail.CancelReason, "and keeps the first reason")

	completed, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	err = surface.ArchiveOrder(ctx, completed.ID)
	require.Error(t, err, "a pending order is not archived")
	require.NoError(t, surface.CompleteOrder(ctx, completed.ID))
	err = surface.CompleteOrder(ctx, completed.ID)
	assert.True(t, errors.IsConflict(err), "a second completion is refused: %v", err)
	err = surface.CancelOrder(ctx, completed.ID, "too late")
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "a completed order is not canceled: %v", err)
	detail, err = svc.GetOrder(ctx, completed.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderCompleted, detail.Status)

	require.NoError(t, surface.ArchiveOrder(ctx, completed.ID))
	detail, err = svc.GetOrder(ctx, completed.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderArchived, detail.Status, "a completed order is archived")
	require.NotNil(t, detail.ArchivedAt)
}

// TestThePanelWritesOffALineOnce is ADR 0341 on the real schema: the surface
// writes off a unit of a line from the count spoken for when it was read,
// and the same write-off sent again, the count having moved, is refused by
// the module under the order's lock.
func TestThePanelWritesOffALineOnce(t *testing.T) {
	ctx := context.Background()
	svc, surface := panelSurface(t)

	placed, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	detail, err := svc.GetOrder(ctx, placed.ID)
	require.NoError(t, err)
	lineID := detail.Items[0].ID

	require.NoError(t, surface.CancelOrderLine(ctx, placed.ID, lineID, 0, 1, "out of stock", "supplier late"))
	err = surface.CancelOrderLine(ctx, placed.ID, lineID, 0, 1, "out of stock", "")
	require.Error(t, err)
	assert.Equal(t, service.CodeLineMoved, errors.CodeOf(err), "the same write-off sent twice: %v", err)
	require.NoError(t, surface.CancelOrderLine(ctx, placed.ID, lineID, 1, 1, "out of stock", ""), "read after the first")

	cancellations, err := svc.ListLineCancellations(ctx, placed.ID)
	require.NoError(t, err)
	require.Len(t, cancellations, 2, "two write-offs, not three")
	assert.Equal(t, "supplier late", cancellations[0].Note)
}
