package cart

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
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
	serveSnapshot(h.carts,
		snapshotOf(4, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 2}}, nil))
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
	assert.Equal(t, 1, h.carts.snapshotCalls,
		"the snapshot is read once, to ask whether the quantity rises (ADR 0281), and never for the totals")
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

// TestRaisingALineAsksTheScopeAgain is ADR 0281: a line whose product left the
// request's channels after it entered the cart is refused a higher quantity
// with the not-found an add gets, and nothing is written; lowering it, or
// keeping it, asks nothing and is written.
func TestRaisingALineAsksTheScopeAgain(t *testing.T) {
	ctx := storefrontContext([]string{testChannelB})

	h := newHarness(t)
	h.catalog.scopedOut = map[string]bool{testVariantA: true}
	serveSnapshot(h.carts,
		snapshotOf(4, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 3}}, nil))

	_, err := h.wf.UpdateLineItem(ctx, UpdateLineItemInput{CartID: testCartID, LineItemID: testLineA, Quantity: 4})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "the same refusal an add of the variant gets: %v", err)
	assert.Empty(t, h.carts.quantities, "no quantity is written for a raise the scope refuses")

	out, err := h.wf.UpdateLineItem(ctx, UpdateLineItemInput{CartID: testCartID, LineItemID: testLineA, Quantity: 2})
	require.NoError(t, err, "a line can always be lowered")
	assert.Equal(t, int64(2), out.Quantity)

	_, err = h.wf.UpdateLineItem(ctx, UpdateLineItemInput{CartID: testCartID, LineItemID: testLineA, Quantity: 3})
	require.NoError(t, err, "a quantity that does not rise past the line's asks nothing")

	inScope := newHarness(t)
	serveSnapshot(inScope.carts,
		snapshotOf(4, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 3}}, nil))
	_, err = inScope.wf.UpdateLineItem(ctx, UpdateLineItemInput{CartID: testCartID, LineItemID: testLineA, Quantity: 9})
	require.NoError(t, err, "a product still in the channel can be raised")
	assert.Equal(t, map[string]int64{testLineA: 9}, inScope.carts.quantities)
}

// TestARaiseThatCannotReadTheCartWritesNothing: whether the quantity rises is
// read off the cart, and a cart that cannot be read refuses the update rather
// than writing a raise nobody asked the scope about (ADR 0281).
func TestARaiseThatCannotReadTheCartWritesNothing(t *testing.T) {
	h := newHarness(t)
	h.carts.snapshotFn = func(context.Context, string) (json.RawMessage, error) {
		return nil, errors.Unavailable("cart_unavailable", "the cart could not be read")
	}

	_, err := h.wf.UpdateLineItem(context.Background(), UpdateLineItemInput{
		CartID: testCartID, LineItemID: testLineA, Quantity: 4,
	})
	require.Error(t, err)
	assert.Empty(t, h.carts.quantities)
}

// testVariantC is a second add-on of the ring the raise tests hold.
const testVariantC = "var_c"

// ringOfThreeWithTwoAddOns scripts a ring of three units carrying two add-ons,
// testVariantB then testVariantC, each a variant of its own product.
func ringOfThreeWithTwoAddOns(h *harness) {
	h.catalog.titles[testVariantC] = "Gift Wrap"
	h.catalog.products[testVariantC] = "prod_c"
	h.links.links[testVariantC] = []string{testPriceSetB}
	serveSnapshot(h.carts, snapshotOf(4, []SnapshotItem{
		{ID: testLineA, VariantID: testVariantA, Quantity: 3},
		{ID: testLineB, VariantID: testVariantB, Quantity: 3, ParentLineID: testLineA},
		{ID: "li_c", VariantID: testVariantC, Quantity: 3, ParentLineID: testLineA},
	}, nil))
}

