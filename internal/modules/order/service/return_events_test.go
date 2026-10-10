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

// A return's opening and its withdrawal are on the bus (ADR 0437): written into
// the outbox in the move's transaction, published after the commit, once, and
// never for a refused move. Its receipt is not.

// openedReturn places an order and opens a return on it that carries the
// shopper's words.
func openedReturn(t *testing.T, e env) (models.Order, models.Return) {
	t.Helper()

	ctx := context.Background()
	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	ret, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: order.ID, RefundAmount: 1000, Reason: "the size did not fit", Note: "call me first",
	})
	require.NoError(t, err)

	return order, ret
}

// TestAnOpenedReturnSaysItOnTheBus: opening a return publishes one event naming
// the order, the return and the moment the record holds, writes the same event
// into the outbox, and carries neither the reason nor the note.
func TestAnOpenedReturnSaysItOnTheBus(t *testing.T) {
	e := newEnv(t)
	order, ret := openedReturn(t, e)

	published := e.bus.eventsNamed(service.EventOrderReturnOpened)
	require.Len(t, published, 1, "one event, published after the commit")
	assert.Equal(t, service.EventOrderReturnOpened+":"+ret.ID, published[0].ID)
	assert.Equal(t, map[string]any{
		service.EventFieldOrderID:  order.ID,
		service.EventFieldReturnID: ret.ID,
		service.EventFieldOpenedAt: ret.CreatedAt.UTC().Format(time.RFC3339Nano),
	}, published[0].Data, "identities and the moment the record holds; not the shopper's words")

	row, ok := e.store.outbox[published[0].ID]
	require.True(t, ok, "the event is promised in the opening's transaction")
	assert.Equal(t, service.EventOrderReturnOpened, row.Name)
	assert.Equal(t, published[0].Data, row.Data, "one builder for the row and the publish")
}

// TestTwoReturnsOnOneOrderSayItTwice: the event's id is the return's, so a
// second return on the same order is a second event and a second row.
func TestTwoReturnsOnOneOrderSayItTwice(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, first := openedReturn(t, e)
	second, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{OrderID: order.ID})
	require.NoError(t, err)

	published := e.bus.eventsNamed(service.EventOrderReturnOpened)
	require.Len(t, published, 2)
	assert.Equal(t, first.ID, published[0].Data[service.EventFieldReturnID])
	assert.Equal(t, second.ID, published[1].Data[service.EventFieldReturnID])
	assert.Contains(t, e.store.outbox, service.EventOrderReturnOpened+":"+first.ID)
	assert.Contains(t, e.store.outbox, service.EventOrderReturnOpened+":"+second.ID)
}

// TestARefusedReturnSaysNothing: a return asking back more than the order took
// is not opened, and nothing says it was.
func TestARefusedReturnSaysNothing(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	_, err = e.svc.CreateReturn(ctx, service.CreateReturnInput{OrderID: order.ID, RefundAmount: order.Total + 1})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Empty(t, e.bus.eventsNamed(service.EventOrderReturnOpened))
	assert.Empty(t, outboxRowsNamed(e, service.EventOrderReturnOpened))
}

// TestAReturnWhoseEventCannotBeWrittenIsNotOpened: a return whose event cannot
// be promised is not opened.
func TestAReturnWhoseEventCannotBeWrittenIsNotOpened(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	e.store.outboxErr = errors.New("the outbox is unreachable")

	_, err = e.svc.CreateReturn(ctx, service.CreateReturnInput{OrderID: order.ID})
	require.Error(t, err)

	returns, total, err := e.svc.ListReturns(ctx, order.ID, service.Page{Limit: 10})
	require.NoError(t, err)
	assert.Zero(t, total, "no promise, no return")
	assert.Empty(t, returns)
	assert.Empty(t, e.bus.eventsNamed(service.EventOrderReturnOpened))
}

// TestALostPublishDoesNotUndoTheReturn: the bus failing after the commit
// leaves the return open and its outbox row for the relay.
func TestALostPublishDoesNotUndoTheReturn(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	e.bus.failErr = errors.New("the bus is down")

	ret, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{OrderID: order.ID})

	require.NoError(t, err, "the return IS open; the relay sends what the bus missed")
	assert.Contains(t, e.store.outbox, service.EventOrderReturnOpened+":"+ret.ID)
}

