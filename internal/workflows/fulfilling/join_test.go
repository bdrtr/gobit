package fulfilling_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/workflows/fulfilling"
)

// oneAddedUnit names one unit of the addition's line.
var oneAddedUnit = []fulfilling.OpenItem{{LineItemID: "li_added", Quantity: 1}}

// joinFixture is a parent with one parcel, and the flow over it. The addition
// was sold no delivery of its own, so it names what it puts in.
func joinFixture(t *testing.T, status string) (*fulfilling.Workflows, *fakeOrders, *fakeLinks, *fakeFulfillments) {
	t.Helper()

	links := newFakeLinks()
	links.bound["order_parent"] = []string{"ful_parent"}
	orders := &fakeOrders{parent: "order_parent"}
	ful := &fakeFulfillments{status: status, links: links}
	flow := newFlow(t, orders, ful, links)

	return flow, orders, links, ful
}

// TestAnAdditionJoinsItsParentsWaitingParcel binds the addition beside the
// parent, and a second call binds nothing new (ADR 0197). Each call hands the
// module the units named, before the addition is bound (ADR 0428).
func TestAnAdditionJoinsItsParentsWaitingParcel(t *testing.T) {
	t.Parallel()

	flow, _, links, ful := joinFixture(t, "pending")

	require.NoError(t, flow.ShipInParcel(context.Background(), "order_addition", "ful_parent", oneAddedUnit))
	require.NoError(t, flow.ShipInParcel(context.Background(), "order_addition", "ful_parent", oneAddedUnit))

	assert.Equal(t, []string{"ful_parent"}, links.bound["order_addition"],
		"the addition is bound to the parent's parcel, once")
	assert.Equal(t, []string{"ful_parent"}, links.bound["order_parent"], "the parent keeps it")
	require.Len(t, ful.joins, 2, "the module is asked each time")
	join := ful.joins[0]
	assert.Equal(t, [3]string{"ful_parent", "order_parent", "order_addition"},
		[3]string{join.parcel, join.parent, join.addition})
	assert.JSONEq(t, `[{"line_item_id":"li_added","quantity":1}]`, string(join.items), "the units named")
	assert.False(t, join.itemsOwed, "an addition sold no delivery is not allowed what it owes")
	assert.Equal(t, []bool{false, true}, ful.boundBeforeJoin,
		"the goods go into the parcel before the addition is bound to it")
}

// TestAnAdditionJoinsNoOtherParcel refuses a parcel that is not the parent's
// before the module is asked, and one no longer pending with the flow's code;
// neither binds anything.
func TestAnAdditionJoinsNoOtherParcel(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, status, parcel, code string
		asked                      int
	}{
		{"another order's parcel", "pending", "ful_other", fulfilling.CodeParcelNotParents, 0},
		{"a shipped parcel", "shipped", "ful_parent", fulfilling.CodeParcelNotWaiting, 1},
		{"a canceled parcel", "canceled", "ful_parent", fulfilling.CodeParcelNotWaiting, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			flow, _, links, ful := joinFixture(t, tc.status)

			err := flow.ShipInParcel(context.Background(), "order_addition", tc.parcel, oneAddedUnit)

			require.Error(t, err)
			assert.True(t, coreerrors.IsConflict(err), "%v", err)
			assert.Equal(t, tc.code, coreerrors.CodeOf(err))
			assert.Empty(t, links.bound["order_addition"], "a refused join binds nothing")
			assert.Len(t, ful.joins, tc.asked, "the module answers whether the addition is in the parcel")
		})
	}
}

// TestAnAdditionNotSoldOneDeliveryNamesItsItems refuses a join naming no unit
// for an addition not sold exactly one delivery on a shipping option, with the
// open's code, and lets one that was ask for what it owes (ADR 0409, 0428).
func TestAnAdditionNotSoldOneDeliveryNamesItsItems(t *testing.T) {
	t.Parallel()

	flow, _, links, ful := joinFixture(t, "pending")

	err := flow.ShipInParcel(context.Background(), "order_addition", "ful_parent", nil)

	require.Error(t, err)
	assert.Equal(t, fulfilling.CodeItemsRequired, coreerrors.CodeOf(err))
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err))
	assert.Empty(t, links.bound["order_addition"], "a refused join binds nothing")

	sold, orders, soldLinks, soldFul := joinFixture(t, "pending")
	orders.sold = "so_one"
	require.NoError(t, sold.ShipInParcel(context.Background(), "order_addition", "ful_parent", nil))
	require.Len(t, soldFul.joins, 1)
	assert.Empty(t, soldFul.joins[0].items, "no unit is named")
	assert.True(t, soldFul.joins[0].itemsOwed, "an addition sold one delivery may ask for what it owes")
	assert.Equal(t, []string{"ful_parent"}, soldLinks.bound["order_addition"])
	assert.False(t, ful.joins[0].itemsOwed, "the first addition was not allowed it")
}

