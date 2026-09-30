package order_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order"
)

// TestAnOpeningWhoseLinesAndQuantitiesDoNotPairIsRefused: the surface takes
// the two as parallel lists, and one shorter than the other is refused before
// the service is asked (ADR 0272).
func TestAnOpeningWhoseLinesAndQuantitiesDoNotPairIsRefused(t *testing.T) {
	t.Parallel()

	surface := &order.AfterSalesSurface{}

	_, err := surface.OpenReturn(context.Background(), "order_1", []string{"oli_1", "oli_2"}, []int64{1}, 0, "")
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "error: %v", err)

	_, err = surface.OpenReplacement(context.Background(), "claim_1", "", []string{"oli_1"}, nil, "so", "sloc")
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "error: %v", err)
}
