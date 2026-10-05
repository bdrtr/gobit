package service_test

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// PlacedMargins returns the scripted parts of the orders asked for.
func (f *fakeStore) PlacedMargins(_ context.Context, orderIDs []string) ([]models.PlacedMargin, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := []models.PlacedMargin{}
	for _, m := range f.margins {
		if slices.Contains(orderIDs, m.OrderID) {
			out = append(out, m)
		}
	}
	return out, nil
}

// TestAMarginIsSalesLessCostWhenThereIsACost is the service's half of ADR 0401:
// the margin is the sales less the cost, a loss is kept negative, an order
// without a cost has no margin, and an order the store has no row for has no
// entry.
func TestAMarginIsSalesLessCostWhenThereIsACost(t *testing.T) {
	e := newEnv(t)
	cost := func(v int64) *int64 { return &v }
	e.store.margins = []models.PlacedMargin{
		{OrderID: "order_gain", Sales: 1000, Cost: cost(400)},
		{OrderID: "order_loss", Sales: 300, Cost: cost(500)},
		{OrderID: "order_free", Sales: 300, Cost: cost(0)},
		{OrderID: "order_unknown", Sales: 900, LinesWithoutCost: 2},
	}

	margins, err := e.svc.PlacedMargins(context.Background(),
		[]string{"order_gain", "order_loss", "order_free", "order_unknown", "order_gift_cards"})
	require.NoError(t, err)

	require.NotNil(t, margins["order_gain"].Margin)
	assert.Equal(t, int64(600), *margins["order_gain"].Margin)
	require.NotNil(t, margins["order_loss"].Margin)
	assert.Equal(t, int64(-200), *margins["order_loss"].Margin, "goods sold at a loss keep their sign")
	require.NotNil(t, margins["order_free"].Margin, "a cost of zero is a cost")
	assert.Equal(t, int64(300), *margins["order_free"].Margin)
	assert.Nil(t, margins["order_unknown"].Margin, "no cost is no margin, not the sales")
	assert.Equal(t, int64(900), margins["order_unknown"].Sales)
	assert.Equal(t, int64(2), margins["order_unknown"].LinesWithoutCost)
	assert.NotContains(t, margins, "order_gift_cards", "an order with no row has no entry")

	none, err := e.svc.PlacedMargins(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, none)
}
