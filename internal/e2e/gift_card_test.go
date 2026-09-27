//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/payment/giftcard"
)

// issueGiftCard issues a card through the admin API and returns its id and
// code.
func issueGiftCard(t *testing.T, currency string, amount int64) (id, code string) {
	t.Helper()

	rec := adminCartRequest(t, http.MethodPost, "/admin/v1/gift-cards",
		fmt.Sprintf(`{"currency_code":%q,"amount":%d,"reason":"an end-to-end test"}`, currency, amount))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var body struct {
		Data struct {
			ID   string `json:"id"`
			Code string `json:"code"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotEmpty(t, body.Data.Code)

	return body.Data.ID, body.Data.Code
}

// giftCardBalance reads a card's balance through the admin API.
func giftCardBalance(t *testing.T, id string) int64 {
	t.Helper()

	rec := adminCartRequest(t, http.MethodGet, "/admin/v1/gift-cards/"+id, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Data struct {
			Balance int64 `json:"balance"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	return body.Data.Balance
}

// giftCart opens a guest cart holding one line and returns it with its total.
func giftCart(t *testing.T, variantID string) (cartID string, total int64) {
	t.Helper()

	cartID = openCartWithKey(t, publishableKey)
	added := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/line-items",
		fmt.Sprintf(`{"variant_id":%q,"quantity":1}`, variantID))
	require.Equal(t, http.StatusCreated, added.Code, added.Body.String())
	read := storefrontRequest(t, http.MethodGet, "/store/v1/carts/"+cartID, "")
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	amount, ok := storefrontData(t, read)["total"].(float64)
	require.True(t, ok, read.Body.String())

	return cartID, int64(amount)
}

// payWithCard completes the cart with the gift card provider and the code.
func payWithCard(t *testing.T, cartID, code string, total int64) (status int, body string) {
	t.Helper()

	rec := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		fmt.Sprintf(`{"payment_provider_id":%q,"payment_data":{%q:%q},"expected_total":%d}`,
			giftcard.ID, giftcard.DataCode, code, total))

	return rec.Code, rec.Body.String()
}

// TestAGuestPaysWithAGiftCard is ADR 0208 on the production wiring: a card
// issued through the admin API, a guest's cart paid with its code, the card's
// balance taken down by the order's total, and the code kept nowhere.
func TestAGuestPaysWithAGiftCard(t *testing.T) {
	ctx := t.Context()
	variantID, _ := newStockedVariant(ctx, t, "Gift card product", map[string]int64{taxedCurrency: 10_000}, 5)
	cardID, code := issueGiftCard(t, taxedCurrency, 50_000)
	cartID, total := giftCart(t, variantID)
	require.Less(t, total, int64(50_000))

	status, body := payWithCard(t, cartID, code, total)

	require.Truef(t, status == http.StatusOK || status == http.StatusCreated, "status %d, body: %s", status, body)
	var orders int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM orders WHERE cart_id = $1`, cartID).Scan(&orders))
	assert.Equal(t, 1, orders)
	assert.Equal(t, 50_000-total, giftCardBalance(t, cardID), "the order's total came off the card")

	var kept int
	require.NoError(t, testPool.Pool().QueryRow(ctx, `
        SELECT (SELECT count(*) FROM payment_sessions WHERE data::text LIKE '%' || $1 || '%')
             + (SELECT count(*) FROM workflow_executions
                WHERE input::text LIKE '%' || $1 || '%' OR output::text LIKE '%' || $1 || '%')
             + (SELECT count(*) FROM workflow_execution_steps WHERE output::text LIKE '%' || $1 || '%')`,
		code).Scan(&kept))
	assert.Zero(t, kept, "the code is a bearer credential and no record of the payment keeps it")
}

// TestAGiftCardThatCannotPayOpensNoOrder: a code that opens no card, and a card
// in another currency, are refused before the order is opened (ADR 0175).
func TestAGiftCardThatCannotPayOpensNoOrder(t *testing.T) {
	ctx := t.Context()
	variantID, _ := newStockedVariant(ctx, t, "Gift card refusal product", map[string]int64{taxedCurrency: 10_000}, 5)
	_, euroCode := issueGiftCard(t, untaxedCurrency, 50_000)

	for name, tc := range map[string]struct {
		code   string
		status int
		error  string
	}{
		"a code nobody was given": {"ZZZZ-ZZZZ-ZZZZ-ZZZZ", http.StatusUnprocessableEntity, giftcard.CodeUnknown},
		"a card in euros":         {euroCode, http.StatusConflict, giftcard.CodeCurrency},
	} {
		t.Run(name, func(t *testing.T) {
			cartID, total := giftCart(t, variantID)

			status, body := payWithCard(t, cartID, tc.code, total)

			assert.Equalf(t, tc.status, status, "body: %s", body)
			var envelope struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal([]byte(body), &envelope), body)
			assert.Equal(t, tc.error, envelope.Error.Code)
			var orders int
			require.NoError(t, testPool.Pool().QueryRow(ctx,
				`SELECT count(*) FROM orders WHERE cart_id = $1`, cartID).Scan(&orders))
			assert.Zero(t, orders, "a payment that could never be made opened an order")
		})
	}
}
