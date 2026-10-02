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
	regionsvc "github.com/bdrtr/gobit/internal/modules/region/service"
)

// TestAnOperatorCorrectsARegionInThePanel is ADR 0362 on the production
// wiring: the Regions screen draws a region's row from the region entity and
// corrects its name, taxes and rate through the registered `region.admin`
// surface, keeping its currency; the same form sent again, now stale, is
// refused on the screen.
func TestAnOperatorCorrectsARegionInThePanel(t *testing.T) {
	ctx := t.Context()
	name := fmt.Sprintf("E2E Panel Region %d", fixtureCounter.Add(1))
	region, err := regionSvc.CreateRegion(ctx, regionsvc.CreateRegionInput{
		Name: name, CurrencyCode: untaxedCurrency, AutomaticTaxes: true, TaxRate: 2000,
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
			ID: "usr_regions", Kind: "user", Scopes: []string{"region:read", "region:write"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	// The regions are listed oldest first, so the new one is on the last
	// page; the pages are walked until its row is found.
	var form string
	found := false
	for page := 1; page <= 50 && !found; page++ {
		body := send(http.MethodGet, fmt.Sprintf("%s?page=%d", adminui.RegionsPath, page), nil).Body.String()
		_, form, found = strings.Cut(body, fmt.Sprintf(`action="%s/%s?page=%d"`, adminui.RegionsPath, region.ID, page))
		if !strings.Contains(body, ">Next</a>") {
			break
		}
	}
	require.True(t, found, "the region's row offers the correction")
	form, _, _ = strings.Cut(form, "</form>")
	assert.Contains(t, form, `name="read_tax_rate" value="2000"`)
	assert.Contains(t, form, `name="tax_rate" value="20"`)

	correction := url.Values{
		"read_name": {name}, "read_automatic_taxes": {"true"}, "read_tax_rate": {"2000"},
		"name": {name + " revised"}, "tax_rate": {"7.5"},
	}
	written := send(http.MethodPost, adminui.RegionsPath+"/"+region.ID, correction)
	require.Equal(t, http.StatusSeeOther, written.Code, written.Body.String())
	stored, err := regionSvc.GetRegion(ctx, region.ID)
	require.NoError(t, err)
	assert.Equal(t, name+" revised|false|750|"+untaxedCurrency,
		fmt.Sprintf("%s|%t|%d|%s", stored.Name, stored.AutomaticTaxes, stored.TaxRate, stored.CurrencyCode))

	stale := send(http.MethodPost, adminui.RegionsPath+"/"+region.ID, correction)
	require.Equal(t, http.StatusUnprocessableEntity, stale.Code, stale.Body.String())
	assert.Contains(t, stale.Body.String(), "draw the list again")
}
