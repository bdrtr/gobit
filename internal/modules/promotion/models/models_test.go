package models_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// ptr bir değerin işaretçisini döner.
func ptr[T any](v T) *T { return &v }

var an = time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

func TestCampaignWindowContains(t *testing.T) {
	testler := []struct {
		ad       string
		campaign models.Campaign
		bekleme  bool
	}{
		{ad: "sınırsız pencere", campaign: models.Campaign{}, bekleme: true},
		{
			ad:       "başlangıçtan önce",
			campaign: models.Campaign{StartsAt: ptr(an.Add(time.Hour))},
			bekleme:  false,
		},
		{
			ad:       "bitişten sonra",
			campaign: models.Campaign{EndsAt: ptr(an.Add(-time.Hour))},
			bekleme:  false,
		},
		{
			ad:       "pencere içinde",
			campaign: models.Campaign{StartsAt: ptr(an.Add(-time.Hour)), EndsAt: ptr(an.Add(time.Hour))},
			bekleme:  true,
		},
		{
			ad:       "başlangıç anı KAPSAYICIDIR",
			campaign: models.Campaign{StartsAt: ptr(an)},
			bekleme:  true,
		},
		{
			ad:       "bitiş anı KAPSAYICIDIR",
			campaign: models.Campaign{EndsAt: ptr(an)},
			bekleme:  true,
		},
	}

	for _, tt := range testler {
		t.Run(tt.ad, func(t *testing.T) {
			assert.Equal(t, tt.bekleme, tt.campaign.WindowContains(an))
		})
	}
}

func TestCampaignBudgetExhausted(t *testing.T) {
	testler := []struct {
		ad       string
		campaign models.Campaign
		bekleme  bool
	}{
		{ad: "bütçesiz", campaign: models.Campaign{BudgetType: models.BudgetNone}, bekleme: false},
		{
			ad:       "sınırsız bütçe",
			campaign: models.Campaign{BudgetType: models.BudgetSpend, BudgetUsed: 999},
			bekleme:  false,
		},
		{
			ad: "sınırın altında",
			campaign: models.Campaign{
				BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(100)), BudgetUsed: 99,
			},
			bekleme: false,
		},
		{
			ad: "sınıra TAM oturmuş bütçe TÜKENMİŞTİR",
			campaign: models.Campaign{
				BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(100)), BudgetUsed: 100,
			},
			bekleme: true,
		},
		{
			ad: "sınırın üstünde",
			campaign: models.Campaign{
				BudgetType: models.BudgetUsage, BudgetLimit: ptr(int64(2)), BudgetUsed: 3,
			},
			bekleme: true,
		},
	}

	for _, tt := range testler {
		t.Run(tt.ad, func(t *testing.T) {
			assert.Equal(t, tt.bekleme, tt.campaign.BudgetExhausted())
		})
	}
}

func TestCampaignBudgetDeltaFor(t *testing.T) {
	testler := []struct {
		ad         string
		budgetType models.CampaignBudgetType
		amount     int64
		bekleme    int64
	}{
		{ad: "para ölçülü bütçe tutarı tüketir", budgetType: models.BudgetSpend, amount: 2500, bekleme: 2500},
		{ad: "adet ölçülü bütçe biri tüketir", budgetType: models.BudgetUsage, amount: 2500, bekleme: 1},
		{ad: "bütçesiz kampanya tüketmez", budgetType: models.BudgetNone, amount: 2500, bekleme: 0},
		{ad: "tanınmayan tür tüketmez", budgetType: "belirsiz", amount: 2500, bekleme: 0},
	}

	for _, tt := range testler {
		t.Run(tt.ad, func(t *testing.T) {
			campaign := models.Campaign{BudgetType: tt.budgetType}
			assert.Equal(t, tt.bekleme, campaign.BudgetDeltaFor(tt.amount))
		})
	}
}

func TestPromotionUsageExhausted(t *testing.T) {
	assert.False(t, models.Promotion{}.UsageExhausted(), "sınırsız kupon tükenmez")
	assert.False(t, models.Promotion{UsageLimit: ptr(int64(2)), UsageCount: 1}.UsageExhausted())
	assert.True(t, models.Promotion{UsageLimit: ptr(int64(2)), UsageCount: 2}.UsageExhausted(),
		"sınıra oturmuş kupon TÜKENMİŞTİR; yeni bir kullanım sınırı aşardı")
	assert.True(t, models.Promotion{UsageLimit: ptr(int64(0)), UsageCount: 0}.UsageExhausted(),
		"sıfır sınırlı kupon hiç kullanılamaz")
}

