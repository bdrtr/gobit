//go:build integration

package order_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestACompletionIsPromisedOnTheRealDatabase is ADR 0386 over the real schema:
// the completion writes its event into event_outbox in its own transaction,
// carrying the moment the order row holds, and archiving the order afterwards
// writes nothing.
func TestACompletionIsPromisedOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	_, err = svc.CompleteOrder(ctx, ord.ID)
	require.NoError(t, err)

	// The moment is compared as a timestamp, in SQL: its text is the
	// application's formatting and the column's is the server's.
	var sameMoment bool
	require.NoError(t, testPool.Pool().QueryRow(ctx, `
        SELECT (e.data->>'completed_at')::timestamptz = o.completed_at
        FROM event_outbox e
        JOIN orders o ON o.id = e.data->>'order_id'
        WHERE e.id = $1 || $2 AND e.name = $3`,
		service.EventOrderCompleted+":", ord.ID, service.EventOrderCompleted).Scan(&sameMoment))
	assert.True(t, sameMoment, "the event carries the moment the order row was stamped with")

	_, err = svc.ArchiveOrder(ctx, ord.ID)
	require.NoError(t, err)

	var completions, archives int
	require.NoError(t, testPool.Pool().QueryRow(ctx, `
        SELECT count(*) FILTER (WHERE name = $1),
               count(*) FILTER (WHERE name = 'order.archived')
        FROM event_outbox
        WHERE data->>'order_id' = $2`,
		service.EventOrderCompleted, ord.ID).Scan(&completions, &archives))
	assert.Equal(t, 1, completions, "one completion, promised once")
	assert.Zero(t, archives, "archiving publishes nothing (ADR 0386)")
}
