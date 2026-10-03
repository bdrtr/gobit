package service

import (
	"context"
	"log/slog"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// RedeemInput is the input of a coupon redemption.
//
// The promotion is named either by id or by code; if both are given the ID
// wins (it is more precise) and the code is only verified.
type RedeemInput struct {
	// PromotionID is the id of the promotion to redeem; if empty, [Code] is
	// used.
	PromotionID string
	// Code is the coupon code of the promotion to redeem; it is REQUIRED when
	// [PromotionID] is empty.
	Code string
	// Reference is the business record the redemption belongs to (e.g. an
	// order id). It is the idempotency KEY: a second call with the same
	// reference does not increment the counter.
	Reference string
	// Amount is the discount amount actually applied (minor unit); a
	// money-measured campaign budget is consumed by this much.
	Amount int64
	// CurrencyCode is the currency of the discount (ISO 4217).
	CurrencyCode string
}

// RedeemPromotion redeems the promotion for a reference and increments the
// counters.
//
// # IT IS IDEMPOTENT
//
// A second call with the same [RedeemInput.Reference] writes NO new redemption
// and increments no counter; it returns the existing record. The order
// completion saga may rerun a step, and a retry MUST NOT mean the coupon is
// spent a second time.
//
// # Reasons for refusal
//
// In these cases errors.Conflict is returned and NOTHING is written:
//
//   - The promotion is NOT LIVE (draft or inactive). The coupon of a promotion
//     that was never published cannot be consumed, and the campaign budget
//     cannot be eaten.
//   - If it has a campaign: the campaign is DELETED, its date window does NOT
//     COVER the moment of redemption, or its budget's currency does not match
//     the redemption's.
//   - The usage allowance is exhausted, or the campaign budget does not
//     suffice.
//
// ALL of the checks are made in the database, under a row lock (see
// repository.Redeem); the counter limits are additionally enforced with a
// conditional UPDATE. Had it been done in the application as "read first, then
// write", two concurrent redemptions could take the same last allowance.
//
// The checks come AFTER idempotency: a second call with the same reference
// returns the existing record even if the promotion was stopped in the
// meantime. The reasoning is in repository.Redeem's godoc.
//
// The elimination here is NOT the same gate as the one in
// [Service.ComputeDiscounts]: the computation has no side effects and its
// elimination decides what is shown to the customer, whereas the check here is
// the arbiter of the moment the counter is written, and it is the ONLY place
// that catches a state that changed between the computation and the
// redemption.
//
// # Why the computation is not redone
//
// The amount comes FROM THE CALLER; the service does not recompute it with
// [Service.ComputeDiscounts]. The reason is that the computation depends on the
// cart's shape at that moment: the cart may have changed by the time of
// redemption, and a second computation made here would write to the budget an
// amount different from the one shown to the customer. The correctness of the
// amount is the responsibility of the caller (the order completion flow); this
// module only records in the ledger what is written.
func (s *Service) RedeemPromotion(ctx context.Context, in RedeemInput) (models.Redemption, error) {
	if err := s.ready(); err != nil {
		return models.Redemption{}, err
	}

	promo, err := s.resolvePromotion(ctx, in.PromotionID, in.Code)
	if err != nil {
		return models.Redemption{}, err
	}
	if err := validateText("redemption reference", in.Reference, 1, MaxReferenceLen); err != nil {
		return models.Redemption{}, err
	}
	if err := validateAmount("discount amount", in.Amount); err != nil {
		return models.Redemption{}, err
	}
	currency, err := normalizeCurrency(in.CurrencyCode)
	if err != nil {
		return models.Redemption{}, err
	}

	now := s.clock()
	redemption, created, err := s.repo.Redeem(ctx, models.Redemption{
		ID:           models.NewRedemptionID(now),
		PromotionID:  promo.ID,
		Reference:    in.Reference,
		Amount:       in.Amount,
		CurrencyCode: currency,
	}, now)
	if err != nil {
		return models.Redemption{}, err
	}

	// The amount is logged because it is the only way to trace the budget
	// accounting; the coupon CODE is not logged (it is a usable secret, plan
	// Section 8).
	s.log.DebugContext(ctx, "promotion redeemed",
		slog.String("promotion_id", promo.ID),
		slog.String("reference", in.Reference),
		slog.Bool("new_record", created),
		slog.Int64("amount", redemption.Amount),
	)
	return redemption, nil
}

// ReleaseInput is the input for reversing a coupon redemption.
type ReleaseInput struct {
	// PromotionID is the id of the promotion; if empty, [Code] is used.
	PromotionID string
	// Code is the coupon code of the promotion; it is REQUIRED when
	// [PromotionID] is empty.
	Code string
	// Reference is the reference of the redemption to reverse.
	Reference string
}

// ReleasePromotion releases a redemption and reverses the counters.
//
// # IT IS A SAGA COMPENSATION and IT IS IDEMPOTENT
//
// If it is called twice, the second call returns NO error and the counters do
// not drop a second time. No error is returned either when no redemption was
// ever written: the compensation must be able to run after a step that blew up
// before writing, too (plan Section 5.5).
//
// If the promotion ITSELF does not exist, errors.NotFound is returned. This is
// a setup error that must not be swallowed silently: compensating a promotion
// that does not exist means a step was called with the wrong id.
//
// The second return value reports whether anything was reversed IN THIS CALL;
// the tests that check the compensation really did work look at it.
func (s *Service) ReleasePromotion(ctx context.Context, in ReleaseInput) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}

	promo, err := s.resolvePromotion(ctx, in.PromotionID, in.Code)
	if err != nil {
		return false, err
	}
	if err := validateText("redemption reference", in.Reference, 1, MaxReferenceLen); err != nil {
		return false, err
	}

	_, released, err := s.repo.Release(ctx, promo.ID, in.Reference, s.clock())
	if err != nil {
		return false, err
	}

	s.log.DebugContext(ctx, "promotion redemption released",
		slog.String("promotion_id", promo.ID),
		slog.String("reference", in.Reference),
		slog.Bool("released", released),
	)
	return released, nil
}

