package api_test

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/order/api"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// provenAs is a customer identity that proves one customer, or fails.
type provenAs struct {
	customerID string
	err        error
}

func (p provenAs) CustomerID(*http.Request) (string, error) { return p.customerID, p.err }

// ownOrdersRouter is a router whose storefront own-orders route asks the
// given identity; nil binds none.
func ownOrdersRouter(svc api.Orders, identity corehttp.Identity) chi.Router {
	r := chi.NewRouter()
	handler := api.New(svc, nil, nil, nil)
	if identity != nil {
		handler = handler.WithIdentity(identity)
	}
	handler.Routes(r)
	return r
}

// TestAShopperListsTheirOwnOrders is ADR 0367: the customer the request
// proves lists their orders, the page they asked for, and nobody else's.
func TestAShopperListsTheirOwnOrders(t *testing.T) {
	svc := &fakeOrders{orders: []models.Order{sampleOrder()}, count: 3}
	r := ownOrdersRouter(svc, provenAs{customerID: "cus_1"})

	rec := doRequest(t, r, http.MethodGet, "/store/v1/customers/cus_1/orders?limit=2&offset=1", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	body := decodeResponse(t, rec)
	assert.Equal(t, float64(3), body["count"])
	data, ok := body["data"].([]any)
	require.True(t, ok)
	assert.Len(t, data, 1)
	require.NotNil(t, svc.listInput.CustomerID)
	assert.Equal(t, "cus_1", *svc.listInput.CustomerID, "the proven customer's orders, and only theirs")
	assert.Nil(t, svc.listInput.Status, "every status is listed")
	assert.Equal(t, int64(2), svc.listInput.Page.Limit)
	assert.Equal(t, int64(1), svc.listInput.Page.Offset)
}

// TestAShopperCannotListAnotherCustomersOrders is ADR 0367's refusal: another
// customer in the path, no identity bound, or an identity that proves nobody,
// and the orders are never read.
func TestAShopperCannotListAnotherCustomersOrders(t *testing.T) {
	for name, c := range map[string]struct {
		identity corehttp.Identity
		status   int
		code     string
	}{
		"another customer":  {provenAs{customerID: "cus_1"}, http.StatusForbidden, corehttp.CodeIdentityMismatch},
		"no identity bound": {nil, http.StatusUnauthorized, corehttp.CodeIdentityNotBound},
		"nobody proven": {
			provenAs{err: coreerrors.Unauthorized("identity_session_none", "the request carries no valid session")},
			http.StatusUnauthorized, "identity_session_none",
		},
	} {
		svc := &fakeOrders{orders: []models.Order{sampleOrder()}, count: 1}
		r := ownOrdersRouter(svc, c.identity)

		rec := doRequest(t, r, http.MethodGet, "/store/v1/customers/cus_2/orders", "")
		assert.Equal(t, c.status, rec.Code, "%s: %s", name, rec.Body.String())
		assert.Contains(t, rec.Body.String(), c.code, name)
		assert.NotContains(t, svc.calls, "ListOrders", "%s: the orders were read", name)
	}
}
