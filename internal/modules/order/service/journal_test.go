package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
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

// JournalCauses returns the scripted causes among the ids asked for.
func (f *fakeStore) JournalCauses(_ context.Context, ids []string) ([]models.JournalCause, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := []models.JournalCause{}
	for _, cause := range f.causes {
		if slices.Contains(ids, cause.ID) {
			out = append(out, cause)
		}
	}
	return out, nil
}

// JournalActOrders returns the scripted credit lines and delivery changes among
// the ids asked for (ADR 0419).
func (f *fakeStore) JournalActOrders(
	_ context.Context, creditLineIDs, changeIDs []string,
) ([]models.JournalActOrder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := []models.JournalActOrder{}
	for _, act := range f.actOrders {
		if (act.Kind == "credit_line" && slices.Contains(creditLineIDs, act.ID)) ||
			(act.Kind == "delivery_change" && slices.Contains(changeIDs, act.ID)) {
			out = append(out, act)
		}
	}
	return out, nil
}

// OrderAfterSaleCauses returns the order's scripted causes (ADR 0406).
func (f *fakeStore) OrderAfterSaleCauses(
	_ context.Context, orderID string, _ int32,
) ([]models.AfterSaleCause, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.afterSaleCauses[orderID]), nil
}

// scriptedRefunds is the payment module's caused refunds, as a JSON answer.
type scriptedRefunds struct{ body string }

// CausedRefundsJSON returns the scripted answer.
func (s scriptedRefunds) CausedRefundsJSON(
	_ context.Context, _, _ time.Time, _ string,
) (json.RawMessage, error) {
	return json.RawMessage(s.body), nil
}

// CausedRefundsOfJSON returns the scripted refunds that name one of the
// causes (ADR 0406).
func (s scriptedRefunds) CausedRefundsOfJSON(_ context.Context, references []string) (json.RawMessage, error) {
	var all []map[string]any
	if err := json.Unmarshal([]byte(s.body), &all); err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, refund := range all {
		if reference, _ := refund["reference"].(string); slices.Contains(references, reference) {
			out = append(out, refund)
		}
	}

	return json.Marshal(out)
}

// CausedRefundsByIDJSON returns the scripted refunds with one of the ids
// (ADR 0419).
func (s scriptedRefunds) CausedRefundsByIDJSON(_ context.Context, ids []string) (json.RawMessage, error) {
	var all []map[string]any
	if err := json.Unmarshal([]byte(s.body), &all); err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, refund := range all {
		if id, _ := refund["id"].(string); slices.Contains(ids, id) {
			out = append(out, refund)
		}
	}

	return json.Marshal(out)
}

// scriptedDocuments is the invoice module's amending documents, as a JSON
// answer read over the window it is asked for (ADR 0419): a document is in
// the answer when it was issued or voided inside the window, as the real
// query reads it.
type scriptedDocuments struct{ body string }

// DocumentedTaxJSON returns the scripted documents issued or voided inside
// [from, to), the bounds cut to the microsecond as the database driver cuts
// them.
func (s scriptedDocuments) DocumentedTaxJSON(
	_ context.Context, from, to time.Time, _ string,
) (json.RawMessage, error) {
	from, to = from.Truncate(time.Microsecond), to.Truncate(time.Microsecond)
	var all []map[string]any
	if err := json.Unmarshal([]byte(s.body), &all); err != nil {
		return nil, err
	}
	in := func(field any) bool {
		text, _ := field.(string)
		at, err := time.Parse(time.RFC3339Nano, text)
		return err == nil && !at.Before(from) && at.Before(to)
	}
	out := []map[string]any{}
	for _, document := range all {
		if in(document["issued_at"]) || in(document["voided_at"]) {
			out = append(out, document)
		}
	}

	return json.Marshal(out)
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
		models.JournalFact{ID: "odchg_1", Kind: models.JournalDeliveryChanged, OrderID: "order_1",
			OccurredAt: orderJournalStart.Add(4 * time.Minute), CurrencyCode: "TRY", Amount: 200},
		models.JournalFact{ID: "odchg_2", Kind: models.JournalDeliveryUpgraded, OrderID: "order_1",
			OccurredAt: orderJournalStart.Add(5 * time.Minute), CurrencyCode: "TRY", Amount: 300},
		models.JournalFact{ID: "exch_1", Kind: models.JournalExchangeFunded, OrderID: "order_1",
			OccurredAt: orderJournalStart.Add(6 * time.Minute), CurrencyCode: "TRY", Amount: 400},
	)
	require.NoError(t, err)
	require.Len(t, journal.Entries, 6)

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

	assert.Equal(t, []models.JournalLine{
		{Account: models.AccountShipping, Debit: 200},
		{Account: models.AccountReceivable, Credit: 200},
	}, journal.Entries[3].Lines, "a cheaper delivery gives back shipping (ADR 0199)")

	assert.Equal(t, []models.JournalLine{
		{Account: models.AccountReceivable, Debit: 300},
		{Account: models.AccountShipping, Credit: 300},
	}, journal.Entries[4].Lines, "a dearer delivery is owed and charged as shipping (ADR 0200)")

	assert.Equal(t, []models.JournalLine{
		{Account: models.AccountReceivable, Debit: 400},
		{Account: models.AccountSales, Credit: 400},
	}, journal.Entries[5].Lines, "an exchange's collected difference is owed and sold (ADR 0203)")
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

