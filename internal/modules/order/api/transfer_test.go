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

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/order/api"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// The transfer routes (ADR 0450): what each hands the service and what it
// answers. The rules are the service's and its tests hold them.

// fakeTransfers records the call and answers the scripted transfer.
type fakeTransfers struct {
	transfer models.OrderTransfer
	err      error
	now      time.Time

	calls                     []string
	gotOrderID, gotTransferID string
	gotCustomerID, gotToken   string
	gotExpectedCustomerID     string
}

var _ api.Transfers = (*fakeTransfers)(nil)

func (f *fakeTransfers) answer(call string) (models.OrderTransfer, error) {
	f.calls = append(f.calls, call)
	return f.transfer, f.err
}

func (f *fakeTransfers) RequestTransfer(_ context.Context, orderID, receiverID string) (models.OrderTransfer, error) {
	f.gotOrderID, f.gotCustomerID = orderID, receiverID
	return f.answer("request")
}

func (f *fakeTransfers) AcceptTransfer(_ context.Context, transferID, token string) (models.OrderTransfer, error) {
	f.gotTransferID, f.gotToken = transferID, token
	return f.answer("accept")
}

func (f *fakeTransfers) DeclineTransfer(_ context.Context, transferID, token string) (models.OrderTransfer, error) {
	f.gotTransferID, f.gotToken = transferID, token
	return f.answer("decline")
}

func (f *fakeTransfers) CancelTransfer(_ context.Context, transferID, customerID string) (models.OrderTransfer, error) {
	f.gotTransferID, f.gotCustomerID = transferID, customerID
	return f.answer("cancel")
}

func (f *fakeTransfers) MoveOrder(
	_ context.Context, orderID, customerID, expectedCustomerID string,
) (models.OrderTransfer, error) {
	f.gotOrderID, f.gotCustomerID, f.gotExpectedCustomerID = orderID, customerID, expectedCustomerID
	return f.answer("move")
}

func (f *fakeTransfers) ListTransfers(
	_ context.Context, orderID string, _ service.Page,
) ([]models.OrderTransfer, int64, error) {
	f.gotOrderID = orderID
	transfer, err := f.answer("list")
	return []models.OrderTransfer{transfer}, 1, err
}

func (f *fakeTransfers) Now() time.Time { return f.now }

// waitingTransfer is a request written an hour before the fake's clock.
func waitingTransfer(now time.Time) models.OrderTransfer {
	expires := now.Add(time.Hour)
	return models.OrderTransfer{
		ID: "otr_1", OrderID: "order_1", Status: models.TransferRequested,
		RequestedBy: models.TransferByCustomer, FromEmail: "guest@example.com",
		ToCustomerID: "cus_RECEIVER", TokenHash: []byte("digest"),
		ExpiresAt: &expires, CreatedAt: now.Add(-time.Hour),
	}
}

// transferRouter is a router whose transfer routes answer from transfers,
// whose order the token opens and whose identity proves identity.
func transferRouter(transfers api.Transfers, identity corehttp.Identity) chi.Router {
	r := chi.NewRouter()
	handler := api.New(gatedOrders("", true), nil, nil, nil).WithIdentity(identity)
	if transfers != nil {
		handler = handler.WithTransfers(transfers)
	}
	handler.Routes(r)

	return r
}

// adminTransferRequest calls an admin route with the given scopes.
func adminTransferRequest(r chi.Router, method, path, body string, scopes ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(corehttp.WithPrincipal(req.Context(),
		corehttp.Principal{ID: "user_1", Kind: "user", Scopes: scopes}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// transferData decodes the record in a single envelope.
func transferData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var body struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())

	return body.Data
}

// TestARequestIsTheProvenShoppersBehindTheOrdersAccess proves the receiver is
// the proven customer and the record names no customer and no address.
func TestARequestIsTheProvenShoppersBehindTheOrdersAccess(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	transfers := &fakeTransfers{transfer: waitingTransfer(now), now: now}
	r := transferRouter(transfers, provenAs{customerID: "cus_RECEIVER"})

	rec := storeRequest(t, r, http.MethodPost, "/store/v1/orders/order_1/transfer-requests", "", "ordtok_1")

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "order_1", transfers.gotOrderID)
	assert.Equal(t, "cus_RECEIVER", transfers.gotCustomerID, "the receiver is the PROVEN customer")
	data := transferData(t, rec)
	assert.Equal(t, "requested", data["status"])
	for _, withheld := range []string{"to_customer_id", "from_customer_id", "from_email", "token_hash"} {
		assert.NotContains(t, data, withheld, "the storefront record carries no %s", withheld)
	}
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
}

