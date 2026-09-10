package cart

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serveCartWithMetadata scripts the fake cart so that the snapshot carries the
// given metadata bag.
func serveCartWithMetadata(carts *stubCarts, metadata map[string]any) {
	carts.snapshotFn = func(_ context.Context, cartID string) (json.RawMessage, error) {
		snap := snapshotOf(1, []SnapshotItem{
			{ID: testLineA, VariantID: testVariantA, Quantity: 1},
		}, nil)
		snap.ID = cartID
		snap.Metadata = metadata

		return json.Marshal(snap)
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

// TestTheCartsOwnDataReachesTheRuleContext is the hook the embedder did not
// have.
//
// The context was built from two names this flow decides — the region and the
// customer's group — and nothing could add a third. A shop selling two brands
// from one installation could not write "10% off, brand A only" without a column
// in the cart module for a concept that module has never heard of.
func TestTheCartsOwnDataReachesTheRuleContext(t *testing.T) {
	h := newModuleHarness(t)
	serveCartWithMetadata(h.carts, map[string]any{"brand": "acme", "channel": "kiosk"})

	attributes := contextOf(t, h)

	assert.Equal(t, "acme", attributes[CartAttributePrefix+"brand"])
	assert.Equal(t, "kiosk", attributes[CartAttributePrefix+"channel"])
	assert.Equal(t, testRegionID, attributes[attrRegionID], "the fixed names still stand")
}

// TestTheCartsDataCannotShadowTheNamesTheFlowDecides is what the prefix is for.
//
// A cart whose metadata carried "customer_group_id" would otherwise let whoever
// writes that bag hand themselves a segment discount. The storefront writes it;
// the storefront is not the party that decides who is in which group.
func TestTheCartsDataCannotShadowTheNamesTheFlowDecides(t *testing.T) {
	h := newModuleHarness(t)
	h.customers.emails = map[string]string{}
	serveCartWithMetadata(h.carts, map[string]any{
		attrRegionID:        "reg_SOMEWHERE_CHEAPER",
		attrCustomerGroupID: "grp_WHOLESALE",
	})

	attributes := contextOf(t, h)

	assert.Equal(t, testRegionID, attributes[attrRegionID],
		"the region is the flow's to decide")
	assert.NotContains(t, attributes, attrCustomerGroupID,
		"a cart with no customer names no group, whatever its metadata says")
	assert.Equal(t, "grp_WHOLESALE", attributes[CartAttributePrefix+attrCustomerGroupID],
		"the claim is carried, under a name that cannot be mistaken for the real one")
}

// TestOnlyStringMetadataBecomesAnAttribute keeps a formatting rule out of the
// engine.
//
// The engine compares whole values, so a number would need one: 1 and 1.0 are
// the same number and two different attribute values, and a rule stored against
// one would silently miss the other.
func TestOnlyStringMetadataBecomesAnAttribute(t *testing.T) {
	h := newModuleHarness(t)
	serveCartWithMetadata(h.carts, map[string]any{
		"tier":     "gold",
		"seats":    4,
		"vip":      true,
		"nested":   map[string]any{"a": "b"},
		"quantity": "5",
	})

	attributes := contextOf(t, h)

	assert.Equal(t, "gold", attributes[CartAttributePrefix+"tier"])
	assert.Equal(t, "5", attributes[CartAttributePrefix+"quantity"],
		"a number written as a string crosses, and the numeric operators parse it")
	for _, skipped := range []string{"seats", "vip", "nested"} {
		assert.NotContains(t, attributes, CartAttributePrefix+skipped,
			"%s is not a string, so it is skipped rather than formatted", skipped)
	}
}

// TestTheNumberOfCartAttributesIsBounded keeps a storefront from making its own
// carts expensive to price.
//
// Every attribute is copied into the discount request on every totals round, and
// the bag is free-form.
func TestTheNumberOfCartAttributesIsBounded(t *testing.T) {
	h := newModuleHarness(t)

	metadata := map[string]any{}
	for i := range MaxCartAttributes + 10 {
		metadata[fmt.Sprintf("k%03d", i)] = "v"
	}
	serveCartWithMetadata(h.carts, metadata)

	attributes := contextOf(t, h)

	// The surviving set is asserted WHOLE, not sampled. The keys are taken in
	// SORTED order, so which ones survive the bound is reproducible; a sample
	// would pass on map-iteration order about two times in three, because the
	// key it names would often be inside the bound by luck.
	want := make([]string, 0, MaxCartAttributes)
	for i := range MaxCartAttributes {
		want = append(want, fmt.Sprintf("%sk%03d", CartAttributePrefix, i))
	}

	carried := make([]string, 0, MaxCartAttributes)
	for key := range attributes {
		if strings.HasPrefix(key, CartAttributePrefix) {
			carried = append(carried, key)
		}
	}

	assert.ElementsMatch(t, want, carried,
		"the first %d keys in sorted order, and no others", MaxCartAttributes)
}

// TestACartWithNoMetadataCarriesNoExtraAttribute keeps the ordinary cart free of
// the feature.
func TestACartWithNoMetadataCarriesNoExtraAttribute(t *testing.T) {
	h := newModuleHarness(t)
	serveCartWithMetadata(h.carts, nil)

	attributes := contextOf(t, h)

	assert.Len(t, attributes, 1, "only the region: %v", attributes)
}
