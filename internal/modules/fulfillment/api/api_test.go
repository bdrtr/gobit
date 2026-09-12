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
	"github.com/bdrtr/gobit/internal/modules/fulfillment/api"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// adminPrincipal is the default caller of the tests: a fully authorized
// administrator.
//
// The admin endpoints are protected with corehttp.RequireScope and that
// middleware returns 401 when there is NO principal in the context. These tests
// build the router directly, so corehttp.RequireAdmin — which places the
// principal into the chain — is absent; the test therefore places the principal
// itself. The only thing added is the PRINCIPAL — the behavior the tests verify
// (status codes, envelopes, leak checks) did not change.
var adminPrincipal = corehttp.Principal{
	ID:     "usr_test",
	Kind:   "user",
	Scopes: []string{corehttp.ScopeAdmin},
}

// readOnlyPrincipal is a narrowly authorized caller carrying only
// [api.ScopeRead].
var readOnlyPrincipal = corehttp.Principal{
	ID:     "usr_narrow",
	Kind:   "user",
	Scopes: []string{api.ScopeRead},
}

// newRouter builds a router running on the fake service.
func newRouter(svc *fakeFulfillments) chi.Router {
	r := chi.NewRouter()
	api.New(svc).Routes(r)
	return r
}

// doRequest applies the given request to the router with the fully authorized
// principal and returns the response.
func doRequest(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	return doRequestAs(t, r, adminPrincipal, method, path, body)
}

// doRequestAs applies the given request to the router with the given principal.
func doRequestAs(t *testing.T, r chi.Router, principal corehttp.Principal, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), principal))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// bodyMap converts the response body into a map.
func bodyMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return out
}

// errorCode returns the code in the error envelope.
//
// The error body is gathered under a single "error" key (see
// corehttp.ErrorResponse); the tests read that shape directly so that a change
// of the envelope does not stay silent.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	failure, ok := bodyMap(t, rec)["error"].(map[string]any)
	require.True(t, ok, "an error envelope was expected: %s", rec.Body.String())
	code, ok := failure["code"].(string)
	require.True(t, ok, "the code has to be text: %s", rec.Body.String())
	return code
}

// sampleQuoted is the sample option used in the store and admin responses.
//
// Every field that deliberately MUST NOT LEAK is filled: provider, profile,
// configuration and metadata.
func sampleQuoted() []service.QuotedOption {
	return []service.QuotedOption{{
		Option: models.ShippingOption{
			ID:                "sopt_1",
			Name:              "Standard shipping",
			ProviderID:        "secret-carrier-company",
			ShippingProfileID: "sprof_1",
			PriceType:         models.PriceFlat,
			Amount:            2_500,
			CurrencyCode:      "TRY",
			RegionID:          "reg_tr",
			Data:              map[string]any{"contract_no": "SECRET-42"},
			Metadata:          map[string]any{"internal_note": "warehouse B"},
		},
		Amount:       2_500,
		CurrencyCode: "TRY",
		ProviderData: json.RawMessage(`{"provider_internal_data":"SECRET"}`),
	}}
}

// TestStoreResponseDoesNotLeakProviderData proves the explicit condition of
// Phase 7.
//
// The provider identifier, the provider's raw data, the option's configuration,
// its metadata and the profile identifier must NEVER appear in the storefront
// response.
func TestStoreResponseDoesNotLeakProviderData(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{quoted: sampleQuoted()}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodGet, "/store/v1/shipping-options?currency_code=TRY", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	raw := rec.Body.String()
	for _, leak := range []string{
		"secret-carrier-company", "provider_id",
		"provider_internal_data", "contract_no", "internal_note",
		"shipping_profile_id", "admin_only", "region_id", "metadata",
	} {
		assert.NotContains(t, raw, leak, "%q must not leak into the store response", leak)
	}

	data, ok := bodyMap(t, rec)["data"].([]any)
	require.True(t, ok, rec.Body.String())
	require.Len(t, data, 1)

	option, ok := data[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "sopt_1", option["id"])
	assert.Equal(t, "Standard shipping", option["name"])
	assert.EqualValues(t, 2_500, option["amount"])
	assert.Equal(t, "TRY", option["currency_code"])
	assert.Equal(t, "flat", option["price_type"])
	assert.Len(t, option, 5, "the store representation must carry only five fields")
}

// TestStoreEndpointCannotRequestAdminOnly proves that the flag is NOT READ from
// a query parameter.
//
// Had it been read, a single parameter coming from the storefront would open
// the admin-only options.
func TestStoreEndpointCannotRequestAdminOnly(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{quoted: sampleQuoted()}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodGet,
		"/store/v1/shipping-options?currency_code=TRY&include_admin_only=true&admin_only=true", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.False(t, svc.lastListInput.IncludeAdminOnly,
		"the store endpoint must never ask for admin_only options")
}

// TestStoreEndpointDoesNotTrustCartFacts fixes the second trust decision.
//
// Regression: the numeric facts the rule engine looks at (subtotal, item_count,
// total_weight) were taken directly from the query parameters and handed to the
// service as TRUSTED. A customer sending "?subtotal=50000" with an empty cart
// saw the free shipping option that was closed to them, and its rate. The flag
// is NOT READ from the query and is a constant false on the store endpoint.
func TestStoreEndpointDoesNotTrustCartFacts(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{quoted: sampleQuoted()}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodGet,
		"/store/v1/shipping-options?currency_code=TRY&subtotal=50000&trusted_facts=true", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.False(t, svc.lastListInput.TrustedFacts,
		"the store endpoint must never report cart facts as trusted")
	assert.Equal(t, int64(50_000), svc.lastListInput.Subtotal,
		"the fact must still be forwarded; not trusting it does not mean not forwarding it")
}

