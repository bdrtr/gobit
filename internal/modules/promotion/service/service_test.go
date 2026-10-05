package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository"
)

func TestAnUnconfiguredServiceReturnsATypedError(t *testing.T) {
	var svc *Service

	_, err := svc.ComputeDiscounts(context.Background(), ComputeInput{CurrencyCode: "TRY"})

	require.Error(t, err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err))
	assert.Equal(t, CodeUnconfigured, errors.CodeOf(err))
}

func TestCreatePromotionUppercasesTheCode(t *testing.T) {
	repo := newMemRepo()

	promo, err := newTestService(repo).CreatePromotion(context.Background(), PromotionInput{
		Code: " yaz-20 ",
	})
	require.NoError(t, err)

	assert.Equal(t, "YAZ-20", promo.Code, "the coupon code is stored normalized to upper case")
	assert.Equal(t, models.PromotionDraft, promo.Status,
		"without a status it becomes a DRAFT; an incomplete request must not go live by accident")
	assert.Equal(t, models.PromotionStandard, promo.Type)
	assert.Equal(t, map[string]string{}, promo.Metadata)
}

func TestCreatePromotionTheSameCodeCannotBeTakenTwice(t *testing.T) {
	repo := newMemRepo()
	svc := newTestService(repo)

	_, err := svc.CreatePromotion(context.Background(), PromotionInput{Code: "YAZ20"})
	require.NoError(t, err)

	_, err = svc.CreatePromotion(context.Background(), PromotionInput{Code: "yaz20"})
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err),
		"the code is UNIQUE regardless of case")
}

func TestABuygetPromotionCanBeActivated(t *testing.T) {
	repo := newMemRepo()
	svc := newTestService(repo)

	active, err := svc.CreatePromotion(context.Background(), PromotionInput{
		Code: "BUYGET", Type: models.PromotionBuyGet, Status: models.PromotionActive,
	})

	require.NoError(t, err, "the mechanic has arrived (ADR 0112); the type no longer blocks going live")
	assert.Equal(t, models.PromotionBuyGet, active.Type)
	assert.Equal(t, models.PromotionActive, active.Status)
}

