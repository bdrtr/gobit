package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// The tests here are about the BOUND on a parcel's contents.
//
// Before it existed this endpoint checked a blank identifier, a global quantity
// range and a duplicate line, and nothing about the order — so a parcel could hold
// a line the order never had, more units than were sold, or units somebody had
// already been told were canceled (ADR 0135).

// TestAParcelCannotHoldMoreThanTheLineOwes is the rule.
func TestAParcelCannotHoldMoreThanTheLineOwes(t *testing.T) {
	setup := newSetup(t)
	optionID := readyOption(t, setup)
	setup.bound.owed = map[string]int64{"oli_1": 2}

	_, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: optionID,
		IdempotencyKey:   "key-over",
		Items: []service.FulfillmentItemInput{
			{LineItemID: "oli_1", Quantity: 3},
		},
	})

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err),
		"a parcel asking for more than is owed is a conflict, not a bad request: the "+
			"request was well formed and the WORLD says no")
	assert.Contains(t, err.Error(), service.CodeLineNotDispatchable)
	assert.Empty(t, setup.provider.createInputs,
		"and nothing reached the provider, so no label was printed")
}

// TestAParcelMayHoldExactlyWhatIsOwed is the boundary on the other side.
func TestAParcelMayHoldExactlyWhatIsOwed(t *testing.T) {
	setup := newSetup(t)
	optionID := readyOption(t, setup)
	setup.bound.owed = map[string]int64{"oli_1": 2}

	ful, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: optionID,
		IdempotencyKey:   "key-exact",
		Items: []service.FulfillmentItemInput{
			{LineItemID: "oli_1", Quantity: 2},
		},
	})

	require.NoError(t, err)
	require.Len(t, ful.Items, 1)
	assert.Equal(t, int64(2), ful.Items[0].Quantity)
}

// TestALineTheOrderDoesNotHaveIsRefused holds the meaning of an ABSENT answer.
//
// The flow leaves a line it does not know out of the map rather than answering
// zero, because zero is what a fully shipped line answers. So absence has to be a
// refusal here: reading it as "no bound" would let a typo open a parcel for goods no
// order sold.
func TestALineTheOrderDoesNotHaveIsRefused(t *testing.T) {
	setup := newSetup(t)
	optionID := readyOption(t, setup)
	setup.bound.owed = map[string]int64{"oli_1": 5}

	_, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: optionID,
		IdempotencyKey:   "key-unknown-line",
		Items: []service.FulfillmentItemInput{
			{LineItemID: "oli_1", Quantity: 1},
			{LineItemID: "oli_NOT_ON_THE_ORDER", Quantity: 1},
		},
	})

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err),
		"a line the order does not have is a bad request rather than a conflict: no "+
			"state of the world makes it right")
	assert.Contains(t, err.Error(), service.CodeLineNotDispatchable)
}

// TestALineThatOwesNothingIsRefused is the canceled case, as the module sees it.
func TestALineThatOwesNothingIsRefused(t *testing.T) {
	setup := newSetup(t)
	optionID := readyOption(t, setup)
	setup.bound.owed = map[string]int64{"oli_1": 0}

	_, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: optionID,
		IdempotencyKey:   "key-owes-nothing",
		Items: []service.FulfillmentItemInput{
			{LineItemID: "oli_1", Quantity: 1},
		},
	})

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))
}

// TestAParcelCannotBeOpenedWhenTheBoundCannotBeREAD is the fail-closed direction.
//
// A bound that cannot be read is not a bound. Opening the parcel anyway would make
// the check a thing that works while the flow is wired and silently stops being one
// when it is not — which is the shape this repository refuses for an unconfigured
// authenticator too (ADR 0007).
func TestAParcelCannotBeOpenedWhenTheBoundCannotBeREAD(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setUp func(*testSetup)
	}{
		{
			name:  "the flow answers an error",
			setUp: func(s *testSetup) { s.bound.err = errors.New("the flow is unreachable") },
		},
		{
			name:  "nobody is bound at all",
			setUp: func(s *testSetup) { s.svc = serviceWithNoBound(t, s) },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setup := newSetup(t)
			optionID := readyOption(t, setup)
			tc.setUp(&setup)

			_, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
				Reference:        "order_1",
				ShippingOptionID: optionID,
				IdempotencyKey:   "key-unreadable",
				Items: []service.FulfillmentItemInput{
					{LineItemID: "oli_1", Quantity: 1},
				},
			})

			require.Error(t, err)
			assert.Contains(t, err.Error(), service.CodeDispatchBoundUnknown)
			assert.Empty(t, setup.provider.createInputs, "and no label was printed")
		})
	}
}

// TestARetryAsksTheBoundNOTHING is what keeps the check from breaking idempotency.
//
// The first request's units are counted as committed the moment it succeeds, so a
// retry re-checked against the bound would be refused — and a retry is exactly the
// request that must come back with the existing shipment rather than an error.
func TestARetryAsksTheBoundNOTHING(t *testing.T) {
	setup := newSetup(t)
	optionID := readyOption(t, setup)
	setup.bound.owed = map[string]int64{"oli_1": 1}

	first, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: optionID,
		IdempotencyKey:   "key-retry",
		Items: []service.FulfillmentItemInput{
			{LineItemID: "oli_1", Quantity: 1},
		},
	})
	require.NoError(t, err)
	asked := setup.bound.asked()

	// The same key again, and by now the bound would say zero.
	setup.bound.owed = map[string]int64{"oli_1": 0}

	second, err := setup.svc.CreateFulfillment(context.Background(), service.CreateFulfillmentInput{
		Reference:        "order_1",
		ShippingOptionID: optionID,
		IdempotencyKey:   "key-retry",
		Items: []service.FulfillmentItemInput{
			{LineItemID: "oli_1", Quantity: 1},
		},
	})

	require.NoError(t, err, "a retry has to come back with the shipment, not a refusal")
	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, asked, setup.bound.asked(),
		"and it asked the bound NOTHING: the parcel it names is already counted as "+
			"committed, so the question would answer no")
}

// serviceWithNoBound rebuilds the setup's service with NO dispatch bound.
//
// It is the composition fault the module fails closed on: a deployment where the
// fulfilling flow was never provided. Written as its own constructor rather than by
// nilling a field, because nilling one after construction is a state no installation
// can produce.
func serviceWithNoBound(t *testing.T, setup *testSetup) *service.Service {
	t.Helper()

	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(setup.provider))

	svc, err := service.New(service.Options{Store: setup.store, Providers: registry})
	require.NoError(t, err)

	return svc
}
