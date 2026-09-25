package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// JournalFacts returns the scripted facts (ADR 0188).
func (f *fakeStore) JournalFacts(
	_ context.Context, _, _ time.Time, _ string, _ int32,
) ([]models.JournalFact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.journal, nil
}

var orderJournalStart = time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)

// orderJournal reads September 2026 over the scripted facts.
func orderJournal(t *testing.T, facts ...models.JournalFact) (service.Journal, error) {
	t.Helper()

	e := newEnv(t)
	e.store.journal = facts

	return e.svc.Journal(t.Context(), service.JournalQuery{
		From: orderJournalStart, To: orderJournalStart.AddDate(0, 1, 0),
	})
}

// placedOrder is an order of 10,000 in goods, 1,000 off, 1,800 tax and 500
// shipping: 11,300 owed.
func placedOrder(kind models.JournalKind, minute int) models.JournalFact {
	return models.JournalFact{
		ID: "order_1", Kind: kind, OrderID: "order_1",
		OccurredAt:   orderJournalStart.Add(time.Duration(minute) * time.Minute),
		CurrencyCode: "TRY", Subtotal: 10_000, DiscountTotal: 1_000, TaxTotal: 1_800,
		ShippingTotal: 500, Total: 11_300,
	}
}

// TestTheOrderChartOfAccounts is ADR 0188's table.
func TestTheOrderChartOfAccounts(t *testing.T) {
	t.Parallel()

	journal, err := orderJournal(t,
		placedOrder(models.JournalOrderPlaced, 1),
		placedOrder(models.JournalOrderCanceled, 2),
		models.JournalFact{ID: "ocl_1", Kind: models.JournalCreditLine, OrderID: "order_1",
			OccurredAt: orderJournalStart.Add(3 * time.Minute), CurrencyCode: "TRY", Amount: 700},
	)
	require.NoError(t, err)
	require.Len(t, journal.Entries, 3)

	placed := []models.JournalLine{
		{Account: models.AccountReceivable, Debit: 11_300},
		{Account: models.AccountSalesDiscounts, Debit: 1_000},
		{Account: models.AccountSales, Credit: 10_000},
		{Account: models.AccountTaxPayable, Credit: 1_800},
		{Account: models.AccountShipping, Credit: 500},
	}
	assert.Equal(t, placed, journal.Entries[0].Lines)

	canceled := make([]models.JournalLine, len(placed))
	for i, line := range placed {
		canceled[i] = models.JournalLine{Account: line.Account, Debit: line.Credit, Credit: line.Debit}
	}
	assert.Equal(t, canceled, journal.Entries[1].Lines, "a cancellation is the placement the other way")

	assert.Equal(t, []models.JournalLine{
		{Account: models.AccountCreditAllowances, Debit: 700},
		{Account: models.AccountReceivable, Credit: 700},
	}, journal.Entries[2].Lines)
}

// TestTheOrderJournalBalances holds every entry and the trial balance to
// debits equal credits, and a zero amount to writing no line.
func TestTheOrderJournalBalances(t *testing.T) {
	t.Parallel()

	undiscounted := placedOrder(models.JournalOrderPlaced, 1)
	undiscounted.ID, undiscounted.OrderID = "order_2", "order_2"
	undiscounted.DiscountTotal, undiscounted.Total = 0, 12_300
	journal, err := orderJournal(t, placedOrder(models.JournalOrderPlaced, 1), undiscounted)
	require.NoError(t, err)

	for _, entry := range journal.Entries {
		var debit, credit int64
		for _, line := range entry.Lines {
			debit += line.Debit
			credit += line.Credit
			assert.True(t, (line.Debit > 0) != (line.Credit > 0), "%s: a line is one side", entry.ID)
		}
		assert.Equal(t, debit, credit, "%s does not balance", entry.ID)
	}
	assert.Len(t, journal.Entries[1].Lines, 4, "an order with no discount writes no discount line")

	var debits, credits int64
	for _, balance := range journal.Balances {
		debits += balance.Debit
		credits += balance.Credit
	}
	assert.Equal(t, debits, credits)
}

// TestAPlacementComesBeforeItsCancellation keeps an order placed and canceled
// in one instant in the order that happened.
func TestAPlacementComesBeforeItsCancellation(t *testing.T) {
	t.Parallel()

	journal, err := orderJournal(t,
		placedOrder(models.JournalOrderCanceled, 1), placedOrder(models.JournalOrderPlaced, 1))
	require.NoError(t, err)

	require.Len(t, journal.Entries, 2)
	assert.Equal(t, models.JournalOrderPlaced, journal.Entries[0].Kind)
}

// TestAnOrderThatDoesNotAddUpIsAnError refuses to book a row that breaks the
// table's own identity, rather than publish books that do not balance.
func TestAnOrderThatDoesNotAddUpIsAnError(t *testing.T) {
	t.Parallel()

	broken := placedOrder(models.JournalOrderPlaced, 1)
	broken.Total++

	_, err := orderJournal(t, broken)

	// The error is required first: KindOf(nil) is KindInternal, so the kind
	// alone would pass for no error at all.
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal), "%v", err)
}

// TestAFreeOrderIsNotAnEntry: an order of nothing moves nothing onto the books.
func TestAFreeOrderIsNotAnEntry(t *testing.T) {
	t.Parallel()

	journal, err := orderJournal(t, models.JournalFact{ID: "order_free", Kind: models.JournalOrderPlaced,
		OrderID: "order_free", OccurredAt: orderJournalStart.Add(time.Minute), CurrencyCode: "TRY"})

	require.NoError(t, err)
	assert.Empty(t, journal.Entries)
}
