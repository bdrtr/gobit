//go:build integration

package cart_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// TestACartsEventsCarryTheStoredMomentOnTheRealDatabase is ADR 0437's rule over
// the real schema (D278): cart.created's occurred_at is the row's created_at
// and cart.completed's its completed_at, compared in Go at full precision — a
// comparison in SQL would round the payload's text to the column's
// microseconds first.
func TestACartsEventsCarryTheStoredMomentOnTheRealDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cart := newCart(ctx, t, svc)
	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: "variant_A", Title: "Red T-Shirt", Quantity: 1, UnitPrice: 1000,
	})
	require.NoError(t, err)
	revision, _ := cartRevisions(ctx, t, cart.ID)
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: revision, Subtotal: 1000, Total: 1000,
		Lines: []service.LineTotals{{LineItemID: item.ID, UnitPrice: 1000, Subtotal: 1000, Total: 1000}},
	}))
	_, err = svc.MarkCompleted(ctx, cart.ID)
	require.NoError(t, err)

	rows, err := testPool.Pool().Query(ctx, `
        SELECT e.name, e.data->>'occurred_at',
               CASE e.name WHEN $2 THEN c.created_at WHEN $3 THEN c.completed_at END
        FROM event_outbox e
        JOIN carts c ON c.id = e.data->>'cart_id'
        WHERE e.data->>'cart_id' = $1`,
		cart.ID, service.EventCartCreated, service.EventCartCompleted)
	require.NoError(t, err)
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		var text *string
		var column *time.Time
		require.NoError(t, rows.Scan(&name, &text, &column))
		names = append(names, name)
		if !assert.NotNil(t, text, "%s carries no moment", name) || !assert.NotNil(t, column, "%s's column is empty", name) {
			continue
		}
		moment, parseErr := time.Parse(time.RFC3339Nano, *text)
		require.NoError(t, parseErr)
		assert.True(t, moment.Equal(*column), "%s carries %s and the cart row holds %s", name, moment, *column)
	}
	require.NoError(t, rows.Err())
	assert.ElementsMatch(t, []string{service.EventCartCreated, service.EventCartCompleted}, names)
}
