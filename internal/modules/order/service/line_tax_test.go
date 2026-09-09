package service_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// stackedInput is validInput with the line taxed by a stack of two rates.
//
// The amounts are the ones the tax module produces for 5% + 8% compound on a
// base of 3000: 150 and 252, adding to the 402 the line then carries. The
// order's own totals move with them, so the input stays consistent and the only
// thing under test is the breakdown.
func stackedInput() service.CreateOrderInput {
	in := validInput()
	in.TaxTotal = 402
	in.Total = 3000 - 0 + 402 + 2500
	in.Items[0].TaxTotal = 402
	in.Items[0].TaxRateBps = 500
	in.Items[0].Total = 3000 + 402
	in.Items[0].TaxComponents = []service.CreateOrderLineTaxInput{
		{RateID: "txr_base", RateBps: 500, TaxableAmount: 3000, TaxAmount: 150},
		{RateID: "txr_top", RateBps: 800, Compound: true, TaxableAmount: 3150, TaxAmount: 252},
	}
	return in
}

// TestAStackedLineKeepsEveryComponent is the record the invoice will read.
func TestAStackedLineKeepsEveryComponent(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, stackedInput())
	require.NoError(t, err)

	lines, err := e.store.ListLineItems(ctx, order.ID)
	require.NoError(t, err)
	require.Len(t, lines, 1)

	components := lines[0].TaxComponents
	require.Len(t, components, 2, "both rates that taxed the line must be stored")

	assert.True(t, strings.HasPrefix(components[0].ID, models.LineTaxIDPrefix))
	assert.Equal(t, lines[0].ID, components[0].OrderLineItemID)

	assert.Equal(t, int32(0), components[0].Position, "the base comes first")
	assert.Equal(t, "txr_base", components[0].RateID)
	assert.Equal(t, int32(500), components[0].RateBps)
	assert.False(t, components[0].Compound)
	assert.Equal(t, int64(3000), components[0].TaxableAmount)
	assert.Equal(t, int64(150), components[0].TaxAmount)

	assert.Equal(t, int32(1), components[1].Position)
	assert.True(t, components[1].Compound)
	assert.Equal(t, int64(3150), components[1].TaxableAmount,
		"the compound component's base is the line plus the tax below it")
	assert.Equal(t, int64(252), components[1].TaxAmount)

	assert.Equal(t, lines[0].TaxTotal,
		components[0].TaxAmount+components[1].TaxAmount,
		"the components must add up to the line's tax")
	assert.Equal(t, int32(500), lines[0].TaxRateBps,
		"the line keeps carrying the stack's BASE rate beside the list")
}

// TestASingleRateLineStoresNoComponents keeps the common line the size it was.
func TestASingleRateLineStoresNoComponents(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	lines, err := e.store.ListLineItems(ctx, order.ID)
	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.Empty(t, lines[0].TaxComponents)
}

// TestComponentsThatDoNotAddUpAreRefused is the identity the document rests on.
//
// Without it an invoice would print a breakdown whose sum is not what the
// customer was charged, and the two figures would be read by two people.
func TestComponentsThatDoNotAddUpAreRefused(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	in := stackedInput()
	in.Items[0].TaxComponents[1].TaxAmount = 251

	_, err := e.svc.CreateOrder(ctx, in)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Contains(t, err.Error(), "have to add up")
	assert.Empty(t, e.store.orders, "nothing may be written when the input is refused")
}

// TestASingleComponentIsRefused keeps an empty list the only way to say "one
// rate applied".
func TestASingleComponentIsRefused(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	in := validInput()
	in.Items[0].TaxComponents = []service.CreateOrderLineTaxInput{
		{RateBps: 2000, TaxableAmount: 3000, TaxAmount: 600},
	}

	_, err := e.svc.CreateOrder(ctx, in)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Contains(t, err.Error(), "says nothing the line does not")
}

// TestMoreComponentsThanTheLimitAreRefused bounds what one line can carry.
func TestMoreComponentsThanTheLimitAreRefused(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	in := validInput()
	in.Items[0].TaxComponents = []service.CreateOrderLineTaxInput{
		{RateBps: 100, TaxableAmount: 3000, TaxAmount: 120},
		{RateBps: 100, TaxableAmount: 3000, TaxAmount: 120},
		{RateBps: 100, TaxableAmount: 3000, TaxAmount: 120},
		{RateBps: 100, TaxableAmount: 3000, TaxAmount: 120},
		{RateBps: 100, TaxableAmount: 3000, TaxAmount: 120},
	}

	_, err := e.svc.CreateOrder(ctx, in)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Contains(t, err.Error(), "at most 4 components")
}