// TestAdminEligibilityEndpointReportsFactsAsTrusted is the other half of the
// distinction.
//
// The admin endpoint is a PREVIEW tool: since the administrator can already
// read the whole catalog and its rules, making up a context opens nothing new
// to them.
func TestAdminEligibilityEndpointReportsFactsAsTrusted(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{quoted: sampleQuoted()}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodGet,
		"/admin/v1/shipping-options/eligible?currency_code=TRY&subtotal=50000", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, svc.lastListInput.TrustedFacts)
}

// TestAdminEligibilityEndpointRequestsAdminOnly proves the other half of the
// distinction.
func TestAdminEligibilityEndpointRequestsAdminOnly(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{quoted: sampleQuoted()}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodGet,
		"/admin/v1/shipping-options/eligible?currency_code=TRY", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, svc.lastListInput.IncludeAdminOnly)

	assert.Contains(t, rec.Body.String(), "provider_id",
		"the admin representation must carry the provider")
	assert.NotContains(t, rec.Body.String(), "provider_internal_data",
		"the provider's raw data is not carried in the admin list either")
}

// TestEligibilityQueryIsParsed proves that the query parameters are forwarded
// to the service correctly.
//
// The profile identifier is a REPEATABLE parameter; a cart may contain products
// bound to several profiles.
func TestEligibilityQueryIsParsed(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodGet,
		"/store/v1/shipping-options?region_id=reg_tr&currency_code=TRY&country_code=TR"+
			"&shipping_profile_id=sprof_1&shipping_profile_id=sprof_2"+
			"&subtotal=50000&item_count=3&total_weight=1500&is_return=true", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	in := svc.lastListInput
	assert.Equal(t, "reg_tr", in.RegionID)
	assert.Equal(t, "TRY", in.CurrencyCode)
	assert.Equal(t, "TR", in.CountryCode)
	assert.Equal(t, []string{"sprof_1", "sprof_2"}, in.ShippingProfileIDs)
	assert.Equal(t, int64(50_000), in.Subtotal)
	assert.Equal(t, int64(3), in.ItemCount)
	assert.Equal(t, int64(1_500), in.TotalWeight)
	assert.True(t, in.IsReturn)
}

