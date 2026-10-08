package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// joinSetup is a parent's pending parcel holding two units of its line, and an
// addition that may ship three units of one line and one of another, one of
// the three already in a parcel of its own (ADR 0428).
func joinSetup(t *testing.T) testSetup {
	t.Helper()

	setup := newSetup(t)
	setup.store.putLiveParcel("ful_parent", "order_parent", "li_parent", 2)
	setup.store.putLiveParcel("ful_own", "order_addition", "li_a", 1)
	setup.bound.owed = map[string]int64{"li_a": 3, "li_b": 1, "li_none": 0}
	setup.store.resetLocks()

	return setup
}

// owedJoin is the addition joining the parent's parcel asking for what it owes,
// as one sold exactly one delivery may.
func owedJoin() service.JoinParcelInput {
	return service.JoinParcelInput{
		FulfillmentID: "ful_parent", ParentReference: "order_parent", AdditionReference: "order_addition",
		ItemsOwed: true,
	}
}

// itemsOf is a parcel's items as line id to units, per order they belong to.
func itemsOf(t *testing.T, setup testSetup, fulfillmentID string) map[string]map[string]int64 {
	t.Helper()

	items, err := setup.store.ListFulfillmentItems(context.Background(), fulfillmentID)
	require.NoError(t, err)
	out := map[string]map[string]int64{}
	for _, item := range items {
		if out[item.Reference] == nil {
			out[item.Reference] = map[string]int64{}
		}
		out[item.Reference][item.LineItemID] += item.Quantity
	}

	return out
}

// TestAJoinPutsWhatTheAdditionOwesIntoTheParcel is ADR 0428: every line's
// units the addition still owes become items of the parent's parcel that the
// addition owns, and the counts read them for the addition and not the parent.
func TestAJoinPutsWhatTheAdditionOwesIntoTheParcel(t *testing.T) {
	t.Parallel()

	setup := joinSetup(t)
	ctx := context.Background()

	joined, err := setup.svc.JoinParcel(ctx, owedJoin())
	require.NoError(t, err)

	assert.Equal(t, map[string]map[string]int64{
		"order_parent":   {"li_parent": 2},
		"order_addition": {"li_a": 2, "li_b": 1},
	}, itemsOf(t, setup, "ful_parent"), "the addition's two owed units of li_a and one of li_b join the box")
	require.Len(t, joined, 2)
	assert.Equal(t, "li_a", joined[0].LineItemID, "the lines are written in line id order")
	assert.Equal(t, []string{"order_addition"}, setup.bound.orders, "the addition's ceiling is read")
	assert.Equal(t, []string{"dispatch:order_addition", "fulfillment"}, setup.store.lockOrder(),
		"the addition's dispatch lock, then the parcel's row")

	held, err := setup.svc.CommittedQuantitiesForReference(ctx, "order_addition", nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"li_a": 3, "li_b": 1}, held, "the addition's units count for it")
	held, err = setup.svc.CommittedQuantitiesForReference(ctx, "order_parent", nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"li_parent": 2}, held, "and not for the parent")
}

// TestARepeatedJoinChangesNothing answers a second join of the same pair with
// the items the first wrote, and writes none, though the ceiling grew.
func TestARepeatedJoinChangesNothing(t *testing.T) {
	t.Parallel()

	setup := joinSetup(t)
	ctx := context.Background()

	first, err := setup.svc.JoinParcel(ctx, owedJoin())
	require.NoError(t, err)
	setup.bound.owed = map[string]int64{"li_a": 5, "li_b": 1}

	again, err := setup.svc.JoinParcel(ctx, owedJoin())
	require.NoError(t, err)
	assert.ElementsMatch(t, first, again, "the repeat answers what the first join wrote")
	assert.Equal(t, map[string]int64{"li_a": 2, "li_b": 1}, itemsOf(t, setup, "ful_parent")["order_addition"],
		"no unit is put in twice")
}

