package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/pricing/api"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// trialReport is a report the flow could return.
const trialReport = `{"price_list_id":"plist_1","from":"2026-05-01T00:00:00Z","to":"2026-06-01T00:00:00Z",` +
	`"assumptions":["todays_prices"],"orders_read":3,"orders_canceled":1,"lines_unpriced":1,` +
	`"currencies":[{"currency_code":"TRY","orders_priced":2,"lines_priced":4,"lines_changed":2,` +
	`"charged":5000,"baseline":5200,"trial":4700}],` +
	`"orders":[{"order_id":"order_1","display_id":7,"currency_code":"TRY",` +
	`"placed_at":"2026-05-02T10:00:00Z","baseline":3000,"trial":2500}]}`

// trialPath is the trial of plist_1 over May.
const trialPath = "/admin/v1/price-lists/plist_1/trial?from=2026-05-01T00:00:00Z&to=2026-06-01T03:00:00%2B03:00"

// fakeTrial records what it was asked and answers what it holds.
type fakeTrial struct {
	answer   string
	err      error
	calls    int
	listID   string
	from, to time.Time
}

func (f *fakeTrial) TrialPriceListJSON(_ context.Context, listID string, from, to time.Time) (json.RawMessage, error) {
	f.calls++
	f.listID, f.from, f.to = listID, from, to
	if f.err != nil {
		return nil, f.err
	}
	return json.RawMessage(f.answer), nil
}

// newTrialRouter mounts the API with the trial bound, or unbound for nil.
func newTrialRouter(trial *fakeTrial) chi.Router {
	svc := service.New(newMemRepo(), service.Options{Now: func() time.Time { return testNow }})
	a := api.New(svc)
	if trial != nil {
		a = a.WithTrial(trial)
	}
	r := chi.NewRouter()
	a.Routes(r)
	return r
}

// trialAs asks for a trial as a caller holding the scopes.
func trialAs(r chi.Router, path string, scopes ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, strings.NewReader(""))
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
		ID: "usr_trial", Kind: "user", Scopes: scopes,
	}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// TestAListTrialAnswersWhatTheFlowReports is ADR 0220: the list and the period
// reach the flow, the moments read in their own zones, and the report is
// answered as the flow wrote it.
func TestAListTrialAnswersWhatTheFlowReports(t *testing.T) {
	trial := &fakeTrial{answer: trialReport}
	r := newTrialRouter(trial)

	rec := trialAs(r, trialPath, api.ScopeRead, "order:read")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "plist_1", trial.listID)
	assert.True(t, trial.from.Equal(time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)))
	assert.True(t, trial.to.Equal(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)), "the zone read, not dropped")

	data := decodeItem(t, rec)
	assert.InDelta(t, 3, data["orders_read"], 0)
	currencies, ok := data["currencies"].([]any)
	require.True(t, ok)
	require.Len(t, currencies, 1)
	sum, ok := currencies[0].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 5200, sum["baseline"], 0)
	assert.InDelta(t, 4700, sum["trial"], 0)
	assert.InDelta(t, 5000, sum["charged"], 0)
	orders, ok := data["orders"].([]any)
	require.True(t, ok)
	assert.Len(t, orders, 1)
}

// TestAListTrialNeedsOrderRead: the report is orders, so pricing's own read is
// not enough.
func TestAListTrialNeedsOrderRead(t *testing.T) {
	trial := &fakeTrial{answer: trialReport}
	r := newTrialRouter(trial)

	assert.Equal(t, http.StatusForbidden, trialAs(r, trialPath, api.ScopeRead).Code)
	assert.Equal(t, http.StatusForbidden, trialAs(r, trialPath, "order:read").Code)
	assert.Zero(t, trial.calls, "a refused caller reads no order")
	assert.Equal(t, http.StatusOK, trialAs(r, trialPath, api.ScopeRead, "order:read").Code)
}

// TestAListTrialNeedsAPastPeriod: a missing, malformed or zoneless end, and an
// end in the future, are refused before the flow is asked.
func TestAListTrialNeedsAPastPeriod(t *testing.T) {
	trial := &fakeTrial{answer: trialReport}
	r := newTrialRouter(trial)
	base := "/admin/v1/price-lists/plist_1/trial"
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)

	for name, query := range map[string]string{
		"no from":    "?to=2026-06-01T00:00:00Z",
		"no to":      "?from=2026-05-01T00:00:00Z",
		"a date":     "?from=2026-05-01&to=2026-06-01T00:00:00Z",
		"no zone":    "?from=2026-05-01T00:00:00Z&to=2026-06-01T00:00:00",
		"the future": "?from=2026-05-01T00:00:00Z&to=" + future,
	} {
		rec := trialAs(r, base+query, corehttp.ScopeAdmin)
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, name)
		assert.Equal(t, "pricing_trial_invalid_period", errorCode(t, rec), name)
	}
	assert.Zero(t, trial.calls)
}

// TestAListTrialAnswersOnlyWhatItCanRead: an unbound flow, a flow failure and
// a report with a field this endpoint does not publish are errors, not a
// partial report.
func TestAListTrialAnswersOnlyWhatItCanRead(t *testing.T) {
	rec := trialAs(newTrialRouter(nil), trialPath, corehttp.ScopeAdmin)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "pricing_trial_unavailable", errorCode(t, rec))

	failing := &fakeTrial{err: coreerrors.NotFound("pricing_price_list_not_found", "no list plist_1")}
	rec = trialAs(newTrialRouter(failing), trialPath, corehttp.ScopeAdmin)
	assert.Equal(t, http.StatusNotFound, rec.Code, "the flow's own failure, as it was")
	assert.Equal(t, "pricing_price_list_not_found", errorCode(t, rec))

	extra := &fakeTrial{answer: strings.Replace(trialReport, `"orders_read":3`, `"orders_read":3,"margin":9`, 1)}
	rec = trialAs(newTrialRouter(extra), trialPath, corehttp.ScopeAdmin)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "pricing_trial_answer_invalid", errorCode(t, rec))
	assert.NotContains(t, rec.Body.String(), "margin")
}