// TestASoldGiftCardIsADebtNotASale is ADR 0211: the part of the subtotal the
// gift card lines sold is owed to the cards' holders, and a cancellation takes
// it back the same way.
func TestASoldGiftCardIsADebtNotASale(t *testing.T) {
	t.Parallel()

	placed := placedOrder(models.JournalOrderPlaced, 1)
	placed.GiftCardSubtotal = 4_000
	canceled := placedOrder(models.JournalOrderCanceled, 2)
	canceled.GiftCardSubtotal = 4_000
	allCards := placedOrder(models.JournalOrderPlaced, 3)
	allCards.ID, allCards.OrderID, allCards.GiftCardSubtotal = "order_2", "order_2", 10_000

	journal, err := orderJournal(t, placed, canceled, allCards)
	require.NoError(t, err)
	require.Len(t, journal.Entries, 3)

	lines := []models.JournalLine{
		{Account: models.AccountReceivable, Debit: 11_300},
		{Account: models.AccountSalesDiscounts, Debit: 1_000},
		{Account: models.AccountSales, Credit: 6_000},
		{Account: models.AccountGiftCard, Credit: 4_000},
		{Account: models.AccountTaxPayable, Credit: 1_800},
		{Account: models.AccountShipping, Credit: 500},
	}
	assert.Equal(t, lines, journal.Entries[0].Lines)
	reversed := make([]models.JournalLine, len(lines))
	for i, line := range lines {
		reversed[i] = models.JournalLine{Account: line.Account, Debit: line.Credit, Credit: line.Debit}
	}
	assert.Equal(t, reversed, journal.Entries[1].Lines)

	for _, line := range journal.Entries[2].Lines {
		assert.NotEqual(t, models.AccountSales, line.Account, "an order of cards alone sold nothing")
	}
	assert.Contains(t, journal.Entries[2].Lines, models.JournalLine{Account: models.AccountGiftCard, Credit: 10_000})
}

// TestGiftCardLinesBeyondTheSubtotalAreAnError: a sum that could only come from
// a broken row would book a negative sale.
func TestGiftCardLinesBeyondTheSubtotalAreAnError(t *testing.T) {
	t.Parallel()

	broken := placedOrder(models.JournalOrderPlaced, 1)
	broken.GiftCardSubtotal = broken.Subtotal + 1

	_, err := orderJournal(t, broken)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal), "%v", err)
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

