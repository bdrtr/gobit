package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// The trial fixture's own ids, beside the shared ones.
const (
	rateE        = models.TaxRateIDPrefix + "E0000000000000000000000000"
	rateUS       = models.TaxRateIDPrefix + "U0000000000000000000000000"
	extRegionID  = models.TaxRegionIDPrefix + "EX0000000000000000000000000"
	missingRule  = models.TaxRateRuleIDPrefix + "Z0000000000000000000000000"
	trialProduct = "prod_trial"
)

// bps is a rate's address for a change.
func bps(value int32) *int32 { return &value }

// compareOne taxes one line of one Turkish order under the change.
func compareOne(t *testing.T, svc *Service, rateID string, change RateChange, item TaxableItem) CompareItem {
	t.Helper()

	answers, _, err := svc.CompareRate(context.Background(), rateID, change,
		[]CompareEntry{{Reference: "order_1", CountryCode: "TR", Items: []TaxableItem{item}}})
	require.NoError(t, err)
	require.Len(t, answers, 1)
	require.Len(t, answers[0].Items, 1)

	return answers[0].Items[0]
}

// TestARateIsComparedAtItsTrialValue: the trial applies the new value, the
// baseline the stored one, and a line the rate takes part in is reached while
// a line another rate takes is not.
func TestARateIsComparedAtItsTrialValue(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)
	repo.seedRuledRate(rateB, trRegionID, 1000)
	repo.seedRule(ruleA, rateB, models.ReferenceProduct, "prod_b")

	got := compareOne(t, svc, rateA, RateChange{RateBps: bps(1800)}, TaxableItem{ID: "li_1", ProductID: "prod_a", Amount: 10_000})
	assert.Equal(t, CompareItem{
		ID: "li_1", Reached: true,
		Baseline: CompareLine{RateID: rateA, TaxAmount: 2000},
		Trial:    CompareLine{RateID: rateA, TaxAmount: 1800},
	}, got)

	other := compareOne(t, svc, rateA, RateChange{RateBps: bps(1800)}, TaxableItem{ID: "li_2", ProductID: "prod_b", Amount: 10_000})
	assert.Equal(t, CompareItem{
		ID:       "li_2",
		Baseline: CompareLine{RateID: rateB, TaxAmount: 1000},
		Trial:    CompareLine{RateID: rateB, TaxAmount: 1000},
	}, other, "a line another rate takes is neither reached nor moved")
}

// TestARuleIsComparedAsIfWritten: a rule added on a tax class takes the lines
// of that class's products in the trial, on the rate tried, and not in the
// baseline.
func TestARuleIsComparedAsIfWritten(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)
	repo.seedRuledRate(rateB, trRegionID, 1000)
	repo.seedRuledRate(rateC, trRegionID, 500)
	repo.members[trialProduct] = "taxclass_reduced"
	change := RateChange{AddRules: []RuleKey{{Reference: "tax_class", ReferenceID: "taxclass_reduced"}}}

	got := compareOne(t, svc, rateB, change, TaxableItem{ID: "li_1", ProductID: trialProduct, Amount: 10_000})
	assert.Equal(t, CompareItem{
		ID: "li_1", Reached: true,
		Baseline: CompareLine{RateID: rateA, TaxAmount: 2000},
		Trial:    CompareLine{RateID: rateB, TaxAmount: 1000},
	}, got, "the class's product moves to the rate tried, through the class the module resolves")

	other := compareOne(t, svc, rateB, change, TaxableItem{ID: "li_2", ProductID: "prod_unclassed", Amount: 10_000})
	assert.Equal(t, int64(2000), other.Trial.TaxAmount, "a product in no class keeps the default")
	assert.Empty(t, repo.rules, "a rule tried is not written")
}

