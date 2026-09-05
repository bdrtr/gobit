package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/order/api"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// fakeOrders is the test counterpart of api.Orders.
//
// The responsibility of the HTTP layer is narrow: decode the body, call the
// service, write the envelope and the status code. This is why the fake service
// carries no business logic; it only returns the recorded call and the response
// set up in advance.
type fakeOrders struct {
	order    models.Order
	detail   models.OrderDetail
	orders   []models.Order
	count    int64
	ret      models.Return
	exchange models.Exchange
	claim    models.Claim
	returns  []models.Return

	// err, when set, makes every method return this error; it is used to
	// exercise the error mapping.
	err error

	// The arguments of the last call.
	listInput     service.ListOrdersInput
	returnInput   service.CreateReturnInput
	exchangeInput service.CreateExchangeInput
	claimInput    service.CreateClaimInput
	page          service.Page
	gotOrderID    string
	gotChildID    string
	gotReason     string
	// calls records the called methods IN ORDER; it is needed to verify that
	// the cancel endpoint performs the read as well.
	calls []string
	// payment is the live payment view the fake reports.
	payment service.OrderPayment
	// paymentBound reports whether a collection is bound at all.
	paymentBound bool
	// paymentErr, when set, makes PaymentOf fail.
	paymentErr error
}

// That the fake satisfies the surface the handler expects is verified at
// compile time.
var _ api.Orders = (*fakeOrders)(nil)

// record appends the call to the sequence.
func (f *fakeOrders) record(name string) { f.calls = append(f.calls, name) }

// GetOrder returns the order with its children.
func (f *fakeOrders) GetOrder(_ context.Context, orderID string) (models.OrderDetail, error) {
	f.record("GetOrder")
	f.gotOrderID = orderID
	return f.detail, f.err
}

// ListOrders returns the orders.
func (f *fakeOrders) ListOrders(_ context.Context, in service.ListOrdersInput) ([]models.Order, int64, error) {
	f.record("ListOrders")
	f.listInput = in
	return f.orders, f.count, f.err
}

// CancelOrder cancels the order.
func (f *fakeOrders) CancelOrder(_ context.Context, orderID, reason string) error {
	f.record("CancelOrder")
	f.gotOrderID = orderID
	f.gotReason = reason
	return f.err
}

// PaymentOf returns the scripted live payment view.
func (f *fakeOrders) PaymentOf(
	_ context.Context, _ string,
) (service.OrderPayment, bool, error) {
	if f.paymentErr != nil {
		return service.OrderPayment{}, false, f.paymentErr
	}

	return f.payment, f.paymentBound, nil
}

// CompleteOrder completes the order.
func (f *fakeOrders) CompleteOrder(_ context.Context, orderID string) (models.Order, error) {
	f.record("CompleteOrder")
	f.gotOrderID = orderID
	return f.order, f.err
}

// ArchiveOrder archives the order.
func (f *fakeOrders) ArchiveOrder(_ context.Context, orderID string) (models.Order, error) {
	f.record("ArchiveOrder")
	f.gotOrderID = orderID
	return f.order, f.err
}

// CreateReturn opens a return record.
func (f *fakeOrders) CreateReturn(_ context.Context, in service.CreateReturnInput) (models.Return, error) {
	f.record("CreateReturn")
	f.returnInput = in
	return f.ret, f.err
}

// GetReturn returns the return record.
func (f *fakeOrders) GetReturn(_ context.Context, returnID string) (models.Return, error) {
	f.record("GetReturn")
	f.gotChildID = returnID
	return f.ret, f.err
}

// ListReturns returns the return records.
func (f *fakeOrders) ListReturns(_ context.Context, orderID string, page service.Page) ([]models.Return, int64, error) {
	f.record("ListReturns")
	f.gotOrderID = orderID
	f.page = page
	return f.returns, f.count, f.err
}

// CreateExchange opens an exchange record.
func (f *fakeOrders) CreateExchange(_ context.Context, in service.CreateExchangeInput) (models.Exchange, error) {
	f.record("CreateExchange")
	f.exchangeInput = in
	return f.exchange, f.err
}

// GetExchange returns the exchange record.
func (f *fakeOrders) GetExchange(_ context.Context, exchangeID string) (models.Exchange, error) {
	f.record("GetExchange")
	f.gotChildID = exchangeID
	return f.exchange, f.err
}

// ListExchanges returns the exchange records.
func (f *fakeOrders) ListExchanges(_ context.Context, orderID string, page service.Page) ([]models.Exchange, int64, error) {
	f.record("ListExchanges")
	f.gotOrderID = orderID
	f.page = page
	return nil, f.count, f.err
}