// TestRaisingALineAsksItsAddOnsListAgain is ADR 0393: a raise raises the
// line's add-ons, so each is asked whether the line's product still takes it.
// The product dropped the second add-on after the line entered the cart: the
// raise is refused as an add of it is, and nothing is written; lowering the
// line, or keeping it, asks nothing.
func TestRaisingALineAsksItsAddOnsListAgain(t *testing.T) {
	ctx := context.Background()

	h := newHarness(t)
	ringAccepts(h, testVariantB)
	ringOfThreeWithTwoAddOns(h)

	_, err := h.wf.UpdateLineItem(ctx, UpdateLineItemInput{CartID: testCartID, LineItemID: testLineA, Quantity: 4})
	require.Error(t, err)
	assert.Equal(t, CodeAddOnNotAccepted, errors.CodeOf(err), "%v", err)
	assert.Empty(t, h.carts.quantities, "no quantity is written for a raise an add-on's list refuses")

	_, err = h.wf.UpdateLineItem(ctx, UpdateLineItemInput{CartID: testCartID, LineItemID: testLineA, Quantity: 2})
	require.NoError(t, err, "a line can always be lowered")
	_, err = h.wf.UpdateLineItem(ctx, UpdateLineItemInput{CartID: testCartID, LineItemID: testLineA, Quantity: 3})
	require.NoError(t, err, "a quantity that does not rise past the line's asks nothing")

	taken := newHarness(t)
	ringAccepts(taken, testVariantB, testVariantC)
	ringOfThreeWithTwoAddOns(taken)
	_, err = taken.wf.UpdateLineItem(ctx, UpdateLineItemInput{CartID: testCartID, LineItemID: testLineA, Quantity: 9})
	require.NoError(t, err, "a line whose add-ons are all still taken can be raised")
	assert.Equal(t, map[string]int64{testLineA: 9}, taken.carts.quantities)
}

// TestRaisingALineAsksItsAddOnsChannelAgain is ADR 0393 beside ADR 0281: the
// product still takes both add-ons, but the second left the request's channels
// after it entered the cart, so the raise is refused with the 404 an add of it
// gets, and nothing is written.
func TestRaisingALineAsksItsAddOnsChannelAgain(t *testing.T) {
	ctx := storefrontContext([]string{testChannelB})

	h := newHarness(t)
	ringAccepts(h, testVariantB, testVariantC)
	ringOfThreeWithTwoAddOns(h)
	h.catalog.scopedOut = map[string]bool{testVariantC: true}

	_, err := h.wf.UpdateLineItem(ctx, UpdateLineItemInput{CartID: testCartID, LineItemID: testLineA, Quantity: 4})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "the same refusal an add of the variant gets: %v", err)
	assert.Equal(t, CodeVariantUnknown, errors.CodeOf(err))
	assert.Empty(t, h.carts.quantities, "no quantity is written for a raise an add-on's channel refuses")

	_, err = h.wf.UpdateLineItem(ctx, UpdateLineItemInput{CartID: testCartID, LineItemID: testLineA, Quantity: 2})
	require.NoError(t, err, "a line can always be lowered")
}

// TestARaiseWhoseAddOnsListCannotBeReadWritesNothing: a list that cannot be
// read is not a list that takes the add-on, so the raise is refused and
// nothing is written; a line without add-ons reads no list and is raised.
func TestARaiseWhoseAddOnsListCannotBeReadWritesNothing(t *testing.T) {
	ctx := context.Background()

	h := newHarness(t)
	ringAccepts(h, testVariantB, testVariantC)
	ringOfThreeWithTwoAddOns(h)
	h.catalog.productErr = errors.Unavailable("catalog_down", "the catalog is away")

	_, err := h.wf.UpdateLineItem(ctx, UpdateLineItemInput{CartID: testCartID, LineItemID: testLineA, Quantity: 4})
	require.Error(t, err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err), "%v", err)
	assert.Empty(t, h.carts.quantities)

	plain := newHarness(t)
	plain.catalog.productErr = errors.Unavailable("catalog_down", "the catalog is away")
	serveSnapshot(plain.carts,
		snapshotOf(4, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 3}}, nil))
	_, err = plain.wf.UpdateLineItem(ctx, UpdateLineItemInput{CartID: testCartID, LineItemID: testLineA, Quantity: 4})
	require.NoError(t, err, "a line without add-ons asks no list")
	assert.Equal(t, map[string]int64{testLineA: 4}, plain.carts.quantities)
}

