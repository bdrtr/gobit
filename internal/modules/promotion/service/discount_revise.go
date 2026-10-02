package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// CodeDiscountRevised refuses a change of a discount's value when the
// promotion's method is no longer of the type and the value the caller read
// (ADR 0338).
const CodeDiscountRevised = "promotion_discount_revised"

// ReviseDiscountValue changes how much the promotion's discount gives,
// writing the value only while the method is of the type and the value the
// caller read, and refuses with [CodeDiscountRevised] when another writer
// changed either since (ADR 0338). The value is checked as a new method's of
// that type is: a fixed discount in minor units up to [models.MaxAmount], a
// percentage in basis points up to [models.BasisPointDenominator]. Nothing
// else of the method is written.
func (s *Service) ReviseDiscountValue(
	ctx context.Context, promotionID string, readType models.ApplicationMethodType, readValue, value int64,
) (models.ApplicationMethod, error) {
	if err := s.ready(); err != nil {
		return models.ApplicationMethod{}, err
	}
	if err := requireID(promotionID, models.PromotionIDPrefix, "promotion id"); err != nil {
		return models.ApplicationMethod{}, err
	}
	switch readType {
	case models.MethodFixed:
		if err := validateAmount("discount amount", value); err != nil {
			return models.ApplicationMethod{}, err
		}
	case models.MethodPercentage:
		if value < 0 || value > models.BasisPointDenominator {
			return models.ApplicationMethod{}, errors.Invalid(CodeInvalidInput,
				"a percentage discount has to be in the [0, %d] basis point range, %d given",
				models.BasisPointDenominator, value)
		}
	default:
		return models.ApplicationMethod{}, errors.Invalid(CodeInvalidInput,
			"application method type is undefined: %q", string(readType))
	}

	method, revised, err := s.repo.ReviseMethodValue(ctx, promotionID, readType, readValue, value, s.clock())
	if err != nil || revised {
		return method, err
	}

	// Nothing was written: the promotion has no discount, or it moved since.
	current, err := s.repo.GetApplicationMethod(ctx, promotionID)
	if err != nil {
		return models.ApplicationMethod{}, err
	}

	return models.ApplicationMethod{}, errors.Conflict(CodeDiscountRevised,
		"promotion %s's discount was changed since it was read: it is %s %d now; draw the page again",
		promotionID, current.Type, current.Value)
}