// CreateClaim opens a claim record.
func (f *fakeOrders) CreateClaim(_ context.Context, in service.CreateClaimInput) (models.Claim, error) {
	f.record("CreateClaim")
	f.claimInput = in
	return f.claim, f.err
}

// GetClaim returns the claim record.
func (f *fakeOrders) GetClaim(_ context.Context, claimID string) (models.Claim, error) {
	f.record("GetClaim")
	f.gotChildID = claimID
	return f.claim, f.err
}

// ListClaims returns the claim records.
func (f *fakeOrders) ListClaims(_ context.Context, orderID string, page service.Page) ([]models.Claim, int64, error) {
	f.record("ListClaims")
	f.gotOrderID = orderID
	f.page = page
	return nil, f.count, f.err
}

// sampleOrder is the order model used in the tests.
func sampleOrder() models.Order {
	return models.Order{
		ID:            "order_1",
		DisplayID:     1042,
		Status:        models.OrderPending,
		RegionID:      "reg_1",
		CustomerID:    "cus_1",
		Email:         "customer@example.com",
		CurrencyCode:  "TRY",
		CartID:        "cart_1",
		Subtotal:      3000,
		TaxTotal:      600,
		ShippingTotal: 2500,
		Total:         6100,
		PlacedAt:      time.Unix(0, 0).UTC(),
		CreatedAt:     time.Unix(0, 0).UTC(),
		UpdatedAt:     time.Unix(0, 0).UTC(),
	}
}

// sampleDetail is the order detail used in the tests.
func sampleDetail() models.OrderDetail {
	return models.OrderDetail{
		Order: sampleOrder(),
		Items: []models.OrderLineItem{{
			ID: "oli_1", OrderID: "order_1", VariantID: "variant_1",
			Title: "Red T-Shirt", Quantity: 3, UnitPrice: 1000,
			Subtotal: 3000, TaxTotal: 600, Total: 3600,
		}},
		Summary: models.OrderSummary{
			ID: "osum_1", OrderID: "order_1", PaidTotal: 6100,
		},
	}
}

// newRouter produces a router wired to the fake service.
func newRouter(svc api.Orders) chi.Router {
	return newRouterWithFlow(svc, &fakeReceiving{})
}

// newRouterWithFlow wires a router with the given service and return flow.
//
// The flow may be nil; the receive endpoint failing CLOSED without it can only
// be exercised that way.
func newRouterWithFlow(svc api.Orders, receiving api.ReturnReceiving) chi.Router {
	r := chi.NewRouter()
	api.New(svc, receiving).Routes(r)
	return r
}

// adminPrincipal is the default identity of the tests: a fully privileged admin
// user.
//
// The router is built DIRECTLY here, that is, corehttp.RequireAdmin is not in
// the chain and there is nobody to put the principal into the context. Because
// the admin endpoints are now protected with corehttp.RequireScope, a request
// without a principal returns 401 and the behavior the tests actually verify
// (envelope, status mapping, body decoding) would never get its turn. This is
// why the principal is added by the test itself; WHAT the tests verify does not
// change, only the missing identity is supplied.
func adminPrincipal() corehttp.Principal {
	return corehttp.Principal{
		ID:     "user_test",
		Kind:   "user",
		Scopes: []string{corehttp.ScopeAdmin},
	}
}

// doRequest calls the given path with a fully privileged principal and returns
// the response.
func doRequest(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	return doRequestAs(t, r, method, path, body, adminPrincipal())
}

