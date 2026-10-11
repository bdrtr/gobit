package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/order/api"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// The storefront order routes' access (ADR 0439): one of the order's tokens,
// or the bound identity proving the order's customer, and nothing else. The
// token comparison is the order's own (models.AccessTokenMatches); what is
// held here is the decision the routes make on the service's two facts, and
// that every refusal is the unknown id's 404.

// storeOrderRoutes are the three routes the gate covers, each with a body it
// accepts once the gate opens.
var storeOrderRoutes = []struct{ method, path, body string }{
	{http.MethodGet, "/store/v1/orders/order_1", ""},
	{http.MethodGet, "/store/v1/orders/order_1/timeline", ""},
	{http.MethodPost, "/store/v1/orders/order_1/returns",
		`{"lines":[{"order_line_item_id":"oli_1","quantity":1}],"reason":"too small"}`},
}

// storeAccessRouter is a router whose storefront order routes ask the given
// identity; nil binds none.
func storeAccessRouter(svc api.Orders, identity corehttp.Identity) chi.Router {
	r := chi.NewRouter()
	handler := api.New(svc, &fakeReceiving{}, nil, nil)
	if identity != nil {
		handler = handler.WithIdentity(identity)
	}
	handler.Routes(r)

	return r
}

// storeRequest calls a storefront route with the publishable key's principal
// and, when token is not empty, the order token header.
func storeRequest(t *testing.T, r chi.Router, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(corehttp.WithPrincipal(req.Context(),
		corehttp.Principal{ID: "pk_1", Kind: "api_key", Scopes: []string{}}))
	if token != "" {
		req.Header.Set(corehttp.OrderTokenHeader, token)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// gatedOrders is a service whose order has the given customer and answers the
// given token match.
func gatedOrders(customerID string, tokenMatches bool) *fakeOrders {
	return &fakeOrders{
		detail:   sampleDetail(),
		timeline: []service.TimelineEntry{{Kind: "order.placed", RefID: "order_1"}},
		access:   &service.StorefrontAccess{CustomerID: customerID, TokenMatches: tokenMatches},
	}
}

// TestAStoreOrderRouteOpensToTheOrdersToken is the guest's way in: the token
// in the header, handed to the service as sent, and no identity asked.
func TestAStoreOrderRouteOpensToTheOrdersToken(t *testing.T) {
	for _, route := range storeOrderRoutes {
		svc := gatedOrders("", true)
		r := storeAccessRouter(svc, provenAs{err: coreerrors.Unavailable("session_store_down", "down")})

		rec := storeRequest(t, r, route.method, route.path, route.body, "ordtok_presented")

		assert.Less(t, rec.Code, 300, "%s %s: %s", route.method, route.path, rec.Body.String())
		assert.Equal(t, "ordtok_presented", svc.gotToken, "%s %s", route.method, route.path)
		assert.Equal(t, "order_1", svc.gotOrderID)
	}
}

// TestAStoreOrderRouteOpensToTheOrdersCustomer is the signed-in shopper's
// way in: no token, and the identity proves the order's customer.
func TestAStoreOrderRouteOpensToTheOrdersCustomer(t *testing.T) {
	for _, route := range storeOrderRoutes {
		svc := gatedOrders("cus_1", false)
		r := storeAccessRouter(svc, provenAs{customerID: "cus_1"})

		rec := storeRequest(t, r, route.method, route.path, route.body, "")

		assert.Less(t, rec.Code, 300, "%s %s: %s", route.method, route.path, rec.Body.String())
	}
}

// TestAStoreOrderRouteRefusesWithTheUnknownIDsAnswer is every refusal: no
// token and no identity, a token that does not match, an identity proving
// another customer, an identity proving nobody, and a guest's order that no
// identity can prove — each answered word for word as an unknown id, and none
// reaching the order or writing a return.
func TestAStoreOrderRouteRefusesWithTheUnknownIDsAnswer(t *testing.T) {
	unknown := storeRequest(t, storeAccessRouter(&fakeOrders{
		err: coreerrors.NotFound(service.CodeOrderNotFound, "order not found: %s", "order_1"),
	}, nil), http.MethodGet, "/store/v1/orders/order_1", "", "")
	require.Equal(t, http.StatusNotFound, unknown.Code, unknown.Body.String())
	unknownError := decodeResponse(t, unknown)["error"]

	nobody := provenAs{err: coreerrors.Unauthorized("identity_session_none", "no session")}
	for name, c := range map[string]struct {
		customerID string
		identity   corehttp.Identity
		token      string
	}{
		"no token, no identity bound":        {"cus_1", nil, ""},
		"a token the order did not issue":    {"cus_1", nil, "ordtok_wrong"},
		"another customer proven":            {"cus_1", provenAs{customerID: "cus_2"}, ""},
		"nobody proven":                      {"cus_1", nobody, ""},
		"a guest's order, no identity bound": {"", nil, ""},
		"a guest's order, nobody proven":     {"", nobody, ""},
		"a guest's order, a customer proven": {"", provenAs{customerID: "cus_1"}, ""},
	} {
		for _, route := range storeOrderRoutes {
			svc := gatedOrders(c.customerID, false)
			r := storeAccessRouter(svc, c.identity)

			rec := storeRequest(t, r, route.method, route.path, route.body, c.token)

			require.Equal(t, http.StatusNotFound, rec.Code, "%s, %s %s: %s",
				name, route.method, route.path, rec.Body.String())
			assert.Equal(t, unknownError, decodeResponse(t, rec)["error"],
				"%s, %s %s: the refusal says something an unknown id's answer does not", name, route.method, route.path)
			assert.Equal(t, []string{"StorefrontAccess"}, svc.calls,
				"%s, %s %s: the refused request reached the order", name, route.method, route.path)
			assert.Empty(t, svc.returnInput.OrderID, "%s: a refused request opened a return", name)
		}
	}
}

// TestAStoreOrderRouteAnswersAnIdentityThatCouldNotDecide is ADR 0371's rule
// on this surface: an identity that could not reach a verdict is not a proof
// of nobody, so the answer is its own class and not the refusal's 404.
func TestAStoreOrderRouteAnswersAnIdentityThatCouldNotDecide(t *testing.T) {
	for _, route := range storeOrderRoutes {
		svc := gatedOrders("cus_1", false)
		r := storeAccessRouter(svc, provenAs{err: coreerrors.Unavailable("session_store_down", "down")})

		rec := storeRequest(t, r, route.method, route.path, route.body, "")

		assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "%s %s: %s",
			route.method, route.path, rec.Body.String())
	}
}

// TestAStoreOrderRouteAnswersAnUnknownOrder keeps the service's not-found as
// it is: the gate adds no answer of its own for an id that names nothing.
func TestAStoreOrderRouteAnswersAnUnknownOrder(t *testing.T) {
	for _, route := range storeOrderRoutes {
		svc := &fakeOrders{err: coreerrors.NotFound(service.CodeOrderNotFound, "order not found: order_1")}
		r := storeAccessRouter(svc, provenAs{customerID: "cus_1"})

		rec := storeRequest(t, r, route.method, route.path, route.body, "ordtok_1")

		assert.Equal(t, http.StatusNotFound, rec.Code, "%s %s", route.method, route.path)
		assert.Contains(t, rec.Body.String(), service.CodeOrderNotFound)
	}
}

// TestAnOutageAnswersTheSameForEveryID is the review's probe of 2026-10-11 as
// a test: during an identity outage an unknown id, a guest's order and a
// customer's order answered 404, 503 and 503, which told an existing order from
// none. The identity is asked before the order's existence is told, so all
// three answer the outage's own class, alike.
func TestAnOutageAnswersTheSameForEveryID(t *testing.T) {
	down := provenAs{err: coreerrors.Unavailable("session_store_down", "down")}
	cases := map[string]*fakeOrders{
		"an unknown id":      {err: coreerrors.NotFound(service.CodeOrderNotFound, "order not found: %s", "order_1")},
		"a guest's order":    gatedOrders("", false),
		"a customer's order": gatedOrders("cus_1", false),
	}
	for _, route := range storeOrderRoutes {
		answers := map[string]string{}
		for name, svc := range cases {
			rec := storeRequest(t, storeAccessRouter(svc, down), route.method, route.path, route.body, "")
			assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "%s, %s %s: %s",
				name, route.method, route.path, rec.Body.String())
			answers[name] = rec.Body.String()
		}
		for name, body := range answers {
			assert.JSONEq(t, answers["an unknown id"], body, "%s, %s %s told apart during an outage",
				name, route.method, route.path)
		}
	}
}

// TestAStoreOrderAnswerIsTheCallersAlone: whatever the gate decides, the answer
// carries no-store and varies on the credentials that opened it, so a shared
// cache in front of the storefront neither keeps an order nor serves a refusal
// to its owner.
func TestAStoreOrderAnswerIsTheCallersAlone(t *testing.T) {
	for _, route := range storeOrderRoutes {
		for name, svc := range map[string]*fakeOrders{
			"opened":  gatedOrders("", true),
			"refused": gatedOrders("", false),
		} {
			rec := storeRequest(t, storeAccessRouter(svc, nil), route.method, route.path, route.body, "ordtok_1")
			assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"), "%s, %s %s", name, route.method, route.path)
			assert.Contains(t, rec.Header().Get("Vary"), corehttp.OrderTokenHeader, "%s, %s %s", name, route.method, route.path)
		}
	}
}
