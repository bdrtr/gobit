package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// orderKinds are the facts that carry an order's amounts, and movementKinds
// every other fact the journal reads, with one it does not.
var (
	orderKinds    = []models.JournalKind{models.JournalOrderPlaced, models.JournalOrderCanceled}
	movementKinds = []models.JournalKind{
		models.JournalCreditLine, models.JournalReturnRefunded, models.JournalClaimRefunded,
		models.JournalDeliveryChanged, models.JournalDeliveryUpgraded, models.JournalExchangeFunded,
		models.JournalExchangeRefunded, "not_a_fact",
	}
	// correctionKinds are a document's two facts (ADR 0419), drawn over every
	// act kind a key can name and one it cannot, and over both document kinds
	// and one that is neither.
	correctionKinds = []models.JournalKind{models.JournalTaxCorrected, models.JournalTaxCorrectionVoided}
	correctedActs   = append(append([]models.JournalKind{}, documentedActs...),
		models.JournalExchangeRefunded, "not_an_act")
	documentKinds = []string{documentRefund, documentSale, "not_a_document"}
)

// drawFact draws a fact. Half are an order's, and each of an order's shapes is
// drawn as often as the others: admitted, an amount below zero, a total off its
// identity, and gift cards past the subtotal. The first draw let each amount
// decide its own sign, and a hundred facts did not reach the last shape: the
// gift card check could be deleted and the property still passed.
func drawFact(t *rapid.T) *models.JournalFact {
	f := &models.JournalFact{
		ID: "f", OrderID: "order_1", CurrencyCode: rapid.SampledFrom([]string{"TRY", "EUR"}).Draw(t, "currency"),
		OccurredAt: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC),
	}
	switch rapid.SampledFrom([]string{"order", "movement", "correction"}).Draw(t, "fact") {
	case "movement":
		f.Kind = rapid.SampledFrom(movementKinds).Draw(t, "kind")
		f.Amount = rapid.OneOf(rapid.Just(int64(0)), rapid.Int64Range(-5, -1), rapid.Int64Range(1, models.MaxTotal)).Draw(t, "amount")

		return f
	case "correction":
		f.Kind = rapid.SampledFrom(correctionKinds).Draw(t, "kind")
		f.ActKind = rapid.SampledFrom(correctedActs).Draw(t, "act")
		f.DocumentKind = rapid.SampledFrom(documentKinds).Draw(t, "document")
		f.Amount = rapid.OneOf(rapid.Just(int64(0)), rapid.Int64Range(-5, -1), rapid.Int64Range(1, models.MaxTotal)).Draw(t, "tax")

		return f
	}

	f.Kind = rapid.SampledFrom(orderKinds).Draw(t, "kind")
	amount := rapid.OneOf(rapid.Just(int64(0)), rapid.Int64Range(1, 5), rapid.Int64Range(0, models.MaxTotal))
	f.Subtotal = amount.Draw(t, "subtotal")
	f.DiscountTotal = amount.Draw(t, "discount")
	f.TaxTotal = amount.Draw(t, "tax")
	f.ShippingTotal = amount.Draw(t, "shipping")
	f.GiftCardSubtotal = rapid.Int64Range(0, f.Subtotal).Draw(t, "gift card part")
	f.Total = f.Subtotal - f.DiscountTotal + f.TaxTotal + f.ShippingTotal
	switch rapid.SampledFrom([]string{"admitted", "negative", "off identity", "gift cards over"}).Draw(t, "shape") {
	case "negative":
		*rapid.SampledFrom([]*int64{&f.Subtotal, &f.DiscountTotal, &f.TaxTotal, &f.ShippingTotal, &f.Total, &f.GiftCardSubtotal}).Draw(t, "which") = -rapid.Int64Range(1, 5).Draw(t, "below zero")
	case "off identity":
		f.Total += rapid.OneOf(rapid.Int64Range(-5, -1), rapid.Int64Range(1, 5)).Draw(t, "off by")
	case "gift cards over":
		f.GiftCardSubtotal = f.Subtotal + rapid.Int64Range(1, 5).Draw(t, "over by")
	}

	return f
}