func TestTheRewardQuantityPairIsGivenWholeOrNotAtAll(t *testing.T) {
	two := int64(2)

	tests := []struct {
		name    string
		in      ApplicationMethodInput
		wantErr bool
	}{
		{
			name: "only the buy quantity",
			in: ApplicationMethodInput{
				Type: models.MethodPercentage, TargetType: models.TargetItems,
				Value: 10000, BuyQuantity: &two,
			},
			wantErr: true,
		},
		{
			name: "only the reward quantity",
			in: ApplicationMethodInput{
				Type: models.MethodPercentage, TargetType: models.TargetItems,
				Value: 10000, ApplyToQuantity: &two,
			},
			wantErr: true,
		},
		{
			name: "both",
			in: ApplicationMethodInput{
				Type: models.MethodPercentage, TargetType: models.TargetItems,
				Value: 10000, BuyQuantity: &two, ApplyToQuantity: &two,
			},
		},
		{
			name: "neither",
			in: ApplicationMethodInput{
				Type: models.MethodPercentage, TargetType: models.TargetItems, Value: 1000,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildApplicationMethod("appm_1", "promo_1", tt.in, time.Now().UTC())
			if tt.wantErr {
				require.Error(t, err, "half a reward is a condition that is either unearned or rewardless")
				assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestPromotionInputValidation(t *testing.T) {
	longValue := strings.Repeat("x", MaxMetadataValueLen+1)

	tests := []struct {
		name   string
		in     PromotionInput
		reason string
	}{
		{name: "short code", in: PromotionInput{Code: "AB"}, reason: "a code is at least three characters"},
		{name: "code with a space", in: PromotionInput{Code: "YAZ 20"}, reason: "a code cannot contain a space"},
		{name: "long code", in: PromotionInput{Code: strings.Repeat("A", MaxCodeLen+1)}, reason: "code limit"},
		{
			name:   "undefined type",
			in:     PromotionInput{Code: "YAZ20", Type: "olmayan"},
			reason: "an undefined type is refused",
		},
		{
			name:   "undefined status",
			in:     PromotionInput{Code: "YAZ20", Status: "olmayan"},
			reason: "an undefined status is refused",
		},
		{
			name:   "campaign id with the wrong prefix",
			in:     PromotionInput{Code: "YAZ20", CampaignID: ptr("promo_yanlis")},
			reason: "the prefix check catches an id of the wrong kind",
		},
		{
			name:   "negative usage limit",
			in:     PromotionInput{Code: "YAZ20", UsageLimit: ptr(int64(-1))},
			reason: "a negative limit is meaningless",
		},
		{
			name:   "long metadata value",
			in:     PromotionInput{Code: "YAZ20", Metadata: map[string]string{"not": longValue}},
			reason: "metadata value limit",
		},
		{
			name:   "empty metadata key",
			in:     PromotionInput{Code: "YAZ20", Metadata: map[string]string{"": "x"}},
			reason: "an empty key is meaningless",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newTestService(newMemRepo()).CreatePromotion(context.Background(), tt.in)
			require.Error(t, err, tt.reason)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), tt.reason)
		})
	}
}

func TestUpdatePromotionKeepsTheUsageCounter(t *testing.T) {
	repo := newMemRepo()
	svc := newTestService(repo)

	promo, err := svc.CreatePromotion(context.Background(), PromotionInput{Code: "YAZ20"})
	require.NoError(t, err)

	record := repo.promotions[promo.ID]
	record.UsageCount = 7
	repo.promotions[promo.ID] = record

	updated, err := svc.UpdatePromotion(context.Background(), promo.ID, PromotionInput{
		Code: "KIS20", Status: models.PromotionActive,
	})
	require.NoError(t, err)

	assert.Equal(t, "KIS20", updated.Code)
	assert.Equal(t, int64(7), updated.UsageCount,
		"only the redemption flow writes the counter; an admin update cannot reset it")
}

func TestUpdatePromotionIsAReplacement(t *testing.T) {
	repo := newMemRepo()
	svc := newTestService(repo)

	promo, err := svc.CreatePromotion(context.Background(), PromotionInput{
		Code: "YAZ20", CampaignID: ptr("camp_1"), UsageLimit: ptr(int64(5)),
	})
	require.NoError(t, err)
	require.NotNil(t, promo.CampaignID)

	updated, err := svc.UpdatePromotion(context.Background(), promo.ID, PromotionInput{Code: "YAZ20"})
	require.NoError(t, err)

	assert.Nil(t, updated.CampaignID, "a field that is not given is RESET; it is not a partial update")
	assert.Nil(t, updated.UsageLimit)
}

func TestCampaignBudgetValidation(t *testing.T) {
	base := CampaignInput{Name: "Yaz", CampaignIdentifier: "YAZ-2026"}

	tests := []struct {
		name   string
		mutate func(in *CampaignInput)
		reason string
	}{
		{
			name:   "no limit without a budget",
			mutate: func(in *CampaignInput) { in.BudgetLimit = ptr(int64(100)) },
			reason: "a budget type has to be chosen first",
		},
		{
			name: "no currency without a budget",
			mutate: func(in *CampaignInput) {
				in.BudgetCurrencyCode = "TRY"
			},
			reason: "a campaign without a budget has no currency",
		},
		{
			name: "a spend budget requires a limit",
			mutate: func(in *CampaignInput) {
				in.BudgetType = models.BudgetSpend
				in.BudgetCurrencyCode = "TRY"
			},
			reason: "for an unlimited budget the type has to be 'none'",
		},
		{
			name: "a spend budget requires a currency",
			mutate: func(in *CampaignInput) {
				in.BudgetType = models.BudgetSpend
				in.BudgetLimit = ptr(int64(100))
			},
			reason: "a money-measured budget is meaningless without a currency",
		},
		{
			name: "no currency on a usage budget",
			mutate: func(in *CampaignInput) {
				in.BudgetType = models.BudgetUsage
				in.BudgetLimit = ptr(int64(100))
				in.BudgetCurrencyCode = "TRY"
			},
			reason: "a count-measured budget has no currency",
		},
		{
			name: "negative limit",
			mutate: func(in *CampaignInput) {
				in.BudgetType = models.BudgetUsage
				in.BudgetLimit = ptr(int64(-1))
			},
			reason: "a negative budget is meaningless",
		},
		{
			name: "the limit cannot exceed the maximum amount",
			mutate: func(in *CampaignInput) {
				in.BudgetType = models.BudgetUsage
				in.BudgetLimit = ptr(models.MaxAmount + 1)
			},
			reason: "overflow protection",
		},
		{
			name:   "empty name",
			mutate: func(in *CampaignInput) { in.Name = "  " },
			reason: "the name cannot be empty",
		},
		{
			name: "start after end",
			mutate: func(in *CampaignInput) {
				in.StartsAt = ptr(testNow.Add(time.Hour))
				in.EndsAt = ptr(testNow)
			},
			reason: "an inverted window is meaningless",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := base
			tt.mutate(&in)

			_, err := newTestService(newMemRepo()).CreateCampaign(context.Background(), in)
			require.Error(t, err, tt.reason)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), tt.reason)
		})
	}
}

