package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestVariantsInStockIsTheStorefrontsBadge is ADR 0215's read: each variant is
// answered as the storefront's badge answers it, and a variant the storefront
// does not show — unknown, or of a product it does not list — is absent.
func TestVariantsInStockIsTheStorefrontsBadge(t *testing.T) {
	t.Parallel()

	fx := newBadgeFixture(t,
		variantSpec{manage: true, inventory: query.Record{"available_quantity": int64(3)}},
		variantSpec{manage: true, inventory: query.Record{"available_quantity": int64(0)}},
	)
	ids := []string{fx.products[0].Variants[0].ID, fx.products[0].Variants[1].ID, "variant_unknown"}

	answer, err := service.NewInterop(fx.svc).VariantsInStock(context.Background(), ids, nil)

	require.NoError(t, err)
	assert.Equal(t, map[string]bool{ids[0]: true, ids[1]: false}, answer)
}

// TestAVariantInvisibleInTheChannelsIsAbsent: a product assigned to another
// channel is not in stock for the mark's, whatever its warehouses hold.
func TestAVariantInvisibleInTheChannelsIsAbsent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newBadgeFixture(t,
		variantSpec{manage: true, inventory: query.Record{"available_quantity": int64(3)}},
	)
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, fx.products[0].ID, "sc_theirs"))
	id := fx.products[0].Variants[0].ID

	elsewhere, err := fx.svc.VariantsInStock(ctx, []string{id}, []string{"sc_ours"})
	require.NoError(t, err)
	assert.Empty(t, elsewhere, "invisible in the mark's channel")

	here, err := fx.svc.VariantsInStock(ctx, []string{id}, []string{"sc_theirs"})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{id: true}, here)
}
