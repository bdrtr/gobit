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
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// cond is a condition with its value written as JSON.
func cond(attribute, operator, value string, values ...string) models.SegmentCondition {
	c := models.SegmentCondition{Attribute: attribute, Operator: operator, Values: values}
	if value != "" {
		c.Value = json.RawMessage(value)
	}
	return c
}

// TestARuleIsNormalizedAsItIsEvaluated is ADR 0217's vocabulary: codes folded
// to upper case, a country listed once, and every value in its one JSON form.
func TestARuleIsNormalizedAsItIsEvaluated(t *testing.T) {
	t.Parallel()

	got, err := NormalizeSegmentRule(models.SegmentRule{
		CurrencyCode: " try ", WindowDays: 365,
		Conditions: []models.SegmentCondition{
			cond(models.SegmentNetSpend, models.SegmentGte, "100000"),
			cond(models.SegmentOrderCount, models.SegmentGt, "2"),
			cond(models.SegmentHasAccount, models.SegmentEq, "true"),
			cond(models.SegmentCountryCode, models.SegmentIn, "", "tr", "DE", "tr"),
			cond(models.SegmentCountryCode, models.SegmentNe, `"fr"`),
			cond(models.SegmentAccountAgeDays, models.SegmentLte, "30"),
		},
	})

	require.NoError(t, err)
	body, err := json.Marshal(got)
	require.NoError(t, err)
	assert.JSONEq(t, `{"currency_code":"TRY","window_days":365,"conditions":[
		{"attribute":"net_spend","operator":"gte","value":100000},
		{"attribute":"order_count","operator":"gt","value":2},
		{"attribute":"has_account","operator":"eq","value":true},
		{"attribute":"country_code","operator":"in","values":["TR","DE"]},
		{"attribute":"country_code","operator":"ne","value":"FR"},
		{"attribute":"account_age_days","operator":"lte","value":30}]}`, string(body))
}

// TestARuleOutsideTheVocabularyIsRefused: every refusal is invalid input with
// the segment's code.
func TestARuleOutsideTheVocabularyIsRefused(t *testing.T) {
	t.Parallel()

	spend := cond(models.SegmentNetSpend, models.SegmentGte, "1")
	count := cond(models.SegmentOrderCount, models.SegmentGte, "1")
	eleven := make([]models.SegmentCondition, models.MaxSegmentConditions+1)
	for i := range eleven {
		eleven[i] = count
	}
	manyCountries := make([]string, models.MaxSegmentValues+1)
	for i := range manyCountries {
		manyCountries[i] = fmt.Sprintf("%c%c", 'A'+i/26, 'A'+i%26)
	}
	for name, rule := range map[string]models.SegmentRule{
		"no condition":                 {},
		"too many conditions":          {Conditions: eleven},
		"unknown attribute":            {Conditions: []models.SegmentCondition{cond("lifetime_value", models.SegmentGt, "1")}},
		"operator the attribute lacks": {Conditions: []models.SegmentCondition{cond(models.SegmentHasAccount, models.SegmentNe, "true")}},
		"a number for has_account":     {Conditions: []models.SegmentCondition{cond(models.SegmentHasAccount, models.SegmentEq, "1")}},
		"no value":                     {Conditions: []models.SegmentCondition{cond(models.SegmentOrderCount, models.SegmentGt, "")}},
		"a null value":                 {Conditions: []models.SegmentCondition{cond(models.SegmentOrderCount, models.SegmentGt, "null")}},
		"a fraction":                   {Conditions: []models.SegmentCondition{cond(models.SegmentOrderCount, models.SegmentGt, "1.5")}},
		"a negative number":            {Conditions: []models.SegmentCondition{cond(models.SegmentAccountAgeDays, models.SegmentGt, "-1")}},
		"a number too large":           {Conditions: []models.SegmentCondition{cond(models.SegmentOrderCount, models.SegmentGt, "1000000000000001")}},
		"a string number":              {Conditions: []models.SegmentCondition{cond(models.SegmentOrderCount, models.SegmentGt, `"3"`)}},
		"a bad country":                {Conditions: []models.SegmentCondition{cond(models.SegmentCountryCode, models.SegmentEq, `"TUR"`)}},
		"in with a value":              {Conditions: []models.SegmentCondition{cond(models.SegmentCountryCode, models.SegmentIn, `"TR"`)}},
		"in with no countries":         {Conditions: []models.SegmentCondition{cond(models.SegmentCountryCode, models.SegmentIn, "")}},
		"in with too many":             {Conditions: []models.SegmentCondition{cond(models.SegmentCountryCode, models.SegmentIn, "", manyCountries...)}},
		"in with a bad country":        {Conditions: []models.SegmentCondition{cond(models.SegmentCountryCode, models.SegmentNin, "", "T1")}},
		"eq with values":               {Conditions: []models.SegmentCondition{cond(models.SegmentCountryCode, models.SegmentEq, `"TR"`, "DE")}},
		"spend without a currency":     {Conditions: []models.SegmentCondition{spend}},
		"spend in no currency":         {CurrencyCode: "TL", Conditions: []models.SegmentCondition{spend}},
		"a currency with no spend":     {CurrencyCode: "TRY", Conditions: []models.SegmentCondition{count}},
		"a window with no orders":      {WindowDays: 30, Conditions: []models.SegmentCondition{cond(models.SegmentHasAccount, models.SegmentEq, "true")}},
		"a negative window":            {WindowDays: -1, Conditions: []models.SegmentCondition{count}},
		"a window too long":            {WindowDays: models.MaxSegmentWindowDays + 1, Conditions: []models.SegmentCondition{count}},
	} {
		_, err := NormalizeSegmentRule(rule)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalid(err), name)
		assert.Equal(t, models.CodeSegmentInvalid, errors.CodeOf(err), name)
	}

	_, err := NormalizeSegmentRule(models.SegmentRule{
		WindowDays: models.MaxSegmentWindowDays, Conditions: []models.SegmentCondition{count},
	})
	require.NoError(t, err, "the longest window is taken")
}

