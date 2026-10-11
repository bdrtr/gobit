//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/personaldata"
)

// The independent review of ADR 0439 (2026-10-11), its probes brought in as
// tests on the production wiring: the idempotency ring replaying a gated write
// to a caller with no proof, a completed cart still read through its option
// list, an addition cart telling a stranger about an order, and an erased
// person's link still opening a retained order.

// keyedStorefrontProofRequest is a storefront request carrying an
// Idempotency-Key and, when not empty, an order token and a customer proof.
func keyedStorefrontProofRequest(
	t *testing.T, token, customer, key, method, path, body string,
) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(corehttp.PublishableKeyHeader, publishableKey)
	if token != "" {
		request.Header.Set(corehttp.OrderTokenHeader, token)
	}
	if customer != "" {
		request.Header.Set(customerHeader, customer)
	}
	request.Header.Set(corehttp.IdempotencyKeyHeader, key)

	recorder := httptest.NewRecorder()
	testRouter.ServeHTTP(recorder, request)

	return recorder
}

// TestAReturnsIdempotencyRecordIsReplayedOnlyToItsProof is the review's M1: a
// stranger with no token replaying the owner's key and body was answered the
// owner's recorded 201, reason included, and a stranger writing first under
// the owner's key recorded a 404 the owner was then answered.
func TestAReturnsIdempotencyRecordIsReplayedOnlyToItsProof(t *testing.T) {
	ctx := t.Context()
	customerID, email := newCustomer(ctx, t)
	placed := placedOrderFor(t, customerID, email, "E2E Review Replay")
	detail, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err)
	path := "/store/v1/orders/" + placed.OrderID + "/returns"
	body := fmt.Sprintf(`{"lines":[{"order_line_item_id":%q,"quantity":1}],"reason":"my private reason 0439"}`,
		detail.Items[0].ID)

	owner := keyedStorefrontProofRequest(t, placed.OrderToken, "", "return-"+placed.OrderID, http.MethodPost, path, body)
	require.Equal(t, http.StatusCreated, owner.Code, owner.Body.String())

	for name, proof := range map[string]struct{ token, customer string }{
		"no proof":                {},
		"another order's token":   {token: placedOrderFor(t, "", "e2e-replay-other@example.test", "E2E Review Replay Other").OrderToken},
		"another customer proven": {customer: aStranger},
	} {
		stranger := keyedStorefrontProofRequest(t, proof.token, proof.customer, "return-"+placed.OrderID, http.MethodPost, path, body)
		assert.Equal(t, http.StatusNotFound, stranger.Code, "%s was answered the owner's record: %s", name, stranger.Body.String())
		assert.Empty(t, stranger.Header().Get(corehttp.IdempotencyReplayedHeader), name)
		assert.NotContains(t, stranger.Body.String(), "my private reason 0439", name)
	}

	again := keyedStorefrontProofRequest(t, placed.OrderToken, "", "return-"+placed.OrderID, http.MethodPost, path, body)
	assert.Equal(t, "true", again.Header().Get(corehttp.IdempotencyReplayedHeader), "the owner's own retry is replayed")
	assert.Equal(t, owner.Body.String(), again.Body.String())

	first := keyedStorefrontProofRequest(t, "", "", "return2-"+placed.OrderID, http.MethodPost, path, body)
	require.Equal(t, http.StatusNotFound, first.Code, first.Body.String())
	owners := keyedStorefrontProofRequest(t, "", customerID, "return2-"+placed.OrderID, http.MethodPost, path, body)
	assert.Equal(t, http.StatusCreated, owners.Code, "a stranger's recorded refusal was answered to the owner: %s",
		owners.Body.String())
	assert.Empty(t, owners.Header().Get(corehttp.IdempotencyReplayedHeader))
}

