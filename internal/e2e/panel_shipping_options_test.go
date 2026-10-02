//go:build integration

package e2e

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
	fulfillmentsvc "github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// TestAnOperatorRevisesAShippingOptionInThePanel is ADR 0333 on the
// production wiring: the Shipping options screen lists an option through the
// fulfillment module's option entity with its fee in its currency's
// decimals; the row's form sets its fee and keeps it off the storefront
// through the registered `fulfillment.admin` surface; and the same form sent
// again, now stale, is refused on the list with the option as it is now.
func TestAnOperatorRevisesAShippingOptionInThePanel(t *testing.T) {
	ctx := t.Context()
	n := fixtureCounter.Add(1)
	option, err := shippingSvc.CreateShippingOption(ctx, fulfillmentsvc.CreateOptionInput{
		Name: fmt.Sprintf("E2E Courier %d", n), ProviderID: carrierSpyID,
		ShippingProfileID: newShippingProfile(ctx, t, "Panel"), Amount: 2500,
		CurrencyCode: taxedCurrency, RegionID: taxedRegionID,
	})
	require.NoError(t, err)
	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_warehouse", Kind: "user", Scopes: []string{"fulfillment:read", "fulfillment:write"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	marker := `action="` + adminui.ShippingOptionsPath + "/" + option.ID + "?page="
	// drawn is the form the option's row carries: its action and its fields.
	drawn := func(page string) (string, url.Values) {
		t.Helper()

		_, form, found := strings.Cut(page, marker)
		require.True(t, found, "the option's row offers its form")
		query, form, _ := strings.Cut(form, `"`)
		form, _, _ = strings.Cut(form, "</form>")
		values := url.Values{}
		for _, field := range []string{
			"read_name", "read_amount", "read_admin_only", "price_type", "currency_code", "name", "amount",
		} {
			_, value, ok := strings.Cut(form, `name="`+field+`" value="`)
			require.True(t, ok, field)
			value, _, _ = strings.Cut(value, `"`)
			values.Set(field, html.UnescapeString(value))
		}

		return adminui.ShippingOptionsPath + "/" + option.ID + "?page=" + html.UnescapeString(query), values
	}

	// The options come newest first; the option is on whichever page holds it.
	var page string
	for number := 1; number <= 100 && !strings.Contains(page, marker); number++ {
		page = send(http.MethodGet, adminui.ShippingOptionsPath+"?page="+strconv.Itoa(number), nil).Body.String()
	}
	action, form := drawn(page)
	assert.Equal(t, "2500", form.Get("read_amount"), "the fee in minor units")
	assert.Equal(t, "25.00", form.Get("amount"), "and in the currency's decimals")
	assert.Equal(t, "flat|"+taxedCurrency, form.Get("price_type")+"|"+form.Get("currency_code"))

	form.Set("amount", "19.90")
	form.Set("admin_only", "1")
	revised := send(http.MethodPost, action, form)
	require.Equal(t, http.StatusSeeOther, revised.Code, revised.Body.String())
	assert.Contains(t, send(http.MethodGet, revised.Header().Get("Location"), nil).Body.String(),
		"Shipping option "+option.Name+" was written.")
	stored, err := shippingSvc.GetShippingOption(ctx, option.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1990), stored.Amount, "the fee read in the currency's decimals")
	assert.True(t, stored.AdminOnly, "the option is off the storefront")
	assert.Equal(t, carrierSpyID, stored.ProviderID, "the provider is kept")

	stale := send(http.MethodPost, action, form)
	require.Equal(t, http.StatusUnprocessableEntity, stale.Code, stale.Body.String())
	assert.Contains(t, stale.Body.String(), "draw the list again")
	_, now := drawn(stale.Body.String())
	assert.Equal(t, "1990|true", now.Get("read_amount")+"|"+now.Get("read_admin_only"),
		"the row is drawn from the option as it is now")
}
