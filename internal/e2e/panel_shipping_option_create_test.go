//go:build integration

package e2e

import (
	"fmt"
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
	fulfillmentsvc "github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// TestAnOperatorWritesAShippingOptionInThePanel is ADR 0334 on the
// production wiring: the Shipping options form offers the providers this
// installation registers, a profile just written and the shop's regions with
// their currencies, through the registered `fulfillment.admin` surface and
// the read layer; the option it writes is offered in the chosen region in
// that region's currency, its fee read in the currency's decimals, and the
// list names it.
func TestAnOperatorWritesAShippingOptionInThePanel(t *testing.T) {
	ctx := t.Context()
	profileID := newShippingProfile(ctx, t, "Panel written")
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

	page := send(http.MethodGet, adminui.ShippingOptionsPath, nil).Body.String()
	_, form, found := strings.Cut(page, "<summary>New shipping option</summary>")
	require.True(t, found, "a writer is offered the form")
	for _, want := range []string{
		`<option value="` + carrierSpyID + `">`, `<option value="` + profileID + `">`,
		`<option value="` + taxedRegionID + `">`, "(" + taxedCurrency + ")</option>",
	} {
		assert.Contains(t, form, want)
	}

	name := fmt.Sprintf("E2E Panel Courier %d", fixtureCounter.Add(1))
	written := send(http.MethodPost, adminui.ShippingOptionsPath, url.Values{
		"name": {name}, "provider_id": {carrierSpyID}, "shipping_profile_id": {profileID},
		"price_type": {"flat"}, "amount": {"12.50"}, "region_id": {taxedRegionID}, "currency_code": {"XXX"},
		"admin_only": {"1"},
	})
	require.Equal(t, http.StatusSeeOther, written.Code, written.Body.String())
	assert.Contains(t, send(http.MethodGet, written.Header().Get("Location"), nil).Body.String(),
		"Shipping option "+name+" was written.")

	options, _, err := shippingSvc.ListShippingOptions(ctx, fulfillmentsvc.ListOptionsAdminInput{ProfileID: &profileID})
	require.NoError(t, err)
	require.Len(t, options, 1, "the profile just written holds the option written")
	option := options[0]
	assert.Equal(t, fmt.Sprintf("%s|%s|%d|%s|%s|%t", name, carrierSpyID, 1250, taxedCurrency, taxedRegionID, true),
		fmt.Sprintf("%s|%s|%d|%s|%s|%t", option.Name, option.ProviderID, option.Amount, option.CurrencyCode,
			option.RegionID, option.AdminOnly),
		"in the region's currency, whatever was typed, its fee in the currency's decimals")
}