// doRequestAs calls the given path with the specified principal and returns the
// response.
//
// It is separate for the tests that exercise scope enforcement: [doRequest]
// always calls fully privileged, here a narrowly scoped principal can be given.
func doRequestAs(
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
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// decodeResponse decodes the response body into a map.
func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

// TestOrderCreationEndpointDoesNotExist verifies that an order cannot be opened
// over HTTP.
//
// Had the endpoint been open, a client could have written an order with a total
// it determined itself — with a total of zero, for example; that the amounts
// correspond to the REAL prices can only be guaranteed by the complete_cart
// workflow.
func TestOrderCreationEndpointDoesNotExist(t *testing.T) {
	svc := &fakeOrders{}
	r := newRouter(svc)

	// Only GET is defined on /admin/v1/orders; chi turns the POST away with 405.
	rec := doRequest(t, r, http.MethodPost, "/admin/v1/orders", `{"region_id":"reg_1"}`)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	// On the customer side this path has no method at all; the result is 404.
	rec = doRequest(t, r, http.MethodPost, "/store/v1/orders", `{"region_id":"reg_1"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	assert.Empty(t, svc.calls, "no call should reach the service")
}

// TestStoreListEndpointDoesNotExist verifies that there is no list endpoint on
// the customer side.
//
// A list endpoint would turn knowing a single order id into reading every
// order; because authorization is left to Phase 8, this door is not opened at
// all today.
func TestStoreListEndpointDoesNotExist(t *testing.T) {
	svc := &fakeOrders{}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodGet, "/store/v1/orders", "")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Empty(t, svc.calls)
}

// TestAdminGetOrderEnvelope verifies the envelope of the single read.
func TestAdminGetOrderEnvelope(t *testing.T) {
	svc := &fakeOrders{detail: sampleDetail()}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodGet, "/admin/v1/orders/order_1", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "order_1", svc.gotOrderID)

	body := decodeResponse(t, rec)
	data, ok := body["data"].(map[string]any)
	require.True(t, ok, "the response has to come back in a data envelope")
	assert.Equal(t, float64(1042), data["display_id"])
	assert.Equal(t, "pending", data["status"])
	assert.Equal(t, float64(6100), data["total"])

	items, ok := data["items"].([]any)
	require.True(t, ok)
	assert.Len(t, items, 1)

	summary, ok := data["summary"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(6100), summary["paid_total"])
	assert.Equal(t, float64(0), summary["outstanding"],
		"the outstanding amount has to be presented as derived")
}

// TestStoreGetOrderReturnsSameEnvelope verifies that the customer endpoint
// returns the same envelope.
func TestStoreGetOrderReturnsSameEnvelope(t *testing.T) {
	svc := &fakeOrders{detail: sampleDetail()}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodGet, "/store/v1/orders/order_1", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"GetOrder"}, svc.calls)
}

// TestAdminListOrders verifies the list envelope and the filters (plan
// Section 8).
func TestAdminListOrders(t *testing.T) {
	svc := &fakeOrders{orders: []models.Order{sampleOrder()}, count: 7}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodGet,
		"/admin/v1/orders?limit=2&offset=4&customer_id=cus_1&region_id=reg_1&status=pending", "")

	require.Equal(t, http.StatusOK, rec.Code)

	body := decodeResponse(t, rec)
	assert.Equal(t, float64(7), body["count"], "count has to be the total of the FILTER")
	assert.Equal(t, float64(4), body["offset"])
	assert.Equal(t, float64(2), body["limit"])
	data, ok := body["data"].([]any)
	require.True(t, ok)
	assert.Len(t, data, 1)

	require.NotNil(t, svc.listInput.CustomerID)
	assert.Equal(t, "cus_1", *svc.listInput.CustomerID)
	require.NotNil(t, svc.listInput.RegionID)
	assert.Equal(t, "reg_1", *svc.listInput.RegionID)
	require.NotNil(t, svc.listInput.Status)
	assert.Equal(t, models.OrderPending, *svc.listInput.Status)
	assert.Equal(t, int64(2), svc.listInput.Page.Limit)
	assert.Equal(t, int64(4), svc.listInput.Page.Offset)
}

// TestAdminListOrdersDefaultLimit verifies that on a request without a limit the
// response shows the limit that is REALLY applied.
func TestAdminListOrdersDefaultLimit(t *testing.T) {
	svc := &fakeOrders{}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodGet, "/admin/v1/orders", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, float64(service.DefaultLimit), decodeResponse(t, rec)["limit"])
	assert.Equal(t, service.DefaultLimit, svc.listInput.Page.Limit)
}

// TestAdminListOrdersMalformedParameter rejects non-numeric paging.
func TestAdminListOrdersMalformedParameter(t *testing.T) {
	svc := &fakeOrders{}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodGet, "/admin/v1/orders?limit=many", "")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Empty(t, svc.calls, "on an invalid parameter the service must not be reached")
}

// TestAdminCancelWorksWithoutBody verifies that a cancel without a reason is
// accepted.
func TestAdminCancelWorksWithoutBody(t *testing.T) {
	svc := &fakeOrders{detail: sampleDetail()}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodPost, "/admin/v1/orders/order_1/cancel", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "", svc.gotReason)
	assert.Equal(t, []string{"CancelOrder", "GetOrder"}, svc.calls,
		"after the cancel the CURRENT state of the order has to be read")
}

// TestAdminCancelPassesReason verifies that the reason in the body reaches the
// service.
func TestAdminCancelPassesReason(t *testing.T) {
	svc := &fakeOrders{detail: sampleDetail()}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodPost, "/admin/v1/orders/order_1/cancel",
		`{"reason":"payment declined"}`)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "payment declined", svc.gotReason)
}

// TestAdminCancelRejectsUnknownField verifies that no field is silently
// swallowed.
func TestAdminCancelRejectsUnknownField(t *testing.T) {
	svc := &fakeOrders{detail: sampleDetail()}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodPost, "/admin/v1/orders/order_1/cancel",
		`{"resaon":"a typo"}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Empty(t, svc.calls)
}

// TestAdminCompleteAndArchive verifies that the transition endpoints reach the
// service.
func TestAdminCompleteAndArchive(t *testing.T) {
	cases := map[string]struct {
		path string
		call string
	}{
		"complete": {path: "/admin/v1/orders/order_1/complete", call: "CompleteOrder"},
		"archive":  {path: "/admin/v1/orders/order_1/archive", call: "ArchiveOrder"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			svc := &fakeOrders{detail: sampleDetail()}
			r := newRouter(svc)

			rec := doRequest(t, r, http.MethodPost, tc.path, "")

			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, []string{tc.call, "GetOrder"}, svc.calls)
		})
	}
}

