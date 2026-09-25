package service

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// The payment journal (ADR 0186): this module's movements as balanced debit and
// credit lines, derived when read from rows the module already keeps.

const (
	// MaxJournalWindow is the widest window one read of the journal covers: a
	// quarter, which is the period a book is closed for.
	MaxJournalWindow = 93 * 24 * time.Hour
	// MaxJournalEntries is the most entries one read returns. A window holding
	// more is refused rather than cut: a journal missing its tail balances, and
	// an export that balances while missing money is worse than a refusal.
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
	// Balances are the entries' sums per currency, account and provider: a
	// trial balance, whose debits equal its credits in every currency.
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

	movements, err := s.store.JournalMovements(ctx, from, to, currency, MaxJournalEntries)
	if err != nil {
		return Journal{}, err
	}
	if len(movements) > MaxJournalEntries {
		return Journal{}, errors.Invalid(CodeInvalidInput,
			"the window holds more than %d movements; ask for a narrower one", MaxJournalEntries)
	}

	entries, err := journalEntries(movements)
	if err != nil {
		return Journal{}, err
	}

	return Journal{
		From: from, To: to, CurrencyCode: currency,
		Entries: entries, Balances: trialBalance(entries),
	}, nil
}

// journalEntries names the accounts of every movement and puts the entries in
// time order, the id breaking a tie.
func journalEntries(movements []models.JournalMovement) ([]models.JournalEntry, error) {
	entries := make([]models.JournalEntry, 0, len(movements))
	for i := range movements {
		entry, err := journalEntry(&movements[i])
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(a, b models.JournalEntry) int {
		return cmp.Or(a.OccurredAt.Compare(b.OccurredAt), cmp.Compare(a.ID, b.ID))
	})

	return entries, nil
}

// journalEntry is the chart of accounts, in one place.
//
// Every entry is built as one debit and one credit of the same amount, so it
// balances by construction; the tests hold the construction to that anyway,
// since a line added to one side only would still compile.
//
//	capture            Dr the tender's account       Cr receivable
//	refund             Dr receivable                 Cr the tender's account
//	store credit issue Dr store_credit_granted       Cr store_credit
//	loyalty earn       Dr loyalty_granted            Cr loyalty
//	loyalty reverse    Dr loyalty                    Cr loyalty_granted
//
// The tender's account is where the money of a capture came from ([tenderLine]).
func journalEntry(m *models.JournalMovement) (models.JournalEntry, error) {
	entry := models.JournalEntry{
		ID: m.ID, Kind: m.Kind, OccurredAt: m.OccurredAt.UTC(),
		CurrencyCode: m.CurrencyCode, CollectionID: m.CollectionID,
	}
	receivable := models.JournalLine{Account: models.AccountReceivable}

	var debit, credit models.JournalLine
	amount := m.Amount
	switch m.Kind {
	case models.JournalCapture:
		debit, credit = tenderLine(m), receivable
	case models.JournalRefund:
		debit, credit = receivable, tenderLine(m)
	case models.JournalStoreCreditIssue:
		debit = models.JournalLine{Account: models.AccountStoreCreditGranted}
		credit = models.JournalLine{Account: models.AccountStoreCredit, CustomerID: m.CustomerID}
	case models.JournalLoyaltyEarn:
		debit = models.JournalLine{Account: models.AccountLoyaltyGranted}
		credit = models.JournalLine{Account: models.AccountLoyalty, CustomerID: m.CustomerID}
	case models.JournalLoyaltyReverse:
		// The ledger stores a reverse as negative points; the entry moves the
		// same amount the other way.
		amount = -m.Amount
		debit = models.JournalLine{Account: models.AccountLoyalty, CustomerID: m.CustomerID}
		credit = models.JournalLine{Account: models.AccountLoyaltyGranted}
	default:
		return models.JournalEntry{}, errors.Internal(CodeInvalidInput,
			"the journal read a movement of an unknown kind %q (%s)", m.Kind, m.ID)
	}
	if amount <= 0 {
		return models.JournalEntry{}, errors.Internal(CodeInvalidInput,
			"the journal read a %s movement %s of %d; every movement it reads moves a positive amount",
			m.Kind, m.ID, m.Amount)
	}

	debit.Debit, credit.Credit = amount, amount
	entry.Lines = []models.JournalLine{debit, credit}

	return entry, nil
}

// tenderLine is the account the money of a capture came from, and a refund of
// it goes back to.
//
// A capture through one of the module's own tenders spends a balance the shop
// owes the customer, so it reduces that liability; every other provider holds
// money for the shop, one clearing line per provider.
func tenderLine(m *models.JournalMovement) models.JournalLine {
	switch m.ProviderID {
	case models.StoreCreditTenderID:
		return models.JournalLine{Account: models.AccountStoreCredit, CustomerID: m.CustomerID}
	case models.LoyaltyTenderID:
		return models.JournalLine{Account: models.AccountLoyalty, CustomerID: m.CustomerID}
	default:
		return models.JournalLine{Account: models.AccountProviderClearing, ProviderID: m.ProviderID}
	}
}

// trialBalance sums the entries per currency, account and provider, in a stable
// order: currency, then account, then provider.
func trialBalance(entries []models.JournalEntry) []models.JournalBalance {
	type key struct {
		currency string
		account  models.JournalAccount
		provider string
	}
	sums := map[key]*models.JournalBalance{}
	for i := range entries {
		for j := range entries[i].Lines {
			line := &entries[i].Lines[j]
			k := key{entries[i].CurrencyCode, line.Account, line.ProviderID}
			sum, ok := sums[k]
			if !ok {
				sum = &models.JournalBalance{
					CurrencyCode: k.currency, Account: k.account, ProviderID: k.provider,
				}
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
		return cmp.Or(cmp.Compare(a.CurrencyCode, b.CurrencyCode),
			cmp.Compare(a.Account, b.Account), cmp.Compare(a.ProviderID, b.ProviderID))
	})

	return out
}
