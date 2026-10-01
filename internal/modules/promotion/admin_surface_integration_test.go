//go:build integration

package promotion_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// TestThePanelListsThePromotionsInAStatus is ADR 0311 against a real
// PostgreSQL: the panel's surface lists the promotions in the status asked
// for — drafts included, which the read provider never returns — with their
// usage and their limit, and counts the status's total; a status the module
// does not know is refused.
func TestThePanelListsThePromotionsInAStatus(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	surface := promotion.NewAdminSurface(svc)

	limit := int64(50)
	draft, err := svc.CreatePromotion(ctx, service.PromotionInput{
		Code: uniqueCode(), Status: models.PromotionDraft, UsageLimit: &limit,
	})
	require.NoError(t, err)
	active := activePromotion(ctx, t, svc, service.PromotionInput{IsAutomatic: true})
	// An active automatic promotion applies to every cart the package's other
	// tests compute in this shared database; it is not left behind (D203).
	t.Cleanup(func() { require.NoError(t, svc.DeletePromotion(context.Background(), active.ID)) })
	_, err = svc.RedeemPromotion(ctx, service.RedeemInput{
		PromotionID: active.ID, Reference: "order_admin_surface", Amount: 100, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	type row struct {
		ID          string `json:"id"`
		Code        string `json:"code"`
		IsAutomatic bool   `json:"is_automatic"`
		Status      string `json:"status"`
		UsageCount  int64  `json:"usage_count"`
		UsageLimit  *int64 `json:"usage_limit"`
	}
	listed := func(status string) (map[string]row, int64) {
		t.Helper()

		raw, total, err := surface.PromotionsJSON(ctx, status, 100, 0)
		require.NoError(t, err)
		var rows []row
		require.NoError(t, json.Unmarshal(raw, &rows))
		out := map[string]row{}
		for _, r := range rows {
			assert.Equal(t, status, r.Status, "only the status asked for")
			out[r.ID] = r
		}

		return out, total
	}

	drafts, draftTotal := listed("draft")
	require.Contains(t, drafts, draft.ID, "a draft is listed to the operator")
	assert.Equal(t, draft.Code, drafts[draft.ID].Code)
	require.NotNil(t, drafts[draft.ID].UsageLimit)
	assert.Equal(t, int64(50), *drafts[draft.ID].UsageLimit)
	assert.Zero(t, drafts[draft.ID].UsageCount)
	assert.GreaterOrEqual(t, draftTotal, int64(len(drafts)))
	assert.NotContains(t, drafts, active.ID)

	actives, _ := listed("active")
	require.Contains(t, actives, active.ID)
	assert.True(t, actives[active.ID].IsAutomatic)
	assert.Equal(t, int64(1), actives[active.ID].UsageCount, "the use the redemption counted")
	assert.Nil(t, actives[active.ID].UsageLimit, "no limit is none")

	_, _, err = surface.PromotionsJSON(ctx, "on-sale", 10, 0)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
}

// TestThePanelSwitchesAStatusFromTheOneItRead is ADR 0312 against a real
// PostgreSQL: the switch moves the promotion only from the status the
// operator read, so a second operator pausing the same coupon is refused
// rather than writing over the first; it writes the status alone, keeping an
// edit made meanwhile; and a deleted or unknown promotion is not found.
func TestThePanelSwitchesAStatusFromTheOneItRead(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	surface := promotion.NewAdminSurface(svc)

	code := uniqueCode()
	promo, err := svc.CreatePromotion(ctx, service.PromotionInput{Code: code, Status: models.PromotionActive})
	require.NoError(t, err)
	bystander, err := svc.CreatePromotion(ctx, service.PromotionInput{Code: uniqueCode(), Status: models.PromotionActive})
	require.NoError(t, err)

	// Another operator raises the limit after the list was drawn.
	limit := int64(7)
	edited, err := svc.UpdatePromotion(ctx, promo.ID, service.PromotionInput{
		Code: code, Status: models.PromotionActive, UsageLimit: &limit,
	})
	require.NoError(t, err)

	require.NoError(t, surface.SwitchPromotionStatus(ctx, promo.ID, "active", "inactive"))
	switched, err := svc.GetPromotion(ctx, promo.ID)
	require.NoError(t, err)
	assert.Equal(t, models.PromotionInactive, switched.Status)
	require.NotNil(t, switched.UsageLimit, "the edit made meanwhile is kept")
	assert.Equal(t, int64(7), *switched.UsageLimit)
	assert.True(t, switched.UpdatedAt.After(edited.UpdatedAt), "the switch stamps the promotion")

	other, err := svc.GetPromotion(ctx, bystander.ID)
	require.NoError(t, err)
	assert.Equal(t, models.PromotionActive, other.Status, "only the named promotion moves")

	err = surface.SwitchPromotionStatus(ctx, promo.ID, "active", "draft")
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "a second operator who read it active is refused: %v", err)
	assert.Equal(t, service.CodeStatusMoved, errors.CodeOf(err))
	again, err := svc.GetPromotion(ctx, promo.ID)
	require.NoError(t, err)
	assert.Equal(t, models.PromotionInactive, again.Status, "the refused switch wrote nothing")

	require.NoError(t, svc.DeletePromotion(ctx, bystander.ID))
	err = surface.SwitchPromotionStatus(ctx, bystander.ID, "active", "inactive")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a deleted promotion is not switched: %v", err)

	err = surface.SwitchPromotionStatus(ctx, models.NewPromotionID(time.Now()), "active", "inactive")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "an unknown promotion is not found: %v", err)
}

