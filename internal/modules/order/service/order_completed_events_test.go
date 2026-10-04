package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// A completed order says so on the bus, as a canceled one does (ADR 0386).

// TestACompletionSaysItOnTheBus: completing an order publishes one event naming
// the order and the moment it was completed, and writes the same event into
// the outbox.
func TestACompletionSaysItOnTheBus(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	_, err = e.svc.CompleteOrder(ctx, order.ID)
	require.NoError(t, err)

	completed, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	require.NotNil(t, completed.CompletedAt)
	published := e.bus.eventsNamed(service.EventOrderCompleted)
	require.Len(t, published, 1, "one event, published after the commit")
	assert.Equal(t, service.EventOrderCompleted+":"+order.ID, published[0].ID)
	assert.Equal(t, map[string]any{
		service.EventFieldOrderID:     order.ID,
		service.EventFieldCompletedAt: completed.CompletedAt.UTC().Format(time.RFC3339Nano),
	}, published[0].Data, "the order and the moment the order holds")

	row, ok := e.store.outbox[published[0].ID]
	require.True(t, ok, "the event is promised in the completion's transaction")
	assert.Equal(t, service.EventOrderCompleted, row.Name)
	assert.Equal(t, published[0].Data, row.Data)
}

// TestASecondCompletionSaysNothingMore: the second call is a Conflict, and the
// order still said it once.
func TestASecondCompletionSaysNothingMore(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	_, err = e.svc.CompleteOrder(ctx, order.ID)
	require.NoError(t, err)

	_, err = e.svc.CompleteOrder(ctx, order.ID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeNotPending, errors.CodeOf(err))
	assert.Len(t, e.bus.eventsNamed(service.EventOrderCompleted), 1)
}

// TestARefusedCompletionSaysNothing: a canceled order is not completed, and
// nothing says it was.
func TestARefusedCompletionSaysNothing(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	require.NoError(t, e.svc.CancelPlacedOrder(ctx, order.ID, ""))

	_, err = e.svc.CompleteOrder(ctx, order.ID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Empty(t, e.bus.eventsNamed(service.EventOrderCompleted))
	assert.NotContains(t, e.store.outbox, service.EventOrderCompleted+":"+order.ID)
}

// TestTheCompletionAndItsEventCommitTogether: a completion whose event cannot
// be promised is not made.
func TestTheCompletionAndItsEventCommitTogether(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	e.store.outboxErr = errors.New("the outbox is unreachable")

	_, err = e.svc.CompleteOrder(ctx, order.ID)
	require.Error(t, err)

	current, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderPending, current.Status, "no promise, no completion")
	assert.Nil(t, current.CompletedAt)
	assert.Empty(t, e.bus.eventsNamed(service.EventOrderCompleted))
}

// TestALostPublishDoesNotUndoTheCompletion: the bus failing after the commit
// leaves the order completed and its outbox row for the relay.
func TestALostPublishDoesNotUndoTheCompletion(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	e.bus.failErr = errors.New("the bus is down")

	completed, err := e.svc.CompleteOrder(ctx, order.ID)

	require.NoError(t, err, "the order IS completed; the relay sends what the bus missed")
	assert.Equal(t, models.OrderCompleted, completed.Status)
	current, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderCompleted, current.Status)
	assert.Contains(t, e.store.outbox, service.EventOrderCompleted+":"+order.ID)
}

// TestArchivingSaysNothing: filing a completed order away publishes nothing
// and promises nothing (ADR 0386).
func TestArchivingSaysNothing(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	_, err = e.svc.CompleteOrder(ctx, order.ID)
	require.NoError(t, err)
	eventsBefore := len(e.bus.events())
	outboxBefore := len(e.store.outboxEvents())

	_, err = e.svc.ArchiveOrder(ctx, order.ID)
	require.NoError(t, err)

	assert.Len(t, e.bus.events(), eventsBefore, "archiving publishes nothing")
	assert.Len(t, e.store.outboxEvents(), outboxBefore, "archiving promises nothing")
}