// TestARefundIsBookedAgainstItsCause is ADR 0189's reading and 0203's: a refund
// naming a return gives back revenue, one naming a claim is an allowance, one
// naming an exchange reverses the sale its funding booked, and one naming no
// record of this module is not an entry.
func TestARefundIsBookedAgainstItsCause(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	store.causes = []models.JournalCause{
		{ID: "ret_1", Kind: "return", OrderID: "order_1", CurrencyCode: "TRY"},
		{ID: "claim_1", Kind: "claim", OrderID: "order_2", CurrencyCode: "TRY"},
		{ID: "exch_1", Kind: "exchange", OrderID: "order_3", CurrencyCode: "TRY"},
	}
	svc, err := service.New(service.Options{Repo: store, Events: newFakeBus(t), Refunds: scriptedRefunds{body: `[
		{"id":"refund_a","reference":"ret_1","amount":1200,"currency_code":"TRY","refunded_at":"2026-09-02T10:00:00Z"},
		{"id":"refund_b","reference":"claim_1","amount":300,"currency_code":"TRY","refunded_at":"2026-09-03T10:00:00Z"},
		{"id":"refund_c","reference":"exch_1","amount":900,"currency_code":"TRY","refunded_at":"2026-09-04T10:00:00Z"},
		{"id":"refund_d","reference":"nothing_1","amount":50,"currency_code":"TRY","refunded_at":"2026-09-05T10:00:00Z"}
	]`}})
	require.NoError(t, err)

	journal, err := svc.Journal(t.Context(), service.JournalQuery{
		From: orderJournalStart, To: orderJournalStart.AddDate(0, 1, 0),
	})
	require.NoError(t, err)

	require.Len(t, journal.Entries, 3, "a refund naming no record of the module is not an entry")
	assert.Equal(t, models.JournalEntry{
		ID: "refund_a", Kind: models.JournalReturnRefunded, OrderID: "order_1",
		OccurredAt: time.Date(2026, time.September, 2, 10, 0, 0, 0, time.UTC), CurrencyCode: "TRY",
		Lines: []models.JournalLine{
			{Account: models.AccountSalesReturns, Debit: 1200},
			{Account: models.AccountReceivable, Credit: 1200},
		},
	}, journal.Entries[0])
	assert.Equal(t, []models.JournalLine{
		{Account: models.AccountClaimAllowances, Debit: 300},
		{Account: models.AccountReceivable, Credit: 300},
	}, journal.Entries[1].Lines)
	assert.Equal(t, "order_2", journal.Entries[1].OrderID)
	assert.Equal(t, models.JournalExchangeRefunded, journal.Entries[2].Kind)
	assert.Equal(t, "order_3", journal.Entries[2].OrderID)
	assert.Equal(t, []models.JournalLine{
		{Account: models.AccountSales, Debit: 900},
		{Account: models.AccountReceivable, Credit: 900},
	}, journal.Entries[2].Lines, "an exchange's refund reverses the sale its funding booked")
}

// TestARefundInAnotherCurrencyThanItsOrderIsAnError: the reference would name
// the wrong order, and books built on it would move money between currencies.
func TestARefundInAnotherCurrencyThanItsOrderIsAnError(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	store.causes = []models.JournalCause{{ID: "ret_1", Kind: "return", OrderID: "order_1", CurrencyCode: "TRY"}}
	svc, err := service.New(service.Options{Repo: store, Events: newFakeBus(t), Refunds: scriptedRefunds{body: `[
		{"id":"refund_a","reference":"ret_1","amount":1200,"currency_code":"EUR","refunded_at":"2026-09-02T10:00:00Z"}
	]`}})
	require.NoError(t, err)

	_, err = svc.Journal(t.Context(), service.JournalQuery{
		From: orderJournalStart, To: orderJournalStart.AddDate(0, 1, 0),
	})

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal), "%v", err)
}

// documentedOrder is order_1's acts as the journal finds them (ADR 0419): a
// credit line, a delivery change, and two refunds, one caused by a return and
// one by a claim, whose ids are not their causes'.
func documentedOrder(t *testing.T, documents string) *service.Service {
	t.Helper()

	store := newFakeStore()
	store.actOrders = []models.JournalActOrder{
		{ID: "credit_1", Kind: "credit_line", OrderID: "order_1", CurrencyCode: "TRY"},
		{ID: "change_1", Kind: "delivery_change", OrderID: "order_1", CurrencyCode: "TRY"},
	}
	store.causes = []models.JournalCause{
		{ID: "ret_1", Kind: "return", OrderID: "order_1", CurrencyCode: "TRY"},
		{ID: "claim_1", Kind: "claim", OrderID: "order_1", CurrencyCode: "TRY"},
	}
	svc, err := service.New(service.Options{
		Repo: store, Events: newFakeBus(t),
		// The refunds are made in August, outside every window these tests
		// read: a documented refund is found by its id, not by the window.
		Refunds: refundsOutsideTheWindow{scriptedRefunds{body: `[
			{"id":"refund_r","reference":"ret_1","amount":1200,"currency_code":"TRY","refunded_at":"2026-08-02T10:00:00Z"},
			{"id":"refund_c","reference":"claim_1","amount":600,"currency_code":"TRY","refunded_at":"2026-08-03T10:00:00Z"}
		]`}},
		Documents: scriptedDocuments{body: documents},
	})
	require.NoError(t, err)

	return svc
}

