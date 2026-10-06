package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/cart/api"
)

// TestTheCartsListingPublishesAnOptionsDeliveryDays is ADR 0421: both doors
// publish the business days the flow answered for an option, and none for an
// option it answered without them.
func TestTheCartsListingPublishesAnOptionsDeliveryDays(t *testing.T) {
	answer := json.RawMessage(`{"options":[` +
		`{"id":"so_std","name":"Standard","amount":2500,"currency_code":"TRY","delivery_days":{"min":3,"max":5}},` +
		`{"id":"so_pick","name":"Pick up","amount":0,"currency_code":"TRY"}]}`)
	for _, path := range []string{"/store/v1/carts/cart_1/shipping-options", "/admin/v1/carts/cart_1/shipping-options"} {
		t.Run(path, func(t *testing.T) {
			h := newServerWithFlows(t, &fakeCarts{}, api.Flows{Shipping: &fakeShipping{options: answer}})

			rec := doRequest(t, h, http.MethodGet, path, "")

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var body struct {
				Data []map[string]json.RawMessage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			require.Len(t, body.Data, 2)
			assert.JSONEq(t, `{"min":3,"max":5}`, string(body.Data[0]["delivery_days"]))
			assert.NotContains(t, body.Data[1], "delivery_days", "an option that says none")
		})
	}
}
