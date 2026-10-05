package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // a zone that changes its clock, whatever the machine carries

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// trialCall is one trial a fake surface was asked.
type trialCall struct {
	id       string
	from, to time.Time
	change   string
}

// fakeTrials answers every trial with its scripted report or refusal and
// records what it was asked.
type fakeTrials struct {
	report string
	err    error
	calls  []trialCall
}

func (f *fakeTrials) answer(id string, from, to time.Time, change json.RawMessage) (json.RawMessage, error) {
	f.calls = append(f.calls, trialCall{id: id, from: from, to: to, change: string(change)})
	return json.RawMessage(f.report), f.err
}

func (f *fakeTrials) TrialPromotionJSON(_ context.Context, id string, from, to time.Time) (json.RawMessage, error) {
	return f.answer(id, from, to, nil)
}

func (f *fakeTrials) TrialPriceListJSON(_ context.Context, id string, from, to time.Time) (json.RawMessage, error) {
	return f.answer(id, from, to, nil)
}

func (f *fakeTrials) TrialTaxRateJSON(
	_ context.Context, id string, from, to time.Time, change json.RawMessage,
) (json.RawMessage, error) {
	return f.answer(id, from, to, change)
}

// The three reports as the cart flows write them, each naming order #1042.
const (
	promotionTrialBody = `{"promotion_id":"promo_1","from":"2026-09-01T00:00:00Z","to":"2026-10-01T00:00:00Z",
		"assumptions":["todays_rules"],"orders_read":7,"orders_canceled":1,"orders_already_discounted":2,
		"skipped":{"no_match":3},"currencies":[{"currency_code":"TRY","orders_priced":4,"orders_discounted":1,
		"subtotal":2000000,"discount_total":5000,"trial_discount_total":12550}],
		"orders":[{"order_id":"order_a","display_id":1042,"currency_code":"TRY","placed_at":"2026-09-03T12:00:00Z",
		"subtotal":2000000,"discount_total":5000,"trial_discount":12550}]}`
	priceListTrialBody = `{"price_list_id":"plist_1","from":"2026-09-01T00:00:00Z","to":"2026-10-01T00:00:00Z",
		"assumptions":["todays_ladder"],"orders_read":5,"orders_canceled":0,"lines_unpriced":2,
		"currencies":[{"currency_code":"TRY","orders_priced":5,"lines_priced":9,"lines_changed":3,
		"charged":900000,"baseline":880000,"trial":850000}],
		"orders":[{"order_id":"order_a","display_id":1042,"currency_code":"TRY","placed_at":"2026-09-03T12:00:00Z",
		"baseline":40000,"trial":30000}]}`
	taxRateTrialBody = `{"tax_rate_id":"taxrate_red","change":{"rate_bps":800,"add_rules":[{"reference":"product","reference_id":"prod_9"}]},
		"from":"2026-09-01T00:00:00Z","to":"2026-10-01T00:00:00Z","assumptions":["todays_rates"],
		"orders_read":6,"orders_canceled":1,"orders_region_rate":0,"orders_other_country":2,
		"currencies":[{"currency_code":"TRY","orders_priced":3,"lines_priced":4,"lines_reached":2,"lines_changed":2,
		"charged":6600,"baseline":6000,"trial":2400}],
		"orders":[{"order_id":"order_a","display_id":1042,"currency_code":"TRY","placed_at":"2026-09-03T12:00:00Z",
		"charged":4400,"baseline":4000,"trial":1600}]}`
)

