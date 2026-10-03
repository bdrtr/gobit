package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/pricing/api"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// testNow is the tests' fixed clock.
var testNow = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

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

// do runs a request and returns the response.
//
// The request carries a FULLY PRIVILEGED identity. In production
// corehttp.RequireAdmin puts the identity into the context; because these
// tests build the router directly, that middleware is not in play and the
// identity is put there by hand. The reason is that corehttp.RequireScope was
// added to the admin endpoints: a request without an identity now gets 401
// before it ever reaches a handler, and the tests here would have tested the
// scope layer instead of pricing behavior. The scope ITSELF is tested in a
// separate file (yetki_test.go); this file's assertions did not change.
func do(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}

	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
		ID:     "usr_test",
		Kind:   "user",
		Scopes: []string{corehttp.ScopeAdmin},
	}))

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

// TestCreatePriceSetWithPrices proves the create flow and the single-record
// envelope.
func TestCreatePriceSetWithPrices(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodPost, "/admin/v1/price-sets",
		`{"prices":[{"currency_code":"try","amount":19900},{"currency_code":"usd","amount":599}]}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	data := decodeItem(t, rec)

	id, ok := data["id"].(string)
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(id, "pset_"))

	prices, ok := data["prices"].([]any)
	require.True(t, ok, "the prices have to be in the response")
	assert.Len(t, prices, 2)

	first, ok := prices[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "TRY", first["currency_code"], "the currency has to be normalized to upper case")
	assert.InDelta(t, 19900, first["amount"], 0)
}

// TestGetPriceSetReturnsPrices proves that the single-record read carries the
// prices.
func TestGetPriceSetReturnsPrices(t *testing.T) {
	r, _ := newTestRouter(t)

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets",
		`{"prices":[{"currency_code":"TRY","amount":100}]}`))
	id, ok := created["id"].(string)
	require.True(t, ok)

	rec := do(t, r, http.MethodGet, "/admin/v1/price-sets/"+id, "")
	require.Equal(t, http.StatusOK, rec.Code)

	data := decodeItem(t, rec)
	assert.Equal(t, id, data["id"])
	prices, ok := data["prices"].([]any)
	require.True(t, ok)
	assert.Len(t, prices, 1)
}

// TestStoreGetPriceSet proves that the store endpoint returns the same body.
func TestStoreGetPriceSet(t *testing.T) {
	r, _ := newTestRouter(t)

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets",
		`{"prices":[{"currency_code":"TRY","amount":100}]}`))
	id, ok := created["id"].(string)
	require.True(t, ok)

	rec := do(t, r, http.MethodGet, "/store/v1/price-sets/"+id, "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, id, decodeItem(t, rec)["id"])
}

// TestListPriceSetsEnvelope proves that the list envelope carries the fields of
// plan Section 8.
func TestListPriceSetsEnvelope(t *testing.T) {
	r, _ := newTestRouter(t)
	for range 3 {
		require.Equal(t, http.StatusCreated,
			do(t, r, http.MethodPost, "/admin/v1/price-sets", `{}`).Code)
	}

	rec := do(t, r, http.MethodGet, "/admin/v1/price-sets?limit=2&offset=1", "")
	require.Equal(t, http.StatusOK, rec.Code)

	data, count, offset, limit := decodeList(t, rec)
	assert.Len(t, data, 2)
	assert.Equal(t, int64(3), count, "count has to be the TOTAL number of records")
	assert.Equal(t, int64(1), offset)
	assert.Equal(t, int64(2), limit)
	assert.NotContains(t, data[0], "prices", "the list response carries no prices")
}

// TestListPriceSetsEmptyIsArray proves that an empty list is [] in JSON, not
// null.
func TestListPriceSetsEmptyIsArray(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodGet, "/admin/v1/price-sets", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"data":[]`)
}

