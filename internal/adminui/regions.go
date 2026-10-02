package adminui

import (
	"net/http"
	"net/url"
	"strings"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// The Regions screen (ADR 0354): the region module's regions through its
// region entity, each with its currency, its tax rate, whether taxes are
// computed for it and the countries it covers, for an operator who may read
// the regions, the codes printed as the module stores them, upper-cased.
// Each row corrects its region for an operator who may write them (ADR
// 0362).

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
	// RateBps is the tax rate in basis points as read, which the correction
	// form carries (ADR 0362).
	RateBps int64
	// Form is what the row's correction form offers: the terms as read, or
	// what was typed when its correction was refused, which Refused says.
	Form    regionForm
	Refused bool
}

// regionForm is what a region's correction form offers.
type regionForm struct {
	Name, TaxRate  string
	AutomaticTaxes bool
}

// listRegions renders the regions a page at a time.
func (u *UI) listRegions(w http.ResponseWriter, r *http.Request) {
	u.renderRegions(w, r, http.StatusOK, "", nil)
}

// renderRegions lists the regions with a refused correction's reason and
// what was typed in the row of the region it was sent for. An operator who
// may correct and not read is told the reason alone (ADR 0260).
func (u *UI) renderRegions(w http.ResponseWriter, r *http.Request, code int, refused string, typed url.Values) {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if refused != "" && !principal.HasScope(scopeRegionRead) {
		u.errorPage(w, r, code, "Not done", refused)
		return
	}
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
			RateBps:        int64(rate),
		}
		row.Form = regionForm{Name: row.Name, TaxRate: row.TaxRate, AutomaticTaxes: row.AutomaticTaxes}
		if refusedID := typed.Get(formRegionID); refusedID != "" && row.ID == refusedID {
			row.Refused = true
			row.Form = regionForm{
				Name: typed.Get(formRegionName), TaxRate: typed.Get(formRegionTaxRate),
				AutomaticTaxes: typed.Get(formRegionAutomatic) != "",
			}
		}
		for _, country := range recordList(record[fieldRegionCountries]) {
			row.Countries = append(row.Countries,
				strings.TrimSpace(recordString(country, fieldCountryCode)+" "+recordString(country, fieldName)))
		}
		rows = append(rows, row)
	}

	data := map[string]any{
		titleKey:     regionsLabel,
		"Regions":    rows,
		canReviseKey: u.regions != nil && principal.HasScope(scopeRegionWrite),
		writtenKey:   r.URL.Query().Get(paramWritten),
		refusedKey:   refused,
	}
	addPaging(data, page, hasNext, RegionsPath)

	u.templates.render(w, r, code, "regions.gohtml", data)
}