// TestARaiseAsksTheListBeforeTheChannel: an add-on its product no longer takes
// and the request's channels no longer hold is refused as an add of it is, on
// the list first, with [CodeAddOnNotAccepted].
func TestARaiseAsksTheListBeforeTheChannel(t *testing.T) {
	ctx := storefrontContext([]string{testChannelB})

	h := newHarness(t)
	ringAccepts(h, testVariantB)
	ringOfThreeWithTwoAddOns(h)
	h.catalog.scopedOut = map[string]bool{testVariantC: true}

	_, err := h.wf.UpdateLineItem(ctx, UpdateLineItemInput{CartID: testCartID, LineItemID: testLineA, Quantity: 4})
	require.Error(t, err)
	assert.Equal(t, CodeAddOnNotAccepted, errors.CodeOf(err), "%v", err)
	assert.Empty(t, h.carts.quantities)
}

// TestARaiseOfALineWithoutAProductIsRefused: a line whose variant the catalog
// names no product of has no list to hold its add-ons to, so its raise is
// refused with the 404 an add of it gets, and nothing is written.
func TestARaiseOfALineWithoutAProductIsRefused(t *testing.T) {
	h := newHarness(t)
	ringAccepts(h, testVariantB, testVariantC)
	ringOfThreeWithTwoAddOns(h)
	delete(h.catalog.products, testVariantA)

	_, err := h.wf.UpdateLineItem(context.Background(),
		UpdateLineItemInput{CartID: testCartID, LineItemID: testLineA, Quantity: 4})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "%v", err)
	assert.Equal(t, CodeVariantUnknown, errors.CodeOf(err))
	assert.Empty(t, h.carts.quantities)
}

// TestARaiseAsksItsOwnAddOnsOfItsOwnProduct: the add-ons a raise asks are the
// raised line's, held to the raised line's product. The cart opens with a
// necklace whose product takes only the wrap, then holds two rings each
// engraved with testVariantB: raising the first ring asks the ring's list of
// its one engraving, and raising the necklace the necklace's list of its wrap.
// When the ring's product drops the engraving and the necklace's takes it, the
// ring's raise is refused.
func TestARaiseAsksItsOwnAddOnsOfItsOwnProduct(t *testing.T) {
	ctx := context.Background()
	const necklace, necklaceLine, secondRing = "var_necklace", "li_necklace", "li_ring_2"
	cart := func(necklaceTakes, ringTakes []string) *harness {
		h := newHarness(t)
		h.catalog.titles[necklace] = "Necklace"
		h.catalog.titles[testVariantC] = "Gift Wrap"
		h.catalog.products[necklace] = "prod_necklace"
		h.catalog.products[testVariantA] = "prod_ring"
		h.catalog.products[testVariantC] = "prod_c"
		h.links.links[necklace] = []string{testPriceSetA}
		h.links.links[testVariantC] = []string{testPriceSetB}
		h.catalog.entities = map[string][]query.Record{EntityProduct: {
			{query.IDField: "prod_necklace", FieldAddOnVariantIDs: necklaceTakes},
			{query.IDField: "prod_ring", FieldAddOnVariantIDs: ringTakes},
		}}
		serveSnapshot(h.carts, snapshotOf(4, []SnapshotItem{
			{ID: necklaceLine, VariantID: necklace, Quantity: 1},
			{ID: "li_wrap", VariantID: testVariantC, Quantity: 1, ParentLineID: necklaceLine},
			{ID: testLineA, VariantID: testVariantA, Quantity: 1},
			{ID: testLineB, VariantID: testVariantB, Quantity: 1, ParentLineID: testLineA},
			{ID: secondRing, VariantID: testVariantA, Quantity: 1},
			{ID: "li_b_2", VariantID: testVariantB, Quantity: 1, ParentLineID: secondRing},
		}, nil))
		return h
	}

	h := cart([]string{testVariantC}, []string{testVariantB})
	_, err := h.wf.UpdateLineItem(ctx, UpdateLineItemInput{CartID: testCartID, LineItemID: testLineA, Quantity: 2})
	require.NoError(t, err, "the other ring's engraving and the necklace's wrap are not this line's")
	_, err = h.wf.UpdateLineItem(ctx, UpdateLineItemInput{CartID: testCartID, LineItemID: necklaceLine, Quantity: 2})
	require.NoError(t, err, "the necklace's wrap is asked of the necklace's product")
	assert.Equal(t, map[string]int64{testLineA: 2, necklaceLine: 2}, h.carts.quantities)

	dropped := cart([]string{testVariantB, testVariantC}, nil)
	_, err = dropped.wf.UpdateLineItem(ctx, UpdateLineItemInput{CartID: testCartID, LineItemID: testLineA, Quantity: 2})
	require.Error(t, err, "the ring's product no longer takes the engraving")
	assert.Equal(t, CodeAddOnNotAccepted, errors.CodeOf(err), "%v", err)
	assert.Empty(t, dropped.carts.quantities)
}

