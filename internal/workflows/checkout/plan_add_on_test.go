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
