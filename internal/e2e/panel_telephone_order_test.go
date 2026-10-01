//go:build integration

package e2e

import (
	"net/http"
	"net/http/httptest"
	"net/url"
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
			ID: "usr_phone", Kind: "user", Scopes: []string{"cart:read", "cart:write"},
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
