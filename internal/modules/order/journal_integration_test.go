//go:build integration

package order_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestTheOrderJournalReadsTheRealRecords is ADR 0188 on the real schema.
//
// One order is placed and written off in part, another is placed and canceled
// before anything was paid. On the order's side of the books, the first owes
// its total less the credit — the order's own "outstanding" before any money
// moved — and the second owes nothing. The database is shared by the package's
// tests, so the journal is read over a window that starts now and narrowed to
// these two orders.
func TestTheOrderJournalReadsTheRealRecords(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	from := time.Now().UTC().Add(-time.Second)

	credited, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	_, err = svc.CreateCreditLine(ctx, credited.ID, service.CreateCreditLineInput{
		Amount: 400, Reason: "journal test",
	})
	require.NoError(t, err)
	canceled, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	require.NoError(t, svc.CancelOrder(ctx, canceled.ID, "journal test"))

	journal, err := svc.Journal(ctx, service.JournalQuery{
		From: from, To: time.Now().UTC().Add(time.Minute), CurrencyCode: testCurrency,
	})
	require.NoError(t, err)

	receivable := map[string]int64{}
	kinds := map[string][]models.JournalKind{}
	for _, entry := range journal.Entries {
		if entry.OrderID != credited.ID && entry.OrderID != canceled.ID {
			continue
		}
		kinds[entry.OrderID] = append(kinds[entry.OrderID], entry.Kind)
		var debit, credit int64
		for _, line := range entry.Lines {
			debit += line.Debit
			credit += line.Credit
			if line.Account == models.AccountReceivable {
				receivable[entry.OrderID] += line.Debit - line.Credit
			}
		}
		require.Equal(t, debit, credit, "%s %s does not balance", entry.Kind, entry.ID)
	}

	assert.Equal(t, []models.JournalKind{models.JournalOrderPlaced, models.JournalCreditLine}, kinds[credited.ID])
	assert.Equal(t, []models.JournalKind{models.JournalOrderPlaced, models.JournalOrderCanceled}, kinds[canceled.ID])
	assert.Equal(t, credited.Total-400, receivable[credited.ID], "the total less what was written off")
	assert.Zero(t, receivable[canceled.ID], "a canceled order owes nothing")
}

// TestAnExchangesDifferenceIsOnTheRealBooks is ADR 0203 on the real schema:
// a funded exchange is an entry at its funding, and the exchange is a cause a
// refund can name.
func TestAnExchangesDifferenceIsOnTheRealBooks(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	from := time.Now().UTC().Add(-time.Second)

	placed, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	exchange, err := svc.CreateExchange(ctx, service.CreateExchangeInput{OrderID: placed.ID, DifferenceDue: 700})
	require.NoError(t, err)
	_, err = svc.FundExchange(ctx, exchange.ID, "pay_col_books_"+exchange.ID, exchange.DifferenceDue)
	require.NoError(t, err)

	journal, err := svc.Journal(ctx, service.JournalQuery{
		From: from, To: time.Now().UTC().Add(time.Minute), CurrencyCode: testCurrency,
	})
	require.NoError(t, err)
	var funded []models.JournalEntry
	for _, entry := range journal.Entries {
		if entry.OrderID == placed.ID && entry.Kind == models.JournalExchangeFunded {
			funded = append(funded, entry)
		}
	}
	require.Len(t, funded, 1)
	assert.Equal(t, exchange.ID, funded[0].ID)
	assert.Equal(t, []models.JournalLine{
		{Account: models.AccountReceivable, Debit: 700},
		{Account: models.AccountSales, Credit: 700},
	}, funded[0].Lines)

	causes, err := repository.New(testPool.Pool()).JournalCauses(ctx, []string{exchange.ID})
	require.NoError(t, err)
	require.Len(t, causes, 1)
	assert.Equal(t, "exchange", causes[0].Kind)
	assert.Equal(t, placed.ID, causes[0].OrderID)
}

// TestASoldGiftCardIsADebtOnTheRealBooks is ADR 0211 on the real schema: the
// line keeps its flag, and the journal's placement and cancellation read the
// gift card lines' subtotal out of the lines.
func TestASoldGiftCardIsADebtOnTheRealBooks(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	from := time.Now().UTC().Add(-time.Second)

	input := validInput()
	input.Items = append(input.Items, service.CreateOrderItemInput{
		VariantID: "variant_card", Title: "Gift card", Quantity: 2, UnitPrice: 2_500,
		Subtotal: 5_000, Total: 5_000, IsGiftcard: true,
	})
	input.Subtotal += 5_000
	input.Total += 5_000
	kept, err := svc.CreateOrder(ctx, input)
	require.NoError(t, err)
	canceled, err := svc.CreateOrder(ctx, input)
	require.NoError(t, err)
	require.NoError(t, svc.CancelOrder(ctx, canceled.ID, "journal test"))

	detail, err := svc.GetOrder(ctx, kept.ID)
	require.NoError(t, err)
	flags := map[string]bool{}
	for _, item := range detail.Items {
		flags[item.VariantID] = item.IsGiftcard
	}
	assert.Equal(t, map[string]bool{"variant_A": false, "variant_card": true}, flags)

	journal, err := svc.Journal(ctx, service.JournalQuery{
		From: from, To: time.Now().UTC().Add(time.Minute), CurrencyCode: testCurrency,
	})
	require.NoError(t, err)
	byKind := map[string][]models.JournalLine{}
	for _, entry := range journal.Entries {
		if entry.OrderID == kept.ID || entry.OrderID == canceled.ID {
			byKind[entry.OrderID+"/"+string(entry.Kind)] = entry.Lines
		}
	}

	placed := byKind[kept.ID+"/"+string(models.JournalOrderPlaced)]
	assert.Contains(t, placed, models.JournalLine{Account: models.AccountSales, Credit: 3_000})
	assert.Contains(t, placed, models.JournalLine{Account: models.AccountGiftCard, Credit: 5_000})
	assert.Contains(t, byKind[canceled.ID+"/"+string(models.JournalOrderCanceled)],
		models.JournalLine{Account: models.AccountGiftCard, Debit: 5_000}, "a cancellation takes the debt back")
}
