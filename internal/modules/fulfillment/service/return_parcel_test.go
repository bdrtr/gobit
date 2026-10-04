package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// The tests here are about the parcel that brings an order return back
// (ADR 0384, D234). The module's documents always said goods a customer sends
// back travel in a second fulfillment on an is_return option; the order's bound
// refused that parcel for units already shipped, and nothing said which return
// it served.

// returnOption creates an is_return option for the test.
func returnOption(t *testing.T, setup testSetup) string {
	t.Helper()
	profileID := setup.createProfile(t, "returns")
	return setup.createOption(t, service.CreateOptionInput{
		Name:              "Return shipping",
		ShippingProfileID: profileID,
		Amount:            2_000,
		IsReturn:          true,
	})
}

// returnParcel is a request for a parcel bringing return ret_A back.
func returnParcel(optionID, key string, quantity int64) service.CreateFulfillmentInput {
	return service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: optionID,
		IdempotencyKey:   key,
		ReturnID:         "ret_A",
		Items:            []service.FulfillmentItemInput{{LineItemID: "oli_1", Quantity: quantity}},
	}
}

// TestAReturnParcelIsBoundedByItsReturnNotByWhatTheOrderOwes is D234: the order
// owes nothing of the line, because it all shipped, and the customer sends one
// unit back.
func TestAReturnParcelIsBoundedByItsReturnNotByWhatTheOrderOwes(t *testing.T) {
	setup := newSetup(t)
	optionID := returnOption(t, setup)
	setup.bound.owed = map[string]int64{"oli_1": 0}
	setup.bound.returnLines = map[string]int64{"oli_1": 1}

	ful, err := setup.svc.CreateFulfillment(context.Background(), returnParcel(optionID, "key-back", 1))

	require.NoError(t, err)
	assert.Equal(t, "ret_A", ful.ReturnID, "the parcel names the return it brings back")
	assert.Equal(t, 0, setup.bound.asked(), "what the order owes is not the question")
	assert.Equal(t, 1, setup.bound.returnAsked(), "what the return awaits is")
}

// TestAReturnParcelMayHoldExactlyWhatItsReturnStillAwaits is the boundary.
func TestAReturnParcelMayHoldExactlyWhatItsReturnStillAwaits(t *testing.T) {
	setup := newSetup(t)
	optionID := returnOption(t, setup)
	setup.bound.returnLines = map[string]int64{"oli_1": 2}
	_, err := setup.svc.CreateFulfillment(context.Background(), returnParcel(optionID, "key-first", 1))
	require.NoError(t, err, "precondition: one unit is already on its way back")

	_, err = setup.svc.CreateFulfillment(context.Background(), returnParcel(optionID, "key-second", 1))

	require.NoError(t, err, "the second unit the return names may still come back")
}

// TestAReturnParcelCannotHoldMoreThanItsReturnStillAwaits subtracts what the
// return's live parcels already hold.
func TestAReturnParcelCannotHoldMoreThanItsReturnStillAwaits(t *testing.T) {
	setup := newSetup(t)
	optionID := returnOption(t, setup)
	setup.bound.returnLines = map[string]int64{"oli_1": 2}
	_, err := setup.svc.CreateFulfillment(context.Background(), returnParcel(optionID, "key-first", 1))
	require.NoError(t, err, "precondition: one unit is already on its way back")
	creates := len(setup.provider.createInputs)

	_, err = setup.svc.CreateFulfillment(context.Background(), returnParcel(optionID, "key-second", 2))

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))
	assert.Equal(t, service.CodeLineNotDispatchable, coreerrors.CodeOf(err))
	assert.Len(t, setup.provider.createInputs, creates, "nothing reached the provider")
}