// TestErrorKindsMapToStatusCodes verifies that the handler DOES NOT CHOOSE the
// status code, that the core/errors kind is mapped (plan Section 8).
func TestErrorKindsMapToStatusCodes(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
	}{
		"not found": {err: errors.NotFound("order_not_found", "missing"), status: http.StatusNotFound},
		"conflict":  {err: errors.Conflict("order_not_pending", "not allowed"), status: http.StatusConflict},
		"invalid":   {err: errors.Invalid("order_invalid_input", "bad input"), status: http.StatusUnprocessableEntity},
		"internal":  {err: errors.Internal("order_query_failed", "blew up"), status: http.StatusInternalServerError},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			svc := &fakeOrders{err: tc.err}
			r := newRouter(svc)

			rec := doRequest(t, r, http.MethodGet, "/admin/v1/orders/order_1", "")

			assert.Equal(t, tc.status, rec.Code)
			body := decodeResponse(t, rec)
			_, ok := body["error"].(map[string]any)
			assert.True(t, ok, "the error body has to come back in an error envelope")
		})
	}
}

// TestAdminCreateReturn verifies the body and the status code of the return
// endpoint.
func TestAdminCreateReturn(t *testing.T) {
	svc := &fakeOrders{ret: models.Return{
		ID: "ret_1", OrderID: "order_1", Status: models.ReturnRequested, RefundAmount: 3600,
	}}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodPost, "/admin/v1/orders/order_1/returns",
		`{"refund_amount":3600,"reason":"the size did not fit","note":"","metadata":{"channel":"support"}}`)

	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "order_1", svc.returnInput.OrderID)
	assert.Equal(t, int64(3600), svc.returnInput.RefundAmount)
	assert.Equal(t, "the size did not fit", svc.returnInput.Reason)
	assert.Equal(t, map[string]any{"channel": "support"}, svc.returnInput.Metadata)

	data, ok := decodeResponse(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "ret_1", data["id"])
	assert.Equal(t, "requested", data["status"])
}

// TestAdminListReturns verifies the list envelope.
func TestAdminListReturns(t *testing.T) {
	svc := &fakeOrders{
		returns: []models.Return{{ID: "ret_1", OrderID: "order_1", Status: models.ReturnRequested}},
		count:   1,
	}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodGet, "/admin/v1/orders/order_1/returns?limit=10", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, int64(10), svc.page.Limit)
	body := decodeResponse(t, rec)
	assert.Equal(t, float64(1), body["count"])
}

// TestAdminAfterSalesSingleRead verifies that the three single-read endpoints
// fetch the record by its id.
//
// The order id in the path does not verify the OWNER of the record; only the
// record's own id goes to the service (see the rationale of
// [api.Handler.adminGetReturn]).
func TestAdminAfterSalesSingleRead(t *testing.T) {
	cases := map[string]struct {
		path  string
		call  string
		id    string
		field string
		value any
	}{
		"return": {
			path: "/admin/v1/orders/order_1/returns/ret_1", call: "GetReturn",
			id: "ret_1", field: "status", value: "requested",
		},
		"exchange": {
			path: "/admin/v1/orders/order_1/exchanges/exch_1", call: "GetExchange",
			id: "exch_1", field: "difference_due", value: float64(-500),
		},
		"claim": {
			path: "/admin/v1/orders/order_1/claims/claim_1", call: "GetClaim",
			id: "claim_1", field: "type", value: "refund",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			svc := &fakeOrders{
				ret: models.Return{
					ID: "ret_1", OrderID: "order_1", Status: models.ReturnRequested,
				},
				exchange: models.Exchange{
					ID: "exch_1", OrderID: "order_1",
					Status: models.ExchangeRequested, DifferenceDue: -500,
				},
				claim: models.Claim{
					ID: "claim_1", OrderID: "order_1", Type: models.ClaimRefund,
					Status: models.ClaimRequested,
				},
			}
			r := newRouter(svc)

			rec := doRequest(t, r, http.MethodGet, tc.path, "")

			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, []string{tc.call}, svc.calls)
			assert.Equal(t, tc.id, svc.gotChildID)

			data, ok := decodeResponse(t, rec)["data"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, tc.id, data["id"])
			assert.Equal(t, tc.value, data[tc.field])
		})
	}
}