// refundsOutsideTheWindow is refunds made before every window read: the
// window holds none of them, and they are found by id or by cause.
type refundsOutsideTheWindow struct{ scriptedRefunds }

// CausedRefundsJSON answers that the window holds no refund.
func (refundsOutsideTheWindow) CausedRefundsJSON(
	_ context.Context, _, _ time.Time, _ string,
) (json.RawMessage, error) {
	return json.RawMessage(`[]`), nil
}

// september reads September 2026, and october the month after.
func september(t *testing.T, svc *service.Service) (service.Journal, error) {
	t.Helper()
	return svc.Journal(t.Context(), service.JournalQuery{From: orderJournalStart, To: orderJournalStart.AddDate(0, 1, 0)})
}

func october(t *testing.T, svc *service.Service) (service.Journal, error) {
	t.Helper()
	return svc.Journal(t.Context(), service.JournalQuery{
		From: orderJournalStart.AddDate(0, 1, 0), To: orderJournalStart.AddDate(0, 2, 0),
	})
}

// TestADocumentsTaxMovesFromTheActsAccountToTaxPayable is ADR 0419's chart: a
// refund document takes its tax off tax_payable and off the account its act
// gave back from, a sale document puts it on tax_payable out of the account its
// act charged to, and a document voided in the same window nets to nothing.
func TestADocumentsTaxMovesFromTheActsAccountToTaxPayable(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		key, kind string
		lines     []models.JournalLine
	}{
		{"credit_line:credit_1", "refund", []models.JournalLine{
			{Account: models.AccountTaxPayable, Debit: 200}, {Account: models.AccountCreditAllowances, Credit: 200},
		}},
		{"delivery_changed:change_1", "refund", []models.JournalLine{
			{Account: models.AccountTaxPayable, Debit: 200}, {Account: models.AccountShipping, Credit: 200},
		}},
		{"return_refunded:refund_r", "refund", []models.JournalLine{
			{Account: models.AccountTaxPayable, Debit: 200}, {Account: models.AccountSalesReturns, Credit: 200},
		}},
		{"claim_refunded:refund_c", "refund", []models.JournalLine{
			{Account: models.AccountTaxPayable, Debit: 200}, {Account: models.AccountClaimAllowances, Credit: 200},
		}},
		{"delivery_upgraded:change_1", "sale", []models.JournalLine{
			{Account: models.AccountShipping, Debit: 200}, {Account: models.AccountTaxPayable, Credit: 200},
		}},
	} {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()

			svc := documentedOrder(t, `[
				{"id":"inv_1","kind":"`+tc.kind+`","amendment_key":"`+tc.key+`","tax_total":200,
				 "currency_code":"TRY","issued_at":"2026-09-10T10:00:00Z","voided_at":null}
			]`)
			journal, err := september(t, svc)
			require.NoError(t, err)
			require.Len(t, journal.Entries, 1)
			assert.Equal(t, models.JournalEntry{
				ID: "inv_1", Kind: models.JournalTaxCorrected, OrderID: "order_1",
				OccurredAt: time.Date(2026, time.September, 10, 10, 0, 0, 0, time.UTC), CurrencyCode: "TRY",
				Lines: tc.lines,
			}, journal.Entries[0])

			voided := documentedOrder(t, `[
				{"id":"inv_1","kind":"`+tc.kind+`","amendment_key":"`+tc.key+`","tax_total":200,
				 "currency_code":"TRY","issued_at":"2026-09-10T10:00:00Z","voided_at":"2026-09-12T10:00:00Z"}
			]`)
			journal, err = september(t, voided)
			require.NoError(t, err)
			require.Len(t, journal.Entries, 2)
			assert.Equal(t, models.JournalTaxCorrectionVoided, journal.Entries[1].Kind)
			assert.Equal(t, time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC), journal.Entries[1].OccurredAt)
			for _, balance := range journal.Balances {
				assert.Equal(t, balance.Debit, balance.Credit, "a voided document nets to nothing on %s", balance.Account)
			}
		})
	}
}

