//go:build integration

package order_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order"
)

// panelMargin is one entry of the surface's margin read, decoded as the panel
// decodes it.
type panelMargin struct {
	OrderID          string `json:"order_id"`
	Sales            int64  `json:"sales"`
	Cost             *int64 `json:"cost"`
	Margin           *int64 `json:"margin"`
	LinesWithoutCost int64  `json:"lines_without_cost"`
}

// TestThePanelReadsAnOrdersMarginAndItsLinesCosts is ADR 0412 on the real
// schema: the surface answers each order's placed margin as the service
// computes it, in the order the ids were given, an uncosted order without a
// cost or a margin and a gift-card-only order not at all; each line's cost is
// keyed by its id, absent on a line that kept none; a read naming more orders
// than the panel pages is refused, not cut.
func TestThePanelReadsAnOrdersMarginAndItsLinesCosts(t *testing.T) {
	ctx := context.Background()
	svc, surface := panelSurface(t)

	costed := placeCosted(t, svc, false,
		costLine{quantity: 2, unitPrice: 1000, discount: 100, cost: costOf(300)},
		costLine{quantity: 1, unitPrice: 500, cost: costOf(0)},
	)
	uncosted := placeCosted(t, svc, false,
		costLine{quantity: 1, unitPrice: 1000, cost: costOf(400)},
		costLine{quantity: 1, unitPrice: 200},
		costLine{quantity: 1, unitPrice: 5000, giftcard: true},
	)
	cards := placeCosted(t, svc, false, costLine{quantity: 1, unitPrice: 5000, giftcard: true})

	raw, err := surface.PlacedMarginsJSON(ctx, []string{uncosted, cards, costed})
	require.NoError(t, err)
	var margins []panelMargin
	require.NoError(t, json.Unmarshal(raw, &margins))
	require.Len(t, margins, 2, "a gift-card-only order has no margin")
	want, err := svc.PlacedMargins(ctx, []string{costed, uncosted})
	require.NoError(t, err)

	assert.Equal(t, uncosted, margins[0].OrderID, "the ids' order is kept")
	assert.Equal(t, want[uncosted].Sales, margins[0].Sales)
	assert.Nil(t, margins[0].Cost, "a line kept no cost")
	assert.Nil(t, margins[0].Margin)
	assert.Equal(t, int64(1), margins[0].LinesWithoutCost)

	assert.Equal(t, costed, margins[1].OrderID)
	assert.Equal(t, int64(2400), margins[1].Sales, "2 × 1000 − 100 + 500")
	require.NotNil(t, margins[1].Cost)
	require.NotNil(t, margins[1].Margin)
	assert.Equal(t, *want[costed].Cost, *margins[1].Cost)
	assert.Equal(t, int64(600), *margins[1].Cost)
	assert.Equal(t, *want[costed].Margin, *margins[1].Margin)
	assert.Equal(t, int64(1800), *margins[1].Margin)
	assert.Zero(t, margins[1].LinesWithoutCost)

	raw, err = surface.LineCostsJSON(ctx, uncosted)
	require.NoError(t, err)
	var lines []struct {
		LineItemID string `json:"line_item_id"`
		UnitCost   *int64 `json:"unit_cost"`
	}
	require.NoError(t, json.Unmarshal(raw, &lines))
	detail, err := svc.GetOrder(ctx, uncosted)
	require.NoError(t, err)
	require.Len(t, lines, len(detail.Items))
	byLine := map[string]*int64{}
	for _, line := range lines {
		byLine[line.LineItemID] = line.UnitCost
	}
	for _, item := range detail.Items {
		got, ok := byLine[item.ID]
		require.True(t, ok, "line %s is answered", item.ID)
		if item.UnitCost == nil {
			assert.Nil(t, got, "line %s kept no cost", item.ID)
			continue
		}
		require.NotNil(t, got, "line %s", item.ID)
		assert.Equal(t, *item.UnitCost, *got, "line %s", item.ID)
	}
	assert.Contains(t, string(raw), `"unit_cost":400`)

	_, err = surface.LineCostsJSON(ctx, "order_missing")
	assert.True(t, errors.IsNotFound(err), "%v", err)

	ids := make([]string, order.MaxPanelMargins+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("order_%d", i)
	}
	_, err = surface.PlacedMarginsJSON(ctx, ids)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "%v", err)
	_, err = surface.PlacedMarginsJSON(ctx, ids[:order.MaxPanelMargins])
	require.NoError(t, err, "the bound itself is read")
}
