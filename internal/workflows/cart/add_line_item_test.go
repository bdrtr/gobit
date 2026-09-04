package cart

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// addedLine holds the arguments of the line written to the fake cart.
type addedLine struct {
	cartID    string
	variantID string
	title     string
	quantity  int64
	unitPrice int64
	metadata  json.RawMessage
	calls     int
}

// recordAddLine scripts the fake cart service so that it records the added
// line.
func recordAddLine(carts *stubCarts, lineID string) *addedLine {
	seen := &addedLine{}
	carts.addLineFn = func(
		_ context.Context,
		cartID, variantID, title string,
		quantity, unitPrice int64,
		metadata json.RawMessage,
	) (string, error) {
		seen.calls++
		seen.cartID, seen.variantID, seen.title = cartID, variantID, title
		seen.quantity, seen.unitPrice, seen.metadata = quantity, unitPrice, metadata
		return lineID, nil
	}
	return seen
}

// TestAddLineItemResolvesPriceAndRefreshesTotals verifies the happy path end to
// end.
func TestAddLineItemResolvesPriceAndRefreshesTotals(t *testing.T) {
	h := newHarness(t)
	seen := recordAddLine(h.carts, testLineA)
	serveSnapshot(h.carts,
		snapshotOf(0, nil, nil),
		snapshotOf(1, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 3}}, nil),
	)

	out, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID:    testCartID,
		VariantID: testVariantA,
		Quantity:  3,
	})
	require.NoError(t, err)

	assert.Equal(t, testLineA, out.LineItemID)
	assert.Equal(t, "Red T-Shirt / M", out.Title, "the title is copied from the catalog")
	assert.Equal(t, int64(1000), out.UnitPrice)

	assert.Equal(t, addedLine{
		cartID:    testCartID,
		variantID: testVariantA,
		title:     "Red T-Shirt / M",
		quantity:  3,
		unitPrice: 1000,
		calls:     1,
	}, *seen)

	// The totals run once the line is written: 1000 x 3 = 3000, 20% tax 600.
	assert.Equal(t, int64(3000), out.Totals.Subtotal)
	assert.Equal(t, int64(600), out.Totals.TaxTotal)
	assert.Equal(t, int64(3600), out.Totals.Total)
	assert.Equal(t, int64(1), out.Totals.Revision)
	requireIdentity(t, out.Totals)
	require.Len(t, h.carts.written, 1)
}

// TestAddLineItemCarriesMetadataUnchanged verifies that the line metadata is
// carried through WITHOUT being read by the workflow.
//
// The workflow does not take it into account and must not; but it is obliged to
// carry it: this workflow is the only path that opens a line, and had it not
// been carried, the field the storefront sent would be silently dropped — "the
// setting believed to be sent but never applied" is exactly why this API
// rejects the fields it does not recognize.
func TestAddLineItemCarriesMetadataUnchanged(t *testing.T) {
	h := newHarness(t)
	seen := recordAddLine(h.carts, testLineA)
	serveSnapshot(h.carts,
		snapshotOf(0, nil, nil),
		snapshotOf(1, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil),
	)

	_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID:    testCartID,
		VariantID: testVariantA,
		Quantity:  1,
		Metadata:  json.RawMessage(`{"note":"gift wrap"}`),
	})
	require.NoError(t, err)

	assert.JSONEq(t, `{"note":"gift wrap"}`, string(seen.metadata))
}

// TestAddLineItemPriceContextCarriesQuantity verifies that the opening price is
// picked according to the requested quantity (tiered pricing).
func TestAddLineItemPriceContextCarriesQuantity(t *testing.T) {
	h := newHarness(t)
	recordAddLine(h.carts, testLineA)
	serveSnapshot(h.carts,
		snapshotOf(0, nil, nil),
		snapshotOf(1, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 5}}, nil),
	)

	_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID: testCartID, VariantID: testVariantA, Quantity: 5,
	})
	require.NoError(t, err)

	require.NotEmpty(t, h.prices.seen)
	assert.Equal(t, int32(5), h.prices.seen[0].quantity)
	assert.Equal(t, map[string]string{attrRegionID: testRegionID}, h.prices.seen[0].attributes)
}

