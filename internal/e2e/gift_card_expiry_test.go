//go:build integration

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/payment/giftcard"
)

// TestAnExpiredCardIsRefusedAtTheStorefront is ADR 0214 on the production
// wiring: an operator issues a card with a moment, the admin read shows it,
// and once the moment has passed the storefront refuses the code before an
// order is opened.
func TestAnExpiredCardIsRefusedAtTheStorefront(t *testing.T) {
	ctx := t.Context()
	moment := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	issued := adminCartRequest(t, http.MethodPost, "/admin/v1/gift-cards",
		fmt.Sprintf(`{"currency_code":%q,"amount":50000,"reason":"expiry","expires_at":%q}`,
			taxedCurrency, moment.Format(time.RFC3339)))
	require.Equal(t, http.StatusCreated, issued.Code, issued.Body.String())
	var body struct {
		Data struct {
			ID        string     `json:"id"`
			Code      string     `json:"code"`
			ExpiresAt *time.Time `json:"expires_at"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(issued.Body.Bytes(), &body))
	require.NotNil(t, body.Data.ExpiresAt)
	assert.True(t, moment.Equal(*body.Data.ExpiresAt))

	// The moment is moved behind, as an hour passing would leave it.
	_, err := testPool.Pool().Exec(ctx,
		`UPDATE payment_gift_cards SET expires_at = created_at + interval '1 microsecond' WHERE id = $1`,
		body.Data.ID)
	require.NoError(t, err)

	mugVariant, _ := newStockedVariant(ctx, t, "Expired card spend product", map[string]int64{taxedCurrency: 2_000}, 5)
	cartID, total := giftCart(t, mugVariant)
	status, answer := payWithCard(t, cartID, body.Data.Code, total)

	assert.Equalf(t, http.StatusConflict, status, "an expired card opens nothing: %s", answer)
	assert.Contains(t, answer, giftcard.CodeExpired)
	var orders int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM orders WHERE cart_id = $1`, cartID).Scan(&orders))
	assert.Zero(t, orders, "no order was opened")
}
