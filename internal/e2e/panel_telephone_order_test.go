//go:build integration

package e2e

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
)

// TestAnOperatorOpensATelephoneOrderInThePanel is ADR 0290 on the production
// wiring: the panel built from this harness's container opens a cart for the
// taxed country through the cart module's surface, adds a variant the catalog
// prices in the key's channel, and its page reads the line and the totals
// through the real read layer. A variant the catalog does not hold is refused
// on the page, which keeps what was typed.
func TestAnOperatorOpensATelephoneOrderInThePanel(t *testing.T) {
	ctx := t.Context()
	const title = "E2E Panel Telephone Order"
	variantID, _ := newStockedVariant(ctx, t, title, map[string]int64{taxedCurrency: adminCartUnitPrice}, adminCartStock)

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_phone", Kind: "user", Scopes: []string{"cart:read", "cart:write", "product:read"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	opened := send(http.MethodPost, adminui.CartsPath, url.Values{
		"country_code": {strings.ToLower(taxedCountry)}, "email": {"caller@example.com"},
	})
	require.Equal(t, http.StatusSeeOther, opened.Code, opened.Body.String())
	cartPath := opened.Header().Get("Location")
	require.True(t, strings.HasPrefix(cartPath, adminui.CartsPath+"/"), cartPath)

	// The cart names the operator who opened it, and the telephone order's page
	// lists it among the open ones through the real read layer (ADR 0296).
	listed := send(http.MethodGet, adminui.CartsPath, nil)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	row := regexp.MustCompile(`(?s)<a href="` + regexp.QuoteMeta(cartPath) + `">.*?</tr>`).FindString(listed.Body.String())
	require.NotEmpty(t, row, "the open cart is listed")
	assert.Contains(t, row, "<td>usr_phone</td>")
	assert.Contains(t, row, "caller@example.com")

	// The operator finds the product by its title and the form offers its
	// variant (ADR 0293).
	found := send(http.MethodGet, cartPath+"?find="+url.QueryEscape("Panel Telephone Order"), nil)
	require.Equal(t, http.StatusOK, found.Code, found.Body.String())
	assert.Contains(t, found.Body.String(), `<option value="`+variantID+`">`)

	added := send(http.MethodPost, cartPath+"/lines", url.Values{
		"sales_channel_id": {testChannelID}, "variant_id": {variantID},
		"quantity": {"2"},
	})
	require.Equal(t, http.StatusSeeOther, added.Code, added.Body.String())

	page := send(http.MethodGet, cartPath, nil)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	body := page.Body.String()
	assert.Contains(t, body, title, "the line's title is the catalog's")
	assert.Contains(t, body, variantID)
	assert.Contains(t, body, "caller@example.com")
	assert.Contains(t, body, taxedCurrency, "the currency is the country's region's")
	// Two units at 250.00 in the taxed region (20%), computed by hand as the
	// admin cart test computes them.
	assert.Contains(t, body, "250.00", "the unit price is the catalog's")
	assert.Contains(t, body, "500.00 TRY")
	assert.Contains(t, body, "100.00 TRY", "the tax is the region's rate")
	assert.Contains(t, body, "<strong>600.00 TRY</strong>")

	refused := send(http.MethodPost, cartPath+"/lines", url.Values{
		"sales_channel_id": {testChannelID}, "variant_id": {"variant_nobody_sells"}, "quantity": {"1"},
	})
	assert.Equal(t, http.StatusUnprocessableEntity, refused.Code, refused.Body.String())
	assert.Contains(t, refused.Body.String(), `<p role="alert">`)
	assert.Contains(t, refused.Body.String(), `value="variant_nobody_sells"`, "the form keeps what was typed")
	assert.Contains(t, refused.Body.String(), title, "the cart is drawn again with its line")
}

