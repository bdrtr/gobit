package returns

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAReturnedBundlePutsBackItsParts is ADR 0235's receipt: two gift boxes
// coming back put back two towels and four soaps, and the box, which tracks
// no stock of its own, is not asked about.
func TestAReturnedBundlePutsBackItsParts(t *testing.T) {
	h := newHarness(t)
	h.orders.detail.Lines = []returnLine{{
		OrderLineItemID: "oli_box", VariantID: "var_box", Quantity: 2,
		Components: []returnLineComponent{
			{VariantID: testVariantA, Quantity: 1},
			{VariantID: testVariantB, Quantity: 2},
		},
	}}

	out, err := h.wf.ReceiveReturn(context.Background(), testReturnID, testLocationID)
	require.NoError(t, err)

	assert.Empty(t, out.Warnings, "the box's own variant is no warning: its parts are what came back")
	assert.Equal(t, 1, out.RestockedLines)
	assert.Equal(t, int64(6), out.RestockedUnits)
	assert.Equal(t, []restockCall{
		{itemID: testItemA, locationID: testLocationID, quantity: 2},
		{itemID: testItemB, locationID: testLocationID, quantity: 4},
	}, h.inventory.calls)
}

// TestABundleWithAPartThatHasNoItemIsNotWhollyRestocked keeps the count
// honest: the part that went back is counted, the one that could not is a
// warning, and the line is not reported restocked.
func TestABundleWithAPartThatHasNoItemIsNotWhollyRestocked(t *testing.T) {
	h := newHarness(t)
	delete(h.links.links, testVariantB)
	h.orders.detail.Lines = []returnLine{{
		OrderLineItemID: "oli_box", VariantID: "var_box", Quantity: 1,
		Components: []returnLineComponent{
			{VariantID: testVariantA, Quantity: 1},
			{VariantID: testVariantB, Quantity: 2},
		},
	}}

	out, err := h.wf.ReceiveReturn(context.Background(), testReturnID, testLocationID)
	require.NoError(t, err)

	assert.Len(t, out.Warnings, 1)
	assert.Equal(t, 0, out.RestockedLines)
	assert.Equal(t, int64(1), out.RestockedUnits)
}
