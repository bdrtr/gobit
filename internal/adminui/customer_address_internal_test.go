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

// fakeAddressReviser does what fakeContactReviser does and corrects
// customers' addresses, recording each correction.
type fakeAddressReviser struct {
	fakeContactReviser
	corrected []string
	err       error
}

func (f *fakeAddressReviser) ReviseCustomerAddress(_ context.Context, customerID, addressID string, read, next json.RawMessage) error {
	f.corrected = append(f.corrected, customerID+"|"+addressID+"|"+string(read)+"|"+string(next))
	return f.err
}

// addressCatalog holds a customer with two addresses.
func addressCatalog() *fakeCatalog {
	return &fakeCatalog{byEntity: map[string][]query.Record{
		EntityCustomer: {{
			fieldID: "cus_1", "email": "ada@example.test", fieldFirstName: "Ada", fieldCustomerGroupIDs: []string{},
			fieldCustomerAddresses: []map[string]any{{
				"id": "cadr_1", "first_name": "Ada", "last_name": "Byron", "company": "", "address_1": "Bagdat Cd. 1",
				"address_2": "", "city": "Istanbul", "postal_code": "34710", "country_code": "TR", "phone": "555",
				"is_default_shipping": true,
			}, {
				"id": "cadr_2", "first_name": "", "last_name": "", "company": "Engines Ltd", "address_1": "Fleet St 2",
				"address_2": "", "city": "London", "postal_code": "", "country_code": "GB", "phone": "",
			}},
		}},
	}}
}

// addressForm is the customer page's form for the address.
func addressForm(t *testing.T, body, addressID string) string {
	t.Helper()

	_, form, found := strings.Cut(body, `action="`+CustomersPath+`/cus_1/addresses/`+addressID+`"`)
	require.True(t, found, "the page offers the form for %s", addressID)
	form, _, _ = strings.Cut(form, "</form>")

	return form
}

// TestACustomersAddressIsCorrectedOnTheirPage is ADR 0342: a writer whose
// surface can correct is offered a form for each address drawn from its
// printed fields as the page was read; the surface is asked to correct that
// address from those, the typed ones trimmed, and the page says so; a
// refusal comes back on the page with what was typed in that address's form
// alone, the drawn fields as they are now and the contact form untouched; a
// reader is offered nothing.
func TestACustomersAddressIsCorrectedOnTheirPage(t *testing.T) {
	t.Parallel()

	reviser := &fakeAddressReviser{}
	panel := membershipPanel(t, addressCatalog(), reviser)
	page := CustomersPath + "/cus_1"
	writer := []string{scopeCustomerRead, scopeCustomerWrite}

	rec := campaignsRequest(panel, http.MethodGet, page, nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	form := addressForm(t, rec.Body.String(), "cadr_1")
	for _, want := range []string{
		`name="read_address_1" value="Bagdat Cd. 1"`, `name="read_city" value="Istanbul"`,
		`name="read_country_code" value="TR"`, `name="read_postal_code" value="34710"`, `name="read_phone" value="555"`,
		`name="read_first_name" value="Ada"`, `name="read_last_name" value="Byron"`, `name="read_company" value=""`,
		`name="read_address_2" value=""`,
		`name="address_1" value="Bagdat Cd. 1"`, `name="city" value="Istanbul"`, `name="country_code" value="TR"`,
		`name="first_name" value="Ada"`, `name="last_name" value="Byron"`, `name="postal_code" value="34710"`,
		`name="phone" value="555"`,
	} {
		assert.Contains(t, form, want)
	}
	form = addressForm(t, rec.Body.String(), "cadr_2")
	assert.Contains(t, form, `name="company" value="Engines Ltd"`)
	assert.Contains(t, form, `name="read_city" value="London"`)
	assert.NotContains(t, rec.Body.String(), "<details open>", "nothing refused, nothing open")
	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopeCustomerRead)
	assert.NotContains(t, rec.Body.String(), "/addresses/", "a reader corrects nothing")
	rec = campaignsRequest(membershipPanel(t, addressCatalog(), &fakeMemberships{}), http.MethodGet, page, nil, writer...)
	assert.NotContains(t, rec.Body.String(), "/addresses/", "a surface that cannot correct offers no form")
	rec = campaignsRequest(membershipPanel(t, addressCatalog(), &fakeMemberships{}), http.MethodPost,
		page+"/addresses/cadr_1", url.Values{"city": {"X"}}, scopeCustomerWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	rec = campaignsRequest(panel, http.MethodPost, page+"/addresses/cadr_2", url.Values{
		"read_company": {"Engines Ltd"}, "read_address_1": {"Fleet St 2"}, "read_city": {"London"},
		"read_country_code": {"GB"}, "company": {" Engines Ltd "}, "address_1": {" Fleet Street 2 "},
		"address_2": {" Floor 3 "}, "city": {" London "}, "postal_code": {" EC4 "}, "country_code": {" gb "},
		"first_name": {" Charles "}, "last_name": {" Babbage "}, "phone": {" +44 20 "},
	}, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, page+"?written=address", rec.Header().Get("Location"))
	assert.Equal(t, []string{`cus_1|cadr_2|` +
		`{"address_1":"Fleet St 2","address_2":"","city":"London","company":"Engines Ltd","country_code":"GB",` +
		`"first_name":"","last_name":"","phone":"","postal_code":""}|` +
		`{"address_1":"Fleet Street 2","address_2":"Floor 3","city":"London","company":"Engines Ltd",` +
		`"country_code":"gb","first_name":"Charles","last_name":"Babbage","phone":"+44 20","postal_code":"EC4"}`},
		reviser.corrected)
	landed := campaignsRequest(panel, http.MethodGet, rec.Header().Get("Location"), nil, scopeCustomerRead)
	assert.Contains(t, landed.Body.String(), "The address was written.")
	assert.NotContains(t, landed.Body.String(), "The contact details were written.")

	reviser.err = errors.Conflict("customer_address_revised",
		"address cadr_1 was corrected since it was read; draw the page again")
	rec = campaignsRequest(panel, http.MethodPost, page+"/addresses/cadr_1", url.Values{
		"read_city": {"Izmir"}, "first_name": {"Typed"}, "address_1": {"Typed St"}, "city": {"Ankara"},
		"country_code": {"TR"},
	}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "draw the page again")
	form = addressForm(t, body, "cadr_1")
	for _, want := range []string{
		`name="read_city" value="Istanbul"`, `name="read_first_name" value="Ada"`,
		`name="first_name" value="Typed"`, `name="address_1" value="Typed St"`, `name="city" value="Ankara"`,
		`name="last_name" value=""`,
	} {
		assert.Contains(t, form, want, "the drawn address as it is now and what was typed")
	}
	before, _, _ := strings.Cut(body, `/addresses/cadr_1"`)
	assert.True(t, strings.HasPrefix(before[strings.LastIndex(before, "<details"):], "<details open>"),
		"the refused form is open")
	assert.Equal(t, 1, strings.Count(body, "<details open>"), "and no other form is")
	assert.Contains(t, addressForm(t, body, "cadr_2"), `name="city" value="London"`, "the other address as drawn")
	assert.Contains(t, contactForm(t, body), `name="first_name" value="Ada"`, "the contact form is not the refused one")
	rec = campaignsRequest(panel, http.MethodPost, page+"/addresses/cadr_1", url.Values{"city": {"X"}}, scopeCustomerWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.NotContains(t, rec.Body.String(), "ada@example.test", "a writer who cannot read is shown none of the customer")

	reviser.err = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, page+"/addresses/cadr_1", url.Values{"city": {"X"}}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
