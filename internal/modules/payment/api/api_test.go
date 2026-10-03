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

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/payment/api"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// newTestRouter builds a router running over the fake service.
func newTestRouter(svc *fakePayments) chi.Router {
	r := chi.NewRouter()
	api.New(svc).Routes(r)
	return r
}

// adminPrincipal is the tests' default identity: a fully privileged admin
// user.
//
// The router is built DIRECTLY here, that is, corehttp.RequireAdmin is not in
// the chain and nobody puts the principal into the context. Because the admin
// endpoints are now protected with corehttp.RequireScope, a request without a
// principal returns 401 and the behavior the tests actually verify (envelope,
// status mapping, body decoding) would never get its turn. This is why the
// principal is added by the test itself; WHAT the tests verify does not
// change, only the missing identity is supplied.
func adminPrincipal() corehttp.Principal {
	return corehttp.Principal{
		ID:     "user_test",
		Kind:   "user",
		Scopes: []string{corehttp.ScopeAdmin},
	}
}

// do applies the given request to the router with a fully privileged identity
// and returns the response.
func do(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	return doAs(t, r, method, path, body, adminPrincipal())
}

// doAs applies the given request with the specified identity and returns the
// response.
//
// It is separate for the tests that exercise scope enforcement: [do] always
// calls fully privileged, here a narrowly scoped identity can be given.
func doAs(
	t *testing.T, r chi.Router, method, path, body string, principal corehttp.Principal,
) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), principal))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// decodeResponse turns the response body into a map.
func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

// errorCode returns the code in the error envelope.
//
// The error body is gathered under a single "error" key (see
// corehttp.ErrorResponse); the tests read this shape directly so that a change
// to the envelope does not go unnoticed.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	errBody, ok := decodeResponse(t, rec)["error"].(map[string]any)
	require.True(t, ok, "an error envelope was expected: %s", rec.Body.String())
	code, ok := errBody["code"].(string)
	require.True(t, ok, "the code has to be a string: %s", rec.Body.String())
	return code
}