func TestCreateCampaignValidBudget(t *testing.T) {
	repo := newMemRepo()

	campaign, err := newTestService(repo).CreateCampaign(context.Background(), CampaignInput{
		Name:               "Yaz",
		CampaignIdentifier: "YAZ-2026",
		BudgetType:         models.BudgetSpend,
		BudgetLimit:        ptr(int64(100_000)),
		BudgetCurrencyCode: "try",
	})
	require.NoError(t, err)

	assert.Equal(t, "TRY", campaign.BudgetCurrencyCode, "the currency is normalized to upper case")
	assert.Zero(t, campaign.BudgetUsed, "the counter starts at zero and is not read from the input")
}

func TestCreateCampaignBusinessIdentifierIsUnique(t *testing.T) {
	repo := newMemRepo()
	svc := newTestService(repo)
	in := CampaignInput{Name: "Yaz", CampaignIdentifier: "YAZ-2026"}

	_, err := svc.CreateCampaign(context.Background(), in)
	require.NoError(t, err)

	_, err = svc.CreateCampaign(context.Background(), in)
	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}

func TestSetApplicationMethodValidation(t *testing.T) {
	tests := []struct {
		name   string
		in     ApplicationMethodInput
		reason string
	}{
		{
			name:   "undefined type",
			in:     ApplicationMethodInput{Type: "olmayan", TargetType: models.TargetItems},
			reason: "an undefined type is refused",
		},
		{
			name:   "undefined target",
			in:     ApplicationMethodInput{Type: models.MethodPercentage, TargetType: "olmayan"},
			reason: "an undefined target is refused",
		},
		{
			name: "a fixed discount requires a currency",
			in: ApplicationMethodInput{
				Type: models.MethodFixed, TargetType: models.TargetItems, Value: 100,
			},
			reason: "a fixed amount cannot be applied without a currency",
		},
		{
			name: "no currency on a percentage discount",
			in: ApplicationMethodInput{
				Type: models.MethodPercentage, TargetType: models.TargetItems,
				Value: 2000, CurrencyCode: "TRY",
			},
			reason: "a percentage carries no currency",
		},
		{
			name: "a percentage cannot exceed 100%",
			in: ApplicationMethodInput{
				Type: models.MethodPercentage, TargetType: models.TargetItems, Value: 10001,
			},
			reason: "the basis-point ceiling is 10000",
		},
		{
			name: "negative value",
			in: ApplicationMethodInput{
				Type: models.MethodPercentage, TargetType: models.TargetItems, Value: -1,
			},
			reason: "a negative discount is meaningless",
		},
		{
			name: "each is refused on the order target",
			in: ApplicationMethodInput{
				Type: models.MethodPercentage, TargetType: models.TargetOrder,
				Allocation: models.AllocationEach, Value: 1000,
			},
			reason: "a silent correction would perpetuate the operator's misconception",
		},
		{
			name: "maximum quantity limit",
			in: ApplicationMethodInput{
				Type: models.MethodFixed, TargetType: models.TargetItems,
				Value: 100, CurrencyCode: "TRY", MaxQuantity: ptr(models.MaxQuantity + 1),
			},
			reason: "the quantity ceiling cannot be exceeded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMemRepo()
			repo.promotions["promo_1"] = models.Promotion{ID: "promo_1", Code: "YAZ20"}

			_, err := newTestService(repo).SetApplicationMethod(context.Background(), "promo_1", tt.in)
			require.Error(t, err, tt.reason)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), tt.reason)
		})
	}
}

