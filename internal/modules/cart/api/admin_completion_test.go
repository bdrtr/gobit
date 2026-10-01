package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/cart/api"
)

// The rest of a telephone order (ADR 0286): the operator writes the addresses
// and the shipping method and completes the cart with an offline method.

// telephoneCompletion is the body an operator sends, and offlineFlowAnswer is
// what the flow answers for an order that owes its total.
const (
	telephoneCompletion = `{"sales_channel_id":"sc_phone","payment_provider_id":"bank_transfer","expected_total":3600}`
	offlineFlowAnswer   = `{"order_id":"order_1","cart_id":"cart_1","currency_code":"TRY","amount":3600,"outstanding":3600}`
)

// TestAnOperatorCompletesATelephoneOrder: the flow is asked for an offline
// method only, under the channel the operator names, with the cart's own
// contact address, and the answer says what the order owes.
func TestAnOperatorCompletesATelephoneOrder(t *testing.T) {
	flow := &fakeCheckout{response: json.RawMessage(offlineFlowAnswer)}
	h := newServerWithFlows(t, withLineItem(), api.Flows{Checkout: flow})

	rec := doRequestAs(t, h, &adminWriter, http.MethodPost, "/admin/v1/carts/cart_1/complete", telephoneCompletion)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, flow.calls)
	data := object(t, bodyMap(t, rec)["data"])
	assert.Equal(t, "order_1", data["order_id"])
	assert.InDelta(t, 3600, data["outstanding"], 0.0, "the order owes its total")

	sent := map[string]any{}
	require.NoError(t, json.Unmarshal(flow.got, &sent))
	assert.Equal(t, "cart_1", sent["cart_id"])
	assert.Equal(t, "bank_transfer", sent["payment_provider_id"])
	assert.Equal(t, true, sent["offline_only"], "the flow refuses a provider it would capture")
	assert.Equal(t, []any{"sc_phone"}, sent["sales_channel_ids"], "the operator's claim narrows the warehouses")
	assert.InDelta(t, 3600, sent["expected_total"], 0.0)
	assert.Equal(t, "a@b.c", sent["email"], "the contact address is the cart's")
	assert.Equal(t, adminWriter.ID, sent["placed_by"], "the order names the operator who placed it (ADR 0298)")
}

// TestTheStorefrontsCompletionIsNotOfflineOnly: a shopper pays with any
// provider, so the storefront never sets the flag.
func TestTheStorefrontsCompletionIsNotOfflineOnly(t *testing.T) {
	flow := &fakeCheckout{response: json.RawMessage(offlineFlowAnswer)}
	h := newServerWithFlows(t, withLineItem(), api.Flows{Checkout: flow})

	rec := doRequest(t, h, http.MethodPost, "/store/v1/carts/cart_1/complete",
		`{"payment_provider_id":"bank_transfer","expected_total":3600}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	sent := map[string]any{}
	require.NoError(t, json.Unmarshal(flow.got, &sent))
	_, set := sent["offline_only"]
	assert.False(t, set)
	_, named := sent["placed_by"]
	assert.False(t, named, "a shopper's order names no operator (ADR 0298)")
	assert.InDelta(t, 3600, object(t, bodyMap(t, rec)["data"])["outstanding"], 0.0)
}

// TestAnOperatorsCompletionNamesItsChannelAndItsTotal: either missing is a
// 422 before the flow is asked.
func TestAnOperatorsCompletionNamesItsChannelAndItsTotal(t *testing.T) {
	for name, body := range map[string]string{
		"no channel": `{"payment_provider_id":"bank_transfer","expected_total":3600}`,
		"no total":   `{"sales_channel_id":"sc_phone","payment_provider_id":"bank_transfer"}`,
	} {
		t.Run(name, func(t *testing.T) {
			flow := &fakeCheckout{response: json.RawMessage(offlineFlowAnswer)}
			h := newServerWithFlows(t, withLineItem(), api.Flows{Checkout: flow})

			rec := doRequestAs(t, h, &adminWriter, http.MethodPost, "/admin/v1/carts/cart_1/complete", body)

			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			assert.Zero(t, flow.calls)
		})
	}
}

// TestAnOperatorsCompletionTakesNoTenderOfTheCustomers: a gift card code or a
// balance is refused rather than ignored — the customer is not there to
// present one, and an accepted-but-unused field is a promise not kept.
func TestAnOperatorsCompletionTakesNoTenderOfTheCustomers(t *testing.T) {
	for name, field := range map[string]string{
		"a gift card":  `"gift_card_code":"ABCD-EFGH-JKMN-PQRS"`,
		"a balance":    `"pay_first_with":["store_credit"]`,
		"payment data": `"payment_data":{"token":"tok_1"}`,
	} {
		t.Run(name, func(t *testing.T) {
			flow := &fakeCheckout{response: json.RawMessage(offlineFlowAnswer)}
			h := newServerWithFlows(t, withLineItem(), api.Flows{Checkout: flow})

			rec := doRequestAs(t, h, &adminWriter, http.MethodPost, "/admin/v1/carts/cart_1/complete",
				`{"sales_channel_id":"sc_phone","payment_provider_id":"bank_transfer","expected_total":3600,`+field+`}`)

			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			assert.Zero(t, flow.calls)
		})
	}
}

// TestTheTelephoneOrderWritesAskForTheWriteScope: each of the five routes is
// bound behind cart:write — a reader is refused with 403, which an unbound
// route would answer with 404 or 405.
func TestTheTelephoneOrderWritesAskForTheWriteScope(t *testing.T) {
	reader := corehttp.Principal{ID: "user_reader", Kind: "user", Scopes: []string{"cart:read"}}

	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPut, "/admin/v1/carts/cart_1/shipping-address", `{}`},
		{http.MethodPut, "/admin/v1/carts/cart_1/billing-address", `{}`},
		{http.MethodPost, "/admin/v1/carts/cart_1/shipping-methods", `{"shipping_option_id":"so_1"}`},
		{http.MethodDelete, "/admin/v1/carts/cart_1/shipping-methods/sm_1", ``},
		{http.MethodPost, "/admin/v1/carts/cart_1/complete", telephoneCompletion},
	} {
		flow := &fakeCheckout{response: json.RawMessage(offlineFlowAnswer)}
		h := newServerWithFlows(t, withLineItem(), api.Flows{Checkout: flow})

		rec := doRequestAs(t, h, &reader, tc.method, tc.path, tc.body)

		assert.Equal(t, http.StatusForbidden, rec.Code, "%s %s: %s", tc.method, tc.path, rec.Body.String())
		assert.Zero(t, flow.calls)
	}
}
