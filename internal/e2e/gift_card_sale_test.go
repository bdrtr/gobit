//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreprovider "github.com/bdrtr/gobit/core/provider"
	inventorysvc "github.com/bdrtr/gobit/internal/modules/inventory/service"
	"github.com/bdrtr/gobit/internal/modules/payment/giftcard"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
	"github.com/bdrtr/gobit/internal/workflows/giftcardsale"
)

// newGiftCardVariant is a gift card product's variant, priced and stocked.
func newGiftCardVariant(ctx context.Context, t *testing.T, price int64) string {
	t.Helper()

	seq := fixtureCounter.Add(1)
	product, err := productSvc.CreateProduct(ctx, productsvc.CreateProductInput{
		Handle: fmt.Sprintf("e2e-gift-card-%d", seq), Title: "E2E Gift Card",
		Status: productmodels.StatusPublished, IsGiftcard: true,
	})
	require.NoError(t, err)
	variant, err := productSvc.CreateVariant(ctx, product.ID, productsvc.CreateVariantInput{Title: "A card"})
	require.NoError(t, err)
	set, err := pricingSvc.CreatePriceSet(ctx, []pricingsvc.PriceInput{
		{CurrencyCode: taxedCurrency, Amount: price, MinQuantity: 1},
	})
	require.NoError(t, err)
	require.NoError(t, productSvc.SetVariantPriceSet(ctx, variant.ID, set.ID))
	item, err := inventorySvc.CreateInventoryItem(ctx, inventorysvc.CreateInventoryItemInput{
		SKU: fmt.Sprintf("E2E-GIFT-%d", seq), Title: "E2E Gift Card",
	})
	require.NoError(t, err)
	require.NoError(t, productSvc.SetVariantInventoryItem(ctx, variant.ID, item.ID))
	_, err = inventorySvc.SetInventoryLevel(ctx, item.ID, stockLocationID, 10)
	require.NoError(t, err)

	return variant.ID
}

// issuedCardMail waits for the gift card mail of an order.
func issuedCardMail(t *testing.T, orderID string) coreprovider.Notification {
	t.Helper()

	var mail coreprovider.Notification
	require.Eventually(t, func() bool {
		for _, sent := range notificationSpy.notificationsFor(orderID) {
			if sent.Template == giftcardsale.TemplateIssued {
				mail = sent
				return true
			}
		}
		return false
	}, 10*time.Second, 20*time.Millisecond, "the card's code was never mailed")

	return mail
}

// TestABoughtGiftCardIsMailedAndSpent is ADR 0210 on the production wiring: a
// customer buys a 5,000 TRY card, the capture issues it and mails its code to
// the order's address, the purchase earns points, spending the card earns none,
// and a replaced code stops the old one.
func TestABoughtGiftCardIsMailedAndSpent(t *testing.T) {
	ctx := t.Context()
	customerID, address := newCustomer(ctx, t)
	cardVariant := newGiftCardVariant(ctx, t, 5_000)
	cartID, totals := prepareCart(ctx, t, customerID, cardVariant, 1)

	bought, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID: cartID, LocationID: stockLocationID, PaymentProviderID: manual.ID,
		PaymentData: paymentBehavior(t, manual.OutcomeAuthorize), Email: address, ExpectedTotal: totals.Total,
	})
	require.NoError(t, err)

	mail := issuedCardMail(t, bought.OrderID)
	assert.Equal(t, address, mail.To, "the code goes to the order's address")
	assert.Equal(t, "5000", mail.Data[giftcardsale.DataAmount], "the card is worth the unit price")
	code := mail.Data[giftcardsale.DataCode]
	require.NotEmpty(t, code)
	var cardID string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT id FROM payment_gift_cards WHERE reason = $1`, "sold on order "+bought.OrderID).Scan(&cardID))
	assert.Equal(t, int64(5_000), giftCardBalance(t, cardID))
	pointsAfterPurchase, err := paymentSvc.LoyaltyBalance(ctx, customerID, taxedCurrency)
	require.NoError(t, err)
	assert.Positive(t, pointsAfterPurchase, "buying the card earned points")

	mugVariant, _ := newStockedVariant(ctx, t, "Gift card spend product", map[string]int64{taxedCurrency: 2_000}, 5)
	spendCart, spendTotals := prepareCart(ctx, t, customerID, mugVariant, 1)
	cardData, err := json.Marshal(map[string]string{giftcard.DataCode: code})
	require.NoError(t, err)
	_, err = orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID: spendCart, LocationID: stockLocationID, PaymentProviderID: giftcard.ID,
		PaymentData: cardData, Email: address, ExpectedTotal: spendTotals.Total,
	})
	require.NoError(t, err)
	assert.Equal(t, 5_000-spendTotals.Total, giftCardBalance(t, cardID))
	pointsAfterSpend, err := paymentSvc.LoyaltyBalance(ctx, customerID, taxedCurrency)
	require.NoError(t, err)
	assert.Equal(t, pointsAfterPurchase, pointsAfterSpend, "spending the card earned nothing")

	replaced := adminCartRequest(t, http.MethodPost, "/admin/v1/gift-cards/"+cardID+"/code", "")
	require.Equal(t, http.StatusOK, replaced.Code, replaced.Body.String())
	var body struct {
		Data struct {
			Code    string `json:"code"`
			Balance int64  `json:"balance"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(replaced.Body.Bytes(), &body))
	assert.Equal(t, 5_000-spendTotals.Total, body.Data.Balance, "the balance stays with the card")
	guestCart, guestTotal := giftCart(t, mugVariant)
	status, answer := payWithCard(t, guestCart, code, guestTotal)
	assert.Equalf(t, http.StatusUnprocessableEntity, status, "the old code opens nothing: %s", answer)
}

// TestAnOrderWithoutAGiftCardIssuesNone: the flow runs on every capture and
// leaves an ordinary order alone.
func TestAnOrderWithoutAGiftCardIssuesNone(t *testing.T) {
	ctx := t.Context()
	customerID, address := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "Ordinary product", map[string]int64{taxedCurrency: 2_000}, 5)
	cartID, totals := prepareCart(ctx, t, customerID, variantID, 1)

	placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID: cartID, LocationID: stockLocationID, PaymentProviderID: manual.ID,
		PaymentData: paymentBehavior(t, manual.OutcomeAuthorize), Email: address, ExpectedTotal: totals.Total,
	})
	require.NoError(t, err)

	require.Eventually(t, func() bool { return len(notificationSpy.notificationsFor(placed.OrderID)) > 0 },
		10*time.Second, 20*time.Millisecond, "the order's own confirmation is the sign the bus ran")
	cardMailed := func() bool {
		for _, sent := range notificationSpy.notificationsFor(placed.OrderID) {
			if sent.Template == giftcardsale.TemplateIssued {
				return true
			}
		}
		return false
	}
	assert.Never(t, cardMailed, 500*time.Millisecond, 20*time.Millisecond, "no card was mailed")
	var cards int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM payment_gift_cards WHERE reason = $1`, "sold on order "+placed.OrderID).Scan(&cards))
	assert.Zero(t, cards)
}
