package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// additionOf is validInput naming parentID as the order it adds to.
func additionOf(parentID string) service.CreateOrderInput {
	in := validInput()
	in.CartID = "cart_ADDITION"
	in.AddsToOrderID = parentID

	return in
}

// TestAnAdditionIsAnOrderThatNamesItsParent is the decision in one test: the
// addition is written with its own amounts and the parent's id, the parent is
// read under a SHARE lock, and nothing about the parent changes (ADR 0192).
func TestAnAdditionIsAnOrderThatNamesItsParent(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	parent, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	addition, err := e.svc.CreateOrder(ctx, additionOf(parent.ID))
	require.NoError(t, err)

	assert.Equal(t, parent.ID, addition.AddsToOrderID)
	assert.NotEqual(t, parent.ID, addition.ID)
	assert.Equal(t, int64(6100), addition.Total, "the addition's amounts are its own")
	assert.Equal(t, []string{parent.ID}, e.store.sharedOrders,
		"the parent is read under a share lock in the write's transaction")
	assert.NotContains(t, e.store.lockedOrders, parent.ID,
		"an addition does not take the parent's exclusive lock: two additions do not wait for each other")

	again, err := e.svc.GetOrder(ctx, parent.ID)
	require.NoError(t, err)
	assert.Empty(t, again.AddsToOrderID)
	assert.Equal(t, parent.Total, again.Total, "nothing is summed into the parent")
	assert.Equal(t, models.OrderPending, again.Status)
}

// TestAnOrderThatAddsToNothingTakesNoLock keeps the ordinary checkout as it was.
func TestAnOrderThatAddsToNothingTakesNoLock(t *testing.T) {
	e := newEnv(t)

	order, err := e.svc.CreateOrder(context.Background(), validInput())
	require.NoError(t, err)

	assert.Empty(t, order.AddsToOrderID)
	assert.Empty(t, e.store.sharedOrders)
}

// additionCase is one refusal: a parent built so that exactly ONE rule fails,
// and the addition's customer and currency.
type additionCase struct {
	name     string
	parent   func(t *testing.T, e env) string
	customer string
	currency string
	kind     errors.Kind
	code     string
}

// placeParent writes a pending parent for customer, and returns its id.
func placeParent(t *testing.T, e env, customer string) string {
	t.Helper()

	in := validInput()
	in.CustomerID = customer
	parent, err := e.svc.CreateOrder(context.Background(), in)
	require.NoError(t, err)

	return parent.ID
}

// additionCases break one rule each. The guest case uses a GUEST parent so the
// customers are equal and only the missing customer can refuse it; every other
// parent is pending, of the test customer and in TRY unless the case says
// otherwise.
func additionCases() []additionCase {
	return []additionCase{
		{
			name:     "a guest adds to nothing",
			parent:   func(t *testing.T, e env) string { return placeParent(t, e, "") },
			customer: "", currency: testCurrency,
			kind: errors.KindConflict, code: service.CodeAdditionNeedsCustomer,
		},
		{
			name:     "another customer's order",
			parent:   func(t *testing.T, e env) string { return placeParent(t, e, "cus_OTHER") },
			customer: testCustomerID, currency: testCurrency,
			kind: errors.KindConflict, code: service.CodeAdditionCustomerMismatch,
		},
		{
			name:     "another currency",
			parent:   func(t *testing.T, e env) string { return placeParent(t, e, testCustomerID) },
			customer: testCustomerID, currency: "EUR",
			kind: errors.KindConflict, code: service.CodeAdditionCurrencyMismatch,
		},
		{
			name: "a canceled order",
			parent: func(t *testing.T, e env) string {
				id := placeParent(t, e, testCustomerID)
				require.NoError(t, e.svc.CancelOrder(context.Background(), id, "test"))

				return id
			},
			customer: testCustomerID, currency: testCurrency,
			kind: errors.KindConflict, code: service.CodeAdditionParentNotPending,
		},
		{
			name: "a completed order",
			parent: func(t *testing.T, e env) string {
				id := placeParent(t, e, testCustomerID)
				_, err := e.svc.CompleteOrder(context.Background(), id)
				require.NoError(t, err)

				return id
			},
			customer: testCustomerID, currency: testCurrency,
			kind: errors.KindConflict, code: service.CodeAdditionParentNotPending,
		},
		{
			name: "an order that is an addition itself",
			parent: func(t *testing.T, e env) string {
				root := placeParent(t, e, testCustomerID)
				first, err := e.svc.CreateOrder(context.Background(), additionOf(root))
				require.NoError(t, err)

				return first.ID
			},
			customer: testCustomerID, currency: testCurrency,
			kind: errors.KindConflict, code: service.CodeAdditionParentIsAddition,
		},
		{
			name:     "an order that does not exist",
			parent:   func(*testing.T, env) string { return "order_MISSING" },
			customer: testCustomerID, currency: testCurrency,
			kind: errors.KindNotFound, code: service.CodeOrderNotFound,
		},
	}
}

