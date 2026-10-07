package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// heldItems is a parcel's items as line id to units.
func heldItems(ful models.Fulfillment) map[string]int64 {
	out := make(map[string]int64, len(ful.Items))
	for _, item := range ful.Items {
		out[item.LineItemID] = item.Quantity
	}

	return out
}

// TestAParcelOfWhatIsOwedHoldsEveryUnitStillOwed is ADR 0409: a parcel asked to
// hold what the order owes holds every line the bound answers a unit for, in
// line id order, and leaves out a line that owes none.
func TestAParcelOfWhatIsOwedHoldsEveryUnitStillOwed(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)
	setup.bound.owed = map[string]int64{"li_c": 1, "li_a": 2, "li_b": 0}

	ful, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "key-owed", ItemsOwed: true,
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"li_a": 2, "li_c": 1}, heldItems(ful))
	require.Len(t, ful.Items, 2)
	assert.Equal(t, "li_a", ful.Items[0].LineItemID, "the lines are held in line id order")
}

// TestNothingOwedOpensNothing refuses a parcel asked to hold what the order
// owes when it owes no unit: no row is written and the carrier is not asked.
func TestNothingOwedOpensNothing(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)
	setup.bound.owed = map[string]int64{"li_a": 0}

	_, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "key-none", ItemsOwed: true,
	})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "%v", err)
	assert.Equal(t, service.CodeNothingOwed, errors.CodeOf(err))
	assert.Empty(t, setup.store.fuls, "nothing was written")
	assert.Zero(t, setup.provider.createCalls, "the carrier was not asked")
}

// TestARetryOfAnOwedParcelIsAnsweredFromItsKey answers a second press of the
// same key with the parcel the first one opened, though the bound moved in
// between: the retry asks the bound nothing and its empty list is no mismatch.
func TestARetryOfAnOwedParcelIsAnsweredFromItsKey(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)
	setup.bound.owed = map[string]int64{"li_a": 2}
	in := service.CreateFulfillmentInput{
		Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "key-retry", ItemsOwed: true,
	}

	first, err := setup.svc.CreateFulfillment(context.Background(), in)
	require.NoError(t, err)
	asked := setup.bound.asked()
	setup.bound.owed = map[string]int64{"li_a": 1}

	second, err := setup.svc.CreateFulfillment(context.Background(), in)
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, map[string]int64{"li_a": 2}, heldItems(second), "the parcel keeps what its first open filled")
	assert.Equal(t, asked, setup.bound.asked(), "a retry asks the bound nothing")
}

// TestAReturnParcelCannotAskForWhatIsOwed keeps the default to outgoing parcels:
// a parcel bringing a return back names its units (ADR 0384).
func TestAReturnParcelCannotAskForWhatIsOwed(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "returns")
	optionID := setup.createOption(t, service.CreateOptionInput{
		Name: "Return pickup", ShippingProfileID: profileID, Amount: 0, IsReturn: true,
	})

	_, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "key-return",
		ReturnID: "oret_1", ItemsOwed: true,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "%v", err)
	assert.Empty(t, setup.store.fuls)
}

// TestAParcelCommittedButNotLinkedIsCounted is the first review probe of gap
// D265 (ADR 0409): a parcel holding two of three units has committed and its
// link to the order is not written, so a bound counted through the link would
// still say three. Counted by reference under the lock, the order may take one
// more unit: three are refused, exactly one opens, and then nothing is owed.
func TestAParcelCommittedButNotLinkedIsCounted(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)
	setup.bound.owed = map[string]int64{"li_a": 3}
	setup.store.putLiveParcel("ful_unlinked", "order_1", "li_a", 2)
	open := func(key string, items ...service.FulfillmentItemInput) (models.Fulfillment, error) {
		return setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
			Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: key,
			Items: items, ItemsOwed: len(items) == 0,
		})
	}

	_, err := open("key-three", service.FulfillmentItemInput{LineItemID: "li_a", Quantity: 3})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "%v", err)
	assert.Equal(t, service.CodeLineNotDispatchable, errors.CodeOf(err))

	owed, err := open("key-owed")
	require.NoError(t, err, "a parcel of what is owed takes what is left")
	assert.Equal(t, map[string]int64{"li_a": 1}, heldItems(owed), "the ceiling less what the parcels hold")

	_, err = open("key-more")
	assert.Equal(t, service.CodeNothingOwed, errors.CodeOf(err), "%v", err)
}

