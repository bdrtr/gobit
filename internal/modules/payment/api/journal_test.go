package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/payment/api"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// Journal records the query and returns the scripted journal.
func (f *fakePayments) Journal(_ context.Context, q service.JournalQuery) (service.Journal, error) {
	f.journalQueries = append(f.journalQueries, q)
	if f.err != nil {
		return service.Journal{}, f.err
	}

	return f.journal, nil
}

// journalGet sends a GET for the journal as a full administrator.
func journalGet(t *testing.T, svc *fakePayments, target string) *httptest.ResponseRecorder {
	t.Helper()

	r := chi.NewRouter()
	api.New(svc).Routes(r)
	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
		ID: "user_test", Kind: "user", Scopes: []string{corehttp.ScopeAdmin},
	}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// TestTheJournalEndpointReadsItsWindow passes the window and the currency the
// query names to the service, and answers with the entries and the balances.
func TestTheJournalEndpointReadsItsWindow(t *testing.T) {
	from := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 1, 0)
	svc := &fakePayments{journal: service.Journal{
		From: from, To: to,
		Entries: []models.JournalEntry{{ID: "pay_1", Kind: models.JournalCapture, CurrencyCode: "TRY",
			Lines: []models.JournalLine{
				{Account: models.AccountProviderClearing, ProviderID: "manual", Debit: 100},
				{Account: models.AccountReceivable, Credit: 100},
			}}},
		Balances: []models.JournalBalance{{CurrencyCode: "TRY", Account: models.AccountReceivable, Credit: 100}},
	}}

	rec := journalGet(t, svc,
		"/admin/v1/payment-journal?from=2026-09-01T00:00:00Z&to=2026-10-01T00:00:00Z&currency_code=try")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, svc.journalQueries, 1)
	assert.Equal(t, service.JournalQuery{From: from, To: to, CurrencyCode: "try"}, svc.journalQueries[0])

	var envelope struct {
		Data struct {
			Entries []struct {
				ID    string `json:"id"`
				Lines []struct {
					Account    string `json:"account"`
					ProviderID string `json:"provider_id"`
					Debit      int64  `json:"debit"`
					Credit     int64  `json:"credit"`
				} `json:"lines"`
			} `json:"entries"`
			Balances []struct {
				Account string `json:"account"`
				Credit  int64  `json:"credit"`
			} `json:"balances"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data.Entries, 1)
	assert.Equal(t, "provider_clearing", envelope.Data.Entries[0].Lines[0].Account)
	assert.Equal(t, "manual", envelope.Data.Entries[0].Lines[0].ProviderID)
	assert.Equal(t, int64(100), envelope.Data.Entries[0].Lines[1].Credit)
	require.Len(t, envelope.Data.Balances, 1)
	assert.Equal(t, "receivable", envelope.Data.Balances[0].Account)
}

// TestTheJournalNeedsAWindow refuses an open or unreadable end before the
// service is asked: an open end would read the whole history.
func TestTheJournalNeedsAWindow(t *testing.T) {
	for name, target := range map[string]string{
		"no from":     "/admin/v1/payment-journal?to=2026-10-01T00:00:00Z",
		"no to":       "/admin/v1/payment-journal?from=2026-09-01T00:00:00Z",
		"a bare date": "/admin/v1/payment-journal?from=2026-09-01&to=2026-10-01T00:00:00Z",
		"a word":      "/admin/v1/payment-journal?from=yesterday&to=2026-10-01T00:00:00Z",
	} {
		svc := &fakePayments{}

		rec := journalGet(t, svc, target)

		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "%s: %s", name, rec.Body.String())
		assert.Empty(t, svc.journalQueries, name)
	}
}
