//go:build integration

package e2e

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
	customersvc "github.com/bdrtr/gobit/internal/modules/customer/service"
)

// TestAnOperatorCorrectsACustomersAddressInThePanel is ADR 0342 on the
// production wiring: a customer's page carries each address's printed fields
// as read through the customer entity; the form corrects one through the
// registered `customer.admin` surface and keeps its default flag; and the
// same form sent again, now stale, is refused on the page with the address
// as it is now.
func TestAnOperatorCorrectsACustomersAddressInThePanel(t *testing.T) {
	ctx := t.Context()
	email := fmt.Sprintf("e2e-address-%d@example.com", fixtureCounter.Add(1))
	created, err := customerSvc.CreateCustomer(ctx, customersvc.CustomerInput{Email: email, FirstName: "Ada"})
	require.NoError(t, err)
	address, err := customerSvc.CreateAddress(ctx, created.ID, customersvc.AddressInput{
		FirstName: "Ada", Address1: "Bagdat Cd. 1", City: "Istanbul", CountryCode: "TR", PostalCode: "34710",
		IsDefaultShipping: true,
	})
	require.NoError(t, err)
	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_support", Kind: "user", Scopes: []string{"customer:read", "customer:write"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	pagePath := adminui.CustomersPath + "/" + created.ID
	action := pagePath + "/addresses/" + address.ID
	keys := []string{
		"first_name", "last_name", "company", "address_1", "address_2", "city", "country_code", "postal_code", "phone",
	}
	// drawn is the address the page's form was drawn with.
	drawn := func(page string) url.Values {
		t.Helper()

		_, form, found := strings.Cut(page, `action="`+action+`"`)
		require.True(t, found, "the page offers the address's form")
		form, _, _ = strings.Cut(form, "</form>")
		values := url.Values{}
		for _, key := range keys {
			_, value, ok := strings.Cut(form, `name="read_`+key+`" value="`)
			require.True(t, ok, key)
			value, _, _ = strings.Cut(value, `"`)
			values.Set("read_"+key, html.UnescapeString(value))
		}

		return values
	}

	form := drawn(send(http.MethodGet, pagePath, nil).Body.String())
	assert.Equal(t, "Ada|Bagdat Cd. 1|Istanbul|TR|34710",
		strings.Join([]string{form.Get("read_first_name"), form.Get("read_address_1"), form.Get("read_city"),
			form.Get("read_country_code"), form.Get("read_postal_code")}, "|"))
	for key, value := range map[string]string{
		"first_name": "Ada", "last_name": "Lovelace", "address_1": "Bagdat Cd. 10", "address_2": "D:4",
		"city": "Istanbul", "country_code": "tr", "postal_code": "34728", "phone": "+90 555 0000",
	} {
		form.Set(key, value)
	}
	corrected := send(http.MethodPost, action, form)
	require.Equal(t, http.StatusSeeOther, corrected.Code, corrected.Body.String())
	assert.Contains(t, send(http.MethodGet, corrected.Header().Get("Location"), nil).Body.String(),
		"The address was written.")
	stored, err := customerSvc.GetAddress(ctx, created.ID, address.ID)
	require.NoError(t, err)
	assert.Equal(t, "Ada|Lovelace|Bagdat Cd. 10|D:4|Istanbul|TR|34728|+90 555 0000",
		strings.Join([]string{stored.FirstName, stored.LastName, stored.Address1, stored.Address2, stored.City,
			stored.CountryCode, stored.PostalCode, stored.Phone}, "|"),
		"the printed fields corrected, the country upper-cased")
	assert.True(t, stored.IsDefaultShipping, "the default flag kept")

	stale := send(http.MethodPost, action, form)
	require.Equal(t, http.StatusUnprocessableEntity, stale.Code, stale.Body.String())
	assert.Contains(t, stale.Body.String(), "draw the page again")
	assert.Equal(t, "Bagdat Cd. 10", drawn(stale.Body.String()).Get("read_address_1"),
		"the page is drawn from the address as it is now")
}