// TestALineTheReturnDoesNotNameIsRefused reads an absent line as a refusal, not
// as unlimited.
func TestALineTheReturnDoesNotNameIsRefused(t *testing.T) {
	setup := newSetup(t)
	optionID := returnOption(t, setup)
	setup.bound.returnLines = map[string]int64{"oli_other": 5}

	_, err := setup.svc.CreateFulfillment(context.Background(), returnParcel(optionID, "key-line", 1))

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err))
	assert.Equal(t, service.CodeLineNotDispatchable, coreerrors.CodeOf(err))
}

// TestAReturnThatAwaitsNothingIsRefused covers another order's return, and one
// received or canceled: the order module answers all three as "awaits nothing".
func TestAReturnThatAwaitsNothingIsRefused(t *testing.T) {
	setup := newSetup(t)
	optionID := returnOption(t, setup)
	setup.bound.returnLines = map[string]int64{"oli_1": 5}
	setup.bound.notAwaited = true

	_, err := setup.svc.CreateFulfillment(context.Background(), returnParcel(optionID, "key-done", 1))

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))
	assert.Equal(t, service.CodeReturnNotAwaited, coreerrors.CodeOf(err))
	assert.Empty(t, setup.provider.createInputs, "no label was printed")
	assert.Zero(t, setup.store.fulfillmentWriteCount(), "and no row was written")
}

// TestAReturnOptionWithoutAReturnIsRefusedBeforeTheBoundIsAsked is the other
// half of D234: a parcel on a return option naming no return was bounded by
// what the order owes, and an itemless one passed.
func TestAReturnOptionWithoutAReturnIsRefusedBeforeTheBoundIsAsked(t *testing.T) {
	setup := newSetup(t)
	optionID := returnOption(t, setup)
	setup.bound.owed = map[string]int64{"oli_1": 0}

	_, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: optionID,
		IdempotencyKey:   "key-no-return",
		Items:            []service.FulfillmentItemInput{{LineItemID: "oli_1", Quantity: 1}},
	})

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err))
	assert.Equal(t, service.CodeOptionDirectionMismatch, coreerrors.CodeOf(err))
	assert.Equal(t, 0, setup.bound.asked(), "the direction is refused before the bound is asked")
	assert.Equal(t, 0, setup.bound.returnAsked())
}

// TestAReturnNamedOnAnOutgoingOptionIsRefused is the check's other direction.
func TestAReturnNamedOnAnOutgoingOptionIsRefused(t *testing.T) {
	setup := newSetup(t)
	optionID := readyOption(t, setup)
	setup.bound.returnLines = map[string]int64{"oli_1": 5}

	_, err := setup.svc.CreateFulfillment(context.Background(), returnParcel(optionID, "key-wrong-way", 1))

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err))
	assert.Equal(t, service.CodeOptionDirectionMismatch, coreerrors.CodeOf(err))
	assert.Equal(t, 0, setup.bound.returnAsked())
}

// TestAReturnParcelWithoutItemsIsRefused keeps the bound from looping over
// nothing.
func TestAReturnParcelWithoutItemsIsRefused(t *testing.T) {
	setup := newSetup(t)
	optionID := returnOption(t, setup)
	setup.bound.returnLines = map[string]int64{"oli_1": 5}
	in := returnParcel(optionID, "key-empty", 1)
	in.Items = nil

	_, err := setup.svc.CreateFulfillment(context.Background(), in)

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err))
	assert.Equal(t, service.CodeInvalidInput, coreerrors.CodeOf(err))
	assert.Empty(t, setup.provider.createInputs)
}

// TestTheOrderDoorCannotOpenAParcelOnAReturnOption goes through the surface the
// fulfilling flow calls, which carries no items and no return: before ADR 0384
// it opened a parcel on a return option, handed it the customer's address and
// bound it to the order.
func TestTheOrderDoorCannotOpenAParcelOnAReturnOption(t *testing.T) {
	setup := newSetup(t)
	optionID := returnOption(t, setup)

	_, err := service.NewInterop(setup.svc).CreateFulfillment(
		context.Background(), "order_1", optionID, "key-door", nil)

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err))
	assert.Equal(t, service.CodeOptionDirectionMismatch, coreerrors.CodeOf(err))
	assert.Empty(t, setup.provider.createInputs)
}

