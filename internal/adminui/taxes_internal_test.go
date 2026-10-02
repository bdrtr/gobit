package adminui

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
)

// taxesPanel is a panel over the tax regions given.
func taxesPanel(t *testing.T, regions ...query.Record) (*UI, *fakeCatalog) {
	t.Helper()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{EntityTaxRegion: regions}}
	panel := newCatalogPanel(t, catalog)
	panel.scopes = builtInScopes()

	return panel, catalog
}

// TestTheTaxesScreenListsTheTaxRegions is ADR 0355: each tax region is
// listed with its country, its province or the whole country, the rates it
// charges as percents with their codes and the default marked, or none, and
// its provider or that it inherits one; the screen reads the tax region
// entity a page at a time, is in the menu and asks to read the taxes.
func TestTheTaxesScreenListsTheTaxRegions(t *testing.T) {
	t.Parallel()

	panel, catalog := taxesPanel(t,
		query.Record{fieldID: "taxreg_tr", fieldTaxCountry: "TR", fieldTaxProvince: nil, fieldTaxProvider: "",
			fieldTaxRates: []map[string]any{
				{"name": "VAT", "code": "tr_vat", "rate_bps": int32(2000), "is_default": true},
				{"name": "Reduced", "code": "", "rate_bps": int32(1000), "is_default": false},
			}},
		query.Record{fieldID: "taxreg_34", fieldTaxCountry: "TR", fieldTaxProvince: "TR-34", fieldTaxProvider: "tax_ext",
			fieldTaxRates: []map[string]any{}},
	)

	rec := campaignsRequest(panel, http.MethodGet, TaxesPath, nil, scopeTaxRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	row := func(id string) string {
		_, after, found := strings.Cut(body, `<span class="muted">`+id+`</span>`)
		require.True(t, found, id)
		after, _, _ = strings.Cut(after, "</tr>")
		return after
	}
	country, province := row("taxreg_tr"), row("taxreg_34")
	assert.Contains(t, body, `<td>TR<br><span class="muted">taxreg_tr</span>`)
	assert.Contains(t, country, `<span class="muted">the whole country</span>`)
	assert.Contains(t, country, `VAT 20% <span class="muted">tr_vat</span> <span class="pill">default</span><br>Reduced 10%</td>`)
	assert.Contains(t, country, `<span class="muted">inherited</span>`)
	assert.Contains(t, province, "<td>TR-34</td>")
	assert.Contains(t, province, `<span class="muted">none</span>`)
	assert.Contains(t, province, "<td>tax_ext</td>")
	assert.Contains(t, body, `href="`+TaxesPath+`"`, "the screen is in the menu")
	assert.NotContains(t, body, ">Next</a>", "two regions are one page")
	spec := catalog.specs[len(catalog.specs)-1]
	assert.Equal(t, EntityTaxRegion, spec.Entity)
	assert.Equal(t, []int{taxRegionsPerPage + 1, 0}, []int{spec.Limit, spec.Offset})

	rec = campaignsRequest(panel, http.MethodGet, TaxesPath, nil, scopeRegionRead)
	assert.Equal(t, http.StatusForbidden, rec.Code, "an operator who may not read the taxes")

	many := make([]query.Record, 0, taxRegionsPerPage+1)
	for i := range taxRegionsPerPage + 1 {
		many = append(many, query.Record{fieldID: fmt.Sprintf("taxreg_%02d", i), fieldTaxCountry: "TR"})
	}
	panel, catalog = taxesPanel(t, many...)
	rec = campaignsRequest(panel, http.MethodGet, TaxesPath+"?page=2", nil, scopeTaxRead)
	assert.Equal(t, taxRegionsPerPage, catalog.specs[len(catalog.specs)-1].Offset)
	assert.Contains(t, rec.Body.String(), `href="`+TaxesPath+`?page=1">Previous</a>`)
	rec = campaignsRequest(panel, http.MethodGet, TaxesPath, nil, scopeTaxRead)
	assert.Contains(t, rec.Body.String(), `href="`+TaxesPath+`?page=2">Next</a>`)
	assert.NotContains(t, rec.Body.String(), fmt.Sprintf("taxreg_%02d", taxRegionsPerPage), "a page holds its size")
}