// TestACustomersIdempotencyRecordIsReplayedOnlyToTheCustomer is the same
// class on a route that names a customer: the path's id is no secret, the
// identity is the proof, and a record of the customer's address is not
// answered to a caller proving somebody else or nobody.
func TestACustomersIdempotencyRecordIsReplayedOnlyToTheCustomer(t *testing.T) {
	customerID, _ := newCustomer(t.Context(), t)
	path := "/store/v1/customers/" + customerID + "/addresses"
	body := fmt.Sprintf(`{"first_name":"Ada","last_name":"Owner","address_1":"5 Private Lane","city":"Hometown",`+
		`"postal_code":"22222","country_code":%q}`, taxedCountry)

	owner := keyedStorefrontProofRequest(t, "", customerID, "address-"+customerID, http.MethodPost, path, body)
	require.Equal(t, http.StatusCreated, owner.Code, owner.Body.String())

	for name, customer := range map[string]string{"no proof": "", "another customer proven": aStranger} {
		stranger := keyedStorefrontProofRequest(t, "", customer, "address-"+customerID, http.MethodPost, path, body)
		assert.GreaterOrEqual(t, stranger.Code, 400, "%s: %s", name, stranger.Body.String())
		assert.Empty(t, stranger.Header().Get(corehttp.IdempotencyReplayedHeader), "%s was answered the customer's record", name)
		assert.NotContains(t, stranger.Body.String(), owner.Body.String(), name)
	}

	again := keyedStorefrontProofRequest(t, "", customerID, "address-"+customerID, http.MethodPost, path, body)
	assert.Equal(t, "true", again.Header().Get(corehttp.IdempotencyReplayedHeader), "the customer's own retry is replayed")
}

// TestEveryStorefrontCartRouteAnswersACompletedCartNotFound is the review's
// L3 over the production router: every route the router binds under
// /store/v1/carts/{id} answers a completed cart the unknown cart's 404, with
// nothing of the order's in the body.
func TestEveryStorefrontCartRouteAnswersACompletedCartNotFound(t *testing.T) {
	orderID := addressedOrder(t)
	detail, err := orderSvc.GetOrder(t.Context(), orderID)
	require.NoError(t, err)
	cartID := detail.CartID

	unknown := storefrontRequest(t, http.MethodGet, "/store/v1/carts/cart_00000000000000000000000000", "")
	require.Equal(t, http.StatusNotFound, unknown.Code)
	want := storeOrderError(t, unknown, "cart_00000000000000000000000000", "ID")

	var routes []string
	require.NoError(t, chi.Walk(testRouter, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/store/v1/carts/{id}") {
			routes = append(routes, method+" "+route)
		}

		return nil
	}))
	require.Contains(t, routes, "GET /store/v1/carts/{id}/shipping-options", "the router walk has gone blind")

	for _, route := range routes {
		method, pattern, _ := strings.Cut(route, " ")
		path := strings.NewReplacer("{id}", cartID, "{line_item_id}", "cali_x", "{code}", "NOPE",
			"{shipping_method_id}", "csm_x").Replace(pattern)
		rec := storefrontRequest(t, method, path, `{}`)
		assert.Equal(t, http.StatusNotFound, rec.Code, "%s answered a completed cart: %s", route, rec.Body.String())
		assert.Equal(t, want, storeOrderError(t, rec, cartID, "ID"), "%s told a completed cart from an unknown one", route)
		for _, leak := range []string{detail.Email, "9 Far Road", detail.CustomerID, orderID} {
			assert.NotContains(t, rec.Body.String(), leak, route)
		}
	}
}

// TestAnAdditionCartSaysNothingAboutAnotherCustomersOrder is the review's L4:
// opening a cart to add to an order told a guest and a proven stranger that the
// order exists, and its status; both are answered the unknown order's 404.
func TestAnAdditionCartSaysNothingAboutAnotherCustomersOrder(t *testing.T) {
	ctx := t.Context()
	ownerID, ownerEmail := newCustomer(ctx, t)
	placed := placedOrderFor(t, ownerID, ownerEmail, "E2E Review Addition Oracle")
	strangerID, strangerEmail := newCustomer(ctx, t)
	const unknownOrder = "order_00000000000000000000000009"

	answers := map[string][2]string{}
	for name, c := range map[string]struct{ customer, email, order string }{
		"guest, real order":       {"", "g@example.test", placed.OrderID},
		"guest, unknown order":    {"", "g@example.test", unknownOrder},
		"stranger, real order":    {strangerID, strangerEmail, placed.OrderID},
		"stranger, unknown order": {strangerID, strangerEmail, unknownOrder},
	} {
		rec := identifiedStorefrontRequest(t, c.customer, http.MethodPost, "/store/v1/carts",
			fmt.Sprintf(`{"country_code":%q,"customer_id":%q,"email":%q,"adds_to_order_id":%q}`,
				taxedCountry, c.customer, c.email, c.order))
		require.Equal(t, http.StatusNotFound, rec.Code, "%s: %s", name, rec.Body.String())
		answers[name] = storeOrderError(t, rec, c.order, "ID")
	}
	for name, got := range answers {
		assert.Equal(t, answers["guest, unknown order"], got, "%s told the order apart from none", name)
	}

	own := identifiedStorefrontRequest(t, ownerID, http.MethodPost, "/store/v1/carts",
		fmt.Sprintf(`{"country_code":%q,"customer_id":%q,"email":%q,"adds_to_order_id":%q}`,
			taxedCountry, ownerID, ownerEmail, placed.OrderID))
	assert.Equal(t, http.StatusCreated, own.Code, "the order's customer opens an addition: %s", own.Body.String())
}