// TestAnOperatorCompletesATelephoneOrderInThePanel is ADR 0291 on the
// production wiring: in the panel built from this harness's container, an
// operator writes the caller's address, chooses the shipping option and
// completes the cart with a bank transfer against the total the page shows.
// A method whose money moves at the checkout is refused on the page; the
// transfer places the order owing its total and sends the operator to it.
func TestAnOperatorCompletesATelephoneOrderInThePanel(t *testing.T) {
	ctx := t.Context()
	variantID, stockItemID := newStockedVariant(ctx, t, "E2E Panel Telephone Completion",
		map[string]int64{taxedCurrency: adminCartUnitPrice}, adminCartStock)
	optionID := newShippingOption(ctx, t, newShippingProfile(ctx, t, "Panel telephone profile"),
		"Panel telephone delivery", 4_900, false)

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_phone", Kind: "user", Scopes: []string{"cart:read", "cart:write", "order:read"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	redirected := func(rec *httptest.ResponseRecorder) string {
		t.Helper()

		require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
		return rec.Header().Get("Location")
	}

	cartPath := redirected(send(http.MethodPost, adminui.CartsPath, url.Values{
		"country_code": {taxedCountry}, "email": {"caller@example.com"},
	}))
	redirected(send(http.MethodPost, cartPath+"/lines", url.Values{
		"sales_channel_id": {testChannelID}, "variant_id": {variantID}, "quantity": {"2"},
	}))
	assert.Equal(t, cartPath, redirected(send(http.MethodPost, cartPath+"/address", url.Values{
		"first_name": {"Tele"}, "last_name": {"Phone"}, "address_1": {"Street 1"},
		"city": {"City"}, "postal_code": {"00000"}, "country_code": {taxedCountry},
	})))
	choosing := send(http.MethodGet, cartPath, nil)
	require.Equal(t, http.StatusOK, choosing.Code, choosing.Body.String())
	assert.Contains(t, choosing.Body.String(), `<option value="`+optionID+`">`,
		"the operator chooses from the options the cart can take (ADR 0292)")
	redirected(send(http.MethodPost, cartPath+"/shipping", url.Values{"shipping_option_id": {optionID}}))

	page := send(http.MethodGet, cartPath, nil)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), "Street 1<br>", "the page reads the address the form wrote")
	assert.Contains(t, page.Body.String(), "Panel telephone delivery")
	match := regexp.MustCompile(`name="read_total" value="(\d+)"`).FindStringSubmatch(page.Body.String())
	require.Len(t, match, 2, "the completion carries the total the page shows")
	total, err := strconv.ParseInt(match[1], 10, 64)
	require.NoError(t, err)
	require.Greater(t, total, adminCartTotal, "the shipping is in the total")

	byCard := send(http.MethodPost, cartPath+"/complete", url.Values{
		"sales_channel_id": {testChannelID}, "payment_provider_id": {"manual"}, "read_total": {match[1]},
	})
	assert.Equal(t, http.StatusUnprocessableEntity, byCard.Code, byCard.Body.String())
	assert.Contains(t, byCard.Body.String(), `<p role="alert">`)

	orderPath := redirected(send(http.MethodPost, cartPath+"/complete", url.Values{
		"sales_channel_id": {testChannelID}, "payment_provider_id": {offlineMethod}, "read_total": {match[1]},
	}))
	require.True(t, strings.HasPrefix(orderPath, adminui.OrdersPath+"/"), orderPath)

	order, err := orderSvc.GetOrder(ctx, strings.TrimPrefix(orderPath, adminui.OrdersPath+"/"))
	require.NoError(t, err)
	assert.Equal(t, total, order.Total, "the order is placed for the total the operator read")
	assert.Zero(t, order.Summary.PaidTotal, "it owes its total")
	assert.Equal(t, "usr_phone", order.PlacedBy, "the order names the operator who placed it (ADR 0298)")
	orderPage := send(http.MethodGet, orderPath, nil)
	require.Equal(t, http.StatusOK, orderPage.Code, orderPage.Body.String())
	assert.Contains(t, orderPage.Body.String(), "by operator usr_phone", "the order page reads it")
	operators := send(http.MethodGet, adminui.OrdersPath+"?placed=1", nil)
	require.Equal(t, http.StatusOK, operators.Code, operators.Body.String())
	assert.Contains(t, operators.Body.String(), `href="`+orderPath+`"`, "the operator's orders list it")
	assert.Equal(t, adminCartStock-adminCartQuantity, sellableQuantity(ctx, t, stockItemID),
		"the order's stock is deducted")

	listed := send(http.MethodGet, adminui.CartsPath, nil)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	assert.Contains(t, listed.Body.String(), "<h2>Open carts</h2>")
	assert.NotContains(t, listed.Body.String(), `href="`+cartPath+`"`,
		"a completed cart is no longer open (ADR 0296)")
}