// newSegmentService builds a service with one group.
func newSegmentService(t *testing.T) (*Service, *memRepo, models.CustomerGroup, *time.Time) {
	t.Helper()

	svc, repo, now := newWishlistService(t)
	group, err := svc.CreateGroup(context.Background(), GroupInput{Name: "big spenders"})
	require.NoError(t, err)

	return svc, repo, group, now
}

// countRule is a rule that reads orders.
func countRule() models.SegmentRule {
	return models.SegmentRule{Conditions: []models.SegmentCondition{
		cond(models.SegmentOrderCount, models.SegmentGte, "3"),
	}}
}

// TestASegmentTakesNoHandEdit is ADR 0217: once a rule decides a group's
// members, adding or removing one by hand is refused, and clearing the rule
// hands the group back.
func TestASegmentTakesNoHandEdit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _, group, now := newSegmentService(t)
	customer := newWishlistCustomer(ctx, t, svc)
	require.NoError(t, svc.AddToGroup(ctx, customer.ID, group.ID))

	segment, err := svc.SetGroupSegment(ctx, group.ID, countRule())
	require.NoError(t, err)
	require.NotNil(t, segment.Segment)
	assert.Equal(t, *now, *segment.SegmentSetAt)
	assert.Nil(t, segment.SegmentEvaluatedAt, "no pass has written its members")

	err = svc.AddToGroup(ctx, customer.ID, group.ID)
	assert.Equal(t, models.CodeSegmentManaged, errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindConflict))
	err = svc.RemoveFromGroup(ctx, customer.ID, group.ID)
	assert.Equal(t, models.CodeSegmentManaged, errors.CodeOf(err))

	cleared, err := svc.ClearGroupSegment(ctx, group.ID)
	require.NoError(t, err)
	assert.Nil(t, cleared.Segment)
	require.NoError(t, svc.RemoveFromGroup(ctx, customer.ID, group.ID), "the member the group kept is the operator's again")
}

