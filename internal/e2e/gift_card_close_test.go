//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	"github.com/bdrtr/gobit/internal/modules/payment/giftcard"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	paymentmodels "github.com/bdrtr/gobit/internal/modules/payment/models"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
	"github.com/bdrtr/gobit/internal/workflows/giftcardsale"
)

// TestABoughtCardIsClosedAndNotReturned is ADR 0213 on the production wiring:
// the line of a bought card cannot be asked back, an operator closes the card,
// its code opens no payment, and on the two journals together the card is owed
// nothing.
func TestABoughtCardIsClosedAndNotReturned(t *testing.T) {
	ctx := t.Context()
	from := time.Now().UTC().Add(-time.Second)
	customerID, address := newCustomer(ctx, t)
	cardVariant := newGiftCardVariant(ctx, t, 5_000)
	cartID, totals := prepareCart(ctx, t, customerID, cardVariant, 1)
	bought, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID: cartID, LocationID: stockLocationID, PaymentProviderID: manual.ID,
		PaymentData: paymentBehavior(t, manual.OutcomeAuthorize), Email: address, ExpectedTotal: totals.Total,
	})
	require.NoError(t, err)
	code := issuedCardMail(t, bought.OrderID).Data[giftcardsale.DataCode]
	var cardID string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT id FROM payment_gift_cards WHERE reason = $1`, "sold on order "+bought.OrderID).Scan(&cardID))
	order, err := orderSvc.GetOrder(ctx, bought.OrderID)
	require.NoError(t, err)

	asked := storefrontRequest(t, http.MethodPost, "/store/v1/orders/"+bought.OrderID+"/returns",
		`{"reason":"changed my mind","lines":[{"order_line_item_id":"`+order.Items[0].ID+`","quantity":1}]}`)
	require.Equal(t, http.StatusConflict, asked.Code, asked.Body.String())
	assert.Contains(t, asked.Body.String(), ordersvc.CodeGiftCardLineFinal)

	closed := adminCartRequest(t, http.MethodPost, "/admin/v1/gift-cards/"+cardID+"/disable",
		`{"reason":"sold to the wrong address"}`)
	require.Equal(t, http.StatusOK, closed.Code, closed.Body.String())
	var body struct {
		Data struct {
			Balance    int64      `json:"balance"`
			DisabledAt *time.Time `json:"disabled_at"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(closed.Body.Bytes(), &body))
	assert.Zero(t, body.Data.Balance)
	assert.NotNil(t, body.Data.DisabledAt)

	mugVariant, _ := newStockedVariant(ctx, t, "Closed card spend product", map[string]int64{taxedCurrency: 2_000}, 5)
	guestCart, guestTotal := giftCart(t, mugVariant)
	status, answer := payWithCard(t, guestCart, code, guestTotal)
	assert.Equalf(t, http.StatusConflict, status, "a closed card opens nothing: %s", answer)
	assert.Contains(t, answer, giftcard.CodeDisabled)

	window := time.Now().UTC().Add(time.Minute)
	orders, err := orderSvc.Journal(ctx, ordersvc.JournalQuery{From: from, To: window})
	require.NoError(t, err)
	payments, err := paymentSvc.Journal(ctx, paymentsvc.JournalQuery{From: from, To: window})
	require.NoError(t, err)
	var owed int64
	for _, entry := range orders.Entries {
		if entry.OrderID != bought.OrderID {
			continue
		}
		for _, line := range entry.Lines {
			if string(line.Account) == string(paymentmodels.AccountGiftCard) {
				owed += line.Credit - line.Debit
			}
		}
	}
	var voidID string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT id FROM payment_gift_card_entries WHERE gift_card_id = $1 AND kind = 'void'`, cardID).Scan(&voidID))
	forfeits := 0
	for _, entry := range payments.Entries {
		if entry.ID != voidID {
			continue
		}
		assert.Equal(t, paymentmodels.JournalGiftCardForfeit, entry.Kind)
		forfeits++
		owed -= entry.Lines[0].Debit
		assert.Equal(t, paymentmodels.AccountGiftCardForfeited, entry.Lines[1].Account, "a sold card's price is kept")
	}
	assert.Equal(t, 1, forfeits)
	assert.Zero(t, owed, "the order's debt to the card's holder is closed by the forfeit")
}
