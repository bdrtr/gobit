package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/region/api"
	"github.com/bdrtr/gobit/internal/modules/region/service"
)

// testNow is the tests' fixed clock.
var testNow = time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

// newTestRouter builds a router with the real service and an in-memory
// repository.
func newTestRouter(t *testing.T) (chi.Router, *memRepo) {
	t.Helper()

	repo := newMemRepo()
	svc := service.New(repo, service.Options{Now: func() time.Time { return testNow }})

	r := chi.NewRouter()
	api.New(svc).Routes(r)
	return r, repo
}

// adminPrincipal is the tests' default caller: a fully privileged admin
// identity.
var adminPrincipal = corehttp.Principal{
	ID:     "user_test",
	Kind:   "user",
	Scopes: []string{corehttp.ScopeAdmin},
}

// do runs a request with a FULLY PRIVILEGED identity and returns the response.
//
// Putting the identity into the context is needed because the admin endpoints
// are guarded by corehttp.RequireScope: that middleware reads the identity from
// the context, and corehttp.RequireAdmin, which puts it there, is ABSENT in
// this test (the router is built directly). Without the identity every admin
// test in this file would get 401 before ever reaching the behavior it tests.
// What the tests verify did not change; only who the caller is was stated.
func do(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return doAs(t, r, &adminPrincipal, method, path, body)
}

// doAs runs the request with the given identity; if the identity is nil the
// request goes WITHOUT an identity.
func doAs(t *testing.T, r chi.Router, principal *corehttp.Principal, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if principal != nil {
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), *principal))
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// decodeItem decodes the data field of the single-record envelope.
func decodeItem(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var envelope struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "body: %s", rec.Body.String())
	return envelope.Data
}

// decodeList decodes the list envelope.
func decodeList(t *testing.T, rec *httptest.ResponseRecorder) (data []map[string]any, count, offset, limit int64) {
	t.Helper()

	var envelope struct {
		Data   []map[string]any `json:"data"`
		Count  int64            `json:"count"`
		Offset int64            `json:"offset"`
		Limit  int64            `json:"limit"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "body: %s", rec.Body.String())
	return envelope.Data, envelope.Count, envelope.Offset, envelope.Limit
}

// errorCode returns the code in the error envelope.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "body: %s", rec.Body.String())
	return envelope.Error.Code
}

// createRegion creates a region for the test and returns its ID.
func createRegion(t *testing.T, r chi.Router, name, currency string) string {
	t.Helper()

	rec := do(t, r, http.MethodPost, "/admin/v1/regions",
		`{"name":"`+name+`","currency_code":"`+currency+`","automatic_taxes":true,"tax_rate_bps":2000}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	id, ok := decodeItem(t, rec)["id"].(string)
	require.True(t, ok, "an ID has to be returned")
	return id
}

// TestCreateRegionReturnsCreatedEnvelope proves the status code and the
// envelope of the create response (plan Section 8).
func TestCreateRegionReturnsCreatedEnvelope(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodPost, "/admin/v1/regions",
		`{"name":"Turkey","currency_code":"try","automatic_taxes":true,"tax_rate_bps":2000}`)

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))

	data := decodeItem(t, rec)
	assert.Equal(t, "Turkey", data["name"])
	assert.Equal(t, "TRY", data["currency_code"], "the code has to be normalized to UPPER case")
	assert.Equal(t, true, data["automatic_taxes"])
	assert.InDelta(t, 2000, data["tax_rate_bps"], 0, "the rate has to come back in basis points")
	assert.Contains(t, data["id"], "reg_")
}

// TestCreateRegionRejectsInvalidInput proves that invalid input comes back
// with 422.
//
// The handler does NOT CHOOSE the status: the service returns errors.Invalid
// and corehttp turns it into 422 (plan Section 2.7).
func TestCreateRegionRejectsInvalidInput(t *testing.T) {
	r, _ := newTestRouter(t)

	cases := map[string]string{
		"invalid currency format": `{"name":"X","currency_code":"TRYX"}`,
		"undefined currency":      `{"name":"X","currency_code":"XYZ"}`,
		"empty name":              `{"name":"  ","currency_code":"TRY"}`,
		"out-of-range tax rate":   `{"name":"X","currency_code":"TRY","tax_rate_bps":10001}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := do(t, r, http.MethodPost, "/admin/v1/regions", body)

			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
			assert.NotEmpty(t, errorCode(t, rec))
		})
	}
}

// TestCreateRegionRejectsUnknownField proves that an unknown field is not
// silently ignored.
//
// Were it silently ignored, a client that misspelled the field name would
// believe the tax rate had been written.
func TestCreateRegionRejectsUnknownField(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodPost, "/admin/v1/regions",
		`{"name":"X","currency_code":"TRY","tax_rate":2000}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "region_invalid_body", errorCode(t, rec))
}

