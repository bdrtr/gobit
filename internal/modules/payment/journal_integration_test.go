//go:build integration

package payment_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/payment/loyaltypoints"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
	"github.com/bdrtr/gobit/internal/modules/payment/storecredit"
)

// pay runs one collection for the customer through a provider, and returns the
// collection and its capture.
func pay(
	ctx context.Context, t *testing.T, svc *service.Service, customerID, providerID string, amount int64,
) (models.PaymentCollection, models.Payment) {
	t.Helper()

	collection, err := svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference: "cart_journal", Amount: amount, CurrencyCode: testCurrency, CustomerID: customerID,
	})
	require.NoError(t, err)
	session, err := svc.CreateSession(ctx, collection.ID, providerID, service.CreateSessionInput{
		IdempotencyKey: "journal-" + collection.ID,
	})
	require.NoError(t, err)
	_, err = svc.AuthorizePayment(ctx, session.ID)
	require.NoError(t, err)
	payment, err := svc.CapturePayment(ctx, session.ID, 0)
	require.NoError(t, err)

	return collection, payment
}

// TestTheJournalAgreesWithTheLedgersItIsReadFrom is ADR 0186 on the real rows.
//
// One customer is given store credit, pays by card (which earns points), is
// refunded part of it (which reverses points), and pays with store credit and
// with points. The journal over the window must balance entry by entry and in
// its trial balance, and the store credit and loyalty accounts it derives for
// the customer must equal the two ledgers' own sums — the ledgers count holds
// and releases the journal leaves out, and with no session pending the two
// ways of counting have to arrive at the same number.
//
// The database is shared by the package's tests, so the journal is read over a
// window that starts now and then narrowed to this customer and these
// collections.
func TestTheJournalAgreesWithTheLedgersItIsReadFrom(t *testing.T) {
	ctx := context.Background()
	svc := balanceTenderService(t, "")
	customer := "cus_journal_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	from := time.Now().UTC().Add(-time.Second)

	_, err := svc.IssueCredit(ctx, service.IssueCreditInput{
		CustomerID: customer, CurrencyCode: testCurrency, Amount: 6_000, Reason: "journal test",
	})
	require.NoError(t, err)
	card, cardPayment := pay(ctx, t, svc, customer, manual.ID, 10_000)
	_, err = svc.RefundPayment(ctx, cardPayment.ID, 2_000, "journal test")
	require.NoError(t, err)
	credit, _ := pay(ctx, t, svc, customer, storecredit.ID, 4_000)
	points, _ := pay(ctx, t, svc, customer, loyaltypoints.ID, 1_500)

	journal, err := svc.Journal(ctx, service.JournalQuery{
		From: from, To: time.Now().UTC().Add(time.Minute), CurrencyCode: testCurrency,
	})
	require.NoError(t, err)

	ours := map[string]bool{card.ID: true, credit.ID: true, points.ID: true}
	accounts := map[models.JournalAccount]int64{}
	var clearing int64
	kinds := map[models.JournalKind]int{}
	for _, entry := range journal.Entries {
		var debit, credit int64
		for _, line := range entry.Lines {
			debit += line.Debit
			credit += line.Credit
		}
		require.Equal(t, debit, credit, "%s %s does not balance", entry.Kind, entry.ID)

		for _, line := range entry.Lines {
			if line.CustomerID == customer {
				accounts[line.Account] += line.Credit - line.Debit
			}
			if ours[entry.CollectionID] && line.Account == models.AccountProviderClearing {
				clearing += line.Debit - line.Credit
			}
		}
		if ours[entry.CollectionID] {
			kinds[entry.Kind]++
		}
	}
	assert.Equal(t, map[models.JournalKind]int{models.JournalCapture: 3, models.JournalRefund: 1}, kinds)

	creditBalance, err := svc.StoreCreditBalance(ctx, customer, testCurrency)
	require.NoError(t, err)
	pointBalance, err := svc.LoyaltyBalance(ctx, customer, testCurrency)
	require.NoError(t, err)
	assert.Equal(t, creditBalance, accounts[models.AccountStoreCredit],
		"the store credit the journal says the shop owes is the ledger's balance")
	assert.Equal(t, pointBalance, accounts[models.AccountLoyalty],
		"the points the journal says the shop owes are the ledger's balance")
	assert.Equal(t, int64(2_000), creditBalance, "the fixture spent some credit and left some")
	assert.Positive(t, pointBalance, "the fixture earned more points than it spent")
	assert.Equal(t, int64(8_000), clearing, "the card took 10,000 and gave 2,000 back")

	var debits, credits int64
	for _, balance := range journal.Balances {
		debits += balance.Debit
		credits += balance.Credit
	}
	assert.Equal(t, debits, credits, "the trial balance balances")
}
