package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestAnOrderIsReadOnlyAtAMomentItExisted refuses the two moments nothing was
// recorded for (ADR 0171): one that has not happened, and one before the order
// was placed. A moment in between gets past both and on to the read.
func TestAnOrderIsReadOnlyAtAMomentItExisted(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	now := time.Now().Add(time.Hour)
	svc, err := service.New(service.Options{
		Repo: store, Events: newFakeBus(), Now: func() time.Time { return now },
	})
	require.NoError(t, err)

	order, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	for name, at := range map[string]time.Time{
		"no moment":            {},
		"a moment to come":     now.Add(time.Second),
		"before it was placed": order.PlacedAt.Add(-time.Second),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := svc.OrderAsOf(ctx, order.ID, at)

			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "%v", err)
			assert.Equal(t, service.CodeAsOfInvalid, errors.CodeOf(err))
		})
	}

	// The moment it was placed is a moment it existed; with no query layer
	// wired the read then fails for THAT reason, which is how this test knows
	// the moment itself was accepted.
	_, err = svc.OrderAsOf(ctx, order.ID, order.PlacedAt)
	require.Error(t, err)
	assert.Equal(t, service.CodeNotReady, errors.CodeOf(err))
}
