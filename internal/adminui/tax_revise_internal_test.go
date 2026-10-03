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

// fakeTaxRateReviser corrects tax rates, recording each correction.
type fakeTaxRateReviser struct {
	corrected []string
	err       error
}

func (f *fakeTaxRateReviser) ReviseTaxRate(_ context.Context, id string, read, next json.RawMessage) error {
	f.corrected = append(f.corrected, id+"|"+string(read)+"|"+string(next))
	return f.err
}

// revisableTaxes is a panel over a country's tax region with two rates whose
// tax surface is the one given.
func revisableTaxes(t *testing.T, reviser TaxRateReviser) *UI {
	t.Helper()

	panel, _ := taxesPanel(t,
		query.Record{fieldID: "taxreg_tr", fieldTaxCountry: "TR", fieldTaxProvince: nil, fieldTaxProvider: "",
			fieldTaxRates: []map[string]any{
				{"id": "taxrate_vat", "name": "VAT", "code": "tr_vat", "rate_bps": int32(2000), "is_default": true},
				{"id": "taxrate_red", "name": "Reduced", "code": "", "rate_bps": int32(850), "is_default": false},
			}},
	)
	if reviser != nil {
		panel.taxes = reviser
	}

	return panel
}

// rateForm is the correction form of the rate its action names.
func rateForm(t *testing.T, body, action string) string {
	t.Helper()

	at := strings.Index(body, `action="`+action+`"`)
	require.GreaterOrEqual(t, at, 0, "the page holds a form for %s", action)
	start := strings.LastIndex(body[:at], "<details")
	end := strings.Index(body[at:], "</details>")
	require.True(t, start >= 0 && end >= 0)

	return body[start : at+end]
}

// TestATaxRateIsCorrectedOnTheTaxesScreen is ADR 0378: a writer whose surface
// can correct is offered each rate's form drawn from its name and its rate as
// a percent, carrying them as read, the rate in basis points; the surface is
// asked to correct the rate from those, the name trimmed and the rate typed as
// a percent, and the page it was sent from says so; a refusal comes back with
// what was typed for that rate alone; a rate read that cannot be read reaches
// no surface; a reader is offered nothing.
func TestATaxRateIsCorrectedOnTheTaxesScreen(t *testing.T) {
	t.Parallel()

	reviser := &fakeTaxRateReviser{}
	panel := revisableTaxes(t, reviser)
	writer := []string{scopeTaxRead, scopeTaxWrite}

	rec := campaignsRequest(panel, http.MethodGet, TaxesPath, nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	vat := rateForm(t, rec.Body.String(), TaxesPath+"/rates/taxrate_vat?page=1")
	for _, want := range []string{
		`name="read_name" value="VAT"`, `name="read_rate_bps" value="2000"`,
		`name="name" value="VAT"`, `name="rate" value="20"`,
	} {
		assert.Contains(t, vat, want)
	}
	reduced := rateForm(t, rec.Body.String(), TaxesPath+"/rates/taxrate_red?page=1")
	assert.Contains(t, reduced, `name="rate" value="8.5"`)
	assert.NotContains(t, rec.Body.String(), "<details open>", "nothing refused, nothing open")
	rec = campaignsRequest(panel, http.MethodGet, TaxesPath, nil, scopeTaxRead)
	assert.NotContains(t, rec.Body.String(), `action="`+TaxesPath+`/rates/`, "a reader corrects nothing")

	rec = campaignsRequest(panel, http.MethodPost, TaxesPath+"/rates/taxrate_vat", url.Values{
		formTaxReadName: {"VAT"}, formTaxReadRate: {"2000"}, formTaxName: {" Standard VAT "}, formTaxRate: {" 18.5 "},
	}, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, TaxesPath+"?written=Standard+VAT", rec.Header().Get("Location"))
	assert.Equal(t, []string{`taxrate_vat|{"name":"VAT","rate_bps":2000}|{"name":"Standard VAT","rate_bps":1850}`},
		reviser.corrected, "the rate typed as a percent reaches the surface in basis points")
	landed := campaignsRequest(panel, http.MethodGet, rec.Header().Get("Location"), nil, scopeTaxRead)
	assert.Contains(t, landed.Body.String(), "Tax rate Standard VAT was written.")
	rec = campaignsRequest(panel, http.MethodPost, TaxesPath+"/rates/taxrate_red?page=2", url.Values{
		formTaxReadName: {"Reduced"}, formTaxReadRate: {"850"}, formTaxName: {"Reduced"}, formTaxRate: {"9"},
	}, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, TaxesPath+"?page=2&written=Reduced", rec.Header().Get("Location"), "back on the page it was sent from")

	reviser.err = errors.Conflict("tax_rate_revised", "tax rate taxrate_vat was revised since it was read; draw the list again")
	rec = campaignsRequest(panel, http.MethodPost, TaxesPath+"/rates/taxrate_vat", url.Values{
		formTaxReadName: {"Old"}, formTaxReadRate: {"0"}, formTaxName: {"Typed"}, formTaxRate: {"5"},
	}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "draw the list again")
	vat = rateForm(t, body, TaxesPath+"/rates/taxrate_vat?page=1")
	assert.Contains(t, vat, "<details open>")
	assert.Contains(t, vat, `name="name" value="Typed"`)
	assert.Contains(t, vat, `name="rate" value="5"`)
	assert.Contains(t, vat, `name="read_name" value="VAT"`, "the terms as they are now")
	assert.Equal(t, 1, strings.Count(body, "<details open>"), "the other rate is closed")

	calls := len(reviser.corrected)
	rec = campaignsRequest(panel, http.MethodPost, TaxesPath+"/rates/taxrate_vat", url.Values{
		formTaxReadName: {"VAT"}, formTaxReadRate: {"2000"}, formTaxName: {"X"}, formTaxRate: {"lots"},
	}, writer...)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "a rate that is not a number")
	rec = campaignsRequest(panel, http.MethodPost, TaxesPath+"/rates/taxrate_vat", url.Values{
		formTaxReadName: {"VAT"}, formTaxReadRate: {"twenty"}, formTaxName: {"X"}, formTaxRate: {"1"},
	}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "could not be read; draw the list again", "a rate read that is not a number")
	assert.Len(t, reviser.corrected, calls, "neither reached the surface")
	rec = campaignsRequest(panel, http.MethodPost, TaxesPath+"/rates/taxrate_vat", url.Values{
		formTaxReadName: {"VAT"}, formTaxReadRate: {"2000"}, formTaxName: {"X"}, formTaxRate: {"1"},
	}, scopeTaxWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.NotContains(t, rec.Body.String(), "Reduced", "a writer who cannot read is shown none of the rates")
	reviser.err = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, TaxesPath+"/rates/taxrate_vat", url.Values{
		formTaxReadName: {"VAT"}, formTaxReadRate: {"2000"}, formTaxName: {"X"}, formTaxRate: {"1"},
	}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	rec = campaignsRequest(panel, http.MethodPost, TaxesPath+"/rates/taxrate_vat", url.Values{}, scopeTaxRead)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	plain := revisableTaxes(t, nil)
	assert.NotContains(t, campaignsRequest(plain, http.MethodGet, TaxesPath, nil, writer...).Body.String(),
		`action="`+TaxesPath+`/rates/`, "a surface that cannot correct offers nothing")
	assert.Equal(t, http.StatusServiceUnavailable,
		campaignsRequest(plain, http.MethodPost, TaxesPath+"/rates/taxrate_vat", url.Values{}, writer...).Code)
}