// TestAdminAfterSalesSingleReadNotFound verifies that a missing record returns
// 404.
func TestAdminAfterSalesSingleReadNotFound(t *testing.T) {
	svc := &fakeOrders{err: errors.NotFound("order_return_not_found", "return record not found")}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodGet, "/admin/v1/orders/order_1/returns/ret_MISSING", "")

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestAdminCreateExchange verifies that a NEGATIVE difference is carried on the
// exchange endpoint.
func TestAdminCreateExchange(t *testing.T) {
	svc := &fakeOrders{exchange: models.Exchange{
		ID: "exch_1", OrderID: "order_1", Status: models.ExchangeRequested, DifferenceDue: -500,
	}}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodPost, "/admin/v1/orders/order_1/exchanges",
		`{"difference_due":-500,"note":"with the cheaper model"}`)

	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, int64(-500), svc.exchangeInput.DifferenceDue)

	data, ok := decodeResponse(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(-500), data["difference_due"])
}

// TestAdminCreateClaimRequiresType verifies that the type field cannot be left
// empty.
//
// Picking a default type would mean deciding on the client's behalf how the
// request is to be met.
func TestAdminCreateClaimRequiresType(t *testing.T) {
	svc := &fakeOrders{}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodPost, "/admin/v1/orders/order_1/claims",
		`{"refund_amount":100}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Empty(t, svc.calls, "without a type the service must not be reached")
}

// TestAdminCreateClaim verifies that the claim endpoint carries its body.
func TestAdminCreateClaim(t *testing.T) {
	svc := &fakeOrders{claim: models.Claim{
		ID: "claim_1", OrderID: "order_1", Type: models.ClaimRefund,
		Status: models.ClaimRequested, RefundAmount: 1200,
	}}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodPost, "/admin/v1/orders/order_1/claims",
		`{"type":"refund","refund_amount":1200,"reason":"it arrived broken"}`)

	require.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, models.ClaimRefund, svc.claimInput.Type)
	assert.Equal(t, int64(1200), svc.claimInput.RefundAmount)

	data, ok := decodeResponse(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "refund", data["type"])
}

// readOnlyPrincipal returns an admin identity carrying only [api.ScopeRead].
func readOnlyPrincipal() corehttp.Principal {
	return corehttp.Principal{
		ID:     "user_readonly",
		Kind:   "user",
		Scopes: []string{api.ScopeRead},
	}
}

// TestReadOnlyPrincipalGets403OnWriteEndpoint verifies that the read scope does
// not suffice for writing.
//
// The endpoint here is an order CANCELLATION and it is irreversible: an order
// whose payment has been captured gets closed. Without scope enforcement,
// authentication alone would stand in for authorization and an identity granted
// for reporting could cancel the order.
func TestReadOnlyPrincipalGets403OnWriteEndpoint(t *testing.T) {
	svc := &fakeOrders{}
	r := newRouter(svc)

	rec := doRequestAs(t, r, http.MethodPost, "/admin/v1/orders/order_1/cancel", "", readOnlyPrincipal())

	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, svc.calls, "when the scope is insufficient the service must not be reached at all")

	errBody, ok := decodeResponse(t, rec)["error"].(map[string]any)
	require.True(t, ok, "an error envelope was expected: %s", rec.Body.String())
	assert.Equal(t, corehttp.CodeForbidden, errBody["code"])
}

// TestReadOnlyPrincipalPassesOnReadEndpoint verifies that the same identity
// passes on a read endpoint.
//
// This is the test that accompanies it as a pair: an endpoint returning 403
// could also have stemmed from the scope map being too narrow. That the same
// identity passes on the read shows that the refusal comes from the scope
// DISTINCTION.
func TestReadOnlyPrincipalPassesOnReadEndpoint(t *testing.T) {
	svc := &fakeOrders{
		orders: []models.Order{{ID: "order_1", Status: models.OrderPending}},
		count:  1,
	}
	r := newRouter(svc)

	rec := doRequestAs(t, r, http.MethodGet, "/admin/v1/orders", "", readOnlyPrincipal())

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"ListOrders"}, svc.calls)
}

// TestPrincipalWithoutScopesCannotOpenAdminEndpoint verifies that an admin user
// whose scopes were left EMPTY can reach no admin endpoint.
//
// The identity is valid — it can log in, it is known who it is — but it has no
// scope. Without this distinction, a user whose scope list was left empty would
// be believed to "reach nothing" while being able to read and close orders.
func TestPrincipalWithoutScopesCannotOpenAdminEndpoint(t *testing.T) {
	svc := &fakeOrders{}
	noScopes := corehttp.Principal{ID: "user_empty", Kind: "user", Scopes: []string{}}
	r := newRouter(svc)

	for _, tc := range []struct {
		name   string
		method string
		path   string
	}{
		{name: "read", method: http.MethodGet, path: "/admin/v1/orders"},
		{name: "write", method: http.MethodPost, path: "/admin/v1/orders/order_1/complete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRequestAs(t, r, tc.method, tc.path, "", noScopes)

			assert.Equal(t, http.StatusForbidden, rec.Code)
		})
	}
	assert.Empty(t, svc.calls, "for a principal without scopes the service must not be reached at all")
}

// TestRequestWithoutPrincipalGets401 verifies that a request with no identity at
// all gets 401, NOT 403.
//
// The distinction is meaningful for the client: 401 means "tell me who you are"
// (try again with an identity), 403 means "I know who you are but you have no
// scope" (there is no point in trying again).
func TestRequestWithoutPrincipalGets401(t *testing.T) {
	svc := &fakeOrders{}
	r := newRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/orders", strings.NewReader(""))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Empty(t, svc.calls)
}

// TestStorefrontEndpointRequiresNoScope verifies that the storefront endpoint
// works with an identity that carries no scope.
//
// The identity of /store/v1 is the publishable key and that key by definition
// CARRIES NO scope. Closing the storefront endpoint too while adding the admin
// scopes would have brought the whole storefront down.
func TestStorefrontEndpointRequiresNoScope(t *testing.T) {
	svc := &fakeOrders{detail: models.OrderDetail{Order: models.Order{ID: "order_1"}}}
	storefront := corehttp.Principal{ID: "pk_1", Kind: "api_key", Scopes: []string{}}
	r := newRouter(svc)

	rec := doRequestAs(t, r, http.MethodGet, "/store/v1/orders/order_1", "", storefront)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"GetOrder"}, svc.calls)
}

// TestTheOrdersLivePaymentIsReadable is the read the "order_payment" link was
// declared for.
//
// Two godocs named that link before it existed — the payment module's query
// provider ("an order listing sees the order's payment status through this
// provider and the order_payment link") and the order module's package doc.
// Nothing declared it, nothing wrote it, and nothing read it. This is the read.
func TestTheOrdersLivePaymentIsReadable(t *testing.T) {
	svc := &fakeOrders{
		paymentBound: true,
		payment: service.OrderPayment{
			CollectionID:     "pcol_1",
			Status:           "captured",
			Amount:           6100,
			AuthorizedAmount: 0,
			CapturedAmount:   6100,
			RefundedAmount:   0,
			CurrencyCode:     "TRY",
		},
	}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodGet, "/admin/v1/orders/order_1/payment", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	payload := decodeResponse(t, rec)
	data, ok := payload["data"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.Equal(t, "pcol_1", data["payment_collection_id"])
	assert.Equal(t, "captured", data["status"])
	assert.InDelta(t, 6100, data["captured_amount"], 0.0)
	assert.Equal(t, "TRY", data["currency_code"])
}

// TestAnOrderWithNoPaymentIsNotTheSameAsNoOrder keeps the two apart.
//
// An order can genuinely have no collection: the saga binds it AFTER the order
// is written, so a checkout that died in between leaves one. A client that
// could not tell that from "there is no such order" would treat a half-finished
// checkout as a missing record.
func TestAnOrderWithNoPaymentIsNotTheSameAsNoOrder(t *testing.T) {
	svc := &fakeOrders{paymentBound: false}
	r := newRouter(svc)

	rec := doRequest(t, r, http.MethodGet, "/admin/v1/orders/order_1/payment", "")

	require.Equal(t, http.StatusNotFound, rec.Code)
	payload := decodeResponse(t, rec)
	failure, ok := payload["error"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.Equal(t, "order_payment_unbound", failure["code"],
		"the code has to say WHICH thing is missing")
}

// fakeReceiving is the return flow's stand-in.
type fakeReceiving struct {
	lines    int
	units    int64
	warnings []string
	err      error

	gotReturnID   string
	gotLocationID string
	calls         int

	refunded       int64
	recorded       bool
	refundWarnings []string
	refundErr      error
	refundCalls    int
	gotAmount      int64
	gotReason      string
	settleCalls    int
	gotClaimID     string
}

// SettleClaim records the call and returns the scripted outcome.
func (f *fakeReceiving) SettleClaim(
	_ context.Context, claimID string, amount int64, reason string,
) (refunded int64, summaryRecorded bool, warnings []string, err error) {
	f.settleCalls++
	f.gotClaimID, f.gotAmount, f.gotReason = claimID, amount, reason
	if f.refundErr != nil {
		return 0, false, nil, f.refundErr
	}

	return f.refunded, f.recorded, f.refundWarnings, nil
}

// RefundReturn records the call and returns the scripted outcome.
func (f *fakeReceiving) RefundReturn(
	_ context.Context, returnID string, amount int64, reason string,
) (refunded int64, summaryRecorded bool, warnings []string, err error) {
	f.refundCalls++
	f.gotReturnID, f.gotAmount, f.gotReason = returnID, amount, reason
	if f.refundErr != nil {
		return 0, false, nil, f.refundErr
	}

	return f.refunded, f.recorded, f.refundWarnings, nil
}

// ReceiveReturn records the call and returns the scripted outcome.
func (f *fakeReceiving) ReceiveReturn(
	_ context.Context, returnID, locationID string,
) (restockedLines int, restockedUnits int64, warnings []string, err error) {
	f.calls++
	f.gotReturnID, f.gotLocationID = returnID, locationID
	if f.err != nil {
		return 0, 0, nil, f.err
	}

	return f.lines, f.units, f.warnings, nil
}

// TestReceivingAReturnGoesTHROUGHTheFlow is why the endpoint is not bound to
// the service method.
//
// Receiving has two halves: the record says the goods arrived, the stock goes
// back. The second reaches the inventory module, which this one does not know,
// so an endpoint on the service would stamp the first and silently skip the
// second.
func TestReceivingAReturnGoesTHROUGHTheFlow(t *testing.T) {
	flow := &fakeReceiving{lines: 2, units: 3}
	r := newRouterWithFlow(&fakeOrders{}, flow)

	rec := doRequest(t, r, http.MethodPost,
		"/admin/v1/orders/order_1/returns/ret_1/receive", `{"location_id":"sloc_main"}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 1, flow.calls)
	assert.Equal(t, "ret_1", flow.gotReturnID)
	assert.Equal(t, "sloc_main", flow.gotLocationID)

	payload := decodeResponse(t, rec)
	data, ok := payload["data"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.InDelta(t, 2, data["restocked_lines"], 0.0)
	assert.InDelta(t, 3, data["restocked_units"], 0.0)
}

