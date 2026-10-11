//go:build integration

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	ordermodels "github.com/bdrtr/gobit/internal/modules/order/models"
	paymentmanual "github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// This file is ADR 0439 on the production wiring: an id gobit hands out opens
// nothing at the storefront.
//
// The second party here is not a guesser. It is a webhook receiver the
// operator registered: gobit itself hands it the order id, and withholds the
// customer id from the same delivery because "a customer id is a lever on that
// customer" (plugins/webhookout, redactedFields). The harness binds a customer
// identity, so the installation did everything the identity records ask of it
// (ADR 0043, 0057, 0125): the customer's own order list refuses the same
// caller.
//
// Before the fix the receiver read the shopper's e-mail and customer id off
// the order, opened a return that held the owner's units, read the tracking
// number off the timeline, followed the order's cart id to the completed
// cart's addresses, read and moved a live cart through cart.created's cart id,
// and read a collection's figures through payment.captured's collection id
// (the reproduction is in the commit that closed D280). Each test below holds
// one of those shut and the shopper's own way in open.

// storeOrderAccessBody cuts a response body down for a log line.
func storeOrderAccessBody(rec *httptest.ResponseRecorder) string {
	body := rec.Body.String()
	if len(body) > 600 {
		return body[:600] + "…"
	}

	return body
}

// storeOrderError is a refusal's code and message with the order id it names
// written as named, so two refusals of two ids compare; the request id is left
// out, since every answer has its own.
func storeOrderError(t *testing.T, rec *httptest.ResponseRecorder, id, as string) [2]string {
	t.Helper()

	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), rec.Body.String())

	return [2]string{envelope.Error.Code, strings.ReplaceAll(envelope.Error.Message, id, as)}
}

// tokenStorefrontRequest makes a storefront request presenting an order's
// access token, as a guest's order page does (ADR 0439).
func tokenStorefrontRequest(t *testing.T, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(corehttp.PublishableKeyHeader, publishableKey)
	request.Header.Set(corehttp.OrderTokenHeader, token)

	recorder := httptest.NewRecorder()
	testRouter.ServeHTTP(recorder, request)

	return recorder
}

// shopperOrderRequest makes a storefront request on an order as its shopper
// does: with an access token the order issued, here one issued for the call.
func shopperOrderRequest(t *testing.T, orderID, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	token, err := orderSvc.IssueAccessToken(t.Context(), orderID)
	require.NoError(t, err, "no access token could be issued for %s", orderID)

	return tokenStorefrontRequest(t, token, method, path, body)
}

// heardOrderPlaced waits for the receiver's order.placed delivery about the
// placed order and answers its data.
func heardOrderPlaced(t *testing.T, receiver *webhookReceiver, orderID string) map[string]any {
	t.Helper()

	var heard map[string]any
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		if !assert.NoError(c, runWebhookJob(t)) {
			return
		}
		for _, delivery := range receiver.deliveries() {
			data, _ := delivery.body["data"].(map[string]any)
			if delivery.event == "order.placed" && delivery.verified && data["order_id"] == orderID {
				heard = data
			}
		}
		assert.NotNil(c, heard)
	}, 15*time.Second, 200*time.Millisecond, "no verified order.placed delivery arrived for %s", orderID)

	return heard
}

// placedOrderFor places a two-unit order for the customer ("" for a guest)
// through the checkout, asking for the completion's token.
func placedOrderFor(t *testing.T, customerID, email, product string) checkoutwf.CompleteCartResult {
	t.Helper()
	ctx := t.Context()

	variantID, _ := newStockedVariant(ctx, t, product, map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, happyInitialStock)
	cartID, _ := prepareCart(ctx, t, customerID, variantID, happyQuantity)
	placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID: cartID, LocationID: stockLocationID, PaymentProviderID: paymentmanual.ID,
		PaymentData: paymentBehavior(t, paymentmanual.OutcomeAuthorize), Email: email,
		ExpectedTotal: happyTotal, IssueOrderToken: true,
	})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(placed.OrderToken, ordermodels.AccessTokenPrefix),
		"the completion issued no token: %q", placed.OrderToken)

	return placed
}

