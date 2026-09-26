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

// soldDelivery places soldExpress (one method of 2,500) and returns the order
// and its method's id.
func soldDelivery(t *testing.T, e env) (order models.Order, methodID string) {
	t.Helper()

	order, err := e.svc.CreateOrder(context.Background(), soldExpress())
	require.NoError(t, err)
	detail, err := e.svc.GetOrder(context.Background(), order.ID)
	require.NoError(t, err)
	require.Len(t, detail.ShippingMethods, 1)

	return order, detail.ShippingMethods[0].ID
}

// quote is a change to the given option at the given price.
func quote(methodID, optionID string, amount int64) service.ChangeDeliveryInput {
	return service.ChangeDeliveryInput{
		ShippingMethodID: methodID, ShippingOptionID: optionID, Name: optionID, Amount: amount,
	}
}

// TestACheaperDeliveryIsCreditedTheDifference writes the change and a credit
// line for the difference, and the change names it (ADR 0199).
func TestACheaperDeliveryIsCreditedTheDifference(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, methodID := soldDelivery(t, e)

	change, err := e.svc.ChangeDelivery(ctx, order.ID, quote(methodID, "so_pickup", 1000))
	require.NoError(t, err)
	require.NotNil(t, change)
	assert.Equal(t, int64(-1500), change.Difference)
	require.NotEmpty(t, change.CreditLineID)

	credits, err := e.svc.ListCreditLines(ctx, order.ID)
	require.NoError(t, err)
	require.Len(t, credits, 1)
	assert.Equal(t, change.CreditLineID, credits[0].ID)
	assert.Equal(t, int64(1500), credits[0].Amount)
	assert.Equal(t, service.CreditReasonDeliveryChange, credits[0].Reason)
	assert.Equal(t, change.ID, credits[0].Note, "the credit line names the change it wrote off")

	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1500), detail.CreditedTotal)
	assert.Equal(t, "so_express", detail.ShippingMethods[0].ShippingOptionID,
		"the method keeps what the order was sold")
	require.Len(t, detail.DeliveryChanges, 1)
	assert.Equal(t, change.ID, detail.DeliveryChanges[0].ID)
	option, err := service.NewInterop(e.svc).ShippingOptionOf(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, "so_pickup", option, "a parcel opened now goes on the new service")
}

// TestAnEquallyPricedDeliveryWritesNoCredit changes the service and nothing
// that is owed.
func TestAnEquallyPricedDeliveryWritesNoCredit(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, methodID := soldDelivery(t, e)

	change, err := e.svc.ChangeDelivery(ctx, order.ID, quote(methodID, "so_other_courier", 2500))
	require.NoError(t, err)
	require.NotNil(t, change)
	assert.Zero(t, change.Difference)
	assert.Empty(t, change.CreditLineID)

	credits, err := e.svc.ListCreditLines(ctx, order.ID)
	require.NoError(t, err)
	assert.Empty(t, credits)
}

// TestADearerDeliveryIsRefused writes nothing: no record can take the money
// yet (ADR 0199).
func TestADearerDeliveryIsRefused(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, methodID := soldDelivery(t, e)

	_, err := e.svc.ChangeDelivery(ctx, order.ID, quote(methodID, "so_same_day", 2501))

	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "%v", err)
	assert.Equal(t, service.CodeDeliveryCostsMore, errors.CodeOf(err))
	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Empty(t, detail.DeliveryChanges)
	assert.Zero(t, detail.CreditedTotal)
}

// TestEachChangeIsPricedAgainstTheOneBeforeIt reads the difference from the
// method's latest change, not from what the order was sold.
func TestEachChangeIsPricedAgainstTheOneBeforeIt(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, methodID := soldDelivery(t, e)

	_, err := e.svc.ChangeDelivery(ctx, order.ID, quote(methodID, "so_economy", 1000))
	require.NoError(t, err)

	// Dearer than the economy service it replaces, cheaper than the one sold.
	_, err = e.svc.ChangeDelivery(ctx, order.ID, quote(methodID, "so_standard", 2000))
	require.Error(t, err)
	assert.Equal(t, service.CodeDeliveryCostsMore, errors.CodeOf(err))

	change, err := e.svc.ChangeDelivery(ctx, order.ID, quote(methodID, "so_pickup", 400))
	require.NoError(t, err)
	require.NotNil(t, change)
	assert.Equal(t, int64(-600), change.Difference)

	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2100), detail.CreditedTotal, "1,500 and then 600")
	current := models.CurrentDeliveries(detail.ShippingMethods, detail.DeliveryChanges)
	require.Len(t, current, 1)
	assert.Equal(t, "so_pickup", current[0].ShippingOptionID)
	assert.Equal(t, int64(400), current[0].Amount)
	assert.Equal(t, methodID, current[0].ID)
}

