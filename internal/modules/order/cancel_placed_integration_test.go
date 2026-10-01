//go:build integration

package order_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// writeOffs reads, per line, the units the order's cancellations wrote off and
// how many order.line_canceled events the outbox holds for the order.
func writeOffs(ctx context.Context, t *testing.T, orderID string) (units map[string]int64, events int) {
	t.Helper()

	rows, err := testPool.Pool().Query(ctx, `
        SELECT c.order_line_item_id, sum(c.quantity)
        FROM order_line_cancellations c
        JOIN order_line_items l ON l.id = c.order_line_item_id
        WHERE l.order_id = $1
        GROUP BY c.order_line_item_id`, orderID)
	require.NoError(t, err)
	defer rows.Close()
	units = map[string]int64{}
	for rows.Next() {
		var lineID string
		var quantity int64
		require.NoError(t, rows.Scan(&lineID, &quantity))
		units[lineID] = quantity
	}
	require.NoError(t, rows.Err())

	require.NoError(t, testPool.Pool().QueryRow(ctx, `
        SELECT count(*) FROM event_outbox
        WHERE name = $1 AND data->>'order_id' = $2`,
		service.EventOrderLineCanceled, orderID).Scan(&events))

	return units, events
}

// TestTheShopsCancelWritesOffWhatIsLeft is ADR 0285 over the real schema: the
// shop's cancel of a placed order writes off each line's units that are not
// yet written off, with an event for each, leaves a gift card line as it is,
// and a second cancel writes nothing.
func TestTheShopsCancelWritesOffWhatIsLeft(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	in := validInput()
	in.Subtotal += 5_000
	in.Total += 5_000
	in.Items = append(in.Items, service.CreateOrderItemInput{
		VariantID: "variant_card", Title: "Gift card", Quantity: 1,
		UnitPrice: 5_000, Subtotal: 5_000, Total: 5_000, IsGiftcard: true,
	})
	ord, err := svc.CreateOrder(ctx, in)
	require.NoError(t, err)
	detail, err := svc.GetOrder(ctx, ord.ID)
	require.NoError(t, err)
	byVariant := map[string]models.OrderLineItem{}
	for _, item := range detail.Items {
		byVariant[item.VariantID] = item
	}
	shirt, card := byVariant["variant_A"], byVariant["variant_card"]
	require.Equal(t, int64(3), shirt.Quantity)

	_, err = svc.CancelOrderLine(ctx, ord.ID, service.CancelOrderLineInput{
		OrderLineItemID: shirt.ID, Quantity: 1, Reason: "out of stock",
	})
	require.NoError(t, err)

	require.NoError(t, svc.CancelPlacedOrder(ctx, ord.ID, "the transfer never came"))

	canceled, err := svc.GetOrder(ctx, ord.ID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderCanceled, canceled.Status)
	units, events := writeOffs(ctx, t, ord.ID)
	assert.Equal(t, map[string]int64{shirt.ID: 3}, units,
		"the two units left are written off beside the one already written off, and the card line not at all")
	assert.Equal(t, 2, events, "each write-off has its event")
	_, cardWrittenOff := units[card.ID]
	assert.False(t, cardWrittenOff)

	listed, err := svc.ListLineCancellations(ctx, ord.ID)
	require.NoError(t, err)
	require.Len(t, listed, 2)
	assert.Equal(t, int64(2), listed[1].Quantity)
	assert.Equal(t, "the transfer never came", listed[1].Reason)

	// The cancel's event says where its row sits among the line's write-offs, so
	// the restock flow does not give back the unit the first one already did.
	var before, bought string
	require.NoError(t, testPool.Pool().QueryRow(ctx, `
        SELECT data->>'canceled_before', data->>'bought_quantity' FROM event_outbox
        WHERE name = $1 AND data->>'cancellation_id' = $2`,
		service.EventOrderLineCanceled, listed[1].ID).Scan(&before, &bought))
	assert.Equal(t, "1", before)
	assert.Equal(t, "3", bought)

	require.NoError(t, svc.CancelPlacedOrder(ctx, ord.ID, "again"))
	units, events = writeOffs(ctx, t, ord.ID)
	assert.Equal(t, map[string]int64{shirt.ID: 3}, units, "a second cancel writes nothing")
	assert.Equal(t, 2, events)
}

// TestTheShopsCancelWithoutAReasonNamesTheCancel: a write-off always carries a
// reason, and a cancel that gave none gives its own.
func TestTheShopsCancelWithoutAReasonNamesTheCancel(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	require.NoError(t, svc.CancelPlacedOrder(ctx, ord.ID, "  "))

	listed, err := svc.ListLineCancellations(ctx, ord.ID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, int64(3), listed[0].Quantity)
	assert.Equal(t, "order canceled", listed[0].Reason)
}

// TestTheSagasCancelWritesNothingOff: the checkout's compensation cancels an
// order whose stock is still reserved, and releasing it is another step's.
func TestTheSagasCancelWritesNothingOff(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	require.NoError(t, svc.CancelOrder(ctx, ord.ID, "compensation"))

	units, events := writeOffs(ctx, t, ord.ID)
	assert.Empty(t, units)
	assert.Zero(t, events)
}

// TestTheShopsCancelOfAPaidOrderWritesNothingOff: the cancel refuses an order
// with money collected, and the refusal rolls back whatever it began.
func TestTheShopsCancelOfAPaidOrderWritesNothingOff(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	ord, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	_, err = svc.SetOrderSummaryTotals(ctx, ord.ID, service.SummaryTotalsInput{PaidTotal: 100})
	require.NoError(t, err)

	err = svc.CancelPlacedOrder(ctx, ord.ID, "")
	require.Error(t, err)
	assert.Equal(t, service.CodeNotPending, errors.CodeOf(err))

	units, events := writeOffs(ctx, t, ord.ID)
	assert.Empty(t, units)
	assert.Zero(t, events)
}
