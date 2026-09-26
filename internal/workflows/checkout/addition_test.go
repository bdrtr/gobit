package checkout

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheOrderAddsToWhatTheCartAddsTo carries the order a cart was opened to
// add to into the body the order module reads (ADR 0192).
//
// Both ends are held by their WIRE name rather than by this package's types:
// the cart module writes "adds_to_order_id" and the order module reads it, the
// order ignores a field it does not know, and a tag misspelled here would place
// an ordinary order in silence.
func TestTheOrderAddsToWhatTheCartAddsTo(t *testing.T) {
	h := newHarness(t)
	h.carts.snapshotFn = func(ctx context.Context, cartID string) (json.RawMessage, error) {
		raw, err := defaultSnapshot(ctx, cartID)
		if err != nil {
			return nil, err
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, err
		}
		body["adds_to_order_id"] = "order_PARENT"

		return json.Marshal(body)
	}
	var sent map[string]any
	h.orders.placeFn = func(_ context.Context, snapshot json.RawMessage) (string, error) {
		if err := json.Unmarshal(snapshot, &sent); err != nil {
			return "", err
		}

		return testOrderID, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	assert.Equal(t, "order_PARENT", sent["adds_to_order_id"])
}

// TestAnOrdinaryCheckoutNamesNoParent keeps the field off every other order.
func TestAnOrdinaryCheckoutNamesNoParent(t *testing.T) {
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

	assert.NotContains(t, sent, "adds_to_order_id")
}

// TestTheOrderIsSoldTheCartsDeliveries carries the cart's shipping methods into
// the order's body by the order's wire names (ADR 0198).
func TestTheOrderIsSoldTheCartsDeliveries(t *testing.T) {
	h := newHarness(t)
	h.carts.snapshotFn = func(ctx context.Context, cartID string) (json.RawMessage, error) {
		raw, err := defaultSnapshot(ctx, cartID)
		if err != nil {
			return nil, err
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			return nil, err
		}
		body["shipping_methods"] = []map[string]any{
			{"id": "csm_1", "shipping_option_id": "so_express", "name": "Next day", "amount": 0},
		}

		return json.Marshal(body)
	}
	var sent map[string]any
	h.orders.placeFn = func(_ context.Context, snapshot json.RawMessage) (string, error) {
		if err := json.Unmarshal(snapshot, &sent); err != nil {
			return "", err
		}

		return testOrderID, nil
	}

	_, err := h.wf.CompleteCart(context.Background(), h.input())
	require.NoError(t, err)

	assert.Equal(t, []any{map[string]any{
		"shipping_option_id": "so_express", "name": "Next day", "amount": float64(0),
	}}, sent["shipping_methods"])
}
