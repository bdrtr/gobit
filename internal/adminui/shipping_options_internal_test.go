package adminui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// fakeOptionReviser moves parcels as fakeParcelMover does and revises
// shipping options, recording each revision.
type fakeOptionReviser struct {
	fakeParcelMover
	revised []string
	err     error
}

func (f *fakeOptionReviser) ReviseShippingOption(
	_ context.Context, id, readName string, readAmount int64, readAdminOnly bool,
	name string, amount int64, adminOnly bool,
) error {
	f.revised = append(f.revised, fmt.Sprintf("%s|%s|%d|%t|%s|%d|%t",
		id, readName, readAmount, readAdminOnly, name, amount, adminOnly))
	return f.err
}

// optionsCatalog holds a flat option, a calculated one the storefront does
// not offer, and a return option, in a shop selling in TRY.
func optionsCatalog() *fakeCatalog {
	return &fakeCatalog{byEntity: map[string][]query.Record{
		EntityRegion: {{
			"id": "reg_1", "name": "Turkey", "currency_code": "TRY",
			"currency": map[string]any{"code": "TRY", "decimal_digits": int64(2)},
		}},
		EntityShippingOption: {
			{
				fieldID: "sopt_std", fieldName: "Standard", fieldOptionProvider: "manual", fieldOptionProfile: "sp_default",
				fieldOptionPrice: "flat", fieldAmount: int64(2500), fieldCurrencyCod: "TRY", fieldOptionRegion: "reg_tr",
				fieldOptionReturn: false, fieldOptionAdmin: false,
			},
			{
				fieldID: "sopt_rate", fieldName: "Carrier rate", fieldOptionProvider: "spy", fieldOptionProfile: "sp_bulky",
				fieldOptionPrice: "calculated", fieldAmount: int64(0), fieldCurrencyCod: "TRY", fieldOptionRegion: "",
				fieldOptionReturn: false, fieldOptionAdmin: true,
			},
			{
				fieldID: "sopt_back", fieldName: "Returns", fieldOptionProvider: "manual", fieldOptionProfile: "sp_default",
				fieldOptionPrice: "flat", fieldAmount: int64(0), fieldCurrencyCod: "TRY", fieldOptionRegion: "reg_tr",
				fieldOptionReturn: true, fieldOptionAdmin: false,
			},
		},
	}}
}

// shippingOptionsPanel is a panel over the options with the fulfillment
// module's surface.
func shippingOptionsPanel(t *testing.T, catalog *fakeCatalog, parcels ParcelMover) *UI {
	t.Helper()

	panel := newCatalogPanel(t, catalog)
	panel.parcels = parcels
	panel.scopes = builtInScopes()

	return panel
}

// optionRowForm is the revise form of the option's row on a page.
func optionRowForm(t *testing.T, body, id string) string {
	t.Helper()

	_, form, found := strings.Cut(body, `action="`+ShippingOptionsPath+"/"+id+"?page=")
	require.True(t, found, "%s offers its form", id)
	form, _, _ = strings.Cut(form, "</form>")

	return form
}