// TestAnAdditionIsRefusedAndNothingIsWritten runs every refusal through the
// write: the error names the rule, and no order is left behind.
func TestAnAdditionIsRefusedAndNothingIsWritten(t *testing.T) {
	for _, tc := range additionCases() {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			e := newEnv(t)
			parentID := tc.parent(t, e)

			before, err := e.svc.ListOrders(ctx, service.ListOrdersInput{})
			require.NoError(t, err)

			in := additionOf(parentID)
			in.CustomerID = tc.customer
			in.CurrencyCode = tc.currency
			_, err = e.svc.CreateOrder(ctx, in)

			require.Error(t, err, "the addition has to be refused")
			assert.Equal(t, tc.kind, errors.KindOf(err))
			assert.Equal(t, tc.code, errors.CodeOf(err))

			after, err := e.svc.ListOrders(ctx, service.ListOrdersInput{})
			require.NoError(t, err)
			assert.Equal(t, before.Count, after.Count, "a refused addition writes no order")
		})
	}
}

// TestCheckAdditionAsksWhatTheWriteAsks holds the early answer to the same
// rules, and to the same codes, without a lock.
func TestCheckAdditionAsksWhatTheWriteAsks(t *testing.T) {
	for _, tc := range additionCases() {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			parentID := tc.parent(t, e)

			err := e.svc.CheckAddition(context.Background(), parentID, tc.customer, tc.currency)

			require.Error(t, err)
			assert.Equal(t, tc.kind, errors.KindOf(err))
			assert.Equal(t, tc.code, errors.CodeOf(err))
		})
	}

	t.Run("a pending order of the same customer and currency", func(t *testing.T) {
		e := newEnv(t)
		parentID := placeParent(t, e, testCustomerID)
		shared := len(e.store.sharedOrders)

		require.NoError(t, e.svc.CheckAddition(context.Background(), parentID, testCustomerID, "try"),
			"the currency is compared in its stored form, whatever case the caller used")
		assert.Len(t, e.store.sharedOrders, shared, "the early answer takes no lock")
	})
}

// TestListOrdersReadsTheAdditionsOfAnOrder is the read the operator uses.
func TestListOrdersReadsTheAdditionsOfAnOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	parent := placeParent(t, e, testCustomerID)
	other := placeParent(t, e, testCustomerID)
	first, err := e.svc.CreateOrder(ctx, additionOf(parent))
	require.NoError(t, err)
	second, err := e.svc.CreateOrder(ctx, additionOf(parent))
	require.NoError(t, err)
	_, err = e.svc.CreateOrder(ctx, additionOf(other))
	require.NoError(t, err)

	listed, err := e.svc.ListOrders(ctx, service.ListOrdersInput{AddsToOrderID: &parent})
	require.NoError(t, err)

	assert.Equal(t, int64(2), listed.Count)
	ids := []string{}
	for i := range listed.Items {
		ids = append(ids, listed.Items[i].ID)
	}
	assert.ElementsMatch(t, []string{first.ID, second.ID}, ids)

	blank := " "
	_, err = e.svc.ListOrders(ctx, service.ListOrdersInput{AddsToOrderID: &blank})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
}

