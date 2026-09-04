package cart

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// TestUpdateLineItemWritesQuantityAndRecomputesTotals verifies that a positive quantity
// is written to the cart and that the totals calculation runs.
func TestUpdateLineItemWritesQuantityAndRecomputesTotals(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts,
		snapshotOf(4, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 5}}, nil))

	out, err := h.wf.UpdateLineItem(context.Background(), UpdateLineItemInput{
		CartID:     testCartID,
		LineItemID: testLineA,
		Quantity:   5,
	})
	require.NoError(t, err)

	assert.False(t, out.Removed)
	assert.Equal(t, int64(5), out.Quantity)
	assert.Equal(t, map[string]int64{testLineA: 5}, h.carts.quantities)
	assert.Empty(t, h.carts.removed)

	assert.Equal(t, int64(5000), out.Totals.Subtotal)
	assert.Equal(t, int64(1000), out.Totals.TaxTotal)
	assert.Equal(t, int64(6000), out.Totals.Total)
	requireIdentity(t, out.Totals)
}

// TestUpdateLineItemZeroQuantityRemovesLine verifies that zero is translated into a
// "remove" intent and that this is REPORTED back to the caller.
func TestUpdateLineItemZeroQuantityRemovesLine(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts,
		snapshotOf(6, []SnapshotItem{{ID: testLineB, VariantID: testVariantB, Quantity: 2}}, nil))

	out, err := h.wf.UpdateLineItem(context.Background(), UpdateLineItemInput{
		CartID:     testCartID,
		LineItemID: testLineA,
		Quantity:   0,
	})
	require.NoError(t, err)

	assert.True(t, out.Removed)
	assert.Zero(t, out.Quantity)
	assert.Equal(t, []string{testLineA}, h.carts.removed, "removal happens through a SEPARATE call")
	assert.Empty(t, h.carts.quantities, "a zero quantity is NOT written to the cart")

	// The remaining line is repriced: 250 x 2 = 500, 20% tax 100.
	assert.Equal(t, int64(500), out.Totals.Subtotal)
	assert.Equal(t, int64(600), out.Totals.Total)
	requireIdentity(t, out.Totals)
}

// TestUpdateLineItemZeroesTotalsWhenLastLineRemoved verifies that totals are zeroed.
func TestUpdateLineItemZeroesTotalsWhenLastLineRemoved(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(7, nil, nil))

	out, err := h.wf.UpdateLineItem(context.Background(), UpdateLineItemInput{
		CartID: testCartID, LineItemID: testLineA, Quantity: 0,
	})
	require.NoError(t, err)

	assert.True(t, out.Removed)
	assert.Equal(t, Totals{Revision: 7, TaxSource: TaxSourceRegion, Lines: []LineTotals{}}, out.Totals)
}

// TestUpdateLineItemRejectsNegativeQuantity verifies that a negative quantity does NOT
// delete the line.
//
// Zero means "remove", while a negative value is a sign error with no intent behind it;
// rounding it to zero would let that error delete data.
func TestUpdateLineItemRejectsNegativeQuantity(t *testing.T) {
	h := newHarness(t)

	_, err := h.wf.UpdateLineItem(context.Background(), UpdateLineItemInput{
		CartID: testCartID, LineItemID: testLineA, Quantity: -1,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, CodeInvalidInput, errors.CodeOf(err))
	assert.Empty(t, h.carts.removed, "a negative quantity must NOT delete the line")
	assert.Empty(t, h.carts.quantities)
	assert.Zero(t, h.carts.snapshotCalls)
}

// TestUpdateLineItemRejectsQuantityAboveCap verifies that the quantity cap is enforced
// before the request reaches the cart.
func TestUpdateLineItemRejectsQuantityAboveCap(t *testing.T) {
	h := newHarness(t)

	_, err := h.wf.UpdateLineItem(context.Background(), UpdateLineItemInput{
		CartID: testCartID, LineItemID: testLineA, Quantity: MaxQuantity + 1,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Empty(t, h.carts.quantities)
}

// TestUpdateLineItemWriteFailureDoesNotAttemptTotals verifies that when the quantity
// cannot be written the calculation never runs at all.
func TestUpdateLineItemWriteFailureDoesNotAttemptTotals(t *testing.T) {
	h := newHarness(t)
	h.carts.setQtyFn = func(_ context.Context, _, lineItemID string, _ int64) error {
		return errors.NotFound("cart_line_item_not_found", "line item not in cart: %s", lineItemID)
	}

	_, err := h.wf.UpdateLineItem(context.Background(), UpdateLineItemInput{
		CartID: testCartID, LineItemID: testLineA, Quantity: 2,
	})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
	assert.NotEqual(t, CodeTotalsAfterChange, errors.CodeOf(err),
		"the cart did NOT change; the error must not be tagged 'applied but not computed'")
	assert.Zero(t, h.carts.snapshotCalls)
}

// TestUpdateLineItemQuantityRemainsWhenTotalsFail verifies that a failure of the second
// write does NOT roll back the quantity change.
func TestUpdateLineItemQuantityRemainsWhenTotalsFail(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts,
		snapshotOf(4, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 2}}, nil))
	h.carts.setTotalsFn = func(_ context.Context, _ string, _ json.RawMessage) error {
		return errors.Unavailable("cart_db_unavailable", "database unavailable")
	}

	_, err := h.wf.UpdateLineItem(context.Background(), UpdateLineItemInput{
		CartID: testCartID, LineItemID: testLineA, Quantity: 2,
	})
	require.Error(t, err)
	assert.Equal(t, CodeTotalsAfterChange, errors.CodeOf(err))
	assert.Equal(t, map[string]int64{testLineA: 2}, h.carts.quantities, "the quantity is not rolled back")
}

// TestUpdateLineItemRejectsInvalidIDs verifies that a malformed ID never reaches any
// module.
func TestUpdateLineItemRejectsInvalidIDs(t *testing.T) {
	tests := map[string]UpdateLineItemInput{
		"cart id empty":      {LineItemID: testLineA, Quantity: 1},
		"line item id empty": {CartID: testCartID, Quantity: 1},
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)

			_, err := h.wf.UpdateLineItem(context.Background(), in)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err))
			assert.Empty(t, h.carts.quantities)
			assert.Empty(t, h.carts.removed)
		})
	}
}
