package service_test

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// costedSnapshot is an order snapshot whose three lines carry a cost, a cost of
// zero and none, written by hand for snapshotJSON's reason: the schema is the
// checkout's, and a body built from this module's types would follow a renamed
// field without anyone noticing.
func costedSnapshot(cost string) string {
	return `{
  "region_id": "reg_TEST", "currency_code": "TRY", "idempotency_key": "wf_COST_` + cost + `",
  "subtotal": 1300, "discount_total": 0, "tax_total": 0, "shipping_total": 0, "total": 1300,
  "items": [
    {"variant_id": "v_costed", "title": "Kettle", "quantity": 1, "unit_price": 1000,
     "subtotal": 1000, "discount_total": 0, "tax_total": 0, "total": 1000, "unit_cost": ` + cost + `},
    {"variant_id": "v_free", "title": "Sample", "quantity": 1, "unit_price": 200,
     "subtotal": 200, "discount_total": 0, "tax_total": 0, "total": 200, "unit_cost": 0},
    {"variant_id": "v_unknown", "title": "Mug", "quantity": 1, "unit_price": 100,
     "subtotal": 100, "discount_total": 0, "tax_total": 0, "total": 100}
  ]
}`
}

// TestPlaceOrderJSONKeepsTheUnitCost is ADR 0401 at the order's door: the cost
// the checkout sends reaches the line as sent, zero included, and a line sent
// without one keeps none.
func TestPlaceOrderJSONKeepsTheUnitCost(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	orderID, err := service.NewInterop(e.svc).PlaceOrderJSON(ctx, json.RawMessage(costedSnapshot("400")))
	require.NoError(t, err)
	detail, err := e.svc.GetOrder(ctx, orderID)
	require.NoError(t, err)

	costs := map[string]*int64{}
	for _, line := range detail.Items {
		costs[line.VariantID] = line.UnitCost
	}
	require.NotNil(t, costs["v_costed"])
	assert.Equal(t, int64(400), *costs["v_costed"])
	require.NotNil(t, costs["v_free"], "a cost of zero is kept as zero")
	assert.Equal(t, int64(0), *costs["v_free"])
	assert.Nil(t, costs["v_unknown"], "a line sent without a cost keeps none")
}

// TestAnOrderLineCostOutOfRangeIsRefused is the order's own guard behind the
// checkout's: a cost outside a unit amount's range refuses the order whole.
func TestAnOrderLineCostOutOfRangeIsRefused(t *testing.T) {
	ctx := context.Background()
	for name, cost := range map[string]int64{
		"negative":       -1,
		"past the bound": models.MaxAmount + 1,
	} {
		e := newEnv(t)
		_, err := service.NewInterop(e.svc).PlaceOrderJSON(ctx,
			json.RawMessage(costedSnapshot(strconv.FormatInt(cost, 10))))
		require.Error(t, err, name)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "%s: %v", name, err)
		assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err), name)
		assert.Contains(t, err.Error(), "unit_cost", name)
		assert.Empty(t, e.store.orders, "%s: no order is opened", name)
	}

	e := newEnv(t)
	_, err := service.NewInterop(e.svc).PlaceOrderJSON(ctx,
		json.RawMessage(costedSnapshot(strconv.FormatInt(models.MaxAmount, 10))))
	require.NoError(t, err, "the bound is a cost")
}

// TestTheInvoiceSurfaceCarriesNoCost: the order detail the invoicing flow reads
// names no cost and not the line's amount; a document a buyer receives is no
// place for what the goods cost the shop.
func TestTheInvoiceSurfaceCarriesNoCost(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	in := validInput()
	planted := int64(987_654_321)
	in.Items[0].UnitCost = &planted
	order, err := e.svc.CreateOrder(ctx, in)
	require.NoError(t, err)
	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	require.NotNil(t, detail.Items[0].UnitCost, "the line keeps the cost the invoice must not carry")

	raw, err := service.NewInterop(e.svc).OrderInvoiceJSON(ctx, order.ID)
	require.NoError(t, err)
	assert.NotContains(t, strings.ToLower(string(raw)), "cost")
	assert.NotContains(t, string(raw), "987654321")
}