// TestSetPricesReplaces proves that the bulk write is a REPLACEMENT.
func TestSetPricesReplaces(t *testing.T) {
	r, _ := newTestRouter(t)

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets",
		`{"prices":[{"currency_code":"TRY","amount":100},{"currency_code":"USD","amount":5}]}`))
	id, ok := created["id"].(string)
	require.True(t, ok)

	rec := do(t, r, http.MethodPost, "/admin/v1/price-sets/"+id+"/prices",
		`{"prices":[{"currency_code":"EUR","amount":9}]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	data, count, _, _ := decodeList(t, rec)
	require.Len(t, data, 1)
	assert.Equal(t, "EUR", data[0]["currency_code"])
	assert.Equal(t, int64(1), count)

	after, _, _, _ := decodeList(t, do(t, r, http.MethodGet, "/admin/v1/price-sets/"+id+"/prices", ""))
	require.Len(t, after, 1, "prices that were not given have to be deleted")
}

// TestDeletePriceSet proves the delete flow and the 204.
func TestDeletePriceSet(t *testing.T) {
	r, _ := newTestRouter(t)

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets", `{}`))
	id, ok := created["id"].(string)
	require.True(t, ok)

	rec := do(t, r, http.MethodDelete, "/admin/v1/price-sets/"+id, "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String(), "a 204 has to have no body")

	assert.Equal(t, http.StatusNotFound,
		do(t, r, http.MethodGet, "/admin/v1/price-sets/"+id, "").Code)
}

// TestCalculateEndpoint proves that the calculation endpoint returns the
// selection result.
func TestCalculateEndpoint(t *testing.T) {
	r, _ := newTestRouter(t)

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets", `{"prices":[
		{"currency_code":"TRY","amount":1000},
		{"currency_code":"TRY","amount":800,"min_quantity":10,"max_quantity":20}
	]}`))
	id, ok := created["id"].(string)
	require.True(t, ok)

	rec := do(t, r, http.MethodGet,
		"/admin/v1/price-sets/"+id+"/calculate?currency_code=TRY&quantity=10", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	data := decodeItem(t, rec)
	assert.InDelta(t, 800, data["amount"], 0, "the tier with the narrower range has to be selected")
	assert.InDelta(t, 8000, data["total"], 0)
	assert.InDelta(t, 10, data["quantity"], 0)
	assert.Nil(t, data["price_list_type"], "a base price's list type has to be null")
}

// TestCalculateEndpointNotCalculable proves that, when there is no valid price,
// a 404 with a distinguishing code is returned.
func TestCalculateEndpointNotCalculable(t *testing.T) {
	r, _ := newTestRouter(t)

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets",
		`{"prices":[{"currency_code":"TRY","amount":1000}]}`))
	id, ok := created["id"].(string)
	require.True(t, ok)

	rec := do(t, r, http.MethodGet,
		"/admin/v1/price-sets/"+id+"/calculate?currency_code=EUR", "")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "price_not_calculable", errorCode(t, rec))
}

// TestOldCalculatePostIsRemoved proves that the POST counterpart of the
// calculation endpoint NO LONGER EXISTS.
//
// It is a breaking change, and a deliberate one: had the POST path been left
// for compatibility, the fault that was fixed would have stayed where it was —
// a calculation endpoint that asks for the write scope would remain standing
// and integrations would keep leaning on it.
func TestOldCalculatePostIsRemoved(t *testing.T) {
	r, _ := newTestRouter(t)

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets",
		`{"prices":[{"currency_code":"TRY","amount":1000}]}`))
	id, ok := created["id"].(string)
	require.True(t, ok)

	rec := do(t, r, http.MethodPost, "/admin/v1/price-sets/"+id+"/calculate",
		`{"currency_code":"TRY"}`)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, rec.Body.String())
}

// TestCalculateReadsRuleContextFromQuery proves that the rule context is carried
// IN FULL through the query string.
//
// This was the one real risk of moving the endpoint from the body to the
// query: if the context is dropped on the way, the calculation does not fail,
// it silently falls to a DIFFERENT price. That is why both directions are
// tested — without the context the rule-bound price has to be eliminated, with
// it, it has to win.
func TestCalculateReadsRuleContextFromQuery(t *testing.T) {
	r, _ := newTestRouter(t)

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets", `{"prices":[
		{"currency_code":"TRY","amount":1000},
		{"currency_code":"TRY","amount":800,"rules":[
			{"attribute":"region_id","operator":"eq","values":["reg_tr"]}
		]}
	]}`))
	id, ok := created["id"].(string)
	require.True(t, ok)
	base := "/admin/v1/price-sets/" + id + "/calculate?currency_code=TRY"

	rec := do(t, r, http.MethodGet, base, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.InDelta(t, 1000, decodeItem(t, rec)["amount"], 0,
		"without a context the rule-bound price has to be eliminated")

	rec = do(t, r, http.MethodGet, base+"&attr_region_id=reg_tr", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	data := decodeItem(t, rec)
	assert.InDelta(t, 800, data["amount"], 0, "when the rule matches, the region-specific price has to be selected")
	assert.InDelta(t, 1, data["matched_rules"], 0)
}

// TestCalculateReadsMomentFromQuery proves that the "at" parameter REALLY
// reaches the calculation.
//
// The timestamp is the one structured value in the query string; if it is not
// parsed and handed to the service, the campaign window is always evaluated
// against "now" and every calculation aimed at the past or the future is
// silently wrong.
func TestCalculateReadsMomentFromQuery(t *testing.T) {
	r, _ := newTestRouter(t)

	priceList := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-lists",
		`{"title":"July campaign","type":"sale","status":"active",`+
			`"starts_at":"2026-07-01T00:00:00Z","ends_at":"2026-08-01T00:00:00Z"}`))
	listID, ok := priceList["id"].(string)
	require.True(t, ok)

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets", `{}`))
	id, ok := created["id"].(string)
	require.True(t, ok)

	require.Equal(t, http.StatusOK, do(t, r, http.MethodPost,
		"/admin/v1/price-sets/"+id+"/prices",
		`{"prices":[{"currency_code":"TRY","amount":1000},`+
			`{"currency_code":"TRY","amount":700,"price_list_id":"`+listID+`"}]}`).Code)
	base := "/admin/v1/price-sets/" + id + "/calculate?currency_code=TRY"

	rec := do(t, r, http.MethodGet, base, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.InDelta(t, 1000, decodeItem(t, rec)["amount"], 0,
		"the test's clock is outside the campaign window; the base price has to win")

	rec = do(t, r, http.MethodGet, base+"&at=2026-07-10T00:00:00Z", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.InDelta(t, 700, decodeItem(t, rec)["amount"], 0,
		"the given moment is inside the window; the campaign price has to win")
}

// TestCalculateQueryDoesNotSilentlyIgnore proves that a malformed query is
// REJECTED.
//
// While the endpoint was a POST, the body was decoded strictly by decodeBody:
// an unknown field was an error. A query string is lenient by nature, and had
// the same strictness not been built by hand, the move would have been a silent
// regression — a client writing "?qty=10" would read the price for a single
// unit while believing it had asked for 10.
func TestCalculateQueryDoesNotSilentlyIgnore(t *testing.T) {
	r, _ := newTestRouter(t)

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets",
		`{"prices":[{"currency_code":"TRY","amount":1000}]}`))
	id, ok := created["id"].(string)
	require.True(t, ok)
	base := "/admin/v1/price-sets/" + id + "/calculate"

	for name, query := range map[string]string{
		"unrecognized parameter": "?currency_code=TRY&qty=10",
		"non-numeric quantity":   "?currency_code=TRY&quantity=abc",
		"malformed timestamp":    "?currency_code=TRY&at=2026-06-15",
		"repeated parameter":     "?currency_code=TRY&currency_code=USD",
		"repeated rule field":    "?currency_code=TRY&attr_region_id=reg_1&attr_region_id=reg_2",
	} {
		t.Run(name, func(t *testing.T) {
			rec := do(t, r, http.MethodGet, base+query, "")

			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			assert.NotEmpty(t, errorCode(t, rec), "the error envelope has to carry a code")
		})
	}
}

// TestPriceListLifecycle proves the price list CRUD.
func TestPriceListLifecycle(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodPost, "/admin/v1/price-lists",
		`{"title":"Summer campaign","type":"sale"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	created := decodeItem(t, rec)
	id, ok := created["id"].(string)
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(id, "plist_"))
	assert.Equal(t, "draft", created["status"], "with no status given it has to be a draft")

	rec = do(t, r, http.MethodPut, "/admin/v1/price-lists/"+id,
		`{"title":"Summer campaign","type":"sale","status":"active"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "active", decodeItem(t, rec)["status"])

	rec = do(t, r, http.MethodGet, "/admin/v1/price-lists", "")
	data, count, _, _ := decodeList(t, rec)
	assert.Len(t, data, 1)
	assert.Equal(t, int64(1), count)

	assert.Equal(t, http.StatusNoContent,
		do(t, r, http.MethodDelete, "/admin/v1/price-lists/"+id, "").Code)
	assert.Equal(t, http.StatusNotFound,
		do(t, r, http.MethodGet, "/admin/v1/price-lists/"+id, "").Code)
}

// TestPriceRuleEndpoints proves the rule add/list/delete flow.
func TestPriceRuleEndpoints(t *testing.T) {
	r, _ := newTestRouter(t)

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets",
		`{"prices":[{"currency_code":"TRY","amount":100}]}`))
	prices, ok := created["prices"].([]any)
	require.True(t, ok)
	first, ok := prices[0].(map[string]any)
	require.True(t, ok)
	priceID, ok := first["id"].(string)
	require.True(t, ok)

	rec := do(t, r, http.MethodPost, "/admin/v1/prices/"+priceID+"/rules",
		`{"attribute":"region_id","operator":"eq","values":["reg_1"]}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	rule := decodeItem(t, rec)
	ruleID, ok := rule["id"].(string)
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(ruleID, "prule_"))
	assert.Equal(t, priceID, rule["price_id"])

	data, _, _, _ := decodeList(t, do(t, r, http.MethodGet, "/admin/v1/prices/"+priceID+"/rules", ""))
	assert.Len(t, data, 1)

	assert.Equal(t, http.StatusNoContent,
		do(t, r, http.MethodDelete, "/admin/v1/price-rules/"+ruleID, "").Code)
}

