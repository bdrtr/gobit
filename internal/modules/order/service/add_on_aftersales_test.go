package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// engravedOrder places withEngraving's order and returns it with the ring's
// and the engraving's line ids: three rings, each engraved.
func engravedOrder(t *testing.T, e env) (orderID, ring, engraving string) {
	t.Helper()
	ctx := context.Background()
	order, err := e.svc.CreateOrder(ctx, withEngraving("li_ring"))
	require.NoError(t, err)
	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	for i := range detail.Items {
		if detail.Items[i].ParentLineItemID == nil {
			ring = detail.Items[i].ID
		} else {
			engraving = detail.Items[i].ID
		}
	}
	require.NotEmpty(t, ring)
	require.NotEmpty(t, engraving)
	return order.ID, ring, engraving
}

// TestAReturnTakesAnAddOnWithItsLine is ADR 0230 on a return: the ring comes
// back only with its engraving at its quantity, the engraving only with its
// ring, and what each refunds is the operator's, nothing included.
func TestAReturnTakesAnAddOnWithItsLine(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	orderID, ring, engraving := engravedOrder(t, e)

	for name, lines := range map[string][]service.ReturnLineInput{
		"the ring alone":      {{OrderLineItemID: ring, Quantity: 1}},
		"the engraving alone": {{OrderLineItemID: engraving, Quantity: 1}},
		"another quantity": {
			{OrderLineItemID: ring, Quantity: 1}, {OrderLineItemID: engraving, Quantity: 2},
		},
	} {
		_, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{OrderID: orderID, Lines: lines})
		require.Error(t, err, name)
		assert.Equal(t, service.CodeAddOnFollows, errors.CodeOf(err), name)
	}

	created, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: orderID, RefundAmount: 1000,
		Lines: []service.ReturnLineInput{
			{OrderLineItemID: ring, Quantity: 1, RefundAmount: 1000},
			{OrderLineItemID: engraving, Quantity: 1},
		},
	})
	require.NoError(t, err, "the ring comes back with its engraving, which refunds nothing")
	assert.Len(t, created.Items, 2)
}

// TestAWriteOffTakesItsAddOns is ADR 0230 on a write-off: two rings written off
// write off two engravings in the same act, each with its own record and
// event; the engraving is not written off alone, and a write-off past what is
// left writes nothing.
func TestAWriteOffTakesItsAddOns(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	orderID, ring, engraving := engravedOrder(t, e)

	_, err := e.svc.CancelOrderLine(ctx, orderID, service.CancelOrderLineInput{
		OrderLineItemID: engraving, Quantity: 1, Reason: "no longer wanted",
	})
	assert.Equal(t, service.CodeAddOnFollows, errors.CodeOf(err))

	cancellation, err := e.svc.CancelOrderLine(ctx, orderID, service.CancelOrderLineInput{
		OrderLineItemID: ring, Quantity: 2, Reason: "out of stock",
	})
	require.NoError(t, err)
	assert.Equal(t, ring, cancellation.OrderLineItemID, "the answer is the line asked for")

	recorded, err := e.svc.ListLineCancellations(ctx, orderID)
	require.NoError(t, err)
	byLine := map[string]int64{}
	for _, c := range recorded {
		byLine[c.OrderLineItemID] += c.Quantity
		assert.Equal(t, "out of stock", c.Reason)
	}
	assert.Equal(t, map[string]int64{ring: 2, engraving: 2}, byLine)
	assert.Len(t, e.bus.eventsNamed(service.EventOrderLineCanceled), 2, "each write-off says so")

	_, err = e.svc.CancelOrderLine(ctx, orderID, service.CancelOrderLineInput{
		OrderLineItemID: ring, Quantity: 2, Reason: "out of stock",
	})
	assert.Equal(t, service.CodeCancelQuantityExceeded, errors.CodeOf(err))
	recorded, err = e.svc.ListLineCancellations(ctx, orderID)
	require.NoError(t, err)
	assert.Len(t, recorded, 2, "a refused write-off writes nothing, its add-ons included")
}
