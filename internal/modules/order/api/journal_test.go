package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// Journal records the query and returns the scripted journal.
func (f *fakeOrders) Journal(_ context.Context, q service.JournalQuery) (service.Journal, error) {
	f.journalQueries = append(f.journalQueries, q)
	if f.err != nil {
		return service.Journal{}, f.err
	}

	return f.journal, nil
}

// TestTheOrderJournalEndpointReadsItsWindow passes the window and the currency
// to the service and answers with the entries and the balances.
func TestTheOrderJournalEndpointReadsItsWindow(t *testing.T) {
	from := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)
	svc := &fakeOrders{journal: service.Journal{
		From: from, To: to,
		Entries: []models.JournalEntry{{ID: "ocl_1", Kind: models.JournalCreditLine, OrderID: "order_1",
			CurrencyCode: "TRY", Lines: []models.JournalLine{
				{Account: models.AccountCreditAllowances, Debit: 700},
				{Account: models.AccountReceivable, Credit: 700},
			}}},
		Balances: []models.JournalBalance{{CurrencyCode: "TRY", Account: models.AccountReceivable, Credit: 700}},
	}}

	rec := doRequest(t, newRouter(svc), http.MethodGet,
		"/admin/v1/order-journal?from=2026-09-01T00:00:00Z&to=2026-10-01T00:00:00Z&currency_code=try", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, svc.journalQueries, 1)
	assert.Equal(t, service.JournalQuery{From: from, To: to, CurrencyCode: "try"}, svc.journalQueries[0])

	var envelope struct {
		Data struct {
			Entries []struct {
				OrderID string `json:"order_id"`
				Lines   []struct {
					Account string `json:"account"`
					Debit   int64  `json:"debit"`
				} `json:"lines"`
			} `json:"entries"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data.Entries, 1)
	assert.Equal(t, "order_1", envelope.Data.Entries[0].OrderID)
	assert.Equal(t, "credit_allowances", envelope.Data.Entries[0].Lines[0].Account)
}

// TestTheOrderJournalNeedsAWindow refuses an open or unreadable end before the
// service is asked.
func TestTheOrderJournalNeedsAWindow(t *testing.T) {
	for name, target := range map[string]string{
		"no from":     "/admin/v1/order-journal?to=2026-10-01T00:00:00Z",
		"no to":       "/admin/v1/order-journal?from=2026-09-01T00:00:00Z",
		"a bare date": "/admin/v1/order-journal?from=2026-09-01&to=2026-10-01T00:00:00Z",
	} {
		svc := &fakeOrders{}

		rec := doRequest(t, newRouter(svc), http.MethodGet, target, "")

		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "%s: %s", name, rec.Body.String())
		assert.Empty(t, svc.journalQueries, name)
	}
}