// GetRedemption returns the VALID redemption of a reference; if there is none,
// errors.NotFound.
func (s *Service) GetRedemption(ctx context.Context, promotionID, reference string) (models.Redemption, error) {
	if err := s.ready(); err != nil {
		return models.Redemption{}, err
	}
	if err := requireID(promotionID, models.PromotionIDPrefix, "promotion id"); err != nil {
		return models.Redemption{}, err
	}
	if err := validateText("redemption reference", reference, 1, MaxReferenceLen); err != nil {
		return models.Redemption{}, err
	}
	return s.repo.GetRedemption(ctx, promotionID, reference)
}

// ListRedemptions returns a promotion's redemption ledger, paginated.
//
// Released records are returned too: the ledger is a history, and the trace of
// a reversed redemption must not be erased.
func (s *Service) ListRedemptions(
	ctx context.Context,
	promotionID string,
	limit, offset int32,
) (Page[models.Redemption], error) {
	if err := s.ready(); err != nil {
		return Page[models.Redemption]{}, err
	}
	if err := requireID(promotionID, models.PromotionIDPrefix, "promotion id"); err != nil {
		return Page[models.Redemption]{}, err
	}
	limit, offset, err := normalizePaging(limit, offset)
	if err != nil {
		return Page[models.Redemption]{}, err
	}
	if _, err := s.repo.GetPromotion(ctx, promotionID); err != nil {
		return Page[models.Redemption]{}, err
	}

	items, total, err := s.repo.ListRedemptions(ctx, promotionID, limit, offset)
	if err != nil {
		return Page[models.Redemption]{}, err
	}
	return Page[models.Redemption]{Items: items, Count: total, Limit: limit, Offset: offset}, nil
}

// resolvePromotion resolves the promotion from an id or from a code.
//
// If an id is given, it is used; if a code is given as well, it is expected to
// MATCH the promotion's code. A mismatch is not silently ignored: a request
// that names two different promotions means the caller does not know which one
// it means, and the counter would be written to the wrong promotion.
func (s *Service) resolvePromotion(ctx context.Context, id, code string) (models.Promotion, error) {
	switch {
	case id != "":
		if err := requireID(id, models.PromotionIDPrefix, "promotion id"); err != nil {
			return models.Promotion{}, err
		}
		promo, err := s.repo.GetPromotion(ctx, id)
		if err != nil {
			return models.Promotion{}, err
		}
		if code != "" {
			normalized, codeErr := normalizeCode(code)
			if codeErr != nil {
				return models.Promotion{}, codeErr
			}
			if normalized != promo.Code {
				return models.Promotion{}, errors.Invalid(CodeInvalidInput,
					"promotion id and coupon code point to different promotions: %s / %s",
					id, normalized)
			}
		}
		return promo, nil
	case code != "":
		normalized, err := normalizeCode(code)
		if err != nil {
			return models.Promotion{}, err
		}
		return s.repo.GetPromotionByCode(ctx, normalized)
	default:
		return models.Promotion{}, errors.Invalid(CodeInvalidInput,
			"a promotion id or a coupon code must be given")
	}
}
