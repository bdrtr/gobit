//go:build integration

package order_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestAnOrdersEventsCarryTheStoredMomentOnTheRealDatabase is ADR 0437's rule
// over the real schema (D278): order.placed's placed_at, order.line_canceled's
// canceled_at (the cancellation row's created_at), order.canceled's and
// order.completed's are the stored moments, compared in Go at full precision.
func TestAnOrdersEventsCarryTheStoredMomentOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	canceled, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	detail, err := svc.GetOrder(ctx, canceled.ID)
	require.NoError(t, err)
	require.NotEmpty(t, detail.Items)
	_, err = svc.CancelOrderLine(ctx, canceled.ID, service.CancelOrderLineInput{
		OrderLineItemID: detail.Items[0].ID, Quantity: 1, Reason: "out of stock",
	})
	require.NoError(t, err)
	require.NoError(t, svc.CancelPlacedOrder(ctx, canceled.ID, "the transfer never came"))
	completed, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	_, err = svc.CompleteOrder(ctx, completed.ID)
	require.NoError(t, err)

	rows, err := testPool.Pool().Query(ctx, `
        SELECT e.name, e.id,
               COALESCE(e.data->>'placed_at', e.data->>'completed_at', e.data->>'canceled_at'),
               CASE e.name
                   WHEN $2 THEN o.placed_at
                   WHEN $3 THEN c.created_at
                   WHEN $4 THEN o.canceled_at
                   WHEN $5 THEN o.completed_at
               END
        FROM event_outbox e
        JOIN orders o ON o.id = e.data->>'order_id'
        LEFT JOIN order_line_cancellations c ON c.id = e.data->>'cancellation_id'
        WHERE e.data->>'order_id' = ANY($1) AND e.name = ANY($6)`,
		[]string{canceled.ID, completed.ID},
		service.EventOrderPlaced, service.EventOrderLineCanceled, service.EventOrderCanceled,
		service.EventOrderCompleted,
		[]string{service.EventOrderPlaced, service.EventOrderLineCanceled, service.EventOrderCanceled,
			service.EventOrderCompleted})
	require.NoError(t, err)
	defer rows.Close()

	seen := map[string]int{}
	for rows.Next() {
		var name, id string
		var text *string
		var column *time.Time
		require.NoError(t, rows.Scan(&name, &id, &text, &column))
		seen[name]++
		if !assert.NotNil(t, text, "%s carries no moment", id) || !assert.NotNil(t, column, "%s has no column to compare", id) {
			continue
		}
		moment, parseErr := time.Parse(time.RFC3339Nano, *text)
		require.NoError(t, parseErr)
		assert.True(t, moment.Equal(*column), "%s carries %s and its row holds %s", id, moment, *column)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, 2, seen[service.EventOrderPlaced], "two orders placed")
	assert.Positive(t, seen[service.EventOrderLineCanceled], "a line written off")
	assert.Equal(t, 1, seen[service.EventOrderCanceled])
	assert.Equal(t, 1, seen[service.EventOrderCompleted])
}
