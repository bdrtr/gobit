package checkout

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAnOrderLineNamesItsProduct is ADR 0365: the product's title, read with
// its gift card flag when the plan is made, reaches each order line beside the
// variant's own title.
func TestAnOrderLineNamesItsProduct(t *testing.T) {
	h := newHarness(t)
	scriptCatalog(h, defaultVariants())

	var sent json.RawMessage
	h.orders.placeFn = func(_ context.Context, snapshot json.RawMessage) (string, error) {
		sent = snapshot
		return testOrderID, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	var body struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(sent, &body))
	titles := map[any]any{}
	for _, item := range body.Items {
		titles[item["variant_id"]] = item["product_title"]
	}
	assert.Equal(t, map[any]any{
		testVariantA: productTitleOf(testVariantA),
		testVariantB: productTitleOf(testVariantB),
	}, titles)
}