// TestADroppedRuleIsComparedAsIfDeleted: a dropped rule stops taking its
// lines in the trial and still takes them in the baseline.
func TestADroppedRuleIsComparedAsIfDeleted(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)
	repo.seedRuledRate(rateB, trRegionID, 1000)
	repo.seedRule(ruleA, rateB, models.ReferenceProduct, trialProduct)

	got := compareOne(t, svc, rateB, RateChange{DropRules: []string{ruleA}},
		TaxableItem{ID: "li_1", ProductID: trialProduct, Amount: 10_000})
	assert.Equal(t, CompareItem{
		ID: "li_1", Reached: true,
		Baseline: CompareLine{RateID: rateB, TaxAmount: 1000},
		Trial:    CompareLine{RateID: rateA, TaxAmount: 2000},
	}, got)
	assert.Contains(t, repo.rules, ruleA, "a rule dropped in a trial is not deleted")
}

// TestAnInclusiveMarketTakesTheTrialOutOfTheSamePrice: where prices include
// their tax, both figures are extracted from the amount sent.
func TestAnInclusiveMarketTakesTheTrialOutOfTheSamePrice(t *testing.T) {
	svc, repo := newTestService(t)
	included := true
	repo.seedRegion(models.TaxRegion{ID: trRegionID, CountryCode: "TR", PricesIncludeTax: &included})
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	got := compareOne(t, svc, rateA, RateChange{RateBps: bps(1000)}, TaxableItem{ID: "li_1", Amount: 12_000})

	baseline, err := TaxIncludedIn(12_000, 2000)
	require.NoError(t, err)
	trial, err := TaxIncludedIn(12_000, 1000)
	require.NoError(t, err)
	assert.Equal(t, []int64{baseline, trial}, []int64{got.Baseline.TaxAmount, got.Trial.TaxAmount})
	assert.Equal(t, int64(2000), got.Baseline.TaxAmount, "12,000 holds 2,000 of 20% tax")
}

// TestAStackedRateIsComparedThroughItsStack: a rate standing on the default
// is tried at its new value inside the stack, and the line is reached through
// the stack's components, since the line's own rate is the base.
func TestAStackedRateIsComparedThroughItsStack(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 500)
	repo.seedStackedRate(rateB, trRegionID, rateA, 800, false)

	got := compareOne(t, svc, rateB, RateChange{RateBps: bps(1000)}, TaxableItem{ID: "li_1", Amount: 10_000})
	assert.Equal(t, CompareItem{
		ID: "li_1", Reached: true,
		Baseline: CompareLine{RateID: rateA, TaxAmount: 500 + 800},
		Trial:    CompareLine{RateID: rateA, TaxAmount: 500 + 1000},
	}, got)
}

// TestAStackTakingMoreThanItsLineIsRefused: a value a write refuses for a
// stack is refused for a trial, before a line is taxed with it.
func TestAStackTakingMoreThanItsLineIsRefused(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 6000)
	repo.seedStackedRate(rateB, trRegionID, rateA, 3000, false)

	_, _, err := svc.CompareRate(context.Background(), rateB, RateChange{RateBps: bps(5000)},
		[]CompareEntry{{Reference: "order_1", CountryCode: "TR", Items: []TaxableItem{{ID: "li_1", Amount: 10_000}}}})
	require.Error(t, err)
	assert.Equal(t, CodeStackExceedsBase, errors.CodeOf(err))
}

// TestAnOrderOutsideTheRatesCountryIsNotPriced: an order in another country is
// marked outside and none of its lines is taxed.
func TestAnOrderOutsideTheRatesCountryIsNotPriced(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedRootRegion(usRegionID, "US")
	repo.seedDefaultRate(rateA, trRegionID, 2000)
	repo.seedDefaultRate(rateUS, usRegionID, 700)

	answers, _, err := svc.CompareRate(context.Background(), rateA, RateChange{RateBps: bps(1800)},
		[]CompareEntry{{Reference: "order_us", CountryCode: "us", Items: []TaxableItem{{ID: "li_1", Amount: 10_000}}}})
	require.NoError(t, err)
	assert.Equal(t, []CompareAnswer{{Reference: "order_us", Outside: true, Items: []CompareItem{}}}, answers)
}

