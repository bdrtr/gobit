package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/tax/api"
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

// The trial tests' period: in the past of the tests' clock.
var (
	rateTrialFrom = testNow.Add(-48 * time.Hour)
	rateTrialTo   = testNow.Add(-time.Hour)
)

// rateTrialReport is a report as the cart flows give it.
const rateTrialReport = `{"tax_rate_id":"taxrate_x","change":{"rate_bps":900},` +
	`"from":"2026-08-22T10:00:00Z","to":"2026-08-24T09:00:00Z","assumptions":["todays_rates"],` +
	`"orders_read":3,"orders_canceled":1,"orders_region_rate":0,"orders_other_country":1,` +
	`"currencies":[{"currency_code":"TRY","orders_priced":1,"lines_priced":2,"lines_reached":2,` +
	`"lines_changed":1,"charged":4000,"baseline":4000,"trial":1800}],` +
	`"orders":[{"order_id":"order_1","display_id":7,"currency_code":"TRY",` +
	`"placed_at":"2026-08-23T10:00:00Z","charged":4000,"baseline":4000,"trial":1800}]}`

// recordingTrial is the cart flows' side of the trial: it records what it is
// asked and answers raw.
type recordingTrial struct {
	calls  int
	rateID string
	from   time.Time
	to     time.Time
	change string
	raw    string
}

// TrialTaxRateJSON records the question and answers the scripted report.
func (f *recordingTrial) TrialTaxRateJSON(
	_ context.Context, rateID string, from, to time.Time, change json.RawMessage,
) (json.RawMessage, error) {
	f.calls++
	f.rateID, f.from, f.to, f.change = rateID, from, to, string(change)

	return json.RawMessage(f.raw), nil
}

// orderReader may read the taxes and the orders.
var orderReader = corehttp.Principal{
	ID: "usr_reader", Kind: "user", Scopes: []string{api.ScopeRead, "order:read"},
}

