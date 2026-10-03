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
	"github.com/bdrtr/gobit/internal/modules/tax/api"
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

// testNow is the tests' fixed clock.
var testNow = time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)

// adminPrincipal is the tests' default caller: a fully privileged admin.
//
// The admin endpoints are guarded by corehttp.RequireScope, and that middleware
// returns 401 when there is NO identity in the context. These tests build the
// router directly, so corehttp.RequireAdmin, which places the identity in the
// chain, is absent; the test therefore puts the identity there itself. The
// only thing added is the IDENTITY — the behavior the tests verify (status
// codes, envelopes, error codes) did not change.
var adminPrincipal = corehttp.Principal{
	ID:     "usr_test",
	Kind:   "user",
	Scopes: []string{corehttp.ScopeAdmin},
}

// readOnlyPrincipal is a narrowly scoped caller that carries only
// [api.ScopeRead].
var readOnlyPrincipal = corehttp.Principal{
	ID:     "usr_narrow",
	Kind:   "user",
	Scopes: []string{api.ScopeRead},
}

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

// do runs a request with a fully privileged identity and returns the response.
func do(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	return doAs(t, r, adminPrincipal, method, path, body)
}

// doAs runs a request with the given identity and returns the response.
func doAs(t *testing.T, r chi.Router, principal corehttp.Principal, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), principal))

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