// TestMalformedQueryParameterReturns422 proves that a non-numeric parameter is
// treated as a client error.
func TestMalformedQueryParameterReturns422(t *testing.T) {
	t.Parallel()

	r := newRouter(&fakeFulfillments{})

	rec := doRequest(t, r, http.MethodGet, "/store/v1/shipping-options?subtotal=abc", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())

	rec = doRequest(t, r, http.MethodGet, "/store/v1/shipping-options?is_return=maybe", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestOptionCreationReturns201AndEnvelope verifies the happy path and the body
// translation.
func TestOptionCreationReturns201AndEnvelope(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{option: models.ShippingOption{
		ID: "sopt_1", Name: "Standard shipping", ProviderID: "manual",
		ShippingProfileID: "sprof_1", PriceType: models.PriceFlat,
		Amount: 2_500, CurrencyCode: "TRY",
	}}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodPost, "/admin/v1/shipping-options",
		`{"name":"Standard shipping","provider_id":"manual","shipping_profile_id":"sprof_1",`+
			`"price_type":"flat","amount":2500,"currency_code":"TRY"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	assert.Equal(t, "Standard shipping", svc.lastOptionInput.Name)
	assert.Equal(t, "manual", svc.lastOptionInput.ProviderID)
	assert.Equal(t, int64(2_500), svc.lastOptionInput.Amount)

	data, ok := bodyMap(t, rec)["data"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.Equal(t, "sopt_1", data["id"])
}

// TestUnknownBodyFieldIsRejected proves that there is no silently swallowed
// setting.
func TestUnknownBodyFieldIsRejected(t *testing.T) {
	t.Parallel()

	r := newRouter(&fakeFulfillments{})

	rec := doRequest(t, r, http.MethodPost, "/admin/v1/shipping-options",
		`{"name":"Shipping","currency_code":"TRY","unknown":1}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestOptionUpdateHasNoProviderField proves that the update body does NOT
// ACCEPT the provider and the profile.
//
// Had it accepted them, which provider the fulfillments opened on that option
// live at would become retroactively misleading.
func TestOptionUpdateHasNoProviderField(t *testing.T) {
	t.Parallel()

	r := newRouter(&fakeFulfillments{})

	rec := doRequest(t, r, http.MethodPatch, "/admin/v1/shipping-options/sopt_1",
		`{"provider_id":"other-company"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())

	rec = doRequest(t, r, http.MethodPatch, "/admin/v1/shipping-options/sopt_1",
		`{"shipping_profile_id":"sprof_2"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestUpdatePointersLeaveOmittedFieldsUnchanged proves the PATCH semantics.
func TestUpdatePointersLeaveOmittedFieldsUnchanged(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodPatch, "/admin/v1/shipping-options/sopt_1", `{"name":"New name"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	require.NotNil(t, svc.lastUpdateOption.Name)
	assert.Equal(t, "New name", *svc.lastUpdateOption.Name)
	assert.Nil(t, svc.lastUpdateOption.Amount, "a field that is not given must stay nil")
	assert.Nil(t, svc.lastUpdateOption.AdminOnly)
}

// TestFulfillmentCreationTranslatesItems proves that the item body is forwarded
// to the service and that an item without a quantity is rejected.
func TestFulfillmentCreationTranslatesItems(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{fulfillment: models.Fulfillment{
		ID: "ful_1", Reference: "order_1", ShippingOptionID: "sopt_1",
		ProviderID: "manual", Status: models.StatusPending,
	}}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodPost, "/admin/v1/fulfillments",
		`{"reference":"order_1","shipping_option_id":"sopt_1","idempotency_key":"a",`+
			`"items":[{"line_item_id":"line_1","quantity":2}]}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Len(t, svc.lastCreateInput.Items, 1)
	assert.Equal(t, int64(2), svc.lastCreateInput.Items[0].Quantity)

	rec = doRequest(t, r, http.MethodPost, "/admin/v1/fulfillments",
		`{"reference":"order_1","shipping_option_id":"sopt_1","idempotency_key":"a",`+
			`"items":[{"line_item_id":"line_1"}]}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"an item without a quantity must be rejected: %s", rec.Body.String())
}

// TestFulfillmentResponseCarriesItems proves that the list field is always an
// array.
func TestFulfillmentResponseCarriesItems(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{fulfillment: models.Fulfillment{
		ID: "ful_1", Reference: "order_1", ShippingOptionID: "sopt_1",
		ProviderID: "manual", Status: models.StatusPending,
	}}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodGet, "/admin/v1/fulfillments/ful_1", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	data, ok := bodyMap(t, rec)["data"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	items, ok := data["items"].([]any)
	require.True(t, ok, "the items must always be an array: %s", rec.Body.String())
	assert.Empty(t, items)
}

// TestCancelReturnsTheCurrentRecord proves that the cancellation answers with a
// body.
//
// The caller has to be able to see from the status field that the cancellation
// was really written.
func TestCancelReturnsTheCurrentRecord(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{fulfillment: models.Fulfillment{
		ID: "ful_1", Reference: "order_1", ShippingOptionID: "sopt_1",
		ProviderID: "manual", Status: models.StatusCanceled,
	}}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodPost, "/admin/v1/fulfillments/ful_1/cancel", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "ful_1", svc.lastCanceledID)

	data, ok := bodyMap(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "canceled", data["status"])
}

// TestShipEndpointAcceptsEmptyBody proves that shipping can be reported without
// a tracking number.
//
// Some carriers provide the number later; making the body required would make
// that flow impossible.
func TestShipEndpointAcceptsEmptyBody(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{fulfillment: models.Fulfillment{
		ID: "ful_1", Reference: "order_1", ShippingOptionID: "sopt_1",
		ProviderID: "manual", Status: models.StatusShipped,
	}}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodPost, "/admin/v1/fulfillments/ful_1/ship", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, [2]string{"", ""}, svc.lastShipTracking)

	rec = doRequest(t, r, http.MethodPost, "/admin/v1/fulfillments/ful_1/ship",
		`{"tracking_number":"TK-1","tracking_url":"https://carrier/1"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, [2]string{"TK-1", "https://carrier/1"}, svc.lastShipTracking)
}

// TestBodyWithUnknownLengthIsNotIgnored proves that a body arriving with
// chunked encoding is read.
//
// A check looking at Content-Length would silently ignore a tracking number
// that really was sent on a request whose length is -1 (chunked); the client
// would only see it on the shipping screen.
func TestBodyWithUnknownLengthIsNotIgnored(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{fulfillment: models.Fulfillment{
		ID: "ful_1", Reference: "order_1", ShippingOptionID: "sopt_1",
		ProviderID: "manual", Status: models.StatusShipped,
	}}
	r := newRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/admin/v1/fulfillments/ful_1/ship",
		strings.NewReader(`{"tracking_number":"TK-9"}`))
	req.Header.Set("Content-Type", "application/json")
	// The admin endpoint now demands a scope; if the principal is not put into
	// the context the request comes back with 401 without the body ever being
	// looked at, and the chunked read path the test exercises would never run.
	// The only thing added is the principal.
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), adminPrincipal))
	// httptest derives the body length from the reader; to imitate a chunked
	// request an UNKNOWN length is set by hand.
	req.ContentLength = -1

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, [2]string{"TK-9", ""}, svc.lastShipTracking,
		"a body whose length is unknown must be read as well")
}

// TestErrorKindsAreTranslatedToStatusCodes proves that the handler does not
// choose the status, that the translation is done in corehttp (plan Section 8).
func TestErrorKindsAreTranslatedToStatusCodes(t *testing.T) {
	t.Parallel()

	t.Run("not found 404", func(t *testing.T) {
		t.Parallel()

		r := newRouter(&fakeFulfillments{err: notFoundError()})
		rec := doRequest(t, r, http.MethodGet, "/admin/v1/fulfillments/ful_1", "")
		assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
		assert.Equal(t, "fulfillment_not_found", errorCode(t, rec))
	})

	t.Run("conflict 409", func(t *testing.T) {
		t.Parallel()

		r := newRouter(&fakeFulfillments{err: conflictError()})
		rec := doRequest(t, r, http.MethodPost, "/admin/v1/fulfillments/ful_1/cancel", "")
		assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
		assert.Equal(t, service.CodeInvalidTransition, errorCode(t, rec))
	})
}

// TestDeletesReturn204 verifies the body-less delete responses.
func TestDeletesReturn204(t *testing.T) {
	t.Parallel()

	r := newRouter(&fakeFulfillments{})

	for _, path := range []string{
		"/admin/v1/shipping-profiles/sprof_1",
		"/admin/v1/shipping-options/sopt_1",
		"/admin/v1/shipping-options/sopt_1/rules/sorule_1",
	} {
		rec := doRequest(t, r, http.MethodDelete, path, "")
		assert.Equal(t, http.StatusNoContent, rec.Code, path)
		assert.Empty(t, rec.Body.String(), path)
	}
}

// TestRuleCreationBodyIsTranslated proves that the rule body is forwarded to
// the service.
func TestRuleCreationBodyIsTranslated(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{rule: models.ShippingOptionRule{
		ID: "sorule_1", ShippingOptionID: "sopt_1",
		Attribute: "subtotal", Operator: models.OpGte, Values: []string{"50000"},
	}}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodPost, "/admin/v1/shipping-options/sopt_1/rules",
		`{"attribute":"subtotal","operator":"gte","values":["50000"]}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "subtotal", svc.lastRuleInput.Attribute)
	assert.Equal(t, []string{"50000"}, svc.lastRuleInput.Values)
}

// TestProviderListIsAdminOnly proves that the carriers are not exposed on the
// store surface.
//
// This is the difference from payment: the customer has to know which payment
// method to choose, but which carrier the store works with is the store's
// operational information.
func TestProviderListIsAdminOnly(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{providerIDs: []string{"manual"}}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodGet, "/admin/v1/fulfillment-providers", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = doRequest(t, r, http.MethodGet, "/store/v1/fulfillment-providers", "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "no such endpoint must exist on the store surface")
}

// TestListEnvelopeIsConsistent verifies the fields of the paging envelope (plan
// Section 8).
func TestListEnvelopeIsConsistent(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{
		options: []models.ShippingOption{{ID: "sopt_1", Name: "Shipping", CurrencyCode: "TRY"}},
		count:   42,
	}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodGet, "/admin/v1/shipping-options?limit=10&offset=20", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	body := bodyMap(t, rec)
	assert.EqualValues(t, 42, body["count"], "count must be the number of ALL records matching the filter")
	assert.EqualValues(t, 20, body["offset"])
	assert.EqualValues(t, 10, body["limit"])
	assert.Contains(t, body, "data")
}

// TestNarrowScopeDoesNotOpenWriteEndpoints proves that a principal carrying
// only the read scope gets 403 on the admin write endpoints.
//
// Authentication alone is not enough: an admin user whose scopes have been
// emptied, or who is authorized only to read, could without scope enforcement
// open a fulfillment and print a shipping label, cancel an opened fulfillment,
// or close an order that was never shipped as "delivered". A 403 is expected,
// not a 401 — the identity is known, what is missing is the scope.
func TestNarrowScopeDoesNotOpenWriteEndpoints(t *testing.T) {
	t.Parallel()

	// The fake service answers every call SUCCESSFULLY; a 403 arriving on its
	// own shows that the middleware never let the request reach the handler.
	svc := &fakeFulfillments{}
	r := newRouter(svc)

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"create profile", http.MethodPost, "/admin/v1/shipping-profiles", `{"name":"Default","type":"default"}`},
		{"update profile", http.MethodPatch, "/admin/v1/shipping-profiles/sprof_1", `{"name":"New"}`},
		{"delete profile", http.MethodDelete, "/admin/v1/shipping-profiles/sprof_1", ``},
		{"create option", http.MethodPost, "/admin/v1/shipping-options", `{"name":"Shipping"}`},
		{"update option", http.MethodPatch, "/admin/v1/shipping-options/sopt_1", `{"name":"Shipping 2"}`},
		{"delete option", http.MethodDelete, "/admin/v1/shipping-options/sopt_1", ``},
		{"add rule", http.MethodPost, "/admin/v1/shipping-options/sopt_1/rules", `{"attribute":"subtotal","operator":"gte","value":"1000"}`},
		{"delete rule", http.MethodDelete, "/admin/v1/shipping-options/sopt_1/rules/sorule_1", ``},
		{"open fulfillment", http.MethodPost, "/admin/v1/fulfillments", `{"reference":"order_1","shipping_option_id":"sopt_1"}`},
		{"cancel fulfillment", http.MethodPost, "/admin/v1/fulfillments/ful_1/cancel", ``},
		{"hand to carrier", http.MethodPost, "/admin/v1/fulfillments/ful_1/ship", `{"tracking_number":"TK-9"}`},
		{"report delivery", http.MethodPost, "/admin/v1/fulfillments/ful_1/deliver", ``},
		// The "iade" route was missing from this table until 2026-09-07 while
		// every other write route was in it, and its handler had never been
		// executed by any test at all. It is a TERMINAL transition: a read-only
		// operator able to reach it could close a shipment that is still in
		// transit as "came back", and nothing in the module can move it out of
		// that status afterwards.
		{"report the parcel came back", http.MethodPost, "/admin/v1/fulfillments/ful_1/returned", ``},
		// Writing a location policy can, unlike the other writes in the module,
		// stop the ORDER PATH: a wrong region binding eliminates the location
		// on every cart. The endpoint is protected with the same scope and this
		// table is the proof that it was not left out of that coverage.
		{"write location policy", http.MethodPut, "/admin/v1/shipping-locations/sloc_1", `{"priority":0,"region_ids":["reg_tr"]}`},
		{"delete location policy", http.MethodDelete, "/admin/v1/shipping-locations/sloc_1", ``},
	} {
		rec := doRequestAs(t, r, readOnlyPrincipal, tc.method, tc.path, tc.body)
		assert.Equal(t, http.StatusForbidden, rec.Code, "case: %s", tc.name)
		assert.Equal(t, corehttp.CodeForbidden, errorCode(t, rec), "case: %s", tc.name)
	}

	assert.Empty(t, svc.lastCanceledID, "the request must never reach the service")
	assert.Empty(t, svc.lastReturnedID, "the request must never reach the service")
	assert.Empty(t, svc.lastDeliveredID, "the request must never reach the service")
	assert.Equal(t, [2]string{}, svc.lastShipTracking, "the request must never reach the service")
	assert.Equal(t, service.SetShippingLocationInput{}, svc.lastLocationInput,
		"the location policy request must never reach the service")
}

// TestNarrowScopePassesOnReadEndpoints proves that the same narrow principal
// passes through the read endpoints.
//
// The pair of this test is [TestNarrowScopeDoesNotOpenWriteEndpoints]: if the
// scope map closed the read endpoints while closing every write endpoint, the
// 403 results would prove not the map's correctness but only its excessive
// restrictiveness.
func TestNarrowScopePassesOnReadEndpoints(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{providerIDs: []string{"manual"}, quoted: sampleQuoted()}
	r := newRouter(svc)

	for _, path := range []string{
		"/admin/v1/fulfillment-providers",
		"/admin/v1/shipping-profiles",
		"/admin/v1/shipping-profiles?type=gift_card",
		"/admin/v1/shipping-profiles/sprof_1",
		"/admin/v1/shipping-options",
		"/admin/v1/shipping-options/eligible?currency_code=TRY",
		"/admin/v1/shipping-options/sopt_1",
		"/admin/v1/shipping-options/sopt_1/rules",
		"/admin/v1/fulfillments",
		"/admin/v1/fulfillments/ful_1",
		"/admin/v1/shipping-locations",
		"/admin/v1/shipping-locations/sloc_1",
	} {
		rec := doRequestAs(t, r, readOnlyPrincipal, http.MethodGet, path, "")
		assert.Equal(t, http.StatusOK, rec.Code, "path: %s — body: %s", path, rec.Body.String())
	}
}

// TestStoreEndpointRequiresNoScope proves that the store endpoint works with an
// unauthorized principal too.
//
// The storefront's identity is the publishable key and that key by definition
// CARRIES no scope. Adding a scope to the store endpoint while adding scopes to
// the admin endpoints is the quietest way to close the whole storefront on the
// first deployment; this test catches that mistake.
func TestStoreEndpointRequiresNoScope(t *testing.T) {
	t.Parallel()

	r := newRouter(&fakeFulfillments{quoted: sampleQuoted()})

	unauthorized := corehttp.Principal{ID: "pk_1", Kind: "api_key"}
	rec := doRequestAs(t, r, unauthorized, http.MethodGet,
		"/store/v1/shipping-options?currency_code=TRY", "")
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// TestLocationPolicyWriteCombinesBodyAndPath proves that the PUT takes the
// location identifier FROM THE PATH and the rest from the body.
//
// Accepting the identifier in the body as well would create two sources, and
// when the two drifted apart which one wins would be a detail known only to
// whoever reads the code.
func TestLocationPolicyWriteCombinesBodyAndPath(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{location: models.ShippingLocation{
		LocationID: "sloc_1", Priority: -2, RegionIDs: []string{"reg_tr"},
	}}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodPut,
		"/admin/v1/shipping-locations/sloc_1", `{"priority":-2,"region_ids":["reg_tr"]}`)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	assert.Equal(t, service.SetShippingLocationInput{
		LocationID: "sloc_1",
		Priority:   -2,
		RegionIDs:  []string{"reg_tr"},
	}, svc.lastLocationInput)
}

// TestLocationPolicyResponseWritesEmptyRegionList proves that the "region_ids"
// key STAYS in the response even on a location with no region binding.
//
// Had the field carried omitempty the key would drop and the client could not
// tell "no information" from "serves all regions"; whereas an empty array
// states the rule itself.
func TestLocationPolicyResponseWritesEmptyRegionList(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{location: models.ShippingLocation{LocationID: "sloc_1"}}
	r := newRouter(svc)

	rec := doRequestAs(t, r, readOnlyPrincipal, http.MethodGet,
		"/admin/v1/shipping-locations/sloc_1", "")
	require.Equal(t, http.StatusOK, rec.Code)

	// The assertion is bound to the TYPE. assert.Empty would pass both null and
	// an empty array, that is, the test would stay green even if the nil->[]
	// conversion were removed; whereas for the client null means "no
	// information" and [] means "serves all regions".
	assert.Contains(t, rec.Body.String(), `"region_ids":[]`,
		"an empty region list must be written into the body as an EMPTY ARRAY, not as null")

	data, ok := bodyMap(t, rec)["data"].(map[string]any)
	require.True(t, ok, "the response must carry the single envelope")
	require.Contains(t, data, "region_ids", "the key must be written even when it is empty")
	assert.Equal(t, []any{}, data["region_ids"])
}

// TestLocationPolicyReadTakesIDFromPath proves that the GET reads the location
// identifier from the path parameter under the RIGHT name.
//
// Had the parameter name been misspelled, chi would return an empty string, the
// service would reject it with a 422, and a test that only asks "did a 200
// arrive" would catch it — but what it caught would be wrong: a reader seeing
// the 422 would look for a flaw in the client's body. Testing WHAT the
// identifier reaches the service AS shows the fault in the right place.
func TestLocationPolicyReadTakesIDFromPath(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{location: models.ShippingLocation{LocationID: "sloc_read"}}
	r := newRouter(svc)

	rec := doRequestAs(t, r, readOnlyPrincipal, http.MethodGet,
		"/admin/v1/shipping-locations/sloc_read", "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "sloc_read", svc.lastReadLocation,
		"the identifier must be taken from the path and passed to the service as it is")
}

// TestLocationPolicyDeleteTakesIDFromPathAndReturns204 proves that the DELETE
// handler really runs, that it takes the identifier from the path and that the
// response has no body.
func TestLocationPolicyDeleteTakesIDFromPathAndReturns204(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodDelete, "/admin/v1/shipping-locations/sloc_to_delete", "")
	require.Equal(t, http.StatusNoContent, rec.Code, "body: %s", rec.Body.String())
	assert.Empty(t, rec.Body.String(), "a 204 must have no body")
	assert.Equal(t, "sloc_to_delete", svc.lastDeletedLocation,
		"the identifier must be taken from the path and passed to the service as it is")
}

// TestLocationPolicyResponseWritesFieldsVerbatim proves that the response body
// carries the values coming from the service AS THEY ARE.
//
// A test looking at the status code is not enough: a translation returning the
// wrong priority or an empty region list also returns 200, and the admin screen
// would show a policy other than the one the operator wrote.
func TestLocationPolicyResponseWritesFieldsVerbatim(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{location: models.ShippingLocation{
		LocationID: "sloc_fields", Priority: -7, RegionIDs: []string{"reg_tr", "reg_de"},
	}}
	r := newRouter(svc)

	rec := doRequestAs(t, r, readOnlyPrincipal, http.MethodGet,
		"/admin/v1/shipping-locations/sloc_fields", "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	data, ok := bodyMap(t, rec)["data"].(map[string]any)
	require.True(t, ok, "the response must carry the single envelope")
	assert.Equal(t, "sloc_fields", data["location_id"])
	assert.EqualValues(t, -7, data["priority"], "a negative priority must be written into the body as it is")
	assert.Equal(t, []any{"reg_tr", "reg_de"}, data["region_ids"],
		"the region list must be written complete and with its ORDER preserved")
}

// TestLocationPolicyListCarriesAllRecordsAndThePage proves that the listing
// writes ALL of the records on the page and that the paging parameters REACH
// the service.
//
// The two assertions stand together because both can break silently: a loop
// that writes only the first record returns 200, and so does a handler that
// ignores limit/offset.
func TestLocationPolicyListCarriesAllRecordsAndThePage(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{
		locations: []models.ShippingLocation{
			{LocationID: "sloc_1", Priority: -1, RegionIDs: []string{"reg_tr"}},
			{LocationID: "sloc_2", Priority: 3},
		},
		count: 42,
	}
	r := newRouter(svc)

	rec := doRequestAs(t, r, readOnlyPrincipal, http.MethodGet,
		"/admin/v1/shipping-locations?limit=1&offset=5", "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	body := bodyMap(t, rec)
	assert.EqualValues(t, 42, body["count"], "count must be the number of ALL records matching the filter")
	assert.EqualValues(t, 1, body["limit"])
	assert.EqualValues(t, 5, body["offset"])

	data, ok := body["data"].([]any)
	require.True(t, ok, "the response must carry the list envelope")
	require.Len(t, data, 2, "EVERY record returned from the service must be written")

	second, ok := data[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "sloc_2", second["location_id"])
	assert.Equal(t, []any{}, second["region_ids"], "a record with no binding must carry an empty array")

	assert.Equal(t, service.Page{Limit: 1, Offset: 5}, svc.lastLocationPage,
		"the paging parameters must REACH the service; had they been ignored the response would still be 200")
}

// TestADeliveryReportAnswersWithTheRecordItJustWrote proves that the deliver
// endpoint addresses the fulfillment named in the PATH and hands the caller
// back the record as it now stands.
//
// This endpoint is the one that CLOSES an order. The operator clicking it is
// not merely writing a row: downstream the order is settled, the customer stops
// being chased and the shipment leaves every "still out there" report. If the
// path parameter never arrived — chi answers an empty string for a name it does
// not know — the service would be asked about fulfillment "" while the handler
// still wrote the canned record it already had, and the screen would show a
// delivery that was recorded against nothing. Nothing else in the chain looks
// at the identifier again, so there is no second place this can be caught.
//
// The delivered moment is asserted for the same reason as the status: the DTO
// carries it with omitempty, so a handler that dropped the stamp would produce
// a response that reads "delivered" with no answer to WHEN, and reconciliation
// against the carrier's portal has nothing to match on.
func TestADeliveryReportAnswersWithTheRecordItJustWrote(t *testing.T) {
	t.Parallel()

	deliveredAt := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	svc := &fakeFulfillments{fulfillment: models.Fulfillment{
		ID: "ful_1", Reference: "order_1", ShippingOptionID: "sopt_1",
		ProviderID: "manual", Status: models.StatusDelivered,
		DeliveredAt: &deliveredAt,
	}}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodPost, "/admin/v1/fulfillments/ful_1/deliver", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "ful_1", svc.lastDeliveredID,
		"the fulfillment named in the path must be the one reported as delivered")

	data, ok := bodyMap(t, rec)["data"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.Equal(t, "delivered", data["status"],
		"the caller has to be able to SEE that the delivery was written")
	assert.NotEmpty(t, data["delivered_at"],
		"a delivery with no moment cannot be reconciled against the carrier's portal")
}

// TestAParcelThatCameBackIsReportedWithoutABody proves that the "iade" endpoint
// works with an EMPTY request, addresses the fulfillment from the path, and
// answers with the returned stamp.
//
// The absence of a body is the design (see returnFulfillment): the route
// asserts one fact and takes no operator input to color it with. Were the
// handler to grow a decode of a required body, every caller — the operator
// screen and the carrier callback ring alike — would start getting 422 on a
// request that is correct, and the parcel would sit in the warehouse reading
// "shipped" forever, which is the exact state migration
// 000004_a_parcel_can_come_back exists to make impossible.
//
// returned_at is asserted because it is the field that migration added and the
// only one that distinguishes this outcome from a cancellation in the response:
// a database CHECK ties the status and the stamp together in both directions,
// so a response carrying one without the other describes a row that cannot
// exist.
func TestAParcelThatCameBackIsReportedWithoutABody(t *testing.T) {
	t.Parallel()

	returnedAt := time.Date(2026, 9, 6, 15, 30, 0, 0, time.UTC)
	svc := &fakeFulfillments{fulfillment: models.Fulfillment{
		ID: "ful_1", Reference: "order_1", ShippingOptionID: "sopt_1",
		ProviderID: "manual", Status: models.StatusReturned,
		ReturnedAt: &returnedAt,
	}}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodPost, "/admin/v1/fulfillments/ful_1/returned", "")
	require.Equal(t, http.StatusOK, rec.Code,
		"the return report carries no body and must not be refused for that: %s", rec.Body.String())
	assert.Equal(t, "ful_1", svc.lastReturnedID,
		"the fulfillment named in the path must be the one reported as returned")

	data, ok := bodyMap(t, rec)["data"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.Equal(t, "returned", data["status"],
		"'returned' is its own status; a parcel that came back is neither canceled nor delivered")
	assert.NotEmpty(t, data["returned_at"],
		"the status and its moment travel together; the schema refuses one without the other")
	assert.NotContains(t, rec.Body.String(), "delivered_at",
		"a parcel that came back never reached the recipient")
	assert.NotContains(t, rec.Body.String(), "canceled_at",
		"a parcel that came back was not recalled by us")
}

// TestProfileCreationForwardsEveryFieldAndAnswers201 proves the write half of
// the profile surface: the body is bound field by field and the created record
// comes back with its identifier.
//
// Three separate things can break here silently. A field bound to the wrong
// slot — name into type — produces a profile called "custom" and a 201 that
// looks perfect. Metadata dropped on the floor loses the only place the store
// keeps its own bookkeeping on a profile, and nothing later reports it missing.
// And an answer without the identifier makes the endpoint unusable: creating an
// option next door REQUIRES a shipping_profile_id, and this response is the
// only place the caller can learn the one it just made.
func TestProfileCreationForwardsEveryFieldAndAnswers201(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{profile: models.ShippingProfile{
		ID:       "sprof_1",
		Name:     "Heavy goods",
		Type:     models.ProfileCustom,
		Metadata: map[string]any{"desk": "warehouse B"},
	}}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodPost, "/admin/v1/shipping-profiles",
		`{"name":"Heavy goods","type":"custom","metadata":{"desk":"warehouse B"}}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	assert.Equal(t, "Heavy goods", svc.lastProfileInput.Name)
	assert.Equal(t, "custom", svc.lastProfileInput.Type,
		"the type must land in the type slot, not in the name")
	assert.Equal(t, map[string]any{"desk": "warehouse B"}, svc.lastProfileInput.Metadata,
		"the store's own bookkeeping must reach the service; dropping it is invisible afterwards")

	data, ok := bodyMap(t, rec)["data"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.Equal(t, "sprof_1", data["id"],
		"the identifier is the only thing the caller cannot obtain any other way")
	assert.Equal(t, "custom", data["type"])
}

// TestAProfileUpdateCarriesEachFieldToItsOwnSlot proves that PATCH takes the
// profile identifier FROM THE PATH and every changed field from the body,
// without the two crossing.
//
// [TestUpdatePointersLeaveOmittedFieldsUnchanged] makes the pointer argument
// for the OPTION handler; it cannot make it for this one. These are two
// separate structs literal-copied into two separate service calls, and the
// mistake this test exists for — name written into type, or the path
// identifier never read — lives entirely inside the copy. A profile renamed to
// "gift_card" is still a valid profile, so neither the service nor the schema
// refuses it; the operator finds out when the wrong products stop being
// shippable.
func TestAProfileUpdateCarriesEachFieldToItsOwnSlot(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{profile: models.ShippingProfile{
		ID: "sprof_1", Name: "Gift cards", Type: models.ProfileGiftCard,
	}}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodPatch, "/admin/v1/shipping-profiles/sprof_1",
		`{"name":"Gift cards","type":"gift_card","metadata":{"note":"no parcel"}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	assert.Equal(t, "sprof_1", svc.lastUpdateProfileID,
		"the profile being edited is the one named in the path; chi answers an "+
			"empty string for a name it does not know and the response is still 200")

	require.NotNil(t, svc.lastUpdateProfile.Name)
	assert.Equal(t, "Gift cards", *svc.lastUpdateProfile.Name)
	require.NotNil(t, svc.lastUpdateProfile.Type)
	assert.Equal(t, "gift_card", *svc.lastUpdateProfile.Type,
		"the type must land in the type slot; a profile renamed to its own type is still valid")
	assert.Equal(t, map[string]any{"note": "no parcel"}, svc.lastUpdateProfile.Metadata)
}

// TestAProfileUpdateDoesNotInventTheFieldsTheCallerOmitted proves that the
// PATCH body's pointers really do separate "not sent" from "sent empty".
//
// The update writes the profile's columns with ABSOLUTE values (see the
// repository's UpdateShippingProfile), so a field the handler turns into a
// non-nil zero is not ignored downstream — it is WRITTEN. An operator renaming
// a profile would silently reset its type to the default and drop its metadata,
// and the response would show exactly the rename they asked for.
func TestAProfileUpdateDoesNotInventTheFieldsTheCallerOmitted(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodPatch, "/admin/v1/shipping-profiles/sprof_1",
		`{"name":"Renamed"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	require.NotNil(t, svc.lastUpdateProfile.Name)
	assert.Equal(t, "Renamed", *svc.lastUpdateProfile.Name)
	assert.Nil(t, svc.lastUpdateProfile.Type,
		"a type that was not sent must stay nil; a zero here is WRITTEN over the real type")
	assert.Nil(t, svc.lastUpdateProfile.Metadata,
		"metadata that was not sent must stay nil; the update REPLACES it, it does not merge")
}

// TestTheProfileTypeFilterReachesTheService proves that ?type= is forwarded
// rather than merely parsed.
//
// A filter that is read and dropped answers 200 with a FULL page, so the
// operator asking for the gift-card profiles sees every profile in the store
// and has no signal that the question was ignored. The absent case is asserted
// alongside it, because a handler that forwarded a pointer to the empty string
// would turn "no filter" into "type = ”" and return nothing at all — the same
// endpoint failing in the opposite direction.
func TestTheProfileTypeFilterReachesTheService(t *testing.T) {
	t.Parallel()

	svc := &fakeFulfillments{}
	r := newRouter(svc)

	rec := doRequestAs(t, r, readOnlyPrincipal, http.MethodGet,
		"/admin/v1/shipping-profiles?type=gift_card", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, svc.lastProfileList.Type, "the type filter must REACH the service")
	assert.Equal(t, "gift_card", *svc.lastProfileList.Type)

	svc.lastProfileList = service.ListProfilesInput{}
	rec = doRequestAs(t, r, readOnlyPrincipal, http.MethodGet, "/admin/v1/shipping-profiles", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Nil(t, svc.lastProfileList.Type,
		"no filter must stay nil; a pointer to the empty string would match no profile at all")
}

// The tracking endpoint (ADR 0149).

// TestTheTrackingEndpointReportsBothSides pins what a client receives.
func TestTheTrackingEndpointReportsBothSides(t *testing.T) {
	moved := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	svc := &fakeFulfillments{tracking: service.ShipmentTracking{
		FulfillmentID:          "ful_1",
		ProviderID:             "manual",
		ExternalID:             "manful_1",
		Answer:                 service.TrackingAnswered,
		LocalStatus:            models.StatusShipped,
		LocalTrackingNumber:    "OPERATOR-1",
		ProviderStatus:         models.StatusPending,
		ProviderTrackingNumber: "CARRIER-1",
		ProviderDetail:         "held at depot",
		ProviderMovedAt:        &moved,
	}}
	h := newRouter(svc)

	rec := doRequest(t, h, http.MethodGet, "/admin/v1/fulfillments/ful_1/tracking", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "ful_1", svc.lastTrackedID,
		"the parcel comes from the path and nothing else carries it")

	data, ok := bodyMap(t, rec)["data"].(map[string]any)
	require.True(t, ok, "the answer has to be a single envelope: %s", rec.Body.String())
	assert.Equal(t, "answered", data["answer"])

	local, ok := data["local"].(map[string]any)
	require.True(t, ok, "the local half has to be an object: %s", rec.Body.String())
	assert.Equal(t, "shipped", local["status"])
	assert.Equal(t, "OPERATOR-1", local["tracking_number"])

	provider, ok := data["provider"].(map[string]any)
	require.True(t, ok, "the provider half has to be an object: %s", rec.Body.String())
	assert.Equal(t, "pending", provider["status"])
	assert.Equal(t, "CARRIER-1", provider["tracking_number"])
	assert.Equal(t, "held at depot", provider["detail"])

	assert.Equal(t, false, data["tracking_numbers_agree"],
		"two different numbers do not agree")
}

// TestASilenceCarriesNoProviderObject is the shape rule that keeps a silence from
// reading as an answer.
//
// An empty provider object would be read as "the carrier says: nothing", which is
// exactly the sentence this endpoint exists to prevent. The four non-answers are
// asserted together because they share one rule; what differs between them is the
// service's business and is tested there.
func TestASilenceCarriesNoProviderObject(t *testing.T) {
	for name, answer := range map[string]service.TrackingAnswer{
		"unaskable":   service.TrackingUnaskable,
		"unknown":     service.TrackingUnknown,
		"unreachable": service.TrackingUnreachable,
		"not opened":  service.TrackingNotOpened,
	} {
		t.Run(name, func(t *testing.T) {
			svc := &fakeFulfillments{tracking: service.ShipmentTracking{
				FulfillmentID: "ful_1",
				Answer:        answer,
				Reason:        "because of something an operator can read",
				LocalStatus:   models.StatusPending,
			}}
			h := newRouter(svc)

			rec := doRequest(t, h, http.MethodGet, "/admin/v1/fulfillments/ful_1/tracking", "")

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			data, ok := bodyMap(t, rec)["data"].(map[string]any)
			require.True(t, ok, "the answer has to be a single envelope: %s", rec.Body.String())

			assert.Equal(t, string(answer), data["answer"])
			assert.NotContains(t, data, "provider",
				"a silence must not carry an empty carrier object")
			assert.Equal(t, "because of something an operator can read", data["reason"])
			assert.Contains(t, data, "local", "what the module knows is still reported")
		})
	}
}
