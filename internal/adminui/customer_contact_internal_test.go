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

// fakeContactReviser puts customers into groups as fakeMemberships does and
// corrects their contact, recording each correction.
type fakeContactReviser struct {
	fakeMemberships
	corrected []string
	err       error
}

func (f *fakeContactReviser) ReviseCustomerContact(_ context.Context, id string, read, next json.RawMessage) error {
	f.corrected = append(f.corrected, id+"|"+string(read)+"|"+string(next))
	return f.err
}

// contactCatalog holds a customer with a name and a phone.
func contactCatalog() *fakeCatalog {
	return &fakeCatalog{byEntity: map[string][]query.Record{
		EntityCustomer: {{
			fieldID: "cus_1", "email": "ada@example.test", fieldFirstName: "Ada", fieldLastName: "Byron",
			fieldPhone: "555", fieldCustomerGroupIDs: []string{},
		}},
	}}
}

// contactForm is the customer page's contact form.
func contactForm(t *testing.T, body string) string {
	t.Helper()

	_, form, found := strings.Cut(body, `action="`+CustomersPath+`/cus_1/contact"`)
	require.True(t, found, "the page offers the contact form")
	form, _, _ = strings.Cut(form, "</form>")

	return form
}

// TestACustomersContactIsCorrectedOnTheirPage is ADR 0337: a writer whose
// surface can correct is offered the form drawn from the name and phone the
// page was read with; the surface is asked to correct them from those, the
// typed ones trimmed, and the page says so; a refusal, a customer corrected
// since included, comes back on the page with what was typed and the drawn
// ones as they are now; a reader is offered nothing.
func TestACustomersContactIsCorrectedOnTheirPage(t *testing.T) {
	t.Parallel()

	reviser := &fakeContactReviser{}
	panel := membershipPanel(t, contactCatalog(), reviser)
	page := CustomersPath + "/cus_1"
	writer := []string{scopeCustomerRead, scopeCustomerWrite}

	rec := campaignsRequest(panel, http.MethodGet, page, nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	form := contactForm(t, rec.Body.String())
	for _, want := range []string{
		`name="read_first_name" value="Ada"`, `name="read_last_name" value="Byron"`, `name="read_phone" value="555"`,
		`name="first_name" value="Ada"`, `name="last_name" value="Byron"`, `name="phone" value="555"`,
	} {
		assert.Contains(t, form, want)
	}
	assert.NotContains(t, rec.Body.String(), "<details open>", "nothing refused, nothing open")
	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopeCustomerRead)
	assert.NotContains(t, rec.Body.String(), "/contact", "a reader corrects nothing")
	rec = campaignsRequest(membershipPanel(t, contactCatalog(), &fakeMemberships{}), http.MethodGet, page, nil, writer...)
	assert.NotContains(t, rec.Body.String(), "/contact", "a surface that cannot correct offers no form")
	rec = campaignsRequest(membershipPanel(t, contactCatalog(), &fakeMemberships{}), http.MethodPost, page+"/contact",
		url.Values{formFirstName: {"X"}}, scopeCustomerWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	rec = campaignsRequest(panel, http.MethodPost, page+"/contact", url.Values{
		formReadFirstName: {"Ada"}, formReadLastName: {"Byron"}, formReadPhone: {"555"},
		formFirstName: {" Ada "}, formLastName: {" Lovelace "}, formPhone: {" +90 555 0000 "},
	}, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, page+"?written=1", rec.Header().Get("Location"))
	assert.Equal(t, []string{`cus_1|{"first_name":"Ada","last_name":"Byron","phone":"555"}|` +
		`{"first_name":"Ada","last_name":"Lovelace","phone":"+90 555 0000"}`}, reviser.corrected)
	landed := campaignsRequest(panel, http.MethodGet, rec.Header().Get("Location"), nil, scopeCustomerRead)
	assert.Contains(t, landed.Body.String(), "The contact details were written.")

	reviser.err = errors.Conflict("customer_contact_revised",
		`customer cus_1 was corrected since it was read: the name is "Ada Byron" and the phone "555" now; draw the page again`)
	rec = campaignsRequest(panel, http.MethodPost, page+"/contact", url.Values{
		formReadFirstName: {"Augusta"}, formFirstName: {"Typed"}, formLastName: {"Name"}, formPhone: {"999"},
	}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "draw the page again")
	form = contactForm(t, body)
	for _, want := range []string{
		`name="read_first_name" value="Ada"`, `name="first_name" value="Typed"`, `name="last_name" value="Name"`,
		`name="phone" value="999"`,
	} {
		assert.Contains(t, form, want, "the drawn contact as it is now and what was typed")
	}
	assert.Contains(t, body, "<details open>", "the refused form is open")
	rec = campaignsRequest(panel, http.MethodPost, page+"/contact", url.Values{formFirstName: {"X"}}, scopeCustomerWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.NotContains(t, rec.Body.String(), "ada@example.test", "a writer who cannot read is shown none of the customer")

	reviser.err = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, page+"/contact", url.Values{formFirstName: {"X"}}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
