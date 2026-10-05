package cart

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serveCartWithMetadata scripts the fake cart so that the snapshot it sends
// carries the given metadata bag.
//
// The bag is set on the WIRE, not on the flow's type: the cart module's
// snapshot still carries it for any reader, and what this flow must show is
// that nothing it decodes from that payload reaches a rule (ADR 0407).
func serveCartWithMetadata(carts *stubCarts, metadata map[string]any) {
	carts.snapshotFn = func(_ context.Context, cartID string) (json.RawMessage, error) {
		snap := snapshotOf(1, []SnapshotItem{
			{ID: testLineA, VariantID: testVariantA, Quantity: 1},
		}, nil)
		snap.ID = cartID

		raw, err := json.Marshal(snap)
		if err != nil || metadata == nil {
			return raw, err
		}
		var wire map[string]any
		if err := json.Unmarshal(raw, &wire); err != nil {
			return nil, err
		}
		wire["metadata"] = metadata

		return json.Marshal(wire)
	}
}

// contextOf runs a round and returns the rule context the discount request
// carried.
func contextOf(t *testing.T, h *harness) map[string]string {
	t.Helper()

	_, err := h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)
	require.NotEmpty(t, h.discounts.requests)

	return h.discounts.requests[len(h.discounts.requests)-1].Context
}

// bagKeys returns the attributes of a context under the prefix a cart's
// metadata once filled.
func bagKeys(attributes map[string]string) []string {
	var keys []string
	for key := range attributes {
		if strings.HasPrefix(key, CartAttributePrefix) {
			keys = append(keys, key)
		}
	}
	return keys
}

// TestTheCartsBagReachesNoRound is gap D261 (ADR 0407), with D260 (ADR 0403)
// beside it: whoever holds the storefront's publishable key writes a cart's
// metadata, so neither a price nor a promotion may rest on it. A line is added
// and the totals computed on a guest cart whose bag claims a brand, a segment
// and another region, and no price call, no batch price request and no
// discount request carries any of it.
func TestTheCartsBagReachesNoRound(t *testing.T) {
	h := newModuleHarness(t)
	serveCartWithMetadata(h.carts, map[string]any{
		"brand":             "acme",
		AttrCustomerGroupID: "grp_WHOLESALE",
		attrRegionID:        "reg_ELSEWHERE",
	})
	recordAddLine(h.carts, testLineA)

	_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID: testCartID, VariantID: testVariantA, Quantity: 1,
	})
	require.NoError(t, err)
	_, err = h.wf.CalculateTotals(context.Background(), testCartID)
	require.NoError(t, err)

	require.NotEmpty(t, h.prices.seen, "the line was priced")
	for i, call := range h.prices.seen {
		assert.Empty(t, bagKeys(call.attributes), "price call %d carried the cart's bag", i)
		assert.Equal(t, testRegionID, call.attributes[attrRegionID], "price call %d names the cart's region", i)
		assert.NotContains(t, call.attributes, AttrCustomerGroupID, "price call %d names a guest's group", i)
	}
	require.NotEmpty(t, h.prices.requests, "the totals round asked for prices")
	for i, request := range h.prices.requests {
		assert.Empty(t, bagKeys(request.Attributes), "price request %d carried the cart's bag", i)
		assert.Equal(t, testRegionID, request.Attributes[attrRegionID], "price request %d names the cart's region", i)
		assert.NotContains(t, request.Attributes, AttrCustomerGroupID, "price request %d names a guest's group", i)
	}
	require.NotEmpty(t, h.discounts.requests, "the rounds asked for discounts")
	for i, request := range h.discounts.requests {
		assert.Empty(t, bagKeys(request.Context), "discount request %d carried the cart's bag", i)
		assert.Equal(t, testRegionID, request.Context[attrRegionID], "discount request %d names the cart's region", i)
		assert.NotContains(t, request.Context, AttrCustomerGroupID, "discount request %d names a guest's group", i)
	}
}

// TestTheCartsBagReachesNoLinePrice holds the line's opening price to the
// price context (ADR 0403, D260): whoever holds the storefront's key writes the
// bag, so a price ruled on it would be a price the caller chooses.
// [TestTheCartsBagReachesNoRound] holds the discount beside it since ADR 0407.
func TestTheCartsBagReachesNoLinePrice(t *testing.T) {
	h := newModuleHarness(t)
	serveCartWithMetadata(h.carts, map[string]any{"arm": "B"})
	recordAddLine(h.carts, testLineA)

	_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID: testCartID, VariantID: testVariantA, Quantity: 1,
	})

	require.NoError(t, err)
	require.NotEmpty(t, h.prices.seen, "the line was priced")
	for i, call := range h.prices.seen {
		assert.Empty(t, bagKeys(call.attributes), "price call %d carried the cart's bag", i)
		assert.Equal(t, testRegionID, call.attributes[attrRegionID], "price call %d still names the region", i)
	}
}

// TestTheCartsBagReachesNoTotalsPrice is the same hold on the totals round's
// batch price request, which is what the cart is charged.
func TestTheCartsBagReachesNoTotalsPrice(t *testing.T) {
	h := newModuleHarness(t)
	serveCartWithMetadata(h.carts, map[string]any{"arm": "B"})

	contextOf(t, h)

	require.NotEmpty(t, h.prices.requests, "the round asked for prices")
	request := h.prices.requests[len(h.prices.requests)-1]
	assert.Empty(t, bagKeys(request.Attributes), "the totals price request carried the cart's bag")
	assert.Equal(t, testRegionID, request.Attributes[attrRegionID])
}

// TestACartWithNoMetadataCarriesNoExtraAttribute keeps the ordinary cart's
// context to the names this flow decides.
func TestACartWithNoMetadataCarriesNoExtraAttribute(t *testing.T) {
	h := newModuleHarness(t)
	serveCartWithMetadata(h.carts, nil)

	attributes := contextOf(t, h)

	assert.Len(t, attributes, 1, "only the region: %v", attributes)
}
