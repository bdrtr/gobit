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

// TestAnOperatorCorrectsACustomersContactInThePanel is ADR 0337 on the
// production wiring: a customer's page carries their name and phone as read
// through the customer entity; the form corrects them through the
// registered `customer.admin` surface and keeps the e-mail; and the same
// form sent again, now stale, is refused on the page with the customer as
// they are now.
func TestAnOperatorCorrectsACustomersContactInThePanel(t *testing.T) {
	ctx := t.Context()
	email := fmt.Sprintf("e2e-contact-%d@example.com", fixtureCounter.Add(1))
	created, err := customerSvc.CreateCustomer(ctx, customersvc.CustomerInput{Email: email, FirstName: "Ada", Phone: "555"})
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
	// drawn is the contact the page's form was drawn with.
	drawn := func(page string) url.Values {
		t.Helper()

		_, form, found := strings.Cut(page, `action="`+pagePath+`/contact"`)
		require.True(t, found, "the page offers the contact form")
		form, _, _ = strings.Cut(form, "</form>")
		values := url.Values{}
		for _, field := range []string{"read_first_name", "read_last_name", "read_phone"} {
			_, value, ok := strings.Cut(form, `name="`+field+`" value="`)
			require.True(t, ok, field)
			value, _, _ = strings.Cut(value, `"`)
			values.Set(field, html.UnescapeString(value))
		}

		return values
	}

	form := drawn(send(http.MethodGet, pagePath, nil).Body.String())
	assert.Equal(t, url.Values{"read_first_name": {"Ada"}, "read_last_name": {""}, "read_phone": {"555"}}, form)
	form.Set("first_name", "Ada")
	form.Set("last_name", "Lovelace")
	form.Set("phone", "+90 555 0000")
	corrected := send(http.MethodPost, pagePath+"/contact", form)
	require.Equal(t, http.StatusSeeOther, corrected.Code, corrected.Body.String())
	assert.Contains(t, send(http.MethodGet, corrected.Header().Get("Location"), nil).Body.String(),
		"The contact details were written.")
	stored, err := customerSvc.GetCustomer(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, "Ada|Lovelace|+90 555 0000|"+email, stored.FirstName+"|"+stored.LastName+"|"+stored.Phone+"|"+stored.Email,
		"the name and the phone corrected, the e-mail kept")

	stale := send(http.MethodPost, pagePath+"/contact", form)
	require.Equal(t, http.StatusUnprocessableEntity, stale.Code, stale.Body.String())
	assert.Contains(t, stale.Body.String(), "draw the page again")
	assert.Equal(t, "Lovelace", drawn(stale.Body.String()).Get("read_last_name"), "the page is drawn from the customer as they are now")
}
