package fulfilling_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWhatTheReturnsAskBackGoesWithTheCeiling is ADR 0423: the ceiling's
// answer carries, per line, how many units a return or a replacement speaks
// for, and the panel's offer hands it to the module's count, which a parcel
// that came back undelivered holds its units to. A line neither names is left
// out of it.
func TestWhatTheReturnsAskBackGoesWithTheCeiling(t *testing.T) {
	t.Parallel()

	h := newDispatchHarnessWithLines(t, []testLine{
		{LineItemID: testLineID, Bought: 5, Canceled: 1, SpokenFor: 2},
		{LineItemID: "oli_kept", Bought: 3},
	}, map[string]int64{testLineID: 2})

	ceilings, spoken, err := h.DispatchCeilings(t.Context(), testOrderID, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{testLineID: 4, "oli_kept": 3}, ceilings, "returns are not taken off a ceiling")
	assert.Equal(t, map[string]int64{testLineID: 2}, spoken)

	owed, err := h.DispatchableQuantities(t.Context(), testOrderID, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{testLineID: 2, "oli_kept": 3}, owed)
	assert.Equal(t, map[string]int64{testLineID: 2}, h.fulfillments.spokenAsked,
		"the module's count is asked with what is spoken for")
}