// TestAddLineItemRejectsUnpricedVariant verifies that a variant with no price
// set DOES NOT ENTER the cart.
func TestAddLineItemRejectsUnpricedVariant(t *testing.T) {
	h := newHarness(t)
	delete(h.links.links, testVariantA)
	seen := recordAddLine(h.carts, testLineA)
	serveSnapshot(h.carts, snapshotOf(0, nil, nil))

	_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID: testCartID, VariantID: testVariantA, Quantity: 1,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, CodeVariantNotPriced, errors.CodeOf(err))
	assert.Zero(t, seen.calls, "a product with no price must not be written to the cart")
}

// TestAddLineItemRejectsVariantWithNoPriceInCurrency verifies that a variant
// which has a price set but no price in the cart's currency is rejected.
func TestAddLineItemRejectsVariantWithNoPriceInCurrency(t *testing.T) {
	h := newHarness(t)
	delete(h.prices.amounts, testPriceSetA)
	seen := recordAddLine(h.carts, testLineA)
	serveSnapshot(h.carts, snapshotOf(0, nil, nil))

	_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID: testCartID, VariantID: testVariantA, Quantity: 1,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, CodePriceUnavailable, errors.CodeOf(err))
	assert.Zero(t, seen.calls)
}

// TestAddLineItemRejectsUnknownVariant verifies that a variant that is not in
// the catalog cannot enter the cart through an orphaned price link.
func TestAddLineItemRejectsUnknownVariant(t *testing.T) {
	h := newHarness(t)
	// The variant was deleted but its price link could not be cleaned up: the
	// orphaned link scenario.
	delete(h.catalog.titles, testVariantA)
	seen := recordAddLine(h.carts, testLineA)
	serveSnapshot(h.carts, snapshotOf(0, nil, nil))

	_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID: testCartID, VariantID: testVariantA, Quantity: 1,
	})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
	assert.Equal(t, CodeVariantUnknown, errors.CodeOf(err))
	assert.Zero(t, seen.calls)
}

// TestAddLineItemRejectsCompletedCart verifies that no line can be added to a
// closed cart.
func TestAddLineItemRejectsCompletedCart(t *testing.T) {
	h := newHarness(t)
	seen := recordAddLine(h.carts, testLineA)
	snap := snapshotOf(2, nil, nil)
	snap.Completed = true
	serveSnapshot(h.carts, snap)

	_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID: testCartID, VariantID: testVariantA, Quantity: 1,
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err))
	assert.Equal(t, CodeCartCompleted, errors.CodeOf(err))
	assert.Zero(t, seen.calls)
	assert.Empty(t, h.prices.seen, "pricing must not be called for a decided request")
}

// TestAddLineItemRejectsInvalidQuantity verifies that the quantity bounds are
// applied before reaching the cart.
func TestAddLineItemRejectsInvalidQuantity(t *testing.T) {
	tests := map[string]int64{
		"zero":                  0,
		"negative":              -1,
		"above the ceiling":     MaxQuantity + 1,
		"absurdly large amount": 1 << 40,
	}

	for name, quantity := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			seen := recordAddLine(h.carts, testLineA)

			_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
				CartID: testCartID, VariantID: testVariantA, Quantity: quantity,
			})
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err))
			assert.Zero(t, seen.calls)
			assert.Zero(t, h.carts.snapshotCalls, "the cart must not be read for an input error")
		})
	}
}

// TestAddLineItemLineSTAYSWhenTotalsFail verifies that a failure of the second
// write DOES NOT roll the line back and that the error is distinguishable.
func TestAddLineItemLineSTAYSWhenTotalsFail(t *testing.T) {
	h := newHarness(t)
	seen := recordAddLine(h.carts, testLineA)
	serveSnapshot(h.carts,
		snapshotOf(0, nil, nil),
		snapshotOf(1, []SnapshotItem{{ID: testLineA, VariantID: testVariantA, Quantity: 1}}, nil),
	)
	h.carts.setTotalsFn = func(_ context.Context, _ string, _ json.RawMessage) error {
		return errors.Unavailable("cart_db_unavailable", "database unreachable")
	}

	_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID: testCartID, VariantID: testVariantA, Quantity: 1,
	})
	require.Error(t, err)
	assert.Equal(t, CodeTotalsAfterChange, errors.CodeOf(err),
		"the caller must tell that the request WAS APPLIED but the totals fell over")
	assert.Equal(t, 1, seen.calls, "the line was added and not rolled back")
	assert.Empty(t, h.carts.removed, "no line must be deleted as compensation")
}

