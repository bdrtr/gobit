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

// An order's end is announced as its start is (ADR 0288).

// TestBothCancelsSayItOnTheBus: the checkout's compensation and the shop's
// cancel each publish one event naming the order and the moment, and write the
// same event into the outbox.
func TestBothCancelsSayItOnTheBus(t *testing.T) {
	for name, cancel := range map[string]func(e env, orderID string) error{
		"the compensation": func(e env, orderID string) error {
			return e.svc.CancelOrder(context.Background(), orderID, "the payment was declined")
		},
		"the shop's cancel": func(e env, orderID string) error {
			return e.svc.CancelPlacedOrder(context.Background(), orderID, "the transfer never came")
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			e := newEnv(t)
			order, err := e.svc.CreateOrder(ctx, validInput())
			require.NoError(t, err)

			require.NoError(t, cancel(e, order.ID))

			canceled, err := e.svc.GetOrder(ctx, order.ID)
			require.NoError(t, err)
			require.NotNil(t, canceled.CanceledAt)
			published := e.bus.eventsNamed(service.EventOrderCanceled)
			require.Len(t, published, 1)
			assert.Equal(t, service.EventOrderCanceled+":"+order.ID, published[0].ID)
			assert.Equal(t, map[string]any{
				service.EventFieldOrderID:    order.ID,
				service.EventFieldCanceledAt: canceled.CanceledAt.UTC().Format(time.RFC3339Nano),
			}, published[0].Data, "the order and the moment, and not the operator's reason")

			row, ok := e.store.outbox[published[0].ID]
			require.True(t, ok, "the event is promised in the cancel's transaction")
			assert.Equal(t, service.EventOrderCanceled, row.Name)
			assert.Equal(t, published[0].Data, row.Data)
		})
	}
}

// TestASecondCancelSaysNothing: a cancel is terminal, and the second call
// writes nothing.
func TestASecondCancelSaysNothing(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	require.NoError(t, e.svc.CancelOrder(ctx, order.ID, ""))
	require.NoError(t, e.svc.CancelPlacedOrder(ctx, order.ID, ""))

	assert.Len(t, e.bus.eventsNamed(service.EventOrderCanceled), 1)
}

// TestARefusedCancelSaysNothing: a completed order is not canceled, and nothing
// says it was.
func TestARefusedCancelSaysNothing(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	_, err = e.svc.CompleteOrder(ctx, order.ID)
	require.NoError(t, err)

	require.Error(t, e.svc.CancelPlacedOrder(ctx, order.ID, ""))

	assert.Empty(t, e.bus.eventsNamed(service.EventOrderCanceled))
	assert.NotContains(t, e.store.outbox, service.EventOrderCanceled+":"+order.ID)
}

// TestTheCancelAndItsEventCommitTogether: a cancel whose event cannot be
// promised is not made, or what the order's payment holds would stay
// authorized with nothing saying it should not.
func TestTheCancelAndItsEventCommitTogether(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	e.store.outboxErr = errors.New("the outbox is unreachable")

	require.Error(t, e.svc.CancelOrder(ctx, order.ID, ""))

	current, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderPending, current.Status, "no promise, no cancel")
	assert.Empty(t, e.bus.eventsNamed(service.EventOrderCanceled))
}