// trialTaxes is a panel over four tax regions — a country computing its own
// tax with a default and a ruled rate, one of its provinces, a country under
// an external provider and one naming the module's own — whose trials the
// fake answers.
func trialTaxes(t *testing.T, trials *fakeTrials) *UI {
	t.Helper()

	panel, catalog := taxesPanel(t,
		query.Record{fieldID: "taxreg_tr", fieldTaxCountry: "TR", fieldTaxProvince: nil, fieldTaxProvider: "",
			fieldTaxRates: []map[string]any{
				{"id": "taxrate_vat", "name": "VAT", "code": "", "rate_bps": int32(2000), "is_default": true},
				{"id": "taxrate_red", "name": "Reduced", "code": "", "rate_bps": int32(1000), "is_default": false},
			}},
		query.Record{fieldID: "taxreg_34", fieldTaxCountry: "TR", fieldTaxProvince: "TR-34", fieldTaxProvider: "",
			fieldTaxRates: []map[string]any{
				{"id": "taxrate_ist", "name": "City", "code": "", "rate_bps": int32(100), "is_default": false},
			}},
		query.Record{fieldID: "taxreg_de", fieldTaxCountry: "DE", fieldTaxProvince: nil, fieldTaxProvider: "tax_ext",
			fieldTaxRates: []map[string]any{
				{"id": "taxrate_de", "name": "MwSt", "code": "", "rate_bps": int32(1900), "is_default": true},
			}},
		query.Record{fieldID: "taxreg_fr", fieldTaxCountry: "FR", fieldTaxProvince: nil, fieldTaxProvider: "local",
			fieldTaxRates: []map[string]any{
				{"id": "taxrate_fr", "name": "TVA", "code": "", "rate_bps": int32(2000), "is_default": true},
			}},
	)
	catalog.byEntity[EntityRegion] = []query.Record{currencyRecord("TRY", 2)}
	if trials != nil {
		panel.taxTrials = trials
	}

	return panel
}

// trialForm is the trial form of the rate its action names, on the Taxes
// screen.
func trialForm(t *testing.T, body, rateID string) string {
	t.Helper()

	at := strings.Index(body, `action="`+TaxesPath+`/rates/`+rateID+`/trial"`)
	require.GreaterOrEqual(t, at, 0, "the page holds a trial form for %s", rateID)
	end := strings.Index(body[at:], "</form>")
	require.GreaterOrEqual(t, end, 0)

	return body[at : at+end]
}

// day is midnight of the date in the server's zone, as the screen reads one.
func day(t *testing.T, text string) time.Time {
	t.Helper()

	at, err := time.ParseInLocation(dayLayout, text, time.Now().Location())
	require.NoError(t, err)

	return at
}