// TestPlaceOrderJSONReadsTheOrderItAddsTo proves the wire name the checkout
// writes, "adds_to_order_id", reaches the write. The order ignores fields it
// does not know, so a misspelled name would place an ordinary order in silence.
func TestPlaceOrderJSONReadsTheOrderItAddsTo(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	interop := service.NewInterop(e.svc)

	parent := placeParent(t, e, testCustomerID)

	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(snapshotJSON), &body))
	body["adds_to_order_id"] = parent
	body["idempotency_key"] = "wf_ADDITION"
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	orderID, err := interop.PlaceOrderJSON(ctx, raw)
	require.NoError(t, err)

	detail, err := e.svc.GetOrder(ctx, orderID)
	require.NoError(t, err)
	assert.Equal(t, parent, detail.AddsToOrderID)

	require.NoError(t, interop.CheckAddition(ctx, parent, testCustomerID, testCurrency))
	err = interop.CheckAddition(ctx, orderID, testCustomerID, testCurrency)
	assert.Equal(t, service.CodeAdditionParentIsAddition, errors.CodeOf(err),
		"the interop answers with the service's rule")
}

// shipsTo is a shipping address for the parcel tests.
func shipsTo(street string) models.OrderAddress {
	return models.OrderAddress{Type: models.AddressShipping, Address1: street, CountryCode: "TR"}
}

// TestAnAdditionShipsWithItsParent answers the parent for an addition going to
// the parent's address or to none, and refuses every other case (ADR 0197).
func TestAnAdditionShipsWithItsParent(t *testing.T) {
	for _, tc := range []struct {
		name     string
		parentTo []models.OrderAddress
		ownTo    []models.OrderAddress
		prepare  func(t *testing.T, e env, parentID, additionID string)
		orphan   bool
		code     string
	}{
		{name: "no address of its own", parentTo: []models.OrderAddress{shipsTo("12 Main St")}},
		{name: "the same address", parentTo: []models.OrderAddress{shipsTo("12 Main St")},
			ownTo: []models.OrderAddress{shipsTo("12 Main St")}},
		{name: "another address", parentTo: []models.OrderAddress{shipsTo("12 Main St")},
			ownTo: []models.OrderAddress{shipsTo("9 Far Road")}, code: service.CodeShipsElsewhere},
		{name: "an address where the parent has none",
			ownTo: []models.OrderAddress{shipsTo("9 Far Road")}, code: service.CodeShipsElsewhere},
		{name: "the same street with other delivery notes", parentTo: []models.OrderAddress{shipsTo("12 Main St")},
			ownTo: func() []models.OrderAddress {
				a := shipsTo("12 Main St")
				a.Metadata = map[string]any{"gate_code": "4411"}
				return []models.OrderAddress{a}
			}(), code: service.CodeShipsElsewhere},
		{name: "an order that adds to nothing", orphan: true, code: service.CodeShipsAlone},
		{name: "a canceled addition", prepare: func(t *testing.T, e env, _, additionID string) {
			require.NoError(t, e.svc.CancelOrder(context.Background(), additionID, "test"))
		}, code: service.CodeNotPending},
		{name: "a completed parent", prepare: func(t *testing.T, e env, parentID, _ string) {
			_, err := e.svc.CompleteOrder(context.Background(), parentID)
			require.NoError(t, err)
		}, code: service.CodeAdditionParentNotPending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			e := newEnv(t)
			parentIn := validInput()
			parentIn.Addresses = tc.parentTo
			parent, err := e.svc.CreateOrder(ctx, parentIn)
			require.NoError(t, err)

			in := additionOf(parent.ID)
			if tc.orphan {
				in.AddsToOrderID = ""
			}
			in.Addresses = tc.ownTo
			addition, err := e.svc.CreateOrder(ctx, in)
			require.NoError(t, err)
			if tc.prepare != nil {
				tc.prepare(t, e, parent.ID, addition.ID)
			}

			got, err := e.svc.ShippingParentOf(ctx, addition.ID)

			if tc.code == "" {
				require.NoError(t, err)
				assert.Equal(t, parent.ID, got)
				return
			}
			require.Error(t, err)
			assert.Equal(t, errors.KindConflict, errors.KindOf(err))
			assert.Equal(t, tc.code, errors.CodeOf(err))
		})
	}
}
