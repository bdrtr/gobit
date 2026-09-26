package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// TestACollectionSaysWhatItWasOpenedFor answers the reference it was opened
// with, which is how a placed order's later money is told apart from its
// checkout's (ADR 0200).
func TestACollectionSaysWhatItWasOpenedFor(t *testing.T) {
	ctx := context.Background()
	svc, err := service.New(service.Options{
		Store: newFakeStore(), Providers: service.NewProviderRegistry(), Events: newFakeBus(),
	})
	require.NoError(t, err)
	interop := service.NewInterop(svc)

	collectionID, err := interop.CreateCollection(ctx, "order_1", "", "TRY", 1500)
	require.NoError(t, err)

	reference, err := interop.CollectionReference(ctx, collectionID)
	require.NoError(t, err)
	assert.Equal(t, "order_1", reference)

	_, err = interop.CollectionReference(ctx, "pay_col_nope")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "%v", err)
}
