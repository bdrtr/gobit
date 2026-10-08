//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/payment/loyaltypoints"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	paymentmodels "github.com/bdrtr/gobit/internal/modules/payment/models"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
	"github.com/bdrtr/gobit/internal/modules/payment/storecredit"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// completeFirstWith completes the cart through the storefront with the given
// balances paying first and the manual provider the rest.
func completeFirstWith(t *testing.T, cartID string, total int64, balances ...string) (status int, body string) {
	t.Helper()

	named := ""
	for i, balance := range balances {
		if i > 0 {
			named += ","
		}
		named += fmt.Sprintf("%q", balance)
	}
	rec := storefrontRequest(t, http.MethodPost, "/store/v1/carts/"+cartID+"/complete",
		fmt.Sprintf(`{"payment_provider_id":%q,"pay_first_with":[%s],"expected_total":%d}`, manual.ID, named, total))

	return rec.Code, rec.Body.String()
}

// TestPointsAndCreditPayPartOfAnOrder is ADR 0269 on the production wiring:
// through the storefront, the customer's points hold what they have, the
// credit what it has of the rest, the manual provider pays what is left, and a
// whole refund gives both balances back.
func TestPointsAndCreditPayPartOfAnOrder(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Balances First Product",
		map[string]int64{taxedCurrency: pointsUnitPrice}, pointsInitialStock)

	// The points come from a paid order, the credit from an operator.
	earnCart, _ := prepareCart(ctx, t, customerID, variantID, pointsQuantity)
	_, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            earnCart,
		LocationID:        stockLocationID,
		PaymentProviderID: manual.ID,
		PaymentData:       paymentBehavior(t, manual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     pointsTotal,
	})
	require.NoError(t, err)
	points, err := paymentSvc.LoyaltyBalance(ctx, customerID, taxedCurrency)
	require.NoError(t, err)
	require.Positive(t, points)
	_, err = paymentSvc.IssueCredit(ctx, paymentsvc.IssueCreditInput{
		CustomerID: customerID, CurrencyCode: taxedCurrency, Amount: creditNotEnough, Reason: "an end-to-end test",
	})
	require.NoError(t, err)

	cartID, totals := prepareCart(ctx, t, customerID, variantID, 2*pointsQuantity)
	require.Greater(t, totals.Total, points+creditNotEnough, "the provider has something left to pay")

	status, body := completeFirstWith(t, cartID, totals.Total, loyaltypoints.ID, storecredit.ID)

	require.Truef(t, status == http.StatusOK || status == http.StatusCreated, "status %d, body: %s", status, body)
	assert.Equal(t, map[string]int64{
		loyaltypoints.ID: points,
		storecredit.ID:   creditNotEnough,
		manual.ID:        totals.Total - points - creditNotEnough,
	}, captures(t, cartID), "each balance holds what it has, in the order named")
	credit, err := paymentSvc.StoreCreditBalance(ctx, customerID, taxedCurrency)
	require.NoError(t, err)
	assert.Zero(t, credit)

	var collectionID string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT id FROM payment_collections WHERE reference = $1`, cartID).Scan(&collectionID))
	_, err = paymentSvc.RefundCollection(ctx, collectionID, 0, paymentmodels.MaxAmount,
		"an end-to-end test", "e2e_balance_"+collectionID)
	require.NoError(t, err)
	credit, err = paymentSvc.StoreCreditBalance(ctx, customerID, taxedCurrency)
	require.NoError(t, err)
	assert.Equal(t, creditNotEnough, credit, "a whole refund puts the credit back")
}

// TestABalanceNamedFirstThatHoldsNothingStopsThePayment: the customer named
// their credit and has none, so the provider is never asked and the stock goes
// back, as for a declined card.
func TestABalanceNamedFirstThatHoldsNothingStopsThePayment(t *testing.T) {
	ctx := t.Context()

	customerID, _ := newCustomer(ctx, t)
	variantID, inventoryItemID := newStockedVariant(ctx, t, "E2E Empty Balance First Product",
		map[string]int64{taxedCurrency: creditUnitPrice}, creditInitialStock)
	cartID, totals := prepareCart(ctx, t, customerID, variantID, creditQuantity)

	status, body := completeFirstWith(t, cartID, totals.Total, storecredit.ID)

	require.Equal(t, http.StatusConflict, status, body)
	assert.Contains(t, body, paymentsvc.CodeAuthorizationDeclined)
	assert.Empty(t, captures(t, cartID))
	var providers []string
	rows, err := testPool.Pool().Query(ctx, `
        SELECT s.provider_id FROM payment_sessions s
        JOIN payment_collections c ON c.id = s.payment_collection_id
        WHERE c.reference = $1`, cartID)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var provider string
		require.NoError(t, rows.Scan(&provider))
		providers = append(providers, provider)
	}
	require.NoError(t, rows.Err())
	assert.Equal(t, []string{storecredit.ID}, providers, "the provider's session is never opened")
	assert.Equal(t, creditInitialStock, sellableQuantity(ctx, t, inventoryItemID))
}

// TestAGuestNamingABalanceFirstIsRefusedBeforeTheOrder: a guest cart has no
// balance, and the refusal is the tender's own conflict, before anything is
// opened (ADR 0175).
func TestAGuestNamingABalanceFirstIsRefusedBeforeTheOrder(t *testing.T) {
	ctx := t.Context()

	variantID, _ := newStockedVariant(ctx, t, "E2E Guest Balance First Product",
		map[string]int64{taxedCurrency: pointsUnitPrice}, pointsInitialStock)
	cartID, total := giftCart(t, variantID)

	status, body := completeFirstWith(t, cartID, total, storecredit.ID)

	require.Equal(t, http.StatusConflict, status, body)
	assert.Contains(t, body, storecredit.CodeNoCustomer)
	var orders int
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM orders WHERE cart_id = $1`, cartID).Scan(&orders))
	assert.Zero(t, orders, "no order was placed")
}