// createRegion creates a root tax region and returns its ID.
func createRegion(t *testing.T, r chi.Router, countryCode string) string {
	t.Helper()

	rec := do(t, r, http.MethodPost, "/admin/v1/tax-regions",
		`{"country_code":"`+countryCode+`"}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	id, ok := decodeItem(t, rec)["id"].(string)
	require.True(t, ok)
	return id
}

// createRate creates a tax rate and returns its ID.
func createRate(t *testing.T, r chi.Router, body string) string {
	t.Helper()

	rec := do(t, r, http.MethodPost, "/admin/v1/tax-rates", body)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	id, ok := decodeItem(t, rec)["id"].(string)
	require.True(t, ok)
	return id
}

// TestRegionLifecycle checks the end-to-end flow of the region endpoints.
func TestRegionLifecycle(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodPost, "/admin/v1/tax-regions",
		`{"country_code":"tr","metadata":{"source":"test"}}`)
	require.Equal(t, http.StatusCreated, rec.Code)

	created := decodeItem(t, rec)
	assert.Equal(t, "TR", created["country_code"])
	assert.Nil(t, created["province_code"], "a root region's province code has to be null")
	assert.Nil(t, created["parent_id"])
	id, _ := created["id"].(string)

	rec = do(t, r, http.MethodGet, "/admin/v1/tax-regions/"+id, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "TR", decodeItem(t, rec)["country_code"])

	rec = do(t, r, http.MethodDelete, "/admin/v1/tax-regions/"+id, "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String(), "a 204 has to have no body")

	rec = do(t, r, http.MethodGet, "/admin/v1/tax-regions/"+id, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestRegionListEnvelope checks the list envelope's plan Section 8 shape.
func TestRegionListEnvelope(t *testing.T) {
	r, _ := newTestRouter(t)
	createRegion(t, r, "TR")
	createRegion(t, r, "US")

	rec := do(t, r, http.MethodGet, "/admin/v1/tax-regions", "")
	require.Equal(t, http.StatusOK, rec.Code)

	data, count, offset, limit := decodeList(t, rec)
	assert.Len(t, data, 2)
	assert.Equal(t, int64(2), count)
	assert.Equal(t, int64(0), offset)
	assert.Equal(t, int64(service.DefaultLimit), limit, "the limit applied has to be reported in the envelope")

	rec = do(t, r, http.MethodGet, "/admin/v1/tax-regions?country_code=tr", "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, count, _, _ = decodeList(t, rec)
	assert.Len(t, data, 1)
	assert.Equal(t, int64(1), count)
}

// TestRegionErrorStatusCodes checks that the handler does NOT CHOOSE the status
// code and that the service's error class decides it.
func TestRegionErrorStatusCodes(t *testing.T) {
	r, _ := newTestRouter(t)
	createRegion(t, r, "TR")

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{"invalid country code", http.MethodPost, "/admin/v1/tax-regions", `{"country_code":"TUR"}`, http.StatusUnprocessableEntity},
		{"second root conflicts", http.MethodPost, "/admin/v1/tax-regions", `{"country_code":"TR"}`, http.StatusConflict},
		{"unknown field", http.MethodPost, "/admin/v1/tax-regions", `{"country_code":"DE","tax_rate":20}`, http.StatusUnprocessableEntity},
		{"empty body", http.MethodPost, "/admin/v1/tax-regions", ``, http.StatusUnprocessableEntity},
		{"two JSON documents", http.MethodPost, "/admin/v1/tax-regions", `{"country_code":"DE"}{"country_code":"FR"}`, http.StatusUnprocessableEntity},
		{"missing region", http.MethodGet, "/admin/v1/tax-regions/taxreg_MISSING", ``, http.StatusNotFound},
		{"id of the wrong kind", http.MethodGet, "/admin/v1/tax-regions/taxrate_ABC", ``, http.StatusUnprocessableEntity},
		{"paging is not a number", http.MethodGet, "/admin/v1/tax-regions?limit=abc", ``, http.StatusUnprocessableEntity},
		{"negative offset", http.MethodGet, "/admin/v1/tax-regions?offset=-1", ``, http.StatusUnprocessableEntity},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, r, tt.method, tt.path, tt.body)
			assert.Equal(t, tt.status, rec.Code, "body: %s", rec.Body.String())
			assert.NotEmpty(t, errorCode(t, rec), "the error envelope has to carry a code")
		})
	}
}

// TestRateLifecycle checks the end-to-end flow of the rate endpoints.
func TestRateLifecycle(t *testing.T) {
	r, _ := newTestRouter(t)
	regionID := createRegion(t, r, "TR")

	rateID := createRate(t, r, `{"tax_region_id":"`+regionID+`","name":"KDV","code":"KDV20","rate_bps":2000,"is_default":true}`)

	rec := do(t, r, http.MethodGet, "/admin/v1/tax-rates/"+rateID, "")
	require.Equal(t, http.StatusOK, rec.Code)
	got := decodeItem(t, rec)
	assert.Equal(t, "KDV", got["name"])
	assert.Equal(t, "KDV20", got["code"])
	assert.Equal(t, float64(2000), got["rate_bps"], "the rate has to come back in BASIS POINTS")
	assert.Equal(t, true, got["is_default"])

	rec = do(t, r, http.MethodPut, "/admin/v1/tax-rates/"+rateID, `{"rate_bps":1800}`)
	require.Equal(t, http.StatusOK, rec.Code)
	got = decodeItem(t, rec)
	assert.Equal(t, float64(1800), got["rate_bps"])
	assert.Equal(t, "KDV", got["name"], "a field not given must NOT CHANGE")

	rec = do(t, r, http.MethodPut, "/admin/v1/tax-rates/"+rateID, `{"code":""}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Nil(t, decodeItem(t, rec)["code"], "an empty string has to remove the code")

	rec = do(t, r, http.MethodDelete, "/admin/v1/tax-rates/"+rateID, "")
	require.Equal(t, http.StatusNoContent, rec.Code)

	rec = do(t, r, http.MethodGet, "/admin/v1/tax-rates/"+rateID, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestTheRateListReadsTheSameDownBothPaths checks that the region's
// sub-resource and the endpoint with the query parameter return the same list.
func TestTheRateListReadsTheSameDownBothPaths(t *testing.T) {
	r, _ := newTestRouter(t)
	regionID := createRegion(t, r, "TR")
	createRate(t, r, `{"tax_region_id":"`+regionID+`","name":"KDV","rate_bps":2000,"is_default":true}`)
	createRate(t, r, `{"tax_region_id":"`+regionID+`","name":"Reduced","rate_bps":100}`)

	subResource := do(t, r, http.MethodGet, "/admin/v1/tax-regions/"+regionID+"/tax-rates", "")
	require.Equal(t, http.StatusOK, subResource.Code)
	data, count, _, limit := decodeList(t, subResource)
	assert.Len(t, data, 2)
	assert.Equal(t, int64(2), count)
	assert.Equal(t, int64(2), limit, "in an unpaged list the limit is the number of records returned")
	assert.Equal(t, true, data[0]["is_default"], "the default rate has to come first")

	byQuery := do(t, r, http.MethodGet, "/admin/v1/tax-rates?tax_region_id="+regionID, "")
	require.Equal(t, http.StatusOK, byQuery.Code)
	assert.JSONEq(t, subResource.Body.String(), byQuery.Body.String(),
		"the two paths have to return the same body")
}

// TestARateListWithoutARegionIsRejected checks the required query parameter.
func TestARateListWithoutARegionIsRejected(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodGet, "/admin/v1/tax-rates", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.NotEmpty(t, errorCode(t, rec))
}

// TestRateErrorStatusCodes checks the error mapping of the rate endpoints.
func TestRateErrorStatusCodes(t *testing.T) {
	r, _ := newTestRouter(t)
	regionID := createRegion(t, r, "TR")
	createRate(t, r, `{"tax_region_id":"`+regionID+`","name":"KDV","rate_bps":2000,"is_default":true}`)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{"rate exceeds one hundred percent", http.MethodPost, "/admin/v1/tax-rates",
			`{"tax_region_id":"` + regionID + `","name":"Excess","rate_bps":10001}`, http.StatusUnprocessableEntity},
		{"empty name", http.MethodPost, "/admin/v1/tax-rates",
			`{"tax_region_id":"` + regionID + `","name":"","rate_bps":100}`, http.StatusUnprocessableEntity},
		{"second default", http.MethodPost, "/admin/v1/tax-rates",
			`{"tax_region_id":"` + regionID + `","name":"Second","rate_bps":100,"is_default":true}`, http.StatusConflict},
		{"missing region", http.MethodPost, "/admin/v1/tax-rates",
			`{"tax_region_id":"taxreg_MISSING","name":"KDV","rate_bps":100}`, http.StatusNotFound},
		{"empty patch", http.MethodPut, "/admin/v1/tax-rates/taxrate_X", `{}`, http.StatusUnprocessableEntity},
		{"missing rate", http.MethodDelete, "/admin/v1/tax-rates/taxrate_MISSING", ``, http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, r, tt.method, tt.path, tt.body)
			assert.Equal(t, tt.status, rec.Code, "body: %s", rec.Body.String())
		})
	}
}

