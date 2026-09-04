package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
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