// TestCreateCollectionReturns201AndEnvelope verifies the happy path.
func TestCreateCollectionReturns201AndEnvelope(t *testing.T) {
	svc := &fakePayments{collection: models.PaymentCollection{
		ID: "paycol_1", Reference: "cart_1", Amount: 1000,
		CurrencyCode: "TRY", Status: models.CollectionNotPaid,
	}}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodPost, "/admin/v1/payment-collections",
		`{"reference":"cart_1","amount":1000,"currency_code":"TRY"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	data, ok := decodeResponse(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "paycol_1", data["id"])
	assert.InDelta(t, 1000, data["amount"], 0)
	assert.Equal(t, "not_paid", data["status"])
	assert.Equal(t, int64(1000), svc.lastCollectionInput.Amount)
}

// TestCreateCollectionRequiresAmount verifies that a missing field produces
// 422.
//
// Using a pointer is deliberate: had a client that does not send the field at
// all been taken to have sent a zero amount, the error message would be
// "amount must be positive" and the client would believe it had sent the
// field.
func TestCreateCollectionRequiresAmount(t *testing.T) {
	r := newTestRouter(&fakePayments{})

	rec := do(t, r, http.MethodPost, "/admin/v1/payment-collections",
		`{"reference":"cart_1","currency_code":"TRY"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestUnknownFieldIsRejected verifies that no field is silently swallowed; a
// swallowed field is a setting the client believes was applied.
func TestUnknownFieldIsRejected(t *testing.T) {
	r := newTestRouter(&fakePayments{})

	rec := do(t, r, http.MethodPost, "/admin/v1/payment-collections",
		`{"reference":"cart_1","amount":1,"currency_code":"TRY","typo":true}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestEmptyBodyIsRejected verifies that an empty request is rejected on the
// endpoints whose body is required.
func TestEmptyBodyIsRejected(t *testing.T) {
	r := newTestRouter(&fakePayments{})

	rec := do(t, r, http.MethodPost, "/admin/v1/payment-collections", "")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestErrorKindsMapToStatusCodes verifies that the handler DOES NOT CHOOSE the
// status code, that the service's error kind is mapped (plan Section 8).
func TestErrorKindsMapToStatusCodes(t *testing.T) {
	tests := map[string]struct {
		err    error
		status int
	}{
		"not found": {notFound(), http.StatusNotFound},
		"invalid": {
			errors.Invalid("payment_invalid_input", "amount must be positive"),
			http.StatusUnprocessableEntity,
		},
		"conflict": {
			errors.Conflict("payment_invalid_transition", "invalid transition"),
			http.StatusConflict,
		},
		"unavailable": {
			errors.Unavailable("payment_provider_down", "the provider could not be reached"),
			http.StatusServiceUnavailable,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := newTestRouter(&fakePayments{err: tt.err})

			rec := do(t, r, http.MethodGet, "/admin/v1/payment-collections/paycol_1", "")

			assert.Equal(t, tt.status, rec.Code, rec.Body.String())
			assert.Equal(t, errors.CodeOf(tt.err), errorCode(t, rec))
		})
	}
}

// TestDeclinedAuthorizationReturns409 verifies that a payment decline is not
// reported as a server error.
//
// Returning 500 would mean whoever wrote the integration looking for the
// problem on their own side; the cause of the decline is not on the server but
// on the card.
func TestDeclinedAuthorizationReturns409(t *testing.T) {
	r := newTestRouter(&fakePayments{
		err: errors.Conflict("payment_authorization_declined", "payment declined"),
	})

	rec := do(t, r, http.MethodPost, "/admin/v1/payment-sessions/payses_1/authorize", "")

	assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Equal(t, "payment_authorization_declined", errorCode(t, rec))
}

// TestListEnvelopeIsConsistent verifies that the list envelope matches the
// shape in plan Section 8.
func TestListEnvelopeIsConsistent(t *testing.T) {
	svc := &fakePayments{
		collections: []models.PaymentCollection{{ID: "paycol_1", Status: models.CollectionNotPaid}},
		count:       7,
	}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodGet, "/admin/v1/payment-collections?limit=1&offset=2", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := decodeResponse(t, rec)
	assert.InDelta(t, 7, body["count"], 0, "count is the FILTER's count, not the page's")
	assert.InDelta(t, 2, body["offset"], 0)
	assert.InDelta(t, 1, body["limit"], 0)
	assert.Len(t, body["data"], 1)
	assert.Equal(t, int64(1), svc.lastListInput.Page.Limit)
	assert.Equal(t, int64(2), svc.lastListInput.Page.Offset)
}

// TestFilterParametersReachTheService verifies that the query parameters reach
// the service.
func TestFilterParametersReachTheService(t *testing.T) {
	svc := &fakePayments{}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodGet,
		"/admin/v1/payment-collections?reference=cart_1&status=captured", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, svc.lastListInput.Reference)
	assert.Equal(t, "cart_1", *svc.lastListInput.Reference)
	require.NotNil(t, svc.lastListInput.Status)
	assert.Equal(t, "captured", *svc.lastListInput.Status)
}

// TestInvalidPagingParameterReturns422 verifies that a limit that is not an
// integer is rejected.
func TestInvalidPagingParameterReturns422(t *testing.T) {
	r := newTestRouter(&fakePayments{})

	rec := do(t, r, http.MethodGet, "/admin/v1/payment-collections?limit=abc", "")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestCreateSessionReturns201AndPassesData verifies that the free-form data in
// the body reaches the service UNALTERED.
//
// Decoding the number as json.Number is critical: an integer turned into a
// float64 can slip into exponent notation when re-encoded and then cannot be
// read as an integer on the provider's side (plan Section 8). The key that
// steers the provider's behavior is sent from the ADMIN endpoint on purpose;
// the store endpoint does not accept it.
func TestCreateSessionReturns201AndPassesData(t *testing.T) {
	svc := &fakePayments{session: models.PaymentSession{
		ID: "payses_1", ProviderID: "manual", Status: models.SessionPending, Amount: 1000,
	}}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodPost, "/admin/v1/payment-collections/paycol_1/payment-sessions",
		`{"provider_id":"manual","idempotency_key":"key-1","data":{"manual_authorized_amount":1000000000000}}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "key-1", svc.lastCreateSession.IdempotencyKey)

	value, ok := svc.lastCreateSession.Data["manual_authorized_amount"].(json.Number)
	require.True(t, ok, "the number has to be decoded as json.Number, got %T",
		svc.lastCreateSession.Data["manual_authorized_amount"])
	assert.Equal(t, "1000000000000", value.String())
}

// TestStoreSessionAcceptsNoAmount verifies that the customer CANNOT decide
// THEMSELVES the amount they will pay.
//
// This was the exact scenario of the finding: a session opened with
// {"amount":1} led to 1 unit being captured from a collection of 50,000 and to
// the order looking paid. The field DOES NOT EXIST AT ALL in the store body;
// it is rejected as an unrecognized field.
func TestStoreSessionAcceptsNoAmount(t *testing.T) {
	svc := &fakePayments{session: models.PaymentSession{ID: "payses_1", Amount: 1000}}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodPost, "/store/v1/payment-collections/paycol_1/payment-sessions",
		`{"provider_id":"manual","idempotency_key":"key-1","amount":1}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Zero(t, svc.lastCreateSession.IdempotencyKey, "the request must NEVER reach the service")
}

// TestStoreSessionCoversTheWholeRemainingAmount verifies that the store
// endpoint DOES NOT PASS an amount to the service; zero means "the whole of
// the collection's remainder".
func TestStoreSessionCoversTheWholeRemainingAmount(t *testing.T) {
	svc := &fakePayments{session: models.PaymentSession{ID: "payses_1", Amount: 1000}}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodPost, "/store/v1/payment-collections/paycol_1/payment-sessions",
		`{"provider_id":"manual","idempotency_key":"key-1"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Zero(t, svc.lastCreateSession.Amount, "the amount must not be taken from the client")
	assert.Equal(t, "key-1", svc.lastCreateSession.IdempotencyKey)
}

// TestStoreSessionRejectsProviderBehaviorKeys verifies that the customer
// cannot write the OUTCOME of their own payment.
//
// The keys are stored with the session and decide the outcome of the
// authorization; had they passed through the store endpoint, the customer
// could have had 1 unit blocked and shown the order as paid. Instead of being
// silently filtered they are REJECTED: a swallowed field is a setting the
// client believes it sent but that is not applied.
func TestStoreSessionRejectsProviderBehaviorKeys(t *testing.T) {
	bodies := map[string]string{
		"partial authorization": `{"provider_id":"manual","idempotency_key":"k",` +
			`"data":{"manual_authorized_amount":1}}`,
		"outcome": `{"provider_id":"manual","idempotency_key":"k","data":{"manual_outcome":"authorize"}}`,
		"decline reason": `{"provider_id":"manual","idempotency_key":"k",` +
			`"data":{"manual_decline_reason":"x"}}`,
	}

	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			svc := &fakePayments{session: models.PaymentSession{ID: "payses_1"}}
			r := newTestRouter(svc)

			rec := do(t, r, http.MethodPost,
				"/store/v1/payment-collections/paycol_1/payment-sessions", body)

			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			assert.Empty(t, svc.lastCreateSession.Data, "the request must NEVER reach the service")
		})
	}
}

