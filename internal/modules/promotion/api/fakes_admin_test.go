package api_test

import (
	"context"
	"sort"
	"time"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// The double's repository methods that the panel's surface brought: the
// admin API has no door of its own to any of them (ADR 0312, ADR 0313,
// ADR 0314).

// CreatePromotionWithMethod writes both, the code kept unique.
func (m *memRepo) CreatePromotionWithMethod(
	ctx context.Context, p models.Promotion, method models.ApplicationMethod, now time.Time,
) (models.Promotion, error) {
	created, err := m.CreatePromotion(ctx, p, now)
	if err != nil {
		return models.Promotion{}, err
	}
	method.CreatedAt, method.UpdatedAt = now, now
	m.methods[created.ID] = method
	return created, nil
}

// SwitchPromotionStatus moves the status only from the one read.
func (m *memRepo) SwitchPromotionStatus(
	_ context.Context, id string, from, to models.PromotionStatus, now time.Time,
) (models.Promotion, bool, error) {
	p, ok := m.promotions[id]
	if !ok || p.Status != from {
		return models.Promotion{}, false, nil
	}
	p.Status = to
	p.UpdatedAt = now
	m.promotions[id] = p
	return p, true, nil
}

// SetPromotionCampaign moves the promotion's campaign from the one read; the
// API tests do not reach it, and the service's own fake holds the rule.
func (m *memRepo) SetPromotionCampaign(
	_ context.Context, id string, from, to *string, now time.Time,
) (models.Promotion, bool, error) {
	p, ok := m.promotions[id]
	if !ok || (p.CampaignID == nil) != (from == nil) || (from != nil && *p.CampaignID != *from) {
		return models.Promotion{}, false, nil
	}
	p.CampaignID = to
	p.UpdatedAt = now
	m.promotions[id] = p
	return p, true, nil
}

// ListLatestRedemptions returns the promotion's uses, newest id first.
func (m *memRepo) ListLatestRedemptions(_ context.Context, promotionID string, limit int32) ([]models.Redemption, error) {
	var out []models.Redemption
	for i := range m.redemptions {
		if m.redemptions[i].PromotionID == promotionID {
			out = append(out, m.redemptions[i])
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if int32(len(out)) > limit {
		out = out[:limit]
	}
	return out, nil
}
