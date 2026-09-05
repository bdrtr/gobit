package adminui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// customerRouter mounts the customer routes so chi fills the URL parameters.
func customerRouter(panel *UI) chi.Router {
	r := chi.NewRouter()
	r.Get(CustomersPath, panel.listCustomers)
	r.Get(CustomerPath, panel.showCustomer)

	return r
}

// getCustomerPage sends a GET as a signed-in operator.
func getCustomerPage(panel *UI, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	request = request.WithContext(corehttp.WithPrincipal(request.Context(),
		corehttp.Principal{ID: "user_1", Kind: "user"}))

	rec := httptest.NewRecorder()
	customerRouter(panel).ServeHTTP(rec, request)

	return rec
}

// TestCustomerListTellsAccountsFromGuests is the distinction this screen exists
// to make.
//
// A shop's guest records outnumber its accounts, and an operator looking for a
// person needs to know which kind of record they are reading before anything
// else on the row means what they think it means.
func TestCustomerListTellsAccountsFromGuests(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityCustomer: {
			{
				"id": "cus_1", "email": "ada@example.test",
				"first_name": "Ada", "last_name": "Lovelace",
				"has_account": true,
				"created_at":  time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC),
			},
			{
				"id": "cus_2", "email": "guest@example.test",
				"has_account": false,
				"created_at":  time.Date(2026, 8, 2, 9, 0, 0, 0, time.UTC),
			},
		},
	}}

	rec := getCustomerPage(newCatalogPanel(t, catalog), CustomersPath)

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "Ada Lovelace", "the two name fields are joined for display")
	assert.Contains(t, body, "registered")
	assert.Contains(t, body, "guest")
	assert.Contains(t, body, "no name", "a record with no name says so rather than showing a blank")
}

// TestACustomerWithoutANameStillHasATitle keeps the detail page identifiable.
//
// A guest record created from a checkout that carried neither a name nor an
// address is a real record. A blank page title would leave the operator unable
// to tell one open tab from another.
func TestACustomerWithoutANameStillHasATitle(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityCustomer: {{"id": "cus_nameless", "has_account": false}},
	}}

	rec := getCustomerPage(newCatalogPanel(t, catalog), CustomersPath+"/cus_nameless")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "cus_nameless",
		"the identifier is the last resort when there is no name and no address")
}

// TestACustomerPageFallsBackToTheEmail covers the middle case of the same rule.
func TestACustomerPageFallsBackToTheEmail(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityCustomer: {{"id": "cus_3", "email": "only@example.test"}},
	}}

	rec := getCustomerPage(newCatalogPanel(t, catalog), CustomersPath+"/cus_3")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "<title>only@example.test",
		"an unnamed customer with an address is called by the address")
}

// TestAMissingCustomerIsNotFound covers the empty read.
func TestAMissingCustomerIsNotFound(t *testing.T) {
	t.Parallel()

	rec := getCustomerPage(newCatalogPanel(t, &fakeCatalog{}), CustomersPath+"/cus_missing")

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestTheMenuCarriesEverySection keeps the list the frame is built from in step
// with the routes.
//
// A section that has a route and no menu entry is one an operator can only
// reach by typing the address.
func TestTheMenuCarriesEverySection(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{}}
	body := getCustomerPage(newCatalogPanel(t, catalog), CustomersPath).Body.String()

	for _, path := range []string{ProductsPath, OrdersPath, CustomersPath} {
		assert.Contains(t, body, `href="`+path+`"`, "the menu has to carry %s", path)
	}

	assert.Contains(t, body, `href="`+CustomersPath+`" aria-current="page"`)
}
