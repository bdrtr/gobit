package api

import (
	"time"

	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// fullOrderJournal is a journal answer with every field written, the omitempty
// one included, so the described schema is held to every key the handler sends.
func fullOrderJournal() orderJournalDTO {
	moment := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)

	return orderJournalDTO{
		From: moment, To: moment, CurrencyCode: "TRY",
		Entries: []models.JournalEntry{{
			ID: "order_1", Kind: models.JournalOrderPlaced, OrderID: "order_1",
			OccurredAt: moment, CurrencyCode: "TRY",
			Lines: []models.JournalLine{
				{Account: models.AccountReceivable, Debit: 100},
				{Account: models.AccountSales, Credit: 100},
			},
		}},
		Balances: []models.JournalBalance{{
			CurrencyCode: "TRY", Account: models.AccountReceivable, Debit: 100,
		}},
	}
}