// TestEveryJournalEntryBalancesOrIsRefused is ADR 0249 on the order journal:
// every fact the journal's rules admit becomes an entry whose debits equal its
// credits, with no empty line and no line both ways, a cancellation is its
// placement the other way, and a fact the rules do not admit is refused.
func TestEveryJournalEntryBalancesOrIsRefused(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		f := drawFact(t)

		entry, err := journalEntry(f)

		var admitted bool
		switch f.Kind {
		case models.JournalOrderPlaced, models.JournalOrderCanceled:
			admitted = f.Subtotal >= 0 && f.DiscountTotal >= 0 && f.TaxTotal >= 0 && f.ShippingTotal >= 0 &&
				f.Total >= 0 && f.GiftCardSubtotal >= 0 &&
				f.Total+f.DiscountTotal == f.Subtotal+f.TaxTotal+f.ShippingTotal &&
				f.GiftCardSubtotal <= f.Subtotal
		case "not_a_fact":
			admitted = false
		case models.JournalTaxCorrected, models.JournalTaxCorrectionVoided:
			_, gives := givenBackTo[f.ActKind]
			_, charges := chargedTo[f.ActKind]
			admitted = f.Amount >= 0 &&
				((f.DocumentKind == documentRefund && gives) || (f.DocumentKind == documentSale && charges))
		default:
			admitted = f.Amount > 0
		}
		if !admitted {
			require.Error(t, err)
			return
		}
		require.NoError(t, err)

		var debits, credits int64
		for _, line := range entry.Lines {
			require.GreaterOrEqual(t, line.Debit, int64(0))
			require.GreaterOrEqual(t, line.Credit, int64(0))
			require.True(t, (line.Debit == 0) != (line.Credit == 0), "a line moves one way: %+v", line)
			debits += line.Debit
			credits += line.Credit
		}
		require.Equal(t, debits, credits, "the entry balances")

		if f.Kind == models.JournalOrderPlaced || f.Kind == models.JournalOrderCanceled {
			mirror := *f
			mirror.Kind = models.JournalOrderCanceled
			if f.Kind == models.JournalOrderCanceled {
				mirror.Kind = models.JournalOrderPlaced
			}
			other, err := journalEntry(&mirror)
			require.NoError(t, err)
			require.Len(t, other.Lines, len(entry.Lines))
			for i := range entry.Lines {
				require.Equal(t, entry.Lines[i].Account, other.Lines[i].Account)
				require.Equal(t, entry.Lines[i].Debit, other.Lines[i].Credit, "the cancellation is the placement the other way")
			}
		}

		if f.Kind == models.JournalTaxCorrected || f.Kind == models.JournalTaxCorrectionVoided {
			mirror := *f
			mirror.Kind = models.JournalTaxCorrectionVoided
			if f.Kind == models.JournalTaxCorrectionVoided {
				mirror.Kind = models.JournalTaxCorrected
			}
			other, err := journalEntry(&mirror)
			require.NoError(t, err)
			require.Len(t, other.Lines, len(entry.Lines))
			for i := range entry.Lines {
				require.Equal(t, entry.Lines[i].Account, other.Lines[i].Account)
				require.Equal(t, entry.Lines[i].Debit, other.Lines[i].Credit, "the voiding is the correction the other way")
			}
			var taxPayable int64
			for _, line := range entry.Lines {
				if line.Account == models.AccountTaxPayable {
					taxPayable += line.Debit + line.Credit
				}
			}
			require.Equal(t, f.Amount, taxPayable, "a correction moves its whole tax through tax_payable")
		}
	})
}

// TestTheTrialBalanceBalancesInEveryCurrency holds the trial balance of any
// admitted entries to its entries: per currency its debits equal its credits,
// and every account's sums are the entries' own.
func TestTheTrialBalanceBalancesInEveryCurrency(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		var entries []models.JournalEntry
		for range rapid.IntRange(0, 12).Draw(t, "facts") {
			if entry, err := journalEntry(drawFact(t)); err == nil {
				entries = append(entries, entry)
			}
		}

		type key struct {
			currency string
			account  models.JournalAccount
		}
		want := map[key][2]int64{}
		for _, entry := range entries {
			for _, line := range entry.Lines {
				k := key{entry.CurrencyCode, line.Account}
				sums := want[k]
				want[k] = [2]int64{sums[0] + line.Debit, sums[1] + line.Credit}
			}
		}

		balance := trialBalance(entries)
		perCurrency := map[string][2]int64{}
		for _, row := range balance {
			require.Equal(t, want[key{row.CurrencyCode, row.Account}], [2]int64{row.Debit, row.Credit})
			sums := perCurrency[row.CurrencyCode]
			perCurrency[row.CurrencyCode] = [2]int64{sums[0] + row.Debit, sums[1] + row.Credit}
		}
		require.Len(t, balance, len(want))
		for currency, sums := range perCurrency {
			require.Equal(t, sums[0], sums[1], "the trial balance of %s balances", currency)
		}
	})
}