// TestAnOperatorFindsTheCallerInThePanel is ADR 0297 on the production wiring:
// the panel finds a customer by the e-mail the caller spells, in any case,
// through the real customer provider, and the cart it opens is that customer's.
func TestAnOperatorFindsTheCallerInThePanel(t *testing.T) {
	ctx := t.Context()
	customerID, email := newCustomer(ctx, t)
	// A second customer, made after the first, is one an unfiltered read
	// would offer as well.
	otherID, _ := newCustomer(ctx, t)

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_phone", Kind: "user", Scopes: []string{"cart:read", "cart:write", "customer:read"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	found := send(http.MethodGet, adminui.CartsPath+"?caller="+url.QueryEscape(strings.ToUpper(email)), nil)
	require.Equal(t, http.StatusOK, found.Code, found.Body.String())
	assert.Contains(t, found.Body.String(), `<option value="`+customerID+`"`,
		"the customer provider normalizes the address the operator typed")
	assert.NotContains(t, found.Body.String(), otherID, "only the records holding the e-mail are offered")

	opened := send(http.MethodPost, adminui.CartsPath, url.Values{
		"country_code": {taxedCountry}, "email": {email}, "customer_id": {customerID},
	})
	require.Equal(t, http.StatusSeeOther, opened.Code, opened.Body.String())
	page := send(http.MethodGet, opened.Header().Get("Location"), nil)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), "customer "+customerID, "the cart is the found customer's")
}

// TestAnOperatorCorrectsATelephoneCartInThePanel is ADR 0300 on the production
// wiring: the operator removes a line from the cart they opened, the page
// reads the cart without it, and the discarded cart leaves the open carts and
// has no page.
func TestAnOperatorCorrectsATelephoneCartInThePanel(t *testing.T) {
	ctx := t.Context()
	keep, _ := newStockedVariant(ctx, t, "E2E Panel Kept Line", map[string]int64{taxedCurrency: adminCartUnitPrice}, adminCartStock)
	drop, _ := newStockedVariant(ctx, t, "E2E Panel Dropped Line", map[string]int64{taxedCurrency: adminCartUnitPrice}, adminCartStock)

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_phone", Kind: "user", Scopes: []string{"cart:read", "cart:write"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	opened := send(http.MethodPost, adminui.CartsPath, url.Values{
		"country_code": {taxedCountry}, "email": {"caller@example.com"},
	})
	require.Equal(t, http.StatusSeeOther, opened.Code, opened.Body.String())
	cartPath := opened.Header().Get("Location")
	for _, variantID := range []string{keep, drop} {
		added := send(http.MethodPost, cartPath+"/lines", url.Values{
			"sales_channel_id": {testChannelID}, "variant_id": {variantID}, "quantity": {"1"},
		})
		require.Equal(t, http.StatusSeeOther, added.Code, added.Body.String())
	}

	cart, err := cartSvc.GetCart(ctx, strings.TrimPrefix(cartPath, adminui.CartsPath+"/"))
	require.NoError(t, err)
	var dropLine string
	for _, line := range cart.Items {
		if line.VariantID == drop {
			dropLine = line.ID
		}
	}
	require.NotEmpty(t, dropLine)

	removed := send(http.MethodPost, cartPath+"/lines/"+dropLine+"/remove", url.Values{})
	require.Equal(t, http.StatusSeeOther, removed.Code, removed.Body.String())
	page := send(http.MethodGet, cartPath, nil)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), "E2E Panel Kept Line")
	assert.NotContains(t, page.Body.String(), "E2E Panel Dropped Line", "the removed line is gone")
	repriced, err := cartSvc.GetCart(ctx, strings.TrimPrefix(cartPath, adminui.CartsPath+"/"))
	require.NoError(t, err)
	assert.False(t, repriced.TotalsStale(), "the removal repriced the cart")
	assert.Equal(t, adminCartUnitPrice, repriced.Subtotal, "the total is the kept line's alone")

	discarded := send(http.MethodPost, cartPath+"/discard", url.Values{})
	require.Equal(t, http.StatusSeeOther, discarded.Code, discarded.Body.String())
	assert.Equal(t, adminui.CartsPath, discarded.Header().Get("Location"))
	listed := send(http.MethodGet, adminui.CartsPath, nil)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	assert.NotContains(t, listed.Body.String(), `href="`+cartPath+`"`, "a discarded cart is not open")
	gone := send(http.MethodGet, cartPath, nil)
	assert.Equal(t, http.StatusNotFound, gone.Code, gone.Body.String())
}
