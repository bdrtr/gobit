package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestAReturnDetailSaysWhatItsUnitsWereSoldFor is the figure a return's
// refunds are held to (ADR 0433): one of three units of a line that came to
// 10 000 after its discount was sold for 3 333, rounded down as the invoicing
// split shares a returned row, and the flow reads it from the return detail.
func TestAReturnDetailSaysWhatItsUnitsWereSoldFor(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	e := newEnv(t)
	in := validInput()
	in.Subtotal, in.DiscountTotal, in.TaxTotal, in.ShippingTotal, in.Total = 10_200, 200, 0, 0, 10_000
	in.Items[0].UnitPrice, in.Items[0].Subtotal, in.Items[0].DiscountTotal = 3_400, 10_200, 200
	in.Items[0].TaxTotal, in.Items[0].Total = 0, 10_000
	order, err := e.svc.CreateOrder(ctx, in)
	require.NoError(t, err)
	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	lineID := detail.Items[0].ID

	for units, want := range map[int64]int64{1: 3_333, 2: 6_666, 3: 10_000} {
		record, err := e.svc.CreateReturnRecord(ctx, service.CreateReturnInput{
			OrderID: order.ID,
			Lines:   []service.ReturnLineInput{{OrderLineItemID: lineID, Quantity: units}},
		})
		require.NoError(t, err)
		assert.Equal(t, want, record.SoldFor, "the opening answers the figure from its own write")
		ret := record.Return

		raw, err := e.svc.ReturnDetailJSON(ctx, ret.ID)
		require.NoError(t, err)
		var read struct {
			SoldFor *int64 `json:"sold_for"`
		}
		require.NoError(t, json.Unmarshal(raw, &read))
		require.NotNil(t, read.SoldFor, "the figure is on the wire: %s", raw)
		assert.Equal(t, want, *read.SoldFor, "%d of 3 units: %s", units, raw)

		_, err = e.svc.CancelReturn(ctx, ret.ID)
		require.NoError(t, err)
	}
}

// countingReads counts the store reads a page of returns makes.
type countingReads struct {
	*fakeStore
	itemReads, lineReads int
}

func (c *countingReads) ReturnItemsOf(ctx context.Context, ids []string) (map[string][]models.ReturnItem, error) {
	c.itemReads++
	return c.fakeStore.ReturnItemsOf(ctx, ids)
}

func (c *countingReads) ListLineItems(ctx context.Context, orderID string) ([]models.OrderLineItem, error) {
	c.lineReads++
	return c.fakeStore.ListLineItems(ctx, orderID)
}

// TestAPageOfReturnsCarriesItsLinesInTwoReads is what the return routes
// publish (ADR 0433): each record with its lines and what its units were sold
// for, a return that names none at zero, read in one query for the page's
// lines and one for the order's, not one per record.
func TestAPageOfReturnsCarriesItsLinesInTwoReads(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	e := newEnv(t)
	order, lineID := returnedOrder(t, e)
	one, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: order.ID, Lines: []service.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 1, RefundAmount: 50}},
	})
	require.NoError(t, err)
	two, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: order.ID, Lines: []service.ReturnLineInput{{OrderLineItemID: lineID, Quantity: 2}},
	})
	require.NoError(t, err)
	none, err := e.svc.CreateReturn(ctx, service.CreateReturnInput{OrderID: order.ID})
	require.NoError(t, err)

	reads := &countingReads{fakeStore: e.store}
	svc, err := service.New(service.Options{Repo: reads, Events: e.bus})
	require.NoError(t, err)
	page, _, err := svc.ListReturns(ctx, order.ID, service.Page{Limit: 10})
	require.NoError(t, err)
	require.Len(t, page, 3)
	for i := range page {
		require.Nil(t, page[i].Items, "the listing reads no lines of its own")
	}

	records, err := svc.ReturnsWithLines(ctx, page)
	require.NoError(t, err)

	assert.Equal(t, 1, reads.itemReads, "one read of the page's lines")
	assert.Equal(t, 1, reads.lineReads, "one read of the order's lines")
	byID := map[string]service.ReturnRecord{}
	for i := range records {
		byID[records[i].ID] = records[i]
	}
	require.Len(t, byID[one.ID].Items, 1)
	assert.Equal(t, int64(50), byID[one.ID].Items[0].RefundAmount)
	assert.Equal(t, int64(1_200), byID[one.ID].SoldFor, "one of three units of a 3 600 line")
	assert.Equal(t, int64(2_400), byID[two.ID].SoldFor)
	assert.Empty(t, byID[none.ID].Items)
	assert.NotNil(t, byID[none.ID].Items, "a return naming no line publishes an empty list")
	assert.Zero(t, byID[none.ID].SoldFor)

	created, err := svc.ReturnsWithLines(ctx, []models.Return{two})
	require.NoError(t, err)
	assert.Equal(t, 1, reads.itemReads, "a created return's lines are not read again")
	assert.Equal(t, int64(2_400), created[0].SoldFor)
}
