package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/api"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// The operator's writes reach only an operator's cart (ADR 0299, D200).

// TestTheAdminWritesRefuseAShoppersCart: every admin write on a cart in the
// path answers 409 on a cart a shopper opened, and nothing behind it is asked.
func TestTheAdminWritesRefuseAShoppersCart(t *testing.T) {
	for _, tc := range []struct {
		method, path, body string
	}{
		{http.MethodPost, "/admin/v1/carts/cart_1/line-items", `{"sales_channel_id":"sc_phone","variant_id":"var_1","quantity":1}`},
		{http.MethodPut, "/admin/v1/carts/cart_1/shipping-address", `{"first_name":"Not","address_1":"Elsewhere 1","country_code":"TR"}`},
		{http.MethodPut, "/admin/v1/carts/cart_1/billing-address", `{"first_name":"Not","address_1":"Elsewhere 1","country_code":"TR"}`},
		{http.MethodPost, "/admin/v1/carts/cart_1/shipping-methods", `{"shipping_option_id":"so_desk"}`},
		{http.MethodDelete, "/admin/v1/carts/cart_1/shipping-methods/sm_1", ""},
		{http.MethodPost, "/admin/v1/carts/cart_1/complete", telephoneCompletion},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			svc := withLineItem()
			pricing := &fakePricing{lineID: "li_2"}
			shipping := &fakeShipping{options: twoOptions, methodID: "sm_2"}
			checkout := &fakeCheckout{response: json.RawMessage(offlineFlowAnswer)}
			h := newServerWithFlows(t, svc, api.Flows{
				Pricing: pricing, Shipping: shipping, Checkout: checkout, Repricing: &fakeRepricing{},
			})

			rec := doRequestAs(t, h, &adminWriter, tc.method, tc.path, tc.body)

			assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "cart_opened_by_shopper")
			assert.Zero(t, pricing.calls, "no line was priced")
			assert.Zero(t, shipping.calls, "no shipping was chosen")
			assert.Zero(t, checkout.calls, "no order was placed")
			assert.Equal(t, service.AddressInput{}, svc.addressInput, "no address was written")
			assert.Empty(t, svc.gotMethodID, "no method was removed")
		})
	}
}

// TestTheAdminWritesReachAnOperatorsCart: the same doors pass a cart an
// operator opened, so the refusal is the opener's and not the door's.
func TestTheAdminWritesReachAnOperatorsCart(t *testing.T) {
	svc := withOperatorsLineItem()
	checkout := &fakeCheckout{response: json.RawMessage(offlineFlowAnswer)}
	h := newServerWithFlows(t, svc, api.Flows{Checkout: checkout, Repricing: &fakeRepricing{}})

	rec := doRequestAs(t, h, &adminWriter, http.MethodPut, "/admin/v1/carts/cart_1/billing-address",
		`{"first_name":"Ada","address_1":"Right St 12","country_code":"TR"}`)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "Right St 12", svc.addressInput.Address1)

	rec = doRequestAs(t, h, &adminWriter, http.MethodPost, "/admin/v1/carts/cart_1/complete", telephoneCompletion)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 1, checkout.calls)
}

// TestThePanelRefusesAShoppersCart: the panel's surface refuses each write on
// a cart a shopper opened, before its flow is asked.
func TestThePanelRefusesAShoppersCart(t *testing.T) {
	pricing := &fakePricing{lineID: "li_2"}
	shipping := &fakeShipping{options: twoOptions, methodID: "sm_2"}
	checkout := &fakeCheckout{response: json.RawMessage(offlineFlowAnswer)}
	carts := withLineItem()
	surface := surfaceOver(api.Flows{Pricing: pricing, Shipping: shipping, Checkout: checkout}, carts)

	refused := func(name string, err error) {
		t.Helper()

		require.Error(t, err, name)
		assert.True(t, errors.IsConflict(err), "%s: %v", name, err)
	}
	_, err := surface.AddLine(operator(), "cart_1", "sc_shop", "var_1", 1)
	refused("line", err)
	refused("address", surface.SetShippingAddress(operator(), "cart_1", map[string]string{api.AddressLine1: "Elsewhere 1"}))
	_, err = surface.AddShippingMethod(operator(), "cart_1", "so_desk")
	refused("shipping", err)
	_, _, err = surface.Complete(operator(), "cart_1", "sc_shop", "bank_transfer", 3600)
	refused("completion", err)

	assert.Zero(t, pricing.calls)
	assert.Zero(t, shipping.calls)
	assert.Zero(t, checkout.calls)
	assert.Equal(t, service.AddressInput{}, carts.addressInput)

	_, _, _, err = surface.ShippingOptions(operator(), "cart_1")
	assert.NoError(t, err, "reading the options changes nothing and is not refused")
}

// TestAMissingCartIsNotAShoppers: an admin write on a cart that does not exist
// answers 404 as before, not as a shopper's cart.
func TestAMissingCartIsNotAShoppers(t *testing.T) {
	svc := &fakeCarts{err: errors.NotFound("cart_not_found", "no such cart")}
	h := newServerWithFlows(t, svc, api.Flows{Repricing: &fakeRepricing{}})

	rec := doRequestAs(t, h, &adminWriter, http.MethodPut, "/admin/v1/carts/cart_x/shipping-address",
		`{"first_name":"Ada","address_1":"Right St 12","country_code":"TR"}`)

	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "cart_opened_by_shopper")
}
