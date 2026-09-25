package service

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// The order journal (ADR 0188): the order module's facts as balanced debit
// and credit lines, derived when read from records the module already keeps.

const (
	// MaxJournalWindow is the widest window one read of the journal covers,
	// the payment journal's quarter (ADR 0186).
	MaxJournalWindow = 93 * 24 * time.Hour
	// MaxJournalEntries is the most entries one read returns; a window holding
	// more is refused rather than cut.
	MaxJournalEntries = 10000
)

// JournalQuery is the window a journal read covers: [From, To), and one
// currency when CurrencyCode is set.
type JournalQuery struct {
	From, To     time.Time
	CurrencyCode string
}

// Journal is the module's books over a window.
type Journal struct {
	From         time.Time
	To           time.Time
	CurrencyCode string
	// Entries are in time order, and every one of them balances.
	Entries []models.JournalEntry
	// Balances are the entries' sums per currency and account.
	Balances []models.JournalBalance
}

// Journal derives the module's books over a window.
func (s *Service) Journal(ctx context.Context, q JournalQuery) (Journal, error) {
	if q.From.IsZero() || q.To.IsZero() {
		return Journal{}, errors.Invalid(CodeInvalidInput, "the journal needs both ends of its window")
	}
	from, to := q.From.UTC(), q.To.UTC()
	if !from.Before(to) {
		return Journal{}, errors.Invalid(CodeInvalidInput,
			"the journal's window has to end after it begins: %s is not before %s",
			from.Format(time.RFC3339), to.Format(time.RFC3339))
	}
	if to.Sub(from) > MaxJournalWindow {
		return Journal{}, errors.Invalid(CodeInvalidInput,
			"the journal reads at most %d days at a time", int(MaxJournalWindow/(24*time.Hour)))
	}
	currency := ""
	if q.CurrencyCode != "" {
		normalized, err := normalizeCurrency(q.CurrencyCode)
		if err != nil {
			return Journal{}, err
		}
		currency = normalized
	}

	facts, err := s.store.JournalFacts(ctx, from, to, currency, MaxJournalEntries)
	if err != nil {
		return Journal{}, err
	}
	if len(facts) > MaxJournalEntries {
		return Journal{}, errors.Invalid(CodeInvalidInput,
			"the window holds more than %d facts; ask for a narrower one", MaxJournalEntries)
	}

	entries, err := journalEntries(facts)
	if err != nil {
		return Journal{}, err
	}

	return Journal{
		From: from, To: to, CurrencyCode: currency,
		Entries: entries, Balances: trialBalance(entries),
	}, nil
}

// journalEntries names the accounts of every fact and puts the entries in time
// order, the id and then the kind breaking a tie: an order placed and canceled
// in the same instant keeps one order of the two.
func journalEntries(facts []models.JournalFact) ([]models.JournalEntry, error) {
	entries := make([]models.JournalEntry, 0, len(facts))
	for i := range facts {
		entry, err := journalEntry(&facts[i])
		if err != nil {
			return nil, err
		}
		if len(entry.Lines) == 0 {
			// A free order moves nothing onto the books.
			continue
		}
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(a, b models.JournalEntry) int {
		return cmp.Or(a.OccurredAt.Compare(b.OccurredAt), cmp.Compare(a.ID, b.ID),
			cmp.Compare(kindOrder(a.Kind), kindOrder(b.Kind)))
	})

	return entries, nil
}

// kindOrder puts a placement before the cancellation of the same order.
func kindOrder(kind models.JournalKind) int {
	switch kind {
	case models.JournalOrderPlaced:
		return 0
	case models.JournalOrderCanceled:
		return 1
	default:
		return 2
	}
}

// journalEntry is the chart of accounts, in one place.
//
//	order placed    Dr receivable (total), sales_discounts (discount)
//	                Cr sales (subtotal), tax_payable (tax), shipping (shipping)
//	order canceled  the same lines, the other way
//	credit line     Dr credit_allowances   Cr receivable
//
// An order balances because its table holds it to
// total = subtotal - discount_total + tax_total + shipping_total; the entry is
// checked anyway, since a row that broke the identity would otherwise be books
// that do not balance. A zero amount writes no line.
func journalEntry(f *models.JournalFact) (models.JournalEntry, error) {
	entry := models.JournalEntry{
		ID: f.ID, Kind: f.Kind, OrderID: f.OrderID,
		OccurredAt: f.OccurredAt.UTC(), CurrencyCode: f.CurrencyCode,
	}

	switch f.Kind {
	case models.JournalOrderPlaced, models.JournalOrderCanceled:
		for _, amount := range []int64{f.Subtotal, f.DiscountTotal, f.TaxTotal, f.ShippingTotal, f.Total} {
			if amount < 0 {
				return models.JournalEntry{}, errors.Internal(CodeInvalidInput,
					"the journal read order %s with a negative amount", f.ID)
			}
		}
		if f.Total+f.DiscountTotal != f.Subtotal+f.TaxTotal+f.ShippingTotal {
			return models.JournalEntry{}, errors.Internal(CodeInvalidInput,
				"the journal read order %s whose total does not add up", f.ID)
		}
		lines := []models.JournalLine{
			{Account: models.AccountReceivable, Debit: f.Total},
			{Account: models.AccountSalesDiscounts, Debit: f.DiscountTotal},
			{Account: models.AccountSales, Credit: f.Subtotal},
			{Account: models.AccountTaxPayable, Credit: f.TaxTotal},
			{Account: models.AccountShipping, Credit: f.ShippingTotal},
		}
		for _, line := range lines {
			if line.Debit == 0 && line.Credit == 0 {
				continue
			}
			if f.Kind == models.JournalOrderCanceled {
				line.Debit, line.Credit = line.Credit, line.Debit
			}
			entry.Lines = append(entry.Lines, line)
		}
	case models.JournalCreditLine:
		if f.Amount <= 0 {
			return models.JournalEntry{}, errors.Internal(CodeInvalidInput,
				"the journal read credit line %s of %d; a credit line is positive", f.ID, f.Amount)
		}
		entry.Lines = []models.JournalLine{
			{Account: models.AccountCreditAllowances, Debit: f.Amount},
			{Account: models.AccountReceivable, Credit: f.Amount},
		}
	default:
		return models.JournalEntry{}, errors.Internal(CodeInvalidInput,
			"the journal read a fact of an unknown kind %q (%s)", f.Kind, f.ID)
	}

	return entry, nil
}

// trialBalance sums the entries per currency and account, in a stable order.
func trialBalance(entries []models.JournalEntry) []models.JournalBalance {
	type key struct {
		currency string
		account  models.JournalAccount
	}
	sums := map[key]*models.JournalBalance{}
	for i := range entries {
		for j := range entries[i].Lines {
			line := &entries[i].Lines[j]
			k := key{entries[i].CurrencyCode, line.Account}
			sum, ok := sums[k]
			if !ok {
				sum = &models.JournalBalance{CurrencyCode: k.currency, Account: k.account}
				sums[k] = sum
			}
			sum.Debit += line.Debit
			sum.Credit += line.Credit
		}
	}

	out := make([]models.JournalBalance, 0, len(sums))
	for _, sum := range sums {
		out = append(out, *sum)
	}
	slices.SortFunc(out, func(a, b models.JournalBalance) int {
		return cmp.Or(cmp.Compare(a.CurrencyCode, b.CurrencyCode), cmp.Compare(a.Account, b.Account))
	})

	return out
}