// TestAWebhookReceiverReadsNoShoppersOrderAndOpensNoReturn is the registered
// customer's case: the receiver's order id opens nothing, and the shopper's
// token and the shopper's own proof open the order, its timeline and a return.
func TestAWebhookReceiverReadsNoShoppersOrderAndOpensNoReturn(t *testing.T) {
	ctx := t.Context()

	receiver := newWebhookReceiver(t)
	_, secret := registerWebhook(t, receiver.server.URL, "order.placed")
	receiver.withSecret(secret)

	customerID, email := newCustomer(ctx, t)
	placed := placedOrderFor(t, customerID, email, "E2E Store Order Access")

	// The receiver holds the order id and, as gobit promises, not the customer.
	heard := heardOrderPlaced(t, receiver, placed.OrderID)
	orderID, _ := heard["order_id"].(string)
	require.Equal(t, placed.OrderID, orderID)
	require.NotContains(t, heard, "customer_id", "webhookout withholds the customer id")

	// The customer's own list, asked by the receiver: refused, as ADR 0367 says.
	list := identifiedStorefrontRequest(t, "", http.MethodGet, "/store/v1/customers/"+customerID+"/orders", "")
	require.NotEqual(t, http.StatusOK, list.Code, "the customer's list is the proven half")

	unknown := storefrontRequest(t, http.MethodGet, "/store/v1/orders/order_00000000000000000000000000", "")
	require.Equal(t, http.StatusNotFound, unknown.Code, unknown.Body.String())

	// The receiver, the order id it was handed and the shop's public key.
	read := storefrontRequest(t, http.MethodGet, "/store/v1/orders/"+orderID, "")
	t.Logf("GET /store/v1/orders/{id}, publishable key alone: %d %s", read.Code, storeOrderAccessBody(read))
	assert.Equal(t, http.StatusNotFound, read.Code,
		"an order id gobit handed a webhook receiver opened the shopper's order")
	assert.NotContains(t, read.Body.String(), email,
		"the order read gave the receiver the shopper's e-mail, which the event leaves out")
	assert.NotContains(t, read.Body.String(), customerID,
		"the order read gave the receiver the customer id the webhook redacted")
	assert.Equal(t, storeOrderError(t, unknown, "order_00000000000000000000000000", orderID),
		storeOrderError(t, read, orderID, orderID), "the refusal is told apart from an unknown id's answer")

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"proving another customer": identifiedStorefrontRequest(t, aStranger, http.MethodGet,
			"/store/v1/orders/"+orderID, ""),
		"a token the order did not issue": tokenStorefrontRequest(t, ordermodels.AccessTokenPrefix+"forged",
			http.MethodGet, "/store/v1/orders/"+orderID, ""),
		"the timeline, publishable key alone": storefrontRequest(t, http.MethodGet,
			"/store/v1/orders/"+orderID+"/timeline", ""),
	} {
		assert.Equal(t, http.StatusNotFound, rec.Code, "%s: %s", name, storeOrderAccessBody(rec))
	}

	// The return: the receiver names the lines (read here off the service, as a
	// receiver that guessed them would) and is refused before anything is held.
	detail, err := orderSvc.GetOrder(ctx, orderID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	lineID := detail.Items[0].ID
	opened := storefrontRequest(t, http.MethodPost, "/store/v1/orders/"+orderID+"/returns",
		fmt.Sprintf(`{"lines":[{"order_line_item_id":%q,"quantity":%d}],"reason":"not mine"}`, lineID, happyQuantity))
	t.Logf("POST /store/v1/orders/{id}/returns, publishable key alone: %d %s", opened.Code, storeOrderAccessBody(opened))
	assert.Equal(t, http.StatusNotFound, opened.Code,
		"an order id gobit handed a webhook receiver opened a return on the shopper's order")

	// The shopper: the completion's token opens the record, which names neither
	// the cart nor the customer, and the timeline.
	own := tokenStorefrontRequest(t, placed.OrderToken, http.MethodGet, "/store/v1/orders/"+orderID, "")
	require.Equal(t, http.StatusOK, own.Code, own.Body.String())
	record := storefrontData(t, own)
	assert.Equal(t, email, record["email"], "the shopper reads their own address")
	assert.NotContains(t, record, "cart_id", "the storefront record names the order's cart")
	assert.NotContains(t, record, "customer_id", "the storefront record names the order's customer")
	assert.NotContains(t, own.Body.String(), placed.OrderToken, "the record carries the token")
	timeline := tokenStorefrontRequest(t, placed.OrderToken, http.MethodGet, "/store/v1/orders/"+orderID+"/timeline", "")
	assert.Equal(t, http.StatusOK, timeline.Code, timeline.Body.String())

	// The owner proving who they are reads the order and sends one unit back:
	// the receiver's request held nothing.
	proven := identifiedStorefrontRequest(t, customerID, http.MethodGet, "/store/v1/orders/"+orderID, "")
	assert.Equal(t, http.StatusOK, proven.Code, proven.Body.String())
	returned := identifiedStorefrontRequest(t, customerID, http.MethodPost, "/store/v1/orders/"+orderID+"/returns",
		fmt.Sprintf(`{"lines":[{"order_line_item_id":%q,"quantity":1}],"reason":"too small"}`, lineID))
	assert.Equal(t, http.StatusCreated, returned.Code,
		"the owner's own return was refused: %s", storeOrderAccessBody(returned))
	byToken := tokenStorefrontRequest(t, placed.OrderToken, http.MethodPost, "/store/v1/orders/"+orderID+"/returns",
		fmt.Sprintf(`{"lines":[{"order_line_item_id":%q,"quantity":1}],"reason":"the other one"}`, lineID))
	assert.Equal(t, http.StatusCreated, byToken.Code, "the token opens a return: %s", storeOrderAccessBody(byToken))
}