// TestARequestAsksTheOrdersAccessAndAProof proves the two refusals before
// the service: the order's 404, and the unproven shopper's 401.
func TestARequestAsksTheOrdersAccessAndAProof(t *testing.T) {
	transfers := &fakeTransfers{}

	var r chi.Router = chi.NewRouter()
	api.New(gatedOrders("", false), nil, nil, nil).
		WithIdentity(provenAs{customerID: "cus_RECEIVER"}).WithTransfers(transfers).Routes(r)
	rec := storeRequest(t, r, http.MethodPost, "/store/v1/orders/order_1/transfer-requests", "", "ordtok_wrong")
	assert.Equal(t, http.StatusNotFound, rec.Code, "an id and a wrong token open nothing")

	r = transferRouter(transfers, provenAs{err: coreerrors.Unauthorized("no_session", "signed out")})
	rec = storeRequest(t, r, http.MethodPost, "/store/v1/orders/order_1/transfer-requests", "", "ordtok_1")
	assert.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "order_transfer_unproven")

	assert.Empty(t, transfers.calls, "a refused request reaches no transfer")
}

// TestAnAnswerCarriesTheMailedTokenInItsBody proves the token travels in the
// body and that the answers need no sign-in.
func TestAnAnswerCarriesTheMailedTokenInItsBody(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	for _, route := range []struct{ path, call string }{
		{"/store/v1/order-transfers/otr_1/accept", "accept"},
		{"/store/v1/order-transfers/otr_1/decline", "decline"},
	} {
		transfers := &fakeTransfers{transfer: waitingTransfer(now), now: now}
		r := transferRouter(transfers, provenAs{err: coreerrors.Unauthorized("no_session", "signed out")})

		rec := storeRequest(t, r, http.MethodPost, route.path, `{"token":"ordxfr_1"}`, "")

		require.Equal(t, http.StatusOK, rec.Code, "%s: %s", route.path, rec.Body.String())
		assert.Equal(t, []string{route.call}, transfers.calls)
		assert.Equal(t, "otr_1", transfers.gotTransferID)
		assert.Equal(t, "ordxfr_1", transfers.gotToken)
		assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	}
}

// TestAnExpiredRequestReadsExpired proves the status is read with the
// service's clock.
func TestAnExpiredRequestReadsExpired(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	transfers := &fakeTransfers{transfer: waitingTransfer(now), now: now.Add(2 * time.Hour)}
	r := transferRouter(transfers, provenAs{customerID: "cus_RECEIVER"})

	rec := storeRequest(t, r, http.MethodPost, "/store/v1/order-transfers/otr_1/cancel", "", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "expired", transferData(t, rec)["status"])
	assert.Equal(t, "cus_RECEIVER", transfers.gotCustomerID, "the withdrawal is the proven customer's")
}

// TestTheTransferRoutesFailClosedWithoutTheSurface proves an unbound surface
// is an error rather than a silent pass.
func TestTheTransferRoutesFailClosedWithoutTheSurface(t *testing.T) {
	r := transferRouter(nil, provenAs{customerID: "cus_RECEIVER"})

	rec := storeRequest(t, r, http.MethodPost, "/store/v1/order-transfers/otr_1/accept", `{"token":"x"}`, "")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestAnOperatorMovesWithTheCustomerTheyRead proves the move needs the read
// customer, an empty string for a guest's, and the write scope.
func TestAnOperatorMovesWithTheCustomerTheyRead(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	moved := waitingTransfer(now)
	moved.Status, moved.RequestedBy, moved.TokenHash, moved.ExpiresAt = models.TransferAccepted, models.TransferByOperator, nil, nil
	moved.ClosedAt = &now
	transfers := &fakeTransfers{transfer: moved, now: now}
	r := transferRouter(transfers, nil)

	rec := adminTransferRequest(r, http.MethodPost, "/admin/v1/orders/order_1/transfers",
		`{"customer_id":"cus_2"}`, api.ScopeWrite)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "a move must say what it was read with")
	assert.Empty(t, transfers.calls)

	rec = adminTransferRequest(r, http.MethodPost, "/admin/v1/orders/order_1/transfers",
		`{"customer_id":"cus_2","expected_customer_id":""}`, api.ScopeRead)
	assert.Equal(t, http.StatusForbidden, rec.Code, "the read scope moves nothing")

	rec = adminTransferRequest(r, http.MethodPost, "/admin/v1/orders/order_1/transfers",
		`{"customer_id":"cus_2","expected_customer_id":""}`, api.ScopeWrite)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "cus_2", transfers.gotCustomerID)
	assert.Empty(t, transfers.gotExpectedCustomerID)
	data := transferData(t, rec)
	assert.Equal(t, "operator", data["requested_by"])
	assert.Equal(t, "accepted", data["status"])

	rec = adminTransferRequest(r, http.MethodGet, "/admin/v1/orders/order_1/transfers", "", api.ScopeRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"to_customer_id":"cus_RECEIVER"`)
}