// TestErrorStatusMapping proves that the service's error kinds are mapped to the
// right status code. Handlers do NOT CHOOSE the status; the mapping lives in
// core/http.
func TestErrorStatusMapping(t *testing.T) {
	r, _ := newTestRouter(t)

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{"missing container", http.MethodGet, "/admin/v1/price-sets/pset_missing", "", http.StatusNotFound},
		{"wrong id prefix", http.MethodGet, "/admin/v1/price-sets/variant_1", "",
			http.StatusUnprocessableEntity},
		{"negative amount", http.MethodPost, "/admin/v1/price-sets",
			`{"prices":[{"currency_code":"TRY","amount":-1}]}`, http.StatusUnprocessableEntity},
		{"invalid currency", http.MethodPost, "/admin/v1/price-sets",
			`{"prices":[{"currency_code":"TRYX","amount":1}]}`, http.StatusUnprocessableEntity},
		{"excessively large amount", http.MethodPost, "/admin/v1/price-sets",
			`{"prices":[{"currency_code":"TRY","amount":9223372036854775807}]}`,
			http.StatusUnprocessableEntity},
		{"malformed body", http.MethodPost, "/admin/v1/price-sets", `{`,
			http.StatusUnprocessableEntity},
		{"empty body", http.MethodPost, "/admin/v1/price-sets", "",
			http.StatusUnprocessableEntity},
		{"unknown field", http.MethodPost, "/admin/v1/price-sets",
			`{"prices":[],"margin":5}`, http.StatusUnprocessableEntity},
		{"second JSON document", http.MethodPost, "/admin/v1/price-sets", `{} {}`,
			http.StatusUnprocessableEntity},
		{"non-numeric limit", http.MethodGet, "/admin/v1/price-sets?limit=abc", "",
			http.StatusUnprocessableEntity},
		{"negative offset", http.MethodGet, "/admin/v1/price-sets?offset=-1", "",
			http.StatusUnprocessableEntity},
		{"invalid list type", http.MethodPost, "/admin/v1/price-lists",
			`{"title":"K","type":"bogus"}`, http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, r, tc.method, tc.path, tc.body)
			assert.Equal(t, tc.status, rec.Code, rec.Body.String())
			assert.NotEmpty(t, errorCode(t, rec), "the error envelope has to carry a code")
		})
	}
}

