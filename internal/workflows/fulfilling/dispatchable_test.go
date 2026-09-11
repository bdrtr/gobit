package fulfilling_test

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/workflows/fulfilling"
)

// TestWhatAParcelMayStillHold is the arithmetic in one table.
//
//	dispatchable = bought − canceled − committed
//
// Committed is what a LIVE parcel holds: a canceled parcel's goods never left, so
// its units are still dispatchable (the fulfillment module's own query decides that
// and this flow does not repeat the rule).
func TestWhatAParcelMayStillHold(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                        string
		bought, canceled, committed int64
		want                        int64
	}{
		{name: "nothing shipped, nothing canceled", bought: 5, want: 5},
		{name: "two canceled", bought: 5, canceled: 2, want: 3},
		{name: "three already in a parcel", bought: 5, committed: 3, want: 2},
		{name: "two canceled and three in a parcel", bought: 5, canceled: 2, committed: 3, want: 0},
		{name: "everything canceled", bought: 5, canceled: 5, want: 0},
		{
			name:   "more in parcels than sold, which a bad parcel would make",
			bought: 2, committed: 3, want: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDispatchHarness(t, tc.bought, tc.canceled, tc.committed)

			owed, err := h.DispatchableQuantities(t.Context(), testOrderID, []string{testLineID})
			require.NoError(t, err)

			assert.Equal(t, tc.want, owed[testLineID],
				"bought %d, canceled %d, in a live parcel %d",
				tc.bought, tc.canceled, tc.committed)
		})
	}
}

// TestALineTheOrderDoesNotHaveIsABSENT holds the signal the caller reads as a
// refusal.
//
// It has to be absence rather than zero: a fully shipped line answers zero, and a
// caller that could not tell the two apart would either refuse a legitimate parcel
// or open one for goods no order sold.
func TestALineTheOrderDoesNotHaveIsABSENT(t *testing.T) {
	t.Parallel()

	h := newDispatchHarness(t, 5, 0, 0)

	owed, err := h.DispatchableQuantities(t.Context(), testOrderID,
		[]string{testLineID, "oli_NOT_ON_THE_ORDER"})
	require.NoError(t, err)

	assert.Contains(t, owed, testLineID)
	assert.NotContains(t, owed, "oli_NOT_ON_THE_ORDER",
		"a line the order does not have is left OUT; zero would be the answer of a "+
			"line that is fully shipped")
}

// TestOnlyTheLinesASKEDAboutAreAnswered keeps the answer from growing with the order.
func TestOnlyTheLinesASKEDAboutAreAnswered(t *testing.T) {
	t.Parallel()

	h := newDispatchHarnessWithLines(t, []testLine{
		{LineItemID: testLineID, Bought: 5},
		{LineItemID: "oli_other", Bought: 7},
	}, nil)

	owed, err := h.DispatchableQuantities(t.Context(), testOrderID, []string{testLineID})
	require.NoError(t, err)

	assert.Equal(t, map[string]int64{testLineID: 5}, owed,
		"the caller is opening ONE parcel; the rest of the order says nothing more")
}

// TestAnOrderWithNoParcelsOwesEverything is the ordinary first dispatch.
func TestAnOrderWithNoParcelsOwesEverything(t *testing.T) {
	t.Parallel()

	h := newDispatchHarnessWithLines(t, []testLine{{LineItemID: testLineID, Bought: 4}}, nil)
	h.links.bound = map[string][]string{}

	owed, err := h.DispatchableQuantities(t.Context(), testOrderID, []string{testLineID})
	require.NoError(t, err)
	assert.Equal(t, int64(4), owed[testLineID],
		"no parcels, so the fulfillment module is not even asked")
	assert.Zero(t, h.fulfillments.committedCalls,
		"and it really was not asked: a link read that came back empty is the answer")
}

// TestAFaultInEitherSideIsREPORTED keeps a failure from reading as "owes nothing".
//
// Answering an empty map on a fault would be the worst possible shape: the caller
// reads absence as a refusal, so a shop whose order module was briefly unreachable
// would be told the line is not on the order.
func TestAFaultInEitherSideIsREPORTED(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		break_ func(*dispatchHarness)
	}{
		{name: "the order cannot be read", break_: func(h *dispatchHarness) {
			h.orders.linesErr = errors.New("the order module is unreachable")
		}},
		{name: "the parcels cannot be read", break_: func(h *dispatchHarness) {
			h.links.listErr = errors.New("the link service is unreachable")
		}},
		{name: "what the parcels hold cannot be read", break_: func(h *dispatchHarness) {
			h.fulfillments.committedErr = errors.New("the fulfillment module is unreachable")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDispatchHarness(t, 5, 0, 0)
			tc.break_(h)

			_, err := h.DispatchableQuantities(t.Context(), testOrderID, []string{testLineID})

			require.Error(t, err)
			assert.Contains(t, err.Error(), fulfilling.CodeDispatchableUnknown)
		})
	}
}

// The identifiers the dispatch tests share.
const (
	testOrderID = "order_01DISPATCHBOUND000000"
	testLineID  = "oli_01DISPATCHBOUNDLINE00"
)

// dispatchHarness is the flow over fakes, with the fakes reachable.
type dispatchHarness struct {
	*fulfilling.Workflows

	orders       *fakeOrders
	fulfillments *fakeFulfillments
	links        *fakeLinks
}

// newDispatchHarness is one line of one order, with the three counts spelled out.
func newDispatchHarness(t *testing.T, bought, canceled, committed int64) *dispatchHarness {
	t.Helper()

	return newDispatchHarnessWithLines(t,
		[]testLine{{LineItemID: testLineID, Bought: bought, Canceled: canceled}},
		map[string]int64{testLineID: committed})
}

// newDispatchHarnessWithLines is [newDispatchHarness] with the order's lines given.
func newDispatchHarnessWithLines(
	t *testing.T, lines []testLine, committed map[string]int64,
) *dispatchHarness {
	t.Helper()

	orders := &fakeOrders{lines: lines}
	fulfillments := &fakeFulfillments{id: "ful_1", committed: committed}
	links := &fakeLinks{bound: map[string][]string{testOrderID: {"ful_1"}}}

	flow, err := fulfilling.New(fulfilling.Deps{
		Orders:       orders,
		Fulfillments: fulfillments,
		Links:        links,
		Logger:       slog.New(slog.DiscardHandler),
	})
	require.NoError(t, err)

	return &dispatchHarness{
		Workflows: flow, orders: orders, fulfillments: fulfillments, links: links,
	}
}
