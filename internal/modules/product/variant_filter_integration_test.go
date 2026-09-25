//go:build integration

package product_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/internal/modules/product"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// variantOf adds a variant to a product and returns its id.
func variantOf(ctx context.Context, t *testing.T, svc *service.Service, productID, title string) string {
	t.Helper()

	variant, err := svc.CreateVariant(ctx, productID, service.CreateVariantInput{Title: title})
	require.NoError(t, err)
	return variant.ID
}

// listedIDs is the product ids of a storefront page, in page order.
func listedIDs(items []service.StoreProduct) []string {
	out := make([]string, 0, len(items))
	for i := range items {
		out = append(out, items[i].ID)
	}
	return out
}

// TestTheCatalogShowsTheProductsOfAListOfVariants is ADR 0191 against the
// database: named variants bring back their products once each, whole, and a
// variant that cannot be shown brings back nothing.
func TestTheCatalogShowsTheProductsOfAListOfVariants(t *testing.T) {
	ctx := context.Background()
	sys := newSystem(t)
	svc, err := container.Resolve[*service.Service](sys.container, product.ServiceName)
	require.NoError(t, err)

	shirt := createStoreProduct(t, svc, "wished-shirt")
	shirtSmall := variantOf(ctx, t, svc, shirt.ID, "S")
	shirtLarge := variantOf(ctx, t, svc, shirt.ID, "L")
	variantOf(ctx, t, svc, shirt.ID, "M")

	elsewhere := createStoreProduct(t, svc, "wished-elsewhere")
	elsewhereVariant := variantOf(ctx, t, svc, elsewhere.ID, "One")
	require.NoError(t, svc.AddProductSalesChannel(ctx, elsewhere.ID, "sc_variant_filter_other"))

	draft, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Title: "wished-draft", Handle: uniqueHandle("wished-draft"), Status: productmodels.StatusDraft,
	})
	require.NoError(t, err)
	draftVariant := variantOf(ctx, t, svc, draft.ID, "One")

	removed := createStoreProduct(t, svc, "wished-removed")
	removedVariant := variantOf(ctx, t, svc, removed.ID, "One")
	variantOf(ctx, t, svc, removed.ID, "Two")
	require.NoError(t, svc.DeleteVariant(ctx, removedVariant))

	unwished := createStoreProduct(t, svc, "unwished")
	variantOf(ctx, t, svc, unwished.ID, "One")

	named := []string{
		shirtSmall, shirtLarge, elsewhereVariant, draftVariant, removedVariant, "variant_never_existed",
	}
	page, err := svc.ListStoreProducts(ctx, service.StoreListOptions{
		VariantIDs:      named,
		SalesChannelIDs: []string{"sc_variant_filter_mine"},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{shirt.ID}, listedIDs(page.Items),
		"two of the shirt's variants bring it back once; the other channel's product, the "+
			"draft, the product whose named variant was deleted, the unknown id and the "+
			"product nobody named are left out")
	require.NotNil(t, page.Count)
	assert.Equal(t, 1, *page.Count, "the count counts products, not named variants")
	assert.Len(t, page.Items[0].Variants, 3, "the product comes back whole")

	unscoped, err := svc.ListStoreProducts(ctx, service.StoreListOptions{VariantIDs: named})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{shirt.ID, elsewhere.ID}, listedIDs(unscoped.Items),
		"without a channel the other channel's product is shown, and the filter still holds")

	nothing, err := svc.ListStoreProducts(ctx, service.StoreListOptions{VariantIDs: []string{}})
	require.NoError(t, err)
	assert.Empty(t, nothing.Items, "a list naming no variant names no product")
}

// TestAVariantFilterIsJudgedBeforeTheDatabase shows the bound and the form of
// the ids refused as invalid input.
func TestAVariantFilterIsJudgedBeforeTheDatabase(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)

	tooMany := make([]string, service.MaxLimit+1)
	for i := range tooMany {
		tooMany[i] = "variant_x"
	}
	for name, ids := range map[string][]string{
		"too many": tooMany,
		"blank":    {""},
		"padded":   {" variant_x"},
	} {
		_, err := svc.ListStoreProducts(ctx, service.StoreListOptions{VariantIDs: ids})
		require.Error(t, err, name)
	}

	_, err := svc.ListStoreProducts(ctx, service.StoreListOptions{VariantIDs: tooMany[:service.MaxLimit]})
	require.NoError(t, err, "a page's worth is taken")
}