// TestACompareAnswersInTheRequestsOrder: the answers follow the request, order
// by order and line by line, however many there are, with the module's
// assumptions beside them.
func TestACompareAnswersInTheRequestsOrder(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	var entries []CompareEntry
	for i := range 12 {
		entry := CompareEntry{Reference: fmt.Sprintf("order_%02d", 11-i), CountryCode: "TR"}
		if i%4 == 3 {
			entry.CountryCode = "DE"
		}
		for j := range 5 {
			entry.Items = append(entry.Items, TaxableItem{ID: fmt.Sprintf("li_%d_%d", 11-i, 4-j), Amount: int64(1000 * (j + 1))})
		}
		entries = append(entries, entry)
	}

	answers, assumptions, err := svc.CompareRate(context.Background(), rateA, RateChange{RateBps: bps(1000)}, entries)
	require.NoError(t, err)
	require.Len(t, answers, len(entries))
	for i := range entries {
		assert.Equal(t, entries[i].Reference, answers[i].Reference)
		if entries[i].CountryCode != "TR" {
			assert.True(t, answers[i].Outside)
			continue
		}
		require.Len(t, answers[i].Items, len(entries[i].Items))
		for j := range entries[i].Items {
			assert.Equal(t, entries[i].Items[j].ID, answers[i].Items[j].ID)
			assert.Equal(t, entries[i].Items[j].Amount/10, answers[i].Items[j].Trial.TaxAmount,
				"each line is answered with its own amount")
		}
	}
	assert.Equal(t, []string{"todays_rates", "todays_tax_classes", "todays_price_inclusion"}, assumptions)
}

// TestAComparisonBoundsItsSize: more orders or lines than one comparison
// taxes are refused, not cut to fit.
func TestAComparisonBoundsItsSize(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)
	change := RateChange{RateBps: bps(1000)}

	entries := make([]CompareEntry, MaxCompareEntries+1)
	for i := range entries {
		entries[i] = CompareEntry{Reference: fmt.Sprintf("order_%d", i), CountryCode: "TR"}
	}
	_, _, err := svc.CompareRate(context.Background(), rateA, change, entries)
	require.Error(t, err)
	assert.Equal(t, CodeInvalidInput, errors.CodeOf(err))
	_, _, err = svc.CompareRate(context.Background(), rateA, change, entries[:MaxCompareEntries])
	require.NoError(t, err, "the bound itself is allowed")

	items := make([]TaxableItem, MaxCompareItems+1)
	for i := range items {
		items[i] = TaxableItem{ID: fmt.Sprintf("li_%d", i), Amount: 100}
	}
	_, _, err = svc.CompareRate(context.Background(), rateA, change,
		[]CompareEntry{{Reference: "order_1", CountryCode: "TR", Items: items}})
	require.Error(t, err)
	assert.Equal(t, CodeInvalidInput, errors.CodeOf(err))
	_, _, err = svc.CompareRate(context.Background(), rateA, change,
		[]CompareEntry{{Reference: "order_1", CountryCode: "TR", Items: items[:MaxCompareItems]}})
	require.NoError(t, err, "the bound itself is allowed")
}

// TestAComparisonRefusesAMalformedRequest: an order without a reference, a
// line without an id or with one repeated, and a negative amount are refused
// rather than answered, and a country sent in lower case is the rate's own.
func TestAComparisonRefusesAMalformedRequest(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)
	change := RateChange{RateBps: bps(1000)}
	line := func(id string, amount int64) TaxableItem { return TaxableItem{ID: id, Amount: amount} }

	for name, entry := range map[string]CompareEntry{
		"no reference":      {CountryCode: "TR", Items: []TaxableItem{line("li_1", 100)}},
		"a line with no id": {Reference: "order_1", CountryCode: "TR", Items: []TaxableItem{line("", 100)}},
		"a repeated line":   {Reference: "order_1", CountryCode: "TR", Items: []TaxableItem{line("li_1", 100), line("li_1", 200)}},
		"a negative line":   {Reference: "order_1", CountryCode: "TR", Items: []TaxableItem{line("li_1", -100)}},
	} {
		_, _, err := svc.CompareRate(context.Background(), rateA, change, []CompareEntry{entry})
		require.Error(t, err, name)
		assert.Equal(t, CodeInvalidInput, errors.CodeOf(err), "%s: %v", name, err)
	}

	answers, _, err := svc.CompareRate(context.Background(), rateA, change,
		[]CompareEntry{{Reference: "order_1", CountryCode: "tr", Items: []TaxableItem{line("li_1", 10_000)}}})
	require.NoError(t, err)
	require.Len(t, answers, 1)
	assert.False(t, answers[0].Outside, "a lower-case code is the rate's country")
	require.Len(t, answers[0].Items, 1)
	assert.Equal(t, int64(1000), answers[0].Items[0].Trial.TaxAmount)
}

