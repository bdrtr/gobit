package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// ReviseMethodValue mirrors the query: the value is written only while the
// method is of the type and the value the caller read.
func (m *memRepo) ReviseMethodValue(
	_ context.Context, promotionID string, readType models.ApplicationMethodType, readValue, value int64,
	now time.Time,
) (models.ApplicationMethod, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.hook("ReviseMethodValue"); err != nil {
		return models.ApplicationMethod{}, false, err
	}
	method, ok := m.methods[promotionID]
	if !ok || method.Type != readType || method.Value != readValue {
		return models.ApplicationMethod{}, false, nil
	}
	method.Value, method.UpdatedAt = value, now
	m.methods[promotionID] = method
	return method, true, nil
}

// TestADiscountsValueIsRevisedFromWhatWasRead is ADR 0338: a percentage is
// written from the type and the value read and nothing else of the method
// changes; a value read before is refused by what the discount is now, and
// so is a type read wrong; a percentage over a hundred, a negative amount and
// an undefined type are refused before the store is asked; a promotion with
// no discount is not found.
func TestADiscountsValueIsRevisedFromWhatWasRead(t *testing.T) {
	ctx := context.Background()
	repo := newMemRepo()
	svc := newTestService(repo)
	promo, err := svc.CreateCoupon(ctx, CouponInput{Code: "SPRING", Method: ApplicationMethodInput{
		Type: models.MethodPercentage, TargetType: models.TargetItems, Value: 1000,
	}})
	require.NoError(t, err)
	before := repo.methods[promo.ID]

	revised, err := svc.ReviseDiscountValue(ctx, promo.ID, models.MethodPercentage, 1000, 1500)
	require.NoError(t, err)
	assert.Equal(t, int64(1500), revised.Value)
	after := repo.methods[promo.ID]
	assert.Equal(t, before.TargetType, after.TargetType, "nothing else of the method changes")
	assert.Equal(t, before.Allocation, after.Allocation)
	assert.Equal(t, before.ID, after.ID)

	_, err = svc.ReviseDiscountValue(ctx, promo.ID, models.MethodPercentage, 1000, 2000)
	require.Error(t, err)
	assert.Equal(t, CodeDiscountRevised, errors.CodeOf(err), "read before the change: %v", err)
	assert.Contains(t, err.Error(), "it is percentage 1500 now")
	_, err = svc.ReviseDiscountValue(ctx, promo.ID, models.MethodFixed, 1500, 2000)
	assert.Equal(t, CodeDiscountRevised, errors.CodeOf(err), "a type read wrong: %v", err)

	calls := repo.calls["ReviseMethodValue"]
	for label, attempt := range map[string]struct {
		method models.ApplicationMethodType
		value  int64
	}{
		"a percentage over a hundred": {models.MethodPercentage, models.BasisPointDenominator + 1},
		"a negative percentage":       {models.MethodPercentage, -1},
		"a negative amount":           {models.MethodFixed, -1},
		"an amount over the maximum":  {models.MethodFixed, models.MaxAmount + 1},
		"an undefined type":           {"bogo", 100},
	} {
		_, err = svc.ReviseDiscountValue(ctx, promo.ID, attempt.method, 1500, attempt.value)
		assert.True(t, errors.IsInvalid(err), "%s: %v", label, err)
	}
	assert.Equal(t, calls, repo.calls["ReviseMethodValue"], "a refused value never reaches the store")

	_, err = svc.ReviseDiscountValue(ctx, "promo_missing", models.MethodPercentage, 0, 100)
	assert.True(t, errors.IsNotFound(err), "a promotion with no discount: %v", err)
}
