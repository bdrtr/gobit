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
	"github.com/bdrtr/gobit/internal/modules/promotion/api"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// fakeTrial is a scripted trial flow that records what it was asked.
type fakeTrial struct {
	answer  json.RawMessage
	calls   int
	gotID   string
	gotFrom time.Time
	gotTo   time.Time
}

// TrialPromotionJSON records the question and returns the scripted answer.
func (f *fakeTrial) TrialPromotionJSON(
	_ context.Context, promotionID string, from, to time.Time,
) (json.RawMessage, error) {
	f.calls++
	f.gotID, f.gotFrom, f.gotTo = promotionID, from, to

	return f.answer, nil
}

// trialOperator holds both privileges the trial asks for.
var trialOperator = corehttp.Principal{
	ID:     "usr_trial",
	Kind:   "user",
	Scopes: []string{api.ScopeRead, "order:read"},
}

// trialRouter mounts the API with the given trial flow; nil binds none.
func trialRouter(t *testing.T, trial api.PromotionTrial) chi.Router {
	t.Helper()

	svc := service.New(newMemRepo(), service.Options{Now: func() time.Time { return testNow }})
	r := chi.NewRouter()
	a := api.New(svc)
	if trial != nil {
		a = a.WithTrial(trial)
	}
	a.Routes(r)

	return r
}

// askTrial sends the trial request as the given principal.
func askTrial(t *testing.T, r chi.Router, principal corehttp.Principal, query string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/promotions/promo_1/trial?"+query, http.NoBody)
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// TestTheTrialEndpointHandsThePeriodToTheFlow verifies the handler's own part:
// the two moments are read, the flow is asked, and its report is published.
func TestTheTrialEndpointHandsThePeriodToTheFlow(t *testing.T) {
	trial := &fakeTrial{answer: json.RawMessage(`{"promotion_id":"promo_1","from":"2026-08-01T00:00:00Z",` +
		`"to":"2026-08-02T00:00:00Z","assumptions":["active"],"orders_read":1,"orders_canceled":0,` +
		`"orders_already_discounted":0,"skipped":{},"currencies":[],"orders":[]}`)}
	r := trialRouter(t, trial)

	rec := askTrial(t, r, trialOperator, "from=2026-08-01T00:00:00Z&to=2026-08-02T00:00:00%2B03:00")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, 1, trial.calls)
	assert.Equal(t, "promo_1", trial.gotID)
	assert.True(t, trial.gotFrom.Equal(time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)))
	assert.True(t, trial.gotTo.Equal(time.Date(2026, 8, 1, 21, 0, 0, 0, time.UTC)),
		"the zone the operator wrote is kept: midnight in +03:00 is 21:00 UTC")
	assert.Contains(t, rec.Body.String(), `"orders_read":1`)
}

// TestTheTrialEndpointRefusesWhatItCannotAnswer verifies the refusals the
// handler owns, and that none of them reaches the flow.
func TestTheTrialEndpointRefusesWhatItCannotAnswer(t *testing.T) {
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)

	for name, tc := range map[string]struct {
		principal corehttp.Principal
		query     string
		status    int
	}{
		"no from":      {trialOperator, "to=2026-08-02T00:00:00Z", http.StatusUnprocessableEntity},
		"no zone":      {trialOperator, "from=2026-08-01T00:00:00&to=2026-08-02T00:00:00Z", http.StatusUnprocessableEntity},
		"a future end": {trialOperator, "from=2026-08-01T00:00:00Z&to=" + future, http.StatusUnprocessableEntity},
		"no order read": {corehttp.Principal{ID: "usr_p", Kind: "user", Scopes: []string{api.ScopeRead}},
			"from=2026-08-01T00:00:00Z&to=2026-08-02T00:00:00Z", http.StatusForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			trial := &fakeTrial{}
			rec := askTrial(t, trialRouter(t, trial), tc.principal, tc.query)

			assert.Equal(t, tc.status, rec.Code, rec.Body.String())
			assert.Zero(t, trial.calls, "the flow was asked although the request was refused")
		})
	}
}

// TestTheTrialEndpointPublishesOnlyItsOwnContract verifies that a field the
// flow sends and this endpoint does not declare fails the request instead of
// reaching a client unannounced.
func TestTheTrialEndpointPublishesOnlyItsOwnContract(t *testing.T) {
	trial := &fakeTrial{answer: json.RawMessage(`{"promotion_id":"promo_1","margin":12}`)}

	rec := askTrial(t, trialRouter(t, trial), trialOperator, "from=2026-08-01T00:00:00Z&to=2026-08-02T00:00:00Z")

	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "margin")
}

// TestTheTrialEndpointWithNoFlowIsAServerError verifies that an API with no flow
// bound refuses rather than answering with an empty report.
func TestTheTrialEndpointWithNoFlowIsAServerError(t *testing.T) {
	rec := askTrial(t, trialRouter(t, nil), trialOperator, "from=2026-08-01T00:00:00Z&to=2026-08-02T00:00:00Z")

	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
}
