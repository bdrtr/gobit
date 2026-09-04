package models_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// ptr returns a pointer to a value.
func ptr[T any](v T) *T { return &v }

var at = time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

func TestCampaignWindowContains(t *testing.T) {
	cases := []struct {
		name     string
		campaign models.Campaign
		want     bool
	}{
		{name: "unbounded window", campaign: models.Campaign{}, want: true},
		{
			name:     "before the start",
			campaign: models.Campaign{StartsAt: ptr(at.Add(time.Hour))},
			want:     false,
		},
		{
			name:     "after the end",
			campaign: models.Campaign{EndsAt: ptr(at.Add(-time.Hour))},
			want:     false,
		},
		{
			name:     "inside the window",
			campaign: models.Campaign{StartsAt: ptr(at.Add(-time.Hour)), EndsAt: ptr(at.Add(time.Hour))},
			want:     true,
		},
		{
			name:     "the starting moment is INCLUSIVE",
			campaign: models.Campaign{StartsAt: ptr(at)},
			want:     true,
		},
		{
			name:     "the ending moment is INCLUSIVE",
			campaign: models.Campaign{EndsAt: ptr(at)},
			want:     true,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.campaign.WindowContains(at))
		})
	}
}

func TestCampaignBudgetExhausted(t *testing.T) {
	cases := []struct {
		name     string
		campaign models.Campaign
		want     bool
	}{
		{name: "no budget", campaign: models.Campaign{BudgetType: models.BudgetNone}, want: false},
		{
			name:     "unbounded budget",
			campaign: models.Campaign{BudgetType: models.BudgetSpend, BudgetUsed: 999},
			want:     false,
		},
		{
			name: "below the bound",
			campaign: models.Campaign{
				BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(100)), BudgetUsed: 99,
			},
			want: false,
		},
		{
			name: "a budget sitting EXACTLY on the bound is EXHAUSTED",
			campaign: models.Campaign{
				BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(100)), BudgetUsed: 100,
			},
			want: true,
		},
		{
			name: "above the bound",
			campaign: models.Campaign{
				BudgetType: models.BudgetUsage, BudgetLimit: ptr(int64(2)), BudgetUsed: 3,
			},
			want: true,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.campaign.BudgetExhausted())
		})
	}
}

func TestCampaignBudgetDeltaFor(t *testing.T) {
	cases := []struct {
		name       string
		budgetType models.CampaignBudgetType
		amount     int64
		want       int64
	}{
		{name: "a money-measured budget consumes the amount", budgetType: models.BudgetSpend, amount: 2500, want: 2500},
		{name: "a count-measured budget consumes one", budgetType: models.BudgetUsage, amount: 2500, want: 1},
		{name: "a campaign without a budget consumes nothing", budgetType: models.BudgetNone, amount: 2500, want: 0},
		{name: "an unrecognized type consumes nothing", budgetType: "undetermined", amount: 2500, want: 0},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			campaign := models.Campaign{BudgetType: tt.budgetType}
			assert.Equal(t, tt.want, campaign.BudgetDeltaFor(tt.amount))
		})
	}
}

func TestPromotionUsageExhausted(t *testing.T) {
	assert.False(t, models.Promotion{}.UsageExhausted(), "an unbounded coupon is never exhausted")
	assert.False(t, models.Promotion{UsageLimit: ptr(int64(2)), UsageCount: 1}.UsageExhausted())
	assert.True(t, models.Promotion{UsageLimit: ptr(int64(2)), UsageCount: 2}.UsageExhausted(),
		"a coupon sitting on the bound is EXHAUSTED; a new use would exceed the bound")
	assert.True(t, models.Promotion{UsageLimit: ptr(int64(0)), UsageCount: 0}.UsageExhausted(),
		"a coupon bounded at zero can never be used")
}