// newTrialRouter builds a router whose trial runs on flow, with a Turkish
// default rate and a ruled rate carrying one product rule.
func newTrialRouter(t *testing.T, flow *recordingTrial) (r chi.Router, defaultID, ruledID, ruleID string) {
	t.Helper()

	repo := newMemRepo()
	svc := service.New(repo, service.Options{Now: func() time.Time { return testNow }})
	r = chi.NewRouter()
	trial := api.New(svc)
	if flow != nil {
		trial = trial.WithTrial(flow)
	}
	trial.Routes(r)

	regionID := createRegion(t, r, "TR")
	defaultID = createRate(t, r, `{"tax_region_id":"`+regionID+`","name":"VAT","rate_bps":2000,"is_default":true}`)
	ruledID = createRate(t, r, `{"tax_region_id":"`+regionID+`","name":"Reduced","rate_bps":1000}`)
	rec := do(t, r, http.MethodPost, "/admin/v1/tax-rates/"+ruledID+"/rules",
		`{"reference":"product","reference_id":"prod_1"}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	ruleID, _ = decodeItem(t, rec)["id"].(string)

	return r, defaultID, ruledID, ruleID
}

// trialURL is a trial's address with the period and the change.
func trialURL(rateID string, from, to time.Time, change url.Values) string {
	query := url.Values{}
	for name, values := range change {
		query[name] = values
	}
	if !from.IsZero() {
		query.Set("from", from.Format(time.RFC3339))
	}
	if !to.IsZero() {
		query.Set("to", to.Format(time.RFC3339))
	}

	return "/admin/v1/tax-rates/" + rateID + "/trial?" + query.Encode()
}

// TestARateTrialAnswersWhatTheFlowReports: the rate, the period and the
// change reach the flow as asked — a rule's id may carry colons of its own —
// and the flow's report is answered.
func TestARateTrialAnswersWhatTheFlowReports(t *testing.T) {
	flow := &recordingTrial{raw: rateTrialReport}
	r, _, ruledID, ruleID := newTrialRouter(t, flow)

	rec := doAs(t, r, orderReader, http.MethodGet, trialURL(ruledID, rateTrialFrom, rateTrialTo, url.Values{
		"rate_bps": {"900"}, "rule": {"product:prod:with:colons", "tax_class:taxclass_1"}, "drop_rule": {ruleID},
	}), "")

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, 1, flow.calls)
	assert.Equal(t, ruledID, flow.rateID)
	assert.True(t, rateTrialFrom.Equal(flow.from) && rateTrialTo.Equal(flow.to), "%s %s", flow.from, flow.to)
	assert.JSONEq(t, `{"rate_bps":900,"add_rules":[{"reference":"product","reference_id":"prod:with:colons"},`+
		`{"reference":"tax_class","reference_id":"taxclass_1"}],"drop_rules":["`+ruleID+`"]}`, flow.change)

	report := decodeItem(t, rec)
	assert.Equal(t, "taxrate_x", report["tax_rate_id"])
	assert.InDelta(t, 3, report["orders_read"], 0)
	orders, _ := report["orders"].([]any)
	require.Len(t, orders, 1)
}

// TestARateTrialNeedsOrderRead: the report is orders, so reading the taxes
// alone is refused before the flow is asked.
func TestARateTrialNeedsOrderRead(t *testing.T) {
	flow := &recordingTrial{raw: rateTrialReport}
	r, defaultID, _, _ := newTrialRouter(t, flow)
	path := trialURL(defaultID, rateTrialFrom, rateTrialTo, url.Values{"rate_bps": {"1800"}})

	rec := doAs(t, r, readOnlyPrincipal, http.MethodGet, path, "")
	assert.Equal(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())
	ordersOnly := corehttp.Principal{ID: "usr_orders", Kind: "user", Scopes: []string{"order:read"}}
	rec = doAs(t, r, ordersOnly, http.MethodGet, path, "")
	assert.Equal(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())
	assert.Zero(t, flow.calls)

	rec = doAs(t, r, orderReader, http.MethodGet, path, "")
	assert.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
}

// TestARateTrialNeedsAPastPeriod: both ends are required RFC 3339 moments and
// the end is not in the future.
func TestARateTrialNeedsAPastPeriod(t *testing.T) {
	flow := &recordingTrial{raw: rateTrialReport}
	r, defaultID, _, _ := newTrialRouter(t, flow)
	change := url.Values{"rate_bps": {"1800"}}

	for name, path := range map[string]string{
		"no start":  trialURL(defaultID, time.Time{}, rateTrialTo, change),
		"no end":    trialURL(defaultID, rateTrialFrom, time.Time{}, change),
		"malformed": "/admin/v1/tax-rates/" + defaultID + "/trial?rate_bps=1800&from=yesterday&to=" + rateTrialTo.Format(time.RFC3339),
		"future":    trialURL(defaultID, rateTrialFrom, testNow.Add(time.Minute), change),
	} {
		rec := doAs(t, r, orderReader, http.MethodGet, path, "")
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "%s: %s", name, rec.Body.String())
		assert.Equal(t, service.CodeTrialInvalidPeriod, errorCode(t, rec), name)
	}
	assert.Zero(t, flow.calls)
}

// TestARateTrialNeedsAChange: a trial without a change, with a malformed rate
// or rule, or with the rate given twice is refused.
func TestARateTrialNeedsAChange(t *testing.T) {
	flow := &recordingTrial{raw: rateTrialReport}
	r, defaultID, _, _ := newTrialRouter(t, flow)

	for name, change := range map[string]url.Values{
		"none":          {},
		"not a number":  {"rate_bps": {"18%"}},
		"twice":         {"rate_bps": {"1800", "1700"}},
		"no colon":      {"rule": {"product"}},
		"out of int32":  {"rate_bps": {"99999999999"}},
		"shipping rule": {"rule": {"shipping_option:so_1"}},
	} {
		rec := doAs(t, r, orderReader, http.MethodGet, trialURL(defaultID, rateTrialFrom, rateTrialTo, change), "")
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "%s: %s", name, rec.Body.String())
		assert.Equal(t, service.CodeTrialInvalidChange, errorCode(t, rec), name)
	}
	assert.Zero(t, flow.calls)
}

// TestARateTrialIsRefusedBeforeTheOrdersAreRead: a change the write path
// refuses and a rate that does not exist are answered without asking the flow.
func TestARateTrialIsRefusedBeforeTheOrdersAreRead(t *testing.T) {
	flow := &recordingTrial{raw: rateTrialReport}
	r, defaultID, _, _ := newTrialRouter(t, flow)

	rec := doAs(t, r, orderReader, http.MethodGet,
		trialURL(defaultID, rateTrialFrom, rateTrialTo, url.Values{"rule": {"product:prod_2"}}), "")
	assert.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, service.CodeTrialChangeRefused, errorCode(t, rec))

	rec = doAs(t, r, orderReader, http.MethodGet,
		trialURL("taxrate_01ZZZZZZZZZZZZZZZZZZZZZZZZ", rateTrialFrom, rateTrialTo, url.Values{"rate_bps": {"900"}}), "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
	assert.Zero(t, flow.calls)
}

// TestARateTrialAnswersOnlyWhatItCanRead: a report carrying a field this
// endpoint does not publish is refused rather than passed on, and a trial with
// no flow bound is a server fault.
func TestARateTrialAnswersOnlyWhatItCanRead(t *testing.T) {
	flow := &recordingTrial{raw: `{"tax_rate_id":"taxrate_x","margin":12}`}
	r, defaultID, _, _ := newTrialRouter(t, flow)
	path := trialURL(defaultID, rateTrialFrom, rateTrialTo, url.Values{"rate_bps": {"1800"}})

	rec := doAs(t, r, orderReader, http.MethodGet, path, "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "tax_trial_answer_invalid", errorCode(t, rec))

	unbound, unboundDefault, _, _ := newTrialRouter(t, nil)
	rec = doAs(t, unbound, orderReader, http.MethodGet,
		trialURL(unboundDefault, rateTrialFrom, rateTrialTo, url.Values{"rate_bps": {"1800"}}), "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, service.CodeTrialUnavailable, errorCode(t, rec))
}