// TestStoreSessionPassesProviderSpecificData verifies that the rule, which
// rests on a denylist rather than an allowlist, does not block LEGITIMATE
// data; fields such as a card token have to reach the provider.
func TestStoreSessionPassesProviderSpecificData(t *testing.T) {
	svc := &fakePayments{session: models.PaymentSession{ID: "payses_1"}}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodPost, "/store/v1/payment-collections/paycol_1/payment-sessions",
		`{"provider_id":"manual","idempotency_key":"key-1","data":{"card_token":"tok_1"}}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "tok_1", svc.lastCreateSession.Data["card_token"])
}

// TestCreateSessionMalformedDataReturns422 verifies that a data field that is
// not an object is rejected.
func TestCreateSessionMalformedDataReturns422(t *testing.T) {
	r := newTestRouter(&fakePayments{})

	rec := do(t, r, http.MethodPost, "/admin/v1/payment-collections/paycol_1/payment-sessions",
		`{"provider_id":"manual","idempotency_key":"key-1","data":[1,2]}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestCaptureBodyIsOptional verifies that a capture request without a body
// means "all of it".
//
// Counting an empty body as an error would force the most common call to
// write an unnecessary JSON object.
func TestCaptureBodyIsOptional(t *testing.T) {
	svc := &fakePayments{payment: models.Payment{ID: "pay_1", Amount: 1000}}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodPost, "/admin/v1/payment-sessions/payses_1/capture", "")

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Zero(t, svc.lastCaptureAmount, "a request without a body means a zero amount")
}

// TestCaptureBodyWithAmount verifies that an explicit amount reaches the
// service.
func TestCaptureBodyWithAmount(t *testing.T) {
	svc := &fakePayments{payment: models.Payment{ID: "pay_1", Amount: 400}}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodPost, "/admin/v1/payment-sessions/payses_1/capture", `{"amount":400}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, int64(400), svc.lastCaptureAmount)
}

// TestCancelReturns204 verifies that the compensation endpoint answers success
// without a body.
func TestCancelReturns204(t *testing.T) {
	svc := &fakePayments{}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodPost, "/admin/v1/payment-sessions/payses_1/cancel", "")

	assert.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	assert.True(t, svc.cancelCalled)
	assert.Empty(t, rec.Body.String())
}

// TestRefundRequestReturns201AndPassesArguments verifies that the refund body
// reaches the service.
func TestRefundRequestReturns201AndPassesArguments(t *testing.T) {
	svc := &fakePayments{refund: models.Refund{ID: "refund_1", Amount: 250, Reason: "customer request"}}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodPost, "/admin/v1/payments/pay_1/refunds",
		`{"amount":250,"reason":"customer request"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, int64(250), svc.lastRefundAmount)
	assert.Equal(t, "customer request", svc.lastRefundReason)
}

// TestProviderList verifies that the storefront and the admin see the same
// list.
func TestProviderList(t *testing.T) {
	svc := &fakePayments{providerIDs: []string{"manual"}}
	r := newTestRouter(svc)

	for _, path := range []string{"/admin/v1/payment-providers", "/store/v1/payment-providers"} {
		rec := do(t, r, http.MethodGet, path, "")

		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		body := decodeResponse(t, rec)
		assert.Equal(t, []any{"manual"}, body["data"])
		assert.InDelta(t, 1, body["count"], 0)
	}
}

// TestStoreSurfaceDoesNotExposeCapture verifies that the customer cannot
// trigger a MONEY MOVEMENT from their own browser.
//
// Had there been a capture endpoint on the store side, money could have been
// taken from a cart whose order never came into being; authorization, capture
// and refund belong to the order completion workflow.
//
// Cancellation is NOT on this list and must not be: it moves no money, it
// releases the reservation the customer opened THEMSELVES (see
// TestStoreSurfaceExposesSessionCancel).
func TestStoreSurfaceDoesNotExposeCapture(t *testing.T) {
	r := newTestRouter(&fakePayments{})

	for _, path := range []string{
		"/store/v1/payment-sessions/payses_1/authorize",
		"/store/v1/payment-sessions/payses_1/capture",
		"/store/v1/payments/pay_1/refunds",
	} {
		rec := do(t, r, http.MethodPost, path, "")

		assert.Equal(t, http.StatusNotFound, rec.Code, "%s must not be open to the store", path)
	}
}