// TestARaiseReadsTheLinesProductWithItsAddOns holds ADR 0393's cost: before it
// writes, a raise of a line with add-ons reads the line's variant (ADR 0281),
// then the line's product and the add-ons' channel in one scoped read, then
// that product's list; a raise of a line without add-ons reads its variant
// alone.
func TestARaiseReadsTheLinesProductWithItsAddOns(t *testing.T) {
	ctx := context.Background()
	readsBeforeTheWrite := func(h *harness, quantity int64) []query.GraphSpec {
		var reads []query.GraphSpec
		h.carts.setQtyFn = func(context.Context, string, string, int64) error {
			reads = slices.Clone(h.catalog.specs)
			return nil
		}
		_, err := h.wf.UpdateLineItem(ctx, UpdateLineItemInput{CartID: testCartID, LineItemID: testLineA, Quantity: quantity})
		require.NoError(t, err)
		return reads
	}

	h := newHarness(t)
	ringAccepts(h, testVariantB, testVariantC)
	ringOfThreeWithTwoAddOns(h)
	reads := readsBeforeTheWrite(h, 4)
	require.Len(t, reads, 3, "%+v", reads)
	assert.Equal(t, EntityVariant, reads[0].Entity)
	assert.Equal(t, testVariantA, reads[0].Filters[query.IDField])
	assert.Equal(t, EntityVariant, reads[1].Entity)
	assert.Equal(t, []string{testVariantA, testVariantB, testVariantC}, reads[1].Filters[FilterIDs])
	assert.Equal(t, EntityProduct, reads[2].Entity)
	assert.Equal(t, []string{"prod_ring"}, reads[2].Filters[FilterIDs])

	plain := newHarness(t)
	serveSnapshot(plain.carts,
		snapshotOf(4, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 3}}, nil))
	require.Len(t, readsBeforeTheWrite(plain, 4), 1, "a line without add-ons reads its variant alone")
}

// TestTheSnapshotCarriesAnAddOnsParent holds the wire name the cart module
// writes (cart/service/interop.go): the raise finds a line's add-ons by it.
func TestTheSnapshotCarriesAnAddOnsParent(t *testing.T) {
	snap, err := decodeSnapshot(testCartID, json.RawMessage(`{"id":"`+testCartID+`","region_id":"reg_tr","currency_code":"TRY","items":[
		{"id":"li_a","variant_id":"var_a","quantity":1},
		{"id":"li_b","variant_id":"var_b","quantity":1,"parent_line_id":"li_a"}]}`))
	require.NoError(t, err)

	require.Len(t, snap.Items, 2)
	assert.Empty(t, snap.Items[0].ParentLineID)
	assert.Equal(t, "li_a", snap.Items[1].ParentLineID)
}
