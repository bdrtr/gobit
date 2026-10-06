package checkout

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheOrderRecordsTheChannelItsCartWasPricedIn carries the channel from
// the cart module's snapshot into the body the order module reads, by the wire
// names on both ends (ADR 0410). The request names other channels, which pick
// the warehouses and may be several: the order records the cart's one, the
// channel its prices were chosen in (ADR 0397).
func TestTheOrderRecordsTheChannelItsCartWasPricedIn(t *testing.T) {
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
		body["sales_channel_id"] = "sc_cart"

		return json.Marshal(body)
	}
	var sent map[string]any
	h.orders.placeFn = func(_ context.Context, snapshot json.RawMessage) (string, error) {
		if err := json.Unmarshal(snapshot, &sent); err != nil {
			return "", err
		}

		return testOrderID, nil
	}

	in := h.input()
	in.SalesChannelIDs = []string{"sc_request_a", "sc_request_b"}
	_, err := h.wf.CompleteCart(context.Background(), in)
	require.NoError(t, err)

	assert.Equal(t, "sc_cart", sent["sales_channel_id"])
}

// TestAnOrderFromACartWithNoChannelRecordsNone keeps the field off the order
// of a cart that named no channel, and off the order a plan saved before the
// field places: an old plan knew no channel, and the order says so.
func TestAnOrderFromACartWithNoChannelRecordsNone(t *testing.T) {
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
	assert.NotContains(t, sent, "sales_channel_id")

	saved, err := json.Marshal(checkoutPlan{CartID: testCartID, SalesChannel: "sc_cart"})
	require.NoError(t, err)
	var old map[string]any
	require.NoError(t, json.Unmarshal(saved, &old))
	require.Contains(t, old, "sales_channel_id", "the plan records the channel under its wire name")
	delete(old, "sales_channel_id")
	saved, err = json.Marshal(old)
	require.NoError(t, err)

	var plan checkoutPlan
	require.NoError(t, json.Unmarshal(saved, &plan))
	body, err := plan.orderSnapshotJSON("wf_OLD")
	require.NoError(t, err)
	var placed map[string]any
	require.NoError(t, json.Unmarshal(body, &placed))
	assert.NotContains(t, placed, "sales_channel_id")
}
