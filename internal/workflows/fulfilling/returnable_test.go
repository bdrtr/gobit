package fulfilling_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// The tests here are about what a parcel bringing a return back may hold
// (ADR 0384): the order module's return read, summed per line and refused for a
// return that is not this order's or awaits nothing.

// testReturnLine is a return line as the order module writes it.
type testReturnLine struct {
	OrderLineItemID string `json:"order_line_item_id"`
	VariantID       string `json:"variant_id"`
	Quantity        int64  `json:"quantity"`
}

// testReturn is the order module's return read as a CONSUMER writes it, with
// the fields the flow does not read as well, so a decoder that refused them
// would fail here.
type testReturn struct {
	ReturnID    string           `json:"return_id"`
	OrderID     string           `json:"order_id"`
	Status      string           `json:"status"`
	AwaitsGoods bool             `json:"awaits_goods"`
	Lines       []testReturnLine `json:"lines"`
}

// testReturnID is the return the tests read.
const testReturnID = "ret_01DISPATCHBOUNDRETURN0"

// returnHarness builds the flow over a scripted return.
func returnHarness(t *testing.T, ret testReturn) *dispatchHarness {
	t.Helper()

	h := newDispatchHarness(t, 5, 0, 0)
	raw, err := json.Marshal(ret)
	require.NoError(t, err)
	h.orders.returnDetail = raw

	return h
}

// TestWhatAReturnBringsBack sums the return's lines per order line.
func TestWhatAReturnBringsBack(t *testing.T) {
	t.Parallel()

	h := returnHarness(t, testReturn{
		ReturnID: testReturnID, OrderID: testOrderID, Status: "requested", AwaitsGoods: true,
		Lines: []testReturnLine{
			{OrderLineItemID: "oli_A", VariantID: "variant_A", Quantity: 1},
			{OrderLineItemID: "oli_A", VariantID: "variant_A", Quantity: 1},
			{OrderLineItemID: "oli_B", VariantID: "variant_B", Quantity: 1},
		},
	})

	awaited, lines, err := h.ReturnLines(t.Context(), testOrderID, testReturnID)

	require.NoError(t, err)
	assert.True(t, awaited, "a requested return of this order awaits its goods")
	assert.Equal(t, map[string]int64{"oli_A": 2, "oli_B": 1}, lines,
		"a line named twice is brought back twice, and no line is dropped")
}

// TestAReturnOfAnotherOrderAwaitsNothingOnThisOne holds the parcel to its own
// order: another order's return would bound it by goods this order never sold.
func TestAReturnOfAnotherOrderAwaitsNothingOnThisOne(t *testing.T) {
	t.Parallel()

	h := returnHarness(t, testReturn{
		ReturnID: testReturnID, OrderID: "order_01SOMEBODYELSE00000000", Status: "requested",
		AwaitsGoods: true,
		Lines:       []testReturnLine{{OrderLineItemID: "oli_A", Quantity: 1}},
	})

	awaited, lines, err := h.ReturnLines(t.Context(), testOrderID, testReturnID)

	require.NoError(t, err)
	assert.False(t, awaited)
	assert.Empty(t, lines)
}

// TestAReturnTheOrderModuleSaysIsDoneAwaitsNothing reads the order module's own
// answer, not its status word: the status here still says "requested", and only
// awaits_goods says the goods are no longer expected (the D59 class).
func TestAReturnTheOrderModuleSaysIsDoneAwaitsNothing(t *testing.T) {
	t.Parallel()

	h := returnHarness(t, testReturn{
		ReturnID: testReturnID, OrderID: testOrderID, Status: "requested", AwaitsGoods: false,
		Lines: []testReturnLine{{OrderLineItemID: "oli_A", Quantity: 1}},
	})

	awaited, lines, err := h.ReturnLines(t.Context(), testOrderID, testReturnID)

	require.NoError(t, err)
	assert.False(t, awaited)
	assert.Empty(t, lines)
}

// TestAnUnreadableReturnKeepsItsKind keeps an unknown return a not-found, so it
// is not read as "awaits nothing" and answered with a conflict.
func TestAnUnreadableReturnKeepsItsKind(t *testing.T) {
	t.Parallel()

	h := newDispatchHarness(t, 5, 0, 0)
	h.orders.returnErr = coreerrors.NotFound("order_return_not_found", "no such return")

	awaited, _, err := h.ReturnLines(t.Context(), testOrderID, testReturnID)

	require.Error(t, err)
	assert.False(t, awaited)
	assert.Equal(t, coreerrors.KindNotFound, coreerrors.KindOf(err))
}