// TestAnAdditionThatOwesNothingDoesNotJoin refuses a join of an addition whose
// every unit is written off or in a parcel, and writes nothing.
func TestAnAdditionThatOwesNothingDoesNotJoin(t *testing.T) {
	t.Parallel()

	setup := joinSetup(t)
	setup.bound.owed = map[string]int64{"li_a": 1, "li_b": 0}

	_, err := setup.svc.JoinParcel(context.Background(), owedJoin())

	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "%v", err)
	assert.Equal(t, service.CodeNothingOwed, errors.CodeOf(err))
	assert.Equal(t, map[string]map[string]int64{"order_parent": {"li_parent": 2}},
		itemsOf(t, setup, "ful_parent"), "the parcel holds what it held")
}

// TestAJoinCountsWhatAnOpenCommittedWhileItWaited counts the addition's parcels
// under its lock: a parcel of the addition that commits while the join waits
// for the lock is counted, and the join takes only what it leaves.
func TestAJoinCountsWhatAnOpenCommittedWhileItWaited(t *testing.T) {
	t.Parallel()

	setup := joinSetup(t)
	setup.store.onLock = func(f *fakeStore) {
		f.onLock = nil
		f.putLiveParcel("ful_raced", "order_addition", "li_b", 1)
	}

	_, err := setup.svc.JoinParcel(context.Background(), owedJoin())
	require.NoError(t, err)

	assert.Equal(t, map[string]int64{"li_a": 2}, itemsOf(t, setup, "ful_parent")["order_addition"],
		"li_b's one unit is in the parcel that committed first")
}

// TestAJoinIsRefusedWhereTheParcelCannotTakeIt refuses a parcel no longer
// pending, one not opened for the parent, one bringing a return back, an
// order joining its own parcel and a ceiling that cannot be read; none of
// them writes an item.
func TestAJoinIsRefusedWhereTheParcelCannotTakeIt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name           string
		arrange        func(setup testSetup)
		parcel, parent string
		addition, code string
		kind           errors.Kind
	}{
		{
			name:    "a shipped parcel",
			arrange: func(setup testSetup) { setup.store.setParcelStatus("ful_parent", models.StatusShipped) },
			parcel:  "ful_parent", parent: "order_parent", addition: "order_addition",
			code: service.CodeInvalidTransition, kind: errors.KindConflict,
		},
		{
			name:    "a canceled parcel",
			arrange: func(setup testSetup) { setup.store.cancelParcel("ful_parent") },
			parcel:  "ful_parent", parent: "order_parent", addition: "order_addition",
			code: service.CodeInvalidTransition, kind: errors.KindConflict,
		},
		{
			name:    "another order's parcel",
			arrange: func(testSetup) {},
			parcel:  "ful_own", parent: "order_parent", addition: "order_addition",
			code: service.CodeInvalidInput, kind: errors.KindInvalid,
		},
		{
			name: "a parcel bringing a return back",
			arrange: func(setup testSetup) {
				setup.store.putLiveReturnParcel("ful_back", "order_parent", "ret_1", "li_parent", 1)
			},
			parcel: "ful_back", parent: "order_parent", addition: "order_addition",
			code: service.CodeInvalidInput, kind: errors.KindInvalid,
		},
		{
			name:    "the parent joining its own parcel",
			arrange: func(testSetup) {},
			parcel:  "ful_parent", parent: "order_parent", addition: "order_parent",
			code: service.CodeInvalidInput, kind: errors.KindInvalid,
		},
		{
			name: "a ceiling that cannot be read",
			arrange: func(setup testSetup) {
				setup.bound.err = errors.Unavailable("flow_down", "the flow is down")
			},
			parcel: "ful_parent", parent: "order_parent", addition: "order_addition",
			code: service.CodeDispatchBoundUnknown, kind: errors.KindUnavailable,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			setup := joinSetup(t)
			tc.arrange(setup)
			before := itemsOf(t, setup, tc.parcel)

			_, err := setup.svc.JoinParcel(context.Background(), service.JoinParcelInput{
				FulfillmentID: tc.parcel, ParentReference: tc.parent, AdditionReference: tc.addition,
				ItemsOwed: true,
			})

			require.Error(t, err)
			assert.Equal(t, tc.code, errors.CodeOf(err), "%v", err)
			assert.Equal(t, tc.kind, errors.KindOf(err), "%v", err)
			assert.Equal(t, before, itemsOf(t, setup, tc.parcel), "nothing joined the parcel")
		})
	}
}