// TestAComparisonRefusesAFigureACartWouldRefuse: a stack written before its
// members were checked on a value write (D237) takes more than a line, and the
// comparison refuses the figure as a cart's calculation does, even where the
// rate tried is not in that stack.
func TestAComparisonRefusesAFigureACartWouldRefuse(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 6000)
	repo.seedStackedRate(rateB, trRegionID, rateA, 6000, false)
	repo.seedRuledRate(rateC, trRegionID, 1000)

	_, _, err := svc.CompareRate(context.Background(), rateC, RateChange{RateBps: bps(900)},
		[]CompareEntry{{Reference: "order_1", CountryCode: "TR", Items: []TaxableItem{{ID: "li_1", Amount: 10_000}}}})
	require.Error(t, err)
	assert.Equal(t, CodeProviderInvalidResult, errors.CodeOf(err), "%v", err)
}

// countingFlow is a rate trial flow that counts its calls.
type countingFlow struct {
	calls  int
	change string
}

// TrialTaxRateJSON records the change and answers an empty report.
func (f *countingFlow) TrialTaxRateJSON(
	_ context.Context, _ string, _, _ time.Time, change json.RawMessage,
) (json.RawMessage, error) {
	f.calls++
	f.change = string(change)

	return json.RawMessage(`{}`), nil
}

// newRefusalWorld seeds one fixture every refusal row breaks exactly one rule
// of: a Turkish default, a ruled rate carrying a product rule, a stacked rate
// on the default, a second ruled rate carrying its own rule, a province's
// rate, and an American default computed by an external provider.
func newRefusalWorld(t *testing.T) (*Service, *memRepo) {
	t.Helper()

	repo := newMemRepo()
	providers := NewProviderRegistry()
	require.NoError(t, providers.Register(NewLocalProvider(repo)))
	require.NoError(t, providers.Register(&idOnlyProvider{id: "ext"}))
	svc := New(repo, Options{Now: func() time.Time { return testNow }, Providers: providers})

	repo.seedRootRegion(trRegionID, "TR")
	repo.seedProvinceRegion(trIstanbul, "TR", "34", trRegionID)
	repo.seedRegion(models.TaxRegion{ID: extRegionID, CountryCode: "US", ProviderID: "ext"})
	repo.seedDefaultRate(rateA, trRegionID, 6000)
	repo.seedRuledRate(rateB, trRegionID, 1000)
	repo.seedRule(ruleA, rateB, models.ReferenceProduct, "prod_1")
	repo.seedStackedRate(rateC, trRegionID, rateA, 3000, false)
	repo.seedRuledRate(rateE, trRegionID, 500)
	repo.seedRule(ruleB, rateE, models.ReferenceProduct, "prod_2")
	repo.seedRuledRate(rateD, trIstanbul, 800)
	repo.seedDefaultRate(rateUS, extRegionID, 700)

	return svc, repo
}

