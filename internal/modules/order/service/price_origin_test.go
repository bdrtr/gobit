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

// A line remembers the price it was charged (ADR 0168).

// TestTheSnapshotSchemaCarriesThePriceOrigin is the boundary the origin had to
// cross in the same step as its sender.
//
// The snapshot parser ignores unknown fields on purpose, so a sender that began
// emitting the origin before this schema knew it would have had it dropped in
// silence, and every order would have recorded "unknown" as if it were an
// answer.
func TestTheSnapshotSchemaCarriesThePriceOrigin(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	orderID, err := service.NewInterop(e.svc).PlaceOrderJSON(ctx, json.RawMessage(`{
      "cart_id": "cart_TEST",
      "region_id": "`+testRegionID+`",
      "email": "customer@example.com",
      "currency_code": "TRY",
      "subtotal": 3000, "discount_total": 0, "tax_total": 600,
      "shipping_total": 0, "total": 3600,
      "items": [{
        "variant_id": "`+testVariantID+`", "title": "Red T-Shirt",
        "quantity": 3, "unit_price": 1000, "subtotal": 3000,
        "discount_total": 0, "tax_total": 600, "tax_rate_bps": 2000, "total": 3600,
        "price_id": "price_SALE", "price_list_id": "plist_SPRING", "price_list_type": "sale"
      }]
    }`))
	require.NoError(t, err)

	lines, err := e.store.ListLineItems(ctx, orderID)
	require.NoError(t, err)
	require.Len(t, lines, 1)
	require.NotNil(t, lines[0].PriceOrigin, "the origin the snapshot carried has to be kept")
	assert.Equal(t, models.LinePriceOrigin{
		PriceID: "price_SALE", PriceListID: "plist_SPRING", PriceListType: "sale",
	}, *lines[0].PriceOrigin)
}

// TestALineWithNoOriginIsStillSold is the recovery path.
//
// A saga recovered after the upgrade can place an order from a plan written
// before the checkout carried an origin. The money was taken either way, so the
// order is placed and the line's origin is UNKNOWN — nil, not an empty origin
// that would read as a base price.
func TestALineWithNoOriginIsStillSold(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	order, err := e.svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	lines, err := e.store.ListLineItems(ctx, order.ID)
	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.Nil(t, lines[0].PriceOrigin)
}

// TestAnIncoherentOriginIsRefused holds the three shapes the column allows —
// unknown, a base price, a list price with its type — as a 400 before the
// database would refuse the rest as a server error.
func TestAnIncoherentOriginIsRefused(t *testing.T) {
	for name, edit := range map[string]func(*service.CreateOrderItemInput){
		"a list with no price": func(item *service.CreateOrderItemInput) {
			item.PriceListID, item.PriceListType = "plist_1", "sale"
		},
		"a list price with no type": func(item *service.CreateOrderItemInput) {
			item.PriceID, item.PriceListID = "price_1", "plist_1"
		},
		"a type with no list": func(item *service.CreateOrderItemInput) {
			item.PriceID, item.PriceListType = "price_1", "override"
		},
		"a type the ladder does not have": func(item *service.CreateOrderItemInput) {
			item.PriceID, item.PriceListID, item.PriceListType = "price_1", "plist_1", "bargain"
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			in := validInput()
			edit(&in.Items[0])

			_, err := e.svc.CreateOrder(context.Background(), in)

			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "an incoherent origin is the caller's fault: %v", err)
			assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
		})
	}

	t.Run("a base price", func(t *testing.T) {
		e := newEnv(t)
		in := validInput()
		in.Items[0].PriceID = "price_BASE"

		order, err := e.svc.CreateOrder(context.Background(), in)
		require.NoError(t, err)

		lines, err := e.store.ListLineItems(context.Background(), order.ID)
		require.NoError(t, err)
		require.NotNil(t, lines[0].PriceOrigin)
		assert.Equal(t, models.LinePriceOrigin{PriceID: "price_BASE"}, *lines[0].PriceOrigin)
	})
}