// TestTheInteropJoinPassesTheModulesAnswerThrough is the surface the fulfilling
// flow resolves (ADR 0428): it writes the addition's items and hands the
// module's refusal back as it is, so the flow can pass it on.
func TestTheInteropJoinPassesTheModulesAnswerThrough(t *testing.T) {
	t.Parallel()

	setup := joinSetup(t)
	interop := service.NewInterop(setup.svc)

	require.NoError(t, interop.JoinParcel(context.Background(), "ful_parent", "order_parent", "order_addition",
		json.RawMessage(`[{"line_item_id":"li_a","quantity":2}]`), false))
	assert.Equal(t, map[string]int64{"li_a": 2}, itemsOf(t, setup, "ful_parent")["order_addition"],
		"the units named, and no other")

	other := joinSetup(t)
	other.bound.owed = map[string]int64{"li_a": 1}
	err := service.NewInterop(other.svc).JoinParcel(context.Background(), "ful_parent", "order_parent",
		"order_addition", nil, true)
	require.Error(t, err)
	assert.Equal(t, service.CodeNothingOwed, errors.CodeOf(err))
	err = service.NewInterop(other.svc).JoinParcel(context.Background(), "ful_parent", "order_parent",
		"order_addition", json.RawMessage(`null`), false)
	assert.Equal(t, service.CodeItemsRequired, errors.CodeOf(err), "no item, and what is owed not allowed")
}

// TestAJoinHoldsTheUnitsItNames takes the units named, each within what its
// line still owes, and leaves every other unit owed: a backordered unit stays
// out of the box (ADR 0428, as an open's named items, ADR 0409).
func TestAJoinHoldsTheUnitsItNames(t *testing.T) {
	t.Parallel()

	setup := joinSetup(t)
	ctx := context.Background()
	in := owedJoin()
	in.ItemsOwed = false
	in.Items = []service.FulfillmentItemInput{{LineItemID: "li_b", Quantity: 1}}

	_, err := setup.svc.JoinParcel(ctx, in)
	require.NoError(t, err)

	assert.Equal(t, map[string]int64{"li_b": 1}, itemsOf(t, setup, "ful_parent")["order_addition"],
		"li_a was not named, so none of it joins the box")
	held, err := setup.svc.CommittedQuantitiesForReference(ctx, "order_addition", nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"li_a": 1, "li_b": 1}, held,
		"li_a holds only the unit in the addition's own parcel; two are still owed")
}

// TestAJoinNamingMoreThanIsOwedIsRefused refuses a line the addition did not
// sell, a line asking for more than it sold, a line asking for more than its
// parcels leave and a join naming nothing where what is owed is not allowed,
// with the open's codes, and writes nothing.
func TestAJoinNamingMoreThanIsOwedIsRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		items []service.FulfillmentItemInput
		code  string
		kind  errors.Kind
	}{
		{"a line the addition did not sell", []service.FulfillmentItemInput{{LineItemID: "li_other", Quantity: 1}},
			service.CodeLineNotDispatchable, errors.KindInvalid},
		{"more than the line sold", []service.FulfillmentItemInput{{LineItemID: "li_b", Quantity: 2}},
			service.CodeLineNotDispatchable, errors.KindConflict},
		{"more than the parcels leave", []service.FulfillmentItemInput{{LineItemID: "li_a", Quantity: 3}},
			service.CodeLineNotDispatchable, errors.KindConflict},
		{"nothing named", nil, service.CodeItemsRequired, errors.KindInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			setup := joinSetup(t)
			in := owedJoin()
			in.ItemsOwed = false
			in.Items = tc.items

			_, err := setup.svc.JoinParcel(context.Background(), in)

			require.Error(t, err)
			assert.Equal(t, tc.code, errors.CodeOf(err), "%v", err)
			assert.Equal(t, tc.kind, errors.KindOf(err), "%v", err)
			assert.Empty(t, itemsOf(t, setup, "ful_parent")["order_addition"], "nothing joined the parcel")
		})
	}
}

