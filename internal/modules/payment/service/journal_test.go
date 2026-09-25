package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// journalCall is one recorded JournalMovements call.
type journalCall struct {
	from, to time.Time
	currency string
	limit    int32
}

// JournalMovements records the call and returns the scripted movements.
func (f *fakeStore) JournalMovements(
	_ context.Context, from, to time.Time, currencyCode string, limit int32,
) ([]models.JournalMovement, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.journalCalls = append(f.journalCalls, journalCall{from, to, currencyCode, limit})
	return f.journal, nil
}

// CausedRefunds returns the scripted refunds that name a cause.
func (f *fakeStore) CausedRefunds(
	_ context.Context, _, _ time.Time, _ string, _ int32,
) ([]models.CausedRefund, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.caused, nil
}

// journalService builds a service over a fake store scripted with movements.
func journalService(t *testing.T, movements ...models.JournalMovement) (*service.Service, *fakeStore) {
	t.Helper()

	store := newFakeStore()
	store.journal = movements
	svc, err := service.New(service.Options{
		Store: store, Providers: service.NewProviderRegistry(), Events: newFakeBus(),
	})
	require.NoError(t, err)

	return svc, store
}

var journalStart = time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)

// journalQuery is September 2026.
func journalQuery() service.JournalQuery {
	return service.JournalQuery{From: journalStart, To: journalStart.AddDate(0, 1, 0)}
}

// at is a moment inside the window.
func at(minutes int) time.Time { return journalStart.Add(time.Duration(minutes) * time.Minute) }

// TestTheChartOfAccounts is ADR 0186's table, one movement of each kind.
func TestTheChartOfAccounts(t *testing.T) {
	t.Parallel()

	svc, _ := journalService(t,
		models.JournalMovement{ID: "pay_card", Kind: models.JournalCapture, OccurredAt: at(1),
			CurrencyCode: "TRY", Amount: 10_000, CollectionID: "paycol_1", ProviderID: "manual", CustomerID: "cus_1"},
		models.JournalMovement{ID: "pay_credit", Kind: models.JournalCapture, OccurredAt: at(2),
			CurrencyCode: "TRY", Amount: 3_000, CollectionID: "paycol_2", ProviderID: models.StoreCreditTenderID,
			CustomerID: "cus_1"},
		models.JournalMovement{ID: "pay_points", Kind: models.JournalCapture, OccurredAt: at(3),
			CurrencyCode: "TRY", Amount: 500, CollectionID: "paycol_3", ProviderID: models.LoyaltyTenderID,
			CustomerID: "cus_1"},
		models.JournalMovement{ID: "ref_card", Kind: models.JournalRefund, OccurredAt: at(4),
			CurrencyCode: "TRY", Amount: 2_500, CollectionID: "paycol_1", ProviderID: "manual", CustomerID: "cus_1"},
		models.JournalMovement{ID: "scr_1", Kind: models.JournalStoreCreditIssue, OccurredAt: at(5),
			CurrencyCode: "TRY", Amount: 4_000, CustomerID: "cus_1"},
		models.JournalMovement{ID: "lpt_earn", Kind: models.JournalLoyaltyEarn, OccurredAt: at(6),
			CurrencyCode: "TRY", Amount: 100, CustomerID: "cus_1"},
		models.JournalMovement{ID: "lpt_reverse", Kind: models.JournalLoyaltyReverse, OccurredAt: at(7),
			CurrencyCode: "TRY", Amount: -25, CustomerID: "cus_1"},
	)

	journal, err := svc.Journal(t.Context(), journalQuery())
	require.NoError(t, err)

	type side struct {
		account          models.JournalAccount
		provider, holder string
		amount           int64
	}
	want := map[string][2]side{
		"pay_card":    {{models.AccountProviderClearing, "manual", "", 10_000}, {models.AccountReceivable, "", "", 10_000}},
		"pay_credit":  {{models.AccountStoreCredit, "", "cus_1", 3_000}, {models.AccountReceivable, "", "", 3_000}},
		"pay_points":  {{models.AccountLoyalty, "", "cus_1", 500}, {models.AccountReceivable, "", "", 500}},
		"ref_card":    {{models.AccountReceivable, "", "", 2_500}, {models.AccountProviderClearing, "manual", "", 2_500}},
		"scr_1":       {{models.AccountStoreCreditGranted, "", "", 4_000}, {models.AccountStoreCredit, "", "cus_1", 4_000}},
		"lpt_earn":    {{models.AccountLoyaltyGranted, "", "", 100}, {models.AccountLoyalty, "", "cus_1", 100}},
		"lpt_reverse": {{models.AccountLoyalty, "", "cus_1", 25}, {models.AccountLoyaltyGranted, "", "", 25}},
	}
	require.Len(t, journal.Entries, len(want))
	for _, entry := range journal.Entries {
		sides, ok := want[entry.ID]
		require.True(t, ok, entry.ID)
		require.Len(t, entry.Lines, 2, entry.ID)
		debit, credit := entry.Lines[0], entry.Lines[1]
		assert.Equal(t, models.JournalLine{Account: sides[0].account, ProviderID: sides[0].provider,
			CustomerID: sides[0].holder, Debit: sides[0].amount}, debit, "%s debit", entry.ID)
		assert.Equal(t, models.JournalLine{Account: sides[1].account, ProviderID: sides[1].provider,
			CustomerID: sides[1].holder, Credit: sides[1].amount}, credit, "%s credit", entry.ID)
	}
}

