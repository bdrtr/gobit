package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// returnedOrder places an order and returns it with the id of its single line,
// which carries a quantity of three.
func returnedOrder(t *testing.T, e env) (order models.Order, lineID string) {
	t.Helper()

	order, err := e.svc.CreateOrder(context.Background(), validInput())
	require.NoError(t, err)

	detail, err := e.svc.GetOrder(context.Background(), order.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)

	return order, detail.Items[0].ID
}

// TestAReturnCarriesTheLinesComingBack is what the record could not say before.
//
// A return used to be an amount and a note. Nothing recorded WHICH goods were
// coming back, so nothing downstream could restock them.
func TestAReturnCarriesTheLinesComingBack(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)

	ret, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID:      order.ID,
		RefundAmount: 1200,
		Lines: []service.ReturnLineInput{
			{OrderLineItemID: lineID, Quantity: 2, RefundAmount: 1200},
		},
	})
	require.NoError(t, err)

	require.Len(t, ret.Items, 1)
	assert.Equal(t, lineID, ret.Items[0].OrderLineItemID)
	assert.Equal(t, int64(2), ret.Items[0].Quantity)
	assert.Equal(t, int64(1200), ret.Items[0].RefundAmount)
	assert.Equal(t, ret.ID, ret.Items[0].ReturnID)
}

// TestMoreCannotBeReturnedThanWasBought holds the rule that spans rows.
func TestMoreCannotBeReturnedThanWasBought(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)
	orderID := order.ID

	_, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: orderID,
		Lines:   []service.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 4}},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeReturnQuantityExceeded, errors.CodeOf(err))
}

// TestTwoReturnsTogetherCannotExceedTheLine is the half a single-record check
// would miss.
//
// Two open requests could each ask back a valid amount and together ask back
// more than was bought, so the rule has to read every OTHER live return.
func TestTwoReturnsTogetherCannotExceedTheLine(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)
	orderID := order.ID

	_, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: orderID,
		Lines:   []service.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 2}},
	})
	require.NoError(t, err)

	_, err = e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: orderID,
		Lines:   []service.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 2}},
	})

	require.Error(t, err)
	assert.Equal(t, service.CodeReturnQuantityExceeded, errors.CodeOf(err))
}

// TestACanceledReturnReleasesItsUnits is the other side of the same sum.
//
// Withdrawing a request gives the units back to the line; counting a canceled
// return would freeze goods nobody is asking for.
func TestACanceledReturnReleasesItsUnits(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)
	orderID := order.ID

	first, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: orderID,
		Lines:   []service.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 3}},
	})
	require.NoError(t, err)

	_, err = e.svc.CancelReturn(ctx, first.ID)
	require.NoError(t, err)

	_, err = e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: orderID,
		Lines:   []service.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 3}},
	})
	assert.NoError(t, err, "a withdrawn request must give its units back")
}

// TestALineThatIsNotOnTheOrderIsRefused stops a return pointing somewhere else.
func TestALineThatIsNotOnTheOrderIsRefused(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, _ := returnedOrder(t, e)

	_, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: order.ID,
		Lines:   []service.ReturnLineInput{{OrderLineItemID: "oli_somebody_elses", Quantity: 1}},
	})

	require.Error(t, err)
	assert.Equal(t, service.CodeReturnLineUnknown, errors.CodeOf(err))
}

// TestTheSameLineCannotAppearTwiceInOneReturn keeps the quantity the single
// answer to "how many".
func TestTheSameLineCannotAppearTwiceInOneReturn(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)

	_, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: order.ID,
		Lines: []service.ReturnLineInput{
			{OrderLineItemID: lineID, Quantity: 1},
			{OrderLineItemID: lineID, Quantity: 1},
		},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Contains(t, err.Error(), "twice")
}

// TestAReturnCanBeReceived is the transition the record could not make at all.
func TestAReturnCanBeReceived(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)

	ret, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: order.ID,
		Lines:   []service.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 1}},
	})
	require.NoError(t, err)
	require.Equal(t, models.ReturnRequested, ret.Status)

	received, err := e.svc.ReceiveReturn(ctx, ret.ID)
	require.NoError(t, err)

	assert.Equal(t, models.ReturnReceived, received.Status)
	require.NotNil(t, received.ReceivedAt)
}

// TestASecondReceiveKeepsTheFirstMoment is why the no-op is not a re-write.
//
// received_at is the moment the goods arrived. Stamping it again would make the
// record claim they arrived when somebody clicked a second time.
func TestASecondReceiveKeepsTheFirstMoment(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)

	ret, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: order.ID,
		Lines:   []service.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 1}},
	})
	require.NoError(t, err)

	first, err := e.svc.ReceiveReturn(ctx, ret.ID)
	require.NoError(t, err)
	second, err := e.svc.ReceiveReturn(ctx, ret.ID)
	require.NoError(t, err, "a second receive is a no-op, not a conflict")

	require.NotNil(t, second.ReceivedAt)
	assert.Equal(t, *first.ReceivedAt, *second.ReceivedAt,
		"the record must keep the moment the goods ACTUALLY arrived")
}

// TestAReceivedReturnCannotBeWithdrawn is the entry in the table that carries
// weight: the goods are physically here.
func TestAReceivedReturnCannotBeWithdrawn(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)

	ret, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: order.ID,
		Lines:   []service.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 1}},
	})
	require.NoError(t, err)
	_, err = e.svc.ReceiveReturn(ctx, ret.ID)
	require.NoError(t, err)

	_, err = e.svc.CancelReturn(ctx, ret.ID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeAfterSalesTransition, errors.CodeOf(err))
}

// TestACanceledReturnCannotBeReceived closes the other direction.
func TestACanceledReturnCannotBeReceived(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)

	ret, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: order.ID,
		Lines:   []service.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 1}},
	})
	require.NoError(t, err)
	_, err = e.svc.CancelReturn(ctx, ret.ID)
	require.NoError(t, err)

	_, err = e.svc.ReceiveReturn(ctx, ret.ID)

	require.Error(t, err)
	assert.Equal(t, service.CodeAfterSalesTransition, errors.CodeOf(err))
}

// TestATransitionTakesTheReturnsLock proves the read and the write cannot be
// split by a second operator.
func TestATransitionTakesTheReturnsLock(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)

	ret, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: order.ID,
		Lines:   []service.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 1}},
	})
	require.NoError(t, err)

	_, err = e.svc.ReceiveReturn(ctx, ret.ID)
	require.NoError(t, err)

	assert.Contains(t, e.store.lockedReturns, ret.ID,
		"a transition that did not lock could be split by a second operator")
}