// TestAnErasedPersonsLinkOpensNoOrder is the review's L8: the erasure
// withdraws the tokens of every order of the person, the one it retains while
// the order is still being performed too, so the link answers the unknown id's
// 404 while the customer's own proof still opens the order.
func TestAnErasedPersonsLinkOpensNoOrder(t *testing.T) {
	ctx := t.Context()
	customerID, email := newCustomer(ctx, t)
	placed := placedOrderFor(t, customerID, email, "E2E Review Erased Link")

	result, err := personalDataCoordinator(t).Erase(ctx, personaldata.Subject{CustomerID: customerID, Email: email})
	require.NoError(t, err)
	require.NotEmpty(t, result.Results, "the erasure asked no holder")

	link := tokenStorefrontRequest(t, placed.OrderToken, http.MethodGet, "/store/v1/orders/"+placed.OrderID, "")
	assert.Equal(t, http.StatusNotFound, link.Code, "the erased person's link opened the order: %s", link.Body.String())
	unknown := storefrontRequest(t, http.MethodGet, "/store/v1/orders/order_00000000000000000000000001", "")
	assert.Equal(t, storeOrderError(t, unknown, "order_00000000000000000000000001", "ID"),
		storeOrderError(t, link, placed.OrderID, "ID"))

	proven := identifiedStorefrontRequest(t, customerID, http.MethodGet, "/store/v1/orders/"+placed.OrderID, "")
	assert.Equal(t, http.StatusOK, proven.Code, "the customer's own proof opens the retained order: %s", proven.Body.String())
}

// openReturns counts the returns on the order that were not withdrawn.
func openReturns(t *testing.T, orderID string) int {
	t.Helper()

	detail, err := orderSvc.GetOrder(t.Context(), orderID)
	require.NoError(t, err)
	rec := adminCartRequest(t, http.MethodGet, "/admin/v1/orders/"+orderID+"/returns", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotEmpty(t, detail.Items)

	return int(decodeList(t, rec).Count)
}

// TestAKeyedReturnDuringAnOutageIsRefusedAndWritesNothing is the follow-up
// review's outage probe as a test: a return carrying the order's token and a
// session the identity cannot read, sent twice under one key, opened two
// returns when the ring served it without a record. The ring refuses it with
// the identity's 503 and nothing is written; once the session reads — here,
// without one — the same key's request is the first run, and its retry is
// replayed.
func TestAKeyedReturnDuringAnOutageIsRefusedAndWritesNothing(t *testing.T) {
	ctx := t.Context()
	customerID, email := newCustomer(ctx, t)
	placed := placedOrderFor(t, customerID, email, "E2E Review2 Outage Return")
	detail, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err)
	path := "/store/v1/orders/" + placed.OrderID + "/returns"
	body := fmt.Sprintf(`{"lines":[{"order_line_item_id":%q,"quantity":1}],"reason":"retried"}`, detail.Items[0].ID)
	key := "outage-return-" + placed.OrderID

	for range 2 {
		rec := keyedStorefrontProofRequest(t, placed.OrderToken, identityDown, key, http.MethodPost, path, body)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	}
	assert.Zero(t, openReturns(t, placed.OrderID), "a write was made while its proof could not be read")

	first := keyedStorefrontProofRequest(t, placed.OrderToken, "", key, http.MethodPost, path, body)
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())
	assert.Empty(t, first.Header().Get(corehttp.IdempotencyReplayedHeader), "the outage's refusal was recorded")
	retry := keyedStorefrontProofRequest(t, placed.OrderToken, "", key, http.MethodPost, path, body)
	assert.Equal(t, "true", retry.Header().Get(corehttp.IdempotencyReplayedHeader))
	assert.Equal(t, 1, openReturns(t, placed.OrderID), "one keyed return, retried, opened more than one")
}

