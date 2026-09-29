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

// inclusiveInput is an order sold in a market whose prices include their tax
// (ADR 0246): three stickers of 1200 less a discount of 300 leave 3300, which
// holds 550 of tax at 20%, so the line's subtotal is 2750 + 300 = 3050.
func inclusiveInput() service.CreateOrderInput {
	in := soldExpress()
	in.PricesIncludeTax = true
	in.Subtotal, in.DiscountTotal, in.TaxTotal, in.Total = 3050, 300, 550, 5800
	in.Items[0].UnitPrice = 1200
	in.Items[0].Subtotal, in.Items[0].DiscountTotal, in.Items[0].TaxTotal, in.Items[0].Total = 3050, 300, 550, 3300
	in.Addresses = []models.OrderAddress{{Type: models.AddressShipping, Address1: "1 Road", CountryCode: "TR"}}

	return in
}

// TestAnInclusiveLineIsHeldToItsStickerLessItsTax verifies the order's subtotal
// check in a market whose prices include their tax (ADR 0246, D169). The same
// line is refused without the flag, and a line whose tax was counted on top of
// its sticker is refused with it.
func TestAnInclusiveLineIsHeldToItsStickerLessItsTax(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, inclusiveInput())
	require.NoError(t, err)
	assert.True(t, order.PricesIncludeTax)
	detail, err := e.svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	assert.True(t, detail.PricesIncludeTax, "the order keeps the flag its lines were checked under")

	withoutFlag := inclusiveInput()
	withoutFlag.PricesIncludeTax = false
	_, err = e.svc.CreateOrder(ctx, withoutFlag)
	require.Error(t, err, "without the flag the subtotal is unit price x quantity")
	assert.Equal(t, service.CodeTotalsInconsistent, errors.CodeOf(err))

	counted := inclusiveInput()
	counted.Subtotal, counted.Total = 3600, 6350
	counted.Items[0].Subtotal, counted.Items[0].Total = 3600, 3850
	_, err = e.svc.CreateOrder(ctx, counted)
	require.Error(t, err, "a tax counted on top of a sticker that includes it is refused")
	assert.Equal(t, service.CodeTotalsInconsistent, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "include their tax")
}

// TestPlaceOrderJSONReadsWhetherThePricesIncludeTax proves the wire name the
// checkout writes, "prices_include_tax", reaches the write: the order ignores
// fields it does not know, and without the flag the line below is refused.
func TestPlaceOrderJSONReadsWhetherThePricesIncludeTax(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	interop := service.NewInterop(e.svc)

	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(snapshotJSON), &body))
	body["prices_include_tax"] = true
	body["subtotal"], body["total"] = 2400, 5500
	items, ok := body["items"].([]any)
	require.True(t, ok)
	item, ok := items[0].(map[string]any)
	require.True(t, ok)
	item["subtotal"], item["total"] = 2400, 3000
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	orderID, err := interop.PlaceOrderJSON(ctx, raw)
	require.NoError(t, err)
	detail, err := e.svc.GetOrder(ctx, orderID)
	require.NoError(t, err)
	assert.True(t, detail.PricesIncludeTax)
	assert.Equal(t, int64(2400), detail.Items[0].Subtotal)
}

// TestTheDeliveryFactsReadTheGoodsAsTheCartQuotedThem verifies that a new
// delivery is quoted on the goods the cart's quote read, the stickers less the
// discount, where the order keeps its subtotal net of an included tax
// (ADR 0246). Read from the subtotal the goods would be 2750, and a threshold
// between the two would quote the delivery differently from the cart.
func TestTheDeliveryFactsReadTheGoodsAsTheCartQuotedThem(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	interop := service.NewInterop(e.svc)

	order, err := e.svc.CreateOrder(ctx, inclusiveInput())
	require.NoError(t, err)

	raw, err := interop.DeliveryFactsJSON(ctx, order.ID)
	require.NoError(t, err)
	assert.JSONEq(t, `{"region_id":"`+testRegionID+`","currency_code":"TRY","country_code":"TR",
		"subtotal":3300,"item_count":3}`, string(raw))
}