// TestARepeatIsAnsweredFromTheParcelInAnyState answers a join of an addition
// whose items are already in the parcel with those items, though the parcel
// has shipped since, when it names nothing or exactly the units the parcel
// holds for it: the caller can then write a binding that failed (ADR 0428).
func TestARepeatIsAnsweredFromTheParcelInAnyState(t *testing.T) {
	t.Parallel()

	setup := joinSetup(t)
	ctx := context.Background()
	in := owedJoin()
	in.ItemsOwed = false
	in.Items = []service.FulfillmentItemInput{{LineItemID: "li_b", Quantity: 1}}
	first, err := setup.svc.JoinParcel(ctx, in)
	require.NoError(t, err)
	setup.store.setParcelStatus("ful_parent", models.StatusShipped)

	again, err := setup.svc.JoinParcel(ctx, in)
	require.NoError(t, err, "a repeat naming the same units is answered whatever the parcel's state")
	assert.ElementsMatch(t, first, again)

	in.Items = nil
	again, err = setup.svc.JoinParcel(ctx, in)
	require.NoError(t, err, "a repeat naming nothing is answered as well")
	assert.ElementsMatch(t, first, again)
}

// TestARepeatNamingOtherUnitsIsRefused refuses a join of an addition already
// in the parcel that names other units than the parcel holds for it, as an
// open's retry with another list is refused: a 200 would report units the
// parcel does not hold. Nothing is written, and the unit named stays owed.
func TestARepeatNamingOtherUnitsIsRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		items []service.FulfillmentItemInput
	}{
		{"another line", []service.FulfillmentItemInput{{LineItemID: "li_a", Quantity: 1}}},
		{"another count", []service.FulfillmentItemInput{{LineItemID: "li_b", Quantity: 2}}},
		{"more lines", []service.FulfillmentItemInput{{LineItemID: "li_a", Quantity: 2}, {LineItemID: "li_b", Quantity: 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			setup := joinSetup(t)
			setup.bound.owed = map[string]int64{"li_a": 3, "li_b": 2}
			ctx := context.Background()
			in := owedJoin()
			in.ItemsOwed = false
			in.Items = []service.FulfillmentItemInput{{LineItemID: "li_b", Quantity: 1}}
			_, err := setup.svc.JoinParcel(ctx, in)
			require.NoError(t, err)

			in.Items = tc.items
			_, err = setup.svc.JoinParcel(ctx, in)

			require.Error(t, err)
			assert.True(t, errors.IsConflict(err), "%v", err)
			assert.Equal(t, service.CodeJoinItemsDiffer, errors.CodeOf(err))
			assert.Contains(t, err.Error(), "li_b:1", "the message names what the parcel holds")
			assert.Equal(t, map[string]int64{"li_b": 1}, itemsOf(t, setup, "ful_parent")["order_addition"],
				"nothing was added")
			held, err := setup.svc.CommittedQuantitiesForReference(ctx, "order_addition", nil)
			require.NoError(t, err)
			assert.Equal(t, int64(1), held["li_a"], "li_a holds only the own parcel's unit; the rest stays owed")
		})
	}
}

// TestAJoinThatWaitedForAnotherAnswersItsRepeat reads the repeat again under
// the locks: a join of the same pair that committed while this one waited for
// the addition's lock, the parcel shipping after it, makes this one a repeat,
// answered as joined rather than refused for the parcel's state (ADR 0428).
func TestAJoinThatWaitedForAnotherAnswersItsRepeat(t *testing.T) {
	t.Parallel()

	setup := joinSetup(t)
	setup.store.onLock = func(f *fakeStore) {
		f.onLock = nil
		f.mu.Lock()
		f.items["item_raced"] = models.FulfillmentItem{
			ID: "item_raced", FulfillmentID: "ful_parent", LineItemID: "li_b", Quantity: 1,
			Reference: "order_addition",
		}
		f.mu.Unlock()
		f.setParcelStatus("ful_parent", models.StatusShipped)
	}

	joined, err := setup.svc.JoinParcel(context.Background(), owedJoin())

	require.NoError(t, err, "the other join put the addition in; this one is its repeat")
	require.Len(t, joined, 1)
	assert.Equal(t, "item_raced", joined[0].ID)
	assert.Equal(t, map[string]int64{"li_b": 1}, itemsOf(t, setup, "ful_parent")["order_addition"])
}
