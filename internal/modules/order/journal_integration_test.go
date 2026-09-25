//go:build integration

package order_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
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
