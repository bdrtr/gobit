//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
)

// TestAnOperatorReadsTheRegionsInThePanel is ADR 0354 on the production
// wiring: the Regions screen reads a region through the region module's
// region entity with its currency, tax rate, whether taxes are computed and
// the countries it covers.
func TestAnOperatorReadsTheRegionsInThePanel(t *testing.T) {
	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)

	req := httptest.NewRequest(http.MethodGet, adminui.RegionsPath, http.NoBody)
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
		ID: "usr_regions", Kind: "user", Scopes: []string{"region:read"},
	}))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	_, row, found := strings.Cut(rec.Body.String(), `<span class="muted">`+multiCountryRegionID+`</span>`)
	require.True(t, found, "the multi-country region is listed")
	row, _, _ = strings.Cut(row, "</tr>")
	assert.Contains(t, row, "<td>"+untaxedCurrency+"</td>")
	assert.Contains(t, row, fmt.Sprintf("<td>%d%%</td>", multiCountryRateBps/100))
	assert.Contains(t, row, "<td>yes</td>")
	for _, country := range multiCountryCountries {
		assert.Contains(t, row, country+" ", "the countries it covers, by code and name")
	}
}