func TestSetApplicationMethodForcesAcrossOnTheOrderTarget(t *testing.T) {
	repo := newMemRepo()
	repo.promotions["promo_1"] = models.Promotion{ID: "promo_1", Code: "YAZ20"}

	method, err := newTestService(repo).SetApplicationMethod(context.Background(), "promo_1",
		ApplicationMethodInput{
			Type: models.MethodPercentage, TargetType: models.TargetOrder, Value: 1000,
		})
	require.NoError(t, err)

	assert.Equal(t, models.AllocationAcross, method.Allocation,
		"when no allocation is given the order target is forced to across")
}

func TestSetApplicationMethodMissingPromotionNotFound(t *testing.T) {
	_, err := newTestService(newMemRepo()).SetApplicationMethod(context.Background(), "promo_missing",
		ApplicationMethodInput{Type: models.MethodPercentage, TargetType: models.TargetItems, Value: 1000})

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"a foreign key violation would go out as a 'constraint error'; the early check says what it is")
}

func TestAddPromotionRuleValidation(t *testing.T) {
	tests := []struct {
		name   string
		in     RuleInput
		reason string
	}{
		{
			name:   "undefined rule type",
			in:     RuleInput{RuleType: "olmayan", Attribute: "a", Operator: models.OpEq, Values: []string{"x"}},
			reason: "an undefined type is refused",
		},
		{
			name:   "empty field name",
			in:     RuleInput{RuleType: models.RuleContext, Operator: models.OpEq, Values: []string{"x"}},
			reason: "the field name cannot be empty",
		},
		{
			name:   "undefined operator",
			in:     RuleInput{RuleType: models.RuleContext, Attribute: "a", Operator: "olmayan", Values: []string{"x"}},
			reason: "an undefined operator is refused",
		},
		{
			name:   "rule without values",
			in:     RuleInput{RuleType: models.RuleContext, Attribute: "a", Operator: models.OpEq},
			reason: "a rule requires at least one value",
		},
		{
			name: "two values for a single-valued operator",
			in: RuleInput{
				RuleType: models.RuleContext, Attribute: "a", Operator: models.OpEq,
				Values: []string{"x", "y"},
			},
			reason: "eq takes exactly one value",
		},
		{
			name: "non-numeric value for a numeric operator",
			in: RuleInput{
				RuleType: models.RuleContext, Attribute: "a", Operator: models.OpGt,
				Values: []string{"abc"},
			},
			reason: "a rule that cannot be converted to a number would silently stay dead",
		},
		{
			name: "empty value",
			in: RuleInput{
				RuleType: models.RuleContext, Attribute: "a", Operator: models.OpIn,
				Values: []string{"x", ""},
			},
			reason: "an empty value is meaningless",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMemRepo()
			repo.promotions["promo_1"] = models.Promotion{ID: "promo_1", Code: "YAZ20"}

			_, err := newTestService(repo).AddPromotionRule(context.Background(), "promo_1", tt.in)
			require.Error(t, err, tt.reason)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), tt.reason)
		})
	}
}

