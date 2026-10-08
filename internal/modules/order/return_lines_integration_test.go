//go:build integration

package order_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestAReturnsLinesAndWorthAreReadOnTheRealSchema is what the return routes
// publish (ADR 0433) over the real reads: a page of one order's returns comes
// back with each one's lines and what its units were sold for, a return that
// names no line with none and at zero, and a single read the same.
func TestAReturnsLinesAndWorthAreReadOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	placed, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	detail, err := svc.GetOrder(ctx, placed.ID)
	require.NoError(t, err)
	lineID := detail.Items[0].ID
	one, err := svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: placed.ID, Lines: []service.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 1, RefundAmount: 70}},
	})
	require.NoError(t, err)
	two, err := svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: placed.ID, Lines: []service.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 2}},
	})
	require.NoError(t, err)
	none, err := svc.CreateReturn(ctx, service.CreateReturnInput{OrderID: placed.ID})
	require.NoError(t, err)

	page, _, err := svc.ListReturns(ctx, placed.ID, service.Page{Limit: 10})
	require.NoError(t, err)
	records, err := svc.ReturnsWithLines(ctx, page)
	require.NoError(t, err)
	require.Len(t, records, 3)
	worth := map[string]int64{}
	lines := map[string]int{}
	for i := range records {
		worth[records[i].ID] = records[i].SoldFor
		lines[records[i].ID] = len(records[i].Items)
	}
	assert.Equal(t, map[string]int64{one.ID: 1_200, two.ID: 2_400, none.ID: 0}, worth,
		"one and two of three units of a 3 600 line, and nothing for a return naming none")
	assert.Equal(t, map[string]int{one.ID: 1, two.ID: 1, none.ID: 0}, lines)

	read, err := svc.GetReturn(ctx, one.ID)
	require.NoError(t, err)
	single, err := svc.ReturnsWithLines(ctx, []models.Return{read})
	require.NoError(t, err)
	require.Len(t, single[0].Items, 1)
	assert.Equal(t, lineID, single[0].Items[0].OrderLineItemID)
	assert.Equal(t, int64(70), single[0].Items[0].RefundAmount)
	assert.Equal(t, int64(1_200), single[0].SoldFor)
}
