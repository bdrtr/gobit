//go:build integration

package promotion_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository"
)

// TestThePanelWritesACoupon is ADR 0314 against a real PostgreSQL: the
// surface writes a draft coupon with its discount, the coupon's page reads
// both back, and a code already taken is refused in the operator's words
// without a second coupon.
func TestThePanelWritesACoupon(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	surface := promotion.NewAdminSurface(svc)
	code := uniqueCode()
	limit := int64(40)

	// Spread across the items, which is not what items default to.
	id, err := surface.CreateCoupon(ctx, code, "fixed", "items", "across", 5000, "TRY", &limit)
	require.NoError(t, err)

	promo, err := svc.GetPromotion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, code, promo.Code)
	assert.Equal(t, models.PromotionDraft, promo.Status)
	assert.False(t, promo.IsAutomatic)
	require.NotNil(t, promo.UsageLimit)
	assert.Equal(t, int64(40), *promo.UsageLimit)
	method, err := svc.GetApplicationMethod(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, models.MethodFixed, method.Type)
	assert.Equal(t, models.TargetItems, method.TargetType)
	assert.Equal(t, models.AllocationAcross, method.Allocation)
	assert.Equal(t, int64(5000), method.Value)
	assert.Equal(t, "TRY", method.CurrencyCode)

	_, err = surface.CreateCoupon(ctx, code, "percentage", "order", "across", 1000, "", nil)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "a taken code: %v", err)
	assert.Contains(t, err.Error(), "a promotion with the code "+code+" exists")
	assert.NotContains(t, err.Error(), "promotion_code", "the constraint's name is not the operator's")
	kept, err := svc.GetApplicationMethod(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, models.MethodFixed, kept.Type, "the first coupon keeps its discount")
	assert.Equal(t, int64(5000), kept.Value)
}

// TestACouponIsWrittenWithItsDiscountOrNotAtAll holds the transaction: a
// discount the database refuses — past every check the service makes, as a
// writer that is not the service could send — leaves no coupon behind.
func TestACouponIsWrittenWithItsDiscountOrNotAtAll(t *testing.T) {
	ctx := context.Background()
	repo := repository.New(testPool.Pool())
	now := time.Now().UTC()
	promo := models.Promotion{
		ID: models.NewPromotionID(now), Code: uniqueCode(), Type: models.PromotionStandard,
		Status: models.PromotionDraft, Metadata: map[string]string{},
	}
	refused := models.ApplicationMethod{
		ID: models.NewApplicationMethodID(now), PromotionID: promo.ID, Type: models.MethodPercentage,
		TargetType: models.TargetOrder, Allocation: models.AllocationAcross, Value: 20_000,
	}

	_, err := repo.CreatePromotionWithMethod(ctx, promo, refused, now)
	require.Error(t, err, "two hundred per cent passes no CHECK")
	// PostgreSQL would abort the transaction by itself; what the write adds is
	// saying which part was refused, as invalid input rather than a failure.
	assert.True(t, errors.IsInvalid(err), "the refusal is the discount's: %v", err)
	assert.Contains(t, err.Error(), "the discount of coupon "+promo.Code)

	_, err = newService(t).GetPromotion(ctx, promo.ID)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "the coupon went with its discount: %v", err)
}
