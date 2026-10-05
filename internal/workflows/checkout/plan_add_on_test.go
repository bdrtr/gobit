package checkout

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// snapshotWith serves a two-line cart whose second line names parentOfB.
func snapshotWith(parentOfA, parentOfB string) func(context.Context, string) (json.RawMessage, error) {
	return func(_ context.Context, cartID string) (json.RawMessage, error) {
		return json.Marshal(Snapshot{
			ID: cartID, RegionID: testRegionID, CustomerID: testCustomerID, CurrencyCode: testCurrency,
			Revision: testRevision,
			Items: []SnapshotItem{
				{ID: testLineA, VariantID: testVariantA, Quantity: 2, ParentLineID: parentOfA},
				{ID: testLineB, VariantID: testVariantB, Quantity: 1, ParentLineID: parentOfB},
			},
		})
	}
}

// TestTheOrderMeetsEveryParentBeforeItsAddOns is ADR 0229 across the checkout:
// the cart's add-on line, listed before its parent, reaches the order after it,
// each named by its cart line id and the add-on by its parent's, on the wire the
// order reads.
func TestTheOrderMeetsEveryParentBeforeItsAddOns(t *testing.T) {
	h := newHarness(t)
	h.carts.snapshotFn = snapshotWith(testLineB, "")

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	require.Len(t, h.orders.placed, 1)
	items := h.orders.placed[0].Items
	require.Len(t, items, 2)
	assert.Equal(t, testLineB, items[0].LineKey, "the parent goes first")
	assert.Empty(t, items[0].ParentLineKey)
	assert.Equal(t, testLineA, items[1].LineKey)
	assert.Equal(t, testLineB, items[1].ParentLineKey, "the add-on names its parent")

	raw, err := json.Marshal(h.orders.placed[0])
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"line_key":"`+testLineB+`"`)
	assert.Contains(t, string(raw), `"parent_line_key":"`+testLineB+`"`)
}

// TestAnAddOnWithoutItsLineIsRefused holds that the plan refuses an add-on
// whose parent is not a line of its own in the cart, before any side effect.
func TestAnAddOnWithoutItsLineIsRefused(t *testing.T) {
	for name, parents := range map[string][2]string{
		"a parent the cart does not hold":   {"", "li_gone"},
		"a parent that is itself an add-on": {testLineB, testLineA},
	} {
		h := newHarness(t)
		h.carts.snapshotFn = snapshotWith(parents[0], parents[1])

		_, err := h.wf.CompleteCart(context.Background(), h.input())

		require.Error(t, err, name)
		assert.Empty(t, h.orders.placed, name)
		for _, call := range h.rec.calls {
			assert.NotContains(t, call, "inventory:reserve", "%s: nothing was reserved", name)
			assert.NotContains(t, call, "payment:", "%s: no money was asked for", name)
		}
	}
}

// TestEachAddOnIsWrittenUnderItsOwnLine is ADR 0393 on the wire the order
// reads: two rings, each with its add-ons listed anywhere after it in the cart,
// reach the order each followed by its own add-ons, in the cart's order, so a
// reader that prints the order's lines as written prints each add-on under its
// ring.
func TestEachAddOnIsWrittenUnderItsOwnLine(t *testing.T) {
	line := func(id, parent string) planLine {
		return planLine{LineItemID: id, VariantID: "var_" + id, Quantity: 1, ParentLineItemID: parent}
	}
	plan := checkoutPlan{Lines: []planLine{
		line("li_A", ""), line("li_B", ""), line("li_a1", "li_A"), line("li_b1", "li_B"), line("li_a2", "li_A"),
	}}

	raw, err := plan.orderSnapshotJSON("k")
	require.NoError(t, err)
	var sent struct {
		Items []struct {
			LineKey string `json:"line_key"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(raw, &sent))
	keys := make([]string, 0, len(sent.Items))
	for _, item := range sent.Items {
		keys = append(keys, item.LineKey)
	}
	assert.Equal(t, []string{"li_A", "li_a1", "li_a2", "li_B", "li_b1"}, keys)
}

// TestLineOrderPlacesEveryLine holds that the order drops no line: an add-on
// naming a line the cart does not hold, which validateAddOns refuses first, is
// still placed, last.
func TestLineOrderPlacesEveryLine(t *testing.T) {
	plan := checkoutPlan{Lines: []planLine{
		{LineItemID: "li_a", ParentLineItemID: "li_gone"},
		{LineItemID: "li_A"},
	}}

	assert.Equal(t, []int{1, 0}, plan.lineOrder())
}
