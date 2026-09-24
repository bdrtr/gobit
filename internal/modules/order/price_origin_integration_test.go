//go:build integration

package order_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// A line remembers the price it was charged, on the real schema (ADR 0168).

// TestAPriceOriginSurvivesTheRoundTripOnTheRealSchema writes each of the three
// shapes the column allows and reads them back.
func TestAPriceOriginSurvivesTheRoundTripOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	for name, tc := range map[string]struct {
		priceID, listID, listType string
		want                      *models.LinePriceOrigin
	}{
		"unknown":      {},
		"a base price": {priceID: "price_BASE", want: &models.LinePriceOrigin{PriceID: "price_BASE"}},
		"a sale price": {
			priceID: "price_SALE", listID: "plist_SPRING", listType: "sale",
			want: &models.LinePriceOrigin{PriceID: "price_SALE", PriceListID: "plist_SPRING", PriceListType: "sale"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			in := validInput()
			in.Items[0].PriceID, in.Items[0].PriceListID, in.Items[0].PriceListType =
				tc.priceID, tc.listID, tc.listType

			order, err := svc.CreateOrder(ctx, in)
			require.NoError(t, err)

			detail, err := svc.GetOrder(ctx, order.ID)
			require.NoError(t, err)
			require.Len(t, detail.Items, 1)
			assert.Equal(t, tc.want, detail.Items[0].PriceOrigin)
		})
	}
}

// TestTheSchemaRefusesAnIncoherentOrigin is the column's own CHECK, reached
// past the service: the service refuses the same shapes as a 400, and the
// constraint is what holds them for a writer that is not the service.
func TestTheSchemaRefusesAnIncoherentOrigin(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	order, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	detail, err := svc.GetOrder(ctx, order.ID)
	require.NoError(t, err)
	lineID := detail.Items[0].ID

	for name, update := range map[string]string{
		"a list with no price":      `price_id = NULL, price_list_id = 'plist_1', price_list_type = 'sale'`,
		"a list price with no type": `price_id = 'price_1', price_list_id = 'plist_1', price_list_type = NULL`,
		"a type with no list":       `price_id = 'price_1', price_list_id = NULL, price_list_type = 'sale'`,
		"a type the ladder lacks":   `price_id = 'price_1', price_list_id = 'plist_1', price_list_type = 'bargain'`,
		"a blank price id":          `price_id = ' ', price_list_id = NULL, price_list_type = NULL`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := testPool.Pool().Exec(ctx,
				`UPDATE order_line_items SET `+update+` WHERE id = $1`, lineID)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "order_line_items_price_origin_coherent")
		})
	}
}