// TestStoreSurfaceExposesSessionCancel verifies that the customer can release
// their own payment session.
//
// Regression: the reservation that prevents a double capture is held at the
// collection level — an open session takes up the collection's remaining
// amount. While the storefront had no way to release it, a customer who picked
// "credit card" and then wanted to switch to "bank transfer" stayed locked
// until an ADMINISTRATOR canceled the session by hand.
//
// That a cancellation is not a money movement is what sets it apart from
// authorize/capture/refund; those three stay closed to the store.
func TestStoreSurfaceExposesSessionCancel(t *testing.T) {
	fake := &fakePayments{}
	r := newTestRouter(fake)

	rec := do(t, r, http.MethodPost, "/store/v1/payment-sessions/payses_1/cancel", "")

	assert.Equal(t, http.StatusNoContent, rec.Code,
		"the customer has to be able to release their own reservation; otherwise the payment method cannot be changed")
}

// TestSessionDTOCarriesDeclineReason verifies that the diagnostic field shows
// in the response; hiding the field would mean whoever writes the integration
// never gets to see the reason for the decline.
func TestSessionDTOCarriesDeclineReason(t *testing.T) {
	svc := &fakePayments{session: models.PaymentSession{
		ID: "payses_1", Status: models.SessionFailed, DeclineReason: "insufficient funds",
	}}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodGet, "/admin/v1/payment-sessions/payses_1", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, ok := decodeResponse(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "failed", data["status"])
	assert.Equal(t, "insufficient funds", data["decline_reason"])
}

// readOnlyPrincipal returns an admin identity carrying only [api.ScopeRead].
func readOnlyPrincipal() corehttp.Principal {
	return corehttp.Principal{
		ID:     "user_readonly",
		Kind:   "user",
		Scopes: []string{api.ScopeRead},
	}
}

// TestReadOnlyPrincipalGets403OnWriteEndpoint verifies that the read scope
// does not suffice for writing.
//
// The endpoint here MOVES MONEY OUT. Without scope enforcement, authentication
// alone would stand in for authorization: an identity granted only to read
// reports could make a refund out of the till.
func TestReadOnlyPrincipalGets403OnWriteEndpoint(t *testing.T) {
	svc := &fakePayments{}
	r := newTestRouter(svc)

	rec := doAs(t, r, http.MethodPost, "/admin/v1/payments/pay_1/refunds",
		`{"amount":500,"reason":"customer refund"}`, readOnlyPrincipal())

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, corehttp.CodeForbidden, errorCode(t, rec))
	assert.Zero(t, svc.lastRefundAmount, "when the scope is insufficient the service must not be reached at all")
}

// TestReadOnlyPrincipalPassesOnReadEndpoint verifies that the same identity
// passes on a read endpoint.
//
// This is the test that accompanies it as a pair: an endpoint returning 403
// could also have stemmed from the scope map being too narrow. That the same
// identity passes on the read shows that the refusal comes from the scope
// DISTINCTION.
func TestReadOnlyPrincipalPassesOnReadEndpoint(t *testing.T) {
	svc := &fakePayments{
		collections: []models.PaymentCollection{{ID: "pcol_1", Amount: 1000, CurrencyCode: "TRY"}},
		count:       1,
	}
	r := newTestRouter(svc)

	rec := doAs(t, r, http.MethodGet, "/admin/v1/payment-collections", "", readOnlyPrincipal())

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, float64(1), decodeResponse(t, rec)["count"])
}

// TestPrincipalWithoutScopesCannotOpenAdminEndpoint verifies that an admin
// user whose scopes were left EMPTY can reach no admin endpoint.
//
// The identity is valid — it can log in, it is known who it is — but it has no
// scope. Without this distinction, a user whose scope list was left empty
// would be believed to "reach nothing" while being able to trigger a capture.
func TestPrincipalWithoutScopesCannotOpenAdminEndpoint(t *testing.T) {
	noScopes := corehttp.Principal{ID: "user_empty", Kind: "user", Scopes: []string{}}

	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{name: "read", method: http.MethodGet, path: "/admin/v1/payment-collections"},
		{name: "write", method: http.MethodPost, path: "/admin/v1/payment-sessions/pses_1/capture"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakePayments{}
			r := newTestRouter(svc)

			rec := doAs(t, r, tc.method, tc.path, "", noScopes)

			assert.Equal(t, http.StatusForbidden, rec.Code)
			assert.Zero(t, svc.lastCaptureAmount, "for a principal without scopes the service must not be reached at all")
		})
	}
}

