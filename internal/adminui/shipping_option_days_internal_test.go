package adminui

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
)

// daysCatalog is optionsCatalog with business days on the standard option and
// one day on the return option, read as the option entity answers them.
func daysCatalog() *fakeCatalog {
	catalog := optionsCatalog()
	options := catalog.byEntity[EntityShippingOption]
	options[0][fieldOptionMinDays], options[0][fieldOptionMaxDays] = int32(3), int32(5)
	options[2][fieldOptionMinDays], options[2][fieldOptionMaxDays] = int32(1), int32(1)
	catalog.byEntity[EntityShippingOption] = options

	return catalog
}

// optionRowOf is the row an option prints on a page, up to its form.
func optionRowOf(t *testing.T, body, name string) string {
	t.Helper()

	_, row, found := strings.Cut(body, name+` <span class="muted">`)
	require.True(t, found, "%s is listed", name)
	row, _, _ = strings.Cut(row, "<details")

	return row
}

// TestTheShippingOptionsScreenShowsAndRevisesDeliveryDays is ADR 0421 on the
// panel: the list prints each option's business days, a range, one day, or
// that it says none, read through the option entity; a row's form carries the
// days as drawn and sends the read and the typed pair; a figure that is not a
// whole number is refused before anything is sent.
func TestTheShippingOptionsScreenShowsAndRevisesDeliveryDays(t *testing.T) {
	t.Parallel()

	catalog := daysCatalog()
	reviser := &fakeOptionReviser{}
	panel := shippingOptionsPanel(t, catalog, reviser)
	writer := []string{scopeFulfillmentRead, scopeFulfillmentWrite}

	rec := campaignsRequest(panel, http.MethodGet, ShippingOptionsPath, nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, optionRowOf(t, body, "Standard"), "<td>3–5 business days</td>")
	assert.Contains(t, optionRowOf(t, body, "Carrier rate"), `<td><span class="muted">not said</span></td>`)
	assert.Contains(t, optionRowOf(t, body, "Returns"), "<td>1 business day</td>")
	var spec query.GraphSpec
	for _, read := range catalog.specs {
		if read.Entity == EntityShippingOption {
			spec = read
		}
	}
	assert.Subset(t, spec.Fields, []string{fieldOptionMinDays, fieldOptionMaxDays}, "the days are read with the option")

	form := optionRowForm(t, body, "sopt_std")
	for _, want := range []string{
		`name="read_delivery_min_days" value="3"`, `name="read_delivery_max_days" value="5"`,
		`name="delivery_min_days" value="3"`, `name="delivery_max_days" value="5"`,
	} {
		assert.Contains(t, form, want)
	}
	rate := optionRowForm(t, body, "sopt_rate")
	assert.Contains(t, rate, `name="read_delivery_min_days" value=""`, "an option that says none reads none")

	rec = campaignsRequest(panel, http.MethodPost, ShippingOptionsPath+"/sopt_std?page=1", url.Values{
		formReadName: {"Standard"}, formReadAmount: {"2500"}, formReadAdminOnly: {"false"},
		formReadMinDays: {"3"}, formReadMaxDays: {"5"},
		formOptionPriceType: {"flat"}, formOptionCurrencyCode: {"TRY"},
		formGroupName: {"Standard"}, formOptionAmount: {"25.00"},
		formOptionMinDays: {" 1 "}, formOptionMaxDays: {"2"},
	}, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"3/5->1/2"}, reviser.days, "the read pair and the typed one")

	rec = campaignsRequest(panel, http.MethodPost, ShippingOptionsPath+"/sopt_rate?page=1", url.Values{
		formReadName: {"Carrier rate"}, formReadAmount: {"0"}, formReadAdminOnly: {"true"},
		formReadMinDays: {""}, formReadMaxDays: {""},
		formOptionPriceType: {"calculated"}, formGroupName: {"Carrier rate"},
	}, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, "-/-->-/-", reviser.days[1], "none read, none typed")

	rec = campaignsRequest(panel, http.MethodPost, ShippingOptionsPath+"/sopt_std", url.Values{
		formReadName: {"Standard"}, formReadAmount: {"2500"}, formReadAdminOnly: {"false"},
		formReadMinDays: {"3"}, formReadMaxDays: {"5"},
		formOptionPriceType: {"flat"}, formOptionCurrencyCode: {"TRY"},
		formGroupName: {"Standard"}, formOptionAmount: {"25.00"},
		formOptionMinDays: {"three"}, formOptionMaxDays: {"5"},
	}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "whole numbers of business days")
	form = optionRowForm(t, rec.Body.String(), "sopt_std")
	assert.Contains(t, form, `name="delivery_min_days" value="three"`, "the refused row carries what was typed")
	assert.Len(t, reviser.days, 2, "what the panel cannot read is not sent")
}

// TestTheShippingOptionFormWritesDeliveryDays is ADR 0421 on the new option's
// form: two figures are sent as the option's days, blank ones as none.
func TestTheShippingOptionFormWritesDeliveryDays(t *testing.T) {
	t.Parallel()

	writer := &fakeOptionWriter{choices: twoProviders}
	panel := shippingOptionsPanel(t, optionsCatalog(), writer)
	scopes := []string{scopeFulfillmentRead, scopeFulfillmentWrite}

	rec := campaignsRequest(panel, http.MethodGet, ShippingOptionsPath, nil, scopes...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	form := newOptionForm(t, rec.Body.String())
	assert.Contains(t, form, `name="delivery_min_days"`)
	assert.Contains(t, form, `name="delivery_max_days"`)

	for _, typed := range []url.Values{
		{formOptionMinDays: {"2"}, formOptionMaxDays: {" 4 "}},
		{formOptionMinDays: {""}, formOptionMaxDays: {""}},
	} {
		typed.Set(formGroupName, "Courier")
		typed.Set(formOptionProvider, "manual")
		typed.Set(formOptionProfile, "sprof_default")
		typed.Set(formOptionPriceType, "flat")
		typed.Set(formOptionCurrencyCode, "TRY")
		rec = campaignsRequest(panel, http.MethodPost, ShippingOptionsPath, typed, scopes...)
		require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	}
	assert.Equal(t, []string{"2/4", "-/-"}, writer.days)
}
