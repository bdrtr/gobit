package adminui

import (
	"net/http"
	"strings"

	"github.com/bdrtr/gobit/core/query"
)

// The Regions screen (ADR 0354): the region module's regions through its
// region entity, each with its currency, its tax rate, whether taxes are
// computed for it and the countries it covers, for an operator who may read
// the regions. Nothing is written here, and the codes are printed as the
// module stores them, upper-cased.

// RegionsPath lists the regions.
const RegionsPath = URLPrefix + "/regions"

// regionsLabel is what the section is called on screen.
const regionsLabel = "Regions"

// regionsPerPage is the list's page size, the other lists'.
const regionsPerPage = 25

// The region's fields the screen reads beside its id, name, currency code
// and the time it was made.
const (
	fieldRegionTaxRate   = "tax_rate"
	fieldRegionAutoTaxes = "automatic_taxes"
	fieldRegionCountries = "countries"
	fieldCountryCode     = "code"
)

// regionRow is one region as the screen draws it.
type regionRow struct {
	ID, Name, Currency string
	// TaxRate is the rate as a percent.
	TaxRate        string
	AutomaticTaxes bool
	// Countries are the countries the region covers, each its code and
	// name.
	Countries []string
}

// listRegions renders the regions a page at a time.
func (u *UI) listRegions(w http.ResponseWriter, r *http.Request) {
	page := pageNumber(r.URL.Query().Get("page"))
	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity: EntityRegion,
		Fields: []string{
			fieldID, fieldName, fieldCurrencyCod, fieldRegionTaxRate, fieldRegionAutoTaxes, fieldRegionCountries,
		},
		Limit:  regionsPerPage + 1,
		Offset: (page - 1) * regionsPerPage,
	})
	if err != nil {
		u.catalogFailure(w, r, err, "The regions could not be read.")
		return
	}
	hasNext := len(records) > regionsPerPage
	if hasNext {
		records = records[:regionsPerPage]
	}

	rows := make([]regionRow, 0, len(records))
	for _, record := range records {
		rate, _ := intValue(record[fieldRegionTaxRate])
		row := regionRow{
			ID: recordString(record, fieldID), Name: recordString(record, fieldName),
			Currency:       recordString(record, fieldCurrencyCod),
			TaxRate:        percentText(int64(rate)),
			AutomaticTaxes: recordBool(record, fieldRegionAutoTaxes),
		}
		for _, country := range recordList(record[fieldRegionCountries]) {
			row.Countries = append(row.Countries,
				strings.TrimSpace(recordString(country, fieldCountryCode)+" "+recordString(country, fieldName)))
		}
		rows = append(rows, row)
	}

	data := map[string]any{titleKey: regionsLabel, "Regions": rows}
	addPaging(data, page, hasNext, RegionsPath)

	u.templates.render(w, r, http.StatusOK, "regions.gohtml", data)
}
