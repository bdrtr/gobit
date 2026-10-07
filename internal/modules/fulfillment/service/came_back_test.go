package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// This file is gap D268 (ADR 0423): a parcel that came back to the sender
// undelivered holds its units only as far as a return or a replacement speaks
// for them; the rest the order owes again.

// cameBackSetup opens an order whose one line may ship ceiling units and whose
// parcel of parcelUnits came back undelivered, with spoken units a return or a
// replacement speaks for.
func cameBackSetup(t *testing.T, ceiling, parcelUnits, spoken int64) (setup testSetup, optionID string) {
	t.Helper()

	setup = newSetup(t)
	optionID = readyOption(t, setup)
	setup.bound.owed = map[string]int64{"li_a": ceiling}
	if spoken > 0 {
		setup.bound.spoken = map[string]int64{"li_a": spoken}
	}
	setup.store.putLiveParcel("ful_back", "order_1", "li_a", parcelUnits)
	setup.store.setParcelStatus("ful_back", models.StatusReturned)

	return setup, optionID
}

// TestAParcelThatCameBackHoldsNothingNoReturnNames: with no return behind
// them, the units of a parcel that came back are owed again, to a parcel that
// names them and to one asked for what is owed.
func TestAParcelThatCameBackHoldsNothingNoReturnNames(t *testing.T) {
	t.Parallel()

	t.Run("a parcel naming them", func(t *testing.T) {
		t.Parallel()
		setup, optionID := cameBackSetup(t, 2, 2, 0)

		ful, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
			Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "key-named",
			Items: []service.FulfillmentItemInput{{LineItemID: "li_a", Quantity: 2}},
		})
		require.NoError(t, err, "the two units came back and nothing speaks for them")
		assert.Equal(t, map[string]int64{"li_a": 2}, heldItems(ful))
	})

	t.Run("a parcel of what is owed", func(t *testing.T) {
		t.Parallel()
		setup, optionID := cameBackSetup(t, 2, 2, 0)

		ful, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
			Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "key-owed", ItemsOwed: true,
		})
		require.NoError(t, err)
		assert.Equal(t, map[string]int64{"li_a": 2}, heldItems(ful), "the default takes every unit that came back")
	})
}

// TestAReturnKeepsTheUnitsOfAParcelThatCameBack: a return the operator recorded
// for the units keeps them held, so its receipt restocks them once and no
// parcel sends them a second time.
func TestAReturnKeepsTheUnitsOfAParcelThatCameBack(t *testing.T) {
	t.Parallel()

	setup, optionID := cameBackSetup(t, 2, 2, 2)

	_, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "key-named",
		Items: []service.FulfillmentItemInput{{LineItemID: "li_a", Quantity: 1}},
	})
	require.Error(t, err, "a return names the two units")
	assert.Equal(t, service.CodeLineNotDispatchable, errors.CodeOf(err), "%v", err)

	_, err = setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "key-owed", ItemsOwed: true,
	})
	require.Error(t, err)
	assert.Equal(t, service.CodeNothingOwed, errors.CodeOf(err), "%v", err)
}

// TestAReturnHoldsOnlyTheUnitsItNames: a return of one of two units that came
// back keeps that one, and the other is owed again.
func TestAReturnHoldsOnlyTheUnitsItNames(t *testing.T) {
	t.Parallel()

	setup, optionID := cameBackSetup(t, 2, 2, 1)

	ful, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "key-owed", ItemsOwed: true,
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"li_a": 1}, heldItems(ful), "one unit is the return's, one is owed again")
}

// TestAReturnOfDeliveredGoodsLeavesTheLineAsItWas: a delivered parcel holds its
// units whatever is spoken for; only a parcel that came back is read
// against them.
func TestAReturnOfDeliveredGoodsLeavesTheLineAsItWas(t *testing.T) {
	t.Parallel()

	for name, spoken := range map[string]int64{"returned": 2, "kept": 0} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			setup := newSetup(t)
			optionID := readyOption(t, setup)
			setup.bound.owed = map[string]int64{"li_a": 2}
			if spoken > 0 {
				setup.bound.spoken = map[string]int64{"li_a": spoken}
			}
			setup.store.putLiveParcel("ful_delivered", "order_1", "li_a", 2)
			setup.store.setParcelStatus("ful_delivered", models.StatusDelivered)

			_, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
				Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "key-owed", ItemsOwed: true,
			})
			require.Error(t, err)
			assert.Equal(t, service.CodeNothingOwed, errors.CodeOf(err), "%v", err)
		})
	}
}

// TestTheComeBackRuleIsConservativeOnAMixedLine pins the stated cost: a return
// does not say which units it asks back, so on a line with delivered goods and
// a parcel that came back, what is spoken for covers the come-back units first.
func TestTheComeBackRuleIsConservativeOnAMixedLine(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)
	setup.bound.owed = map[string]int64{"li_a": 4}
	setup.bound.spoken = map[string]int64{"li_a": 2}
	setup.store.putLiveParcel("ful_delivered", "order_1", "li_a", 2)
	setup.store.setParcelStatus("ful_delivered", models.StatusDelivered)
	setup.store.putLiveParcel("ful_back", "order_1", "li_a", 2)
	setup.store.setParcelStatus("ful_back", models.StatusReturned)

	_, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "key-owed", ItemsOwed: true,
	})
	require.Error(t, err)
	assert.Equal(t, service.CodeNothingOwed, errors.CodeOf(err), "%v", err)
}