// TestAGuestsOrderIsNotReadByItsID is the guest's case: no customer to prove,
// an e-mail on the order all the same, and the completion's token the one way
// in — the token of this order, not of another.
func TestAGuestsOrderIsNotReadByItsID(t *testing.T) {
	receiver := newWebhookReceiver(t)
	_, secret := registerWebhook(t, receiver.server.URL, "order.placed")
	receiver.withSecret(secret)

	email := fmt.Sprintf("e2e-guest-%d@example.test", fixtureCounter.Add(1))
	placed := placedOrderFor(t, "", email, "E2E Guest Order Access")
	other := placedOrderFor(t, "", fmt.Sprintf("e2e-guest-%d@example.test", fixtureCounter.Add(1)),
		"E2E Other Guest Order Access")

	heard := heardOrderPlaced(t, receiver, placed.OrderID)
	orderID, _ := heard["order_id"].(string)

	read := storefrontRequest(t, http.MethodGet, "/store/v1/orders/"+orderID, "")
	t.Logf("guest order, GET /store/v1/orders/{id}, publishable key alone: %d %s",
		read.Code, storeOrderAccessBody(read))
	assert.Equal(t, http.StatusNotFound, read.Code,
		"an order id gobit handed a webhook receiver opened a guest's order")
	assert.NotContains(t, read.Body.String(), email, "the guest's e-mail reached the receiver")

	foreign := tokenStorefrontRequest(t, other.OrderToken, http.MethodGet, "/store/v1/orders/"+orderID, "")
	assert.Equal(t, http.StatusNotFound, foreign.Code, "another order's token opened this one")
	proving := identifiedStorefrontRequest(t, aStranger, http.MethodGet, "/store/v1/orders/"+orderID, "")
	assert.Equal(t, http.StatusNotFound, proving.Code, "a proven customer opened a guest's order")

	own := tokenStorefrontRequest(t, placed.OrderToken, http.MethodGet, "/store/v1/orders/"+orderID, "")
	require.Equal(t, http.StatusOK, own.Code, own.Body.String())
	assert.Equal(t, email, storefrontData(t, own)["email"], "the guest reads their own address")
}

