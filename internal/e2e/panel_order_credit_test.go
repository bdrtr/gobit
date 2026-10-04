//go:build integration

package e2e

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
)

// orderWriter is the panel on the production wiring, sending as an operator
// who may read and write orders, and holds the extra privileges given.
func orderWriter(t *testing.T, extra ...string) func(method, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)

	return func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_desk", Kind: "user", Scopes: append([]string{"order:read", "order:write"}, extra...),
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
}

// readCredited is the credited total the page's credit form carries.
var readCredited = regexp.MustCompile(`name="read_credited" value="(\d+)"`)

// creditCount is how many credit lines the admin API lists for the order.
func creditCount(t *testing.T, orderID string) int {
	t.Helper()

	rec := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+orderID+"/credit-lines", "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	return strings.Count(rec.Body.String(), `"reason"`)
}

// TestAnOperatorCreditsAnOrderInThePanel is ADR 0388 on the production
// wiring: the order's page carries the credited total through the order
// module's registered surface; a credit from it is written, the page drawn
// again carries the new total, a credit from that goes through, and the
// first form sent again is refused, the total having moved.
func TestAnOperatorCreditsAnOrderInThePanel(t *testing.T) {
	orderID, _ := deliveryOrder(t, spyOptionPriced(t, soldDeliveryFee, false))
	send := orderWriter(t)
	pagePath := adminui.OrdersPath + "/" + orderID

	page := send(http.MethodGet, pagePath, nil).Body.String()
	read := readCredited.FindStringSubmatch(page)
	require.Len(t, read, 2, "the order page offers the credit")
	assert.Equal(t, "0", read[1], "nothing credited yet")

	first := url.Values{
		"read_credited": {read[1]}, "amount": {"10.00"}, "currency": {taxedCurrency},
		"reason": {"goodwill"}, "note": {"late parcel"},
	}
	credited := send(http.MethodPost, pagePath+"/credit-lines", first)
	require.Equal(t, http.StatusOK, credited.Code, credited.Body.String())
	assert.Contains(t, credited.Body.String(), "10.00 TRY was credited")
	read = readCredited.FindStringSubmatch(credited.Body.String())
	require.Len(t, read, 2)
	assert.Equal(t, "1000", read[1], "the page drawn again carries the sum")
	assert.Contains(t, credited.Body.String(), "late parcel")

	second := url.Values{
		"read_credited": {read[1]}, "amount": {"5.00"}, "currency": {taxedCurrency}, "reason": {"goodwill"},
	}
	credited = send(http.MethodPost, pagePath+"/credit-lines", second)
	require.Equal(t, http.StatusOK, credited.Code, credited.Body.String())
	assert.Equal(t, "1500", readCredited.FindStringSubmatch(credited.Body.String())[1],
		"the sum of the two, not their count or the last")
	assert.Equal(t, 2, creditCount(t, orderID))

	again := send(http.MethodPost, pagePath+"/credit-lines", first)
	require.Equal(t, http.StatusUnprocessableEntity, again.Code, again.Body.String())
	assert.Contains(t, again.Body.String(), "draw the page again", "the same form sent twice credits once")
	assert.Equal(t, 2, creditCount(t, orderID))
}
