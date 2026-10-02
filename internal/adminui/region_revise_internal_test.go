package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// fakeRegionReviser corrects regions, recording each correction.
type fakeRegionReviser struct {
	corrected []string
	err       error
}

func (f *fakeRegionReviser) ReviseRegion(_ context.Context, id string, read, next json.RawMessage) error {
	f.corrected = append(f.corrected, id+"|"+string(read)+"|"+string(next))
	return f.err
}

// revisableRegions is a panel over two regions whose region surface is the
// one given.
func revisableRegions(t *testing.T, reviser RegionReviser) *UI {
	t.Helper()

	panel, _ := regionsPanel(t,
		query.Record{fieldID: "reg_tr", fieldName: "Turkey", fieldCurrencyCod: "TRY", fieldRegionTaxRate: int32(2000),
			fieldRegionAutoTaxes: true},
		query.Record{fieldID: "reg_eu", fieldName: "Europe", fieldCurrencyCod: "EUR", fieldRegionTaxRate: int32(1850),
			fieldRegionAutoTaxes: false},
	)
	panel.regions = reviser

	return panel
}

// TestARegionIsCorrectedOnTheRegionsScreen is ADR 0362: a writer whose
// surface can correct is offered each row's form drawn from its name, taxes
// and rate as a percent, carrying them as read, the rate in basis points; the
// surface is asked to correct the region from those, the name trimmed and the
// rate typed as a percent, and the page it was sent from says so; a refusal
// comes back with what was typed in that row alone, its box as typed; terms
// read that cannot be read reach no surface; a reader is offered nothing.
func TestARegionIsCorrectedOnTheRegionsScreen(t *testing.T) {
	t.Parallel()

	reviser := &fakeRegionReviser{}
	panel := revisableRegions(t, reviser)
	writer := []string{scopeRegionRead, scopeRegionWrite}

	rec := campaignsRequest(panel, http.MethodGet, RegionsPath, nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	turkey := rowHolding(t, rec.Body.String(), `action="`+RegionsPath+`/reg_tr?page=1"`)
	for _, want := range []string{
		`name="read_name" value="Turkey"`, `name="read_automatic_taxes" value="true"`, `name="read_tax_rate" value="2000"`,
		`name="name" value="Turkey"`, `name="tax_rate" value="20"`, `value="1" checked> taxes computed`,
	} {
		assert.Contains(t, turkey, want)
	}
	europe := rowHolding(t, rec.Body.String(), `action="`+RegionsPath+`/reg_eu?page=1"`)
	assert.Contains(t, europe, `name="tax_rate" value="18.5"`)
	assert.Contains(t, europe, `name="read_automatic_taxes" value="false"`)
	assert.NotContains(t, europe, " checked>")
	assert.NotContains(t, rec.Body.String(), "<details open>", "nothing refused, nothing open")
	rec = campaignsRequest(panel, http.MethodGet, RegionsPath, nil, scopeRegionRead)
	assert.NotContains(t, rec.Body.String(), `action="`+RegionsPath+`/`, "a reader corrects nothing")

	rec = campaignsRequest(panel, http.MethodPost, RegionsPath+"/reg_tr", url.Values{
		formRegionReadName: {"Turkey"}, formRegionReadAutomatic: {"true"}, formRegionReadRate: {"2000"},
		formRegionName: {" Turkey (mainland) "}, formRegionTaxRate: {" 18.5 "},
	}, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, RegionsPath+"?written=Turkey+%28mainland%29", rec.Header().Get("Location"))
	assert.Equal(t, []string{`reg_tr|{"name":"Turkey","automatic_taxes":true,"tax_rate":2000}|` +
		`{"name":"Turkey (mainland)","automatic_taxes":false,"tax_rate":1850}`}, reviser.corrected,
		"an unticked box stops the taxes, the rate a percent")
	landed := campaignsRequest(panel, http.MethodGet, rec.Header().Get("Location"), nil, scopeRegionRead)
	assert.Contains(t, landed.Body.String(), "Region Turkey (mainland) was written.")
	rec = campaignsRequest(panel, http.MethodPost, RegionsPath+"/reg_eu?page=2", url.Values{
		formRegionReadName: {"Europe"}, formRegionReadAutomatic: {"false"}, formRegionReadRate: {"1850"},
		formRegionName: {"Europe"}, formRegionTaxRate: {"19"},
	}, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, RegionsPath+"?page=2&written=Europe", rec.Header().Get("Location"), "back on the page it was sent from")

	reviser.err = errors.Conflict("region_revised", "region reg_tr was revised since it was read; draw the list again")
	rec = campaignsRequest(panel, http.MethodPost, RegionsPath+"/reg_tr", url.Values{
		formRegionReadName: {"Old"}, formRegionReadAutomatic: {"false"}, formRegionReadRate: {"0"},
		formRegionName: {"Typed"}, formRegionTaxRate: {"5"},
	}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "draw the list again")
	turkey = rowHolding(t, body, `action="`+RegionsPath+`/reg_tr?page=1"`)
	assert.Contains(t, turkey, "<details open>")
	assert.Contains(t, turkey, `name="name" value="Typed"`)
	assert.Contains(t, turkey, `name="tax_rate" value="5"`)
	assert.Contains(t, turkey, `value="1"> taxes computed`, "the box as typed, unticked, though the region computes them")
	assert.Contains(t, turkey, `name="read_name" value="Turkey"`, "the terms as they are now")
	assert.Equal(t, 1, strings.Count(body, "<details open>"), "the other rows are closed")

	calls := len(reviser.corrected)
	rec = campaignsRequest(panel, http.MethodPost, RegionsPath+"/reg_tr", url.Values{
		formRegionReadAutomatic: {"true"}, formRegionReadRate: {"2000"}, formRegionName: {"X"}, formRegionTaxRate: {"lots"},
	}, writer...)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "a rate that is not a number")
	rec = campaignsRequest(panel, http.MethodPost, RegionsPath+"/reg_tr", url.Values{
		formRegionReadAutomatic: {"maybe"}, formRegionReadRate: {"2000"}, formRegionName: {"X"}, formRegionTaxRate: {"1"},
	}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "could not be read; draw the list again")
	rec = campaignsRequest(panel, http.MethodPost, RegionsPath+"/reg_tr", url.Values{
		formRegionReadAutomatic: {"true"}, formRegionReadRate: {"twenty"}, formRegionName: {"X"}, formRegionTaxRate: {"1"},
	}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "could not be read; draw the list again", "a rate read that is not a number")
	assert.Len(t, reviser.corrected, calls, "none of them reached the surface")
	rec = campaignsRequest(panel, http.MethodPost, RegionsPath+"/reg_tr", url.Values{
		formRegionReadAutomatic: {"true"}, formRegionReadRate: {"2000"}, formRegionName: {"X"}, formRegionTaxRate: {"1"},
	}, scopeRegionWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.NotContains(t, rec.Body.String(), "Europe", "a writer who cannot read is shown none of the regions")
	reviser.err = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, RegionsPath+"/reg_tr", url.Values{
		formRegionReadAutomatic: {"true"}, formRegionReadRate: {"2000"}, formRegionName: {"X"}, formRegionTaxRate: {"1"},
	}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	rec = campaignsRequest(panel, http.MethodPost, RegionsPath+"/reg_tr", url.Values{}, scopeRegionRead)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	plain := revisableRegions(t, nil)
	assert.NotContains(t, campaignsRequest(plain, http.MethodGet, RegionsPath, nil, writer...).Body.String(),
		`action="`+RegionsPath+`/`, "a surface that cannot correct offers nothing")
	assert.Equal(t, http.StatusServiceUnavailable,
		campaignsRequest(plain, http.MethodPost, RegionsPath+"/reg_tr", url.Values{}, writer...).Code)
}
