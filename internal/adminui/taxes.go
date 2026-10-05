package adminui

import (
	"net/http"
	"net/url"
	"time"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// The Taxes screen (ADR 0355): the tax module's tax regions through its tax
// region entity, a country's own and its provinces', each with the rates it
// charges, for an operator who may read the taxes, the codes printed as the
// module stores them, upper-cased. Each rate corrects its name and rate for
// an operator who may write the taxes (ADR 0378), and is tried on past orders
// for one who may read the orders too (ADR 0395).

// TaxesPath lists the tax regions.
const TaxesPath = URLPrefix + "/taxes"

// taxesLabel is what the section is called on screen.
const taxesLabel = "Taxes"

// EntityTaxRegion is the tax module's tax region entity in the read layer,
// pinned against the module's in internal/arch.
const EntityTaxRegion = "tax_region"

// localTaxProvider is the tax module's own provider, as it names it; a region
// naming another one is taxed outside the module.
const localTaxProvider = "local"

// taxRegionsPerPage is the list's page size, the other lists'.
const taxRegionsPerPage = 25

// The tax region's fields the screen reads beside its id, and its rates'.
const (
	fieldTaxCountry  = "country_code"
	fieldTaxProvince = "province_code"
	fieldTaxProvider = "provider_id"
	fieldTaxRates    = "rates"
	fieldRateBps     = "rate_bps"
	fieldRateDefault = "is_default"
)

// taxRegionRow is one tax region as the screen draws it.
type taxRegionRow struct {
	ID string
	// Country is the region's country code.
	Country string
	// Province is the sub-country unit the tax schema defines (an il, ADR
	// 0067), empty on a country's own region.
	Province string
	// Provider is the tax provider the region names, empty when it inherits
	// its country's.
	Provider string
	Rates    []taxRateView
	// Tryable says the region's rates can be tried on past orders (ADR
	// 0395): a country's own, which a cart reaches, whose tax the module
	// computes. A province's rate is reached by no cart, which sends no
	// province, and an external provider's table cannot be amended.
	Tryable bool
}

// taxRateView is one rate a tax region charges.
type taxRateView struct {
	ID, Name, Code string
	// Percent is the rate as a percent.
	Percent string
	Default bool
	// RateBps is the rate in basis points as read, which the correction form
	// carries (ADR 0378).
	RateBps int64
	// Form is what the rate's correction form offers: the terms as read, or
	// what was typed when its correction was refused, which Refused says.
	Form    taxRateForm
	Refused bool
}

// taxRateForm is what a rate's correction form offers.
type taxRateForm struct {
	Name, Rate string
}

// listTaxes renders the tax regions a page at a time, in the module's order:
// by country, a country's own before its provinces.
func (u *UI) listTaxes(w http.ResponseWriter, r *http.Request) {
	u.renderTaxes(w, r, http.StatusOK, "", nil)
}

// renderTaxes lists the tax regions with a refused correction's reason and
// what was typed for the rate it was sent for. An operator who may correct and
// not read is told the reason alone (ADR 0260).
func (u *UI) renderTaxes(w http.ResponseWriter, r *http.Request, code int, refused string, typed url.Values) {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if refused != "" && !principal.HasScope(scopeTaxRead) {
		u.errorPage(w, r, code, "Not done", refused)
		return
	}
	page := pageNumber(r.URL.Query().Get("page"))
	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity: EntityTaxRegion,
		Fields: []string{fieldID, fieldTaxCountry, fieldTaxProvince, fieldTaxProvider, fieldTaxRates},
		Limit:  taxRegionsPerPage + 1,
		Offset: (page - 1) * taxRegionsPerPage,
	})
	if err != nil {
		u.catalogFailure(w, r, err, "The tax regions could not be read.")
		return
	}
	hasNext := len(records) > taxRegionsPerPage
	if hasNext {
		records = records[:taxRegionsPerPage]
	}

	rows := make([]taxRegionRow, 0, len(records))
	for _, record := range records {
		row := taxRegionRow{
			ID:       recordString(record, fieldID),
			Country:  recordString(record, fieldTaxCountry),
			Province: recordString(record, fieldTaxProvince),
			Provider: recordString(record, fieldTaxProvider),
		}
		row.Tryable = row.Province == "" && (row.Provider == "" || row.Provider == localTaxProvider)
		for _, rate := range recordList(record[fieldTaxRates]) {
			bps, _ := intValue(rate[fieldRateBps])
			view := taxRateView{
				ID: recordString(rate, fieldID), Name: recordString(rate, fieldName), Code: recordString(rate, "code"),
				Percent: percentText(int64(bps)), Default: recordBool(rate, fieldRateDefault), RateBps: int64(bps),
			}
			view.Form = taxRateForm{Name: view.Name, Rate: view.Percent}
			if refusedID := typed.Get(formTaxRateID); refusedID != "" && view.ID == refusedID {
				view.Refused = true
				view.Form = taxRateForm{Name: typed.Get(formTaxName), Rate: typed.Get(formTaxRate)}
			}
			row.Rates = append(row.Rates, view)
		}
		rows = append(rows, row)
	}

	trial, _ := trialPeriodOf(nil, time.Now())
	data := map[string]any{
		titleKey:     taxesLabel,
		"TaxRegions": rows,
		canReviseKey: u.taxes != nil && principal.HasScope(scopeTaxWrite),
		canTryKey:    u.taxTrials != nil && principal.HasScope(scopeOrderRead),
		"Trial":      trial,
		"References": trialRuleReferences,
		writtenKey:   r.URL.Query().Get(paramWritten),
		refusedKey:   refused,
	}
	addPaging(data, page, hasNext, TaxesPath)

	u.templates.render(w, r, code, "taxes.gohtml", data)
}