// TestADocumentIsBookedAtItsOwnMoment: a document issued in September and
// voided in October is a correction in September and its voiding in October,
// and September reads the same after the voiding (ADR 0419).
func TestADocumentIsBookedAtItsOwnMoment(t *testing.T) {
	t.Parallel()

	standing := documentedOrder(t, `[
		{"id":"inv_1","kind":"refund","amendment_key":"return_refunded:refund_r","tax_total":2000,
		 "currency_code":"TRY","issued_at":"2026-09-10T10:00:00Z","voided_at":null}
	]`)
	before, err := september(t, standing)
	require.NoError(t, err)

	voided := documentedOrder(t, `[
		{"id":"inv_1","kind":"refund","amendment_key":"return_refunded:refund_r","tax_total":2000,
		 "currency_code":"TRY","issued_at":"2026-09-10T10:00:00Z","voided_at":"2026-10-05T10:00:00Z"}
	]`)
	after, err := september(t, voided)
	require.NoError(t, err)
	assert.Equal(t, before, after, "a later voiding leaves the window of the issue as it was read")
	require.Len(t, after.Entries, 1)
	assert.Equal(t, models.JournalTaxCorrected, after.Entries[0].Kind)

	next, err := october(t, voided)
	require.NoError(t, err)
	require.Len(t, next.Entries, 1)
	assert.Equal(t, models.JournalEntry{
		ID: "inv_1", Kind: models.JournalTaxCorrectionVoided, OrderID: "order_1",
		OccurredAt: time.Date(2026, time.October, 5, 10, 0, 0, 0, time.UTC), CurrencyCode: "TRY",
		Lines: []models.JournalLine{
			{Account: models.AccountTaxPayable, Credit: 2000}, {Account: models.AccountSalesReturns, Debit: 2000},
		},
	}, next.Entries[0], "the voiding puts the tax back on tax_payable at its own moment")
}

// TestAZeroTaxDocumentWritesNoEntry: a delivery's document moves no tax, since
// carriage is untaxed, and writes no line (ADR 0419).
func TestAZeroTaxDocumentWritesNoEntry(t *testing.T) {
	t.Parallel()

	svc := documentedOrder(t, `[
		{"id":"inv_1","kind":"sale","amendment_key":"delivery_upgraded:change_1","tax_total":0,
		 "currency_code":"TRY","issued_at":"2026-09-10T10:00:00Z","voided_at":null}
	]`)
	journal, err := september(t, svc)
	require.NoError(t, err)
	assert.Empty(t, journal.Entries)
}

// TestADocumentTheJournalCannotPlaceIsAnError: a document in another currency
// than its order's, one whose key names an act kind no document names, one
// naming an act this module has not got, one whose refund's cause is of
// another kind than its key says, and a sale document naming an act that
// charged nothing are errors rather than entries (ADR 0419).
func TestADocumentTheJournalCannotPlaceIsAnError(t *testing.T) {
	t.Parallel()

	for name, document := range map[string]string{
		"another currency":     `"kind":"refund","amendment_key":"credit_line:credit_1","currency_code":"EUR"`,
		"an exchange":          `"kind":"refund","amendment_key":"exchange_refunded:refund_r","currency_code":"TRY"`,
		"no kind":              `"kind":"refund","amendment_key":"credit_1","currency_code":"TRY"`,
		"an act it has not":    `"kind":"refund","amendment_key":"credit_line:credit_9","currency_code":"TRY"`,
		"a claim as a return":  `"kind":"refund","amendment_key":"return_refunded:refund_c","currency_code":"TRY"`,
		"a sale on a credit":   `"kind":"sale","amendment_key":"credit_line:credit_1","currency_code":"TRY"`,
		"a refund on a charge": `"kind":"refund","amendment_key":"delivery_upgraded:change_1","currency_code":"TRY"`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc := documentedOrder(t, `[{"id":"inv_1",`+document+`,"tax_total":200,
				"issued_at":"2026-09-10T10:00:00Z","voided_at":null}]`)
			_, err := september(t, svc)
			require.Error(t, err)
			assert.True(t, errors.HasKind(err, errors.KindInternal), "%v", err)
		})
	}
}