// TestTheSameOptionAgainWritesNothing makes a repeated request no second
// change, against what the order was sold and against its latest change.
func TestTheSameOptionAgainWritesNothing(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, methodID := soldDelivery(t, e)

	change, err := e.svc.ChangeDelivery(ctx, order.ID, quote(methodID, "so_express", 2000))
	require.NoError(t, err)
	assert.Nil(t, change, "the delivery is already on the option it was sold")

	_, err = e.svc.ChangeDelivery(ctx, order.ID, quote(methodID, "so_pickup", 1000))
	require.NoError(t, err)
	change, err = e.svc.ChangeDelivery(ctx, order.ID, quote(methodID, "so_pickup", 900))
	require.NoError(t, err)
	assert.Nil(t, change, "a repeated change is not a second one, whatever the quote says now")

	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.Len(t, detail.DeliveryChanges, 1)
	assert.Equal(t, int64(1500), detail.CreditedTotal)
}

// TestOnlyAPendingOrdersDeliveryChanges refuses a canceled and a completed
// order.
func TestOnlyAPendingOrdersDeliveryChanges(t *testing.T) {
	for name, end := range map[string]func(e env, orderID string) error{
		"canceled": func(e env, orderID string) error {
			return e.svc.CancelOrder(context.Background(), orderID, "test")
		},
		"completed": func(e env, orderID string) error {
			_, err := e.svc.CompleteOrder(context.Background(), orderID)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			order, methodID := soldDelivery(t, e)
			require.NoError(t, end(e, order.ID))

			_, err := e.svc.ChangeDelivery(context.Background(), order.ID, quote(methodID, "so_pickup", 0))

			require.Error(t, err)
			assert.Equal(t, service.CodeDeliveryNotChangeable, errors.CodeOf(err))
		})
	}
}

// TestADeliveryChangeNamesAMethodOfTheOrder refuses another order's method, a
// made-up one and an order sold none.
func TestADeliveryChangeNamesAMethodOfTheOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, _ := soldDelivery(t, e)
	_, otherMethod := soldDelivery(t, e)
	plain, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	for _, attempt := range []struct{ orderID, methodID string }{
		{order.ID, otherMethod}, {order.ID, "oship_nope"}, {plain.ID, otherMethod},
	} {
		_, err := e.svc.ChangeDelivery(ctx, attempt.orderID, quote(attempt.methodID, "so_pickup", 0))
		require.Error(t, err)
		assert.True(t, errors.IsNotFound(err), "%v", err)
		assert.Equal(t, service.CodeDeliveryMissing, errors.CodeOf(err))
	}
}

// TestADeliveryChangeIsAQuote refuses a change with no method, no option, no
// name or a negative price before reading anything.
func TestADeliveryChangeIsAQuote(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order, methodID := soldDelivery(t, e)

	for name, in := range map[string]service.ChangeDeliveryInput{
		"no method":      quote("", "so_pickup", 0),
		"no option":      quote(methodID, "  ", 0),
		"no name":        {ShippingMethodID: methodID, ShippingOptionID: "so_pickup", Name: " "},
		"negative price": quote(methodID, "so_pickup", -1),
	} {
		_, err := e.svc.ChangeDelivery(ctx, order.ID, in)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalid(err), "%s: %v", name, err)
	}
}

// TestTheDeliverySurfaceSpeaksItsWireNames holds the facts a quote is asked on
// and the change's request and answer by the names a consumer writes.
func TestTheDeliverySurfaceSpeaksItsWireNames(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	interop := service.NewInterop(e.svc)

	in := soldExpress()
	in.Subtotal, in.DiscountTotal, in.Total = 3000, 400, 5700
	in.Addresses = []models.OrderAddress{{Type: models.AddressShipping, Address1: "1 Road", CountryCode: "TR"}}
	order, err := e.svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	raw, err := interop.DeliveryFactsJSON(ctx, order.ID)
	require.NoError(t, err)
	assert.JSONEq(t, `{"region_id":"`+testRegionID+`","currency_code":"TRY","country_code":"TR",
		"subtotal":2600,"item_count":3}`, string(raw))

	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	methodID := detail.ShippingMethods[0].ID

	_, err = interop.ChangeDeliveryJSON(ctx, order.ID, json.RawMessage(
		`{"shipping_method_id":"`+methodID+`","shipping_option_id":"so_pickup","name":"Pickup","amount":0,"note":"x"}`))
	require.Error(t, err, "a field the schema does not name is refused")

	raw, err = interop.ChangeDeliveryJSON(ctx, order.ID, json.RawMessage(
		`{"shipping_method_id":"`+methodID+`","shipping_option_id":"so_pickup","name":"Pickup","amount":0}`))
	require.NoError(t, err)
	var answer map[string]any
	require.NoError(t, json.Unmarshal(raw, &answer))
	assert.Equal(t, methodID, answer["shipping_method_id"])
	assert.Equal(t, "so_pickup", answer["shipping_option_id"])
	assert.Equal(t, "Pickup", answer["name"])
	assert.InDelta(t, 0, answer["amount"], 0)
	assert.InDelta(t, -2500, answer["difference"], 0)
	assert.NotEmpty(t, answer["credit_line_id"])
	assert.NotEmpty(t, answer["id"])

	raw, err = interop.ChangeDeliveryJSON(ctx, order.ID, json.RawMessage(
		`{"shipping_method_id":"`+methodID+`","shipping_option_id":"so_pickup","name":"Pickup","amount":0}`))
	require.NoError(t, err)
	assert.JSONEq(t, `null`, string(raw), "nothing changed")
}
