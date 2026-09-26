package service

import (
	"cmp"
	"context"
	"encoding/json"
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
	refunded, err := s.refundFacts(ctx, from, to, currency)
	if err != nil {
		return Journal{}, err
	}
	facts = append(facts, refunded...)
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
//	order placed     Dr receivable (total), sales_discounts (discount)
//	                 Cr sales (subtotal), tax_payable (tax), shipping (shipping)
//	order canceled   the same lines, the other way
//	credit line      Dr credit_allowances   Cr receivable
//	return refunded  Dr sales_returns       Cr receivable
//	claim refunded   Dr claim_allowances    Cr receivable
//	delivery changed Dr shipping            Cr receivable
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
	case models.JournalCreditLine, models.JournalReturnRefunded, models.JournalClaimRefunded,
		models.JournalDeliveryChanged:
		if f.Amount <= 0 {
			return models.JournalEntry{}, errors.Internal(CodeInvalidInput,
				"the journal read %s %s of %d; it moves a positive amount", f.Kind, f.ID, f.Amount)
		}
		entry.Lines = []models.JournalLine{
			{Account: givenBackTo[f.Kind], Debit: f.Amount},
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

// givenBackTo is the account each kind of amount given back is debited to.
var givenBackTo = map[models.JournalKind]models.JournalAccount{
	models.JournalCreditLine:     models.AccountCreditAllowances,
	models.JournalReturnRefunded: models.AccountSalesReturns,
	models.JournalClaimRefunded:  models.AccountClaimAllowances,
	// A cheaper delivery gives back shipping the order charged, not a
	// concession (ADR 0199).
	models.JournalDeliveryChanged: models.AccountShipping,
}

// CausedRefunds is the surface of the payment module ("payment.interop") the
// journal reads (ADR 0189): the refunds inside [from, to) that name their
// cause, as a JSON array of {id, reference, amount, currency_code,
// collection_id, refunded_at}.
type CausedRefunds interface {
	CausedRefundsJSON(ctx context.Context, from, to time.Time, currencyCode string) (json.RawMessage, error)
}

// causedRefund is one element of [CausedRefunds.CausedRefundsJSON]'s answer.
// The field names are the payment module's and are written here on purpose:
// this module cannot import that one, and the e2e closure test is what holds
// the two spellings together (ADR 0189).
type causedRefund struct {
	ID           string    `json:"id"`
	Reference    string    `json:"reference"`
	Amount       int64     `json:"amount"`
	CurrencyCode string    `json:"currency_code"`
	RefundedAt   time.Time `json:"refunded_at"`
}

// refundFacts reads the refunds in the window that name one of this module's
// returns or claims, as facts on the order they belong to (ADR 0189).
//
// A refund whose reference is an exchange's, or nothing of this module's, is
// not a fact here: an exchange's difference is outside these books (ADR 0188).
// A refund in another currency than its order's is an error rather than an
// entry, because the reference would then name the wrong order.
func (s *Service) refundFacts(
	ctx context.Context, from, to time.Time, currency string,
) ([]models.JournalFact, error) {
	if s.refunds == nil {
		return nil, nil
	}
	raw, err := s.refunds.CausedRefundsJSON(ctx, from, to, currency)
	if err != nil {
		return nil, err
	}
	var refunds []causedRefund
	if err := json.Unmarshal(raw, &refunds); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeInvalidInput,
			"the payment module's refunds could not be read")
	}
	if len(refunds) == 0 {
		return nil, nil
	}

	references := make([]string, 0, len(refunds))
	for i := range refunds {
		references = append(references, refunds[i].Reference)
	}
	causes, err := s.store.JournalCauses(ctx, references)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]models.JournalCause, len(causes))
	for i := range causes {
		byID[causes[i].ID] = causes[i]
	}

	facts := make([]models.JournalFact, 0, len(refunds))
	for i := range refunds {
		refund := &refunds[i]
		cause, ok := byID[refund.Reference]
		if !ok {
			continue
		}
		if cause.CurrencyCode != refund.CurrencyCode {
			return nil, errors.Internal(CodeInvalidInput,
				"refund %s names %s %s in %s, and the order is in %s",
				refund.ID, cause.Kind, cause.ID, refund.CurrencyCode, cause.CurrencyCode)
		}
		kind := models.JournalReturnRefunded
		if cause.Kind == "claim" {
			kind = models.JournalClaimRefunded
		}
		facts = append(facts, models.JournalFact{
			ID: refund.ID, Kind: kind, OrderID: cause.OrderID,
			OccurredAt: refund.RefundedAt, CurrencyCode: refund.CurrencyCode, Amount: refund.Amount,
		})
	}

	return facts, nil
}