// TestRuleLifecycle checks the rule endpoints.
func TestRuleLifecycle(t *testing.T) {
	r, _ := newTestRouter(t)
	regionID := createRegion(t, r, "TR")
	rateID := createRate(t, r, `{"tax_region_id":"`+regionID+`","name":"Reduced","rate_bps":100}`)

	rec := do(t, r, http.MethodPost, "/admin/v1/tax-rates/"+rateID+"/rules",
		`{"reference":"product","reference_id":"prod_1"}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	rule := decodeItem(t, rec)
	assert.Equal(t, "product", rule["reference"])
	assert.Equal(t, "prod_1", rule["reference_id"])
	assert.Equal(t, rateID, rule["tax_rate_id"], "the rate id has to be taken FROM THE PATH")
	ruleID, _ := rule["id"].(string)

	rec = do(t, r, http.MethodGet, "/admin/v1/tax-rates/"+rateID+"/rules", "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, count, _, _ := decodeList(t, rec)
	assert.Len(t, data, 1)
	assert.Equal(t, int64(1), count)

	rec = do(t, r, http.MethodDelete, "/admin/v1/tax-rates/"+rateID+"/rules/"+ruleID, "")
	require.Equal(t, http.StatusNoContent, rec.Code)

	rec = do(t, r, http.MethodGet, "/admin/v1/tax-rates/"+rateID+"/rules", "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, _, _, _ = decodeList(t, rec)
	assert.Empty(t, data)
}

// TestARuleCannotBeAddedToTheDefaultRate checks that the scope rule shows up
// as a 409 over HTTP.
func TestARuleCannotBeAddedToTheDefaultRate(t *testing.T) {
	r, _ := newTestRouter(t)
	regionID := createRegion(t, r, "TR")
	rateID := createRate(t, r, `{"tax_region_id":"`+regionID+`","name":"KDV","rate_bps":2000,"is_default":true}`)

	rec := do(t, r, http.MethodPost, "/admin/v1/tax-rates/"+rateID+"/rules",
		`{"reference":"product","reference_id":"prod_1"}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
}

// TestARuleWithAnInvalidReference checks an undefined reference kind.
func TestARuleWithAnInvalidReference(t *testing.T) {
	r, _ := newTestRouter(t)
	regionID := createRegion(t, r, "TR")
	rateID := createRate(t, r, `{"tax_region_id":"`+regionID+`","name":"Reduced","rate_bps":100}`)

	rec := do(t, r, http.MethodPost, "/admin/v1/tax-rates/"+rateID+"/rules",
		`{"reference":"variant","reference_id":"var_1"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestThereIsNoStoreEndpoint checks that the tax endpoints are NOT OPENED to
// the customer.
//
// The assertion is the proof of a DECISION, not of a gap: tax shows on the
// storefront only inside the cart total (see the api package comment). A
// /store/v1 endpoint added by accident is caught here.
func TestThereIsNoStoreEndpoint(t *testing.T) {
	r, _ := newTestRouter(t)

	for _, path := range []string{
		"/store/v1/tax-regions",
		"/store/v1/tax-rates",
		"/store/v1/taxes",
	} {
		rec := do(t, r, http.MethodGet, path, "")
		assert.Equal(t, http.StatusNotFound, rec.Code, "path: %s", path)
	}
}

// TestTheBodySizeIsBounded checks that an oversized body is rejected.
func TestTheBodySizeIsBounded(t *testing.T) {
	r, _ := newTestRouter(t)

	oversized := `{"country_code":"TR","metadata":{"x":"` + strings.Repeat("a", 128<<10) + `"}}`
	rec := do(t, r, http.MethodPost, "/admin/v1/tax-regions", oversized)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestANarrowScopeDoesNotOpenAWriteEndpoint checks that an identity carrying
// only the read scope gets 403 on the admin write endpoints.
//
// Authentication alone is not enough: an admin user whose scopes were emptied,
// or who is scoped only to read, could change or delete the tax catalog
// without scope enforcement. A 403 is expected, not a 401 — the identity is
// known, what is missing is the scope.
func TestANarrowScopeDoesNotOpenAWriteEndpoint(t *testing.T) {
	r, _ := newTestRouter(t)

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"create region", http.MethodPost, "/admin/v1/tax-regions", `{"country_code":"TR"}`},
		{"delete region", http.MethodDelete, "/admin/v1/tax-regions/taxreg_1", ``},
		{"create rate", http.MethodPost, "/admin/v1/tax-rates", `{"tax_region_id":"taxreg_1","name":"KDV","rate_bps":2000}`},
		{"update rate", http.MethodPut, "/admin/v1/tax-rates/taxrate_1", `{"rate_bps":1800}`},
		{"delete rate", http.MethodDelete, "/admin/v1/tax-rates/taxrate_1", ``},
		{"add rule", http.MethodPost, "/admin/v1/tax-rates/taxrate_1/rules", `{"reference":"product","reference_id":"prod_1"}`},
		{"delete rule", http.MethodDelete, "/admin/v1/tax-rates/taxrate_1/rules/taxrule_1", ``},
	} {
		rec := doAs(t, r, readOnlyPrincipal, tc.method, tc.path, tc.body)
		assert.Equal(t, http.StatusForbidden, rec.Code, "case: %s", tc.name)
		assert.Equal(t, corehttp.CodeForbidden, errorCode(t, rec), "case: %s", tc.name)
	}
}

// TestANarrowScopePassesAReadEndpoint checks that the same narrow identity
// passes the read endpoints.
//
// This test's pair is [TestANarrowScopeDoesNotOpenAWriteEndpoint]: had the
// scope map closed the read endpoints along with every write endpoint, the 403
// results would prove only that the map is overly restrictive, not that it is
// correct.
func TestANarrowScopePassesAReadEndpoint(t *testing.T) {
	r, _ := newTestRouter(t)
	regionID := createRegion(t, r, "TR")

	for _, path := range []string{
		"/admin/v1/tax-regions",
		"/admin/v1/tax-regions/" + regionID,
		"/admin/v1/tax-regions/" + regionID + "/tax-rates",
		"/admin/v1/tax-rates?tax_region_id=" + regionID,
	} {
		rec := doAs(t, r, readOnlyPrincipal, http.MethodGet, path, "")
		assert.Equal(t, http.StatusOK, rec.Code, "path: %s — body: %s", path, rec.Body.String())
	}
}
