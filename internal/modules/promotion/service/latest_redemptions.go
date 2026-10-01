package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// LatestRedemptions returns the promotion's latest uses, newest first and at
// most limit of them, released ones included (ADR 0313): the ledger is a
// history, and a use given back stays in it.
func (s *Service) LatestRedemptions(ctx context.Context, promotionID string, limit int32) ([]models.Redemption, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := requireID(promotionID, models.PromotionIDPrefix, "promotion id"); err != nil {
		return nil, err
	}
	limit, _, err := normalizePaging(limit, 0)
	if err != nil {
		return nil, err
	}

	return s.repo.ListLatestRedemptions(ctx, promotionID, limit)
}