func TestPromotionCandidateSeparatesRulesByType(t *testing.T) {
	candidate := models.PromotionCandidate{
		Rules: []models.PromotionRule{
			{ID: "prule_1", RuleType: models.RuleContext, Attribute: "region_id"},
			{ID: "prule_2", RuleType: models.RuleTarget, Attribute: "kategori"},
			{ID: "prule_3", RuleType: models.RuleContext, Attribute: "customer_group_id"},
		},
	}

	contextRules := candidate.ContextRules()
	targetRules := candidate.TargetRules()

	require.Len(t, contextRules, 2)
	require.Len(t, targetRules, 1)
	assert.Equal(t, "prule_2", targetRules[0].ID)
}

func TestValidityHelpers(t *testing.T) {
	assert.True(t, models.BudgetSpend.Valid())
	assert.False(t, models.CampaignBudgetType("nonexistent").Valid())

	assert.True(t, models.PromotionBuyGet.Valid())
	assert.False(t, models.PromotionType("nonexistent").Valid())

	assert.True(t, models.PromotionActive.Valid())
	assert.False(t, models.PromotionStatus("nonexistent").Valid())

	assert.True(t, models.MethodPercentage.Valid())
	assert.False(t, models.ApplicationMethodType("nonexistent").Valid())

	assert.True(t, models.TargetOrder.Valid())
	assert.False(t, models.ApplicationTargetType("nonexistent").Valid())

	assert.True(t, models.AllocationAcross.Valid())
	assert.False(t, models.Allocation("nonexistent").Valid())

	assert.True(t, models.RuleTarget.Valid())
	assert.False(t, models.RuleType("nonexistent").Valid())
}

func TestRuleOperatorProperties(t *testing.T) {
	assert.True(t, models.OpGte.Valid())
	assert.False(t, models.RuleOperator("nonexistent").Valid())

	assert.True(t, models.OpLt.Numeric())
	assert.False(t, models.OpEq.Numeric())
	assert.False(t, models.RuleOperator("nonexistent").Numeric())

	assert.True(t, models.OpIn.MultiValue())
	assert.True(t, models.OpNin.MultiValue())
	assert.False(t, models.OpEq.MultiValue())
}

func TestRedemptionReleased(t *testing.T) {
	assert.False(t, models.Redemption{}.Released())
	assert.True(t, models.Redemption{ReleasedAt: ptr(at)}.Released())
}

func TestIDsArePrefixedAndTimeOrdered(t *testing.T) {
	generators := map[string]func(time.Time) string{
		models.CampaignIDPrefix:          models.NewCampaignID,
		models.PromotionIDPrefix:         models.NewPromotionID,
		models.PromotionRuleIDPrefix:     models.NewPromotionRuleID,
		models.ApplicationMethodIDPrefix: models.NewApplicationMethodID,
		models.RedemptionIDPrefix:        models.NewRedemptionID,
	}

	for prefix, generate := range generators {
		id := generate(at)
		assert.True(t, strings.HasPrefix(id, prefix), "the %q prefix was expected: %s", prefix, id)
		assert.Len(t, id, len(prefix)+models.IDBodyLength())
	}

	earlier := models.NewPromotionID(at)
	later := models.NewPromotionID(at.Add(time.Second))
	assert.Less(t, earlier, later,
		"identifiers must carry the order of time lexicographically; the application order rests on it")
}

func TestNewIDIsUnique(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for range 1000 {
		id := models.NewPromotionID(at)
		_, repeated := seen[id]
		require.False(t, repeated, "identifiers produced in the same millisecond must not collide")
		seen[id] = struct{}{}
	}
}

func TestNewIDClampsPreEpochTime(t *testing.T) {
	id := models.NewID(models.PromotionIDPrefix, time.Unix(-1000, 0))

	assert.True(t, strings.HasPrefix(id, models.PromotionIDPrefix))
	assert.Len(t, id, len(models.PromotionIDPrefix)+models.IDBodyLength(),
		"a pre-1970 stamp must not break ordering and the identifier must still be produced")
}
