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

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
)

// TestAnOperatorShipsOneOfAnOrdersDeliveriesInThePanel is ADR 0332 on the
// production wiring: an order the storefront sold two deliveries is asked on
// its page which one a parcel goes on, each delivery named with its option
// through the order module's registered surface, and the parcel reaches the
// carrier on the option of the delivery chosen. Before ADR 0332 the form
// named none and the flow refused an order it could not default (D207).
func TestAnOperatorShipsOneOfAnOrdersDeliveriesInThePanel(t *testing.T) {
	customerID, email := newCustomer(t.Context(), t)
	variantID, _ := newStockedVariant(t.Context(), t, "E2E Panel Two Deliveries", map[string]int64{
		taxedCurrency: additionUnitPrice,
	}, additionStock)
	post, freight := spyOptionPriced(t, soldDeliveryFee, false), spyOptionPriced(t, soldDeliveryFee, false)

	f := additionFixture{customerID: customerID, email: email, variantID: variantID}
	cartID := f.openCart(t, "")
	address := fmt.Sprintf(`{"first_name":"Ada","address_1":"12 Main St","city":"Springfield",`+
		`"postal_code":"62701","country_code":%q}`, taxedCountry)
	rec := storefrontRequest(t, http.MethodPut, "/store/v1/carts/"+cartID+"/shipping-address", address)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	for _, option := range []string{post, freight} {
		rec = storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/shipping-methods",
			fmt.Sprintf(`{"shipping_option_id":%q}`, option))
		require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	}
	done := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		storefrontCompletionBody(t, additionTotal+2*soldDeliveryFee))
	require.Equal(t, http.StatusOK, done.Code, "body: %s", done.Body.String())
	orderID, _ := storefrontData(t, done)["order_id"].(string)
	require.NotEmpty(t, orderID)

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
	key := regexp.MustCompile(`name="key" value="(panel-[0-9a-f]+)"`).FindStringSubmatch(page)
	require.Len(t, key, 2, "the order page offers to open a parcel")
	choice := regexp.MustCompile(`<option value="([^"]+)">[^<]*\(` + regexp.QuoteMeta(freight) + `\)</option>`).
		FindStringSubmatch(page)
	require.Len(t, choice, 2, "the form names the freight delivery by its option")
	assert.Contains(t, page, "("+post+")</option>", "and the other delivery")

	opened := send(http.MethodPost, pagePath+"/parcels", url.Values{"key": {key[1]}, "delivery": {choice[1]}})
	require.Equal(t, http.StatusOK, opened.Code, opened.Body.String())
	require.Len(t, panelParcelOpened.FindStringSubmatch(opened.Body.String()), 2, "the page names the parcel it opened")
	handed, ok := carrierSpy.shipmentFor(key[1])
	require.True(t, ok, "the parcel reached the carrier under the form's key")
	assert.Equal(t, freight, handed.OptionID, "on the delivery chosen")

	unnamed := send(http.MethodPost, pagePath+"/parcels", url.Values{"key": {"panel-unnamed-" + orderID}})
	require.Equal(t, http.StatusUnprocessableEntity, unnamed.Code, unnamed.Body.String())
	assert.Contains(t, unnamed.Body.String(), "not sold exactly one to default to",
		"a form that names no delivery is refused on an order sold two")
}
