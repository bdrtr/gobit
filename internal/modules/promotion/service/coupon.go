package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// CouponInput is a coupon as an operator writes it in one form (ADR 0314):
// its code, how often it may be used, and what it gives.
type CouponInput struct {
	// Code is the coupon's code; it is stored in capitals.
	Code string
	// UsageLimit bounds the uses; nil leaves them unbounded.
	UsageLimit *int64
	// Method is what the coupon gives, as [Service.SetApplicationMethod]
	// takes it.
	Method ApplicationMethodInput
}

// CreateCoupon writes a draft coupon and its discount together (ADR 0314):
// both are validated before either is written, and they are written in one
// transaction, so a refused discount leaves no coupon and a coupon is never
// without one. The coupon is a standard promotion that a shopper applies by
// its code; it is published by switching its status (ADR 0312).
func (s *Service) CreateCoupon(ctx context.Context, in CouponInput) (models.Promotion, error) {
	if err := s.ready(); err != nil {
		return models.Promotion{}, err
	}

	now := s.clock()
	promo, err := buildPromotion(models.NewPromotionID(now), PromotionInput{
		Code: in.Code, UsageLimit: in.UsageLimit,
	}, now)
	if err != nil {
		return models.Promotion{}, err
	}
	method, err := buildApplicationMethod(models.NewApplicationMethodID(now), promo.ID, in.Method, now)
	if err != nil {
		return models.Promotion{}, err
	}

	return s.repo.CreatePromotionWithMethod(ctx, promo, method, now)
}
