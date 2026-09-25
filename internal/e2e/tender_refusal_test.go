//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/payment/loyaltypoints"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
	"github.com/bdrtr/gobit/internal/modules/payment/storecredit"
)

// TestAPaymentTheCartCannotMakeOpensNoOrder is the gate ADR 0175 rests on.
//
// Some payments are refused whatever happens later: a guest cannot spend a
// balance that belongs to a person, and nobody can pay with a provider the
// installation never registered. The completion used to find out only at the
// payment step, the third of the saga, after the order was opened and
// `order.placed` published — so a shop with a mail provider told the shopper
// their order was placed a moment before canceling it (D130).
//
// The refusal is asserted, and so is its absence of effect: a status code alone
// cannot tell a refusal before the order from a refusal after it, and only the
// second one opened an order.
func TestAPaymentTheCartCannotMakeOpensNoOrder(t *testing.T) {
	ctx := t.Context()

	variantID, _ := newStockedVariant(ctx, t, "Tender refusal product",
		map[string]int64{taxedCurrency: 10_000}, 5)

	for name, tc := range map[string]struct {
		provider string
		status   int
		code     string
	}{
		"a guest spending loyalty points": {loyaltypoints.ID, http.StatusConflict, loyaltypoints.CodeNoCustomer},
		"a guest spending store credit":   {storecredit.ID, http.StatusConflict, storecredit.CodeNoCustomer},
		"a provider nobody registered": {"no_such_provider", http.StatusNotFound,
			paymentsvc.CodeProviderNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			cartID := openCartWithKey(t, publishableKey)
			added := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/line-items",
				fmt.Sprintf(`{"variant_id":%q,"quantity":1}`, variantID))
			require.Equal(t, http.StatusCreated, added.Code, "body: %s", added.Body.String())

			read := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+cartID, "")
			require.Equal(t, http.StatusOK, read.Code, "body: %s", read.Body.String())
			total, ok := storefrontData(t, read)["total"].(float64)
			require.True(t, ok, "the cart carries no total; body: %s", read.Body.String())

			rec := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
				fmt.Sprintf(`{"payment_provider_id":%q,"expected_total":%d}`, tc.provider, int64(total)))

			assert.Equalf(t, tc.status, rec.Code, "body: %s", rec.Body.String())
			var envelope struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "body: %s", rec.Body.String())
			assert.Equal(t, tc.code, envelope.Error.Code,
				"the refusal has to name why, so a storefront can tell the shopper to choose again")

			var orders int
			require.NoError(t, testPool.Pool().QueryRow(ctx,
				`SELECT count(*) FROM orders WHERE cart_id = $1`, cartID).Scan(&orders))
			assert.Zerof(t, orders,
				"%d order(s) were opened for a payment that could never be made. Opening one "+
					"publishes order.placed, and a shop with a mail provider tells the shopper "+
					"their order is placed before the saga cancels it (D130)", orders)
		})
	}
}
