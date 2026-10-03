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
	taxsvc "github.com/bdrtr/gobit/internal/modules/tax/service"
)

// TestAnOperatorCorrectsATaxRateInThePanel is ADR 0378 on the production
// wiring: the Taxes screen draws a rate from the tax region entity and
// corrects its name and rate through the registered `tax.admin` surface,
// keeping its code and default; the same form sent again, now stale, is
// refused on the screen.
func TestAnOperatorCorrectsATaxRateInThePanel(t *testing.T) {
	ctx := t.Context()
	// ISO 3166-1 leaves the Q codes to their users, so no other fixture taxes
	// one, and the tax module checks the shape alone.
	country := fmt.Sprintf("Q%c", 'A'+rune(fixtureCounter.Add(1)%26))
	root, err := taxSvc.CreateTaxRegion(ctx, taxsvc.CreateTaxRegionInput{CountryCode: country})
	require.NoError(t, err)
	rate, err := taxSvc.CreateTaxRate(ctx, taxsvc.CreateTaxRateInput{
		TaxRegionID: root.ID, Name: "VAT", Code: "E2E20", RateBps: 2000, IsDefault: true,
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
			ID: "usr_taxes", Kind: "user", Scopes: []string{"tax:read", "tax:write"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	// The tax regions are listed by country, so the pages are walked until
	// the rate's form is found.
	var form string
	found := false
	for page := 1; page <= 50 && !found; page++ {
		body := send(http.MethodGet, fmt.Sprintf("%s?page=%d", adminui.TaxesPath, page), nil).Body.String()
		_, form, found = strings.Cut(body, fmt.Sprintf(`action="%s/rates/%s?page=%d"`, adminui.TaxesPath, rate.ID, page))
		if !strings.Contains(body, ">Next</a>") {
			break
		}
	}
	require.True(t, found, "the rate offers the correction")
	form, _, _ = strings.Cut(form, "</form>")
	assert.Contains(t, form, `name="read_rate_bps" value="2000"`)
	assert.Contains(t, form, `name="rate" value="20"`)

	correction := url.Values{
		"read_name": {"VAT"}, "read_rate_bps": {"2000"}, "name": {"Standard VAT"}, "rate": {"18.5"},
	}
	written := send(http.MethodPost, adminui.TaxesPath+"/rates/"+rate.ID, correction)
	require.Equal(t, http.StatusSeeOther, written.Code, written.Body.String())
	stored, err := taxSvc.GetTaxRate(ctx, rate.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.Code)
	assert.Equal(t, "Standard VAT|1850|E2E20|true",
		fmt.Sprintf("%s|%d|%s|%t", stored.Name, stored.RateBps, *stored.Code, stored.IsDefault))

	stale := send(http.MethodPost, adminui.TaxesPath+"/rates/"+rate.ID, correction)
	require.Equal(t, http.StatusUnprocessableEntity, stale.Code, stale.Body.String())
	assert.Contains(t, stale.Body.String(), "draw the list again")
}