func TestPromotionCandidateKurallariTuruneGoreAyirir(t *testing.T) {
	candidate := models.PromotionCandidate{
		Rules: []models.PromotionRule{
			{ID: "prule_1", RuleType: models.RuleContext, Attribute: "region_id"},
			{ID: "prule_2", RuleType: models.RuleTarget, Attribute: "kategori"},
			{ID: "prule_3", RuleType: models.RuleContext, Attribute: "customer_group_id"},
		},
	}

	baglam := candidate.ContextRules()
	hedef := candidate.TargetRules()

	require.Len(t, baglam, 2)
	require.Len(t, hedef, 1)
	assert.Equal(t, "prule_2", hedef[0].ID)
}

func TestGecerlilikYardimcilari(t *testing.T) {
	assert.True(t, models.BudgetSpend.Valid())
	assert.False(t, models.CampaignBudgetType("olmayan").Valid())

	assert.True(t, models.PromotionBuyGet.Valid())
	assert.False(t, models.PromotionType("olmayan").Valid())

	assert.True(t, models.PromotionActive.Valid())
	assert.False(t, models.PromotionStatus("olmayan").Valid())

	assert.True(t, models.MethodPercentage.Valid())
	assert.False(t, models.ApplicationMethodType("olmayan").Valid())

	assert.True(t, models.TargetOrder.Valid())
	assert.False(t, models.ApplicationTargetType("olmayan").Valid())

	assert.True(t, models.AllocationAcross.Valid())
	assert.False(t, models.Allocation("olmayan").Valid())

	assert.True(t, models.RuleTarget.Valid())
	assert.False(t, models.RuleType("olmayan").Valid())
}

func TestRuleOperatorOzellikleri(t *testing.T) {
	assert.True(t, models.OpGte.Valid())
	assert.False(t, models.RuleOperator("olmayan").Valid())

	assert.True(t, models.OpLt.Numeric())
	assert.False(t, models.OpEq.Numeric())
	assert.False(t, models.RuleOperator("olmayan").Numeric())

	assert.True(t, models.OpIn.MultiValue())
	assert.True(t, models.OpNin.MultiValue())
	assert.False(t, models.OpEq.MultiValue())
}

func TestRedemptionReleased(t *testing.T) {
	assert.False(t, models.Redemption{}.Released())
	assert.True(t, models.Redemption{ReleasedAt: ptr(an)}.Released())
}

func TestKimliklerOneklidirVeZamanSiralidir(t *testing.T) {
	uretici := map[string]func(time.Time) string{
		models.CampaignIDPrefix:          models.NewCampaignID,
		models.PromotionIDPrefix:         models.NewPromotionID,
		models.PromotionRuleIDPrefix:     models.NewPromotionRuleID,
		models.ApplicationMethodIDPrefix: models.NewApplicationMethodID,
		models.RedemptionIDPrefix:        models.NewRedemptionID,
	}

	for prefix, yap := range uretici {
		id := yap(an)
		assert.True(t, strings.HasPrefix(id, prefix), "%q öneki bekleniyordu: %s", prefix, id)
		assert.Len(t, id, len(prefix)+models.IDBodyLength())
	}

	erken := models.NewPromotionID(an)
	gec := models.NewPromotionID(an.Add(time.Second))
	assert.Less(t, erken, gec,
		"kimlikler sözlüksel olarak zaman sırasını taşımalı; uygulama sırası buna dayanır")
}

func TestNewIDTekildir(t *testing.T) {
	gorulen := make(map[string]struct{}, 1000)
	for range 1000 {
		id := models.NewPromotionID(an)
		_, tekrar := gorulen[id]
		require.False(t, tekrar, "aynı milisaniyede üretilen kimlikler çakışmamalı")
		gorulen[id] = struct{}{}
	}
}

func TestNewIDEpokOncesiZamanTabanaCekilir(t *testing.T) {
	id := models.NewID(models.PromotionIDPrefix, time.Unix(-1000, 0))

	assert.True(t, strings.HasPrefix(id, models.PromotionIDPrefix))
	assert.Len(t, id, len(models.PromotionIDPrefix)+models.IDBodyLength(),
		"1970 öncesi damga sıralamayı bozmamalı ve kimlik yine üretilmeli")
}
