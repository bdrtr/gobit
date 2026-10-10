//go:build integration

package order_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestAReturnsMovesArePromisedOnTheRealDatabase is ADR 0437 over the real
// schema: each of two returns on one order writes its own opening into
// event_outbox in its own transaction, carrying the moment the row was stamped
// with; a withdrawal does the same once; and a receipt, and a withdrawal the
// receipt refuses, write nothing.
func TestAReturnsMovesArePromisedOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	withdrawn, err := svc.CreateReturn(ctx, service.CreateReturnInput{OrderID: ord.ID, Reason: "too small"})
	require.NoError(t, err)
	received, err := svc.CreateReturn(ctx, service.CreateReturnInput{OrderID: ord.ID})
	require.NoError(t, err)

	_, err = svc.CancelReturn(ctx, withdrawn.ID)
	require.NoError(t, err)
	_, err = svc.CancelReturn(ctx, withdrawn.ID)
	require.NoError(t, err, "a second withdrawal is a no-op")
	_, err = svc.ReceiveReturn(ctx, received.ID, "sloc_returns")
	require.NoError(t, err)
	_, err = svc.CancelReturn(ctx, received.ID)
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err), "a received return is not withdrawn")

	// The moment is compared in Go, at full precision, against the column.
	rows, err := testPool.Pool().Query(ctx, `
        SELECT e.name, r.id,
               COALESCE(e.data->>'opened_at', e.data->>'canceled_at'),
               CASE e.name WHEN $2 THEN r.created_at WHEN $3 THEN r.canceled_at END
        FROM event_outbox e
        JOIN order_returns r ON r.id = e.data->>'return_id'
        WHERE e.data->>'order_id' = $1
        ORDER BY e.name, r.id`,
		ord.ID, service.EventOrderReturnOpened, service.EventOrderReturnCanceled)
	require.NoError(t, err)
	defer rows.Close()

	type promised struct {
		name, returnID string
	}
	var got []promised
	for rows.Next() {
		var row promised
		var text *string
		var column *time.Time
		require.NoError(t, rows.Scan(&row.name, &row.returnID, &text, &column))
		got = append(got, row)
		if !assert.NotNil(t, text, "%s for %s carries no moment", row.name, row.returnID) ||
			!assert.NotNil(t, column, "%s for %s has no column to compare", row.name, row.returnID) {
			continue
		}
		moment, parseErr := time.Parse(time.RFC3339Nano, *text)
		require.NoError(t, parseErr)
		assert.True(t, moment.Equal(*column), "%s for %s carries %s and the row holds %s",
			row.name, row.returnID, moment, *column)
	}
	require.NoError(t, rows.Err())

	assert.ElementsMatch(t, []promised{
		{service.EventOrderReturnCanceled, withdrawn.ID},
		{service.EventOrderReturnOpened, withdrawn.ID},
		{service.EventOrderReturnOpened, received.ID},
	}, got, "two openings, one withdrawal, and nothing for the receipt or the refused withdrawal")
}