// TestTheShippingOptionsScreenListsTheOptions is ADR 0333: each option with
// its provider, profile, region or every region, its fee in its currency or
// the provider's, whether the storefront offers it and whether it is for
// returns, read a page and one more at a time through the option entity; a
// reader is offered no form.
func TestTheShippingOptionsScreenListsTheOptions(t *testing.T) {
	t.Parallel()

	catalog := optionsCatalog()
	panel := shippingOptionsPanel(t, catalog, &fakeOptionReviser{})

	rec := campaignsRequest(panel, http.MethodGet, ShippingOptionsPath+"?page=2", nil, scopeFulfillmentRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	_, standard, _ := strings.Cut(body, "Standard ")
	standard, _, _ = strings.Cut(standard, "</tr>")
	for _, want := range []string{"sopt_std", "<td>manual</td>", "sp_default", "<td>reg_tr</td>", "25.00 TRY", "<td>offered</td>"} {
		assert.Contains(t, standard, want)
	}
	_, rate, _ := strings.Cut(body, "Carrier rate ")
	rate, _, _ = strings.Cut(rate, "</tr>")
	for _, want := range []string{"every region", "from the provider", "panel only"} {
		assert.Contains(t, rate, want)
	}
	assert.Contains(t, body, `Returns <span class="muted">sopt_back</span> <span class="pill">return</span>`)
	assert.NotContains(t, body, "Revise", "a reader revises nothing")
	var spec query.GraphSpec
	for _, read := range catalog.specs {
		if read.Entity == EntityShippingOption {
			spec = read
		}
	}
	require.Equal(t, EntityShippingOption, spec.Entity, "the options are read through their entity")
	assert.Equal(t, shippingOptionsPerPage+1, spec.Limit, "a page and one more, to know there is a next")
	assert.Equal(t, shippingOptionsPerPage, spec.Offset, "the second page")
	assert.NotContains(t, body, "page=3", "three options fill no second page")

	rec = campaignsRequest(shippingOptionsPanel(t, optionsCatalog(), &fakeParcelMover{}), http.MethodGet,
		ShippingOptionsPath, nil, scopeFulfillmentRead, scopeFulfillmentWrite)
	assert.NotContains(t, rec.Body.String(), "Revise", "a surface that cannot revise offers no form")
	rec = campaignsRequest(shippingOptionsPanel(t, optionsCatalog(), &fakeParcelMover{}), http.MethodPost,
		ShippingOptionsPath+"/sopt_std", url.Values{formGroupName: {"X"}}, scopeFulfillmentWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestAShippingOptionRowRevisesItsOption is ADR 0333: each row offers a
// writer the form that revises its option, carrying the name, the fee in
// minor units and the visibility as drawn, the fee field only on a flat
// option and shown in its currency's decimals; the surface is asked to revise
// the option from those, a calculated option with no fee; what the panel
// cannot read is not sent, and a refusal comes back in the row with what was
// typed, drawn from the option as it is now.
func TestAShippingOptionRowRevisesItsOption(t *testing.T) {
	t.Parallel()

	reviser := &fakeOptionReviser{}
	panel := shippingOptionsPanel(t, optionsCatalog(), reviser)
	writer := []string{scopeFulfillmentRead, scopeFulfillmentWrite}

	rec := campaignsRequest(panel, http.MethodGet, ShippingOptionsPath+"?page=2", nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	form := optionRowForm(t, body, "sopt_std")
	assert.True(t, strings.HasPrefix(form, `2">`), "the form returns to the page it was drawn on")
	for _, want := range []string{
		`name="read_name" value="Standard"`, `name="read_amount" value="2500"`, `name="read_admin_only" value="false"`,
		`name="price_type" value="flat"`, `name="currency_code" value="TRY"`, `name="name" value="Standard"`,
		`name="amount" value="25.00"`, `name="admin_only" value="1">`,
	} {
		assert.Contains(t, form, want)
	}
	rate := optionRowForm(t, body, "sopt_rate")
	assert.NotContains(t, rate, `name="amount"`, "a calculated option's fee is its provider's")
	assert.Contains(t, rate, `name="read_admin_only" value="true"`)
	assert.Contains(t, rate, `name="admin_only" value="1" checked>`)
	assert.NotContains(t, body, "<details open>", "no row is open until a refusal opens it")

	sent := url.Values{
		formReadName: {"Standard"}, formReadAmount: {"2500"}, formReadAdminOnly: {"false"},
		formOptionPriceType: {"flat"}, formOptionCurrencyCode: {"TRY"},
		formGroupName: {" Economy "}, formOptionAmount: {" 19.90 "}, formOptionAdminOnly: {"1"},
	}
	rec = campaignsRequest(panel, http.MethodPost, ShippingOptionsPath+"/sopt_std?page=2", sent, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, ShippingOptionsPath+"?created=Economy&page=2", rec.Header().Get("Location"))
	assert.Equal(t, []string{"sopt_std|Standard|2500|false|Economy|1990|true"}, reviser.revised,
		"a flat fee in its currency's decimals, the box ticked hidden")
	landed := campaignsRequest(panel, http.MethodGet, rec.Header().Get("Location"), nil, scopeFulfillmentRead)
	assert.Contains(t, landed.Body.String(), "Shipping option Economy was written.")

	rec = campaignsRequest(panel, http.MethodPost, ShippingOptionsPath+"/sopt_rate?page=1", url.Values{
		formReadName: {"Carrier rate"}, formReadAmount: {"0"}, formReadAdminOnly: {"true"},
		formOptionPriceType: {"calculated"}, formGroupName: {"Carrier rate"}, formOptionAmount: {"5.00"},
	}, writer...)
	assert.Equal(t, ShippingOptionsPath+"?created=Carrier+rate", rec.Header().Get("Location"), "the first page is the list")
	assert.Equal(t, "sopt_rate|Carrier rate|0|true|Carrier rate|0|false", reviser.revised[1],
		"a calculated option sends no fee, and an unticked box offers it")

	for reason, form := range map[string]url.Values{
		"could not be read; draw the list again": {formReadAmount: {"25.00"}, formReadAdminOnly: {"false"}},
		"draw the list again":                    {formReadAmount: {"2500"}, formReadAdminOnly: {"maybe"}},
		"more than one decimal point": {
			formReadAmount: {"2500"}, formReadAdminOnly: {"false"}, formOptionPriceType: {"flat"},
			formOptionCurrencyCode: {"TRY"}, formOptionAmount: {"1.2.3"},
		},
	} {
		rec = campaignsRequest(panel, http.MethodPost, ShippingOptionsPath+"/sopt_std", form, writer...)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, reason)
		assert.Contains(t, rec.Body.String(), reason)
	}
	assert.Len(t, reviser.revised, 2, "what the panel cannot read is not sent")

	reviser.err = errors.Conflict("fulfillment_shipping_option_revised",
		`shipping option sopt_std was revised since it was read: it is "Standard" now; draw the list again`)
	rec = campaignsRequest(panel, http.MethodPost, ShippingOptionsPath+"/sopt_std", url.Values{
		formReadName: {"Old"}, formReadAmount: {"100"}, formReadAdminOnly: {"false"},
		formOptionPriceType: {"flat"}, formOptionCurrencyCode: {"TRY"},
		formGroupName: {"Gold"}, formOptionAmount: {"7.50"}, formOptionAdminOnly: {"1"},
	}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body = rec.Body.String()
	assert.Contains(t, body, "draw the list again")
	form = optionRowForm(t, body, "sopt_std")
	for _, want := range []string{
		`name="read_name" value="Standard"`, `name="read_amount" value="2500"`, `name="read_admin_only" value="false"`,
		`name="name" value="Gold"`, `name="amount" value="7.50"`, `name="admin_only" value="1" checked>`,
	} {
		assert.Contains(t, form, want, "the row carries the option as it is now and what was typed")
	}
	assert.Contains(t, body, "<details open>", "the refused row is open")
	assert.Contains(t, optionRowForm(t, body, "sopt_back"), `name="name" value="Returns"`, "another row is as drawn")

	reviser.err = errors.NotFound("fulfillment_shipping_option_not_found", "shipping option not found: sopt_std")
	rec = campaignsRequest(panel, http.MethodPost, ShippingOptionsPath+"/sopt_std", sent, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "shipping option not found: sopt_std")
	rec = campaignsRequest(panel, http.MethodPost, ShippingOptionsPath+"/sopt_std", sent, scopeFulfillmentWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.NotContains(t, rec.Body.String(), "sopt_back", "a writer who cannot read is shown none of the list")
	reviser.err = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, ShippingOptionsPath+"/sopt_std", sent, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// fakeOptionWriter revises options as fakeOptionReviser does, offers the
// scripted choices and records each option written.
type fakeOptionWriter struct {
	fakeOptionReviser
	choices   string
	choiceErr error
	written   []string
	writeErr  error
}

func (f *fakeOptionWriter) OptionChoicesJSON(context.Context) (json.RawMessage, error) {
	return json.RawMessage(f.choices), f.choiceErr
}

func (f *fakeOptionWriter) CreateShippingOption(
	_ context.Context, name, providerID, profileID, priceType string, amount int64,
	currency, regionID string, isReturn, adminOnly bool,
) (string, error) {
	f.written = append(f.written, fmt.Sprintf("%s|%s|%s|%s|%d|%s|%s|%t|%t",
		name, providerID, profileID, priceType, amount, currency, regionID, isReturn, adminOnly))
	return "sopt_new", f.writeErr
}

// twoProviders is what an option is written on: two providers and a profile,
// with more profiles than the form offers.
const twoProviders = `{"providers":["manual","spy"],
	"profiles":[{"id":"sprof_default","name":"Default","type":"default"}],"profiles_more":true}`

// newOptionForm is the page's form that writes an option.
func newOptionForm(t *testing.T, body string) string {
	t.Helper()

	_, form, found := strings.Cut(body, `<summary>New shipping option</summary>`)
	require.True(t, found, "the page offers the form that writes an option")
	form, _, _ = strings.Cut(form, "</details>")

	return form
}

// TestTheShippingOptionFormWritesWhatWasTyped is ADR 0334: a writer whose
// surface can write is offered the registered providers, the newest
// profiles and the regions with their currencies; the option is written with
// the name trimmed, in the chosen region's currency whatever was typed or in
// the typed one for every region, its fee read in that currency's decimals,
// and the list it lands on names it; a region or a fee the panel cannot read
// is not sent, and a refusal comes back with what was typed.
func TestTheShippingOptionFormWritesWhatWasTyped(t *testing.T) {
	t.Parallel()

	writer := &fakeOptionWriter{choices: twoProviders}
	panel := shippingOptionsPanel(t, optionsCatalog(), writer)
	scopes := []string{scopeFulfillmentRead, scopeFulfillmentWrite}

	rec := campaignsRequest(panel, http.MethodGet, ShippingOptionsPath, nil, scopes...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	form := newOptionForm(t, rec.Body.String())
	for _, want := range []string{
		`<option value="manual">manual</option>`, `<option value="spy">spy</option>`,
		`<option value="sprof_default">Default</option>`, "the newest profiles",
		`<option value="">every region</option>`, `<option value="reg_1">Turkey (TRY)</option>`,
		`<option value="calculated">the provider's rate</option>`,
	} {
		assert.Contains(t, form, want)
	}
	assert.NotContains(t, rec.Body.String(), "<details open>", "nothing typed, nothing open")

	rec = campaignsRequest(panel, http.MethodPost, ShippingOptionsPath, url.Values{
		formGroupName: {" Courier "}, formOptionProvider: {"spy"}, formOptionProfile: {"sprof_default"},
		formOptionPriceType: {"flat"}, formOptionAmount: {" 19.90 "}, formOptionRegion: {"reg_1"},
		formOptionCurrencyCode: {"usd"}, formOptionReturn: {"1"},
	}, scopes...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, ShippingOptionsPath+"?created=Courier", rec.Header().Get("Location"))
	assert.Equal(t, []string{"Courier|spy|sprof_default|flat|1990|TRY|reg_1|true|false"}, writer.written,
		"the region's currency, whatever was typed; for returns and offered")

	campaignsRequest(panel, http.MethodPost, ShippingOptionsPath, url.Values{
		formGroupName: {"Abroad"}, formOptionProvider: {"manual"}, formOptionProfile: {"sprof_default"},
		formOptionPriceType: {"flat"}, formOptionAmount: {"500"}, formOptionCurrencyCode: {" usd "},
		formOptionAdminOnly: {"1"},
	}, scopes...)
	assert.Equal(t, "Abroad|manual|sprof_default|flat|500|USD||false|true", writer.written[1],
		"every region in the typed currency, a fee in minor units where the scale is not known; panel only")

	for reason, typed := range map[string]url.Values{
		"The region reg_gone could not be read; draw the page again.": {formGroupName: {"Gold"}, formOptionRegion: {"reg_gone"}},
		"This currency has 2 decimal digits":                          {formGroupName: {"Gold"}, formOptionRegion: {"reg_1"}, formOptionAmount: {"1.234"}},
	} {
		rec = campaignsRequest(panel, http.MethodPost, ShippingOptionsPath, typed, scopes...)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, reason)
		assert.Contains(t, rec.Body.String(), reason)
		assert.Contains(t, newOptionForm(t, rec.Body.String()), `value="Gold"`, "what was typed comes back")
		assert.Contains(t, rec.Body.String(), "<details open>", "the form is open on what was typed")
	}
	assert.Len(t, writer.written, 2, "what the panel cannot read is not sent")

	writer.writeErr = errors.NotFound("fulfillment_provider_not_found", `the shipping provider "spy" is not registered`)
	rec = campaignsRequest(panel, http.MethodPost, ShippingOptionsPath, url.Values{formGroupName: {"Gold"}}, scopes...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "is not registered")
	writer.writeErr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, ShippingOptionsPath, url.Values{formGroupName: {"Gold"}}, scopes...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	rec = campaignsRequest(shippingOptionsPanel(t, optionsCatalog(), &fakeOptionWriter{choices: `{"providers":["manual"],"profiles":[]}`}),
		http.MethodGet, ShippingOptionsPath, nil, scopes...)
	assert.Contains(t, rec.Body.String(), "No shipping profile has been written; an option is written on one.")
	assert.NotContains(t, rec.Body.String(), `action="`+ShippingOptionsPath+`"`, "no profile, no form")
	rec = campaignsRequest(shippingOptionsPanel(t, optionsCatalog(), &fakeOptionWriter{choiceErr: errors.Unavailable("db_down", "x")}),
		http.MethodGet, ShippingOptionsPath, nil, scopes...)
	assert.NotContains(t, rec.Body.String(), "New shipping option", "choices that cannot be read draw no form")
	rec = campaignsRequest(panel, http.MethodGet, ShippingOptionsPath, nil, scopeFulfillmentRead)
	assert.NotContains(t, rec.Body.String(), "New shipping option", "a reader writes nothing")
	rec = campaignsRequest(shippingOptionsPanel(t, optionsCatalog(), &fakeOptionReviser{}), http.MethodGet,
		ShippingOptionsPath, nil, scopes...)
	assert.NotContains(t, rec.Body.String(), "New shipping option", "a surface that cannot write offers no form")
	rec = campaignsRequest(shippingOptionsPanel(t, optionsCatalog(), &fakeOptionReviser{}), http.MethodPost,
		ShippingOptionsPath, url.Values{formGroupName: {"X"}}, scopeFulfillmentWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
