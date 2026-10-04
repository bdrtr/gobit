package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/pricing/api"
)

// This file tests the SCOPE layer on pricing's admin endpoints.
//
// The identity layer (corehttp.RequireAdmin) is faked here: what the test
// wants to prove is not "is the identity resolved correctly" but "is the
// resolved identity's SCOPE enforced endpoint by endpoint". When the two are
// tested separately, the case where authentication works flawlessly while
// authorization was never wired up — that is, the fault that was fixed — stays
// visible.
//
// The service is REAL (with an in-memory repository): that a rejected request
// did not change the repository can only be verified meaningfully when there
// is a real write path.

// requestWithScopes makes a request with an identity carrying the given scopes.
//
// Granting no scope at all is a valid case and produces the "has an identity
// but no scope" caller — that user was the fault itself.
func requestWithScopes(
	t *testing.T, r chi.Router, method, path, body string, scopes ...string,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
		ID:     "usr_narrow",
		Kind:   "user",
		Scopes: scopes,
	}))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// requestWithoutPrincipal makes a request without putting ANY identity into the
// context.
func requestWithoutPrincipal(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// scopeFixture creates a price set, a price list and a price record with a
// fully privileged identity.
//
// For the read endpoints to be able to return 200 they need real ids: reading
// a record that does not exist would return 404, and the test would measure
// that the record was not found rather than that the scope layer let the
// request through. The fixture is built with do(); that helper adds the fully
// privileged identity to the request.
func scopeFixture(t *testing.T, r chi.Router) (priceSetID, priceID, priceListID string) {
	t.Helper()

	set := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets",
		`{"prices":[{"currency_code":"TRY","amount":19900}]}`))
	priceSetID, ok := set["id"].(string)
	require.True(t, ok, "the price set id could not be read")

	prices, ok := set["prices"].([]any)
	require.True(t, ok)
	require.Len(t, prices, 1)
	first, ok := prices[0].(map[string]any)
	require.True(t, ok)
	priceID, ok = first["id"].(string)
	require.True(t, ok, "the price id could not be read")

	priceList := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-lists",
		`{"title":"Summer campaign","type":"sale"}`))
	priceListID, ok = priceList["id"].(string)
	require.True(t, ok, "the price list id could not be read")

	return priceSetID, priceID, priceListID
}

// TestWriteEndpointRejectsNarrowScopedCaller proves that the write endpoints
// ask for [api.ScopeWrite].
//
// The caller is a REAL identity and has the read scope; the only thing it
// lacks is the write scope. That was exactly the fault: every authenticated
// caller could change every price, whatever its scopes.
func TestWriteEndpointRejectsNarrowScopedCaller(t *testing.T) {
	r, _ := newTestRouter(t)
	setID, priceID, listID := scopeFixture(t, r)

	endpoints := map[string]struct {
		method string
		path   string
		body   string
	}{
		"create price set": {http.MethodPost, "/admin/v1/price-sets", `{"prices":[]}`},
		"delete price set": {http.MethodDelete, "/admin/v1/price-sets/" + setID, ""},
		"write prices": {
			http.MethodPost, "/admin/v1/price-sets/" + setID + "/prices",
			`{"prices":[{"currency_code":"TRY","amount":1}]}`,
		},
		"create price list": {
			http.MethodPost, "/admin/v1/price-lists", `{"title":"x","type":"sale"}`,
		},
		"update price list": {
			http.MethodPut, "/admin/v1/price-lists/" + listID, `{"title":"x","type":"sale"}`,
		},
		"delete price list": {http.MethodDelete, "/admin/v1/price-lists/" + listID, ""},
		"create rule": {
			http.MethodPost, "/admin/v1/prices/" + priceID + "/rules",
			`{"attribute":"region_id","operator":"eq","values":["reg_1"]}`,
		},
		"delete rule": {http.MethodDelete, "/admin/v1/price-rules/prule_1", ""},
	}

	for name, tt := range endpoints {
		t.Run(name, func(t *testing.T) {
			rec := requestWithScopes(t, r, tt.method, tt.path, tt.body, api.ScopeRead)

			assert.Equal(t, http.StatusForbidden, rec.Code,
				"a caller with the read scope has to get 403 on a write endpoint; body: %s", rec.Body.String())
			assert.Equal(t, corehttp.CodeForbidden, errorCode(t, rec))
		})
	}

	// A rejected request must NEVER reach the service. The status code alone does
	// not prove that: a handler that deleted BEFORE writing the 403 would return
	// the same code.
	assert.Equal(t, http.StatusOK, do(t, r, http.MethodGet, "/admin/v1/price-sets/"+setID, "").Code,
		"the rejected delete request must not delete the price set")
	assert.Equal(t, http.StatusOK, do(t, r, http.MethodGet, "/admin/v1/price-lists/"+listID, "").Code,
		"the rejected delete request must not delete the price list")

	prices, _, _, _ := decodeList(t, do(t, r, http.MethodGet, "/admin/v1/price-sets/"+setID+"/prices", ""))
	require.Len(t, prices, 1, "the rejected price write request must not change the prices")
	assert.InDelta(t, 19900, prices[0]["amount"], 0)
}