// TestAPromotionRuleCannotNameTheCartsBag is gap D261 (ADR 0407): a cart's
// metadata reaches no rule, so a rule on an attribute under the prefix it once
// filled would be stored and never met. Every rule type is refused before
// anything is written, and a name that merely starts like the prefix is not.
func TestAPromotionRuleCannotNameTheCartsBag(t *testing.T) {
	for _, ruleType := range []models.RuleType{models.RuleContext, models.RuleTarget, models.RuleBuy} {
		t.Run(string(ruleType), func(t *testing.T) {
			repo := newMemRepo()
			repo.promotions["promo_1"] = models.Promotion{ID: "promo_1", Code: "SUMMER20"}

			_, err := newTestService(repo).AddPromotionRule(context.Background(), "promo_1", RuleInput{
				RuleType: ruleType, Attribute: "cart.brand", Operator: models.OpEq, Values: []string{"A"},
			})

			require.Error(t, err, "a rule on the cart's bag is refused")
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
			assert.Equal(t, CodeRuleAttributeReserved, errors.CodeOf(err))
			assert.Empty(t, repo.rules["promo_1"], "nothing is written")
		})
	}

	for _, attribute := range []string{"cart_brand", "cartridge", "brand"} {
		t.Run("near miss "+attribute, func(t *testing.T) {
			repo := newMemRepo()
			repo.promotions["promo_1"] = models.Promotion{ID: "promo_1", Code: "SUMMER20"}

			_, err := newTestService(repo).AddPromotionRule(context.Background(), "promo_1", RuleInput{
				RuleType: models.RuleContext, Attribute: attribute, Operator: models.OpEq, Values: []string{"A"},
			})

			require.NoError(t, err, "%q is not under the prefix", attribute)
			assert.Len(t, repo.rules["promo_1"], 1)
		})
	}
}

func TestAddPromotionRuleCopiesTheValues(t *testing.T) {
	repo := newMemRepo()
	repo.promotions["promo_1"] = models.Promotion{ID: "promo_1", Code: "YAZ20"}

	values := []string{"vip", "b2b"}
	rule, err := newTestService(repo).AddPromotionRule(context.Background(), "promo_1", RuleInput{
		RuleType: models.RuleContext, Attribute: "customer_group_id",
		Operator: models.OpIn, Values: values,
	})
	require.NoError(t, err)

	values[0] = "degistirildi"
	assert.Equal(t, []string{"vip", "b2b"}, rule.Values,
		"changing the caller's slice afterwards must not corrupt the written rule")
}

func TestListPromotionRulesMissingPromotionNotFound(t *testing.T) {
	_, err := newTestService(newMemRepo()).ListPromotionRules(context.Background(), "promo_missing")

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"had an empty slice been returned, the client would think 'it has no rules'")
}

func TestPagingLimitsAreApplied(t *testing.T) {
	repo := newMemRepo()
	svc := newTestService(repo)
	for i := range 5 {
		_, err := svc.CreateCampaign(context.Background(), CampaignInput{
			Name: "K", CampaignIdentifier: string(rune('A' + i)),
		})
		require.NoError(t, err)
	}

	page, err := svc.ListCampaigns(context.Background(), 0, 0)
	require.NoError(t, err)
	assert.Equal(t, DefaultLimit, page.Limit, "without a limit the default is applied")
	assert.Equal(t, int64(5), page.Count)

	page, err = svc.ListCampaigns(context.Background(), MaxLimit+50, 0)
	require.NoError(t, err)
	assert.Equal(t, MaxLimit, page.Limit, "the maximum page size cannot be exceeded and the APPLIED value is reported")

	_, err = svc.ListCampaigns(context.Background(), 10, -1)
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

func TestLookupStoreCouponReturnsOnlyAUsableCoupon(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YAZ20"},
		percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))

	coupon, err := newTestService(repo).LookupStoreCoupon(context.Background(), "yaz20")
	require.NoError(t, err)

	assert.Equal(t, "YAZ20", coupon.Code)
	assert.Equal(t, models.MethodPercentage, coupon.MethodType)
	assert.Equal(t, int64(2000), coupon.Value)
	assert.Empty(t, coupon.CurrencyCode)
}

