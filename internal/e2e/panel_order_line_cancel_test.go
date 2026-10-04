//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
)

// TestAnOperatorWritesOffALineInThePanel is ADR 0341 on the production
// wiring: a pending order's line offers its write-off with the count spoken
// for as read through the line entity; the unit is written off through the
// order module's registered surface and its stock comes back to the shelf;
// the same form sent again is refused, the line having moved.
func TestAnOperatorWritesOffALineInThePanel(t *testing.T) {
	ctx := t.Context()
	variantID, itemID := newStockedVariant(ctx, t, "E2E Panel Written Off", map[string]int64{taxedCurrency: 10_000}, 5)
	cartID, total := giftCart(t, variantID)
	completed := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		fmt.Sprintf(`{"payment_provider_id":%q,"expected_total":%d}`, offlineMethod, total))
	require.Equal(t, http.StatusOK, completed.Code, completed.Body.String())
	orderID, ok := storefrontData(t, completed)["order_id"].(string)
	require.True(t, ok, completed.Body.String())
	require.Equal(t, int64(4), stockLevel(ctx, t, itemID).StockedQuantity, "precondition: the sale deducted the unit")

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_warehouse", Kind: "user", Scopes: []string{"order:read", "order:write"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	pagePath := adminui.OrdersPath + "/" + orderID
	page := send(http.MethodGet, pagePath, nil).Body.String()
	line := regexp.MustCompile(`name="line" value="([^"]+)">\s*<input type="hidden" name="read_spoken_for" value="(\d+)">`).
		FindStringSubmatch(page)
	require.Len(t, line, 3, "the line offers its write-off")
	assert.Equal(t, "0", line[2], "nothing of it spoken for yet")

	form := url.Values{"line": {line[1]}, "read_spoken_for": {line[2]}, "quantity": {"1"}, "reason": {"out of stock"}}
	written := send(http.MethodPost, pagePath+"/line-cancellations", form)
	require.Equal(t, http.StatusOK, written.Code, written.Body.String())
	assert.Contains(t, written.Body.String(), "1 units were written off")
	var stocked int64
	require.Eventually(t, func() bool {
		levels, err := inventorySvc.ListInventoryLevels(ctx, itemID)
		if err != nil || len(levels) != 1 {
			return false
		}
		stocked = levels[0].StockedQuantity
		return stocked == 5
	}, eventWaitTimeout, 20*time.Millisecond, "the written-off unit comes back to the shelf (last read %d)", stocked)

	again := send(http.MethodPost, pagePath+"/line-cancellations", form)
	require.Equal(t, http.StatusUnprocessableEntity, again.Code, again.Body.String())
	assert.Contains(t, again.Body.String(), "draw the page again", "the same form sent twice writes off once")
}