// TestAParcelMayTakeExactlyWhatIsLeft is the boundary of the check under the
// lock: three may ship, two are held, and a parcel of one opens.
func TestAParcelMayTakeExactlyWhatIsLeft(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)
	setup.bound.owed = map[string]int64{"li_a": 3}
	setup.store.putLiveParcel("ful_held", "order_1", "li_a", 2)

	ful, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "key-exact",
		Items: []service.FulfillmentItemInput{{LineItemID: "li_a", Quantity: 1}},
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"li_a": 1}, heldItems(ful))
	assert.Contains(t, setup.store.lockOrder(), "dispatch:order_1", "the order's lock was taken")
}

// TestACancelAndAnOpenInBetweenAreBothCounted is the second review probe of gap
// D265 (ADR 0409): while this open asks what the order may ship, a parcel of
// two is canceled and another of two opens. Their net change is zero; what the
// order's parcels hold under the lock is two, so a parcel of three is refused
// and one of one opens.
func TestACancelAndAnOpenInBetweenAreBothCounted(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)
	setup.bound.owed = map[string]int64{"li_a": 3}
	setup.store.putLiveParcel("ful_canceled", "order_1", "li_a", 2)
	setup.bound.during = func() {
		setup.store.cancelParcel("ful_canceled")
		setup.store.putLiveParcel("ful_between", "order_1", "li_a", 2)
	}

	_, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "key-three",
		Items: []service.FulfillmentItemInput{{LineItemID: "li_a", Quantity: 3}},
	})
	require.Error(t, err)
	assert.Equal(t, service.CodeLineNotDispatchable, errors.CodeOf(err), "%v", err)
	assert.Zero(t, setup.provider.createCalls, "the carrier was not asked")

	_, err = setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "key-one",
		Items: []service.FulfillmentItemInput{{LineItemID: "li_a", Quantity: 1}},
	})
	require.NoError(t, err)
}

// TestAParcelOpenedWhileWaitingForTheLockIsCounted holds the count to the
// lock: another parcel of two commits while this open waits for the order's
// lock, so what the parcels hold is read after it and a parcel of two of three
// is refused (ADR 0409).
func TestAParcelOpenedWhileWaitingForTheLockIsCounted(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)
	setup.bound.owed = map[string]int64{"li_a": 3}
	setup.store.onLock = func(f *fakeStore) {
		f.onLock = nil
		f.putLiveParcel("ful_first", "order_1", "li_a", 2)
	}

	_, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "key-second",
		Items: []service.FulfillmentItemInput{{LineItemID: "li_a", Quantity: 2}},
	})
	require.Error(t, err)
	assert.Equal(t, service.CodeLineNotDispatchable, errors.CodeOf(err), "%v", err)
}

// TestARetryNamingAnotherListIsAKeyMismatch answers a second press of a key
// with a different list through the holding interop as a mismatch, not with
// the parcel the first press opened (ADR 0409).
func TestARetryNamingAnotherListIsAKeyMismatch(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)
	setup.bound.owed = map[string]int64{"li_a": 2, "li_b": 1}
	interop := service.NewInterop(setup.svc)

	_, err := interop.CreateFulfillmentHolding(context.Background(), "order_1", optionID, "key-list", nil,
		[]byte(`[{"line_item_id":"li_a","quantity":1}]`))
	require.NoError(t, err)

	_, err = interop.CreateFulfillmentHolding(context.Background(), "order_1", optionID, "key-list", nil,
		[]byte(`[{"line_item_id":"li_b","quantity":1}]`))
	require.Error(t, err)
	assert.Equal(t, service.CodeIdempotencyMismatch, errors.CodeOf(err), "%v", err)
}

