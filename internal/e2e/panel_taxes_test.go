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

// TestAnOperatorReadsTheTaxesInThePanel is ADR 0355 on the production
// wiring: the Taxes screen reads a country's tax region through the tax
// module's tax region entity with the default rate it charges.
func TestAnOperatorReadsTheTaxesInThePanel(t *testing.T) {
	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)

	req := httptest.NewRequest(http.MethodGet, adminui.TaxesPath, http.NoBody)
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
		ID: "usr_taxes", Kind: "user", Scopes: []string{"tax:read"},
	}))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	_, row, found := strings.Cut(rec.Body.String(), "<td>"+secondTaxCountry+"<br>")
	require.True(t, found, "the second tax country's region is listed")
	row, _, _ = strings.Cut(row, "</tr>")
	assert.Contains(t, row, "the whole country")
	assert.Contains(t, row, fmt.Sprintf(`E2E TVA %d%% <span class="pill">default</span>`, secondTaxRateBps/100))
}
