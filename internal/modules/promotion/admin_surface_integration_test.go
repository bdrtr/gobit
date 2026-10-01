//go:build integration

package promotion_test

import (
	"context"
	"encoding/json"
	"testing"

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
