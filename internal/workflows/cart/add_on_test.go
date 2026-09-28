package cart

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// ringAccepts makes testVariantA a ring whose product accepts the given
// add-on variants (ADR 0228).
func ringAccepts(h *harness, addOns ...string) {
	h.catalog.products[testVariantA] = "prod_ring"
	h.catalog.entities = map[string][]query.Record{
		EntityProduct: {{query.IDField: "prod_ring", FieldAddOnVariantIDs: addOns}},
	}
}

// TestAnAddOnIsPricedAsTheLineIs is ADR 0229 in the flow: an add-on the ring's
// product accepts is priced from its own price set at the line's quantity,
// titled from the catalog, and handed to the cart with the shopper's words.
func TestAnAddOnIsPricedAsTheLineIs(t *testing.T) {
	h := newHarness(t)
	ringAccepts(h, testVariantB)
	recordAddLine(h.carts, testLineA)
	serveSnapshot(h.carts,
		snapshotOf(0, nil, nil),
		snapshotOf(1, []SnapshotItem{
			{ID: testLineA, VariantID: testVariantA, Quantity: 2},
			{ID: testLineB, VariantID: testVariantB, Quantity: 2},
		}, nil),
	)

	_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID: testCartID, VariantID: testVariantA, Quantity: 2,
		AddOns: []AddOnRequest{{VariantID: testVariantB, Properties: map[string]string{"Text": "Ada"}}},
	})
	require.NoError(t, err)

	require.Len(t, h.carts.addedAddOns, 1)
	var handed []pricedAddOn
	require.NoError(t, json.Unmarshal(h.carts.addedAddOns[0], &handed))
	assert.Equal(t, []pricedAddOn{{
		VariantID: testVariantB, Title: h.catalog.titles[testVariantB], UnitPrice: 250,
		Properties: map[string]string{"Text": "Ada"},
	}}, handed)
	var askedB []int32
	for _, call := range h.prices.seen {
		if call.priceSetID == testPriceSetB {
			askedB = append(askedB, call.quantity)
		}
	}
	assert.Equal(t, []int32{2}, askedB, "the add-on is priced at the line's quantity")
}

// TestAnAddOnTheProductDoesNotTakeIsRefused holds the check against the list:
// an add-on the product does not accept, and one named twice, are refused
// before anything is priced or written.
func TestAnAddOnTheProductDoesNotTakeIsRefused(t *testing.T) {
	for name, addOns := range map[string][]AddOnRequest{
		"not on the list": {{VariantID: "var_other"}},
		"named twice":     {{VariantID: testVariantB}, {VariantID: testVariantB}},
	} {
		h := newHarness(t)
		ringAccepts(h, testVariantB)
		serveSnapshot(h.carts, snapshotOf(0, nil, nil))

		_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
			CartID: testCartID, VariantID: testVariantA, Quantity: 1, AddOns: addOns,
		})

		require.Error(t, err, name)
		assert.Equal(t, CodeAddOnNotAccepted, errors.CodeOf(err), name)
		assert.Empty(t, h.carts.addedAddOns, "%s: nothing is written", name)
		assert.Empty(t, h.prices.seen, "%s: nothing is priced", name)
	}
}