// TestEveryEntryAndTheTrialBalanceBalance is the double-entry property,
// asserted on the output rather than trusted to the construction.
func TestEveryEntryAndTheTrialBalanceBalance(t *testing.T) {
	t.Parallel()

	svc, _ := journalService(t,
		models.JournalMovement{ID: "pay_1", Kind: models.JournalCapture, OccurredAt: at(1),
			CurrencyCode: "TRY", Amount: 7_000, ProviderID: "manual"},
		models.JournalMovement{ID: "pay_2", Kind: models.JournalCapture, OccurredAt: at(2),
			CurrencyCode: "EUR", Amount: 900, ProviderID: "stripe"},
		models.JournalMovement{ID: "ref_1", Kind: models.JournalRefund, OccurredAt: at(3),
			CurrencyCode: "TRY", Amount: 1_000, ProviderID: "manual"},
		models.JournalMovement{ID: "lpt_1", Kind: models.JournalLoyaltyReverse, OccurredAt: at(4),
			CurrencyCode: "EUR", Amount: -40, CustomerID: "cus_1"},
	)

	journal, err := svc.Journal(t.Context(), journalQuery())
	require.NoError(t, err)

	for _, entry := range journal.Entries {
		var debit, credit int64
		for _, line := range entry.Lines {
			debit += line.Debit
			credit += line.Credit
			assert.True(t, (line.Debit > 0) != (line.Credit > 0), "%s: a line is a debit or a credit", entry.ID)
		}
		assert.Equal(t, debit, credit, "%s does not balance", entry.ID)
	}

	totals := map[string][2]int64{}
	for _, balance := range journal.Balances {
		sum := totals[balance.CurrencyCode]
		totals[balance.CurrencyCode] = [2]int64{sum[0] + balance.Debit, sum[1] + balance.Credit}
	}
	assert.Equal(t, map[string][2]int64{"TRY": {8_000, 8_000}, "EUR": {940, 940}}, totals,
		"the trial balance balances in every currency, and no currency is summed into another")
}

// TestTheEntriesAreInTimeOrder puts the four kinds, read grouped by kind, into
// one sequence, the id breaking a tie.
func TestTheEntriesAreInTimeOrder(t *testing.T) {
	t.Parallel()

	svc, _ := journalService(t,
		models.JournalMovement{ID: "pay_b", Kind: models.JournalCapture, OccurredAt: at(5), CurrencyCode: "TRY", Amount: 1},
		models.JournalMovement{ID: "pay_a", Kind: models.JournalCapture, OccurredAt: at(5), CurrencyCode: "TRY", Amount: 1},
		models.JournalMovement{ID: "scr_1", Kind: models.JournalStoreCreditIssue, OccurredAt: at(1), CurrencyCode: "TRY",
			Amount: 1, CustomerID: "cus_1"},
		models.JournalMovement{ID: "ref_1", Kind: models.JournalRefund, OccurredAt: at(3), CurrencyCode: "TRY", Amount: 1},
	)

	journal, err := svc.Journal(t.Context(), journalQuery())
	require.NoError(t, err)

	ids := make([]string, 0, len(journal.Entries))
	for _, entry := range journal.Entries {
		ids = append(ids, entry.ID)
	}
	assert.Equal(t, []string{"scr_1", "ref_1", "pay_a", "pay_b"}, ids)
}

