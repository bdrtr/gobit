package api_test

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// SwitchPromotionStatus completes the double's repository; the admin API has
// no status door of its own (ADR 0312).
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