// TestAReturnParcelIsNotBoundToItsOrder keeps it out of every reader that counts
// the order's parcels through the link: the dispatch bound, the stock targets, the
// address and delivery guards and the join.
func TestAReturnParcelIsNotBoundToItsOrder(t *testing.T) {
	setup, links := newSetupWithLinks(t)
	returnID := returnOption(t, setup)
	outgoingID := readyOption(t, setup)
	setup.bound.returnLines = map[string]int64{"oli_1": 1}

	_, err := setup.svc.CreateFulfillment(context.Background(), returnParcel(returnID, "key-back", 1))
	require.NoError(t, err)
	assert.Equal(t, 0, links.calls, "a parcel bringing a return back writes no binding")

	_, err = setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference: "order_1", ShippingOptionID: outgoingID, IdempotencyKey: "key-out",
		Items: []service.FulfillmentItemInput{{LineItemID: "oli_1", Quantity: 1}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, links.calls, "an outgoing parcel is bound as before")
	assert.Len(t, links.linked("order_1"), 1)
}

// TestAKeyCannotChangeTheReturnItBringsBack holds idempotency to the return as
// well: a retry naming another return is a different request.
func TestAKeyCannotChangeTheReturnItBringsBack(t *testing.T) {
	setup := newSetup(t)
	optionID := returnOption(t, setup)
	setup.bound.returnLines = map[string]int64{"oli_1": 5}
	_, err := setup.svc.CreateFulfillment(context.Background(), returnParcel(optionID, "key-same", 1))
	require.NoError(t, err)

	other := returnParcel(optionID, "key-same", 1)
	other.ReturnID = "ret_B"
	_, err = setup.svc.CreateFulfillment(context.Background(), other)

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))
	assert.Equal(t, service.CodeIdempotencyMismatch, coreerrors.CodeOf(err))
}

// TestAReturnParcelFailsClosed refuses when the return cannot be read: with no
// bound at all, and with one whose answer is an error, which keeps its kind.
func TestAReturnParcelFailsClosed(t *testing.T) {
	t.Run("no bound", func(t *testing.T) {
		setup := newSetup(t)
		optionID := returnOption(t, setup)
		registry := service.NewProviderRegistry()
		require.NoError(t, registry.Register(setup.provider))
		svc, err := service.New(service.Options{Store: setup.store, Providers: registry})
		require.NoError(t, err)

		_, err = svc.CreateFulfillment(context.Background(), returnParcel(optionID, "key-unbound", 1))

		require.Error(t, err)
		assert.Equal(t, coreerrors.KindInternal, coreerrors.KindOf(err))
		assert.Equal(t, service.CodeDispatchBoundUnknown, coreerrors.CodeOf(err))
		assert.Empty(t, setup.provider.createInputs)
	})

	t.Run("an unknown return", func(t *testing.T) {
		setup := newSetup(t)
		optionID := returnOption(t, setup)
		setup.bound.returnErr = coreerrors.NotFound("order_return_not_found", "no such return")

		_, err := setup.svc.CreateFulfillment(context.Background(), returnParcel(optionID, "key-unknown", 1))

		require.Error(t, err)
		assert.Equal(t, coreerrors.KindNotFound, coreerrors.KindOf(err),
			"an unknown return answers 404, the way an unknown order does")
		assert.Equal(t, service.CodeDispatchBoundUnknown, coreerrors.CodeOf(err))
		assert.Empty(t, setup.provider.createInputs)
	})

	t.Run("an unreadable return", func(t *testing.T) {
		setup := newSetup(t)
		optionID := returnOption(t, setup)
		setup.bound.returnErr = errors.New("connection reset")

		_, err := setup.svc.CreateFulfillment(context.Background(), returnParcel(optionID, "key-down", 1))

		require.Error(t, err)
		assert.Equal(t, coreerrors.KindInternal, coreerrors.KindOf(err))
		assert.Empty(t, setup.provider.createInputs)
	})
}