// TestBodySizeLimit proves that an excessively large body is rejected.
func TestBodySizeLimit(t *testing.T) {
	r, _ := newTestRouter(t)

	var sb strings.Builder
	sb.WriteString(`{"prices":[`)
	for i := range 40000 {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"currency_code":"TRY","amount":1}`)
	}
	sb.WriteString(`]}`)

	rec := do(t, r, http.MethodPost, "/admin/v1/price-sets", sb.String())

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestUnpagedListReportsRealLimit proves that the limit field of the unpaged
// list envelope reflects the TRUTH.
//
// Were the limit clipped to service.MaxLimit, a response with 150 records
// would say "count=150, limit=100"; the client would take the page size to be
// 100, enter a paging loop and read the same records again. That is why the
// record count is chosen ABOVE MaxLimit — below it, the clipping would not
// show at all.
func TestUnpagedListReportsRealLimit(t *testing.T) {
	r, _ := newTestRouter(t)

	const priceCount = int64(service.MaxLimit) + 50
	items := make([]string, 0, priceCount)
	for i := range priceCount {
		items = append(items,
			`{"currency_code":"TRY","amount":`+strconv.FormatInt(100+i, 10)+
				`,"min_quantity":`+strconv.FormatInt(i+1, 10)+`}`)
	}
	body := `{"prices":[` + strings.Join(items, ",") + `]}`

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets", `{}`))
	id, ok := created["id"].(string)
	require.True(t, ok)

	rec := do(t, r, http.MethodPost, "/admin/v1/price-sets/"+id+"/prices", body)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	data, count, offset, limit := decodeList(t, rec)
	assert.Len(t, data, int(priceCount))
	assert.Equal(t, priceCount, count)
	assert.Zero(t, offset)
	assert.Equal(t, priceCount, limit, "in an unpaged response the limit has to equal the record count")

	data, count, _, limit = decodeList(t, do(t, r, http.MethodGet,
		"/admin/v1/price-sets/"+id+"/prices", ""))
	assert.Len(t, data, int(priceCount))
	assert.Equal(t, priceCount, count)
	assert.Equal(t, priceCount, limit, "it must not be clipped on the read path either")
}
