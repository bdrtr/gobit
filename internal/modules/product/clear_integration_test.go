//go:build integration

package product_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// text returns a pointer to the given text.
func text(v string) *string { return &v }

// TestAnUpdateGivenAnEmptyTextClearsIt is D177 on the real schema (ADR 0256).
// An absent optional text keeps its value, an empty one clears it to NULL, and
// any other is trimmed. Before, a product's and a category's empty texts were
// ignored, and a variant's empty codes were written as empty strings, so
// clearing a second variant's SKU was refused as a duplicate.
func TestAnUpdateGivenAnEmptyTextClearsIt(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)

	prod, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("clear"), Title: "Clear", Status: models.StatusPublished,
		Subtitle: text("A subtitle"), Description: text("A description"), Material: text("Wool"),
		Variants: []service.CreateVariantInput{
			{Title: "Small", SKU: text(uniqueHandle("sku-small"))},
			{Title: "Large", SKU: text(uniqueHandle("sku-large"))},
		},
	})
	require.NoError(t, err)
	require.Len(t, prod.Variants, 2)

	updated, err := svc.UpdateProduct(ctx, prod.ID, service.UpdateProductInput{
		Subtitle: text("  "), Material: text("  Merino wool "),
	})
	require.NoError(t, err)
	read, err := svc.GetProduct(ctx, prod.ID)
	require.NoError(t, err)
	for _, p := range []models.Product{updated, read} {
		assert.Nil(t, p.Subtitle, "an empty subtitle clears it")
		require.NotNil(t, p.Description)
		assert.Equal(t, "A description", *p.Description, "an absent description is kept")
		require.NotNil(t, p.Material)
		assert.Equal(t, "Merino wool", *p.Material, "a given text is trimmed")
	}

	for _, variant := range prod.Variants {
		cleared, err := svc.UpdateVariant(ctx, variant.ID, service.UpdateVariantInput{SKU: text("")})
		require.NoError(t, err, "clearing %s's SKU was refused", variant.Title)
		assert.Nil(t, cleared.SKU, "an empty SKU clears it rather than writing an empty one")
	}
	sku := uniqueHandle("sku-trimmed")
	trimmed, err := svc.UpdateVariant(ctx, prod.Variants[0].ID, service.UpdateVariantInput{SKU: text("  " + sku + " ")})
	require.NoError(t, err)
	require.NotNil(t, trimmed.SKU)
	assert.Equal(t, sku, *trimmed.SKU, "a given SKU is trimmed as a create trims it")
	_, err = svc.UpdateVariant(ctx, prod.Variants[1].ID, service.UpdateVariantInput{SKU: text(sku)})
	require.Error(t, err, "the trimmed SKU is the same SKU")
	assert.True(t, errors.IsConflict(err), "error: %v", err)

	category, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Clearable", Handle: uniqueHandle("clearable"), Description: text("Soon gone"),
	})
	require.NoError(t, err)
	category, err = svc.UpdateCategory(ctx, category.ID, service.UpdateCategoryInput{Description: text("")})
	require.NoError(t, err)
	assert.Nil(t, category.Description, "an empty description clears the category's")
}