// TestRequestWithoutPrincipalGets401 verifies that a request with no identity
// at all gets 401, NOT 403.
//
// The distinction is meaningful for the client: 401 means "tell me who you
// are" (try again with an identity), 403 means "I know who you are but you
// have no scope" (there is no point in trying again).
func TestRequestWithoutPrincipalGets401(t *testing.T) {
	r := newTestRouter(&fakePayments{})

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/payment-collections", strings.NewReader(""))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestStoreEndpointsRequireNoScope verifies that the store endpoints work with
// an identity that carries no scope.
//
// The identity of /store/v1 is the publishable key and that key by definition
// CARRIES NO scope. Closing the store endpoint too while adding the admin
// scopes would have brought the whole storefront down; the risk is real,
// because the provider list and the collection read endpoints share the SAME
// handler on the two surfaces.
func TestStoreEndpointsRequireNoScope(t *testing.T) {
	svc := &fakePayments{
		providerIDs: []string{"manual"},
		collection:  models.PaymentCollection{ID: "pcol_1", Amount: 1000, CurrencyCode: "TRY"},
	}
	storefront := corehttp.Principal{ID: "pk_1", Kind: "api_key", Scopes: []string{}}
	r := newTestRouter(svc)

	for _, path := range []string{
		"/store/v1/payment-providers",
		"/store/v1/payment-collections/pcol_1",
	} {
		t.Run(path, func(t *testing.T) {
			rec := doAs(t, r, http.MethodGet, path, "", storefront)

			assert.Equal(t, http.StatusOK, rec.Code)
		})
	}
}

// --- read surface ------------------------------------------------------------
//
// The four endpoints below (the collection's sessions, the collection's
// payments, the payment itself, the payment's refunds) are the operator's only
// view of the MONEY RECORDS. What sets them apart from the write endpoints is
// that nobody hears about it when they fail: an endpoint reading the wrong
// record still returns 200, and so does a list that returns null instead of
// empty. That is why each of them is tested separately.

// TestReadEndpointsTakeTheRecordIDFromThePath verifies that the read endpoints
// ask for the record in the URL.
//
// This is a read handler's only job: passing the ID from the path on to the
// service. Were the ID lost (dropped to an empty string) or read from another
// path segment, the endpoint would still return 200 and the envelope would
// still look right — the operator would believe they got the answer for the
// payment they asked about. With money records, that means looking for a
// refund under another payment.
func TestReadEndpointsTakeTheRecordIDFromThePath(t *testing.T) {
	tests := map[string]struct {
		path    string
		wantID  string
		askedID func(*fakePayments) string
	}{
		"the collection's sessions": {
			path:    "/admin/v1/payment-collections/paycol_9/payment-sessions",
			wantID:  "paycol_9",
			askedID: func(f *fakePayments) string { return f.lastSessionListID },
		},
		"the collection's payments": {
			path:    "/admin/v1/payment-collections/paycol_9/payments",
			wantID:  "paycol_9",
			askedID: func(f *fakePayments) string { return f.lastPaymentListID },
		},
		"the payment itself": {
			path:    "/admin/v1/payments/pay_9",
			wantID:  "pay_9",
			askedID: func(f *fakePayments) string { return f.lastPaymentID },
		},
		"the payment's refunds": {
			path:    "/admin/v1/payments/pay_9/refunds",
			wantID:  "pay_9",
			askedID: func(f *fakePayments) string { return f.lastRefundListID },
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			svc := &fakePayments{}
			r := newTestRouter(svc)

			rec := do(t, r, http.MethodGet, tt.path, "")

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, tt.wantID, tt.askedID(svc),
				"the endpoint asked for some other ID, not the record in the path")
		})
	}
}

// TestEmptyReadListsReturnAnEmptyArray verifies that a list with no records
// returns an empty array, NOT null.
//
// In Go a nil slice is encoded as null in JSON. If the envelope's "data" field
// comes back null, every loop on the client side either blows up or is
// silently skipped; worse, "this payment has no refund" and "the refund list
// could not be fetched" become indistinguishable. That is exactly the
// operator's reconciliation question, and the two answers mean different
// things.
func TestEmptyReadListsReturnAnEmptyArray(t *testing.T) {
	paths := map[string]string{
		"the collection's sessions": "/admin/v1/payment-collections/paycol_1/payment-sessions",
		"the collection's payments": "/admin/v1/payment-collections/paycol_1/payments",
		"the payment's refunds":     "/admin/v1/payments/pay_1/refunds",
	}

	for name, path := range paths {
		t.Run(name, func(t *testing.T) {
			r := newTestRouter(&fakePayments{})

			rec := do(t, r, http.MethodGet, path, "")

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			body := decodeResponse(t, rec)
			assert.Equal(t, []any{}, body["data"],
				"with no records the list has to be an empty array, not null: %s", rec.Body.String())
			assert.InDelta(t, 0, body["count"], 0)
		})
	}
}

// TestReadEndpointsKeepTheServiceErrorKind verifies that the read endpoints
// too DO NOT CHOOSE the status code.
//
// When the sessions of a collection that does not exist are asked for, the
// answer has to be 404; returning 500 would tell an operator who mistyped the
// ID "the server broke" and send them to open an incident instead of looking
// for their own mistake. This rule, already tested on a single read endpoint,
// the collection itself (see TestErrorKindsMapToStatusCodes), is verified
// separately on these four: had all four of them set out to write the error
// by their own hand, none of the other tests would have broken.
func TestReadEndpointsKeepTheServiceErrorKind(t *testing.T) {
	paths := map[string]string{
		"the collection's sessions": "/admin/v1/payment-collections/paycol_missing/payment-sessions",
		"the collection's payments": "/admin/v1/payment-collections/paycol_missing/payments",
		"the payment itself":        "/admin/v1/payments/pay_missing",
		"the payment's refunds":     "/admin/v1/payments/pay_missing/refunds",
	}

	for name, path := range paths {
		t.Run(name, func(t *testing.T) {
			r := newTestRouter(&fakePayments{err: notFound()})

			rec := do(t, r, http.MethodGet, path, "")

			assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
			assert.Equal(t, "payment_collection_not_found", errorCode(t, rec),
				"the error code has to be carried as it came from the service")
		})
	}
}

