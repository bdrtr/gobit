package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// CodeStatusMoved refuses a status switch whose promotion is no longer in the
// status the caller read (ADR 0312).
const CodeStatusMoved = "promotion_status_moved"

// SwitchPromotionStatus moves the promotion from the status the caller read to
// another, and refuses with [CodeStatusMoved] when it is no longer in the first
// (ADR 0312): an operator pausing a coupon another has paused, or publishing a
// draft another has withdrawn, is told so rather than undoing the other. It
// writes the status alone, so an edit of the promotion's other fields made
// meanwhile is kept.
func (s *Service) SwitchPromotionStatus(ctx context.Context, id string, from, to models.PromotionStatus) (models.Promotion, error) {
	if err := s.ready(); err != nil {
		return models.Promotion{}, err
	}
	if err := requireID(id, models.PromotionIDPrefix, "promotion id"); err != nil {
		return models.Promotion{}, err
	}
	for _, status := range []models.PromotionStatus{from, to} {
		if !status.Valid() {
			return models.Promotion{}, errors.Invalid(CodeInvalidInput,
				"promotion status is undefined: %q", string(status))
		}
	}
	if from == to {
		return models.Promotion{}, errors.Invalid(CodeInvalidInput,
			"the promotion is switched to the status it is in: %q", string(to))
	}

	promo, switched, err := s.repo.SwitchPromotionStatus(ctx, id, from, to, s.clock())
	if err != nil {
		return models.Promotion{}, err
	}
	if switched {
		return promo, nil
	}

	// Nothing moved: the promotion is gone, or it is in another status now.
	current, err := s.GetPromotion(ctx, id)
	if err != nil {
		return models.Promotion{}, err
	}

	return models.Promotion{}, errors.Conflict(CodeStatusMoved,
		"promotion %s is %s now, not %s; draw the list again", id, current.Status, from)
}