// TestThePanelReadsAPromotionsPage is ADR 0313 against a real PostgreSQL: the
// page carries the promotion's discount, its rules, its campaign and its
// latest uses newest first, a released one included; a promotion with no
// discount or campaign carries null for each, and an unknown one is not found.
func TestThePanelReadsAPromotionsPage(t *testing.T) {
	ctx := context.Background()
	clock := time.Now().UTC()
	svc := service.New(repository.New(testPool.Pool()), service.Options{Now: func() time.Time {
		clock = clock.Add(time.Millisecond)
		return clock
	}})
	surface := promotion.NewAdminSurface(svc)

	limit := int64(100)
	campaign, err := svc.CreateCampaign(ctx, service.CampaignInput{
		Name: "Spring", CampaignIdentifier: "spring-" + uniqueCode(),
		BudgetType: models.BudgetUsage, BudgetLimit: &limit,
	})
	require.NoError(t, err)
	promo := activePromotion(ctx, t, svc, service.PromotionInput{CampaignID: &campaign.ID})
	_, err = svc.AddPromotionRule(ctx, promo.ID, service.RuleInput{
		RuleType: models.RuleContext, Attribute: "currency_code", Operator: models.OpIn, Values: []string{"TRY", "EUR"},
	})
	require.NoError(t, err)
	for _, ref := range []string{"order_first", "order_second", "order_third"} {
		_, err = svc.RedeemPromotion(ctx, service.RedeemInput{
			PromotionID: promo.ID, Reference: ref, Amount: 250, CurrencyCode: "TRY",
		})
		require.NoError(t, err)
	}
	released, err := svc.ReleasePromotion(ctx, service.ReleaseInput{PromotionID: promo.ID, Reference: "order_second"})
	require.NoError(t, err)
	require.True(t, released)

	type page struct {
		ID       string `json:"id"`
		Campaign *struct {
			Name        string `json:"name"`
			BudgetType  string `json:"budget_type"`
			BudgetLimit *int64 `json:"budget_limit"`
			BudgetUsed  int64  `json:"budget_used"`
		} `json:"campaign"`
		Method *struct {
			Type       string `json:"type"`
			TargetType string `json:"target_type"`
			Allocation string `json:"allocation"`
			Value      int64  `json:"value"`
		} `json:"application_method"`
		Rules []struct {
			Type      string   `json:"type"`
			Attribute string   `json:"attribute"`
			Operator  string   `json:"operator"`
			Values    []string `json:"values"`
		} `json:"rules"`
		Uses []struct {
			Reference  string     `json:"reference"`
			Amount     int64      `json:"amount"`
			ReleasedAt *time.Time `json:"released_at"`
		} `json:"latest_uses"`
	}
	read := func(id string) page {
		t.Helper()

		raw, err := surface.PromotionJSON(ctx, id)
		require.NoError(t, err)
		var p page
		require.NoError(t, json.Unmarshal(raw, &p))

		return p
	}

	full := read(promo.ID)
	assert.Equal(t, promo.ID, full.ID)
	require.NotNil(t, full.Method)
	assert.Equal(t, "percentage", full.Method.Type)
	assert.Equal(t, "items", full.Method.TargetType)
	assert.Equal(t, "each", full.Method.Allocation)
	assert.Equal(t, int64(2000), full.Method.Value)
	require.NotNil(t, full.Campaign)
	assert.Equal(t, "Spring", full.Campaign.Name)
	assert.Equal(t, "usage", full.Campaign.BudgetType)
	require.NotNil(t, full.Campaign.BudgetLimit)
	assert.Equal(t, int64(100), *full.Campaign.BudgetLimit)
	assert.Equal(t, int64(2), full.Campaign.BudgetUsed, "three uses, one given back")
	require.Len(t, full.Rules, 1)
	assert.Equal(t, "context", full.Rules[0].Type)
	assert.Equal(t, "currency_code", full.Rules[0].Attribute)
	assert.Equal(t, "in", full.Rules[0].Operator)
	assert.Equal(t, []string{"TRY", "EUR"}, full.Rules[0].Values)
	require.Len(t, full.Uses, 3)
	assert.Equal(t, []string{"order_third", "order_second", "order_first"},
		[]string{full.Uses[0].Reference, full.Uses[1].Reference, full.Uses[2].Reference}, "newest first")
	assert.Equal(t, int64(250), full.Uses[0].Amount)
	assert.Nil(t, full.Uses[0].ReleasedAt)
	assert.NotNil(t, full.Uses[1].ReleasedAt, "a use given back stays in the history, marked")

	bare, err := svc.CreatePromotion(ctx, service.PromotionInput{Code: uniqueCode()})
	require.NoError(t, err)
	empty := read(bare.ID)
	assert.Nil(t, empty.Method, "a promotion with no discount applies nothing")
	assert.Nil(t, empty.Campaign)
	assert.Empty(t, empty.Rules)
	assert.Empty(t, empty.Uses)
	raw, err := surface.PromotionJSON(ctx, bare.ID)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"rules":[]`, "an empty list is a list, not null")

	_, err = surface.PromotionJSON(ctx, models.NewPromotionID(time.Now()))
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "an unknown promotion is not found: %v", err)
}

// TestTheLatestUsesAreTheNewest holds the query's order and its bound on a
// real PostgreSQL: more uses than the page shows yields the newest ones. An
// identifier carries the millisecond it was written in, so the clock moves
// one millisecond per use; two uses in one millisecond have no order.
func TestTheLatestUsesAreTheNewest(t *testing.T) {
	ctx := context.Background()
	clock := time.Now().UTC()
	svc := service.New(repository.New(testPool.Pool()), service.Options{Now: func() time.Time {
		clock = clock.Add(time.Millisecond)
		return clock
	}})

	promo := activePromotion(ctx, t, svc, service.PromotionInput{})
	for i := range 25 {
		_, err := svc.RedeemPromotion(ctx, service.RedeemInput{
			PromotionID: promo.ID, Reference: fmt.Sprintf("order_%02d", i), Amount: 1, CurrencyCode: "TRY",
		})
		require.NoError(t, err)
	}

	uses, err := svc.LatestRedemptions(ctx, promo.ID, 20)
	require.NoError(t, err)
	require.Len(t, uses, 20)
	assert.Equal(t, "order_24", uses[0].Reference)
	assert.Equal(t, "order_05", uses[19].Reference, "the five oldest are left out")
}

// TestThePanelWritesAndListsCampaigns is ADR 0319 against a real PostgreSQL:
// the surface writes a campaign with its window and budget, lists the
// campaigns in the order they were written with how much of each budget a
// use consumed, refuses an identifier a live campaign holds by naming it, and
// a budget the module does not accept.
func TestThePanelWritesAndListsCampaigns(t *testing.T) {
	ctx := context.Background()
	// A campaign's id is ordered to the millisecond, so two written in one
	// would be listed in either order; the clock moves a millisecond a call.
	clock := time.Now().UTC()
	svc := service.New(repository.New(testPool.Pool()), service.Options{Now: func() time.Time {
		clock = clock.Add(time.Millisecond)
		return clock
	}})
	surface := promotion.NewAdminSurface(svc)

	starts := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	ends := starts.Add(96 * time.Hour)
	limit, uses := int64(500000), int64(100)
	sale, loyalty := "bf-"+uniqueCode(), "loyal-"+uniqueCode()
	saleID, err := surface.CreateCampaign(ctx, "Black Friday", sale, "the November sale",
		&starts, &ends, "spend", &limit, "TRY")
	require.NoError(t, err)
	loyaltyID, err := surface.CreateCampaign(ctx, "Loyalty", loyalty, "", nil, nil, "usage", &uses, "")
	require.NoError(t, err)
	promo := activePromotion(ctx, t, svc, service.PromotionInput{CampaignID: &saleID})
	_, err = svc.RedeemPromotion(ctx, service.RedeemInput{
		PromotionID: promo.ID, Reference: "order_" + uniqueCode(), Amount: 1250, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	_, total, err := surface.CampaignsJSON(ctx, 1, 0)
	require.NoError(t, err)
	require.GreaterOrEqual(t, total, int64(2))
	raw, _, err := surface.CampaignsJSON(ctx, 2, int32(total-2))
	require.NoError(t, err)
	var rows []struct {
		ID                 string     `json:"id"`
		Name               string     `json:"name"`
		CampaignIdentifier string     `json:"campaign_identifier"`
		Description        string     `json:"description"`
		StartsAt           *time.Time `json:"starts_at"`
		EndsAt             *time.Time `json:"ends_at"`
		BudgetType         string     `json:"budget_type"`
		BudgetLimit        *int64     `json:"budget_limit"`
		BudgetUsed         int64      `json:"budget_used"`
		BudgetCurrencyCode string     `json:"budget_currency_code"`
	}
	require.NoError(t, json.Unmarshal(raw, &rows))
	require.Len(t, rows, 2)
	assert.Equal(t, []string{saleID, loyaltyID}, []string{rows[0].ID, rows[1].ID}, "in the order they were written")
	first := rows[0]
	assert.Equal(t, "Black Friday|"+sale+"|the November sale", first.Name+"|"+first.CampaignIdentifier+"|"+first.Description)
	require.NotNil(t, first.StartsAt)
	require.NotNil(t, first.EndsAt)
	assert.True(t, starts.Equal(*first.StartsAt) && ends.Equal(*first.EndsAt), "the window as written")
	require.NotNil(t, first.BudgetLimit)
	assert.Equal(t, "spend|500000|1250|TRY", fmt.Sprintf("%s|%d|%d|%s",
		first.BudgetType, *first.BudgetLimit, first.BudgetUsed, first.BudgetCurrencyCode))
	assert.Nil(t, rows[1].StartsAt, "an open window")
	assert.Equal(t, "usage", rows[1].BudgetType)

	_, err = surface.CreateCampaign(ctx, "Again", sale, "", nil, nil, "none", nil, "")
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "a live campaign's identifier: %v", err)
	assert.Contains(t, err.Error(), "a campaign with the identifier "+sale+" exists")
	assert.NotContains(t, err.Error(), "campaign_identifier_uniq", "the constraint's name is not the operator's")

	_, err = surface.CreateCampaign(ctx, "No currency", "nc-"+uniqueCode(), "", nil, nil, "spend", &limit, "")
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a money budget without its currency: %v", err)
}

// TestThePanelPutsAPromotionIntoACampaign is ADR 0320 against a real
// PostgreSQL: the promotion moves into a campaign, to another and out of any,
// each from the one the operator read and writing the campaign alone; a
// promotion moved since and a deleted campaign are refused, each by its code.
func TestThePanelPutsAPromotionIntoACampaign(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	surface := promotion.NewAdminSurface(svc)

	spring, err := svc.CreateCampaign(ctx, service.CampaignInput{Name: "Spring", CampaignIdentifier: "spring-" + uniqueCode()})
	require.NoError(t, err)
	summer, err := svc.CreateCampaign(ctx, service.CampaignInput{Name: "Summer", CampaignIdentifier: "summer-" + uniqueCode()})
	require.NoError(t, err)
	limit := int64(7)
	promo := activePromotion(ctx, t, svc, service.PromotionInput{UsageLimit: &limit})
	campaignOf := func() *string {
		t.Helper()
		current, err := svc.GetPromotion(ctx, promo.ID)
		require.NoError(t, err)
		require.NotNil(t, current.UsageLimit, "the promotion's other fields are kept")
		assert.Equal(t, limit, *current.UsageLimit)
		return current.CampaignID
	}

	require.NoError(t, surface.SetPromotionCampaign(ctx, promo.ID, "", spring.ID))
	require.NotNil(t, campaignOf())
	assert.Equal(t, spring.ID, *campaignOf())

	err = surface.SetPromotionCampaign(ctx, promo.ID, "", summer.ID)
	require.Error(t, err)
	assert.Equal(t, service.CodeCampaignMoved, errors.CodeOf(err), "read out of none, it is in spring: %v", err)
	assert.Equal(t, spring.ID, *campaignOf(), "a refused move leaves it where it was")

	require.NoError(t, surface.SetPromotionCampaign(ctx, promo.ID, spring.ID, summer.ID))
	assert.Equal(t, summer.ID, *campaignOf())

	require.NoError(t, svc.DeleteCampaign(ctx, spring.ID))
	err = surface.SetPromotionCampaign(ctx, promo.ID, summer.ID, spring.ID)
	require.Error(t, err)
	assert.Equal(t, service.CodeCampaignGone, errors.CodeOf(err), "a deleted campaign is not live: %v", err)
	assert.Equal(t, summer.ID, *campaignOf())

	// D205: the promotion still names a campaign deleted under it, its page
	// says which, and the move out of it starts from that campaign.
	require.NoError(t, svc.DeleteCampaign(ctx, summer.ID))
	raw, err := surface.PromotionJSON(ctx, promo.ID)
	require.NoError(t, err)
	var page struct {
		CampaignID *string         `json:"campaign_id"`
		Campaign   json.RawMessage `json:"campaign"`
	}
	require.NoError(t, json.Unmarshal(raw, &page))
	require.NotNil(t, page.CampaignID, "the page carries the campaign the promotion names")
	assert.Equal(t, summer.ID, *page.CampaignID)
	assert.JSONEq(t, "null", string(page.Campaign), "a deleted campaign reads as null")

	require.NoError(t, surface.SetPromotionCampaign(ctx, promo.ID, summer.ID, ""))
	assert.Nil(t, campaignOf(), "out of any campaign")

	autumn, err := svc.CreateCampaign(ctx, service.CampaignInput{Name: "Autumn", CampaignIdentifier: "autumn-" + uniqueCode()})
	require.NoError(t, err)
	require.NoError(t, svc.DeletePromotion(ctx, promo.ID))
	err = surface.SetPromotionCampaign(ctx, promo.ID, "", autumn.ID)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a deleted promotion is not moved: %v", err)
}
