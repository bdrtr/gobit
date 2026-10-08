//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	paymentmodels "github.com/bdrtr/gobit/internal/modules/payment/models"
)

// payWithCardAndManual completes the cart with the card first and the manual
// provider for the rest.
func payWithCardAndManual(t *testing.T, cartID, code string, total int64) (status int, body string) {
	t.Helper()

	rec := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		fmt.Sprintf(`{"payment_provider_id":"manual","gift_card_code":%q,"expected_total":%d}`, code, total))

	return rec.Code, rec.Body.String()
}

// captures returns what each provider captured on the cart's collection.
func captures(t *testing.T, cartID string) map[string]int64 {
	t.Helper()

	rows, err := testPool.Pool().Query(t.Context(), `
        SELECT s.provider_id, p.amount
        FROM payments p
        JOIN payment_sessions s ON s.id = p.payment_session_id
        JOIN payment_collections c ON c.id = p.payment_collection_id
        WHERE c.reference = $1`, cartID)
	require.NoError(t, err)
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var provider string
		var amount int64
		require.NoError(t, rows.Scan(&provider, &amount))
		out[provider] += amount
	}
	require.NoError(t, rows.Err())

	return out
}

// TestACardAndAProviderPayOneOrder is ADR 0209 on the production wiring: the
// card pays what it holds, the manual provider the rest, and a refund of the
// order draws the card's capture first, as the newer of the two.
func TestACardAndAProviderPayOneOrder(t *testing.T) {
	ctx := t.Context()
	variantID, _ := newStockedVariant(ctx, t, "Split payment product", map[string]int64{taxedCurrency: 10_000}, 5)
	cardID, code := issueGiftCard(t, taxedCurrency, 3_000)
	cartID, total := giftCart(t, variantID)
	require.Greater(t, total, int64(3_000))

	status, body := payWithCardAndManual(t, cartID, code, total)

	require.Truef(t, status == http.StatusOK || status == http.StatusCreated, "status %d, body: %s", status, body)
	assert.Equal(t, map[string]int64{"gift_card": 3_000, "manual": total - 3_000}, captures(t, cartID))
	assert.Zero(t, giftCardBalance(t, cardID))

	var collectionID string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT id FROM payment_collections WHERE reference = $1`, cartID).Scan(&collectionID))
	// The refund names a cause the order does not know, held to no figure:
	// what is tested is how a collection's refund draws its captures.
	_, err := paymentSvc.RefundCollection(ctx, collectionID, 1_000, paymentmodels.MaxAmount,
		"an end-to-end test", "e2e_split_"+collectionID)
	require.NoError(t, err)
	assert.Equal(t, int64(1_000), giftCardBalance(t, cardID), "the card's capture is the newer and is drawn first")
	_, err = paymentSvc.RefundCollection(ctx, collectionID, 0, paymentmodels.MaxAmount,
		"an end-to-end test", "e2e_split_"+collectionID)
	require.NoError(t, err)
	assert.Equal(t, int64(3_000), giftCardBalance(t, cardID), "a whole refund puts the card back")

	var kept int
	require.NoError(t, testPool.Pool().QueryRow(ctx, `
        SELECT (SELECT count(*) FROM payment_sessions WHERE data::text LIKE '%' || $1 || '%')
             + (SELECT count(*) FROM workflow_executions
                WHERE input::text LIKE '%' || $1 || '%' OR output::text LIKE '%' || $1 || '%')
             + (SELECT count(*) FROM workflow_execution_steps WHERE output::text LIKE '%' || $1 || '%')`,
		code).Scan(&kept))
	assert.Zero(t, kept, "the code is kept in no record of the payment")
}

// TestACardThatCoversTheOrderLeavesTheProviderUnasked: the provider is named
// and takes nothing.
func TestACardThatCoversTheOrderLeavesTheProviderUnasked(t *testing.T) {
	ctx := t.Context()
	variantID, _ := newStockedVariant(ctx, t, "Card-covered product", map[string]int64{taxedCurrency: 10_000}, 5)
	cardID, code := issueGiftCard(t, taxedCurrency, 50_000)
	cartID, total := giftCart(t, variantID)

	status, body := payWithCardAndManual(t, cartID, code, total)

	require.Truef(t, status == http.StatusOK || status == http.StatusCreated, "status %d, body: %s", status, body)
	assert.Equal(t, map[string]int64{"gift_card": total}, captures(t, cartID))
	var sessions int
	require.NoError(t, testPool.Pool().QueryRow(ctx, `
        SELECT count(*) FROM payment_sessions s
        JOIN payment_collections c ON c.id = s.payment_collection_id
        WHERE c.reference = $1 AND s.provider_id = 'manual'`, cartID).Scan(&sessions))
	assert.Zero(t, sessions, "no session was opened at the provider")
	assert.Equal(t, 50_000-total, giftCardBalance(t, cardID))
}

// TestASplitWithACardThatCannotPayOpensNoOrder: the card beside a provider is
// checked before the order as a card alone is.
func TestASplitWithACardThatCannotPayOpensNoOrder(t *testing.T) {
	ctx := t.Context()
	variantID, _ := newStockedVariant(ctx, t, "Split refusal product", map[string]int64{taxedCurrency: 10_000}, 5)
	cartID, total := giftCart(t, variantID)

	status, body := payWithCardAndManual(t, cartID, "ZZZZ-ZZZZ-ZZZZ-ZZZZ", total)

	assert.Equalf(t, http.StatusUnprocessableEntity, status, "body: %s", body)
	assert.Contains(t, body, "payment_gift_card_unknown")
	var orders int
	require.NoError(t, testPool.Pool().QueryRow(ctx, `SELECT count(*) FROM orders WHERE cart_id = $1`, cartID).Scan(&orders))
	assert.Zero(t, orders)
}

// TestACardAloneThatDoesNotCoverTheOrderIsReleased: without a provider beside
// it, a card that holds less than the total is refused by the checkout's
// full-payment rule, and the card keeps its balance.
func TestACardAloneThatDoesNotCoverTheOrderIsReleased(t *testing.T) {
	ctx := t.Context()
	variantID, _ := newStockedVariant(ctx, t, "Short card product", map[string]int64{taxedCurrency: 10_000}, 5)
	cardID, code := issueGiftCard(t, taxedCurrency, 1_000)
	cartID, total := giftCart(t, variantID)

	status, body := payWithCard(t, cartID, code, total)

	assert.Equalf(t, http.StatusConflict, status, "body: %s", body)
	assert.Contains(t, body, "checkout_workflow_payment_underauthorized")
	assert.Equal(t, int64(1_000), giftCardBalance(t, cardID), "the card's hold was released")
}