// TestAddLineItemRejectsMalformedIdentifier verifies that a malformed
// identifier reaches no module.
func TestAddLineItemRejectsMalformedIdentifier(t *testing.T) {
	tests := map[string]AddLineItemInput{
		"empty cart":                 {VariantID: testVariantA, Quantity: 1},
		"empty variant":              {CartID: testCartID, Quantity: 1},
		"variant carries whitespace": {CartID: testCartID, VariantID: "var_a\n", Quantity: 1},
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)

			_, err := h.wf.AddLineItem(context.Background(), in)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err))
			assert.Zero(t, h.carts.snapshotCalls)
		})
	}
}

// linesUpToCeiling fills the cart with the given number of lines.
//
// All of the lines look at the SAME variant; the ceiling check looks at the
// line COUNT and at whether the variant to be added is already in the cart, not
// at the variety of the variants.
func linesUpToCeiling(variantID string, count int) []SnapshotItem {
	items := make([]SnapshotItem, 0, count)
	for i := range count {
		items = append(items, SnapshotItem{
			ID:        "li_" + strconv.Itoa(i),
			VariantID: variantID,
			Quantity:  1,
		})
	}
	return items
}

// TestAddLineItemRejectsNewLineBeyondTheLineCeiling verifies that NO new line
// can be opened on a cart that has reached the ceiling.
//
// The ceiling is not silent: the request is rejected, it is not clamped, and
// the message writes the ceiling out.
func TestAddLineItemRejectsNewLineBeyondTheLineCeiling(t *testing.T) {
	h := newHarness(t)
	serveSnapshot(h.carts, snapshotOf(1, linesUpToCeiling(testVariantA, MaxLineItems), nil))

	_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID: testCartID, VariantID: testVariantB, Quantity: 1,
	})

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "expected Invalid: %v", err)
	assert.Equal(t, CodeCartLineLimit, errors.CodeOf(err))
	assert.Contains(t, err.Error(), strconv.Itoa(MaxLineItems), "the ceiling must be visible to the operator")
	assert.Empty(t, h.catalog.specs, "a decided request must not busy the catalog")
	assert.Empty(t, h.prices.seen, "a decided request must not busy pricing")
	assert.Empty(t, h.carts.written)
}

// TestAddLineItemOpensANewLineJustBelowTheCeiling verifies that on a cart ONE
// BELOW the ceiling a new line can still be opened.
//
// The PLACE of the bound is as much a contract as the bound itself: an
// off-by-one comparison would silently reject the last line the customer is
// allowed to add.
func TestAddLineItemOpensANewLineJustBelowTheCeiling(t *testing.T) {
	h := newHarness(t)
	full := linesUpToCeiling(testVariantA, MaxLineItems-1)
	seen := recordAddLine(h.carts, testLineB)
	serveSnapshot(h.carts,
		snapshotOf(1, full, nil),
		snapshotOf(2, append(full, SnapshotItem{ID: testLineB, VariantID: testVariantB, Quantity: 1}), nil),
	)

	out, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID: testCartID, VariantID: testVariantB, Quantity: 1,
	})

	require.NoError(t, err)
	assert.Equal(t, testLineB, out.LineItemID)
	assert.Equal(t, 1, seen.calls)
	assert.Len(t, out.Totals.Lines, MaxLineItems)
}

// TestAddLineItemGrowsAnExistingLineOnACartAtTheCeiling verifies that MERGING
// is not rejected on a cart that has reached the ceiling.
//
// A merge opens no new line; had it been rejected, the owner of a full cart
// could not even raise the quantity of their own line.
func TestAddLineItemGrowsAnExistingLineOnACartAtTheCeiling(t *testing.T) {
	h := newHarness(t)
	full := linesUpToCeiling(testVariantA, MaxLineItems)
	seen := recordAddLine(h.carts, full[0].ID)
	serveSnapshot(h.carts, snapshotOf(1, full, nil), snapshotOf(2, full, nil))

	out, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
		CartID: testCartID, VariantID: testVariantA, Quantity: 2,
	})

	require.NoError(t, err)
	assert.Equal(t, full[0].ID, out.LineItemID)
	assert.Equal(t, 1, seen.calls, "the merge must be written")
}

