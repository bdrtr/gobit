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

// panelParcelOpened reads the parcel the open form reports.
var panelParcelOpened = regexp.MustCompile(`Parcel (ful_[0-9A-Z]+) was opened\.`)

// TestAnOperatorShipsAnOrderInThePanel is ADR 0324 on the production wiring:
// an order sold one delivery is opened a parcel from its page through the
// order module's surface and the fulfilling flow, on that delivery; the same
// form sent again opens nothing new; the parcel is marked shipped with its
// tracking and then delivered through the fulfillment module's surface, and a
// delivered parcel offers no move.
func TestAnOperatorShipsAnOrderInThePanel(t *testing.T) {
	ctx := t.Context()
	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Panel Shipped", map[string]int64{
		taxedCurrency: additionUnitPrice,
	}, additionStock)
	optionID := spyOptionPriced(t, soldDeliveryFee, false)

	f := additionFixture{customerID: customerID, email: email, variantID: variantID}
	cartID := f.openCart(t, "")
	address := fmt.Sprintf(`{"first_name":"Ada","address_1":"12 Main St","city":"Springfield",`+
		`"postal_code":"62701","country_code":%q}`, taxedCountry)
	rec := storefrontRequest(t, http.MethodPut, "/store/v1/carts/"+cartID+"/shipping-address", address)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	rec = storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/shipping-methods",
		fmt.Sprintf(`{"shipping_option_id":%q}`, optionID))
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	done := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		storefrontCompletionBody(t, additionTotal+soldDeliveryFee))
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
			ID: "usr_warehouse", Kind: "user",
			Scopes: []string{"order:read", "order:write", "fulfillment:read", "fulfillment:write"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	pagePath := adminui.OrdersPath + "/" + orderID
	page := send(http.MethodGet, pagePath, nil).Body.String()
	key := regexp.MustCompile(`name="key" value="(panel-[0-9a-f]+)"`).FindStringSubmatch(page)
	require.Len(t, key, 2, "the order page offers to open a parcel")

	opened := send(http.MethodPost, pagePath+"/parcels", url.Values{"key": {key[1]}})
	require.Equal(t, http.StatusOK, opened.Code, opened.Body.String())
	match := panelParcelOpened.FindStringSubmatch(opened.Body.String())
	require.Len(t, match, 2, "the page names the parcel it opened")
	parcel := match[1]
	handed, ok := carrierSpy.shipmentFor(key[1])
	require.True(t, ok, "the parcel reached the carrier under the form's key")
	assert.Equal(t, optionID, handed.OptionID, "on the delivery the order was sold")

	again := send(http.MethodPost, pagePath+"/parcels", url.Values{"key": {key[1]}})
	assert.Contains(t, again.Body.String(), "This form had already opened parcel "+parcel+"; nothing new was opened.")

	shipped := send(http.MethodPost, pagePath+"/parcels/"+parcel+"/ship",
		url.Values{"tracking_number": {"E2E-TK-1"}, "tracking_url": {"https://carrier.example/E2E-TK-1"}})
	require.Equal(t, http.StatusOK, shipped.Code, shipped.Body.String())
	assert.Contains(t, shipped.Body.String(), "Parcel "+parcel+" is on its way.")
	assert.Contains(t, shipped.Body.String(), `<a href="https://carrier.example/E2E-TK-1" rel="noopener noreferrer">E2E-TK-1</a>`,
		"the parcel carries its tracking")

	// The Parcels screen lists it among the shipped, its order named (ADR
	// 0356).
	listed := send(http.MethodGet, adminui.ParcelsPath+"?status=shipped", nil).Body.String()
	_, row, found := strings.Cut(listed, "<td>"+parcel+"</td>")
	require.True(t, found, "the shipped parcel is on the Parcels screen")
	row, _, _ = strings.Cut(row, "</tr>")
	assert.Contains(t, row, `<a href="`+pagePath+`">#`, "with its order")
	assert.Contains(t, row, "E2E-TK-1</a>")
	assert.NotContains(t, send(http.MethodGet, adminui.ParcelsPath, nil).Body.String(), "<td>"+parcel+"</td>",
		"and not among those still to be shipped")

	delivered := send(http.MethodPost, pagePath+"/parcels/"+parcel+"/deliver", url.Values{})
	require.Equal(t, http.StatusOK, delivered.Code, delivered.Body.String())
	assert.Contains(t, delivered.Body.String(), "Parcel "+parcel+" was delivered.")
	assert.NotContains(t, delivered.Body.String(), "/parcels/"+parcel+"/", "a delivered parcel offers no move")

	refused := send(http.MethodPost, pagePath+"/parcels/"+parcel+"/cancel", url.Values{})
	require.Equal(t, http.StatusUnprocessableEntity, refused.Code, refused.Body.String())
}