// TestReadEndpointWorksWithNarrowScope proves that the read endpoints LET THE
// SAME narrow identity THROUGH.
//
// That it is a separate test is deliberate: a middleware that rejects every
// request would pass the table above flawlessly but lock the whole admin
// surface. [api.ScopeRead] exists only to keep writing closed; tying reading
// to admin as well would force a narrow-scoped integration that reports prices
// to run with an identity that can write prices.
func TestReadEndpointWorksWithNarrowScope(t *testing.T) {
	r, _ := newTestRouter(t)
	setID, priceID, listID := scopeFixture(t, r)

	endpoints := map[string]string{
		"price set list":         "/admin/v1/price-sets",
		"single price set":       "/admin/v1/price-sets/" + setID,
		"price listing":          "/admin/v1/price-sets/" + setID + "/prices",
		"price lists":            "/admin/v1/price-lists",
		"single price list":      "/admin/v1/price-lists/" + listID,
		"a price's rule listing": "/admin/v1/prices/" + priceID + "/rules",
		"price calculation":      "/admin/v1/price-sets/" + setID + "/calculate?currency_code=TRY",
	}

	for name, path := range endpoints {
		t.Run(name, func(t *testing.T) {
			rec := requestWithScopes(t, r, http.MethodGet, path, "", api.ScopeRead)

			assert.Equal(t, http.StatusOK, rec.Code,
				"the read scope has to be enough for a read endpoint; body: %s", rec.Body.String())
		})
	}
}

// TestCalculateEndpointWorksWithReadScope proves the fixed fault itself: getting
// a price CALCULATED does not take an identity that CAN WRITE prices.
//
// The endpoint used to be a POST; because the scope vocabulary looks at the
// method (see api.API.Routes) it asked for [api.ScopeWrite], and an
// integration that only reports prices — price comparison, export — had to run
// with an identity that could change the whole catalog in a single request.
//
// The second assertion stands in the same test, and deliberately: while the
// calculation is opened to reading, the WRITE surface has to stay closed. Had
// only the first assertion been tested, a regression that also granted the
// narrow identity the write scope would pass the test, and the fix would have
// enlarged the fault.
func TestCalculateEndpointWorksWithReadScope(t *testing.T) {
	r, _ := newTestRouter(t)
	setID, _, _ := scopeFixture(t, r)

	rec := requestWithScopes(t, r, http.MethodGet,
		"/admin/v1/price-sets/"+setID+"/calculate?currency_code=TRY&quantity=2", "",
		api.ScopeRead)

	require.Equal(t, http.StatusOK, rec.Code,
		"the read scope has to be enough to calculate a price; body: %s", rec.Body.String())
	calculated := decodeItem(t, rec)
	assert.InDelta(t, 19900, calculated["amount"], 0)
	assert.InDelta(t, 39800, calculated["total"], 0, "the calculation has to really be made, not return an empty envelope")

	writeAttempt := requestWithScopes(t, r, http.MethodPost, "/admin/v1/price-sets/"+setID+"/prices",
		`{"prices":[{"currency_code":"TRY","amount":1}]}`, api.ScopeRead)

	assert.Equal(t, http.StatusForbidden, writeAttempt.Code,
		"the same narrow identity must not be able to write prices; body: %s", writeAttempt.Body.String())
}

// TestAdminIsASuperscope proves that corehttp.ScopeAdmin is enough to write
// without "pricing:write" being granted separately.
func TestAdminIsASuperscope(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := requestWithScopes(t, r, http.MethodPost, "/admin/v1/price-sets",
		`{"prices":[{"currency_code":"TRY","amount":100}]}`, corehttp.ScopeAdmin)

	assert.Equal(t, http.StatusCreated, rec.Code,
		"the admin scope alone has to be enough to write; body: %s", rec.Body.String())
}

// TestUserWithoutScopesCannotReachPrices proves that an admin user with no
// scope at all cannot reach the read endpoint either.
//
// The godoc of auth service.CreateUserInput.Scopes says an empty scope list
// produces a user who "can sign in but cannot reach any admin endpoint"; this
// test is that sentence's counterpart on the pricing side.
func TestUserWithoutScopesCannotReachPrices(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := requestWithScopes(t, r, http.MethodGet, "/admin/v1/price-sets", "")

	assert.Equal(t, http.StatusForbidden, rec.Code,
		"a user without scopes has to get 403 on a read endpoint; body: %s", rec.Body.String())
	assert.Equal(t, corehttp.CodeForbidden, errorCode(t, rec))
}

// TestStoreEndpointRequiresNoScope proves that the /store/v1 endpoint does NOT
// ASK for a scope.
//
// The storefront surface's identity is the publishable key, and that key by
// definition CARRIES no scope. Had a scope been added to this endpoint, no
// storefront client could read prices.
func TestStoreEndpointRequiresNoScope(t *testing.T) {
	r, _ := newTestRouter(t)
	setID, _, _ := scopeFixture(t, r)

	rec := requestWithoutPrincipal(t, r, http.MethodGet, "/store/v1/price-sets/"+setID, "")

	assert.Equal(t, http.StatusOK, rec.Code,
		"the store endpoint must not ask for a scope; body: %s", rec.Body.String())
}

// TestAdminRequestWithoutPrincipalReturns401 proves that, when there is no
// identity at all, the scope layer returns 401, NOT 403.
//
// The distinction matters to the client: 401 means "tell me who you are", 403
// means "I know who you are but you do not have the scope". Had it returned
// 403, a client that forgot the identity header would go off to ask for a
// scope instead of refreshing its token.
func TestAdminRequestWithoutPrincipalReturns401(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := requestWithoutPrincipal(t, r, http.MethodGet, "/admin/v1/price-sets", "")

	assert.Equal(t, http.StatusUnauthorized, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"),
		"RFC 9110: a 401 has to state which scheme is expected")
}
