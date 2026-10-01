//go:build integration

package promotion_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
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
