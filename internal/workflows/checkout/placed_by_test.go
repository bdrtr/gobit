package checkout

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheOrderNamesTheOperatorWhoPlacedIt carries the operator from the cart
// module's request, through the JSON boundary, into the body the order module
// reads, by the wire names on both ends (ADR 0298).
func TestTheOrderNamesTheOperatorWhoPlacedIt(t *testing.T) {
	h := laterHarness(t, 0)
	// The interop takes no location, so the reservation asks where the stock
	// is, and the test location holds it.
	h.inventory.locationsFn = func(context.Context, string, int64) ([]string, error) {
		return []string{testLocationID}, nil
	}
	h.fulfillment.rankFn = rankByGreatestID
	var sent map[string]any
	h.orders.placeFn = func(_ context.Context, snapshot json.RawMessage) (string, error) {
		if err := json.Unmarshal(snapshot, &sent); err != nil {
			return "", err
		}

		return testOrderID, nil
	}

	request, err := json.Marshal(map[string]any{
		"cart_id": testCartID, "payment_provider_id": h.input().PaymentProviderID,
		"expected_total": testAmount, "offline_only": true, "placed_by": "usr_operator",
	})
	require.NoError(t, err)
	_, err = NewInterop(h.wf).CompleteCartJSON(context.Background(), request)
	require.NoError(t, err)

	assert.Equal(t, "usr_operator", sent["placed_by"])
}

// TestAShoppersOrderNamesNoOperator keeps the field off every other order.
func TestAShoppersOrderNamesNoOperator(t *testing.T) {
	h := newHarness(t)
	var sent map[string]any
	h.orders.placeFn = func(_ context.Context, snapshot json.RawMessage) (string, error) {
		if err := json.Unmarshal(snapshot, &sent); err != nil {
			return "", err
		}

		return testOrderID, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	assert.NotContains(t, sent, "placed_by")
}