// TestAnOrderReadLeadsToNoAddress is the address half: the storefront record
// names no cart, and the completed cart the order was born from is not read
// at the storefront, so the addresses ADR 0193 keeps on the admin record are
// not one request away.
func TestAnOrderReadLeadsToNoAddress(t *testing.T) {
	orderID := addressedOrder(t)

	read := shopperOrderRequest(t, orderID, http.MethodGet, "/store/v1/orders/"+orderID, "")
	require.Equal(t, http.StatusOK, read.Code, "body: %s", read.Body.String())
	order := storefrontData(t, read)
	require.NotContains(t, order, "shipping_address", "ADR 0193: the storefront order read carries no address")
	assert.NotContains(t, order, "cart_id", "the storefront order read names the cart")

	detail, err := orderSvc.GetOrder(t.Context(), orderID)
	require.NoError(t, err)
	require.NotEmpty(t, detail.CartID)
	cart := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+detail.CartID, "")
	t.Logf("GET /store/v1/carts/{completed cart}, publishable key alone: %d %s", cart.Code, storeOrderAccessBody(cart))
	assert.Equal(t, http.StatusNotFound, cart.Code, "a completed cart is read at the storefront")
	assert.Contains(t, cart.Body.String(), "cart_not_found", "the refusal is an unknown cart's")
	assert.NotContains(t, cart.Body.String(), "9 Far Road",
		"the completed cart opened the home address ADR 0193 keeps off the storefront")
}

// cartEventKeyFor is the digest a cart's event id names it by (ADR 0439),
// written out here from the rule so that a change to it fails this file.
func cartEventKeyFor(cartID string) string {
	sum := sha256.Sum256([]byte(cartID))

	return hex.EncodeToString(sum[:16])
}

// TestACartCreatedDeliveryOpensNoCart is the cart's capability under the same
// receiver: cart.created reaches it with the region, the currency and the
// moment, and with no cart id anywhere in the delivery, while the shopper's
// own storefront still reads the cart by its id.
func TestACartCreatedDeliveryOpensNoCart(t *testing.T) {
	ctx := t.Context()

	receiver := newWebhookReceiver(t)
	_, secret := registerWebhook(t, receiver.server.URL, "cart.created")
	receiver.withSecret(secret)

	customerID, email := newCustomer(ctx, t)
	cartID := openStorefrontCartInCountry(t, taxedCountry, customerID, email)
	home := fmt.Sprintf(`{"first_name":"Ada","last_name":"Shopper","address_1":"1 Home Street",`+
		`"city":"Hometown","postal_code":"22222","country_code":%q,"phone":"+905550000000"}`, taxedCountry)
	set := storefrontRequest(t, http.MethodPut, "/store/v1/carts/"+cartID+"/shipping-address", home)
	require.Equal(t, http.StatusOK, set.Code, set.Body.String())

	eventID := "cart.created:" + cartEventKeyFor(cartID)
	var heard webhookDelivery
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		if !assert.NoError(c, runWebhookJob(t)) {
			return
		}
		for _, delivery := range receiver.deliveries() {
			if delivery.event == "cart.created" && delivery.verified && delivery.body["event_id"] == eventID {
				heard = delivery
			}
		}
		assert.NotNil(c, heard.body)
	}, 15*time.Second, 200*time.Millisecond, "no verified cart.created delivery arrived as %s", eventID)
	raw, err := json.Marshal(heard.body)
	require.NoError(t, err)
	t.Logf("cart.created delivery: %s", raw)

	assert.NotContains(t, string(raw), cartID, "a cart.created delivery carried the cart's id, which is its access")
	data, _ := heard.body["data"].(map[string]any)
	assert.NotContains(t, data, "cart_id")
	assert.Contains(t, heard.body["redacted"], "cart_id", "the withholding is not visible to the receiver")
	for _, field := range []string{"region_id", "currency_code", "occurred_at"} {
		assert.Contains(t, data, field, "the delivery lost what a funnel counts by")
	}

	// The shopper's storefront holds the id and keeps its way in.
	own := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+cartID, "")
	assert.Equal(t, http.StatusOK, own.Code, own.Body.String())
	assert.Contains(t, own.Body.String(), "1 Home Street")
}