func TestLookupStoreCouponDoesNotLeak(t *testing.T) {
	closedCampaign := models.Campaign{
		ID: "camp_1", Name: "Ended", CampaignIdentifier: "BITMIS",
		BudgetType: models.BudgetNone, EndsAt: ptr(testNow.Add(-time.Hour)),
	}
	exhaustedCampaign := models.Campaign{
		ID: "camp_2", Name: "Exhausted", CampaignIdentifier: "TUKENMIS",
		BudgetType: models.BudgetUsage, BudgetLimit: ptr(int64(1)), BudgetUsed: 1,
	}

	tests := []struct {
		name   string
		setup  func(repo *memRepo)
		code   string
		reason string
	}{
		{
			name:   "nonexistent code",
			setup:  func(*memRepo) {},
			code:   "HICYOK",
			reason: "a nonexistent code returns not found",
		},
		{
			name: "draft promotion",
			setup: func(repo *memRepo) {
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "TASLAK", Status: models.PromotionDraft,
				}, percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))
			},
			code:   "TASLAK",
			reason: "a draft coupon must not APPEAR TO EXIST to the customer",
		},
		{
			name: "inactive promotion",
			setup: func(repo *memRepo) {
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "PASIF", Status: models.PromotionInactive,
				}, percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))
			},
			code:   "PASIF",
			reason: "an inactive coupon must not APPEAR TO EXIST to the customer",
		},
		{
			name: "its campaign has closed",
			setup: func(repo *memRepo) {
				repo.campaigns[closedCampaign.ID] = closedCampaign
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "KAPALI", CampaignID: ptr(closedCampaign.ID),
				}, percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))
			},
			code:   "KAPALI",
			reason: "the campaign schedule must not be given away",
		},
		{
			name: "its budget is exhausted",
			setup: func(repo *memRepo) {
				repo.campaigns[exhaustedCampaign.ID] = exhaustedCampaign
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "TUKENMIS", CampaignID: ptr(exhaustedCampaign.ID),
				}, percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))
			},
			code:   "TUKENMIS",
			reason: "the budget state must not be given away",
		},
		{
			name: "usage allowance exhausted",
			setup: func(repo *memRepo) {
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "BITTI", UsageLimit: ptr(int64(1)), UsageCount: 1,
				}, percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))
			},
			code:   "BITTI",
			reason: "the usage counter must not be given away",
		},
		{
			name: "no application method",
			setup: func(repo *memRepo) {
				seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YONTEMSIZ"}, nil)
			},
			code:   "YONTEMSIZ",
			reason: "a coupon without a method produces no discount",
		},
		{
			name:   "formally invalid code",
			setup:  func(*memRepo) {},
			code:   "a b",
			reason: "a format error counts as 'does not exist' too; format validation would narrow the search space",
		},
		{
			name: "buyget without a quantity pair",
			setup: func(repo *memRepo) {
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "YARIM", Type: models.PromotionBuyGet,
				}, percentageMethod("promo_1", 10000, models.TargetItems, models.AllocationEach))
			},
			code: "YARIM",
			reason: "a coupon the computation would eliminate must NOT BE OFFERED to the customer; " +
				"had it been offered, the customer would type the code and nothing would happen",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMemRepo()
			tt.setup(repo)

			_, err := newTestService(repo).LookupStoreCoupon(context.Background(), tt.code)

			require.Error(t, err, tt.reason)
			assert.Equal(t, errors.KindNotFound, errors.KindOf(err), tt.reason)
			assert.Equal(t, CodePromotionNotUsable, errors.CodeOf(err),
				"every reason has to return the SAME code; telling them apart would be a leak")
		})
	}
}

