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
	"github.com/bdrtr/gobit/internal/modules/notification/api"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// fakeDeliveries is the scriptable counterpart of api.Deliveries.
//
// It exists so that the HTTP behavior can be tested without a real database:
// the handler's job is not to CHOOSE the status code but to give the service's
// typed error to corehttp.WriteError, and that can only be verified by putting
// a fake in place of the service.
type fakeDeliveries struct {
	records []models.Delivery
	total   int64
	err     error

	// lastInput is the input of the last call; it is what proves that the query
	// parameters reach the service UNCORRUPTED.
	lastInput service.ListDeliveriesInput
}

func (f *fakeDeliveries) ListDeliveries(
	_ context.Context,
	in service.ListDeliveriesInput,
) ([]models.Delivery, int64, error) {
	f.lastInput = in
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.records, f.total, nil
}

// sampleDelivery is a typical delivery log record.
func sampleDelivery() models.Delivery {
	moment := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)

	return models.Delivery{
		ID:         "notif_01H",
		Template:   "order.placed",
		Channel:    "email",
		Reference:  "order_01H",
		ProviderID: "log",
		Status:     models.DeliverySent,
		CreatedAt:  moment,
		UpdatedAt:  moment,
	}
}

// newRouter sets up a router that works over the fake service.
func newRouter(svc *fakeDeliveries) chi.Router {
	r := chi.NewRouter()
	api.New(svc).Routes(r)

	return r
}

// adminPrincipal is the tests' default identity: a fully scoped administration
// user.
//
// The router is built DIRECTLY here, that is, corehttp.RequireAdmin is not in
// the chain and there is nobody to put the identity into the context; because
// the endpoint is protected with corehttp.RequireScope, a request without an
// identity returns 401 and the behavior the test really verifies would never
// get its turn.
func adminPrincipal() corehttp.Principal {
	return corehttp.Principal{ID: "user_test", Kind: "user", Scopes: []string{corehttp.ScopeAdmin}}
}

// doRequest applies the given request to the router with a fully scoped
// identity.
func doRequest(t *testing.T, r chi.Router, path string) *httptest.ResponseRecorder {
	t.Helper()

	return doRequestAs(t, r, path, adminPrincipal())
}

// doRequestAs applies the given request with the stated identity.
func doRequestAs(t *testing.T, r chi.Router, path string, principal corehttp.Principal) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, strings.NewReader(""))
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), principal))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// TestListReturnsRecordsInsideTheEnvelope verifies the response envelope and
// the body fields.
//
// That the recipient address IS NOT in the body is tested separately: it is not
// in the record either and the only source to invent it from would be the order
// itself — this endpoint must not turn into a door that serves personal data
// out of a second place.
func TestListReturnsRecordsInsideTheEnvelope(t *testing.T) {
	svc := &fakeDeliveries{records: []models.Delivery{sampleDelivery()}, total: 1}

	rec := doRequest(t, newRouter(svc), "/admin/v1/notifications")

	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Data   []map[string]any `json:"data"`
		Count  int64            `json:"count"`
		Offset int64            `json:"offset"`
		Limit  int64            `json:"limit"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	assert.Equal(t, int64(1), body.Count)
	assert.Equal(t, service.DefaultLimit, body.Limit, "the envelope must report the APPLIED limit")
	require.Len(t, body.Data, 1)

	record := body.Data[0]
	assert.Equal(t, "notif_01H", record["id"])
	assert.Equal(t, "order.placed", record["template"])
	assert.Equal(t, "order_01H", record["reference"])
	assert.Equal(t, "sent", record["status"])
	assert.Equal(t, "log", record["provider_id"])
	assert.NotContains(t, record, "to", "the body MUST NOT carry the recipient address")
	assert.NotContains(t, record, "email")
	assert.NotContains(t, record, "error", "an empty error field must not be written into the body")
}

// TestListFiltersReachTheService verifies that the query parameters reach the
// service uncorrupted.
func TestListFiltersReachTheService(t *testing.T) {
	svc := &fakeDeliveries{}

	rec := doRequest(t, newRouter(svc),
		"/admin/v1/notifications?reference=order_01H&status=failed&limit=10&offset=20")

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, svc.lastInput.Reference)
	assert.Equal(t, "order_01H", *svc.lastInput.Reference)
	require.NotNil(t, svc.lastInput.Status)
	assert.Equal(t, "failed", *svc.lastInput.Status)
	assert.Equal(t, int64(10), svc.lastInput.Page.Limit)
	assert.Equal(t, int64(20), svc.lastInput.Page.Offset)
}

// TestFilterThatIsNotGivenPassesAsNIL verifies that the distinction between
// "was not given" and "was given empty" is preserved.
//
// Had the distinction been lost, a client that wrote "?reference=" would
// silently have got the WHOLE log.
func TestFilterThatIsNotGivenPassesAsNIL(t *testing.T) {
	svc := &fakeDeliveries{}

	require.Equal(t, http.StatusOK, doRequest(t, newRouter(svc), "/admin/v1/notifications").Code)
	assert.Nil(t, svc.lastInput.Reference)
	assert.Nil(t, svc.lastInput.Status)

	require.Equal(t, http.StatusOK, doRequest(t, newRouter(svc), "/admin/v1/notifications?reference=").Code)
	require.NotNil(t, svc.lastInput.Reference)
	assert.Empty(t, *svc.lastInput.Reference)
}

// TestNonNumericPagingParameterIsRejected verifies that there is no silent fall
// back to the first page.
func TestNonNumericPagingParameterIsRejected(t *testing.T) {
	rec := doRequest(t, newRouter(&fakeDeliveries{}), "/admin/v1/notifications?limit=abc")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestServiceErrorDoesNotChooseTheStatusCode verifies that the handler DOES NOT
// CHOOSE the status code, the error kind translates it (plan Section 2.7).
func TestServiceErrorDoesNotChooseTheStatusCode(t *testing.T) {
	tests := map[string]struct {
		err    error
		status int
		code   string
	}{
		"invalid filter": {
			err:    errors.Invalid("notification_invalid_input", "unrecognized status"),
			status: http.StatusUnprocessableEntity,
		},
		"no database": {
			err:    errors.Unavailable("notification_query_failed", "no pool"),
			status: http.StatusServiceUnavailable,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rec := doRequest(t, newRouter(&fakeDeliveries{err: tt.err}), "/admin/v1/notifications")

			assert.Equal(t, tt.status, rec.Code)
		})
	}
}

// TestPrincipalWithoutTheScopeIsRejected verifies that an administration
// identity without the read scope cannot see the log.
//
// Had authentication stood in for authorization, a user whose scopes had been
// emptied could have read the timeline of the order flow too.
func TestPrincipalWithoutTheScopeIsRejected(t *testing.T) {
	narrow := corehttp.Principal{ID: "user_narrow", Kind: "user", Scopes: []string{"product:read"}}

	rec := doRequestAs(t, newRouter(&fakeDeliveries{}), "/admin/v1/notifications", narrow)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// TestReadScopeIsEnough verifies that the module can be reached with its own
// scope; the admin superior scope is NOT REQUIRED.
func TestReadScopeIsEnough(t *testing.T) {
	narrow := corehttp.Principal{ID: "user_reader", Kind: "user", Scopes: []string{api.ScopeRead}}

	rec := doRequestAs(t, newRouter(&fakeDeliveries{}), "/admin/v1/notifications", narrow)

	assert.Equal(t, http.StatusOK, rec.Code)
}