// TestRefundListCarriesAmountAndReasonForReconciliation verifies that a refund
// that was made shows up IN FULL on the list.
//
// This endpoint is the only answer to the operator's question "how much went
// back from this payment, and why". A DTO that drops the amount turns partial
// refunds into a table whose total does not add up; a DTO that drops the
// reason makes two refunds indistinguishable from each other. Both are silent:
// the answer is still 200 and still a full list. The envelope's "count" field,
// too, is the number of rows returned, not of the page, and on endpoints that
// are not paged it is the only number the client sees.
func TestRefundListCarriesAmountAndReasonForReconciliation(t *testing.T) {
	svc := &fakePayments{refunds: []models.Refund{
		{ID: "refund_2", PaymentID: "pay_1", Amount: 250, Reason: "customer request"},
		{ID: "refund_1", PaymentID: "pay_1", Amount: 750, Reason: "damaged item"},
	}}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodGet, "/admin/v1/payments/pay_1/refunds", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := decodeResponse(t, rec)
	assert.InDelta(t, 2, body["count"], 0, "count has to be the number of rows returned")

	data, ok := body["data"].([]any)
	require.True(t, ok, "a refund list was expected: %s", rec.Body.String())
	require.Len(t, data, 2)

	first, ok := data[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "refund_2", first["id"])
	assert.Equal(t, "pay_1", first["payment_id"], "the refund has to carry which payment it belongs to")
	assert.InDelta(t, 250, first["amount"], 0, "the refund amount has to show in the response")
	assert.Equal(t, "customer request", first["reason"], "the refund's reason has to show in the response")

	second, ok := data[1].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 750, second["amount"], 0)
	assert.Equal(t, "damaged item", second["reason"])
}

// TestPaymentDetailShowsTheRefundedAmount verifies that the single read of a
// payment carries the refunded amount.
//
// The answer to "how much of this payment do we still hold" is a single field,
// and it is read here, not from the list. If the field drops, the payment
// keeps showing with its full amount — that is, a fully refunded payment
// cannot be told apart from one that was never refunded. That the answer comes
// back in the single envelope (not the list envelope) is pinned here too; a
// switch between the two shapes would silently void the client's read.
func TestPaymentDetailShowsTheRefundedAmount(t *testing.T) {
	svc := &fakePayments{payment: models.Payment{
		ID: "pay_1", PaymentSessionID: "payses_1", PaymentCollectionID: "paycol_1",
		Amount: 1000, CurrencyCode: "TRY", RefundedAmount: 400,
	}}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodGet, "/admin/v1/payments/pay_1", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, ok := decodeResponse(t, rec)["data"].(map[string]any)
	require.True(t, ok, "a single envelope was expected: %s", rec.Body.String())
	assert.Equal(t, "pay_1", data["id"])
	assert.InDelta(t, 1000, data["amount"], 0)
	assert.InDelta(t, 400, data["refunded_amount"], 0,
		"if the refunded amount does not show, a fully refunded payment looks untouched")
	assert.Equal(t, "paycol_1", data["payment_collection_id"])
}

// TestSessionListReturnsTheCollectionsSessions verifies that a collection's
// session list carries the rows.
//
// A collection having more than one session is normal: every declined or
// released attempt leaves a row behind, and the question "why could the
// customer not pay" can only be answered by looking at this list. If the list
// showed only the last one, or only the open one, that question would go
// unanswered.
func TestSessionListReturnsTheCollectionsSessions(t *testing.T) {
	svc := &fakePayments{sessions: []models.PaymentSession{
		{ID: "payses_2", PaymentCollectionID: "paycol_1", Status: models.SessionPending, Amount: 1000},
		{
			ID: "payses_1", PaymentCollectionID: "paycol_1", Status: models.SessionFailed,
			Amount: 1000, DeclineReason: "insufficient funds",
		},
	}}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodGet,
		"/admin/v1/payment-collections/paycol_1/payment-sessions", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := decodeResponse(t, rec)
	assert.InDelta(t, 2, body["count"], 0)

	data, ok := body["data"].([]any)
	require.True(t, ok, "a session list was expected: %s", rec.Body.String())
	require.Len(t, data, 2, "the failed attempt has to stay on the list too")

	failed, ok := data[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "failed", failed["status"])
	assert.Equal(t, "insufficient funds", failed["decline_reason"],
		"the reason for the decline has to show on the list too; diagnosis must not be left to reading sessions one by one")
}

// TestPaymentListReturnsTheCollectionsPayments verifies that a collection's
// payment list carries the rows.
//
// A partial capture produces several rows, and the collection's
// captured_amount field is their SUM. If the sum and the rows diverge, that
// can only be seen by looking at the list; a single total does not say which
// payment was written short.
func TestPaymentListReturnsTheCollectionsPayments(t *testing.T) {
	svc := &fakePayments{payments: []models.Payment{
		{ID: "pay_2", PaymentCollectionID: "paycol_1", Amount: 400, CurrencyCode: "TRY"},
		{ID: "pay_1", PaymentCollectionID: "paycol_1", Amount: 600, CurrencyCode: "TRY"},
	}}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodGet, "/admin/v1/payment-collections/paycol_1/payments", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := decodeResponse(t, rec)
	assert.InDelta(t, 2, body["count"], 0)

	data, ok := body["data"].([]any)
	require.True(t, ok, "a payment list was expected: %s", rec.Body.String())
	require.Len(t, data, 2, "ALL the partial captures have to be on the list")

	var total float64
	for i := range data {
		row, rowOK := data[i].(map[string]any)
		require.True(t, rowOK)
		amount, amountOK := row["amount"].(float64)
		require.True(t, amountOK, "the amount field has to be present: %s", rec.Body.String())
		total += amount
	}
	assert.InDelta(t, 1000, total, 0,
		"the sum of the rows has to give the collection's captured amount")
}