// TestTheModulesRefusalOfAJoinBindsNothing passes the fulfillment module's
// conflict through as it is, an addition that owes nothing among them, and
// wraps any other fault in the flow's code with its kind; neither binds the
// addition to the parcel (ADR 0428).
func TestTheModulesRefusalOfAJoinBindsNothing(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		err      error
		code     string
		conflict bool
	}{
		{"owes nothing", coreerrors.Conflict("fulfillment_nothing_owed", "owes nothing"),
			"fulfillment_nothing_owed", true},
		{"unreachable", coreerrors.Unavailable("fulfillment_down", "down"),
			fulfilling.CodeCreateFailed, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			flow, _, links, ful := joinFixture(t, "pending")
			ful.joinErr = tc.err

			err := flow.ShipInParcel(context.Background(), "order_addition", "ful_parent", oneAddedUnit)

			require.Error(t, err)
			assert.Equal(t, tc.code, coreerrors.CodeOf(err))
			assert.Equal(t, coreerrors.KindOf(tc.err), coreerrors.KindOf(err), "the kind is kept")
			assert.Equal(t, tc.conflict, coreerrors.IsConflict(err))
			assert.Empty(t, links.bound["order_addition"], "a refused join binds nothing")
		})
	}
}

// TestABindingThatFailedIsWrittenAgainAfterTheParcelLeft reports the failed
// binding with its own code, and a repeated call binds the addition though the
// parcel has shipped since: the module answers the repeat from the parcel in
// any state (ADR 0428).
func TestABindingThatFailedIsWrittenAgainAfterTheParcelLeft(t *testing.T) {
	t.Parallel()

	flow, _, links, ful := joinFixture(t, "pending")
	links.createErr = coreerrors.Unavailable("link_down", "down")

	err := flow.ShipInParcel(context.Background(), "order_addition", "ful_parent", oneAddedUnit)
	require.Error(t, err)
	assert.Equal(t, fulfilling.CodeLinkFailed, coreerrors.CodeOf(err))
	assert.Contains(t, err.Error(), "ful_parent", "the message names the parcel the goods are in")

	links.createErr = nil
	ful.status = "shipped"
	require.NoError(t, flow.ShipInParcel(context.Background(), "order_addition", "ful_parent", oneAddedUnit))
	assert.Equal(t, []string{"ful_parent"}, links.bound["order_addition"], "the binding is written")
	assert.Len(t, ful.joins, 2, "the module is asked again, and its repeat changes nothing")
}

// TestARepeatNamingOtherUnitsIsNotAWaitingParcel passes the module's refusal
// of a repeat naming other units through as it is, though the parcel has left:
// the flow answers a parcel that left with its own code only when the module
// refuses it for its state (ADR 0428).
func TestARepeatNamingOtherUnitsIsNotAWaitingParcel(t *testing.T) {
	t.Parallel()

	flow, _, links, ful := joinFixture(t, "pending")
	require.NoError(t, flow.ShipInParcel(context.Background(), "order_addition", "ful_parent", oneAddedUnit))
	ful.status = "shipped"

	err := flow.ShipInParcel(context.Background(), "order_addition", "ful_parent",
		[]fulfilling.OpenItem{{LineItemID: "li_added", Quantity: 2}})

	require.Error(t, err)
	assert.Equal(t, "fulfillment_join_items_differ", coreerrors.CodeOf(err))
	assert.True(t, coreerrors.IsConflict(err))
	assert.Equal(t, []string{"ful_parent"}, links.bound["order_addition"], "the first join's binding stands")
}

// TestTheOrderModulesRefusalStopsTheJoin passes the order module's answer
// through as it is, before any parcel is read.
func TestTheOrderModulesRefusalStopsTheJoin(t *testing.T) {
	t.Parallel()

	flow, orders, links, ful := joinFixture(t, "pending")
	orders.parentErr = coreerrors.Conflict("order_ships_elsewhere", "another address")

	err := flow.ShipInParcel(context.Background(), "order_addition", "ful_parent", oneAddedUnit)

	require.Error(t, err)
	assert.Equal(t, "order_ships_elsewhere", coreerrors.CodeOf(err))
	assert.Empty(t, links.bound["order_addition"])
	assert.Empty(t, ful.joins)
}

// TestTheInteropReadsTheJoinsItems decodes the order route's body into the
// units named, and an empty body as none (ADR 0428).
func TestTheInteropReadsTheJoinsItems(t *testing.T) {
	t.Parallel()

	flow, _, _, ful := joinFixture(t, "pending")
	interop := fulfilling.NewInterop(flow)

	require.NoError(t, interop.ShipInParcel(context.Background(), "order_addition", "ful_parent",
		json.RawMessage(`{"items":[{"line_item_id":"li_added","quantity":1}]}`)))
	require.Len(t, ful.joins, 1)
	assert.JSONEq(t, `[{"line_item_id":"li_added","quantity":1}]`, string(ful.joins[0].items))

	err := interop.ShipInParcel(context.Background(), "order_other", "ful_parent", json.RawMessage(`{}`))
	assert.Equal(t, fulfilling.CodeItemsRequired, coreerrors.CodeOf(err), "an empty body names no unit")
	err = interop.ShipInParcel(context.Background(), "order_other", "ful_parent", json.RawMessage(`{"items":7}`))
	assert.Equal(t, fulfilling.CodeInvalidInput, coreerrors.CodeOf(err))
}