// TestTheHoldingInteropAsksForWhatIsOwedOrHoldsWhatItNames is the surface the
// fulfilling flow opens an order's parcel through (ADR 0409): no list asks for
// every unit still owed, a list is held as named.
func TestTheHoldingInteropAsksForWhatIsOwedOrHoldsWhatItNames(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)
	setup.bound.owed = map[string]int64{"li_a": 2, "li_b": 1}
	interop := service.NewInterop(setup.svc)

	namedID, err := interop.CreateFulfillmentHolding(context.Background(), "order_1", optionID, "key-named", nil,
		[]byte(`[{"line_item_id":"li_b","quantity":1}]`))
	require.NoError(t, err)
	named, err := setup.svc.GetFulfillment(context.Background(), namedID)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"li_b": 1}, heldItems(named))

	owedID, err := interop.CreateFulfillmentHolding(context.Background(), "order_1", optionID, "key-owed", nil, nil)
	require.NoError(t, err)
	owed, err := setup.svc.GetFulfillment(context.Background(), owedID)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"li_a": 2}, heldItems(owed), "what the first parcel holds is not owed")

	_, err = interop.CreateFulfillmentHolding(context.Background(), "order_1", optionID, "key-bad", nil, []byte(`{`))
	assert.True(t, errors.IsInvalid(err), "%v", err)
}

// TestTheModuleRouteRefusesAnOutgoingParcelNamingNone is gap D264 on the module's
// route (ADR 0409): an outgoing parcel that names no items and does not ask for
// what is owed is refused before anything is read or written.
func TestTheModuleRouteRefusesAnOutgoingParcelNamingNone(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)

	_, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "key-none", ItemsRequired: true,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "%v", err)
	assert.Equal(t, service.CodeItemsRequired, errors.CodeOf(err))
	assert.Empty(t, setup.store.fuls, "nothing was written")
	assert.Zero(t, setup.bound.asked(), "what the order may ship was not even asked")
}

// TestAReplayOfAnItemlessParcelIsAnsweredWithIt reads the key before the
// refusal: a parcel opened with no items before ADR 0409, replayed under its
// key on the module's route, is answered with that parcel, not refused as if
// nothing had been opened.
func TestAReplayOfAnItemlessParcelIsAnsweredWithIt(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)
	in := service.CreateFulfillmentInput{Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "key-old"}

	first, err := setup.svc.CreateFulfillment(context.Background(), in)
	require.NoError(t, err, "an itemless parcel as the module opened one before ADR 0409")

	in.ItemsRequired = true
	replay, err := setup.svc.CreateFulfillment(context.Background(), in)
	require.NoError(t, err, "a replay is answered from its key")
	assert.Equal(t, first.ID, replay.ID)
}

// TestAReturnOptionWithoutItsReturnKeepsItsRefusal reads the option's
// direction before the refusal: a return option sent with no return_id and no
// items answers the direction mismatch the route describes.
func TestAReturnOptionWithoutItsReturnKeepsItsRefusal(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "returns")
	optionID := setup.createOption(t, service.CreateOptionInput{
		Name: "Return pickup", ShippingProfileID: profileID, Amount: 0, IsReturn: true,
	})

	_, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "key-return", ItemsRequired: true,
	})
	require.Error(t, err)
	assert.Equal(t, service.CodeOptionDirectionMismatch, errors.CodeOf(err), "%v", err)
}

// TestTheInteropCountsTheReferencesLiveOutgoingParcels is the count the panel's
// offer and the module's check share (ADR 0409): the live outgoing parcels
// opened for the reference, whether or not anything links them, and neither a
// canceled one, one bringing a return back, nor another reference's.
func TestTheInteropCountsTheReferencesLiveOutgoingParcels(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	setup.store.putLiveParcel("ful_live", "order_1", "li_a", 2)
	setup.store.putLiveParcel("ful_canceled", "order_1", "li_a", 5)
	setup.store.cancelParcel("ful_canceled")
	setup.store.putLiveParcel("ful_return", "order_1", "li_a", 7)
	setup.store.mu.Lock()
	ret := setup.store.fuls["ful_return"]
	ret.ReturnID = "oret_1"
	setup.store.fuls["ful_return"] = ret
	setup.store.mu.Unlock()
	setup.store.putLiveParcel("ful_other", "order_2", "li_a", 11)

	held, err := service.NewInterop(setup.svc).CommittedQuantitiesForReference(context.Background(), "order_1", nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"li_a": 2}, held)
}