// TestATaxRateIsTriedFromTheTaxesScreen is ADR 0395: a rate on the Taxes
// screen offers its trial form to an operator who may read the orders; the
// trial asks the tax module's surface with the period from the first day's
// midnight to the one after the last, the value typed as a percent in basis
// points and the rule typed, and draws the report: the counts, the sums per
// currency with the change's effect, the orders linked to their pages, what
// was tried and what was assumed.
func TestATaxRateIsTriedFromTheTaxesScreen(t *testing.T) {
	t.Parallel()

	trials := &fakeTrials{report: taxRateTrialBody}
	panel := trialTaxes(t, trials)
	reader := []string{scopeTaxRead, scopeOrderRead}

	today := time.Now()
	rec := campaignsRequest(panel, http.MethodGet, TaxesPath, nil, reader...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	form := trialForm(t, rec.Body.String(), "taxrate_red")
	assert.Contains(t, form, `name="from" value="`+today.AddDate(0, 0, -(salesWindowDays-1)).Format(dayLayout)+`"`,
		"the form offers the last thirty days")
	assert.Contains(t, form, `name="to" value="`+today.Format(dayLayout)+`"`)
	assert.Contains(t, form, `name="rate" value="10"`, "the rate as it is, to change")
	assert.Contains(t, form, `name="rule_id"`)
	assert.Contains(t, form, `<option value="product">product</option>`)
	assert.NotContains(t, form, `value="shipping_option"`, "the cart taxes no shipping")

	rec = campaignsRequest(panel, http.MethodGet, TaxesPath+"/rates/taxrate_red/trial?"+url.Values{
		paramTrialFrom: {"2026-09-01"}, paramTrialTo: {"2026-09-30"}, paramTrialRate: {" 8.5 "},
		paramTrialRuleReference: {"product"}, paramTrialRuleID: {" prod_9 "},
	}.Encode(), nil, reader...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, trials.calls, 1)
	call := trials.calls[0]
	assert.Equal(t, "taxrate_red", call.id)
	assert.Equal(t, day(t, "2026-09-01"), call.from)
	assert.Equal(t, day(t, "2026-10-01"), call.to, "the last day is included: the period ends the midnight after it")
	assert.JSONEq(t, `{"rate_bps":850,"add_rules":[{"reference":"product","reference_id":"prod_9"}]}`, call.change,
		"the value typed as a percent reaches the surface in basis points, with the rule")

	body := rec.Body.String()
	for _, want := range []string{
		"Tried at 8%, with a rule on product prod_9.",
		"6 orders read", "1 canceled, left out", "2 in another country, left out",
		"<td>TRY</td><td>3</td><td>4</td><td>2</td><td>2</td><td>66.00 TRY</td><td>60.00 TRY</td><td>24.00 TRY</td><td>-36.00 TRY</td>",
		`<a href="` + OrdersPath + `/order_a">#1042</a>`, "<td>44.00 TRY</td><td>40.00 TRY</td><td>16.00 TRY</td><td>-24.00 TRY</td>",
		"Assumed: todays_rates.",
		`name="rate" value="8.5"`, `name="rule_id" value="prod_9"`, `<option value="product" selected>`,
	} {
		assert.Contains(t, body, want)
	}

	rec = campaignsRequest(panel, http.MethodGet, TaxesPath+"/rates/taxrate_red/trial?"+url.Values{
		paramTrialFrom: {"2026-09-01"}, paramTrialTo: {"2026-09-30"}, paramTrialRate: {""}, paramTrialRuleID: {""},
	}.Encode(), nil, reader...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{}`, trials.calls[1].change, "nothing typed tries nothing; the module says so")
}

// TestATrialNeedsOrderRead: a trial's report is orders, so an operator who
// may read the subject and not the orders is offered no trial and refused one
// with 403, and the surface is not asked.
func TestATrialNeedsOrderRead(t *testing.T) {
	t.Parallel()

	trials := &fakeTrials{report: taxRateTrialBody}
	panel := trialTaxes(t, trials)
	panel.promotions = &fakePromotionReader{page: fullPromotion}
	panel.promotionTrials = trials
	lists := &fakePriceLists{body: twoLists, total: 2}
	panel.prices = lists
	panel.priceListTrials = trials

	period := "?" + url.Values{paramTrialFrom: {"2026-09-01"}, paramTrialTo: {"2026-09-30"}}.Encode()
	for _, screen := range []struct{ list, trial, scope string }{
		{TaxesPath, TaxesPath + "/rates/taxrate_red/trial", scopeTaxRead},
		{PromotionsPath + "/promo_1", PromotionsPath + "/promo_1/trial", scopePromotionRead},
		{PriceListsPath, PriceListsPath + "/plist_1/trial", scopePricingRead},
	} {
		rec := campaignsRequest(panel, http.MethodGet, screen.list, nil, screen.scope)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.NotContains(t, rec.Body.String(), "/trial", "%s: no trial is offered without order:read", screen.list)
		rec = campaignsRequest(panel, http.MethodGet, screen.list, nil, screen.scope, scopeOrderRead)
		assert.Contains(t, rec.Body.String(), screen.trial, "%s: the trial is offered with order:read", screen.list)

		rec = campaignsRequest(panel, http.MethodGet, screen.trial+period, nil, screen.scope)
		assert.Equal(t, http.StatusForbidden, rec.Code, screen.trial)
		assert.Contains(t, rec.Body.String(), "order:read")
		rec = campaignsRequest(panel, http.MethodGet, screen.trial+period, nil, scopeOrderRead)
		assert.Equal(t, http.StatusForbidden, rec.Code, "%s: the route asks for its module's read", screen.trial)
	}
	assert.Empty(t, trials.calls, "a refused trial asks no surface")
}

// TestATrialPeriodEndsNow: a period whose last day is today ends now, since a
// trial reads orders already placed, and a form drawn without a period runs
// nothing and offers the last thirty days; a period with one day sent is
// refused with the form as typed, not read as the thirty days.
func TestATrialPeriodEndsNow(t *testing.T) {
	t.Parallel()

	trials := &fakeTrials{report: promotionTrialBody}
	panel := newCatalogPanel(t, &fakeCatalog{})
	panel.promotionTrials = trials
	panel.scopes = builtInScopes()
	reader := []string{scopePromotionRead, scopeOrderRead}

	rec := campaignsRequest(panel, http.MethodGet, PromotionsPath+"/promo_1/trial", nil, reader...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, trials.calls, "no period, no trial")
	today := time.Now()
	assert.Contains(t, rec.Body.String(), `name="to" value="`+today.Format(dayLayout)+`"`)
	assert.Contains(t, rec.Body.String(), `name="from" value="`+today.AddDate(0, 0, -(salesWindowDays-1)).Format(dayLayout)+`"`)

	before := time.Now()
	rec = campaignsRequest(panel, http.MethodGet, PromotionsPath+"/promo_1/trial?"+url.Values{
		paramTrialFrom: {before.AddDate(0, 0, -3).Format(dayLayout)}, paramTrialTo: {before.Format(dayLayout)},
	}.Encode(), nil, reader...)
	after := time.Now()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, trials.calls, 1)
	assert.False(t, trials.calls[0].to.Before(before), "the period ends now")
	assert.False(t, trials.calls[0].to.After(after), "and not at the midnight after today")
	assert.Equal(t, "promo_1", trials.calls[0].id)

	rec = campaignsRequest(panel, http.MethodGet, PromotionsPath+"/promo_1/trial?"+url.Values{
		paramTrialFrom: {"2026-09-01"}, paramTrialTo: {"last week"},
	}.Encode(), nil, reader...)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "The period could not be read")
	assert.Len(t, trials.calls, 1, "an unreadable period asks no surface")

	rec = campaignsRequest(panel, http.MethodGet, PromotionsPath+"/promo_1/trial?"+url.Values{
		paramTrialFrom: {"2026-09-01"},
	}.Encode(), nil, reader...)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "one day sent is not the default period")
	assert.Contains(t, rec.Body.String(), "The period could not be read")
	assert.Contains(t, rec.Body.String(), `name="from" value="2026-09-01"`, "the form as typed")
	assert.Len(t, trials.calls, 1, "half a period asks no surface")
}

// TestATrialPeriodIsBoundInDays: the bound is the days the form offers, the
// last one included. A period of TrialMaxDays days across the autumn change of
// a zone's clock is an hour longer than the flows' bound in time, and it is
// run with its start moved by that hour; a day more is refused at the form
// with the days counted, and no surface is asked.
func TestATrialPeriodIsBoundInDays(t *testing.T) {
	t.Parallel()

	berlin, err := time.LoadLocation("Europe/Berlin")
	require.NoError(t, err)
	now := time.Date(2026, 12, 1, 12, 0, 0, 0, berlin)
	widest := TrialMaxDays * 24 * time.Hour

	// 2026-08-01 to 2026-11-01 is 93 days, across 2026-10-25 when Berlin
	// leaves summer time.
	period, err := trialPeriodOf(url.Values{paramTrialFrom: {"2026-08-01"}, paramTrialTo: {"2026-11-01"}}, now)
	require.NoError(t, err)
	assert.True(t, period.asked)
	assert.Equal(t, time.Date(2026, 11, 2, 0, 0, 0, 0, berlin), period.to, "the midnight after the last day")
	assert.Equal(t, widest, period.to.Sub(period.from), "the start moves by the hour the clock gave back")
	assert.Equal(t, time.Date(2026, 8, 1, 1, 0, 0, 0, berlin), period.from)

	_, err = trialPeriodOf(url.Values{paramTrialFrom: {"2026-08-01"}, paramTrialTo: {"2026-11-02"}}, now)
	require.Error(t, err, "94 days")
	assert.True(t, errors.IsInvalid(err))
	assert.Contains(t, messageFor(err), "this period has 94")
	// 94 days across the spring change are an hour short of 94 times 24
	// hours, and still 94 days.
	_, err = trialPeriodOf(url.Values{paramTrialFrom: {"2026-03-01"}, paramTrialTo: {"2026-06-02"}}, now)
	require.Error(t, err, "94 days across the spring change")
	assert.Contains(t, messageFor(err), "this period has 94")

	// Where the clock does not change, the start stays at its midnight.
	period, err = trialPeriodOf(url.Values{paramTrialFrom: {"2026-08-01"}, paramTrialTo: {"2026-11-01"}},
		now.In(time.UTC))
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), period.from)
	assert.Equal(t, widest, period.to.Sub(period.from))

	trials := &fakeTrials{report: promotionTrialBody}
	panel := newCatalogPanel(t, &fakeCatalog{})
	panel.promotionTrials = trials
	panel.scopes = builtInScopes()
	rec := campaignsRequest(panel, http.MethodGet, PromotionsPath+"/promo_1/trial?"+url.Values{
		paramTrialFrom: {"2026-01-01"}, paramTrialTo: {"2026-04-30"},
	}.Encode(), nil, scopePromotionRead, scopeOrderRead)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "A trial covers at most 93 days")
	assert.Empty(t, trials.calls, "a period over the bound asks no surface")
}

// TestATrialRefusalIsShownOnTheScreen: what the module refuses comes back on
// the trial screen with its reason and the status it was refused with, the
// form kept as typed; a failure the operator cannot act on is not shown.
func TestATrialRefusalIsShownOnTheScreen(t *testing.T) {
	t.Parallel()

	trials := &fakeTrials{err: errors.Conflict("tax_trial_change_refused",
		"taxrate_vat is a default rate; a rule cannot be tried on it")}
	panel := trialTaxes(t, trials)
	reader := []string{scopeTaxRead, scopeOrderRead}
	path := TaxesPath + "/rates/taxrate_vat/trial?" + url.Values{
		paramTrialFrom: {"2026-09-01"}, paramTrialTo: {"2026-09-30"}, paramTrialRuleID: {"prod_9"},
	}.Encode()

	rec := campaignsRequest(panel, http.MethodGet, path, nil, reader...)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), `<p role="alert">taxrate_vat is a default rate; a rule cannot be tried on it</p>`)
	assert.Contains(t, rec.Body.String(), `name="rule_id" value="prod_9"`, "the form as typed")

	trials.err = errors.Invalid("tax_trial_invalid_change", "a change to try is required")
	rec = campaignsRequest(panel, http.MethodGet, path, nil, reader...)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "a change to try is required")

	// A rate retired after the Taxes screen was drawn, or a stale link.
	trials.err = errors.NotFound("tax_rate_not_found", "tax rate not found: taxrate_x")
	rec = campaignsRequest(panel, http.MethodGet, path, nil, reader...)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), `<p role="alert">tax rate not found: taxrate_x</p>`)
	assert.Contains(t, rec.Body.String(), `name="rule_id" value="prod_9"`, "the form as typed")

	rec = campaignsRequest(panel, http.MethodGet, TaxesPath+"/rates/taxrate_red/trial?"+url.Values{
		paramTrialFrom: {"2026-09-01"}, paramTrialTo: {"2026-09-30"}, paramTrialRate: {"lots"},
	}.Encode(), nil, reader...)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "a value that is not a number")
	assert.Len(t, trials.calls, 3, "reaches no surface")

	trials.err = errors.Internal("tax_trial_unavailable", "the flow at db://secret is not bound")
	rec = campaignsRequest(panel, http.MethodGet, path, nil, reader...)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "db://secret")

	trials.err, trials.report = nil, `{"tax_rate_id":"taxrate_vat","renamed":1}`
	rec = campaignsRequest(panel, http.MethodGet, path, nil, reader...)
	assert.Equal(t, http.StatusInternalServerError, rec.Code, "a report with a field the screen does not know")

	panel.taxTrials = nil
	rec = campaignsRequest(panel, http.MethodGet, path, nil, reader...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "no surface, no trial")
}

// TestAProvincesRateOffersNoTrial: a province's rate, which no cart reaches,
// and a rate under an external provider, whose table cannot be amended, offer
// no trial; a country naming the module's own provider does.
func TestAProvincesRateOffersNoTrial(t *testing.T) {
	t.Parallel()

	panel := trialTaxes(t, &fakeTrials{})
	rec := campaignsRequest(panel, http.MethodGet, TaxesPath, nil, scopeTaxRead, scopeOrderRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.NotContains(t, body, "taxrate_ist/trial", "a province's rate")
	assert.NotContains(t, body, "taxrate_de/trial", "a rate under an external provider")
	assert.Contains(t, body, "taxrate_fr/trial", "a country naming the module's own provider")
	assert.Contains(t, body, "taxrate_vat/trial")
}

// TestADefaultRateIsTriedAtAValueOnly: a default rate takes no rule, so its
// form offers the value alone and says so to the trial screen, which offers
// the value alone in turn and sends no rule.
func TestADefaultRateIsTriedAtAValueOnly(t *testing.T) {
	t.Parallel()

	trials := &fakeTrials{report: taxRateTrialBody}
	panel := trialTaxes(t, trials)
	reader := []string{scopeTaxRead, scopeOrderRead}

	rec := campaignsRequest(panel, http.MethodGet, TaxesPath, nil, reader...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	vat := trialForm(t, rec.Body.String(), "taxrate_vat")
	assert.Contains(t, vat, `name="value_only" value="1"`)
	assert.NotContains(t, vat, `name="rule_id"`)
	assert.Contains(t, trialForm(t, rec.Body.String(), "taxrate_red"), `name="rule_id"`, "a ruled rate takes one")

	rec = campaignsRequest(panel, http.MethodGet, TaxesPath+"/rates/taxrate_vat/trial?"+url.Values{
		paramTrialFrom: {"2026-09-01"}, paramTrialTo: {"2026-09-30"}, paramTrialRate: {"18"},
		paramTrialValueOnly: {"1"}, paramTrialRuleID: {"prod_9"},
	}.Encode(), nil, reader...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"rate_bps":1800}`, trials.calls[0].change)
	assert.NotContains(t, rec.Body.String(), `name="rule_id"`)
}

// TestAPromotionAndAPriceListAreTriedOnPastOrders: each screen asks its
// module's surface for the period and draws its own report's figures.
func TestAPromotionAndAPriceListAreTriedOnPastOrders(t *testing.T) {
	t.Parallel()

	promotions, lists := &fakeTrials{report: promotionTrialBody}, &fakeTrials{report: priceListTrialBody}
	panel := newCatalogPanel(t, &fakeCatalog{byEntity: map[string][]query.Record{
		EntityRegion: {currencyRecord("TRY", 2)},
	}})
	panel.promotionTrials, panel.priceListTrials = promotions, lists
	panel.scopes = builtInScopes()
	period := "?" + url.Values{paramTrialFrom: {"2026-09-01"}, paramTrialTo: {"2026-09-30"}}.Encode()

	rec := campaignsRequest(panel, http.MethodGet, PromotionsPath+"/promo_1/trial"+period, nil,
		scopePromotionRead, scopeOrderRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, promotions.calls, 1)
	assert.Equal(t, "promo_1", promotions.calls[0].id)
	assert.Equal(t, day(t, "2026-09-01"), promotions.calls[0].from)
	assert.Equal(t, day(t, "2026-10-01"), promotions.calls[0].to)
	for _, want := range []string{
		"7 orders read", "2 already discounted by it", "3 not applied: no_match",
		"<td>TRY</td><td>4</td><td>1</td><td>20000.00 TRY</td><td>50.00 TRY</td><td>125.50 TRY</td>",
		`<a href="` + OrdersPath + `/order_a">#1042</a></td><td>2026-09-03 12:00</td>` +
			"<td>20000.00 TRY</td><td>50.00 TRY</td><td>125.50 TRY</td></tr>",
	} {
		assert.Contains(t, rec.Body.String(), want)
	}

	rec = campaignsRequest(panel, http.MethodGet, PriceListsPath+"/plist_1/trial"+period, nil,
		scopePricingRead, scopeOrderRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, lists.calls, 1)
	assert.Equal(t, "plist_1", lists.calls[0].id)
	assert.Equal(t, day(t, "2026-10-01"), lists.calls[0].to)
	for _, want := range []string{
		"5 orders read", "2 lines with no price",
		"<td>TRY</td><td>5</td><td>9</td><td>3</td><td>9000.00 TRY</td><td>8800.00 TRY</td><td>8500.00 TRY</td><td>-300.00 TRY</td>",
		"<td>400.00 TRY</td><td>300.00 TRY</td><td>-100.00 TRY</td>",
	} {
		assert.Contains(t, rec.Body.String(), want)
	}
}
