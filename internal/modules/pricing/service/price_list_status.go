package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// CodePriceListMoved refuses a status switch whose list is no longer in the
// status the caller read (ADR 0328).
const CodePriceListMoved = "pricing_price_list_moved"

// SwitchPriceListStatus moves the list from the status the caller read to
// another, and refuses with [CodePriceListMoved] when it is no longer in the
// first (ADR 0328): an operator publishing a draft somebody already published,
// or ending a list somebody reopened, is told so rather than undoing the
// other. It writes the status alone and keeps the list's other fields.
func (s *Service) SwitchPriceListStatus(
	ctx context.Context, id string, from, to models.PriceListStatus,
) (models.PriceList, error) {
	if err := s.ready(); err != nil {
		return models.PriceList{}, err
	}
	if err := requireID(id, models.PriceListIDPrefix, "price list id"); err != nil {
		return models.PriceList{}, err
	}
	for _, status := range []models.PriceListStatus{from, to} {
		if !status.Valid() {
			return models.PriceList{}, errors.Invalid(CodeInvalidInput,
				"the price list status is undefined: %q", string(status))
		}
	}
	if from == to {
		return models.PriceList{}, errors.Invalid(CodeInvalidInput,
			"the price list is switched to the status it is in: %q", string(to))
	}

	list, switched, err := s.repo.SwitchPriceListStatus(ctx, id, from, to, s.clock)
	if err != nil {
		return models.PriceList{}, err
	}
	if !switched {
		return models.PriceList{}, errors.Conflict(CodePriceListMoved,
			"price list %s is %s now, not %s; draw the list again", id, list.Status, from)
	}

	return list, nil
}