// TestTheWindowIsChecked refuses a window the journal would have to cut or
// guess, and passes the store the window and currency it was asked for.
func TestTheWindowIsChecked(t *testing.T) {
	t.Parallel()

	svc, store := journalService(t)
	for name, q := range map[string]service.JournalQuery{
		"no start":         {To: journalStart},
		"backwards":        {From: journalStart, To: journalStart.Add(-time.Hour)},
		"empty":            {From: journalStart, To: journalStart},
		"over a quarter":   {From: journalStart, To: journalStart.Add(service.MaxJournalWindow + time.Second)},
		"a bogus currency": {From: journalStart, To: journalStart.Add(time.Hour), CurrencyCode: "T1"},
	} {
		_, err := svc.Journal(t.Context(), q)
		assert.True(t, errors.IsInvalid(err), "%s: %v", name, err)
	}
	assert.Empty(t, store.journalCalls, "a refused window reads nothing")

	q := journalQuery()
	q.CurrencyCode = "try"
	_, err := svc.Journal(t.Context(), q)
	require.NoError(t, err)
	require.Len(t, store.journalCalls, 1)
	assert.Equal(t, journalCall{q.From, q.To, "TRY", service.MaxJournalEntries}, store.journalCalls[0])
}

// TestAWindowOverTheBoundIsRefusedNotCut: an export that balances while missing
// its tail is worse than a refusal.
func TestAWindowOverTheBoundIsRefusedNotCut(t *testing.T) {
	t.Parallel()

	movements := make([]models.JournalMovement, service.MaxJournalEntries+1)
	for i := range movements {
		movements[i] = models.JournalMovement{ID: "pay", Kind: models.JournalCapture,
			OccurredAt: at(1), CurrencyCode: "TRY", Amount: 1}
	}
	svc, _ := journalService(t, movements...)

	_, err := svc.Journal(t.Context(), journalQuery())

	assert.True(t, errors.IsInvalid(err), "%v", err)
}

// TestAMovementTheChartDoesNotKnowIsAnError keeps an unknown kind or a
// non-positive amount from becoming an entry that balances at zero.
func TestAMovementTheChartDoesNotKnowIsAnError(t *testing.T) {
	t.Parallel()

	for name, movement := range map[string]models.JournalMovement{
		"an unknown kind":    {ID: "x_1", Kind: "settlement", OccurredAt: at(1), CurrencyCode: "TRY", Amount: 1},
		"a zero capture":     {ID: "pay_1", Kind: models.JournalCapture, OccurredAt: at(1), CurrencyCode: "TRY"},
		"a positive reverse": {ID: "lpt_1", Kind: models.JournalLoyaltyReverse, OccurredAt: at(1), CurrencyCode: "TRY", Amount: 5},
	} {
		svc, _ := journalService(t, movement)

		_, err := svc.Journal(t.Context(), journalQuery())

		assert.Error(t, err, name)
	}
}

// TestCausedRefundsAreReadOverACheckedWindow refuses the windows the journal
// refuses, and hands back what the store read.
func TestCausedRefundsAreReadOverACheckedWindow(t *testing.T) {
	t.Parallel()

	svc, store := journalService(t)
	store.caused = []models.CausedRefund{{ID: "refund_1", Reference: "ret_1", Amount: 500, CurrencyCode: "TRY"}}

	_, err := svc.CausedRefunds(t.Context(), service.JournalQuery{From: journalStart, To: journalStart})
	assert.True(t, errors.IsInvalid(err), "an empty window: %v", err)

	refunds, err := svc.CausedRefunds(t.Context(), journalQuery())
	require.NoError(t, err)
	assert.Equal(t, store.caused, refunds)
}