// TestLookupStoreCouponReturnsABuygetCouponWithItsMechanic pins the half that
// says what the coupon gives.
//
// The measure alone is misleading: a "buy 2, get one" coupon carries ten
// thousand basis points, and had the mechanic not been stated, the storefront
// would show it as "100% off".
func TestLookupStoreCouponReturnsABuygetCouponWithItsMechanic(t *testing.T) {
	repo := newMemRepo()
	method := percentageMethod("promo_1", 10000, models.TargetItems, models.AllocationEach)
	method.BuyQuantity = ptr(int64(2))
	method.ApplyToQuantity = ptr(int64(1))
	seedPromotion(repo, models.Promotion{
		ID: "promo_1", Code: "AL2KAZAN1", Type: models.PromotionBuyGet,
	}, method)

	coupon, err := newTestService(repo).LookupStoreCoupon(context.Background(), "al2kazan1")
	require.NoError(t, err, "a configured buyget coupon EXISTS for the customer")

	assert.Equal(t, models.PromotionBuyGet, coupon.Mechanic)
	require.NotNil(t, coupon.BuyQuantity)
	require.NotNil(t, coupon.ApplyToQuantity)
	assert.Equal(t, int64(2), *coupon.BuyQuantity)
	assert.Equal(t, int64(1), *coupon.ApplyToQuantity)
}

func TestGetPromotionByCodeAdminSeesTheDraft(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{
		ID: "promo_1", Code: "TASLAK", Status: models.PromotionDraft,
	}, nil)

	promo, err := newTestService(repo).GetPromotionByCode(context.Background(), "taslak")
	require.NoError(t, err)

	assert.Equal(t, models.PromotionDraft, promo.Status,
		"the operator must be able to see a draft promotion; the filter is only on the customer surface")
}

// TestLookupStoreCouponReturnsACouponOfAnOpenCampaign pins the POSITIVE side of
// [storeCandidate]'s campaign read.
//
// The other store tests check only the REFUSAL side (closed campaign,
// exhausted budget -> 404). A change that deleted the read entirely would leave
// candidate.Campaign nil, [campaignUsable] would return false and EVERY coupon
// tied to a campaign would look "nonexistent" to the customer — no REFUSAL test
// can catch that.
func TestLookupStoreCouponReturnsACouponOfAnOpenCampaign(t *testing.T) {
	campaign := models.Campaign{
		ID: "camp_1", Name: "Yaz", CampaignIdentifier: "YAZ",
		StartsAt:   ptr(testNow.Add(-time.Hour)),
		EndsAt:     ptr(testNow.Add(time.Hour)),
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(100_000)),
		BudgetUsed: 25_000, BudgetCurrencyCode: "TRY",
	}
	repo := newMemRepo()
	repo.campaigns[campaign.ID] = campaign
	seedPromotion(repo, models.Promotion{
		ID: "promo_1", Code: "YAZ20", CampaignID: ptr(campaign.ID),
	}, fixedMethod("promo_1", 1500, models.TargetItems, models.AllocationEach))

	coupon, err := newTestService(repo).LookupStoreCoupon(context.Background(), "yaz20")
	require.NoError(t, err, "the coupon of a campaign whose window is open and whose budget remains is VISIBLE to the customer")

	assert.Equal(t, "YAZ20", coupon.Code)
	assert.Equal(t, models.MethodFixed, coupon.MethodType)
	assert.Equal(t, models.TargetItems, coupon.TargetType)
	assert.Equal(t, int64(1500), coupon.Value)
	assert.Equal(t, "TRY", coupon.CurrencyCode)
}