// --- store credit ------------------------------------------------------------

// The store credit admin endpoints (ADR 0152).
//
// This layer's only job is translation: read the body, pass it to the service,
// write the envelope. That is why each test holds WHAT REACHES the service or
// WHAT the client SEES.

// TestIssueCreditBodyReachesTheService nails down the handler's only job.
func TestIssueCreditBodyReachesTheService(t *testing.T) {
	svc := &fakePayments{creditEntry: models.StoreCreditEntry{
		ID:           "scredit_1",
		CustomerID:   "cus_1",
		CurrencyCode: "TRY",
		Amount:       5_000,
		Kind:         models.StoreCreditIssue,
		Reason:       "credit in place of a refund",
	}}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodPost, "/admin/v1/store-credits",
		`{"customer_id":"cus_1","currency_code":"TRY","amount":5000,"reason":"credit in place of a refund"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "cus_1", svc.lastCreditInput.CustomerID)
	assert.Equal(t, int64(5_000), svc.lastCreditInput.Amount)
	assert.Equal(t, "credit in place of a refund", svc.lastCreditInput.Reason,
		"the reason has to REACH the service: its validation is there, and if it were "+
			"dropped here every credit would look as if it had no reason")

	var envelope struct {
		Data struct {
			ID     string `json:"id"`
			Amount int64  `json:"amount"`
			Kind   string `json:"kind"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	assert.Equal(t, "scredit_1", envelope.Data.ID)
	assert.Equal(t, "issue", envelope.Data.Kind)
}

// TestBalanceEndpointPassesTheLedgerAsked holds which ledger the balance
// belongs to.
//
// The customer and the currency together name one ledger; if either were
// dropped, the answer would show somebody else's money or another currency.
func TestBalanceEndpointPassesTheLedgerAsked(t *testing.T) {
	svc := &fakePayments{creditBalance: 7_500}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodGet,
		"/admin/v1/store-credits/balance?customer_id=cus_1&currency_code=try", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, [2]string{"cus_1", "try"}, svc.lastCreditQuery)

	var envelope struct {
		Data struct {
			CustomerID   string `json:"customer_id"`
			CurrencyCode string `json:"currency_code"`
			Balance      int64  `json:"balance"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	assert.Equal(t, int64(7_500), envelope.Data.Balance)
	assert.Equal(t, "TRY", envelope.Data.CurrencyCode,
		"the answer has to say WHICH ledger it read: a client sending 'try' has to see 'TRY'")
}

// TestCreditHistoryReturnsTheListEnvelope nails down the envelope's shape.
func TestCreditHistoryReturnsTheListEnvelope(t *testing.T) {
	svc := &fakePayments{creditHistory: []models.StoreCreditEntry{
		{ID: "scredit_2", Kind: models.StoreCreditHold, Amount: -5_000},
		{ID: "scredit_1", Kind: models.StoreCreditIssue, Amount: 5_000},
	}}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodGet,
		"/admin/v1/store-credits?customer_id=cus_1&currency_code=TRY", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var envelope struct {
		Data []struct {
			ID     string `json:"id"`
			Amount int64  `json:"amount"`
			Kind   string `json:"kind"`
		} `json:"data"`
		Count int64 `json:"count"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))

	assert.Equal(t, int64(2), envelope.Count)
	require.Len(t, envelope.Data, 2)
	assert.Equal(t, "hold", envelope.Data[0].Kind)
	assert.Equal(t, int64(-5_000), envelope.Data[0].Amount,
		"a hold reads NEGATIVE: the balance is the sum of the rows and the client reads it that way too")
}

// TestIssueCreditIsClosedToTheREADScope nails down that the scope is attached
// to the route.
//
// Issuing credit CREATES MONEY the customer can spend — money that will leave
// the shop's till. Had an identity granted only to read reports been able to
// do this, authentication alone would stand in for authorization.
func TestIssueCreditIsClosedToTheREADScope(t *testing.T) {
	svc := &fakePayments{}
	r := newTestRouter(svc)

	rec := doAs(t, r, http.MethodPost, "/admin/v1/store-credits",
		`{"customer_id":"cus_1","currency_code":"TRY","amount":5000,"reason":"x"}`,
		readOnlyPrincipal())

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, svc.lastCreditInput.CustomerID, "when the scope is insufficient the service must not be reached at all")
}

// TestCreditReadsPassWithTheREADScope shows that the refusal comes from the
// scope DISTINCTION.
//
// This is the accompanying pair: a lone 403 could also have come from the
// scope map being too narrow across the board. The same identity passing on
// the two read endpoints says that the distinction lies on the write/read
// axis.
func TestCreditReadsPassWithTheREADScope(t *testing.T) {
	for name, path := range map[string]string{
		"balance": "/admin/v1/store-credits/balance?customer_id=cus_1&currency_code=TRY",
		"history": "/admin/v1/store-credits?customer_id=cus_1&currency_code=TRY",
	} {
		t.Run(name, func(t *testing.T) {
			r := newTestRouter(&fakePayments{})

			rec := doAs(t, r, http.MethodGet, path, "", readOnlyPrincipal())

			assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		})
	}
}