// TestAComponentOutsideTheRateRangeIsRefused applies the line's own bound one
// component at a time.
func TestAComponentOutsideTheRateRangeIsRefused(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	in := stackedInput()
	in.Items[0].TaxComponents[1].RateBps = 10_001

	_, err := e.svc.CreateOrder(ctx, in)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Contains(t, err.Error(), "must be in [0, 10000] basis points")
}

// TestAComponentTakingMoreThanItsBaseIsRefused catches the unit mix-up the
// line-level check cannot see.
//
// The line's tax stays inside the line's own amount while one component takes
// more than the base it was computed on.
func TestAComponentTakingMoreThanItsBaseIsRefused(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	in := stackedInput()
	in.Items[0].TaxComponents[0].TaxableAmount = 100

	_, err := e.svc.CreateOrder(ctx, in)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Contains(t, err.Error(), "cannot exceed its own base")
}

// TestTheFirstComponentCannotCompound refuses a stack that stands on nothing.
func TestTheFirstComponentCannotCompound(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	in := stackedInput()
	in.Items[0].TaxComponents[0].Compound = true

	_, err := e.svc.CreateOrder(ctx, in)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Contains(t, err.Error(), "stands on nothing and cannot be compound")
}

// TestTheSnapshotSchemaCarriesTheBreakdown is the boundary this whole change had
// to cross in one step.
//
// The snapshot parser IGNORES unknown fields on purpose, so a sender that
// started emitting "tax_components" before this type knew the field would have
// been accepted with the breakdown DROPPED IN SILENCE, and the order would hold
// a line that looks single-rate with nothing left to say otherwise. This test is
// what makes that drop impossible to reintroduce quietly.
func TestTheSnapshotSchemaCarriesTheBreakdown(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	interop := service.NewInterop(e.svc)

	orderID, err := interop.PlaceOrderJSON(ctx, json.RawMessage(`{
      "cart_id": "cart_TEST",
      "region_id": "`+testRegionID+`",
      "email": "customer@example.com",
      "currency_code": "TRY",
      "subtotal": 3000,
      "discount_total": 0,
      "tax_total": 402,
      "shipping_total": 0,
      "total": 3402,
      "items": [{
        "variant_id": "`+testVariantID+`",
        "title": "Red T-Shirt",
        "quantity": 3,
        "unit_price": 1000,
        "subtotal": 3000,
        "discount_total": 0,
        "tax_total": 402,
        "tax_rate_bps": 500,
        "total": 3402,
        "tax_components": [
          {"rate_id": "txr_base", "rate_bps": 500, "compound": false,
           "taxable_amount": 3000, "tax_amount": 150},
          {"rate_id": "txr_top", "rate_bps": 800, "compound": true,
           "taxable_amount": 3150, "tax_amount": 252}
        ]
      }]
    }`))
	require.NoError(t, err)

	lines, err := e.store.ListLineItems(ctx, orderID)
	require.NoError(t, err)
	require.Len(t, lines, 1)
	require.Len(t, lines[0].TaxComponents, 2,
		"the breakdown the snapshot carried must reach the line")

	assert.Equal(t, "txr_top", lines[0].TaxComponents[1].RateID)
	assert.Equal(t, int32(800), lines[0].TaxComponents[1].RateBps)
	assert.True(t, lines[0].TaxComponents[1].Compound)
	assert.Equal(t, int64(3150), lines[0].TaxComponents[1].TaxableAmount)
	assert.Equal(t, int64(252), lines[0].TaxComponents[1].TaxAmount)
}

// TestASnapshotWithoutABreakdownStillPlaces keeps every existing sender working.
func TestASnapshotWithoutABreakdownStillPlaces(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	interop := service.NewInterop(e.svc)

	orderID, err := interop.PlaceOrderJSON(ctx, json.RawMessage(`{
      "cart_id": "cart_TEST",
      "region_id": "`+testRegionID+`",
      "email": "customer@example.com",
      "currency_code": "TRY",
      "subtotal": 3000, "discount_total": 0, "tax_total": 600,
      "shipping_total": 0, "total": 3600,
      "items": [{
        "variant_id": "`+testVariantID+`", "title": "Red T-Shirt",
        "quantity": 3, "unit_price": 1000, "subtotal": 3000,
        "discount_total": 0, "tax_total": 600, "tax_rate_bps": 2000, "total": 3600
      }]
    }`))
	require.NoError(t, err)

	lines, err := e.store.ListLineItems(ctx, orderID)
	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.Empty(t, lines[0].TaxComponents)
}
