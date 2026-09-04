package service_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// snapshotJSON is the valid order snapshot used in the tests.
//
// The body IS WRITTEN BY HAND and is NOT PRODUCED from interop's own types. The
// reason is this: the schema is a cross-module contract and the consumer (the
// complete_cart workflow) builds this JSON in its own package. Had we produced
// the snapshot from interop's type, the test would change together with a field
// name and NO test would see that the contract was broken.
const snapshotJSON = `{
  "cart_id":         "cart_TEST",
  "region_id":       "reg_TEST",
  "customer_id":     "cus_TEST",
  "email":           "Customer@Example.COM",
  "currency_code":   "try",
  "idempotency_key": "wf_STEP_1",
  "subtotal":        3000,
  "discount_total":  0,
  "tax_total":       600,
  "shipping_total":  2500,
  "total":           6100,
  "metadata":        {"channel": "web"},
  "items": [
    {
      "variant_id":     "variant_TEST",
      "title":          "Red T-Shirt",
      "quantity":       3,
      "unit_price":     1000,
      "subtotal":       3000,
      "discount_total": 0,
      "tax_total":      600,
      "total":          3600,
      "metadata":       {"line": 1}
    }
  ]
}`

// TestPlaceOrderJSONReadsTheSchema validates that the snapshot schema is parsed
// EXACTLY as expected.
//
// This test is the only guarantee of the schema OUTSIDE compile time: because
// the order module cannot import the workflow package, the compiler cannot see a
// shift in the field names (ADR 0001/0006).
func TestPlaceOrderJSONReadsTheSchema(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	interop := service.NewInterop(e.svc)

	orderID, err := interop.PlaceOrderJSON(ctx, json.RawMessage(snapshotJSON))
	require.NoError(t, err)
	require.NotEmpty(t, orderID)

	detail, err := e.svc.GetOrder(ctx, orderID)
	require.NoError(t, err)

	assert.Equal(t, "reg_TEST", detail.RegionID)
	assert.Equal(t, "cus_TEST", detail.CustomerID)
	assert.Equal(t, "cart_TEST", detail.CartID)
	assert.Equal(t, "customer@example.com", detail.Email)
	assert.Equal(t, "TRY", detail.CurrencyCode)
	assert.Equal(t, "wf_STEP_1", detail.IdempotencyKey)
	assert.Equal(t, int64(3000), detail.Subtotal)
	assert.Equal(t, int64(600), detail.TaxTotal)
	assert.Equal(t, int64(2500), detail.ShippingTotal)
	assert.Equal(t, int64(6100), detail.Total)
	assert.Equal(t, map[string]any{"channel": "web"}, detail.Metadata)

	require.Len(t, detail.Items, 1)
	line := detail.Items[0]
	assert.Equal(t, "variant_TEST", line.VariantID)
	assert.Equal(t, "Red T-Shirt", line.Title)
	assert.Equal(t, int64(3), line.Quantity)
	assert.Equal(t, int64(1000), line.UnitPrice)
	assert.Equal(t, int64(3000), line.Subtotal)
	assert.Equal(t, int64(600), line.TaxTotal)
	assert.Equal(t, int64(3600), line.Total)
	assert.Equal(t, map[string]any{"line": float64(1)}, line.Metadata)
}

// TestPlaceOrderJSONIgnoresUnknownFields validates that the consumer can pass a
// WIDER snapshot.
//
// Strict parsing would make changing this module mandatory whenever a new field
// was added on the consumer's side and would lock the two packages to each other
// without a compile time dependency.
func TestPlaceOrderJSONIgnoresUnknownFields(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	interop := service.NewInterop(e.svc)

	wide := `{
      "region_id": "reg_TEST", "currency_code": "TRY",
      "revision": 7, "completed": true,
      "shipping_methods": [{"id": "csm_1", "amount": 2500}],
      "subtotal": 1000, "tax_total": 0, "shipping_total": 0, "total": 1000,
      "items": [{"variant_id": "v1", "title": "T", "quantity": 1,
                 "unit_price": 1000, "subtotal": 1000, "total": 1000,
                 "line_item_id": "li_1"}]
    }`

	orderID, err := interop.PlaceOrderJSON(ctx, json.RawMessage(wide))

	require.NoError(t, err, "unknown fields must not make the snapshot rejected")
	assert.NotEmpty(t, orderID)
}

// TestPlaceOrderJSONRejectsAMalformedBody validates that an unparseable body
// returns Invalid.
func TestPlaceOrderJSONRejectsAMalformedBody(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	interop := service.NewInterop(e.svc)

	_, err := interop.PlaceOrderJSON(ctx, json.RawMessage(`{"region_id":`))

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeInteropSnapshotInvalid, errors.CodeOf(err))
}

