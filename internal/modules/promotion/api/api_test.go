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
	"github.com/bdrtr/gobit/internal/modules/promotion/api"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// testNow is the tests' fixed clock.
var testNow = time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

// adminPrincipal is the tests' default caller: a fully privileged admin.
//
// The admin endpoints are guarded by corehttp.RequireScope, and that
// middleware returns 401 when there is NO identity in the context. These tests
// build the router directly, so corehttp.RequireAdmin, which puts the identity
// there, is not in the chain; that is why the test puts the identity there
// itself. The only thing added is the IDENTITY — the behavior the tests verify
// (status codes, envelopes, leak checks) did not change.
var adminPrincipal = corehttp.Principal{
	ID:     "usr_test",
	Kind:   "user",
	Scopes: []string{corehttp.ScopeAdmin},
}

// narrowPrincipal is a narrowly scoped caller that carries only
// [api.ScopeRead].
var narrowPrincipal = corehttp.Principal{
	ID:     "usr_dar",
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

// do runs a request with the fully privileged identity and returns the
// response.
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

// createPromotion creates a promotion and returns its id.
func createPromotion(t *testing.T, r chi.Router, body string) string {
	t.Helper()

	rec := do(t, r, http.MethodPost, "/admin/v1/promotions", body)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	id, ok := decodeItem(t, rec)["id"].(string)
	require.True(t, ok, "the response has to carry an id")
	return id
}

func TestAdminCampaignLifecycle(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodPost, "/admin/v1/campaigns", `{
	  "name": "Summer Sale",
	  "campaign_identifier": "YAZ-2026",
	  "budget_type": "spend",
	  "budget_limit": 100000,
	  "budget_currency_code": "TRY"
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	created := decodeItem(t, rec)
	id, _ := created["id"].(string)
	require.NotEmpty(t, id)
	assert.Equal(t, "TRY", created["budget_currency_code"])
	assert.InDelta(t, 0, created["budget_used"], 0)

	rec = do(t, r, http.MethodGet, "/admin/v1/campaigns/"+id, "")
	require.Equal(t, http.StatusOK, rec.Code)

	rec = do(t, r, http.MethodPut, "/admin/v1/campaigns/"+id, `{
	  "name": "Summer Sale 2",
	  "campaign_identifier": "YAZ-2026",
	  "budget_type": "none"
	}`)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "Summer Sale 2", decodeItem(t, rec)["name"])

	_, count, offset, limit := decodeList(t, do(t, r, http.MethodGet, "/admin/v1/campaigns", ""))
	assert.Equal(t, int64(1), count)
	assert.Equal(t, int64(0), offset)
	assert.Equal(t, int64(service.DefaultLimit), limit, "the envelope reports the APPLIED limit")

	rec = do(t, r, http.MethodDelete, "/admin/v1/campaigns/"+id, "")
	assert.Equal(t, http.StatusNoContent, rec.Code)

	rec = do(t, r, http.MethodGet, "/admin/v1/campaigns/"+id, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAdminPromotionLifecycle(t *testing.T) {
	r, _ := newTestRouter(t)

	id := createPromotion(t, r, `{"code": "yaz20", "status": "active", "is_automatic": true}`)

	rec := do(t, r, http.MethodGet, "/admin/v1/promotions/"+id, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "YAZ20", decodeItem(t, rec)["code"])

	rec = do(t, r, http.MethodPut, "/admin/v1/promotions/"+id+"/application-method", `{
	  "type": "percentage", "target_type": "items", "allocation": "each", "value": 2000
	}`)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	method := decodeItem(t, rec)
	assert.Equal(t, "percentage", method["type"])
	assert.Nil(t, method["currency_code"], "a percentage discount has to have a null currency")

	rec = do(t, r, http.MethodPost, "/admin/v1/promotions/"+id+"/rules", `{
	  "rule_type": "context", "attribute": "region_id", "operator": "eq", "values": ["reg_1"]
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	ruleID, ok := decodeItem(t, rec)["id"].(string)
	require.True(t, ok, "the rule response has to carry an id")

	rules, count, _, _ := decodeList(t, do(t, r, http.MethodGet, "/admin/v1/promotions/"+id+"/rules", ""))
	require.Len(t, rules, 1)
	assert.Equal(t, int64(1), count)

	rec = do(t, r, http.MethodDelete, "/admin/v1/promotion-rules/"+ruleID, "")
	assert.Equal(t, http.StatusNoContent, rec.Code)

	rec = do(t, r, http.MethodDelete, "/admin/v1/promotions/"+id+"/application-method", "")
	assert.Equal(t, http.StatusNoContent, rec.Code)

	rec = do(t, r, http.MethodDelete, "/admin/v1/promotions/"+id, "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

// TestARuleOnTheCartsBagIsRefused holds gap D261's refusal (ADR 0407) at the
// route: the code is asserted as its literal, since a client matches on the
// string the wire carries, and nothing is listed afterwards.
func TestARuleOnTheCartsBagIsRefused(t *testing.T) {
	r, _ := newTestRouter(t)
	id := createPromotion(t, r, `{"code": "summer20", "status": "active", "is_automatic": true}`)

	rec := do(t, r, http.MethodPost, "/admin/v1/promotions/"+id+"/rules", `{
	  "rule_type": "context", "attribute": "cart.brand", "operator": "eq", "values": ["acme"]
	}`)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
	code, _ := errorCodeAndMessage(t, rec)
	assert.Equal(t, "promotion_rule_attribute_reserved", code)
	rules, count, _, _ := decodeList(t, do(t, r, http.MethodGet, "/admin/v1/promotions/"+id+"/rules", ""))
	assert.Empty(t, rules)
	assert.Equal(t, int64(0), count)
}

// TestAdminPromotionUpdateResetsFieldsAbsentFromTheBody proves that PUT is a
// REPLACEMENT and NOT a partial update.
//
// When measured (2026-09-07) this handler was at 0%: create, read, list and
// delete were covered while the edit endpoint had never been called — that is,
// the HTTP surface of promotion editing had shipped without ever running.
//
// The weight of the assertion is on the reset. Had PUT silently behaved as a
// partial update, an operator sending only the code in the body would have kept
// the promotion's automatic flag and usage limit WITHOUT REALIZING IT;
// conversely, when a replacement is done as expected here, those fields going
// away is what the operator ASKED FOR, and the rationale is in the
// [service.Service.UpdatePromotion] godoc: leaving the distinction between
// "field not sent" and "clear the field" to the client would silently swallow a
// request to detach a promotion from its campaign.
//
// The status field dropping is tested separately too: when the body carries no
// status, the promotion goes back to DRAFT, that is, it does not stay
// published. This says that an edit sent incomplete by mistake ends in the SAFE
// direction.
func TestAdminPromotionUpdateResetsFieldsAbsentFromTheBody(t *testing.T) {
	r, _ := newTestRouter(t)

	id := createPromotion(t, r, `{
	  "code": "yaz20",
	  "status": "active",
	  "is_automatic": true,
	  "usage_limit": 5,
	  "metadata": {"kanal": "eposta"}
	}`)

	rec := do(t, r, http.MethodPut, "/admin/v1/promotions/"+id, `{"code": "kis20"}`)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	updated := decodeItem(t, rec)
	assert.Equal(t, id, updated["id"], "an edit does not change the id")
	assert.Equal(t, "KIS20", updated["code"], "the code has to be written converted to UPPER case")
	assert.Equal(t, false, updated["is_automatic"], "a flag absent from the body must NOT KEEP its old value")
	assert.Nil(t, updated["usage_limit"], "a usage limit absent from the body has to go away")
	// The metadata comes back as an EMPTY object, not as null: normalizeMetadata
	// always produces a map instead of nil, so the client does not have to write
	// a null check to tell the field apart.
	assert.Equal(t, map[string]any{}, updated["metadata"], "metadata absent from the body has to go away")
	assert.Equal(t, string(models.PromotionDraft), updated["status"],
		"an edit that does not send the status must not leave the promotion PUBLISHED")

	// What is tested is the RECORD, not the response: the handler could have
	// written a correct body and never done the write at all.
	rec = do(t, r, http.MethodGet, "/admin/v1/promotions/"+id, "")
	require.Equal(t, http.StatusOK, rec.Code)
	readBack := decodeItem(t, rec)
	assert.Equal(t, "KIS20", readBack["code"])
	assert.Equal(t, string(models.PromotionDraft), readBack["status"])
	assert.InDelta(t, 0, readBack["usage_count"], 0, "an edit does not touch the usage counter")

	// The old code belongs to nobody any more and can be taken again.
	rec = do(t, r, http.MethodPost, "/admin/v1/promotions", `{"code": "YAZ20"}`)
	assert.Equal(t, http.StatusCreated, rec.Code,
		"a code given up by an edit must not stay reserved; if it did, the code would not really have been written")
}

// TestAdminUpdatingAMissingPromotionReturns404 proves the edit endpoint's error
// branch.
//
// It is a separate test because the handler's error branch is a separate path:
// the error branch may never have run while the write path works, and in that
// case a client arriving with the wrong id would see 200 or 500 — neither of
// which says "there is no such promotion".
func TestAdminUpdatingAMissingPromotionReturns404(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodPut,
		"/admin/v1/promotions/promo_YOKYOKYOKYOKYOKYOKYOKYOKYO", `{"code": "YENI"}`)

	assert.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
}

func TestAdminPromotionListCanBeFiltered(t *testing.T) {
	r, _ := newTestRouter(t)
	createPromotion(t, r, `{"code": "AKTIF", "status": "active"}`)
	createPromotion(t, r, `{"code": "TASLAK", "status": "draft"}`)

	data, count, _, _ := decodeList(t, do(t, r, http.MethodGet, "/admin/v1/promotions?status=active", ""))
	require.Len(t, data, 1)
	assert.Equal(t, int64(1), count)
	assert.Equal(t, "AKTIF", data[0]["code"])

	rec := do(t, r, http.MethodGet, "/admin/v1/promotions?status=olmayan", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "an undefined status filter is rejected")
}

func TestErrorKindBecomesStatusCode(t *testing.T) {
	r, _ := newTestRouter(t)
	id := createPromotion(t, r, `{"code": "YAZ20"}`)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{
			name: "promotion not found", method: http.MethodGet,
			path: "/admin/v1/promotions/promo_YOKYOKYOKYOKYOKYOKYOKYOKYO", status: http.StatusNotFound,
		},
		{
			name: "id with the wrong prefix", method: http.MethodGet,
			path: "/admin/v1/promotions/camp_1", status: http.StatusUnprocessableEntity,
		},
		{
			name: "invalid body", method: http.MethodPost,
			path: "/admin/v1/promotions", body: `{"code": "AB"}`, status: http.StatusUnprocessableEntity,
		},
		{
			name: "unknown field", method: http.MethodPost,
			path: "/admin/v1/promotions", body: `{"code": "YENI", "bilinmeyen": 1}`,
			status: http.StatusUnprocessableEntity,
		},
		{
			name: "empty body", method: http.MethodPost,
			path: "/admin/v1/promotions", body: "", status: http.StatusUnprocessableEntity,
		},
		{
			name: "duplicate code", method: http.MethodPost,
			path: "/admin/v1/promotions", body: `{"code": "yaz20"}`, status: http.StatusConflict,
		},
		{
			name: "non-numeric paging parameter", method: http.MethodGet,
			path: "/admin/v1/promotions?limit=abc", status: http.StatusUnprocessableEntity,
		},
		{
			name: "method on a missing promotion", method: http.MethodPut,
			path:   "/admin/v1/promotions/promo_YOKYOKYOKYOKYOKYOKYOKYOKYO/application-method",
			body:   `{"type": "percentage", "target_type": "items", "value": 1000}`,
			status: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, r, tt.method, tt.path, tt.body)
			assert.Equal(t, tt.status, rec.Code, "body: %s", rec.Body.String())
		})
	}

	// Even though the existing promotion's id was not used in the cases above,
	// its lifecycle must not be broken.
	rec := do(t, r, http.MethodGet, "/admin/v1/promotions/"+id, "")
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAdminComputeEndpoint(t *testing.T) {
	r, _ := newTestRouter(t)
	id := createPromotion(t, r, `{"code": "YAZ20", "status": "active", "is_automatic": true}`)

	rec := do(t, r, http.MethodPut, "/admin/v1/promotions/"+id+"/application-method", `{
	  "type": "percentage", "target_type": "items", "allocation": "each", "value": 2000
	}`)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = do(t, r, http.MethodPost, "/admin/v1/promotions/compute", `{
	  "currency_code": "TRY",
	  "items": [{"id": "li_1", "amount": 10000, "unit_amount": 10000, "quantity": 1}],
	  "codes": ["HICYOK"]
	}`)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	result := decodeItem(t, rec)
	assert.InDelta(t, 2000, result["discount_total"], 0)
	assert.InDelta(t, 2000, result["items_discount_total"], 0)
	assert.InDelta(t, 0, result["shipping_discount_total"], 0)
	assert.Equal(t, []any{"HICYOK"}, result["unmatched_codes"])

	items, ok := result["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)
	firstItem, ok := items[0].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 2000, firstItem["amount"], 0)
}

func TestAdminRedeemAndRelease(t *testing.T) {
	r, repo := newTestRouter(t)
	id := createPromotion(t, r, `{"code": "YAZ20", "status": "active"}`)

	rec := do(t, r, http.MethodPost, "/admin/v1/promotions/"+id+"/redeem",
		`{"reference": "order_1", "amount": 2500, "currency_code": "TRY"}`)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	firstRedemption := decodeItem(t, rec)["id"]

	rec = do(t, r, http.MethodPost, "/admin/v1/promotions/"+id+"/redeem",
		`{"reference": "order_1", "amount": 2500, "currency_code": "TRY"}`)
	require.Equal(t, http.StatusOK, rec.Code, "an idempotent request returns no error")
	assert.Equal(t, firstRedemption, decodeItem(t, rec)["id"])
	assert.Equal(t, int64(1), repo.promotions[id].UsageCount)

	redemptions, count, _, _ := decodeList(t, do(t, r, http.MethodGet, "/admin/v1/promotions/"+id+"/redemptions", ""))
	require.Len(t, redemptions, 1)
	assert.Equal(t, int64(1), count)

	rec = do(t, r, http.MethodPost, "/admin/v1/promotions/"+id+"/release", `{"reference": "order_1"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, true, decodeItem(t, rec)["released"])

	rec = do(t, r, http.MethodPost, "/admin/v1/promotions/"+id+"/release", `{"reference": "order_1"}`)
	require.Equal(t, http.StatusOK, rec.Code, "the compensation can be run again")
	assert.Equal(t, false, decodeItem(t, rec)["released"])
	assert.Zero(t, repo.promotions[id].UsageCount)
}

func TestStoreCouponValidationDoesNotLeak(t *testing.T) {
	r, _ := newTestRouter(t)
	id := createPromotion(t, r, `{
	  "code": "YAZ20", "status": "active", "usage_limit": 5,
	  "metadata": {"ic_not": "gizli"}
	}`)

	rec := do(t, r, http.MethodPut, "/admin/v1/promotions/"+id+"/application-method", `{
	  "type": "fixed", "target_type": "items", "allocation": "each",
	  "value": 5000, "currency_code": "TRY"
	}`)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = do(t, r, http.MethodPost, "/admin/v1/promotions/"+id+"/rules", `{
	  "rule_type": "context", "attribute": "customer_group_id", "operator": "eq", "values": ["vip"]
	}`)
	require.Equal(t, http.StatusCreated, rec.Code)

	rec = do(t, r, http.MethodGet, "/store/v1/promotions/yaz20", "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	coupon := decodeItem(t, rec)
	assert.Equal(t, "YAZ20", coupon["code"])
	assert.Equal(t, "fixed", coupon["type"])
	assert.Equal(t, "items", coupon["target_type"])
	assert.InDelta(t, 5000, coupon["value"], 0)
	assert.Equal(t, "TRY", coupon["currency_code"])

	for _, field := range []string{
		"status", "usage_limit", "usage_count", "campaign_id", "metadata",
		"rules", "is_automatic", "id",
	} {
		assert.NotContains(t, coupon, field,
			"%q must NOT BE in the customer body; exactly this class of finding turned up in the pricing module", field)
	}

	// The value of the rule condition must appear NOWHERE in the body.
	assert.NotContains(t, rec.Body.String(), "vip", "the rule condition must not leak to the customer")
	assert.NotContains(t, rec.Body.String(), "gizli", "the metadata must not leak to the customer")
}

// errorCodeAndMessage decodes the error code and message in the response body.
func errorCodeAndMessage(t *testing.T, rec *httptest.ResponseRecorder) (code, message string) {
	t.Helper()

	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "body: %s", rec.Body.String())
	return envelope.Error.Code, envelope.Error.Message
}

func TestStoreCouponDoesNotDistinguishByStatus(t *testing.T) {
	r, repo := newTestRouter(t)

	// A draft promotion; it has an application method too, so the only thing
	// it lacks is its STATUS.
	id := createPromotion(t, r, `{"code": "TASLAK", "status": "draft"}`)
	rec := do(t, r, http.MethodPut, "/admin/v1/promotions/"+id+"/application-method", `{
	  "type": "percentage", "target_type": "items", "value": 2000
	}`)
	require.Equal(t, http.StatusOK, rec.Code)

	missingRec := do(t, r, http.MethodGet, "/store/v1/promotions/HICBOYLEBIRKODYOK", "")
	missingCode, missingMessage := errorCodeAndMessage(t, missingRec)
	require.Equal(t, http.StatusNotFound, missingRec.Code)

	// Leak check: every case returns the SAME status and the SAME error code,
	// and the message only repeats the code the client ALREADY knows — not the
	// reason.
	cases := []struct {
		name    string
		prepare func()
		reason  string
	}{
		{
			name:    "draft",
			prepare: func() {},
			reason:  "a draft coupon must be indistinguishable from a nonexistent code",
		},
		{
			name: "inactive",
			prepare: func() {
				promo := repo.promotions[id]
				promo.Status = models.PromotionInactive
				repo.promotions[id] = promo
			},
			reason: "an inactive coupon must be indistinguishable from a nonexistent code",
		},
		{
			name: "usage allowance run out",
			prepare: func() {
				promo := repo.promotions[id]
				promo.Status = models.PromotionActive
				promo.UsageLimit = new(int64)
				repo.promotions[id] = promo
			},
			reason: "the usage counter must not be given away",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			tt.prepare()

			rec := do(t, r, http.MethodGet, "/store/v1/promotions/TASLAK", "")
			code, message := errorCodeAndMessage(t, rec)

			assert.Equal(t, missingRec.Code, rec.Code, tt.reason)
			assert.Equal(t, missingCode, code, tt.reason)
			assert.Equal(t,
				strings.Replace(missingMessage, "HICBOYLEBIRKODYOK", "TASLAK", 1), message,
				"the message only repeats the code the client already knows; it gives no reason")
		})
	}
}

func TestStoreHasNoWriteSurface(t *testing.T) {
	r, _ := newTestRouter(t)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := do(t, r, method, "/store/v1/promotions/YAZ20", `{}`)
		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code,
			"%s: writing a coupon is an admin job", method)
	}
}

// TestNarrowScopeDoesNotOpenWriteEndpoints proves that an identity carrying
// only the read scope gets 403 on the admin write endpoints.
//
// Authentication alone is not enough: without scope enforcement, an admin user
// whose scopes were emptied, or who is only allowed to read, could write
// themselves a promotion with a 100% discount or reset a campaign budget. 403
// is expected, not 401 — the identity is known; what is missing is the scope.
func TestNarrowScopeDoesNotOpenWriteEndpoints(t *testing.T) {
	r, _ := newTestRouter(t)

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"create campaign", http.MethodPost, "/admin/v1/campaigns", `{"name":"X","campaign_identifier":"X","budget_type":"none"}`},
		{"update campaign", http.MethodPut, "/admin/v1/campaigns/promocamp_1", `{"name":"X","campaign_identifier":"X","budget_type":"none"}`},
		{"delete campaign", http.MethodDelete, "/admin/v1/campaigns/promocamp_1", ``},
		{"create promotion", http.MethodPost, "/admin/v1/promotions", `{"code":"BEDAVA"}`},
		{"update promotion", http.MethodPut, "/admin/v1/promotions/promo_1", `{"code":"BEDAVA"}`},
		{"delete promotion", http.MethodDelete, "/admin/v1/promotions/promo_1", ``},
		{"write application method", http.MethodPut, "/admin/v1/promotions/promo_1/application-method",
			`{"type":"percentage","target_type":"items","value":10000}`},
		{"delete application method", http.MethodDelete, "/admin/v1/promotions/promo_1/application-method", ``},
		{"add rule", http.MethodPost, "/admin/v1/promotions/promo_1/rules", `{"attribute":"x","operator":"eq","values":["y"]}`},
		{"delete rule", http.MethodDelete, "/admin/v1/promotion-rules/promorule_1", ``},
		{"redeem", http.MethodPost, "/admin/v1/promotions/promo_1/redeem", `{}`},
		{"release", http.MethodPost, "/admin/v1/promotions/promo_1/release", `{}`},
		// The compute endpoint WRITES nothing, but the vocabulary is defined by
		// the method: POST → write. Had it an exception, the vocabulary would be
		// something argued endpoint by endpoint.
		{"compute discount", http.MethodPost, "/admin/v1/promotions/compute", `{"items":[]}`},
	} {
		rec := doAs(t, r, narrowPrincipal, tc.method, tc.path, tc.body)
		assert.Equal(t, http.StatusForbidden, rec.Code, "case: %s", tc.name)
		code, _ := errorCodeAndMessage(t, rec)
		assert.Equal(t, corehttp.CodeForbidden, code, "case: %s", tc.name)
	}
}

// TestNarrowScopePassesOnReadEndpoints proves that the same narrow identity
// passes on the read endpoints.
//
// This test's pair is [TestNarrowScopeDoesNotOpenWriteEndpoints]: had the scope
// map closed the read endpoints as well while closing every write endpoint, the
// 403 results would prove not the map's correctness but only its
// over-restrictiveness.
func TestNarrowScopePassesOnReadEndpoints(t *testing.T) {
	r, _ := newTestRouter(t)
	id := createPromotion(t, r, `{"code": "YAZ20", "status": "active"}`)

	for _, path := range []string{
		"/admin/v1/campaigns",
		"/admin/v1/promotions",
		"/admin/v1/promotions/" + id,
		"/admin/v1/promotions/" + id + "/rules",
		"/admin/v1/promotions/" + id + "/redemptions",
	} {
		rec := doAs(t, r, narrowPrincipal, http.MethodGet, path, "")
		assert.Equal(t, http.StatusOK, rec.Code, "path: %s — body: %s", path, rec.Body.String())
	}
}

// TestStoreEndpointRequiresNoScope proves that the store endpoint works with an
// unscoped identity too.
//
// The storefront's identity is the publishable key, and that key by definition
// CARRIES no scope. Adding a scope to the store endpoint while adding scopes to
// the admin endpoints is the quietest way to close the whole storefront on the
// first deployment; this test catches that mistake at TEST time, not at compile
// time. 404 is expected: there is no such code — but NOT 403 or 401.
func TestStoreEndpointRequiresNoScope(t *testing.T) {
	r, _ := newTestRouter(t)

	unscoped := corehttp.Principal{ID: "pk_1", Kind: "api_key"}
	rec := doAs(t, r, unscoped, http.MethodGet, "/store/v1/promotions/HICBOYLEBIRKODYOK", "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
}

// TestTheComputeEndpointSaysWhyAPromotionDidNotApply is the answer the body
// could not give.
//
// A merchant looking at a cart with no discount could see WHAT applied and never
// why the coupon they published did not. The commonest cause is the one this test
// uses: they never activated it.
func TestTheComputeEndpointSaysWhyAPromotionDidNotApply(t *testing.T) {
	r, _ := newTestRouter(t)
	id := createPromotion(t, r, `{"code": "DRAFTED", "status": "draft", "is_automatic": true}`)

	rec := do(t, r, http.MethodPut, "/admin/v1/promotions/"+id+"/application-method", `{
	  "type": "percentage", "target_type": "items", "allocation": "each", "value": 2000
	}`)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = do(t, r, http.MethodPost, "/admin/v1/promotions/compute", `{
	  "currency_code": "TRY",
	  "items": [{"id": "li_1", "amount": 10000, "unit_amount": 10000, "quantity": 1}]
	}`)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	result := decodeItem(t, rec)
	assert.InDelta(t, 0, result["discount_total"], 0, "a draft promotion discounts nothing")

	skipped, ok := result["skipped"].([]any)
	require.True(t, ok, "the body has to carry the reasons: %s", rec.Body.String())
	require.Len(t, skipped, 1)

	row, ok := skipped[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, id, row["promotion_id"])
	assert.Equal(t, "not_active", row["reason"])
	assert.Equal(t, "DRAFTED", row["code"],
		"the code is written even for an AUTOMATIC promotion: every promotion here has "+
			"one, and it is what the merchant published and will search for")
}

// TestTheComputeEndpointReportsNoReasonWhenNothingWasRefused keeps the wire from
// having two spellings for "none".
func TestTheComputeEndpointReportsNoReasonWhenNothingWasRefused(t *testing.T) {
	r, _ := newTestRouter(t)
	id := createPromotion(t, r, `{"code": "LIVE20", "status": "active", "is_automatic": true}`)

	rec := do(t, r, http.MethodPut, "/admin/v1/promotions/"+id+"/application-method", `{
	  "type": "percentage", "target_type": "items", "allocation": "each", "value": 2000
	}`)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = do(t, r, http.MethodPost, "/admin/v1/promotions/compute", `{
	  "currency_code": "TRY",
	  "items": [{"id": "li_1", "amount": 10000, "unit_amount": 10000, "quantity": 1}]
	}`)
	require.Equal(t, http.StatusOK, rec.Code)

	result := decodeItem(t, rec)
	assert.Equal(t, []any{}, result["skipped"])
}
