package checkout

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
)

// A line remembers the price it was charged (ADR 0168). The e2e test walks the
// whole chain; these hold the plan's two hops on their own, so a hop that
// drops the origin fails here and not only four modules away.

// TestThePlanCarriesThePriceOriginFromItsOwnRound is the plan's first hop, with
// the REAL cart calculation in front of it: the pricing surface names a base
// price for one line and a sale price for the other, and the order is handed
// both as the round produced them.
func TestThePlanCarriesThePriceOriginFromItsOwnRound(t *testing.T) {
	h := newHarness(t)
	h.carts.setTotalsFn = func(context.Context, string) error { return nil }

	c := container.New(nil)
	provideCheckout(t, c, h)
	provideCartTotals(t, c)
	wf, err := FromContainer(c)
	require.NoError(t, err)

	_, err = wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	require.Len(t, h.orders.placed, 1)
	byVariant := map[string]orderSnapshotItem{}
	for _, item := range h.orders.placed[0].Items {
		byVariant[item.VariantID] = item
	}

	base := byVariant[testVariantA]
	assert.Equal(t, "price_of_"+testPriceSetA, base.PriceID)
	assert.Nil(t, base.PriceListID, "a base price has no list")
	assert.Empty(t, base.PriceListType)

	sale := byVariant[testVariantB]
	assert.Equal(t, "price_of_"+testPriceSetB, sale.PriceID)
	require.NotNil(t, sale.PriceListID)
	assert.Equal(t, testSaleListID, *sale.PriceListID)
	assert.Equal(t, "sale", sale.PriceListType)
}

// TestTheOrderSnapshotCarriesThePriceOrigin is the second hop, on the wire: the
// order ignores unknown fields, so a name that drifts here is dropped there in
// silence, and the order cannot import this package to check the names.
func TestTheOrderSnapshotCarriesThePriceOrigin(t *testing.T) {
	list := testSaleListID
	plan := &checkoutPlan{
		CartID: "cart_1",
		Lines: []planLine{
			{LineItemID: "li_1", VariantID: "var_1", Title: "Legacy", Quantity: 1},
			{LineItemID: "li_2", VariantID: "var_2", Title: "Base", Quantity: 1, PriceID: "price_base"},
			{
				LineItemID: "li_3", VariantID: "var_3", Title: "Sale", Quantity: 1,
				PriceID: "price_sale", PriceListID: &list, PriceListType: "sale",
			},
		},
	}

	raw, err := plan.orderSnapshotJSON("idem_1")
	require.NoError(t, err)

	var body struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	require.Len(t, body.Items, 3)

	for _, key := range []string{"price_id", "price_list_id", "price_list_type"} {
		assert.NotContains(t, body.Items[0], key,
			"a plan written before the origin existed sends none of it: the order records UNKNOWN")
	}
	assert.Equal(t, "price_base", body.Items[1]["price_id"])
	assert.NotContains(t, body.Items[1], "price_list_id")
	assert.Equal(t, "price_sale", body.Items[2]["price_id"])
	assert.Equal(t, testSaleListID, body.Items[2]["price_list_id"])
	assert.Equal(t, "sale", body.Items[2]["price_list_type"])
}
