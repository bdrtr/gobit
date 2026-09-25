package api

import (
	"time"

	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// fullJournal is a journal answer with every field written, the omitempty ones
// included, so the described schema is held to every key the handler can send.
func fullJournal() journalDTO {
	moment := time.Now().UTC()

	return journalDTO{
		From: moment, To: moment, CurrencyCode: "TRY",
		Entries: []models.JournalEntry{{
			ID: "pay_1", Kind: models.JournalCapture, OccurredAt: moment,
			CurrencyCode: "TRY", CollectionID: "paycol_1",
			Lines: []models.JournalLine{
				{Account: models.AccountStoreCredit, CustomerID: "cus_1", Debit: 100},
				{Account: models.AccountProviderClearing, ProviderID: "manual", Credit: 100},
			},
		}},
		Balances: []models.JournalBalance{{
			CurrencyCode: "TRY", Account: models.AccountProviderClearing, ProviderID: "manual",
			Debit: 100, Credit: 100,
		}},
	}
}