// TestTheJournalsCeilingCountsTheDocuments: a window whose facts fit the
// ceiling only without its documents is refused rather than cut (ADR 0419).
func TestTheJournalsCeilingCountsTheDocuments(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	store.actOrders = []models.JournalActOrder{{ID: "credit_1", Kind: "credit_line", OrderID: "order_1", CurrencyCode: "TRY"}}
	for i := range service.MaxJournalEntries {
		fact := placedOrder(models.JournalOrderPlaced, i%1000)
		fact.ID = fmt.Sprintf("order_%d", i)
		store.journal = append(store.journal, fact)
	}
	svc, err := service.New(service.Options{
		Repo: store, Events: newFakeBus(t),
		Documents: scriptedDocuments{body: `[
			{"id":"inv_1","kind":"refund","amendment_key":"credit_line:credit_1","tax_total":200,
			 "currency_code":"TRY","issued_at":"2026-09-10T10:00:00Z","voided_at":null}
		]`},
	})
	require.NoError(t, err)

	_, err = september(t, svc)
	require.Error(t, err, "%d facts and a document are past the ceiling", service.MaxJournalEntries)
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "%v", err)
}

// TestADocumentAtAWindowsEdgeIsInExactlyOneWindow: a window takes the document
// stamped at its start and leaves the one stamped at its end to the next, and
// a bound written past the microsecond is cut as the database cuts it, so two
// adjacent windows hold the document once between them (ADR 0419).
func TestADocumentAtAWindowsEdgeIsInExactlyOneWindow(t *testing.T) {
	t.Parallel()

	svc := documentedOrder(t, `[
		{"id":"inv_1","kind":"refund","amendment_key":"credit_line:credit_1","tax_total":200,
		 "currency_code":"TRY","issued_at":"2026-09-10T10:00:00.000001Z","voided_at":null}
	]`)
	stamp := time.Date(2026, time.September, 10, 10, 0, 0, 1000, time.UTC)
	count := func(from, to time.Time) int {
		t.Helper()
		journal, err := svc.Journal(t.Context(), service.JournalQuery{From: from, To: to})
		require.NoError(t, err)
		return len(journal.Entries)
	}

	assert.Equal(t, 1, count(stamp, stamp.Add(time.Hour)), "a window takes the document stamped at its start")
	assert.Equal(t, 0, count(stamp.Add(-time.Hour), stamp), "and leaves the one stamped at its end")

	// A bound half a microsecond past the stamp is the stamp's microsecond to
	// the database: the window ending there leaves the document out, so the
	// window starting there takes it.
	bound := stamp.Add(500 * time.Nanosecond)
	assert.Equal(t, 1, count(stamp.Add(-time.Hour), bound)+count(bound, stamp.Add(time.Hour)),
		"two adjacent windows hold the document once between them")
}

// refusingDocuments is an invoice module that refuses the read as invalid,
// as it refuses a window holding more documents than one read takes.
type refusingDocuments struct{}

// DocumentedTaxJSON refuses the read.
func (refusingDocuments) DocumentedTaxJSON(context.Context, time.Time, time.Time, string) (json.RawMessage, error) {
	return nil, errors.Invalid("invoice_invalid_input", "the window holds more than 10000 documents that name an act")
}

// TestTheInvoiceModulesRefusalIsAnsweredInTheJournalsCode: the caller asked
// the order module, and is answered in its code, not the invoice module's
// (ADR 0419).
func TestTheInvoiceModulesRefusalIsAnsweredInTheJournalsCode(t *testing.T) {
	t.Parallel()

	svc, err := service.New(service.Options{Repo: newFakeStore(), Events: newFakeBus(t), Documents: refusingDocuments{}})
	require.NoError(t, err)

	_, err = september(t, svc)
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "%v", err)
	assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
}

// TestAZeroTaxDocumentIsHeldToItsKinds: a document that moved no tax writes no
// line, and a kind the journal cannot place is refused all the same, as it is
// on a document that moved some (ADR 0419).
func TestAZeroTaxDocumentIsHeldToItsKinds(t *testing.T) {
	t.Parallel()

	for name, document := range map[string]string{
		"a refund on a charge":  `"kind":"refund","amendment_key":"delivery_upgraded:change_1"`,
		"a sale on a credit":    `"kind":"sale","amendment_key":"credit_line:credit_1"`,
		"neither kind of paper": `"kind":"proforma","amendment_key":"credit_line:credit_1"`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc := documentedOrder(t, `[{"id":"inv_1",`+document+`,"tax_total":0,
				"currency_code":"TRY","issued_at":"2026-09-10T10:00:00Z","voided_at":null}]`)
			_, err := september(t, svc)
			require.Error(t, err)
			assert.True(t, errors.HasKind(err, errors.KindInternal), "%v", err)
		})
	}
}