// TestAReceiptWithWarningsIsStillASuccess is not a contradiction.
//
// The goods arrived and the record says so; something about the stock needs a
// human. Refusing the receipt would deny a physical fact and leave the operator
// with no record to work from.
func TestAReceiptWithWarningsIsStillASuccess(t *testing.T) {
	flow := &fakeReceiving{
		lines:    1,
		units:    1,
		warnings: []string{"variant var_a has no inventory item; its stock was not put back"},
	}
	r := newRouterWithFlow(&fakeOrders{}, flow)

	rec := doRequest(t, r, http.MethodPost,
		"/admin/v1/orders/order_1/returns/ret_1/receive", `{"location_id":"sloc_main"}`)

	require.Equal(t, http.StatusOK, rec.Code)
	payload := decodeResponse(t, rec)
	data, ok := payload["data"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	warnings, ok := data["warnings"].([]any)
	require.True(t, ok, "a warning must reach the operator, not only the log")
	assert.Len(t, warnings, 1)
}

// TestAReturnIsNotReceivedWithoutTheFlow pins the fail-closed branch.
//
// Recording the receipt and skipping the stock would put the goods in the
// warehouse while the count says they are not there — with a record claiming
// the receipt succeeded.
func TestAReturnIsNotReceivedWithoutTheFlow(t *testing.T) {
	svc := &fakeOrders{}
	r := newRouterWithFlow(svc, nil)

	rec := doRequest(t, r, http.MethodPost,
		"/admin/v1/orders/order_1/returns/ret_1/receive", `{"location_id":"sloc_main"}`)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Empty(t, svc.calls, "nothing may be written when the stock cannot follow")
}

// TestRefundingAReturnGoesTHROUGHTheFlow keeps the money on the same path as
// the stock.
//
// The refund reaches the payment module and the recording reaches this one; an
// endpoint bound to either alone would do half of it.
func TestRefundingAReturnGoesTHROUGHTheFlow(t *testing.T) {
	flow := &fakeReceiving{refunded: 1200, recorded: true}
	r := newRouterWithFlow(&fakeOrders{}, flow)

	rec := doRequest(t, r, http.MethodPost,
		"/admin/v1/orders/order_1/returns/ret_1/refund", `{"amount":1200,"reason":"damaged"}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 1, flow.refundCalls)
	assert.Equal(t, "ret_1", flow.gotReturnID)
	assert.Equal(t, int64(1200), flow.gotAmount)
	assert.Equal(t, "damaged", flow.gotReason)

	payload := decodeResponse(t, rec)
	data, ok := payload["data"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.InDelta(t, 1200, data["refunded_amount"], 0.0)
	assert.Equal(t, true, data["summary_recorded"])
}

// TestMoneyThatLeftWithoutBeingRecordedReachesTheOperator keeps the risk the
// ordering accepts from being invisible.
//
// summary_recorded false does not mean the money stayed — it means the ORDER
// does not say it left, and an operator has to see that in the response rather
// than only in a log.
func TestMoneyThatLeftWithoutBeingRecordedReachesTheOperator(t *testing.T) {
	flow := &fakeReceiving{
		refunded:       1200,
		recorded:       false,
		refundWarnings: []string{"the order was not told about the refund"},
	}
	r := newRouterWithFlow(&fakeOrders{}, flow)

	rec := doRequest(t, r, http.MethodPost,
		"/admin/v1/orders/order_1/returns/ret_1/refund", `{"amount":1200}`)

	require.Equal(t, http.StatusOK, rec.Code)
	payload := decodeResponse(t, rec)
	data, ok := payload["data"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.Equal(t, false, data["summary_recorded"])
	assert.InDelta(t, 1200, data["refunded_amount"], 0.0)
	assert.NotEmpty(t, data["warnings"])
}

// TestACustomerCanAskForAReturn is the surface the storefront did not have.
//
// The record could only be opened from the admin side, so a shop had no way to
// let a customer start one at all.
func TestACustomerCanAskForAReturn(t *testing.T) {
	svc := &fakeOrders{ret: models.Return{ID: "ret_1", OrderID: "order_1"}}
	r := newRouter(svc)

	rec := doRequestAs(t, r, http.MethodPost, "/store/v1/orders/order_1/returns",
		`{"lines":[{"order_line_item_id":"oli_1","quantity":2}],"reason":"too small"}`,
		corehttp.Principal{})

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Len(t, svc.returnInput.Lines, 1)
	assert.Equal(t, "oli_1", svc.returnInput.Lines[0].OrderLineItemID)
	assert.Equal(t, int64(2), svc.returnInput.Lines[0].Quantity)
	assert.Equal(t, "too small", svc.returnInput.Reason)
}

// TestACustomerCannotNameTheirOwnRefund is the shipping-price defect in another
// place, refused before it exists.
//
// What a return is worth is the shop's to decide after seeing what comes back.
// A body that could carry an amount would let a customer decide their own
// refund; the field is not in the request at all, and this API rejects fields
// it does not recognize.
func TestACustomerCannotNameTheirOwnRefund(t *testing.T) {
	svc := &fakeOrders{}
	r := newRouter(svc)

	rec := doRequestAs(t, r, http.MethodPost, "/store/v1/orders/order_1/returns",
		`{"lines":[{"order_line_item_id":"oli_1","quantity":1}],"refund_amount":9999}`,
		corehttp.Principal{})

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Zero(t, svc.returnInput.RefundAmount)
}

// TestAReturnRequestWithNoLinesIsRefused keeps an empty record out.
//
// A return that names nothing cannot be restocked and cannot be judged; it
// would sit in the operator's list saying only that somebody clicked.
func TestAReturnRequestWithNoLinesIsRefused(t *testing.T) {
	svc := &fakeOrders{}
	r := newRouter(svc)

	rec := doRequestAs(t, r, http.MethodPost, "/store/v1/orders/order_1/returns",
		`{"lines":[]}`, corehttp.Principal{})

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Empty(t, svc.calls)
}
