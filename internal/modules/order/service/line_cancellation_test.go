package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestALineCanBeWrittenOffInPart is the decision in one assertion.
//
// The base order is one line of three units at 1000 each. One unit is written
// off; what was sold is unchanged and the record says why.
func TestALineCanBeWrittenOffInPart(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)

	cancellation, err := e.svc.CancelOrderLine(ctx, order.ID, service.CancelOrderLineInput{
		OrderLineItemID: lineID, Quantity: 1, Reason: "out of stock",
	})
	require.NoError(t, err)

	assert.Equal(t, lineID, cancellation.OrderLineItemID)
	assert.Equal(t, int64(1), cancellation.Quantity)
	assert.Equal(t, "out of stock", cancellation.Reason)

	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, order.Total, detail.Total,
		"the order's total says what was SOLD and goes on saying it")
	assert.Equal(t, models.OrderPending, detail.Status,
		"writing off a line does not close the order; that is a separate act")
	require.Len(t, detail.Items, 1)
	assert.Equal(t, int64(3), detail.Items[0].Quantity,
		"the line's quantity is the cart's snapshot and does not move either")
}

// TestMoreCannotBeWrittenOffThanTheLineHas is the ceiling.
func TestMoreCannotBeWrittenOffThanTheLineHas(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)

	_, err := e.svc.CancelOrderLine(ctx, order.ID, service.CancelOrderLineInput{
		OrderLineItemID: lineID, Quantity: 4, Reason: "all of it and one more",
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeCancelQuantityExceeded, errors.CodeOf(err))
}

// TestTwoCancellationsTogetherCannotExceedTheLine is the half a per-record check
// would miss; it is why the sum is read under the order's lock.
func TestTwoCancellationsTogetherCannotExceedTheLine(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)

	_, err := e.svc.CancelOrderLine(ctx, order.ID, service.CancelOrderLineInput{
		OrderLineItemID: lineID, Quantity: 2, Reason: "damaged",
	})
	require.NoError(t, err)

	_, err = e.svc.CancelOrderLine(ctx, order.ID, service.CancelOrderLineInput{
		OrderLineItemID: lineID, Quantity: 2, Reason: "damaged too",
	})

	require.Error(t, err)
	assert.Equal(t, service.CodeCancelQuantityExceeded, errors.CodeOf(err))
}

// TestAUnitAlreadyAskedBackCannotBeWrittenOff is one direction of the shared
// ceiling.
//
// Two units of three are coming back. Writing off two more would leave the line
// owing four units of a three-unit line, and the goods arriving at the warehouse
// would disagree with the record by a quantity nobody could account for.
func TestAUnitAlreadyAskedBackCannotBeWrittenOff(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)

	_, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: order.ID,
		Lines:   []service.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 2}},
	})
	require.NoError(t, err)

	_, err = e.svc.CancelOrderLine(ctx, order.ID, service.CancelOrderLineInput{
		OrderLineItemID: lineID, Quantity: 2, Reason: "out of stock",
	})
	require.Error(t, err)
	assert.Equal(t, service.CodeCancelQuantityExceeded, errors.CodeOf(err))

	_, err = e.svc.CancelOrderLine(ctx, order.ID, service.CancelOrderLineInput{
		OrderLineItemID: lineID, Quantity: 1, Reason: "out of stock",
	})
	require.NoError(t, err, "the one unit nobody spoke for is still writable off")
}

// TestAWrittenOffUnitCannotBeAskedBack is the SAME ceiling from the other side,
// and it is the reason the cancellation record has a consumer on its first day.
func TestAWrittenOffUnitCannotBeAskedBack(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)

	_, err := e.svc.CancelOrderLine(ctx, order.ID, service.CancelOrderLineInput{
		OrderLineItemID: lineID, Quantity: 2, Reason: "out of stock",
	})
	require.NoError(t, err)

	_, err = e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: order.ID,
		Lines:   []service.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 2}},
	})
	require.Error(t, err, "goods that will never arrive cannot come back")
	assert.Equal(t, service.CodeReturnQuantityExceeded, errors.CodeOf(err))

	_, err = e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: order.ID,
		Lines:   []service.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 1}},
	})
	require.NoError(t, err, "the delivered unit can")
}

// TestALineOfAnotherOrderCannotBeWrittenOff keeps the record inside the order it
// names.
func TestALineOfAnotherOrderCannotBeWrittenOff(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, _ := returnedOrder(t, e)
	other, otherLineID := returnedOrder(t, e)
	require.NotEqual(t, order.ID, other.ID)

	_, err := e.svc.CancelOrderLine(ctx, order.ID, service.CancelOrderLineInput{
		OrderLineItemID: otherLineID, Quantity: 1, Reason: "wrong order",
	})

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, service.CodeCancelLineUnknown, errors.CodeOf(err))
}

