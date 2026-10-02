//go:build integration

package promotion_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// TestThePanelRevisesADiscountsValueFromWhatItRead is ADR 0338 against a real
// PostgreSQL: the value is written from the type and the value read, the
// target and the allocation kept; a value read before, or a type read wrong,
// writes nothing; a promotion whose discount was deleted is not found.
func TestThePanelRevisesADiscountsValueFromWhatItRead(t *testing.T) {
	ctx := context.Background()
	svc := service.New(repository.New(testPool.Pool()), service.Options{})
	surface := promotion.NewAdminSurface(svc)
	promo, err := svc.CreateCoupon(ctx, service.CouponInput{Code: "REV" + uniqueCode(), Method: service.ApplicationMethodInput{
		Type: models.MethodPercentage, TargetType: models.TargetItems, Value: 1000,
	}})
	require.NoError(t, err)

	require.NoError(t, surface.ReviseDiscountValue(ctx, promo.ID, "percentage", 1000, 1250))
	method, err := svc.GetApplicationMethod(ctx, promo.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1250), method.Value)
	assert.Equal(t, models.TargetItems, method.TargetType, "the target is kept")

	for label, read := range map[string]struct {
		kind  string
		value int64
	}{
		"a value read before": {"percentage", 1000},
		"a type read wrong":   {"fixed", 1250},
	} {
		err = surface.ReviseDiscountValue(ctx, promo.ID, read.kind, read.value, 500)
		require.Error(t, err, label)
		assert.Equal(t, service.CodeDiscountRevised, errors.CodeOf(err), "%s: %v", label, err)
	}
	method, err = svc.GetApplicationMethod(ctx, promo.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1250), method.Value, "a stale read writes nothing")

	require.NoError(t, svc.DeleteApplicationMethod(ctx, promo.ID))
	err = surface.ReviseDiscountValue(ctx, promo.ID, "percentage", 1250, 500)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a deleted discount: %v", err)
}