// TestASegmentRuleIsNormalizedWhenSet: a rule the vocabulary refuses is never
// stored.
func TestASegmentRuleIsNormalizedWhenSet(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, repo, group, _ := newSegmentService(t)

	_, err := svc.SetGroupSegment(ctx, group.ID, models.SegmentRule{})
	require.Error(t, err)
	assert.Zero(t, repo.calls["SetGroupSegment"])

	set, err := svc.SetGroupSegment(ctx, group.ID, models.SegmentRule{Conditions: []models.SegmentCondition{
		cond(models.SegmentCountryCode, models.SegmentEq, `"tr"`),
	}})
	require.NoError(t, err)
	assert.Equal(t, json.RawMessage(`"TR"`), set.Segment.Conditions[0].Value)
}

// TestTheSegmentsAreBounded: a group becomes a segment only while fewer than
// MaxSegments others are, and a segment given a new rule is not counted twice.
func TestTheSegmentsAreBounded(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, _, first, _ := newSegmentService(t)
	_, err := svc.SetGroupSegment(ctx, first.ID, countRule())
	require.NoError(t, err)
	for i := 1; i < models.MaxSegments; i++ {
		g, err := svc.CreateGroup(ctx, GroupInput{Name: fmt.Sprintf("segment %d", i)})
		require.NoError(t, err)
		_, err = svc.SetGroupSegment(ctx, g.ID, countRule())
		require.NoError(t, err)
	}
	extra, err := svc.CreateGroup(ctx, GroupInput{Name: "one too many"})
	require.NoError(t, err)

	_, err = svc.SetGroupSegment(ctx, extra.ID, countRule())
	assert.Equal(t, models.CodeSegmentLimit, errors.CodeOf(err))
	_, err = svc.SetGroupSegment(ctx, first.ID, countRule())
	require.NoError(t, err, "a new rule for a segment is no new segment")
}

// TestTheFlowReadsAndWritesSegmentsThroughTheInterop: the segments, the facts
// page and the page write as the segment flow uses them.
func TestTheFlowReadsAndWritesSegmentsThroughTheInterop(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, repo, group, now := newSegmentService(t)
	segment, err := svc.SetGroupSegment(ctx, group.ID, countRule())
	require.NoError(t, err)
	a, err := svc.CreateCustomer(ctx, CustomerInput{Email: "a@example.com"})
	require.NoError(t, err)
	b, err := svc.CreateCustomer(ctx, CustomerInput{Email: "b@example.com"})
	require.NoError(t, err)

	raw, err := svc.SegmentsJSON(ctx)
	require.NoError(t, err)
	var segments []map[string]any
	require.NoError(t, json.Unmarshal(raw, &segments))
	require.Len(t, segments, 1)
	assert.Equal(t, group.ID, segments[0]["group_id"])
	assert.Equal(t, now.Format(time.RFC3339Nano), segments[0]["set_at"])
	assert.NotNil(t, segments[0]["rule"])

	raw, err = svc.SegmentFactsJSON(ctx, "", 500)
	require.NoError(t, err)
	var facts []map[string]any
	require.NoError(t, json.Unmarshal(raw, &facts))
	require.Len(t, facts, 2)
	_, err = svc.SegmentFactsJSON(ctx, "", 501)
	assert.True(t, errors.IsInvalid(err))

	first, last := a.ID, b.ID
	if first > last {
		first, last = last, first
	}
	added, removed, applied, err := svc.ApplySegmentPage(ctx, group.ID, *segment.SegmentSetAt, "", "", []string{first})
	require.NoError(t, err)
	assert.Equal(t, []any{1, 0, true}, []any{added, removed, applied})
	_, _, applied, err = svc.ApplySegmentPage(ctx, group.ID, segment.SegmentSetAt.Add(-time.Second), "", "", nil)
	require.NoError(t, err)
	assert.False(t, applied, "a page for another rule writes nothing")
	assert.True(t, repo.members[first][group.ID])

	_, _, _, err = svc.ApplySegmentPage(ctx, group.ID, *segment.SegmentSetAt, first, "", []string{first})
	assert.True(t, errors.IsInvalid(err), "a member outside the page is refused")
	_, _, _, err = svc.ApplySegmentPage(ctx, group.ID, *segment.SegmentSetAt, last, first, nil)
	assert.True(t, errors.IsInvalid(err), "a page that ends before it starts is refused")

	done, err := svc.FinishSegment(ctx, group.ID, *segment.SegmentSetAt, now.Add(time.Minute))
	require.NoError(t, err)
	assert.True(t, done)
}