// TestAWriteOffNeedsAReason refuses the unexplained one, including the one made
// of spaces.
func TestAWriteOffNeedsAReason(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)

	for name, reason := range map[string]string{"empty": "", "spaces": "   "} {
		t.Run(name, func(t *testing.T) {
			_, err := e.svc.CancelOrderLine(ctx, order.ID, service.CancelOrderLineInput{
				OrderLineItemID: lineID, Quantity: 1, Reason: reason,
			})

			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err))
		})
	}
}

// TestNothingIsWrittenOffOnACanceledOrder keeps the act on live orders.
func TestNothingIsWrittenOffOnACanceledOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)

	require.NoError(t, e.svc.CancelOrder(ctx, order.ID, "the customer changed their mind"))

	_, err := e.svc.CancelOrderLine(ctx, order.ID, service.CancelOrderLineInput{
		OrderLineItemID: lineID, Quantity: 1, Reason: "out of stock",
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}

// TestAZeroOrNegativeWriteOffIsNotAnAct refuses the shapes that are not one.
func TestAZeroOrNegativeWriteOffIsNotAnAct(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)

	for name, quantity := range map[string]int64{"zero": 0, "negative": -1} {
		t.Run(name, func(t *testing.T) {
			_, err := e.svc.CancelOrderLine(ctx, order.ID, service.CancelOrderLineInput{
				OrderLineItemID: lineID, Quantity: quantity, Reason: "out of stock",
			})

			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err))
		})
	}
}

// TestTheCancellationsAreListedOldestFirst pins the order of the listing.
func TestTheCancellationsAreListedOldestFirst(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)

	for _, reason := range []string{"first", "second", "third"} {
		_, err := e.svc.CancelOrderLine(ctx, order.ID, service.CancelOrderLineInput{
			OrderLineItemID: lineID, Quantity: 1, Reason: reason,
		})
		require.NoError(t, err)
	}

	list, err := e.svc.ListLineCancellations(ctx, order.ID)
	require.NoError(t, err)

	require.Len(t, list, 3)
	assert.Equal(t, []string{"first", "second", "third"},
		[]string{list[0].Reason, list[1].Reason, list[2].Reason})
}

// TestAnotherOrdersCancellationsAreNotListed keeps the join honest: the listing
// reaches the order through the LINE, and a fake that ignored that would answer
// with the whole table.
func TestAnotherOrdersCancellationsAreNotListed(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)
	other, otherLineID := returnedOrder(t, e)

	_, err := e.svc.CancelOrderLine(ctx, order.ID, service.CancelOrderLineInput{
		OrderLineItemID: lineID, Quantity: 1, Reason: "ours",
	})
	require.NoError(t, err)
	_, err = e.svc.CancelOrderLine(ctx, other.ID, service.CancelOrderLineInput{
		OrderLineItemID: otherLineID, Quantity: 1, Reason: "theirs",
	})
	require.NoError(t, err)

	list, err := e.svc.ListLineCancellations(ctx, order.ID)
	require.NoError(t, err)

	require.Len(t, list, 1)
	assert.Equal(t, "ours", list[0].Reason)
}

// TestAWriteOffSAYSSoOnTheBus is the half of the act this module can do.
//
// It cannot put the stock back — the units live in another module (ADR 0006) — and
// it cannot ask how many already shipped either. What it can do is say what
// happened, with everything a listener needs that will not change afterwards
// (ADR 0134).
func TestAWriteOffSAYSSoOnTheBus(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)

	cancellation, err := e.svc.CancelOrderLine(ctx, order.ID, service.CancelOrderLineInput{
		OrderLineItemID: lineID, Quantity: 1, Reason: "out of stock",
	})
	require.NoError(t, err)

	published := e.bus.eventsNamed(service.EventOrderLineCanceled)
	require.Len(t, published, 1, "one write-off, one event")

	data := published[0].Data
	assert.Equal(t, cancellation.ID, data[service.EventFieldCancellationID],
		"the cancellation's id is what makes putting the stock back idempotent")
	assert.Equal(t, order.ID, data[service.EventFieldOrderID])
	assert.Equal(t, lineID, data[service.EventFieldOrderLineItemID])
	assert.Equal(t, testVariantID, data[service.EventFieldVariantID],
		"the variant is how a listener reaches the inventory item without reading the order")
	assert.Equal(t, "1", data[service.EventFieldCanceledQuantity],
		"every count travels as a decimal STRING: JSON has one number type")
	assert.Equal(t, "3", data[service.EventFieldBoughtQuantity])

	assert.Equal(t, service.EventOrderLineCanceled+":"+cancellation.ID, published[0].ID,
		"the event's id comes from the ROW, not the order: a line is written off many "+
			"times and the outbox drops a duplicate id without a word")
}

