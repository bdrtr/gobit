package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository"
)

// CreatePromotionWithMethod mirrors the transaction: both or neither.
func (m *memRepo) CreatePromotionWithMethod(
	_ context.Context, p models.Promotion, method models.ApplicationMethod, now time.Time,
) (models.Promotion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("CreatePromotionWithMethod"); err != nil {
		return models.Promotion{}, err
	}
	for id := range m.promotions {
		if m.promotions[id].Code == p.Code {
			return models.Promotion{}, errors.Conflict(repository.CodeDuplicate,
				"a promotion with the code %s exists", p.Code)
		}
	}
	p.CreatedAt, p.UpdatedAt = now, now
	m.promotions[p.ID] = p
	method.CreatedAt, method.UpdatedAt = now, now
	m.methods[p.ID] = method
	return p, nil
}

func TestCreateCouponWritesADraftWithItsDiscount(t *testing.T) {
	repo := newMemRepo()
	limit := int64(50)

	promo, err := newTestService(repo).CreateCoupon(context.Background(), CouponInput{
		Code: " spring-15 ", UsageLimit: &limit,
		Method: ApplicationMethodInput{
			Type: models.MethodPercentage, TargetType: models.TargetOrder, Value: 1500,
		},
	})
	require.NoError(t, err)

	assert.Equal(t, "SPRING-15", promo.Code)
	assert.Equal(t, models.PromotionDraft, promo.Status, "a coupon starts as a draft")
	assert.Equal(t, models.PromotionStandard, promo.Type)
	assert.False(t, promo.IsAutomatic, "a coupon is applied by its code")
	require.NotNil(t, promo.UsageLimit)
	assert.Equal(t, int64(50), *promo.UsageLimit)

	method := repo.methods[promo.ID]
	assert.Equal(t, promo.ID, method.PromotionID, "the discount is the coupon's")
	assert.Equal(t, models.MethodPercentage, method.Type)
	assert.Equal(t, models.TargetOrder, method.TargetType)
	assert.Equal(t, models.AllocationAcross, method.Allocation, "an order discount is spread across it")
	assert.Equal(t, int64(1500), method.Value)
	assert.NotEmpty(t, method.ID)
}

func TestCreateCouponRefusesBeforeWritingEither(t *testing.T) {
	cases := map[string]CouponInput{
		"no code": {Method: ApplicationMethodInput{Type: models.MethodPercentage, TargetType: models.TargetOrder, Value: 1000}},
		"a fixed discount without a currency": {Code: "FIXED", Method: ApplicationMethodInput{
			Type: models.MethodFixed, TargetType: models.TargetOrder, Value: 1000,
		}},
		"over a hundred per cent": {Code: "TOOMUCH", Method: ApplicationMethodInput{
			Type: models.MethodPercentage, TargetType: models.TargetOrder, Value: 10001,
		}},
		"no measure": {Code: "NOMEASURE", Method: ApplicationMethodInput{TargetType: models.TargetOrder, Value: 1000}},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			repo := newMemRepo()

			_, err := newTestService(repo).CreateCoupon(context.Background(), in)

			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
			assert.Zero(t, repo.calls["CreatePromotionWithMethod"], "nothing is written")
			assert.Empty(t, repo.promotions)
		})
	}
}

func TestCreateCouponReportsATakenCode(t *testing.T) {
	repo := newMemRepo()
	svc := newTestService(repo)
	in := CouponInput{Code: "TAKEN", Method: ApplicationMethodInput{
		Type: models.MethodFixed, TargetType: models.TargetItems, Value: 500, CurrencyCode: "TRY",
	}}
	_, err := svc.CreateCoupon(context.Background(), in)
	require.NoError(t, err)

	_, err = svc.CreateCoupon(context.Background(), in)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Len(t, repo.promotions, 1)
}