// TestATrialRefusesAChangeTheWritePathWouldRefuse holds every refusal on the
// three ways in: the check itself, the endpoint's TryRate before the flow is
// asked, and the comparison the flow asks.
func TestATrialRefusesAChangeTheWritePathWouldRefuse(t *testing.T) {
	product := func(id string) RuleKey { return RuleKey{Reference: "product", ReferenceID: id} }
	manyRules := make([]RuleKey, MaxTrialRuleChanges+1)
	for i := range manyRules {
		manyRules[i] = product(fmt.Sprintf("prod_%d", i+10))
	}
	// shape marks a refusal made before anything is read; says, where given,
	// is what the refusal names, for two rows another check would refuse with
	// the same code.
	rows := []struct {
		name   string
		rateID string
		change RateChange
		code   string
		shape  bool
		says   string
	}{
		{"no change", rateB, RateChange{}, CodeTrialInvalidChange, true, ""},
		{"too many rules", rateB, RateChange{AddRules: manyRules}, CodeTrialInvalidChange, true, ""},
		{"a rate over a hundred percent", rateB, RateChange{RateBps: bps(10_001)}, CodeInvalidInput, true, ""},
		{"a negative rate", rateB, RateChange{RateBps: bps(-1)}, CodeInvalidInput, true, ""},
		{"an unknown reference", rateB, RateChange{AddRules: []RuleKey{{Reference: "brand", ReferenceID: "b"}}}, CodeTrialInvalidChange, true, ""},
		{"a shipping rule", rateB, RateChange{AddRules: []RuleKey{{Reference: "shipping_option", ReferenceID: "so_1"}}}, CodeTrialInvalidChange, true, ""},
		{"an empty reference id", rateB, RateChange{AddRules: []RuleKey{product("")}}, CodeTrialInvalidChange, true, ""},
		{"a rule added twice", rateB, RateChange{AddRules: []RuleKey{product("prod_9"), product("prod_9")}}, CodeTrialInvalidChange, true, ""},
		{"a rule dropped twice", rateB, RateChange{DropRules: []string{ruleA, ruleA}}, CodeTrialInvalidChange, true, ""},
		{"a malformed rule to drop", rateB, RateChange{DropRules: []string{"rule_1"}}, CodeTrialInvalidChange, true, ""},
		{"a rule another rate carries", rateB, RateChange{DropRules: []string{ruleB}}, CodeTrialInvalidChange, false, ""},
		{"a rule no rate carries", rateB, RateChange{DropRules: []string{missingRule}}, CodeTrialInvalidChange, false, ""},
		{"a rule the rate carries", rateB, RateChange{AddRules: []RuleKey{product("prod_1")}}, CodeTrialInvalidChange, false, "already carries"},
		{"a rule dropped and added back", rateB, RateChange{AddRules: []RuleKey{product("prod_1")}, DropRules: []string{ruleA}}, CodeTrialInvalidChange, false, "changes nothing"},
		{"a rule on the default", rateA, RateChange{AddRules: []RuleKey{product("prod_9")}}, CodeTrialChangeRefused, false, ""},
		{"a rule on a stacked rate", rateC, RateChange{AddRules: []RuleKey{product("prod_9")}}, CodeTrialChangeRefused, false, ""},
		{"a stack past its line", rateC, RateChange{RateBps: bps(4001)}, CodeStackExceedsBase, false, ""},
		{"a province's rate", rateD, RateChange{RateBps: bps(900)}, CodeTrialRateUnreached, false, ""},
		{"an external provider's rate", rateUS, RateChange{RateBps: bps(800)}, CodeTrialProviderExternal, false, ""},
		{"no such rate", rateA + "X", RateChange{RateBps: bps(800)}, "tax_rate_not_found", false, ""},
	}
	ctx := context.Background()

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			svc, repo := newRefusalWorld(t)

			_, err := svc.checkRateChange(ctx, row.rateID, row.change)
			require.Error(t, err)
			assert.Equal(t, row.code, errors.CodeOf(err), "the check: %v", err)
			assert.Contains(t, err.Error(), row.says)
			if row.shape {
				assert.Zero(t, repo.calls["GetTaxRate"], "a malformed change is refused before the rate is read")
			}

			flow := &countingFlow{}
			_, err = svc.TryRate(ctx, flow, row.rateID, testNow.Add(-time.Hour), testNow, row.change)
			require.Error(t, err)
			assert.Equal(t, row.code, errors.CodeOf(err), "TryRate: %v", err)
			assert.Zero(t, flow.calls, "a refused change reads no order")

			change, err := json.Marshal(row.change)
			require.NoError(t, err)
			_, err = NewInterop(svc).CompareRateJSON(ctx, row.rateID, change, json.RawMessage(`{"entries":[]}`))
			require.Error(t, err)
			assert.Equal(t, row.code, errors.CodeOf(err), "the comparison: %v", err)
		})
	}

	t.Run("every row's target accepts a change of its own", func(t *testing.T) {
		svc, _ := newRefusalWorld(t)
		for _, ok := range []struct {
			rateID string
			change RateChange
		}{
			{rateB, RateChange{AddRules: []RuleKey{product("prod_9")}, DropRules: []string{ruleA}}},
			{rateA, RateChange{RateBps: bps(7000)}},
			{rateC, RateChange{RateBps: bps(4000)}},
		} {
			_, err := svc.checkRateChange(ctx, ok.rateID, ok.change)
			assert.NoError(t, err, "%s", ok.rateID)
		}
	})
}

