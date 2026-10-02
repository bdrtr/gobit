package adminui

import (
	"net/http"

	"github.com/bdrtr/gobit/core/query"
)

// The Taxes screen (ADR 0355): the tax module's tax regions through its tax
// region entity, a country's own and its provinces', each with the rates it
// charges, for an operator who may read the taxes. Nothing is written here,
// and the codes are printed as the module stores them, upper-cased.

// TaxesPath lists the tax regions.
const TaxesPath = URLPrefix + "/taxes"

// taxesLabel is what the section is called on screen.
const taxesLabel = "Taxes"

// EntityTaxRegion is the tax module's tax region entity in the read layer,
// pinned against the module's in internal/arch.
const EntityTaxRegion = "tax_region"

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
}

// taxRateView is one rate a tax region charges.
type taxRateView struct {
	Name, Code string
	// Percent is the rate as a percent.
	Percent string
	Default bool
}

// listTaxes renders the tax regions a page at a time, in the module's order:
// by country, a country's own before its provinces.
func (u *UI) listTaxes(w http.ResponseWriter, r *http.Request) {
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
		for _, rate := range recordList(record[fieldTaxRates]) {
			bps, _ := intValue(rate[fieldRateBps])
			row.Rates = append(row.Rates, taxRateView{
				Name: recordString(rate, fieldName), Code: recordString(rate, "code"),
				Percent: percentText(int64(bps)), Default: recordBool(rate, fieldRateDefault),
			})
		}
		rows = append(rows, row)
	}

	data := map[string]any{titleKey: taxesLabel, "TaxRegions": rows}
	addPaging(data, page, hasNext, TaxesPath)

	u.templates.render(w, r, http.StatusOK, "taxes.gohtml", data)
}
