package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestALineSaysItSoldAGiftCard reads the flag off both order views (ADR 0211).
func TestALineSaysItSoldAGiftCard(t *testing.T) {
	detail := sampleDetail()
	card := detail.Items[0]
	card.ID, card.IsGiftcard = "oli_card", true
	detail.Items = append(detail.Items, card)

	for _, path := range []string{"/admin/v1/orders/order_1", "/store/v1/orders/order_1"} {
		t.Run(path, func(t *testing.T) {
			rec := doRequest(t, newRouter(&fakeOrders{detail: detail}), http.MethodGet, path, "")
			require.Equal(t, http.StatusOK, rec.Code)

			data, ok := decodeResponse(t, rec)["data"].(map[string]any)
			require.True(t, ok)
			items, ok := data["items"].([]any)
			require.True(t, ok)
			flags := make([]any, 0, len(items))
			for _, item := range items {
				line, isMap := item.(map[string]any)
				require.True(t, isMap)
				flags = append(flags, line["is_giftcard"])
			}

			assert.Equal(t, []any{false, true}, flags)
		})
	}
}