// TestATrialAsksTheFlowOnlyWithAPastPeriodAndABoundFlow: TryRate refuses a
// period reaching into the future and an unbound flow, and hands the flow the
// change as the comparison will decode it.
func TestATrialAsksTheFlowOnlyWithAPastPeriodAndABoundFlow(t *testing.T) {
	svc, _ := newRefusalWorld(t)
	ctx := context.Background()
	change := RateChange{RateBps: bps(900), AddRules: []RuleKey{{Reference: "product", ReferenceID: "prod_9"}}}

	_, err := svc.TryRate(ctx, nil, rateB, testNow.Add(-time.Hour), testNow, change)
	require.Error(t, err)
	assert.Equal(t, CodeTrialUnavailable, errors.CodeOf(err))

	flow := &countingFlow{}
	_, err = svc.TryRate(ctx, flow, rateB, testNow.Add(-time.Hour), testNow.Add(time.Second), change)
	require.Error(t, err)
	assert.Equal(t, CodeTrialInvalidPeriod, errors.CodeOf(err))
	assert.Zero(t, flow.calls)

	_, err = svc.TryRate(ctx, flow, rateB, testNow.Add(-time.Hour), testNow, change)
	require.NoError(t, err)
	assert.Equal(t, 1, flow.calls)
	assert.JSONEq(t, `{"rate_bps":900,"add_rules":[{"reference":"product","reference_id":"prod_9"}]}`, flow.change)
}

// TestThePanelTriesARate is ADR 0395 through the tax module's panel surface:
// the change crosses as JSON, read strictly, and the trial is TryRate's, its
// refusals included; a surface with no flow bound tries nothing.
func TestThePanelTriesARate(t *testing.T) {
	svc, _ := newRefusalWorld(t)
	ctx := context.Background()
	flow := &countingFlow{}
	surface := NewAdminSurface(svc).WithTrial(flow)
	change := json.RawMessage(`{"rate_bps":900,"add_rules":[{"reference":"product","reference_id":"prod_9"}]}`)

	report, err := surface.TrialTaxRateJSON(ctx, rateB, testNow.Add(-time.Hour), testNow, change)
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(report), "the flow's report as it is")
	assert.Equal(t, 1, flow.calls)
	assert.JSONEq(t, string(change), flow.change)

	for name, body := range map[string]string{
		"an unknown field": `{"rate_bps":900,"rate":9}`,
		"not a change":     `[`,
	} {
		_, err = surface.TrialTaxRateJSON(ctx, rateB, testNow.Add(-time.Hour), testNow, json.RawMessage(body))
		require.Error(t, err, name)
		assert.Equal(t, CodeTrialInvalidChange, errors.CodeOf(err), "%s: %v", name, err)
	}
	_, err = surface.TrialTaxRateJSON(ctx, rateB, testNow.Add(-time.Hour), testNow.Add(time.Second), change)
	require.Error(t, err)
	assert.Equal(t, CodeTrialInvalidPeriod, errors.CodeOf(err), "TryRate's refusal")
	assert.Equal(t, 1, flow.calls, "a refused trial reads no order")

	_, err = NewAdminSurface(svc).TrialTaxRateJSON(ctx, rateB, testNow.Add(-time.Hour), testNow, change)
	require.Error(t, err)
	assert.Equal(t, CodeTrialUnavailable, errors.CodeOf(err))
}