// TestAWithdrawnReturnSaysItOnTheBus: withdrawing a return publishes one event
// naming the order, the return and the moment the withdrawal was stamped, and
// writes the same event into the outbox.
func TestAWithdrawnReturnSaysItOnTheBus(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, ret := openedReturn(t, e)

	withdrawn, err := e.svc.CancelReturn(ctx, ret.ID)
	require.NoError(t, err)
	require.NotNil(t, withdrawn.CanceledAt)

	published := e.bus.eventsNamed(service.EventOrderReturnCanceled)
	require.Len(t, published, 1, "one event, published after the commit")
	assert.Equal(t, service.EventOrderReturnCanceled+":"+ret.ID, published[0].ID)
	assert.Equal(t, map[string]any{
		service.EventFieldOrderID:    order.ID,
		service.EventFieldReturnID:   ret.ID,
		service.EventFieldCanceledAt: withdrawn.CanceledAt.UTC().Format(time.RFC3339Nano),
	}, published[0].Data, "identities and the moment the record holds")

	row, ok := e.store.outbox[published[0].ID]
	require.True(t, ok, "the event is promised in the withdrawal's transaction")
	assert.Equal(t, service.EventOrderReturnCanceled, row.Name)
	assert.Equal(t, published[0].Data, row.Data)
}

// TestASecondWithdrawalSaysNothingMore: the second call is a no-op, and the
// return still said it once.
func TestASecondWithdrawalSaysNothingMore(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	_, ret := openedReturn(t, e)

	_, err := e.svc.CancelReturn(ctx, ret.ID)
	require.NoError(t, err)
	again, err := e.svc.CancelReturn(ctx, ret.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ReturnCanceled, again.Status)

	assert.Len(t, e.bus.eventsNamed(service.EventOrderReturnCanceled), 1)
	assert.Len(t, outboxRowsNamed(e, service.EventOrderReturnCanceled), 1)
}

// TestARefusedWithdrawalSaysNothing: a received return, and a return a live
// exchange takes back, are not withdrawn, and nothing says they were.
func TestARefusedWithdrawalSaysNothing(t *testing.T) {
	t.Run("received", func(t *testing.T) {
		ctx := context.Background()
		e := newEnv(t)
		_, ret := openedReturn(t, e)
		_, err := e.svc.ReceiveReturn(ctx, ret.ID, "sloc_1")
		require.NoError(t, err)

		_, err = e.svc.CancelReturn(ctx, ret.ID)

		require.Error(t, err)
		assert.Equal(t, errors.KindConflict, errors.KindOf(err))
		assert.Equal(t, service.CodeAfterSalesTransition, errors.CodeOf(err))
		assert.Empty(t, e.bus.eventsNamed(service.EventOrderReturnCanceled))
		assert.Empty(t, outboxRowsNamed(e, service.EventOrderReturnCanceled))
	})

	t.Run("named by an exchange", func(t *testing.T) {
		ctx := context.Background()
		e := newPricedEnv(t, jacketQuote)
		order, _, _, ret := pricedSale(t, e.env, 1)
		_, err := e.svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: order.ID, ReturnID: ret.ID})
		require.NoError(t, err)

		_, err = e.svc.CancelReturn(ctx, ret.ID)

		require.Error(t, err)
		assert.Equal(t, errors.KindConflict, errors.KindOf(err))
		assert.Equal(t, service.CodeReturnNamedByExchange, errors.CodeOf(err))
		assert.Empty(t, e.bus.eventsNamed(service.EventOrderReturnCanceled))
		assert.Empty(t, outboxRowsNamed(e.env, service.EventOrderReturnCanceled))
	})
}

// TestAWithdrawalWhoseEventCannotBeWrittenIsNotMade: the outbox refusing the
// row fails the withdrawal, and the return stays requested.
func TestAWithdrawalWhoseEventCannotBeWrittenIsNotMade(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	_, ret := openedReturn(t, e)
	e.store.outboxErr = errors.New("the outbox is unreachable")

	_, err := e.svc.CancelReturn(ctx, ret.ID)
	require.Error(t, err)

	current, err := e.svc.GetReturn(ctx, ret.ID)
	require.NoError(t, err)
	assert.Equal(t, models.ReturnRequested, current.Status, "no promise, no withdrawal")
	assert.Nil(t, current.CanceledAt)
	assert.Empty(t, e.bus.eventsNamed(service.EventOrderReturnCanceled))
}

// TestReceivingAReturnSaysNothing: a return's receipt is not published (ADR
// 0437); its refund is the payment module's payment.refunded.
func TestReceivingAReturnSaysNothing(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	_, ret := openedReturn(t, e)
	eventsBefore := len(e.bus.events())
	outboxBefore := len(e.store.outboxEvents())

	_, err := e.svc.ReceiveReturn(ctx, ret.ID, "sloc_1")
	require.NoError(t, err)

	assert.Len(t, e.bus.events(), eventsBefore, "receiving publishes nothing")
	assert.Len(t, e.store.outboxEvents(), outboxBefore, "receiving promises nothing")
}

// outboxRowsNamed answers the fake outbox's rows of one topic.
func outboxRowsNamed(e env, name string) []string {
	var ids []string
	for _, row := range e.store.outboxEvents() {
		if row.Name == name {
			ids = append(ids, row.ID)
		}
	}

	return ids
}
