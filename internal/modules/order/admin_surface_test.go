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
// the lines, their quantities and the field beside them as parallel lists,
// and one shorter than the others is refused before the service is asked
// (ADR 0272, ADR 0279).
func TestAnOpeningWhoseLinesAndQuantitiesDoNotPairIsRefused(t *testing.T) {
	t.Parallel()

	surface := &order.AfterSalesSurface{}
	two := []string{"oli_1", "oli_2"}

	for name, open := range map[string]func() error{
		"a return short of a quantity": func() error {
			_, err := surface.OpenReturn(context.Background(), "order_1", two, []int64{1}, []int64{0, 0}, 0, "")
			return err
		},
		"a return short of a line refund": func() error {
			_, err := surface.OpenReturn(context.Background(), "order_1", two, []int64{1, 1}, []int64{0}, 0, "")
			return err
		},
		"a replacement short of a quantity": func() error {
			_, err := surface.OpenReplacement(context.Background(), "claim_1", "", two, []int64{1}, nil, nil,
				"so", "sloc")
			return err
		},
		"a replacement's variant short of a quantity": func() error {
			_, err := surface.OpenReplacement(context.Background(), "claim_1", "", nil, nil,
				[]string{"variant_1", "variant_2"}, []int64{1}, "so", "sloc")
			return err
		},
	} {
		err := open()
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalid(err), "%s: %v", name, err)
	}
}
