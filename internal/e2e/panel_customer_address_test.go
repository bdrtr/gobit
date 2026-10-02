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

	"github.com/bdrtr/gobit/core/errors"
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

// TestAnOperatorAddsACustomersAddressInThePanel is ADR 0359 on the
// production wiring: a customer's page adds an address through the
// registered `customer.admin` surface, as the default shipping address when
// ticked, and a second one made the default takes the flag from the first.
func TestAnOperatorAddsACustomersAddressInThePanel(t *testing.T) {
	ctx := t.Context()
	created, err := customerSvc.CreateCustomer(ctx, customersvc.CustomerInput{
		Email: fmt.Sprintf("e2e-address-added-%d@example.com", fixtureCounter.Add(1)),
	})
	require.NoError(t, err)
	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	add := func(form url.Values) {
		t.Helper()

		req := httptest.NewRequest(http.MethodPost, adminui.CustomersPath+"/"+created.ID+"/addresses",
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_support", Kind: "user", Scopes: []string{"customer:read", "customer:write"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	}

	add(url.Values{"first_name": {"Ada"}, "address_1": {"1 First St"}, "city": {"Izmir"}, "country_code": {"tr"},
		"default_shipping": {"1"}})
	add(url.Values{"address_1": {"2 Second St"}, "city": {"Ankara"}, "country_code": {"TR"}, "default_shipping": {"1"}})
	addresses, err := customerSvc.ListAddresses(ctx, created.ID)
	require.NoError(t, err)
	require.Len(t, addresses, 2)
	byLine := map[string]bool{}
	for _, address := range addresses {
		byLine[address.Address1+"|"+address.CountryCode] = address.IsDefaultShipping
	}
	assert.Equal(t, map[string]bool{"1 First St|TR": false, "2 Second St|TR": true}, byLine,
		"the second default takes the flag from the first, the country upper-cased")
}

// TestAnOperatorMovesADefaultAndRemovesAnAddressInThePanel is ADR 0360 on
// the production wiring: an address's row on a customer's page makes it the
// default shipping address, taking the flag from the address that held it,
// and another's row removes it.
func TestAnOperatorMovesADefaultAndRemovesAnAddressInThePanel(t *testing.T) {
	ctx := t.Context()
	created, err := customerSvc.CreateCustomer(ctx, customersvc.CustomerInput{
		Email: fmt.Sprintf("e2e-address-acts-%d@example.com", fixtureCounter.Add(1)),
	})
	require.NoError(t, err)
	first, err := customerSvc.CreateAddress(ctx, created.ID, customersvc.AddressInput{
		Address1: "1 First St", City: "Izmir", CountryCode: "TR", IsDefaultShipping: true,
	})
	require.NoError(t, err)
	second, err := customerSvc.CreateAddress(ctx, created.ID, customersvc.AddressInput{
		Address1: "2 Second St", City: "Ankara", CountryCode: "TR",
	})
	require.NoError(t, err)
	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	send := func(path string, form url.Values) {
		t.Helper()

		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_support", Kind: "user", Scopes: []string{"customer:read", "customer:write"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	}
	addresses := adminui.CustomersPath + "/" + created.ID + "/addresses/"

	send(addresses+second.ID+"/default", url.Values{"kind": {"shipping"}})
	moved, err := customerSvc.GetAddress(ctx, created.ID, second.ID)
	require.NoError(t, err)
	assert.True(t, moved.IsDefaultShipping, "the second is the default now")
	left, err := customerSvc.GetAddress(ctx, created.ID, first.ID)
	require.NoError(t, err)
	assert.False(t, left.IsDefaultShipping, "and the first is not")

	send(addresses+first.ID+"/remove", nil)
	_, err = customerSvc.GetAddress(ctx, created.ID, first.ID)
	assert.True(t, errors.IsNotFound(err), "the removed address is found no more: %v", err)
}
