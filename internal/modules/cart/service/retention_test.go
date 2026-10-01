package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// TestAbandonedCartsAreDeletedAfterTheShopsPeriod is ADR 0301's arithmetic:
// the store is asked for the carts untouched since now less the period, and
// with no period it is not asked at all.
func TestAbandonedCartsAreDeletedAfterTheShopsPeriod(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	store := newFakeStore()
	store.abandoned = 7
	svc, err := service.New(service.Options{Repo: store, Events: &fakeBus{}, RetentionDays: 30})
	require.NoError(t, err)
	assert.True(t, svc.AbandonedCartsExpire())

	deleted, err := svc.DeleteAbandonedCarts(ctx, now, 500)
	require.NoError(t, err)
	assert.Equal(t, int64(7), deleted)
	assert.Equal(t, []time.Time{now.Add(-30 * 24 * time.Hour)}, store.abandonedCutoffs)
	assert.Equal(t, []int64{500}, store.abandonedLimits)

	keeping := newFakeStore()
	keeping.abandoned = 7
	svc, err = service.New(service.Options{Repo: keeping, Events: &fakeBus{}})
	require.NoError(t, err)
	assert.False(t, svc.AbandonedCartsExpire())
	deleted, err = svc.DeleteAbandonedCarts(ctx, now, 500)
	require.NoError(t, err)
	assert.Zero(t, deleted)
	assert.Empty(t, keeping.abandonedCutoffs, "with no period nothing is asked")
}

// TestTheRetentionIsBounded: a negative period or one past the ceiling builds
// no service.
func TestTheRetentionIsBounded(t *testing.T) {
	for _, days := range []int{-1, service.MaxRetentionDays + 1} {
		_, err := service.New(service.Options{Repo: newFakeStore(), Events: &fakeBus{}, RetentionDays: days})
		require.Error(t, err, "%d days", days)
		assert.True(t, errors.HasKind(err, errors.KindInternal), "%d days", days)
	}
	_, err := service.New(service.Options{Repo: newFakeStore(), Events: &fakeBus{}, RetentionDays: service.MaxRetentionDays})
	require.NoError(t, err, "the ceiling itself is a period")
}