// TestACartsKeyedWritesAreReplayedDuringAnOutage is the other half: a cart's
// routes carry their access in the path and ask the identity nothing, so
// during an outage a retried line is not added twice and a retried completion
// is replayed with its order token, as before ADR 0439, rather than answered
// the completed cart's 404.
func TestACartsKeyedWritesAreReplayedDuringAnOutage(t *testing.T) {
	ctx := t.Context()
	_, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Review2 Outage Cart", map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, happyInitialStock)
	cartID := openStorefrontCartInCountry(t, taxedCountry, "", email)
	line := fmt.Sprintf(`{"variant_id":%q,"quantity":1}`, variantID)

	added := keyedStorefrontProofRequest(t, "", identityDown, "add-"+cartID, http.MethodPost,
		"/store/v1/carts/"+cartID+"/line-items", line)
	require.Equal(t, http.StatusCreated, added.Code, added.Body.String())
	again := keyedStorefrontProofRequest(t, "", identityDown, "add-"+cartID, http.MethodPost,
		"/store/v1/carts/"+cartID+"/line-items", line)
	assert.Equal(t, "true", again.Header().Get(corehttp.IdempotencyReplayedHeader), "a cart's write asked the identity")
	cart, err := cartSvc.GetCart(ctx, cartID)
	require.NoError(t, err)
	var quantity int64
	for _, item := range cart.Items {
		quantity += item.Quantity
	}
	assert.Equal(t, int64(1), quantity, "one keyed line add, retried during an outage, added the line twice")

	read := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+cartID, "")
	require.Equal(t, http.StatusOK, read.Code)
	total, _ := storefrontData(t, read)["total"].(float64)
	body := storefrontCompletionBody(t, int64(total))
	done := keyedStorefrontProofRequest(t, "", identityDown, "complete-"+cartID, http.MethodPost,
		"/store/v1/carts/"+cartID+"/complete", body)
	require.Equal(t, http.StatusOK, done.Code, done.Body.String())
	retried := keyedStorefrontProofRequest(t, "", identityDown, "complete-"+cartID, http.MethodPost,
		"/store/v1/carts/"+cartID+"/complete", body)
	require.Equal(t, http.StatusOK, retried.Code, "the retried completion lost its answer: %s", retried.Body.String())
	assert.Equal(t, "true", retried.Header().Get(corehttp.IdempotencyReplayedHeader))
	assert.Equal(t, storefrontData(t, done)["order_token"], storefrontData(t, retried)["order_token"],
		"the retried completion lost its order token")
}

// TestAMergeOfACompletedCartAnswersAsAnUnknownOne is the follow-up review's
// low finding: a completed cart named as a merge's source answered 409
// cart_completed while an unknown one answered 404, which told a cart id's
// holder the cart exists and completed.
func TestAMergeOfACompletedCartAnswersAsAnUnknownOne(t *testing.T) {
	ctx := t.Context()
	orderID := addressedOrder(t)
	detail, err := orderSvc.GetOrder(ctx, orderID)
	require.NoError(t, err)
	_, email := newCustomer(ctx, t)
	mine := openStorefrontCartInCountry(t, taxedCountry, "", email)
	const unknown = "cart_00000000000000000000000000"

	completed := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+mine+"/merge",
		fmt.Sprintf(`{"source_cart_id":%q}`, detail.CartID))
	missing := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+mine+"/merge",
		fmt.Sprintf(`{"source_cart_id":%q}`, unknown))
	require.Equal(t, http.StatusNotFound, missing.Code, missing.Body.String())
	assert.Equal(t, http.StatusNotFound, completed.Code, completed.Body.String())
	assert.Equal(t, storeOrderError(t, missing, unknown, "ID"), storeOrderError(t, completed, detail.CartID, "ID"))
}