// TestPlaceOrderJSONRejectsAMissingRequiredField validates that missing fields
// are not ignored.
func TestPlaceOrderJSONRejectsAMissingRequiredField(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	interop := service.NewInterop(e.svc)

	_, err := interop.PlaceOrderJSON(ctx, json.RawMessage(`{"currency_code": "TRY"}`))

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestInteropPlaceOrderIsIdempotent validates that sending the same snapshot a
// second time does not open a new order.
//
// A saga can retry a step; without a key a repeat would mean opening a SECOND
// ORDER for the customer.
func TestInteropPlaceOrderIsIdempotent(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	interop := service.NewInterop(e.svc)

	first, err := interop.PlaceOrderJSON(ctx, json.RawMessage(snapshotJSON))
	require.NoError(t, err)
	second, err := interop.PlaceOrderJSON(ctx, json.RawMessage(snapshotJSON))
	require.NoError(t, err)

	assert.Equal(t, first, second)
	_, count, err := e.svc.ListOrders(ctx, service.ListOrdersInput{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

// TestInteropCancelOrderIsIdempotent validates that the saga compensation can be
// called twice from the primitive surface too.
func TestInteropCancelOrderIsIdempotent(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	interop := service.NewInterop(e.svc)

	orderID, err := interop.PlaceOrderJSON(ctx, json.RawMessage(snapshotJSON))
	require.NoError(t, err)

	require.NoError(t, interop.CancelOrder(ctx, orderID, "the payment was declined"))
	require.NoError(t, interop.CancelOrder(ctx, orderID, "a repeat of the compensation"))

	detail, err := e.svc.GetOrder(ctx, orderID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderCanceled, detail.Status)
}

// TestInteropCompleteOrderIsNotACompensation validates that completing returns
// an error on the second call.
func TestInteropCompleteOrderIsNotACompensation(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	interop := service.NewInterop(e.svc)

	orderID, err := interop.PlaceOrderJSON(ctx, json.RawMessage(snapshotJSON))
	require.NoError(t, err)

	require.NoError(t, interop.CompleteOrder(ctx, orderID))
	err = interop.CompleteOrder(ctx, orderID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}

// TestOrderContactJSONFillsTheFields validates the schema and the values of the
// notification surface.
//
// The body is decoded into a map[string]string: this is the only proof of the
// "ALL the values are strings" contract. Had one of the fields been written as a
// number the parsing would FALL OVER here; if the target type were a
// map[string]any the same shift would pass silently.
func TestOrderContactJSONFillsTheFields(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	interop := service.NewInterop(e.svc)

	in := validInput()
	in.Items = append(in.Items, service.CreateOrderItemInput{
		VariantID: "variant_SECOND", Title: "Blue Mug",
		Quantity: 1, UnitPrice: 500, Subtotal: 500, TaxTotal: 100, Total: 600,
	})
	in.Subtotal = 3500
	in.TaxTotal = 700
	in.Total = 6700
	order, err := e.svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	body, err := interop.OrderContactJSON(ctx, order.ID)
	require.NoError(t, err)

	var fields map[string]string
	require.NoError(t, json.Unmarshal(body, &fields),
		"ALL the values of the payload have to be strings")

	assert.Equal(t, map[string]string{
		"order_id":      order.ID,
		"display_id":    strconv.FormatInt(order.DisplayID, 10),
		"email":         "customer@example.com",
		"currency_code": "TRY",
		"total":         "6700",
		"item_count":    "2",
	}, fields)
}

// TestOrderContactJSONUsesTheSameNamesAsTheEventPayload validates that the
// surface and the "order.placed" event carry the same fields UNDER THE SAME
// NAME.
//
// The subscriber uses the two side by side: the order_id comes from the event,
// the rest from here. If one of the names shifted the subscriber could not find
// the field and the gap would only show up when the notification could not be
// sent — that is, in production.
func TestOrderContactJSONUsesTheSameNamesAsTheEventPayload(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	interop := service.NewInterop(e.svc)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	body, err := interop.OrderContactJSON(ctx, order.ID)
	require.NoError(t, err)
	var fields map[string]string
	require.NoError(t, json.Unmarshal(body, &fields))

	events := e.bus.events()
	require.Len(t, events, 1)
	event := events[0]
	for _, field := range []string{
		service.EventFieldOrderID,
		service.EventFieldDisplayID,
		service.EventFieldCurrencyCode,
		service.EventFieldTotal,
		service.EventFieldItemCount,
	} {
		require.Contains(t, fields, field, "the surface has to carry the field of the event payload")
		assert.Equal(t, event.Data[field], fields[field],
			"the %q field has to carry the same value as the event", field)
	}
}

// TestOrderContactJSONReturnsAnEmptyFieldForAnOrderWithoutAnEmail validates that
// an order without an address returns NOT an error but an empty field.
//
// For the subscriber "there is no address to send to" is a permanent state and
// it has to be skipped; returning an error would make it impossible to tell
// apart from a fault that will be retried (rationale:
// [service.Interop.OrderContactJSON]).
func TestOrderContactJSONReturnsAnEmptyFieldForAnOrderWithoutAnEmail(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	interop := service.NewInterop(e.svc)

	in := validInput()
	in.Email = ""
	order, err := e.svc.CreateOrder(ctx, in)
	require.NoError(t, err)

	body, err := interop.OrderContactJSON(ctx, order.ID)
	require.NoError(t, err)

	var fields map[string]string
	require.NoError(t, json.Unmarshal(body, &fields))
	require.Contains(t, fields, "email", "the field MUST NOT FALL out of the body")
	assert.Empty(t, fields["email"])
}

// TestOrderContactJSONNotFoundOnAMissingOrder validates that NotFound is
// returned.
func TestOrderContactJSONNotFoundOnAMissingOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	interop := service.NewInterop(e.svc)

	_, err := interop.OrderContactJSON(ctx, "order_MISSING")

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "got: %v", err)
}

// TestOrderContactJSONRejectsAnEmptyID validates that an empty identifier
// returns Invalid without going to the database at all.
func TestOrderContactJSONRejectsAnEmptyID(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	interop := service.NewInterop(e.svc)

	_, err := interop.OrderContactJSON(ctx, "")

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}
