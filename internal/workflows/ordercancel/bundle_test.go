package ordercancel_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A gift box of one towel and two soaps, sold as the test line (ADR 0235).
const (
	testTowelID     = "variant_01CANCELTOWEL00000"
	testSoapID      = "variant_01CANCELSOAP000000"
	testTowelItemID = "invitem_01CANCELTOWEL0000"
	testSoapItemID  = "invitem_01CANCELSOAP00000"
)

// boxLinesJSON is the order's answer for a line that sold the box; the line's
// own variant, testVariantID, is the box and tracks no stock.
func boxLinesJSON(bought, canceled int64) string {
	return fmt.Sprintf(
		`[{"line_item_id":%q,"bought":%d,"canceled":%d,"variant_id":%q,"components":[`+
			`{"variant_id":%q,"quantity":1},{"variant_id":%q,"quantity":2}]}]`,
		testLineItemID, bought, canceled, testVariantID, testTowelID, testSoapID)
}

// newBoxHarness is the harness with the box's parts linked and deducted from
// the shelf, and the box itself linked to nothing, as ADR 0234 keeps it.
func newBoxHarness(t *testing.T, bought, canceled int64) *harness {
	t.Helper()
	h := newHarness(t)
	h.lines = boxLinesJSON(bought, canceled)
	h.links[linkVariantInventory] = map[string][]string{
		testTowelID: {testTowelItemID}, testSoapID: {testSoapItemID},
	}
	h.inventory.locations = map[string]string{
		testTowelItemID: testLocationID, testSoapItemID: testLocationID,
	}
	return h
}

// TestAWrittenOffBundlePutsBackItsParts is the write-off of ADR 0235: two of
// three boxes written off put back two towels and four soaps.
func TestAWrittenOffBundlePutsBackItsParts(t *testing.T) {
	t.Parallel()
	h := newBoxHarness(t, 3, 2)

	require.NoError(t, h.handle(t, canceledEvent(3, 0, 2)))
	assert.Equal(t, map[string]int64{testTowelItemID: 2, testSoapItemID: 4}, h.inventory.returnedPerItem,
		"each part comes back the line's units times its units per box")

	require.NoError(t, h.handle(t, canceledEvent(3, 0, 2)))
	assert.Equal(t, map[string]int64{testTowelItemID: 2, testSoapItemID: 4}, h.inventory.returnedPerItem,
		"a second delivery finds every part already back")
}

// TestAParcelAndAWriteOffPutBackABundlesPartsOnce is ADR 0142's invariant with
// a bundle: whichever act comes first, three boxes written off with two in a
// canceled parcel end as three towels and six soaps.
func TestAParcelAndAWriteOffPutBackABundlesPartsOnce(t *testing.T) {
	t.Parallel()

	for _, order := range []string{"the write-off first", "the parcel first"} {
		t.Run(order, func(t *testing.T) {
			t.Parallel()
			h := newBoxHarness(t, 3, 3)
			h.held = map[string]int64{testLineItemID: 2}

			if order == "the write-off first" {
				h.committed = map[string]int64{testLineItemID: 2}
				require.NoError(t, h.handle(t, canceledEvent(3, 0, 3)))
				require.Equal(t, map[string]int64{testTowelItemID: 1, testSoapItemID: 2},
					h.inventory.returnedPerItem, "only the box outside the parcel comes back yet")

				h.committed = map[string]int64{}
				require.NoError(t, h.handleParcel(t, parcelEvent(testFulfillmentID)))
			} else {
				h.committed = map[string]int64{}
				require.NoError(t, h.handleParcel(t, parcelEvent(testFulfillmentID)))
				require.NoError(t, h.handle(t, canceledEvent(3, 0, 3)))
			}

			assert.Equal(t, map[string]int64{testTowelItemID: 3, testSoapItemID: 6},
				h.inventory.returnedPerItem)
		})
	}
}

// TestABundlesPartThatTracksNoStockIsSkipped keeps a part nobody counts from
// stopping the others, as a line that tracks no stock stops nothing.
func TestABundlesPartThatTracksNoStockIsSkipped(t *testing.T) {
	t.Parallel()
	h := newBoxHarness(t, 1, 1)
	// The FIRST part tracks nothing, so an act that stopped at it would put
	// back no soap at all.
	delete(h.links[linkVariantInventory], testTowelID)

	require.NoError(t, h.handle(t, canceledEvent(1, 0, 1)))
	assert.Equal(t, map[string]int64{testSoapItemID: 2}, h.inventory.returnedPerItem)
}
