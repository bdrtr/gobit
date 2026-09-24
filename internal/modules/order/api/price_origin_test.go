package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// TestALinesPriceOriginIsPublished reads the three shapes a line's origin takes
// off both order views (ADR 0168): absent when it is unknown, a price with a
// null list for a base price, and the list and its type for a list price.
func TestALinesPriceOriginIsPublished(t *testing.T) {
	detail := sampleDetail()
	unknown := detail.Items[0]
	base, sale := unknown, unknown
	base.ID, base.PriceOrigin = "oli_2", &models.LinePriceOrigin{PriceID: "price_BASE"}
	sale.ID, sale.PriceOrigin = "oli_3", &models.LinePriceOrigin{
		PriceID: "price_SALE", PriceListID: "plist_SPRING", PriceListType: "sale",
	}
	detail.Items = []models.OrderLineItem{unknown, base, sale}

	for _, path := range []string{"/admin/v1/orders/order_1", "/store/v1/orders/order_1"} {
		t.Run(path, func(t *testing.T) {
			rec := doRequest(t, newRouter(&fakeOrders{detail: detail}), http.MethodGet, path, "")
			require.Equal(t, http.StatusOK, rec.Code)

			data, ok := decodeResponse(t, rec)["data"].(map[string]any)
			require.True(t, ok)
			items, ok := data["items"].([]any)
			require.True(t, ok)
			require.Len(t, items, 3)
			origins := make([]any, len(items))
			present := make([]bool, len(items))
			for i, item := range items {
				line, isMap := item.(map[string]any)
				require.True(t, isMap)
				origins[i], present[i] = line["price_origin"]
			}

			assert.False(t, present[0], "an unknown origin is absent, not an empty one that reads as a base price")
			assert.Equal(t, map[string]any{
				"price_id": "price_BASE", "price_list_id": nil, "price_list_type": nil,
			}, origins[1])
			assert.Equal(t, map[string]any{
				"price_id": "price_SALE", "price_list_id": "plist_SPRING", "price_list_type": "sale",
			}, origins[2])
		})
	}
}
