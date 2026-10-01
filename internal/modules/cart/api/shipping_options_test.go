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

// The options a cart can take (ADR 0292).

// twoOptions is the flow's answer: a free option a subtotal rule opened, and a
// standard one.
var twoOptions = json.RawMessage(`{"options":[` +
	`{"id":"so_free","name":"Free over 500","amount":0,"currency_code":"TRY"},` +
	`{"id":"so_std","name":"Standard","amount":2500,"currency_code":"TRY"}]}`)

// TestBothDoorsListTheCartsOptions: the storefront's read and the admin's
// return the flow's options for the cart in the path, all on one page of the
// list envelope.
func TestBothDoorsListTheCartsOptions(t *testing.T) {
	for _, path := range []string{"/store/v1/carts/cart_1/shipping-options", "/admin/v1/carts/cart_1/shipping-options"} {
		t.Run(path, func(t *testing.T) {
			shipping := &fakeShipping{options: twoOptions}
			h := newServerWithFlows(t, &fakeCarts{}, api.Flows{Shipping: shipping})

			rec := doRequest(t, h, http.MethodGet, path, "")

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, "cart_1", shipping.gotCartID)
			var body struct {
				Data  []map[string]any `json:"data"`
				Count int64            `json:"count"`
				Limit int64            `json:"limit"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			require.Len(t, body.Data, 2)
			assert.Equal(t, "so_free", body.Data[0]["id"])
			assert.Equal(t, "Free over 500", body.Data[0]["name"])
			assert.InDelta(t, 2500, body.Data[1]["amount"], 0)
			assert.Equal(t, "TRY", body.Data[1]["currency_code"])
			assert.Equal(t, int64(2), body.Count)
			assert.Equal(t, int64(2), body.Limit)
		})
	}
}

// TestACartNoOptionServesAnswersAnEmptyList: "nothing ships this cart" is a
// list with nothing in it, not a missing one.
func TestACartNoOptionServesAnswersAnEmptyList(t *testing.T) {
	h := newServerWithFlows(t, &fakeCarts{}, api.Flows{Shipping: &fakeShipping{options: json.RawMessage(`{}`)}})

	rec := doRequest(t, h, http.MethodGet, "/store/v1/carts/cart_1/shipping-options", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"data":[],"count":0,"offset":0,"limit":0}`, rec.Body.String())
}

// TestTheListingWithoutItsFlowFails: an installation whose shipping flow is
// not bound answers an error rather than an empty list.
func TestTheListingWithoutItsFlowFails(t *testing.T) {
	h := newServerWithFlows(t, &fakeCarts{}, api.Flows{})

	rec := doRequest(t, h, http.MethodGet, "/store/v1/carts/cart_1/shipping-options", "")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestTheAdminListingAsksForTheCartsRead: the admin's door is a read.
func TestTheAdminListingAsksForTheCartsRead(t *testing.T) {
	h := newServerWithFlows(t, &fakeCarts{}, api.Flows{Shipping: &fakeShipping{options: twoOptions}})
	writer := corehttp.Principal{ID: "usr_1", Kind: "user", Scopes: []string{api.ScopeWrite}}

	rec := doRequestAs(t, h, &writer, http.MethodGet, "/admin/v1/carts/cart_1/shipping-options", "")

	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
}

// TestThePanelListsTheCartsOptions: the panel's surface reads the same
// listing, each option's id, name and amount in order.
func TestThePanelListsTheCartsOptions(t *testing.T) {
	surface := surfaceOver(api.Flows{Shipping: &fakeShipping{options: twoOptions}}, &fakeCarts{})

	ids, names, amounts, err := surface.ShippingOptions(operator(), "cart_1")

	require.NoError(t, err)
	assert.Equal(t, []string{"so_free", "so_std"}, ids)
	assert.Equal(t, []string{"Free over 500", "Standard"}, names)
	assert.Equal(t, []int64{0, 2500}, amounts)
}
