package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// An event's moment is the stored moment (ADR 0437, D278): a cart's two events
// carry the created_at and completed_at the row was written with, not a clock
// read before the transaction. The fake store stamps from a clock of its own,
// so a moment taken from the wall clock cannot equal it.

// TestACartsEventsCarryTheStoredMoments: cart.created's occurred_at is the
// row's created_at and cart.completed's its completed_at, in the outbox row and
// in the publish alike.
func TestACartsEventsCarryTheStoredMoments(t *testing.T) {
	svc, store, bus := newServiceWithBus(t)
	ctx := context.Background()

	cart := newCart(ctx, t, svc)
	item, err := svc.AddLineItem(ctx, cart.ID, service.AddLineItemInput{
		VariantID: variantA, Title: "T-shirt", Quantity: 1, UnitPrice: 500,
	})
	require.NoError(t, err)
	current, err := svc.GetCart(ctx, cart.ID)
	require.NoError(t, err)
	require.NoError(t, svc.SetTotals(ctx, cart.ID, service.Totals{
		Revision: current.Revision, Subtotal: 500, Total: 500,
		Lines: []service.LineTotals{{LineItemID: item.ID, UnitPrice: 500, Subtotal: 500, Total: 500}},
	}))
	completed, err := svc.MarkCompleted(ctx, cart.ID)
	require.NoError(t, err)
	require.NotNil(t, completed.CompletedAt)

	require.Len(t, store.outbox, 2)
	require.Len(t, bus.published, 2)
	for i, stored := range []time.Time{cart.CreatedAt, *completed.CompletedAt} {
		for where, data := range map[string]map[string]any{
			"outbox": store.outbox[i].data, "publish": bus.published[i].Data,
		} {
			text, ok := data[service.EventFieldOccurredAt].(string)
			require.Truef(t, ok, "the %s of %s carries occurred_at as a string", where, store.outbox[i].name)
			moment, parseErr := time.Parse(time.RFC3339Nano, text)
			require.NoError(t, parseErr)
			assert.Truef(t, moment.Equal(stored), "the %s of %s carries %s and the cart row holds %s",
				where, store.outbox[i].name, moment, stored)
		}
	}
}