// TestAParcelDeliveryOpensNoTrackingNumber is the timeline's half: the
// fulfillment.delivered delivery carries no tracking number by design and
// names the order in "reference", and the storefront timeline of that order
// opens to the shopper alone.
func TestAParcelDeliveryOpensNoTrackingNumber(t *testing.T) {
	ctx := t.Context()

	receiver := newWebhookReceiver(t)
	_, secret := registerWebhook(t, receiver.server.URL, "fulfillment.delivered")
	receiver.withSecret(secret)

	orderID, lineID, _ := placedForParcels(ctx, t, "E2E Tracking Access")
	profileID := newShippingProfile(ctx, t, "E2E Tracking Access Profile")
	optionID := newShippingOption(ctx, t, profileID, "E2E Tracking Access Shipping", shippingOptionFee, false)
	code, parcelID, body := openOrderParcel(t, orderID, map[string]any{
		"shipping_option_id": optionID,
		"idempotency_key":    "e2e-tracking-access-" + orderID,
		"items":              []map[string]any{{"line_item_id": lineID, "quantity": cancelQuantity}},
	})
	require.Equal(t, http.StatusCreated, code, "body: %s", body)
	const tracking = "TRK-E2E-ACCESS-0001"
	shipped, err := adminRequestWithBody(http.MethodPost, "/admin/v1/fulfillments/"+parcelID+"/ship",
		map[string]any{"tracking_number": tracking})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, shipped.Code, "body: %s", shipped.Body.String())
	delivered, err := adminRequestWithBody(http.MethodPost, "/admin/v1/fulfillments/"+parcelID+"/deliver", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, delivered.Code, "body: %s", delivered.Body.String())

	var heard map[string]any
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		if !assert.NoError(c, runWebhookJob(t)) {
			return
		}
		for _, delivery := range receiver.deliveries() {
			data, _ := delivery.body["data"].(map[string]any)
			if delivery.event == "fulfillment.delivered" && delivery.verified && data["fulfillment_id"] == parcelID {
				heard = data
			}
		}
		assert.NotNil(c, heard)
	}, 15*time.Second, 200*time.Millisecond, "no verified fulfillment.delivered delivery arrived for %s", parcelID)
	require.NotContains(t, heard, "tracking_number", "ADR 0437: the delivery carries no tracking number")
	reference, _ := heard["reference"].(string)
	require.Equal(t, orderID, reference)

	timeline := storefrontRequest(t, http.MethodGet, "/store/v1/orders/"+reference+"/timeline", "")
	t.Logf("GET /store/v1/orders/{reference}/timeline, publishable key alone: %d %s",
		timeline.Code, storeOrderAccessBody(timeline))
	assert.Equal(t, http.StatusNotFound, timeline.Code)
	assert.NotContains(t, timeline.Body.String(), tracking,
		"the order the delivery names handed its receiver the tracking number the delivery withholds")

	own := shopperOrderRequest(t, orderID, http.MethodGet, "/store/v1/orders/"+orderID+"/timeline", "")
	require.Equal(t, http.StatusOK, own.Code, own.Body.String())
	assert.Contains(t, own.Body.String(), tracking, "the shopper reads their parcel's tracking number")
}

