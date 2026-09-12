package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// The module's two events (ADR 0153).
//
// Every test here has one of two subjects: what a SUBSCRIBER is told, or what
// happens to the promise when the write fails. The first is the contract; the
// second is the reason the row goes inside the transaction.

// TestCreatingACartPublishesItOnceThroughBothPaths pins the pair.
//
// The outbox row and the direct publish are ONE event carrying ONE id. If they
// ever carried two, a subscriber that is idempotent on the id — which the bus's
// at-least-once contract already requires — would act twice on one cart.
func TestCreatingACartPublishesItOnceThroughBothPaths(t *testing.T) {
	svc, store, bus := newServiceWithBus(t)
	ctx := context.Background()

	cart, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CurrencyCode: currency,
	})
	require.NoError(t, err)

	assert.Equal(t, []string{service.EventCartCreated}, store.outboxNames(),
		"the event must be recorded in the outbox INSIDE the cart's transaction")
	assert.Equal(t, []string{service.EventCartCreated}, bus.names(),
		"and published directly after the commit, so a subscriber hears in the same request")

	require.Len(t, store.outbox, 1)
	require.Len(t, bus.published, 1)
	assert.Equal(t, store.outbox[0].id, bus.published[0].ID,
		"both deliveries must carry the SAME id; two ids would make one cart look "+
			"like two events to every subscriber")
	assert.Equal(t, store.outbox[0].data, bus.published[0].Data,
		"and the SAME body: the payload is built once for exactly this reason")

	assert.Equal(t, map[string]any{
		service.EventFieldCartID:       cart.ID,
		service.EventFieldRegionID:     regionID,
		service.EventFieldCurrencyCode: currency,
		service.EventFieldOccurredAt:   store.outbox[0].data[service.EventFieldOccurredAt],
	}, store.outbox[0].data,
		"the payload carries the cart, its region and its currency — and NOTHING else: "+
			"no amount and no identity go out on a wire that is forwarded to third parties")
}

// TestCompletingACartPublishesTheOtherTopic keeps the two moments apart.
//
// A completion is not a placement. The checkout saga places the order at its
// second step and completes the cart at its last, so an order that fails in
// between has an "order.placed" and no completed cart — and telling the two
// apart is the whole reason a funnel can say where carts die.
func TestCompletingACartPublishesTheOtherTopic(t *testing.T) {
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
		Revision: current.Revision,
		Subtotal: 500, Total: 500,
		Lines: []service.LineTotals{{
			LineItemID: item.ID, UnitPrice: 500, Subtotal: 500, Total: 500,
		}},
	}))

	_, err = svc.MarkCompleted(ctx, cart.ID)
	require.NoError(t, err)

	assert.Equal(t, []string{service.EventCartCreated, service.EventCartCompleted},
		store.outboxNames(), "one cart produces both events, in order")
	assert.Equal(t, []string{service.EventCartCreated, service.EventCartCompleted},
		bus.names())

	require.Len(t, store.outbox, 2)
	assert.NotEqual(t, store.outbox[0].id, store.outbox[1].id,
		"the two events of ONE cart must not share an id: keyed on the cart alone the "+
			"completion would look like a redelivery of the creation, and the outbox's "+
			"ON CONFLICT DO NOTHING would drop it in silence")
}

// TestARefusedCompletionPublishesNothing is the negative half of the pair.
//
// An empty cart cannot be completed. If the event were published anyway a
// subscriber would count a conversion that did not happen.
func TestARefusedCompletionPublishesNothing(t *testing.T) {
	svc, store, bus := newServiceWithBus(t)
	ctx := context.Background()

	cart := newCart(ctx, t, svc)

	_, err := svc.MarkCompleted(ctx, cart.ID)
	require.Error(t, err, "a cart with no lines cannot be completed")

	assert.Equal(t, []string{service.EventCartCreated}, store.outboxNames(),
		"the refused completion must leave no outbox row")
	assert.Equal(t, []string{service.EventCartCreated}, bus.names(),
		"and must publish nothing")
}

// TestAFailedOutboxWriteFailsTheCart is why the row goes INSIDE the transaction.
//
// The alternative — write the cart, then try to record the event — produces a
// cart nobody was told about, which is the state the outbox exists to prevent
// while looking like it prevents it.
func TestAFailedOutboxWriteFailsTheCart(t *testing.T) {
	svc, store, bus := newServiceWithBus(t)
	ctx := context.Background()
	store.failWriteOutboxEvent = errors.New("outbox is down")

	_, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CurrencyCode: currency,
	})

	require.Error(t, err, "a cart whose event cannot be recorded must not be written")
	assert.Empty(t, store.carts, "the cart must have been rolled back with its event")
	assert.Empty(t, bus.names(), "and nothing may be published for a cart that does not exist")
}

// TestALostPublishStillWritesTheCart is the same boundary from the other side.
//
// The bus failing AFTER the commit is not the cart's problem: the cart exists,
// the outbox row exists, and the relay will deliver what this call missed. An
// error here would tell the caller that something did not happen when it did.
func TestALostPublishStillWritesTheCart(t *testing.T) {
	svc, store, bus := newServiceWithBus(t)
	ctx := context.Background()
	bus.err = errors.New("the bus is down")

	cart, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CurrencyCode: currency,
	})

	require.NoError(t, err,
		"the cart is already written and its event is already in the outbox; a failed "+
			"direct publish costs a subscriber a minute, not the cart")
	assert.NotEmpty(t, cart.ID)
	assert.Equal(t, []string{service.EventCartCreated}, store.outboxNames(),
		"the row the relay will deliver must still be there")
}

// TestTheOutboxRefusesOutsideATransaction pins the fake to the real rule.
//
// It is a test of the FIXTURE as much as of the rule: a fake that accepted the
// write outside a transaction would let every test above pass while the real
// repository refused, which is the "fake that does not imitate the schema" class.
func TestTheOutboxRefusesOutsideATransaction(t *testing.T) {
	store := newFakeStore()

	err := store.WriteOutboxEvent(context.Background(), "id", "cart.created", nil)

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInternal, coreerrors.KindOf(err))
}
