package returns

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// TestAReturnedBundlePutsBackItsParts is ADR 0235's receipt: two gift boxes
// coming back put back two towels and four soaps, and the box, which tracks
// no stock of its own, is not asked about.
func TestAReturnedBundlePutsBackItsParts(t *testing.T) {
	h := newHarness(t)
	h.orders.detail.Lines = []returnLine{{
		OrderLineItemID: "oli_box", VariantID: "var_box", Quantity: 2,
		Components: []returnLineComponent{
			{VariantID: testVariantA, Quantity: 1},
			{VariantID: testVariantB, Quantity: 2},
		},
	}}

	out, err := h.wf.ReceiveReturn(context.Background(), testReturnID, testLocationID)
	require.NoError(t, err)

	assert.Empty(t, out.Warnings, "the box's own variant is no warning: its parts are what came back")
	assert.Equal(t, 1, out.RestockedLines)
	assert.Equal(t, int64(6), out.RestockedUnits)
	assert.Equal(t, []restockCall{
		{itemID: testItemA, locationID: testLocationID, quantity: 2},
		{itemID: testItemB, locationID: testLocationID, quantity: 4},
	}, h.inventory.calls)
}

// TestABundleWithAPartThatHasNoItemIsNotWhollyRestocked keeps the count
// honest: the part that went back is counted, the one that could not is a
// warning, and the line is not reported restocked.
func TestABundleWithAPartThatHasNoItemIsNotWhollyRestocked(t *testing.T) {
	h := newHarness(t)
	delete(h.links.links, testVariantB)
	h.orders.detail.Lines = []returnLine{{
		OrderLineItemID: "oli_box", VariantID: "var_box", Quantity: 1,
		Components: []returnLineComponent{
			{VariantID: testVariantA, Quantity: 1},
			{VariantID: testVariantB, Quantity: 2},
		},
	}}

	out, err := h.wf.ReceiveReturn(context.Background(), testReturnID, testLocationID)
	require.NoError(t, err)

	assert.Len(t, out.Warnings, 1)
	assert.Equal(t, 0, out.RestockedLines)
	assert.Equal(t, int64(1), out.RestockedUnits)
}

// boxDispatchHarness is the dispatch harness with the box as its first line:
// two boxes of a towel and two soaps, beside the harness's second line. The
// box's own variant is linked to no item, as a bundle never is.
func boxDispatchHarness(t *testing.T) *harness {
	t.Helper()
	h := dispatchHarness(t)
	h.orders.replacement.Lines[0] = replacementLine{
		ReplacementItemID: "oreplitem_box", OrderLineItemID: "oli_box", VariantID: "var_box", Quantity: 2,
		Parts: []replacementPart{
			{VariantID: testVariantA, Quantity: 1},
			{VariantID: testVariantB, Quantity: 2},
		},
	}
	return h
}

// TestAReplacedBoxIsSentFromItsParts is ADR 0238's dispatch: each part is set
// aside for the boxes times its units and held under its own promise, written
// on the part, and each promise leaves the count after the parcel opens.
func TestAReplacedBoxIsSentFromItsParts(t *testing.T) {
	h := boxDispatchHarness(t)

	result, err := h.wf.DispatchReplacement(context.Background(), testReplacementID)
	require.NoError(t, err, "the box's own variant has no item and that is no refusal")

	assert.Equal(t, []reserveCall{
		{itemID: testItemA, locationID: testLocationID, quantity: 2, lineItemID: "oli_box"},
		{itemID: testItemB, locationID: testLocationID, quantity: 4, lineItemID: "oli_box"},
		{itemID: testItemB, locationID: testLocationID, quantity: 1, lineItemID: "oli_b"},
	}, h.inventory.reserveCalls)
	assert.Equal(t, []reservationCall{
		{replacementID: testReplacementID, itemID: "oreplitem_box", variantID: testVariantA, reservationID: "invres_1"},
		{replacementID: testReplacementID, itemID: "oreplitem_box", variantID: testVariantB, reservationID: "invres_2"},
		{replacementID: testReplacementID, itemID: "oreplitem_b", variantID: testVariantB, reservationID: "invres_3"},
	}, h.orders.reservationCalls)
	assert.Equal(t, []string{"invres_1", "invres_2", "invres_3"}, h.inventory.confirmed)
	assert.Equal(t, int64(7), result.SentUnits, "two towels, four soaps and the other line's one")
	require.Len(t, h.orders.dispatchCalls, 1)
}

// TestAPartAlreadyHeldIsNotHeldAgain is the retry at the part: the promise an
// earlier attempt wrote on the towel is confirmed, and only the soaps are set
// aside.
func TestAPartAlreadyHeldIsNotHeldAgain(t *testing.T) {
	h := boxDispatchHarness(t)
	h.orders.replacement.Lines = h.orders.replacement.Lines[:1]
	h.orders.replacement.Lines[0].Parts[0].ReservationID = "invres_earlier"

	result, err := h.wf.DispatchReplacement(context.Background(), testReplacementID)
	require.NoError(t, err)

	assert.Equal(t, []reserveCall{
		{itemID: testItemB, locationID: testLocationID, quantity: 4, lineItemID: "oli_box"},
	}, h.inventory.reserveCalls)
	assert.Equal(t, []string{"invres_earlier", "invres_1"}, h.inventory.confirmed)
	assert.Equal(t, int64(6), result.SentUnits)
}

// TestAPartNoWarehouseStocksStopsTheBox refuses the dispatch before a parcel
// opens, as a line with no item does; the part already held stays held for
// the withdrawal to give back.
func TestAPartNoWarehouseStocksStopsTheBox(t *testing.T) {
	h := boxDispatchHarness(t)
	delete(h.links.links, testVariantB)

	_, err := h.wf.DispatchReplacement(context.Background(), testReplacementID)
	require.Error(t, err)
	assert.Equal(t, CodeNoInventoryItem, coreerrors.CodeOf(err))
	assert.Contains(t, err.Error(), testVariantB, "the refusal names the part, not the box")

	require.Len(t, h.inventory.reserveCalls, 1, "the towel was set aside before the soap was found wanting")
	assert.Empty(t, h.shipping.calls, "no parcel opens for a box that cannot be made up")
	assert.Empty(t, h.inventory.confirmed)
}

// TestAWithdrawnBoxGivesBackEveryPart is ADR 0237's release at the part:
// every promise a box's parts hold goes back before the record is withdrawn.
func TestAWithdrawnBoxGivesBackEveryPart(t *testing.T) {
	h := boxDispatchHarness(t)
	h.orders.replacement.Lines[0].Parts[0].ReservationID = "invres_towel"
	h.orders.replacement.Lines[0].Parts[1].ReservationID = "invres_soap"
	h.orders.replacement.Lines[1].ReservationID = "invres_line"

	out, err := h.wf.WithdrawReplacement(context.Background(), testReplacementID)
	require.NoError(t, err)

	assert.Equal(t, []string{"invres_towel", "invres_soap", "invres_line"}, h.inventory.released)
	assert.Equal(t, 3, out.ReleasedPromises)
	assert.Equal(t, []string{testReplacementID}, h.orders.canceled)
}