// TestAPaymentCapturedDeliveryOpensNoCollection is the payment module's copy:
// payment.captured carries the collection and no amount, and the storefront
// takes no collection id at all, nor reads the completed cart the collection
// paid for.
func TestAPaymentCapturedDeliveryOpensNoCollection(t *testing.T) {
	receiver := newWebhookReceiver(t)
	_, secret := registerWebhook(t, receiver.server.URL, "payment.captured")
	receiver.withSecret(secret)

	orderID, collectionID, _ := placedHappyOrder(t, "E2E Collection Access")

	var heard map[string]any
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		if !assert.NoError(c, runWebhookJob(t)) {
			return
		}
		for _, delivery := range receiver.deliveries() {
			data, _ := delivery.body["data"].(map[string]any)
			if delivery.event == "payment.captured" && delivery.verified && data["payment_collection_id"] == collectionID {
				heard = data
			}
		}
		assert.NotNil(c, heard)
	}, 15*time.Second, 200*time.Millisecond, "no verified payment.captured delivery arrived for %s", collectionID)

	for _, request := range []struct{ method, path string }{
		{http.MethodGet, "/store/v1/payment-collections/" + collectionID},
		{http.MethodPost, "/store/v1/payment-collections/" + collectionID + "/payment-sessions"},
	} {
		rec := storefrontRequest(t, request.method, request.path, `{"provider_id":"manual","idempotency_key":"k"}`)
		t.Logf("%s %s, publishable key alone: %d %s", request.method, request.path, rec.Code, storeOrderAccessBody(rec))
		assert.Equal(t, http.StatusNotFound, rec.Code,
			"%s: a collection id gobit handed a webhook receiver opened the collection", request.path)
	}

	detail, err := orderSvc.GetOrder(t.Context(), orderID)
	require.NoError(t, err)
	cart := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+detail.CartID, "")
	assert.Equal(t, http.StatusNotFound, cart.Code, "the collection's cart is read at the storefront")
}

// TestAStorefrontCompletionAnswersTheOrdersToken is the shopper's half over
// HTTP: the completion answers the order's token once, the token opens the
// order, a repeat under the same Idempotency-Key answers the recorded token,
// and the order mail carries a token of its own that opens it too.
func TestAStorefrontCompletionAnswersTheOrdersToken(t *testing.T) {
	ctx := t.Context()

	_, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Completion Token", map[string]int64{
		taxedCurrency: happyUnitPrice,
	}, happyInitialStock)
	cartID, totals := prepareCart(ctx, t, "", variantID, happyQuantity)
	contact := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID, fmt.Sprintf(`{"email":%q}`, email))
	require.Equal(t, http.StatusOK, contact.Code, contact.Body.String())

	complete := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
			strings.NewReader(storefrontCompletionBody(t, totals.Total)))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(corehttp.PublishableKeyHeader, publishableKey)
		request.Header.Set(corehttp.IdempotencyKeyHeader, "e2e-completion-token-"+cartID)
		recorder := httptest.NewRecorder()
		testRouter.ServeHTTP(recorder, request)

		return recorder
	}
	done := complete()
	require.Equal(t, http.StatusOK, done.Code, done.Body.String())
	answer := storefrontData(t, done)
	orderID, _ := answer["order_id"].(string)
	token, _ := answer["order_token"].(string)
	require.True(t, strings.HasPrefix(token, ordermodels.AccessTokenPrefix), "the completion answered no token: %v", answer)

	read := tokenStorefrontRequest(t, token, http.MethodGet, "/store/v1/orders/"+orderID, "")
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	assert.NotContains(t, read.Body.String(), token, "the order record carries its token")

	replayed := complete()
	require.Equal(t, http.StatusOK, replayed.Code, replayed.Body.String())
	assert.Equal(t, token, storefrontData(t, replayed)["order_token"],
		"the idempotency record answers the recorded response, token included (docs/known-limits.md)")

	var mailed string
	require.Eventually(t, func() bool {
		for _, sent := range notificationSpy.notificationsFor(orderID) {
			if sent.Template == "order.placed" {
				mailed = sent.Data["order_token"]
			}
		}

		return mailed != ""
	}, 10*time.Second, 50*time.Millisecond, "the order mail carried no token")
	assert.NotEqual(t, token, mailed, "the mail carries the completion's token rather than its own")
	fromMail := tokenStorefrontRequest(t, mailed, http.MethodGet, "/store/v1/orders/"+orderID, "")
	assert.Equal(t, http.StatusOK, fromMail.Code, fromMail.Body.String())
}