// TestCalculateTotalsCanComputeACartAboveTheCeiling verifies that a cart which
// was opened BEFORE the ceiling was put in place, and carries more lines than
// the ceiling, can still have its totals computed.
//
// Rejecting the computation would make the customer's existing cart unpayable;
// the ceiling is applied only on the path that OPENS a line.
func TestCalculateTotalsCanComputeACartAboveTheCeiling(t *testing.T) {
	h := newHarness(t)
	large := linesUpToCeiling(testVariantA, MaxLineItems+5)
	serveSnapshot(h.carts, snapshotOf(9, large, nil))

	totals, err := h.wf.CalculateTotals(context.Background(), testCartID)

	require.NoError(t, err)
	assert.Len(t, totals.Lines, MaxLineItems+5)
	assert.Equal(t, int64(1000)*int64(MaxLineItems+5), totals.Subtotal)
	requireIdentity(t, totals)
}

// growingCart is an in-memory cart the workflow can genuinely grow.
//
// The existing fakes return scripted snapshots; the COST of adding a line,
// however, can only be counted while the cart actually grows.
type growingCart struct {
	items    []SnapshotItem
	revision int64
}

// growingCartHarness sets up a harness that knows the given number of variants
// and grows as lines are added.
func growingCartHarness(t *testing.T, variants int) *harness {
	t.Helper()

	h := newHarness(t)
	state := &growingCart{}

	h.carts.snapshotFn = func(_ context.Context, cartID string) (json.RawMessage, error) {
		snap := snapshotOf(state.revision, append([]SnapshotItem(nil), state.items...), nil)
		snap.ID = cartID
		return json.Marshal(snap)
	}
	h.carts.addLineFn = func(
		_ context.Context,
		_ string, variantID, _ string,
		quantity, _ int64,
		_ json.RawMessage,
	) (string, error) {
		id := "li_" + strconv.Itoa(len(state.items)+1)
		state.items = append(state.items, SnapshotItem{ID: id, VariantID: variantID, Quantity: quantity})
		state.revision++
		return id, nil
	}

	for i := range variants {
		variant := "var_" + strconv.Itoa(i)
		set := "pset_" + strconv.Itoa(i)
		h.prices.amounts[set] = 1000
		h.links.links[variant] = []string{set}
		h.catalog.titles[variant] = "Product " + strconv.Itoa(i)
	}
	return h
}

// TestAddLineItemCartBuildCostIsLinear verifies that the number of price round
// trips for building an N-line cart grows with N.
//
// The claim is this change itself. Every line addition runs a totals round that
// reprices ALL of the cart's lines; when the price was asked per line, the cost
// of building a cart was N² (measured: 5150 price calls for a 100-line cart).
// With the batched read, exactly TWO round trips per line addition remain: the
// single price asked while the line is opened, and the single batched question
// of the totals round.
//
// It is not the DURATION but the ROUND TRIP COUNT that is checked; a duration
// test binds to the machine, a round trip count does not.
func TestAddLineItemCartBuildCostIsLinear(t *testing.T) {
	const lineCount = 25

	h := growingCartHarness(t, lineCount)

	for i := range lineCount {
		_, err := h.wf.AddLineItem(context.Background(), AddLineItemInput{
			CartID: testCartID, VariantID: "var_" + strconv.Itoa(i), Quantity: 2,
		})
		require.NoError(t, err)
	}

	assert.Len(t, h.prices.seen, lineCount, "a single opening price per line")
	assert.Len(t, h.prices.requests, lineCount, "a single batched question per totals round")

	// That the round trips are bound to LINE ADDITION and not to the line item
	// is seen in the last round carrying the whole cart in a single question.
	assert.Len(t, h.prices.requests[lineCount-1].Items, lineCount)
}
