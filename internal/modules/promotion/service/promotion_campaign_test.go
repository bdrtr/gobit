package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// SetPromotionCampaign mirrors the query: the campaign changes only from the
// one the caller read and only to a live campaign, and only the campaign and
// the stamp are written.
func (m *memRepo) SetPromotionCampaign(
	_ context.Context, id string, from, to *string, now time.Time,
) (models.Promotion, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("SetPromotionCampaign"); err != nil {
		return models.Promotion{}, false, err
	}
	p, ok := m.promotions[id]
	if !ok || p.DeletedAt != nil || !sameCampaign(p.CampaignID, from) {
		return models.Promotion{}, false, nil
	}
	if to != nil {
		if c, live := m.campaigns[*to]; !live || c.DeletedAt != nil {
			return models.Promotion{}, false, nil
		}
	}
	p.CampaignID = copyString(to)
	p.UpdatedAt = now
	m.promotions[id] = p
	return p, true, nil
}

// TestAPromotionIsPutIntoTheCampaignItWasReadOutOf is ADR 0320: the promotion
// moves into a live campaign, to another, and out of any, each from the one
// the caller read; a promotion moved since, a campaign that is not live, the
// campaign it is already in and an unknown promotion are refused, each by its
// own reason.
func TestAPromotionIsPutIntoTheCampaignItWasReadOutOf(t *testing.T) {
	ctx := context.Background()
	repo := newMemRepo()
	svc := newTestService(repo)
	spring, err := svc.CreateCampaign(ctx, CampaignInput{Name: "Spring", CampaignIdentifier: "SPRING"})
	require.NoError(t, err)
	summer, err := svc.CreateCampaign(ctx, CampaignInput{Name: "Summer", CampaignIdentifier: "SUMMER"})
	require.NoError(t, err)
	promo, err := svc.CreatePromotion(ctx, PromotionInput{Code: "SPRING10", UsageLimit: ptr(int64(5))})
	require.NoError(t, err)

	placed, err := svc.SetPromotionCampaign(ctx, promo.ID, nil, &spring.ID)
	require.NoError(t, err)
	require.NotNil(t, placed.CampaignID)
	assert.Equal(t, spring.ID, *placed.CampaignID)
	require.NotNil(t, placed.UsageLimit, "the promotion's other fields are kept")

	_, err = svc.SetPromotionCampaign(ctx, promo.ID, nil, &summer.ID)
	require.Error(t, err)
	assert.Equal(t, CodeCampaignMoved, errors.CodeOf(err), "read out of none, it is in spring now: %v", err)
	assert.Contains(t, err.Error(), "is in campaign "+spring.ID+" now, not no campaign")

	moved, err := svc.SetPromotionCampaign(ctx, promo.ID, &spring.ID, &summer.ID)
	require.NoError(t, err)
	assert.Equal(t, summer.ID, *moved.CampaignID)

	require.NoError(t, svc.DeleteCampaign(ctx, spring.ID))
	_, err = svc.SetPromotionCampaign(ctx, promo.ID, &summer.ID, &spring.ID)
	require.Error(t, err)
	assert.Equal(t, CodeCampaignGone, errors.CodeOf(err), "a deleted campaign is not live: %v", err)
	assert.True(t, errors.IsNotFound(err))

	_, err = svc.SetPromotionCampaign(ctx, promo.ID, &summer.ID, &summer.ID)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the campaign it is in: %v", err)

	out, err := svc.SetPromotionCampaign(ctx, promo.ID, &summer.ID, nil)
	require.NoError(t, err)
	assert.Nil(t, out.CampaignID, "out of any campaign")

	_, err = svc.SetPromotionCampaign(ctx, "promo_missing", nil, &summer.ID)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "an unknown promotion: %v", err)
	malformed := "summer"
	_, err = svc.SetPromotionCampaign(ctx, promo.ID, nil, &malformed)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a campaign id without its prefix: %v", err)
}