// TestCreateRegionRejectsEmptyAndDoubleBody proves that an empty body and a
// body with two JSON documents are rejected.
func TestCreateRegionRejectsEmptyAndDoubleBody(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodPost, "/admin/v1/regions", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Equal(t, "region_invalid_body", errorCode(t, rec))

	rec = do(t, r, http.MethodPost, "/admin/v1/regions",
		`{"name":"A","currency_code":"TRY"}{"name":"B","currency_code":"USD"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Equal(t, "region_invalid_body", errorCode(t, rec))
}

// TestRegionLifecycle proves reading, listing, partially updating and deleting
// a region end to end.
func TestRegionLifecycle(t *testing.T) {
	r, _ := newTestRouter(t)
	id := createRegion(t, r, "Turkey", "TRY")

	rec := do(t, r, http.MethodGet, "/admin/v1/regions/"+id, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "Turkey", decodeItem(t, rec)["name"])

	rec = do(t, r, http.MethodGet, "/admin/v1/regions", "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, count, offset, limit := decodeList(t, rec)
	assert.Len(t, data, 1)
	assert.Equal(t, int64(1), count)
	assert.Equal(t, int64(0), offset)
	assert.Equal(t, int64(service.DefaultLimit), limit, "the applied limit has to come back in the envelope")

	// Only the name is sent; the currency and the rate must NOT CHANGE.
	rec = do(t, r, http.MethodPut, "/admin/v1/regions/"+id, `{"name":"Turkey Region"}`)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	updated := decodeItem(t, rec)
	assert.Equal(t, "Turkey Region", updated["name"])
	assert.Equal(t, "TRY", updated["currency_code"], "a field that is not given must not change")
	assert.InDelta(t, 2000, updated["tax_rate_bps"], 0, "a field that is not given must not change")

	rec = do(t, r, http.MethodPut, "/admin/v1/regions/"+id, `{}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "an empty patch has to be rejected")

	rec = do(t, r, http.MethodDelete, "/admin/v1/regions/"+id, "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String(), "a 204 has to have no body")

	rec = do(t, r, http.MethodGet, "/admin/v1/regions/"+id, "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "a deleted region must not be readable")
}

// TestGetRegionWrongPrefixIsUnprocessable proves that an ID of the wrong kind
// returns 422, not 404.
func TestGetRegionWrongPrefixIsUnprocessable(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodGet, "/admin/v1/regions/prod_01", "")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Equal(t, "region_invalid_input", errorCode(t, rec))
}

// TestCountryUniquenessIsConflict proves that adding the same country to a
// second region returns 409.
func TestCountryUniquenessIsConflict(t *testing.T) {
	r, _ := newTestRouter(t)
	first := createRegion(t, r, "Turkey", "TRY")
	second := createRegion(t, r, "Europe", "USD")

	rec := do(t, r, http.MethodPost, "/admin/v1/regions/"+first+"/countries", `{"country_code":"tr"}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	country := decodeItem(t, rec)
	assert.Equal(t, "TR", country["code"], "the country code has to be normalized to UPPER case")
	assert.Equal(t, first, country["region_id"])

	rec = do(t, r, http.MethodPost, "/admin/v1/regions/"+second+"/countries", `{"country_code":"TR"}`)
	assert.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "country_already_in_region", errorCode(t, rec))
}

// TestRegionCountryListAndRemoval proves a region's country list and removing
// a country from it.
func TestRegionCountryListAndRemoval(t *testing.T) {
	r, _ := newTestRouter(t)
	id := createRegion(t, r, "Europe", "USD")

	for _, code := range []string{"DE", "TR"} {
		rec := do(t, r, http.MethodPost, "/admin/v1/regions/"+id+"/countries",
			`{"country_code":"`+code+`"}`)
		require.Equal(t, http.StatusCreated, rec.Code)
	}

	rec := do(t, r, http.MethodGet, "/admin/v1/regions/"+id+"/countries", "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, count, _, _ := decodeList(t, rec)
	require.Len(t, data, 2)
	assert.Equal(t, int64(2), count)
	assert.Equal(t, "DE", data[0]["code"])

	rec = do(t, r, http.MethodDelete, "/admin/v1/regions/"+id+"/countries/de", "")
	require.Equal(t, http.StatusNoContent, rec.Code)

	rec = do(t, r, http.MethodDelete, "/admin/v1/regions/"+id+"/countries/DE", "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "a second removal has to return not found")

	rec = do(t, r, http.MethodGet, "/admin/v1/regions/"+id+"/countries", "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, _, _, _ = decodeList(t, rec)
	require.Len(t, data, 1)
	assert.Equal(t, "TR", data[0]["code"])
}

// TestListCountriesFilter proves the region filter of the country list.
func TestListCountriesFilter(t *testing.T) {
	r, _ := newTestRouter(t)
	id := createRegion(t, r, "Turkey", "TRY")
	rec := do(t, r, http.MethodPost, "/admin/v1/regions/"+id+"/countries", `{"country_code":"TR"}`)
	require.Equal(t, http.StatusCreated, rec.Code)

	rec = do(t, r, http.MethodGet, "/admin/v1/countries", "")
	require.Equal(t, http.StatusOK, rec.Code)
	_, count, _, _ := decodeList(t, rec)
	assert.Equal(t, int64(3), count, "a request without the filter has to count every country")

	rec = do(t, r, http.MethodGet, "/admin/v1/countries?region_id="+id, "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, count, _, _ := decodeList(t, rec)
	require.Len(t, data, 1)
	assert.Equal(t, int64(1), count)
	assert.Equal(t, "TR", data[0]["code"])

	// An empty region_id is NOT "no filter"; it is a client error.
	rec = do(t, r, http.MethodGet, "/admin/v1/countries?region_id=", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestCurrencyEndpointsAreReadOnly proves the read surface of the currency
// endpoints and their decimal digits field.
func TestCurrencyEndpointsAreReadOnly(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodGet, "/admin/v1/currencies", "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, count, _, _ := decodeList(t, rec)
	assert.Equal(t, int64(3), count)
	require.NotEmpty(t, data)

	rec = do(t, r, http.MethodGet, "/admin/v1/currencies/jpy", "")
	require.Equal(t, http.StatusOK, rec.Code)
	currency := decodeItem(t, rec)
	assert.Equal(t, "JPY", currency["code"])
	assert.InDelta(t, 0, currency["decimal_digits"], 0, "JPY has no decimals")

	rec = do(t, r, http.MethodGet, "/admin/v1/currencies/XYZ", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// Reference data has NO write surface; the route is not registered at all.
	rec = do(t, r, http.MethodPost, "/admin/v1/currencies", `{"code":"XYZ"}`)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code,
		"the currency write surface is deliberately absent")
}

// TestStoreRegionsExposeCurrencyScale proves that the storefront endpoint
// returns the currency's DECIMAL DIGITS.
//
// Amounts are minor-unit integers; a client that does not learn the divisor
// from the same response assumes a fixed 100 and shows yen amounts a hundred
// times too small.
func TestStoreRegionsExposeCurrencyScale(t *testing.T) {
	r, _ := newTestRouter(t)
	jpID := createRegion(t, r, "Japan", "JPY")
	rec := do(t, r, http.MethodPost, "/admin/v1/regions/"+jpID+"/countries", `{"country_code":"JP"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	createRegion(t, r, "Turkey", "TRY")

	rec = do(t, r, http.MethodGet, "/store/v1/regions", "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	data, count, _, _ := decodeList(t, rec)
	require.Len(t, data, 2)
	assert.Equal(t, int64(2), count)

	byID := map[string]map[string]any{}
	for _, item := range data {
		id, ok := item["id"].(string)
		require.True(t, ok)
		byID[id] = item
	}

	jp := byID[jpID]
	currency, ok := jp["currency"].(map[string]any)
	require.True(t, ok, "there has to be a currency body")
	assert.Equal(t, "JPY", currency["code"])
	assert.Equal(t, "¥", currency["symbol"])
	assert.InDelta(t, 0, currency["decimal_digits"], 0)

	countries, ok := jp["countries"].([]any)
	require.True(t, ok, "the countries have to be in the body")
	require.Len(t, countries, 1)

	// The tax configuration does NOT go to the CUSTOMER.
	assert.NotContains(t, jp, "tax_rate_bps")
	assert.NotContains(t, jp, "automatic_taxes")
}

// TestStoreRegionEmptyCountriesIsArray proves that a region with no countries
// returns an empty array, not null.
func TestStoreRegionEmptyCountriesIsArray(t *testing.T) {
	r, _ := newTestRouter(t)
	id := createRegion(t, r, "Turkey", "TRY")

	rec := do(t, r, http.MethodGet, "/store/v1/regions/"+id, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"countries":[]`,
		"an empty list has to be [], not null")
}

// TestStoreRegionNotFound proves that a missing region returns 404.
func TestStoreRegionNotFound(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodGet, "/store/v1/regions/reg_MISSING", "")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "region_not_found", errorCode(t, rec))
}

// TestPagingParamsMustBeIntegers proves that a paging parameter that cannot be
// converted to a number does not silently fall back to the first page.
func TestPagingParamsMustBeIntegers(t *testing.T) {
	r, _ := newTestRouter(t)

	for _, path := range []string{
		"/admin/v1/regions?limit=abc",
		"/admin/v1/regions?offset=abc",
		"/store/v1/regions?limit=x",
	} {
		rec := do(t, r, http.MethodGet, path, "")

		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "path: %s", path)
		assert.Equal(t, "region_invalid_body", errorCode(t, rec), "path: %s", path)
	}
}

// TestNarrowScopeDoesNotOpenWriteEndpoints proves that an identity carrying
// only [api.ScopeRead] cannot pass the admin WRITE endpoints.
//
// The scenario under test is concrete: an admin identity signed in with the
// read scope could, without scope enforcement, delete regions with
// DELETE /admin/v1/regions/{id}, or move a country to another region and so
// change the currency and tax rate of every order that comes from that
// country.
func TestNarrowScopeDoesNotOpenWriteEndpoints(t *testing.T) {
	r, _ := newTestRouter(t)
	id := createRegion(t, r, "Turkey", "TRY")
	narrowPrincipal := corehttp.Principal{ID: "user_narrow", Kind: "user", Scopes: []string{api.ScopeRead}}

	for _, endpoint := range []struct{ method, path, body string }{
		{http.MethodPost, "/admin/v1/regions", `{"name":"Europe","currency_code":"USD"}`},
		{http.MethodPut, "/admin/v1/regions/" + id, `{"name":"New"}`},
		{http.MethodDelete, "/admin/v1/regions/" + id, ""},
		{http.MethodPost, "/admin/v1/regions/" + id + "/countries", `{"country_code":"tr"}`},
		{http.MethodDelete, "/admin/v1/regions/" + id + "/countries/tr", ""},
	} {
		rec := doAs(t, r, &narrowPrincipal, endpoint.method, endpoint.path, endpoint.body)
		assert.Equal(t, http.StatusForbidden, rec.Code,
			"%s %s must not open with the read scope: %s", endpoint.method, endpoint.path, rec.Body.String())
	}

	// The rejected requests must really have WRITTEN NOTHING: the region has to
	// still be in place, under its old name. Looking at the status code alone
	// would miss a fault in which the middleware runs AFTER the handler.
	rec := do(t, r, http.MethodGet, "/admin/v1/regions/"+id, "")
	require.Equal(t, http.StatusOK, rec.Code, "the region must not have been deleted")
	assert.Equal(t, "Turkey", decodeItem(t, rec)["name"], "the region must not have been updated")
}

// TestNarrowScopePassesOnReadEndpoints proves that the same narrow identity
// DOES PASS the admin READ endpoints.
//
// The value of scope enforcement is that it really accepts the narrow scope
// too: if it only ever rejected, nobody would hand out narrow scopes and
// everybody would be given admin.
func TestNarrowScopePassesOnReadEndpoints(t *testing.T) {
	r, _ := newTestRouter(t)
	id := createRegion(t, r, "Turkey", "TRY")
	narrowPrincipal := corehttp.Principal{ID: "user_narrow", Kind: "user", Scopes: []string{api.ScopeRead}}

	for _, path := range []string{
		"/admin/v1/regions",
		"/admin/v1/regions/" + id,
		"/admin/v1/regions/" + id + "/countries",
		"/admin/v1/countries",
		"/admin/v1/currencies",
	} {
		rec := doAs(t, r, &narrowPrincipal, http.MethodGet, path, "")
		assert.Equal(t, http.StatusOK, rec.Code, "GET %s: %s", path, rec.Body.String())
	}
}

// TestAdminRequestWithoutPrincipalReturns401 proves that a request with no
// identity at all gets 401.
//
// The distinction is deliberate: 401 means "tell me who you are", 403 means "I
// know who you are, but you lack the scope". Were the two mixed up, the client
// would try refreshing its session for a problem that renewing its identity
// will not solve.
func TestAdminRequestWithoutPrincipalReturns401(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := doAs(t, r, nil, http.MethodGet, "/admin/v1/regions", "")

	assert.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
}

// TestStoreEndpointsRequireNoScope proves that NO scope was added to the store
// surface.
//
// The storefront surface's identity is the publishable key, and that key by
// definition carries no scope; if a scope is ever attached to the store
// endpoints by mistake, the storefront stops working entirely, and this test
// catches that at once.
func TestStoreEndpointsRequireNoScope(t *testing.T) {
	r, _ := newTestRouter(t)
	id := createRegion(t, r, "Turkey", "TRY")

	for _, path := range []string{"/store/v1/regions", "/store/v1/regions/" + id} {
		rec := doAs(t, r, nil, http.MethodGet, path, "")
		assert.Equal(t, http.StatusOK, rec.Code, "GET %s: %s", path, rec.Body.String())
	}
}