// TestARetryReturnsItsParcelAfterTheOptionTurned keeps a retry a retry (ADR
// 0135): an option's is_return can be changed, and a key that already names a
// parcel answers with that parcel, whichever way the option now points.
func TestARetryReturnsItsParcelAfterTheOptionTurned(t *testing.T) {
	turn := func(t *testing.T, setup testSetup, optionID string, isReturn bool) {
		t.Helper()
		_, err := setup.svc.UpdateShippingOption(context.Background(), optionID,
			service.UpdateOptionInput{IsReturn: &isReturn})
		require.NoError(t, err, "precondition: the option turns")
	}

	t.Run("an outgoing parcel on an option now marked for returns", func(t *testing.T) {
		setup := newSetup(t)
		optionID := readyOption(t, setup)
		in := service.CreateFulfillmentInput{
			Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "key-out",
			Items: []service.FulfillmentItemInput{{LineItemID: "oli_1", Quantity: 1}},
		}
		first, err := setup.svc.CreateFulfillment(context.Background(), in)
		require.NoError(t, err)
		turn(t, setup, optionID, true)

		again, err := setup.svc.CreateFulfillment(context.Background(), in)

		require.NoError(t, err, "the response was lost, the parcel was not")
		assert.Equal(t, first.ID, again.ID)
		assert.Len(t, setup.provider.createInputs, 1, "no second label")
	})

	t.Run("a return parcel on an option now marked outgoing", func(t *testing.T) {
		setup := newSetup(t)
		optionID := returnOption(t, setup)
		setup.bound.returnLines = map[string]int64{"oli_1": 1}
		in := returnParcel(optionID, "key-back", 1)
		first, err := setup.svc.CreateFulfillment(context.Background(), in)
		require.NoError(t, err)
		turn(t, setup, optionID, false)

		again, err := setup.svc.CreateFulfillment(context.Background(), in)

		require.NoError(t, err, "the response was lost, the parcel was not")
		assert.Equal(t, first.ID, again.ID)
		assert.Equal(t, "ret_A", again.ReturnID)
		assert.Len(t, setup.provider.createInputs, 1, "no second label")
	})

	t.Run("the retry still has to be the same request", func(t *testing.T) {
		setup := newSetup(t)
		optionID := readyOption(t, setup)
		setup.bound.returnLines = map[string]int64{"oli_1": 1}
		_, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
			Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "key-same",
			Items: []service.FulfillmentItemInput{{LineItemID: "oli_1", Quantity: 1}},
		})
		require.NoError(t, err)
		turn(t, setup, optionID, true)

		_, err = setup.svc.CreateFulfillment(context.Background(), returnParcel(optionID, "key-same", 1))

		require.Error(t, err, "an outgoing parcel's key cannot open it as a return")
		assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))
		assert.Equal(t, service.CodeIdempotencyMismatch, coreerrors.CodeOf(err))
	})
}

// TestAnOverlongReturnIDIsRefusedBeforeTheBoundIsAsked keeps the free-text
// bound on return_id: it is refused here, not carried to the order module.
func TestAnOverlongReturnIDIsRefusedBeforeTheBoundIsAsked(t *testing.T) {
	setup := newSetup(t)
	optionID := returnOption(t, setup)
	setup.bound.returnLines = map[string]int64{"oli_1": 1}
	in := returnParcel(optionID, "key-long", 1)
	in.ReturnID = "ret_" + strings.Repeat("x", 600)

	_, err := setup.svc.CreateFulfillment(context.Background(), in)

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err))
	assert.Equal(t, service.CodeInvalidInput, coreerrors.CodeOf(err))
	assert.Equal(t, 0, setup.bound.returnAsked(), "the order module is not asked")
	assert.Empty(t, setup.provider.createInputs)
}
