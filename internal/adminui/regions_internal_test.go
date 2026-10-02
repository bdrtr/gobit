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

// regionsPanel is a panel over the regions given.
func regionsPanel(t *testing.T, regions ...query.Record) (*UI, *fakeCatalog) {
	t.Helper()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{EntityRegion: regions}}
	panel := newCatalogPanel(t, catalog)
	panel.scopes = builtInScopes()

	return panel, catalog
}

// TestTheRegionsScreenListsTheRegions is ADR 0354: each region is listed
// with its currency, its tax rate as a percent, whether taxes are computed
// for it and the countries it covers, or none; the screen reads the region
// entity a page at a time, is in the menu and asks to read the regions.
func TestTheRegionsScreenListsTheRegions(t *testing.T) {
	t.Parallel()

	panel, catalog := regionsPanel(t,
		query.Record{fieldID: "reg_tr", fieldName: "Turkey", fieldCurrencyCod: "try", fieldRegionTaxRate: int32(2000),
			fieldRegionAutoTaxes: true, fieldRegionCountries: []map[string]any{{"code": "tr", "name": "Turkey"}}},
		query.Record{fieldID: "reg_eu", fieldName: "Europe", fieldCurrencyCod: "eur", fieldRegionTaxRate: int32(1850),
			fieldRegionAutoTaxes: false, fieldRegionCountries: []map[string]any{
				{"code": "de", "name": "Germany"}, {"code": "fr", "name": "France"},
			}},
		query.Record{fieldID: "reg_x", fieldName: "Nowhere", fieldCurrencyCod: "usd", fieldRegionTaxRate: int32(0),
			fieldRegionCountries: []map[string]any{}},
	)

	rec := campaignsRequest(panel, http.MethodGet, RegionsPath, nil, scopeRegionRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	row := func(id string) string {
		_, after, found := strings.Cut(body, `<span class="muted">`+id+`</span>`)
		require.True(t, found, id)
		after, _, _ = strings.Cut(after, "</tr>")
		return after
	}
	tr, eu, nowhere := row("reg_tr"), row("reg_eu"), row("reg_x")
	assert.Contains(t, tr, "<td>TRY</td>")
	assert.Contains(t, tr, "<td>20%</td>")
	assert.Contains(t, tr, "<td>yes</td>")
	assert.Contains(t, tr, "<td>TR Turkey</td>")
	assert.Contains(t, eu, "<td>18.5%</td>")
	assert.Contains(t, eu, "<td>no</td>")
	assert.Contains(t, eu, "<td>DE Germany, FR France</td>")
	assert.Contains(t, nowhere, "<td>0%</td>")
	assert.Contains(t, nowhere, `<span class="muted">none</span>`)
	assert.Contains(t, body, `href="`+RegionsPath+`"`, "the screen is in the menu")
	assert.NotContains(t, body, ">Next</a>", "three regions are one page")
	require.NotEmpty(t, catalog.specs)
	spec := catalog.specs[len(catalog.specs)-1]
	assert.Equal(t, EntityRegion, spec.Entity)
	assert.Equal(t, []int{regionsPerPage + 1, 0}, []int{spec.Limit, spec.Offset}, "one more than a page, to know there is another")

	rec = campaignsRequest(panel, http.MethodGet, RegionsPath, nil, scopeOrderRead)
	assert.Equal(t, http.StatusForbidden, rec.Code, "an operator who may not read the regions")

	many := make([]query.Record, 0, regionsPerPage+1)
	for i := range regionsPerPage + 1 {
		many = append(many, query.Record{fieldID: fmt.Sprintf("reg_%02d", i), fieldName: "R", fieldCurrencyCod: "try"})
	}
	panel, catalog = regionsPanel(t, many...)
	rec = campaignsRequest(panel, http.MethodGet, RegionsPath+"?page=2", nil, scopeRegionRead)
	assert.Equal(t, regionsPerPage, catalog.specs[len(catalog.specs)-1].Offset, "the second page starts a page in")
	assert.Contains(t, rec.Body.String(), `href="`+RegionsPath+`?page=1">Previous</a>`)
	rec = campaignsRequest(panel, http.MethodGet, RegionsPath, nil, scopeRegionRead)
	assert.Contains(t, rec.Body.String(), `href="`+RegionsPath+`?page=2">Next</a>`)
	assert.NotContains(t, rec.Body.String(), fmt.Sprintf("reg_%02d", regionsPerPage), "a page holds its size")
}