// TestUpdateCampaignBudgetUnitCannotChangeWhileTheCounterIsNonZero pins the
// silent accounting corruption that comes from the counter staying in the old
// unit (see [Service.UpdateCampaign]).
func TestUpdateCampaignBudgetUnitCannotChangeWhileTheCounterIsNonZero(t *testing.T) {
	countCampaign := models.Campaign{
		ID: "camp_1", Name: "Adet", CampaignIdentifier: "ADET",
		BudgetType: models.BudgetUsage, BudgetLimit: ptr(int64(100)), BudgetUsed: 30,
	}
	moneyCampaign := models.Campaign{
		ID: "camp_1", Name: "Para", CampaignIdentifier: "PARA",
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(100_000)),
		BudgetUsed: 30_000, BudgetCurrencyCode: "TRY",
	}

	tests := []struct {
		name     string
		existing models.Campaign
		request  CampaignInput
		reason   string
	}{
		{
			name:     "type from count to money",
			existing: countCampaign,
			request: CampaignInput{
				Name: "Adet", CampaignIdentifier: "ADET",
				BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(100_000)),
				BudgetCurrencyCode: "TRY",
			},
			reason: "the 30 UNITS in the counter would be read as 30 MINOR UNITS once the type changed",
		},
		{
			name:     "currency changes",
			existing: moneyCampaign,
			request: CampaignInput{
				Name: "Para", CampaignIdentifier: "PARA",
				BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(100_000)),
				BudgetCurrencyCode: "USD",
			},
			reason: "the earlier TRY spend would count as USD and TRY redemptions would start being refused",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMemRepo()
			repo.campaigns[tt.existing.ID] = tt.existing

			_, err := newTestService(repo).UpdateCampaign(context.Background(), tt.existing.ID, tt.request)

			require.Error(t, err, tt.reason)
			assert.Equal(t, errors.KindConflict, errors.KindOf(err), tt.reason)
			assert.Equal(t, repository.CodeBudgetUnitLocked, errors.CodeOf(err))
			assert.Equal(t, tt.existing.BudgetType, repo.campaigns[tt.existing.ID].BudgetType,
				"a refused update changes no field")
			assert.Equal(t, tt.existing.BudgetCurrencyCode, repo.campaigns[tt.existing.ID].BudgetCurrencyCode)
		})
	}
}

// TestUpdateCampaignDefinitionCanChangeWhileTheCounterIsNonZero pins that the
// lock is NARROW: what freezes is only the budget's unit, not the campaign's
// definition.
func TestUpdateCampaignDefinitionCanChangeWhileTheCounterIsNonZero(t *testing.T) {
	repo := newMemRepo()
	repo.campaigns["camp_1"] = models.Campaign{
		ID: "camp_1", Name: "Eski", CampaignIdentifier: "YAZ",
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(100_000)),
		BudgetUsed: 30_000, BudgetCurrencyCode: "TRY",
	}

	campaign, err := newTestService(repo).UpdateCampaign(context.Background(), "camp_1", CampaignInput{
		Name: "Yeni", CampaignIdentifier: "YAZ", Description: "updated",
		EndsAt:     ptr(testNow.Add(48 * time.Hour)),
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(250_000)),
		BudgetCurrencyCode: "TRY",
	})
	require.NoError(t, err, "the name, description, window and budget LIMIT are independent of the counter")

	assert.Equal(t, "Yeni", campaign.Name)
	assert.Equal(t, int64(250_000), *campaign.BudgetLimit)
	assert.Equal(t, int64(30_000), campaign.BudgetUsed, "the counter does not change through this path")
}

// TestUpdateCampaignBudgetUnitCanChangeWhileTheCounterIsZero pins that the lock
// binds only while the counter is non-zero; a campaign that was never used
// must be freely redefinable.
func TestUpdateCampaignBudgetUnitCanChangeWhileTheCounterIsZero(t *testing.T) {
	repo := newMemRepo()
	repo.campaigns["camp_1"] = models.Campaign{
		ID: "camp_1", Name: "Adet", CampaignIdentifier: "ADET",
		BudgetType: models.BudgetUsage, BudgetLimit: ptr(int64(100)),
	}

	campaign, err := newTestService(repo).UpdateCampaign(context.Background(), "camp_1", CampaignInput{
		Name: "Para", CampaignIdentifier: "ADET",
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(100_000)),
		BudgetCurrencyCode: "TRY",
	})
	require.NoError(t, err)

	assert.Equal(t, models.BudgetSpend, campaign.BudgetType)
	assert.Equal(t, "TRY", campaign.BudgetCurrencyCode)
}
