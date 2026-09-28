package checkout

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheOrderReceivesEachLinesNoteAndWords is ADR 0223 and D152 across the
// checkout: the cart's snapshot carries a line's metadata and properties, the
// plan keeps them, and the order is handed both, on the wire it reads.
func TestTheOrderReceivesEachLinesNoteAndWords(t *testing.T) {
	h := newHarness(t)
	h.carts.snapshotFn = func(_ context.Context, cartID string) (json.RawMessage, error) {
		return json.Marshal(Snapshot{
			ID: cartID, RegionID: testRegionID, CustomerID: testCustomerID, CurrencyCode: testCurrency,
			Revision: testRevision,
			Items: []SnapshotItem{
				{
					ID: testLineA, VariantID: testVariantA, Quantity: 2,
					Metadata:   map[string]any{"gift_wrap": true},
					Properties: map[string]string{"Engraving": "For Anna"},
				},
				{ID: testLineB, VariantID: testVariantB, Quantity: 1},
			},
		})
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	require.Len(t, h.orders.placed, 1)
	byVariant := map[string]orderSnapshotItem{}
	for _, item := range h.orders.placed[0].Items {
		byVariant[item.VariantID] = item
	}
	engraved := byVariant[testVariantA]
	assert.Equal(t, map[string]string{"Engraving": "For Anna"}, engraved.Properties)
	assert.Equal(t, map[string]any{"gift_wrap": true}, engraved.Metadata, "the note reaches the order (D152)")
	assert.Nil(t, byVariant[testVariantB].Properties)

	raw, err := json.Marshal(h.orders.placed[0])
	require.NoError(t, err)
	var wire struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(raw, &wire))
	names := map[string]bool{}
	for _, item := range wire.Items {
		for key := range item {
			names[key] = true
		}
	}
	assert.True(t, names["properties"], "the order reads \"properties\"")
	assert.True(t, names["metadata"], "the order reads \"metadata\"")
}