// TestASecondWriteOffSaysWhereItSitsInTheLINE is what keeps two cancellations from
// putting the same units back twice.
//
// The listener caps what it returns by `bought - shipped`, and a second write-off
// must not re-return what the first did. So the event carries where this row starts
// rather than only how big it is.
func TestASecondWriteOffSaysWhereItSitsInTheLINE(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)

	for range 2 {
		_, err := e.svc.CancelOrderLine(ctx, order.ID, service.CancelOrderLineInput{
			OrderLineItemID: lineID, Quantity: 1, Reason: "out of stock",
		})
		require.NoError(t, err)
	}

	published := e.bus.eventsNamed(service.EventOrderLineCanceled)
	require.Len(t, published, 2)
	assert.Equal(t, "0", published[0].Data[service.EventFieldCanceledBefore])
	assert.Equal(t, "1", published[1].Data[service.EventFieldCanceledBefore],
		"the second write-off starts where the first ended; a listener that read only "+
			"the quantity would give back the same unit twice")
}

// TestTheCANCELLATIONAndItsPromiseCommitTogether is why the outbox row is written
// inside the transaction.
//
// A write-off recorded with no event is stock that stays deducted forever with
// nothing anywhere saying it should not be. That is the state the outbox exists to
// prevent, so a failure to write the row fails the write-off.
func TestTheCANCELLATIONAndItsPromiseCommitTogether(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)
	e.store.outboxErr = errors.New("the outbox is unreachable")

	_, err := e.svc.CancelOrderLine(ctx, order.ID, service.CancelOrderLineInput{
		OrderLineItemID: lineID, Quantity: 1, Reason: "out of stock",
	})
	require.Error(t, err, "no promise, no write-off")

	cancellations, listErr := e.svc.ListLineCancellations(ctx, order.ID)
	require.NoError(t, listErr)
	assert.Empty(t, cancellations, "and the row rolled back with it")
}

// TestTheDispatchableLinesSayWhatWasWrittenOff is the producer's side of the bound.
//
// A parcel may not hold more than the order owes, and what it owes is what was sold
// minus what was written off (ADR 0135). The flow that computes the bound reads this
// surface, so a zero here would let a canceled unit ship with every gate green —
// which is exactly what happened before the surface existed.
func TestTheDispatchableLinesSayWhatWasWrittenOff(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)

	_, err := e.svc.CancelOrderLine(ctx, order.ID, service.CancelOrderLineInput{
		OrderLineItemID: lineID, Quantity: 2, Reason: "out of stock",
	})
	require.NoError(t, err)

	raw, err := service.NewInterop(e.svc).DispatchableLinesJSON(ctx, order.ID)
	require.NoError(t, err)

	var lines []struct {
		LineItemID string `json:"line_item_id"`
		Bought     int64  `json:"bought"`
		Canceled   int64  `json:"canceled"`
	}
	require.NoError(t, json.Unmarshal(raw, &lines))

	require.Len(t, lines, 1)
	assert.Equal(t, lineID, lines[0].LineItemID)
	assert.Equal(t, int64(3), lines[0].Bought, "the line's quantity is the cart's snapshot")
	assert.Equal(t, int64(2), lines[0].Canceled,
		"and the write-off is reported, so a parcel is bounded by one unit")
}

// TestTheDispatchableLinesCountNoRETURNS keeps the two sums apart.
//
// A returned unit shipped, came back and was restocked on receipt. Subtracting it
// here would bound a parcel by goods that already left once, which is a new decision
// rather than an outstanding quantity — and the cancellation CEILING does count both,
// so the two sums genuinely differ.
//
// # The fixture has to have a real return
//
// The first version of this leaned on the name of the shared `returnedOrder` helper,
// which despite it creates NO return — so the test asserted zero on an order with
// neither a return nor a cancellation and could not tell the two sums apart. A
// mutation swapping this surface for the returns-inclusive sum survived it. Found by
// that mutation, which is what mutations are for.
func TestTheDispatchableLinesCountNoRETURNS(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)

	_, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID:      order.ID,
		RefundAmount: 1200,
		Lines: []service.ReturnLineInput{
			{OrderLineItemID: lineID, Quantity: 2, RefundAmount: 1200},
		},
	})
	require.NoError(t, err)

	raw, err := service.NewInterop(e.svc).DispatchableLinesJSON(ctx, order.ID)
	require.NoError(t, err)

	var lines []struct {
		Canceled int64 `json:"canceled"`
	}
	require.NoError(t, json.Unmarshal(raw, &lines))
	require.Len(t, lines, 1)
	assert.Zero(t, lines[0].Canceled,
		"two units of this line are on a RETURN and none is canceled; this surface "+
			"reports write-offs only, because a returned unit already shipped")
}