// TestTheLoyaltyBalanceEndpointNamesTheLedgerItRead holds which ledger was read.
//
// The customer and the currency name one ledger together. If either is dropped
// the answer would be about somebody else's points, or about a currency the
// caller did not ask for, and the answer repeats them so a client can see which.
func TestTheLoyaltyBalanceEndpointNamesTheLedgerItRead(t *testing.T) {
	svc := &fakePayments{loyaltyBalance: 340}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodGet,
		"/admin/v1/loyalty-points/balance?customer_id=cus_1&currency_code=try", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, [2]string{"cus_1", "try"}, svc.lastLoyaltyQuery,
		"the query keys the handler reads are asserted nowhere else: the arch audit "+
			"that holds described-against-read deliberately does not reach this package")

	var envelope struct {
		Data struct {
			CustomerID   string `json:"customer_id"`
			CurrencyCode string `json:"currency_code"`
			Points       int64  `json:"points"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	assert.Equal(t, int64(340), envelope.Data.Points)
	assert.Equal(t, "cus_1", envelope.Data.CustomerID)
	assert.Equal(t, "TRY", envelope.Data.CurrencyCode,
		"the answer says WHICH ledger it read: a client sending 'try' sees 'TRY'")
}

// TestTheLoyaltyHistoryReturnsTheListEnvelope nails the envelope's shape.
func TestTheLoyaltyHistoryReturnsTheListEnvelope(t *testing.T) {
	svc := &fakePayments{loyaltyHistory: []models.LoyaltyEntry{
		{ID: "lpoint_2", Kind: models.LoyaltyReverse, Points: -25, Reference: "paycol_1"},
		{ID: "lpoint_1", Kind: models.LoyaltyEarn, Points: 100, Reference: "paycol_1"},
	}}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodGet,
		"/admin/v1/loyalty-points?customer_id=cus_1&currency_code=TRY&limit=5&offset=10", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, int64(5), svc.lastLoyaltyPage.Limit, "the paging reaches the service")
	assert.Equal(t, int64(10), svc.lastLoyaltyPage.Offset)

	var envelope struct {
		Data []struct {
			ID        string `json:"id"`
			Points    int64  `json:"points"`
			Kind      string `json:"kind"`
			Reference string `json:"reference"`
		} `json:"data"`
		Count  int64 `json:"count"`
		Offset int64 `json:"offset"`
		Limit  int64 `json:"limit"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))

	assert.Equal(t, int64(2), envelope.Count)
	require.Len(t, envelope.Data, 2)
	assert.Equal(t, "reverse", envelope.Data[0].Kind)
	assert.Equal(t, int64(-25), envelope.Data[0].Points,
		"a reversal reads NEGATIVE: the balance is the sum of the rows and a client reads it the same way")
	assert.Equal(t, "paycol_1", envelope.Data[0].Reference,
		"the row names the collection it came from, which is what makes the history readable")
}

// TestTheLoyaltyReadsAreOpenToTheReadScope holds the privilege on the route.
//
// Both endpoints are READS, so an identity holding only payment:read must reach
// them. There is no write half to check, and that is the record's own decision:
// in this module a write means money moved, and a points adjustment does not.
func TestTheLoyaltyReadsAreOpenToTheReadScope(t *testing.T) {
	for _, path := range []string{
		"/admin/v1/loyalty-points?customer_id=cus_1&currency_code=TRY",
		"/admin/v1/loyalty-points/balance?customer_id=cus_1&currency_code=TRY",
	} {
		t.Run(path, func(t *testing.T) {
			r := newTestRouter(&fakePayments{})

			rec := doAs(t, r, http.MethodGet, path, "", readOnlyPrincipal())

			assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		})
	}
}

// TestACreditNamesTheOrderItCompensates is ADR 0274 at the door: the issue
// carries the order to the service, the row says which order it names, and
// the history is read for one order.
func TestACreditNamesTheOrderItCompensates(t *testing.T) {
	svc := &fakePayments{creditEntry: models.StoreCreditEntry{
		ID: "scredit_1", CustomerID: "cus_1", CurrencyCode: "TRY", Amount: 5_000,
		Kind: models.StoreCreditIssue, Reason: "a late delivery", OrderID: "order_7",
	}}
	r := newTestRouter(svc)

	rec := do(t, r, http.MethodPost, "/admin/v1/store-credits",
		`{"customer_id":"cus_1","currency_code":"TRY","amount":5000,"reason":"a late delivery","order_id":"order_7"}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "order_7", svc.lastCreditInput.OrderID)
	var envelope struct {
		Data struct {
			OrderID string `json:"order_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	assert.Equal(t, "order_7", envelope.Data.OrderID)

	rec = do(t, r, http.MethodGet, "/admin/v1/store-credits?customer_id=cus_1&currency_code=TRY&order_id=order_7", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "order_7", svc.lastCreditList.OrderID, "the history is read for the order named")
}
