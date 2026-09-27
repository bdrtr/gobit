package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// cardOrder places an order of the valid input's line and a line of two gift
// cards, and returns the order's id and the two lines.
func cardOrder(t *testing.T, e env) (orderID, cardLine, otherLine string) {
	t.Helper()

	input := validInput()
	input.Items = append(input.Items, service.CreateOrderItemInput{
		VariantID: secondVariantID, Title: "Gift card", Quantity: 2, UnitPrice: 2_500,
		Subtotal: 5_000, Total: 5_000, IsGiftcard: true,
	})
	input.Subtotal += 5_000
	input.Total += 5_000
	order, err := e.svc.CreateOrder(context.Background(), input)
	require.NoError(t, err)
	detail, err := e.svc.GetOrder(context.Background(), order.ID)
	require.NoError(t, err)
	for i := range detail.Items {
		if detail.Items[i].IsGiftcard {
			cardLine = detail.Items[i].ID
		} else {
			otherLine = detail.Items[i].ID
		}
	}
	require.NotEmpty(t, cardLine)
	require.NotEmpty(t, otherLine)

	return order.ID, cardLine, otherLine
}

// TestAGiftCardLineCannotBeReturned is ADR 0213: the cards were mailed, so a
// return naming their line is refused, and one naming only the other line is
// not.
func TestAGiftCardLineCannotBeReturned(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	orderID, cardLine, otherLine := cardOrder(t, e)

	_, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: orderID,
		Lines: []service.ReturnLineInput{
			{OrderLineItemID: otherLine, Quantity: 1},
			{OrderLineItemID: cardLine, Quantity: 1},
		},
	})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeGiftCardLineFinal, errors.CodeOf(err))

	returns, total, err := e.svc.ListReturns(ctx, orderID, service.Page{})
	require.NoError(t, err)
	assert.Zero(t, total, "the refused return left nothing behind")
	assert.Empty(t, returns)

	_, err = e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: orderID, Lines: []service.ReturnLineInput{{OrderLineItemID: otherLine, Quantity: 1}},
	})
	require.NoError(t, err, "the order's other line is returned as before")
}

// TestAGiftCardLineCannotBeWrittenOff: the sale flow issues a card for every
// unit bought, so a unit written off would still be issued.
func TestAGiftCardLineCannotBeWrittenOff(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	orderID, cardLine, otherLine := cardOrder(t, e)

	_, err := e.svc.CancelOrderLine(ctx, orderID, service.CancelOrderLineInput{
		OrderLineItemID: cardLine, Quantity: 1, Reason: "out of stock",
	})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeGiftCardLineFinal, errors.CodeOf(err))

	_, err = e.svc.CancelOrderLine(ctx, orderID, service.CancelOrderLineInput{
		OrderLineItemID: otherLine, Quantity: 1, Reason: "out of stock",
	})
	require.NoError(t, err, "the order's other line is written off as before")
}
