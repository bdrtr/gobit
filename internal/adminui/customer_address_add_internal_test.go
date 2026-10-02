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
)

// fakeAddressAdder corrects addresses as fakeAddressReviser does and adds
// them, recording each one added.
type fakeAddressAdder struct {
	fakeAddressReviser
	added  []string
	addErr error
}

func (f *fakeAddressAdder) AddCustomerAddress(
	_ context.Context, customerID string, address json.RawMessage, defaultShipping, defaultBilling bool,
) (string, error) {
	f.added = append(f.added, fmt.Sprintf("%s|%s|%t|%t", customerID, address, defaultShipping, defaultBilling))
	return "cadr_9", f.addErr
}

// newAddressForm is the customer page's form that adds an address.
func newAddressForm(t *testing.T, body string) string {
	t.Helper()

	_, form, found := strings.Cut(body, "<summary>Add an address</summary>")
	require.True(t, found, "the page offers the form")
	form, _, _ = strings.Cut(form, "</details>")

	return form
}

// TestAnAddressIsAddedOnTheCustomersPage is ADR 0359: a writer whose surface
// can add is offered the form, empty, a reader is not; the surface is asked
// to add the address typed, trimmed, as the default shipping or billing
// address when ticked, and the page says so; a refusal comes back with what
// was typed in the form alone, open.
func TestAnAddressIsAddedOnTheCustomersPage(t *testing.T) {
	t.Parallel()

	adder := &fakeAddressAdder{}
	panel := membershipPanel(t, addressCatalog(), adder)
	page := CustomersPath + "/cus_1"
	writer := []string{scopeCustomerRead, scopeCustomerWrite}

	rec := campaignsRequest(panel, http.MethodGet, page, nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	form := newAddressForm(t, rec.Body.String())
	assert.Contains(t, form, `action="`+page+`/addresses"`)
	assert.Contains(t, form, `name="address_1" value=""`)
	assert.NotContains(t, form, " checked")
	assert.NotContains(t, rec.Body.String(), "<details open>", "nothing refused, nothing open")
	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopeCustomerRead)
	assert.NotContains(t, rec.Body.String(), "Add an address", "a reader adds nothing")

	rec = campaignsRequest(panel, http.MethodPost, page+"/addresses", url.Values{
		"first_name": {" Ada "}, "address_1": {" 1 New St "}, "city": {" Izmir "}, "country_code": {" tr "},
		formDefaultShipping: {"1"},
	}, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, page+"?written=address", rec.Header().Get("Location"))
	rec = campaignsRequest(panel, http.MethodPost, page+"/addresses", url.Values{
		"address_1": {"2 Other St"}, "city": {"Ankara"}, "country_code": {"TR"}, formDefaultBilling: {"1"},
	}, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, []string{
		`cus_1|{"address_1":"1 New St","address_2":"","city":"Izmir","company":"","country_code":"tr",` +
			`"first_name":"Ada","last_name":"","phone":"","postal_code":""}|true|false`,
		`cus_1|{"address_1":"2 Other St","address_2":"","city":"Ankara","company":"","country_code":"TR",` +
			`"first_name":"","last_name":"","phone":"","postal_code":""}|false|true`,
	}, adder.added, "trimmed, the defaults as ticked")

	adder.addErr = errors.Invalid("customer_invalid_input", "the address's first line is required")
	rec = campaignsRequest(panel, http.MethodPost, page+"/addresses", url.Values{
		"first_name": {"Typed"}, "city": {"Izmir"}, "country_code": {"TR"}, formDefaultBilling: {"1"},
	}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "the address&#39;s first line is required")
	form = newAddressForm(t, body)
	assert.Contains(t, form, `name="first_name" value="Typed"`)
	assert.Contains(t, form, `name="city" value="Izmir"`)
	assert.Contains(t, form, `name="default_billing" value="1" checked`)
	assert.NotContains(t, form, `name="default_shipping" value="1" checked`)
	assert.Equal(t, 1, strings.Count(body, "<details open>"), "the new address's form alone is open")
	assert.Contains(t, body, "<details open>\n  <summary>Add an address</summary>")
	assert.Contains(t, contactForm(t, body), `name="first_name" value="Ada"`, "the contact form is not the refused one")

	adder.addErr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, page+"/addresses", url.Values{"city": {"X"}}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	rec = campaignsRequest(panel, http.MethodPost, page+"/addresses", url.Values{"city": {"X"}}, scopeCustomerRead)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	plain := membershipPanel(t, addressCatalog(), &fakeAddressReviser{})
	assert.NotContains(t, campaignsRequest(plain, http.MethodGet, page, nil, writer...).Body.String(), "Add an address")
	assert.Equal(t, http.StatusServiceUnavailable,
		campaignsRequest(plain, http.MethodPost, page+"/addresses", url.Values{"city": {"X"}}, writer...).Code)
}