// TestTheLockedCountReadsTheReturns is the cancellation's count (ADR 0420) under
// the same rule: what came back counts only as far as the figure passed in
// speaks for it, and a parcel that left counts whole.
func TestTheLockedCountReadsTheReturns(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	setup.store.putLiveParcel("ful_back", "order_1", "li_a", 2)
	setup.store.setParcelStatus("ful_back", models.StatusReturned)
	setup.store.putLiveParcel("ful_shipped", "order_1", "li_b", 3)
	setup.store.setParcelStatus("ful_shipped", models.StatusShipped)

	held, err := setup.svc.HeldForReferenceLocked(context.Background(), "order_1", nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"li_a": 0, "li_b": 3}, held, "no return: the two units are owed again")

	held, err = setup.svc.HeldForReferenceLocked(context.Background(), "order_1", map[string]int64{"li_a": 1})
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"li_a": 1, "li_b": 3}, held, "a return of one keeps one")
}

// TestThePanelsCountReadsTheReturns is the count the fulfilling flow offers the
// panel's form from, outside the lock, under the same rule: a parcel that came
// back holds its units only as far as the figure passed in speaks for them.
func TestThePanelsCountReadsTheReturns(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	setup.store.putLiveParcel("ful_back", "order_1", "li_a", 2)
	setup.store.setParcelStatus("ful_back", models.StatusReturned)
	setup.store.putLiveParcel("ful_delivered", "order_1", "li_a", 1)
	setup.store.setParcelStatus("ful_delivered", models.StatusDelivered)
	interop := service.NewInterop(setup.svc)

	held, err := interop.CommittedQuantitiesForReference(context.Background(), "order_1", nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"li_a": 1}, held, "no return: only the delivered unit is held")

	held, err = interop.CommittedQuantitiesForReference(context.Background(), "order_1", map[string]int64{"li_a": 1})
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"li_a": 2}, held, "a return of one keeps one of those that came back")
}

// TestTheInteropPassesWhatIsSpokenForToTheLockedCount is the seam the
// cancellation flows resolve as fulfillment.interop: the figure they pass
// reaches the module's locked count, so a write-off puts back no unit a return
// or a replacement speaks for.
func TestTheInteropPassesWhatIsSpokenForToTheLockedCount(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	setup.store.putLiveParcel("ful_back", "order_1", "li_a", 2)
	setup.store.setParcelStatus("ful_back", models.StatusReturned)

	held, err := service.NewInterop(setup.svc).HeldForReferenceLocked(
		context.Background(), "order_1", map[string]int64{"li_a": 1})
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"li_a": 1}, held, "one unit is spoken for, so one is held")
}

// TestAParcelThatCameBackBeforeTheUpgradeIsHeldWhole is fulfillment migration
// 000008: a parcel already returned when ADR 0423 shipped holds every unit, as
// it did then, so nothing the operator settled outside a return or a
// replacement is offered or restocked again.
func TestAParcelThatCameBackBeforeTheUpgradeIsHeldWhole(t *testing.T) {
	t.Parallel()

	setup, optionID := cameBackSetup(t, 2, 2, 0)
	setup.store.markHeldWhole("ful_back")

	_, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference: "order_1", ShippingOptionID: optionID, IdempotencyKey: "key-owed", ItemsOwed: true,
	})
	require.Error(t, err)
	assert.Equal(t, service.CodeNothingOwed, errors.CodeOf(err), "%v", err)

	held, err := setup.svc.HeldForReferenceLocked(context.Background(), "order_1", nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"li_a": 2}, held, "held whole, with nothing spoken for")
}

// TestAShipmentSaysItIsHeldWhole is the read the order page makes so it never
// offers again the units of a parcel that came back before ADR 0423.
func TestAShipmentSaysItIsHeldWhole(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	setup.store.putLiveParcel("ful_before", "order_1", "li_a", 2)
	setup.store.setParcelStatus("ful_before", models.StatusReturned)
	setup.store.markHeldWhole("ful_before")
	setup.store.putLiveParcel("ful_after", "order_1", "li_a", 1)
	setup.store.setParcelStatus("ful_after", models.StatusReturned)

	records, err := service.NewShipmentQueryProvider(setup.svc).List(context.Background(), query.ListOptions{
		Fields:  []string{service.FieldShipmentID, service.FieldShipmentHeldWhole},
		Filters: map[string]any{service.FieldReference: "order_1"},
		Limit:   10,
	})
	require.NoError(t, err)
	whole := map[any]any{}
	for _, record := range records {
		whole[record[service.FieldShipmentID]] = record[service.FieldShipmentHeldWhole]
	}
	assert.Equal(t, map[any]any{"ful_before": true, "ful_after": false}, whole)
}
