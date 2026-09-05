package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestNewRequiresRepo verifies that a setup without a repository is rejected at
// setup time.
func TestNewRequiresRepo(t *testing.T) {
	t.Parallel()

	_, err := service.New(service.Options{})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a missing repository is a validation error: %v", err)
}

// TestCreateProductAssignsPrefixedIDs verifies that the ids carry the prefixes
// from Section 8 of the plan and that the product comes back together with its
// child records.
func TestCreateProductAssignsPrefixedIDs(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	product, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Title:  "Shirt",
		Status: models.StatusPublished,
		Options: []service.CreateOptionInput{
			{Title: "Size", Values: []string{"S", "M"}},
		},
		Variants: []service.CreateVariantInput{
			{Title: "Size S", Options: map[string]string{"Size": "S"}},
		},
		Images: []service.CreateImageInput{{URL: "https://cdn.example/1.png"}},
	})
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(product.ID, "prod_"), "the product id should start with prod_: %s", product.ID)
	require.Len(t, product.Variants, 1)
	assert.True(t, strings.HasPrefix(product.Variants[0].ID, "variant_"),
		"the variant id should start with variant_: %s", product.Variants[0].ID)
	require.Len(t, product.Options, 1)
	assert.True(t, strings.HasPrefix(product.Options[0].ID, "popt_"),
		"the option id should start with popt_: %s", product.Options[0].ID)
	require.Len(t, product.Options[0].Values, 2)
	assert.True(t, strings.HasPrefix(product.Options[0].Values[0].ID, "poptval_"),
		"the option value id should start with poptval_: %s", product.Options[0].Values[0].ID)
	require.Len(t, product.Images, 1)
	assert.True(t, strings.HasPrefix(product.Images[0].ID, "pimg_"),
		"the image id should start with pimg_: %s", product.Images[0].ID)
}

// TestCreateProductDerivesHandleFromTitle verifies that when no handle is given
// it is derived from the title (Turkish letters are folded to ASCII).
func TestCreateProductDerivesHandleFromTitle(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	product, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Title: "\u015e\u0131k Ti\u015f\u00f6rt  Mavi",
	})
	require.NoError(t, err)
	assert.Equal(t, "sik-tisort-mavi", product.Handle)
}

// TestCreateProductDerivedHandleStaysAddressable verifies that a handle derived
// from a long title leaves the product REACHABLE in the storefront.
//
// A title can be up to 255 characters and a handle up to 128; had the derived
// slug not been truncated, the product would be created but
// /store/v1/products/{handle} would return 422 and the record could never be
// opened at its own address.
func TestCreateProductDerivedHandleStaysAddressable(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()

	title := strings.Repeat("a lengthy title ", 15) + "end"
	require.Len(t, title, 243, "the title should be under the limit (255) but well above the handle limit (128)")

	product, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Title:  title,
		Status: models.StatusPublished,
	})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(product.Handle), 128, "the derived handle should stay within the limit")
	assert.NotEmpty(t, product.Handle)
	assert.False(t, strings.HasSuffix(product.Handle, "-"), "the truncation should not leave a trailing dash")

	// The real assertion: the product has to be openable in the storefront with
	// its own handle.
	fetched, err := svc.GetStoreProduct(ctx, product.Handle, nil)
	require.NoError(t, err, "the product should be readable with the derived handle")
	assert.Equal(t, product.ID, fetched.ID)
}

// TestCreateProductValidations verifies the cases in which the input is rejected.
func TestCreateProductValidations(t *testing.T) {
	t.Parallel()

	cases := map[string]service.CreateProductInput{
		"empty title":         {Title: "   "},
		"invalid status":      {Title: "Shirt", Status: models.Status("live")},
		"invalid handle":      {Title: "Shirt", Handle: "Upper Case"},
		"empty variant title": {Title: "Shirt", Variants: []service.CreateVariantInput{{Title: ""}}},
		"empty option title":  {Title: "Shirt", Options: []service.CreateOptionInput{{Title: " "}}},
		"duplicate option":    {Title: "Shirt", Options: []service.CreateOptionInput{{Title: "Size"}, {Title: "size"}}},
		"duplicate value":     {Title: "Shirt", Options: []service.CreateOptionInput{{Title: "Size", Values: []string{"S", "s"}}}},
		"tag id with whitespace": {
			Title:  "Shirt",
			TagIDs: []string{"ptag_1\n"},
		},
	}

	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svc := newService(t, newMemStore(), newFakeLinker(), nil)

			_, err := svc.CreateProduct(context.Background(), in)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "a validation error was expected: %v", err)
		})
	}
}

// TestCreateProductHandleConflict verifies that the same handle cannot be used a
// second time and that the error is of the Conflict kind.
func TestCreateProductHandleConflict(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()

	_, err := svc.CreateProduct(ctx, service.CreateProductInput{Handle: "shirt", Title: "Shirt"})
	require.NoError(t, err)

	_, err = svc.CreateProduct(ctx, service.CreateProductInput{Handle: "shirt", Title: "Another Shirt"})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "a conflict error was expected: %v", err)
	assert.Equal(t, "product_handle_taken", errors.CodeOf(err))
}

// TestCreateProductConflictFromStore verifies that even when the pre-check is
// SKIPPED the conflict comes from the repository (the counterpart of the
// database constraint).
//
// This is the unit-test counterpart of the scenario where two concurrent
// requests slip past each other: the pre-check says "empty", the write conflicts
// all the same.
func TestCreateProductConflictFromStore(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	ctx := context.Background()

	_, err := svc.CreateProduct(ctx, service.CreateProductInput{Handle: "shirt", Title: "Shirt"})
	require.NoError(t, err)

	// The pre-check now says "not found"; the only defense is the repository
	// constraint.
	store.fail("GetProductByHandle", errors.NotFound("product_not_found", "not found"))

	_, err = svc.CreateProduct(ctx, service.CreateProductInput{Handle: "shirt", Title: "Another Shirt"})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "the conflict coming from the repository should be preserved: %v", err)
}

// TestCreateProductIsSingleTransaction verifies that the product and its child
// records are written in a SINGLE transaction.
func TestCreateProductIsSingleTransaction(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)

	_, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Title:    "Shirt",
		Options:  []service.CreateOptionInput{{Title: "Size", Values: []string{"S", "M"}}},
		Variants: []service.CreateVariantInput{{Title: "S"}, {Title: "M"}},
		Images:   []service.CreateImageInput{{URL: "https://cdn.example/1.png"}},
	})
	require.NoError(t, err)

	assert.Equal(t, 1, store.callCount("InTx"),
		"the product, the options, the variants and the images should be written in one transaction")
}

// TestCreateProductRollsBackOnVariantFailure verifies that when the variant write
// blows up the error reaches the caller.
func TestCreateProductRollsBackOnVariantFailure(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	store.fail("CreateVariant", errors.Internal("db", "the variant could not be written"))
	svc := newService(t, store, newFakeLinker(), nil)

	_, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Title:    "Shirt",
		Variants: []service.CreateVariantInput{{Title: "S"}},
	})
	require.Error(t, err)
	assert.Equal(t, "db", errors.CodeOf(err))
}

// TestCreateVariantBindsOptionValuesByTitle verifies that the variant is bound to
// its option values through the title.
func TestCreateVariantBindsOptionValuesByTitle(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	product, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Title:   "Shirt",
		Options: []service.CreateOptionInput{{Title: "Size", Values: []string{"S", "M"}}},
		Variants: []service.CreateVariantInput{
			{Title: "Size S", Options: map[string]string{"size": "s"}},
		},
	})
	require.NoError(t, err)

	require.Len(t, product.Variants, 1)
	require.Len(t, product.Variants[0].OptionValues, 1)
	assert.Equal(t, "S", product.Variants[0].OptionValues[0].Value)
	assert.Equal(t, "Size", product.Variants[0].OptionValues[0].OptionTitle)
}

// TestCreateVariantRejectsUnknownOptionValue verifies that an undefined option
// value is not skipped silently.
func TestCreateVariantRejectsUnknownOptionValue(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()

	product, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Title:   "Shirt",
		Options: []service.CreateOptionInput{{Title: "Size", Values: []string{"S"}}},
	})
	require.NoError(t, err)

	_, err = svc.CreateVariant(ctx, product.ID, service.CreateVariantInput{
		Title:   "Size XL",
		Options: map[string]string{"Size": "XL"},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "an undefined value should give a validation error: %v", err)
}

// TestCreateVariantRejectsForeignOptionValue verifies that the option value of
// another product cannot be bound.
func TestCreateVariantRejectsForeignOptionValue(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()

	foreign, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Title:   "Trousers",
		Options: []service.CreateOptionInput{{Title: "Size", Values: []string{"42"}}},
	})
	require.NoError(t, err)
	target, err := svc.CreateProduct(ctx, service.CreateProductInput{Title: "Shirt"})
	require.NoError(t, err)

	_, err = svc.CreateVariant(ctx, target.ID, service.CreateVariantInput{
		Title:          "Wrong",
		OptionValueIDs: []string{foreign.Options[0].Values[0].ID},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a foreign value should be rejected: %v", err)
}

// TestCreateVariantRequiresProduct verifies that no variant can be added to a
// product that does not exist.
func TestCreateVariantRequiresProduct(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	_, err := svc.CreateVariant(context.Background(), "prod_missing", service.CreateVariantInput{Title: "S"})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a not found was expected: %v", err)
}

// TestCreateOptionReturnsStoredRow verifies that the created option comes back as
// the STORED row.
//
// The timestamps are produced by the database; had the in-memory model been
// returned, the response would carry "created_at":"0001-01-01T00:00:00Z" (there
// is no omitzero on those fields in models.Option) and a client that trusts the
// stamp would read wrong data. Every other create endpoint returns the stored
// row; this endpoint should not depart from the contract.
func TestCreateOptionReturnsStoredRow(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()
	product := seedProduct(t, svc, "shirt", "Shirt")

	option, err := svc.CreateOption(ctx, product.ID, service.CreateOptionInput{
		Title:  "Size",
		Values: []string{"S", "M"},
	})
	require.NoError(t, err)
	assert.False(t, option.CreatedAt.IsZero(), "the option should come back as the stored row")
	assert.False(t, option.UpdatedAt.IsZero(), "the option should come back as the stored row")

	require.Len(t, option.Values, 2, "the values should come back too")
	for _, value := range option.Values {
		assert.False(t, value.CreatedAt.IsZero(),
			"the %q value should come back as the stored row", value.Value)
	}
}

// TestAddOptionValueAppendsToEnd verifies that a value added later goes to the
// END of the list.
//
// Had the rank not been filled in, the new value would get 0 and, because reads
// are ordered by rank, an "XL" added to an option defined as "S, M, L" would fall
// at the head.
func TestAddOptionValueAppendsToEnd(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()
	product := seedProduct(t, svc, "shirt", "Shirt")

	option, err := svc.CreateOption(ctx, product.ID, service.CreateOptionInput{
		Title:  "Size",
		Values: []string{"S", "M", "L"},
	})
	require.NoError(t, err)

	added, err := svc.AddOptionValue(ctx, option.ID, "XL")
	require.NoError(t, err)
	assert.Equal(t, int32(3), added.Rank, "the new value should get one more than the largest rank")

	options, err := svc.ListOptions(ctx, product.ID)
	require.NoError(t, err)
	require.Len(t, options, 1)

	values := make([]string, 0, len(options[0].Values))
	for _, value := range options[0].Values {
		values = append(values, value.Value)
	}
	assert.Equal(t, []string{"S", "M", "L", "XL"}, values,
		"the value added later should be at the end of the list")
}

// TestUpdateProductKeepsOwnHandle verifies that updating a product with its own
// handle does not count as a conflict.
func TestUpdateProductKeepsOwnHandle(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()

	product, err := svc.CreateProduct(ctx, service.CreateProductInput{Handle: "shirt", Title: "Shirt"})
	require.NoError(t, err)

	updated, err := svc.UpdateProduct(ctx, product.ID, service.UpdateProductInput{
		Handle: ptr("shirt"),
		Title:  ptr("Shirt v2"),
	})
	require.NoError(t, err)
	assert.Equal(t, "Shirt v2", updated.Title)
}

// TestUpdateProductRejectsTakenHandle verifies that another product's handle
// cannot be taken.
func TestUpdateProductRejectsTakenHandle(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()

	_, err := svc.CreateProduct(ctx, service.CreateProductInput{Handle: "shirt", Title: "Shirt"})
	require.NoError(t, err)
	other, err := svc.CreateProduct(ctx, service.CreateProductInput{Handle: "trousers", Title: "Trousers"})
	require.NoError(t, err)

	_, err = svc.UpdateProduct(ctx, other.ID, service.UpdateProductInput{Handle: ptr("shirt")})
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "a conflict was expected: %v", err)
}

// TestDeleteProductSoftDeletesAndCleansLinks verifies that the delete drops the
// product from the reads and cleans up the price/stock links of the variant.
func TestDeleteProductSoftDeletesAndCleansLinks(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	svc := newService(t, newMemStore(), links, nil)
	ctx := context.Background()

	product := seedProduct(t, svc, "shirt", "Shirt")
	variantID := product.Variants[0].ID
	require.NoError(t, svc.SetVariantPriceSet(ctx, variantID, "pset_1"))
	require.NoError(t, svc.SetVariantInventoryItem(ctx, variantID, "invitem_1"))

	require.NoError(t, svc.DeleteProduct(ctx, product.ID))

	_, err := svc.GetProduct(ctx, product.ID)
	assert.True(t, errors.IsNotFound(err), "a deleted product should not be readable: %v", err)
	assert.Empty(t, links.linked(service.LinkVariantPriceSet, variantID),
		"the variant of a deleted product should not stay linked to a price set")
	assert.Empty(t, links.linked(service.LinkVariantInventory, variantID),
		"the variant of a deleted product should not stay linked to a stock item")
}

// TestDeleteProductNotFound verifies that deleting a product that does not exist
// does not succeed silently.
func TestDeleteProductNotFound(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	err := svc.DeleteProduct(context.Background(), "prod_missing")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a not found was expected: %v", err)
}

// TestDeleteVariantCleansLinks verifies that the links are cleaned up when a
// variant is deleted.
func TestDeleteVariantCleansLinks(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	svc := newService(t, newMemStore(), links, nil)
	ctx := context.Background()

	product := seedProduct(t, svc, "shirt", "Shirt")
	variantID := product.Variants[0].ID
	require.NoError(t, svc.SetVariantPriceSet(ctx, variantID, "pset_1"))

	require.NoError(t, svc.DeleteVariant(ctx, variantID))

	_, err := svc.GetVariant(ctx, variantID)
	assert.True(t, errors.IsNotFound(err), "a deleted variant should not be readable: %v", err)
	assert.Empty(t, links.linked(service.LinkVariantPriceSet, variantID))
}

// TestListProductsPaging verifies the paging contract: the default limit, the
// ceiling clamp and the rejection of a negative value.
func TestListProductsPaging(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()

	for _, title := range []string{"One", "Two", "Three"} {
		_, err := svc.CreateProduct(ctx, service.CreateProductInput{Title: title})
		require.NoError(t, err)
	}

	result, err := svc.ListProducts(ctx, service.ListProductsOptions{})
	require.NoError(t, err)
	assert.Equal(t, service.DefaultLimit, result.Limit, "when no limit is given the default should be used")
	assert.Equal(t, 3, requireCount(t, result), "the count is the total, independent of the page")
	assert.Len(t, result.Items, 3)

	result, err = svc.ListProducts(ctx, service.ListProductsOptions{Limit: 5000})
	require.NoError(t, err)
	assert.Equal(t, service.MaxLimit, result.Limit, "a limit above the ceiling should be clamped")

	result, err = svc.ListProducts(ctx, service.ListProductsOptions{Limit: 2, Offset: 2})
	require.NoError(t, err)
	assert.Equal(t, 3, requireCount(t, result), "the count should not be affected by the paging")
	assert.Len(t, result.Items, 1)

	_, err = svc.ListProducts(ctx, service.ListProductsOptions{Limit: -1})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a negative limit should be rejected: %v", err)

	_, err = svc.ListProducts(ctx, service.ListProductsOptions{Offset: -1})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a negative offset should be rejected: %v", err)
}

// TestListProductsWithRelationsIsBatched verifies that the related records are
// read in bulk and NOT per product: this is the unit-test evidence against N+1.
func TestListProductsWithRelationsIsBatched(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	ctx := context.Background()

	for _, title := range []string{"One", "Two", "Three", "Four"} {
		_, err := svc.CreateProduct(ctx, service.CreateProductInput{
			Title:    title,
			Variants: []service.CreateVariantInput{{Title: "Single"}, {Title: "Double"}},
		})
		require.NoError(t, err)
	}

	before := map[string]int{
		"ListVariantsByProductIDs": store.callCount("ListVariantsByProductIDs"),
		"ListVariantOptionValues":  store.callCount("ListVariantOptionValues"),
		"ListOptionsByProductIDs":  store.callCount("ListOptionsByProductIDs"),
		"ListImagesByProductIDs":   store.callCount("ListImagesByProductIDs"),
	}

	result, err := svc.ListProducts(ctx, service.ListProductsOptions{WithRelations: true})
	require.NoError(t, err)
	require.Len(t, result.Items, 4)
	require.Len(t, result.Items[0].Variants, 2)

	for name, previous := range before {
		assert.Equal(t, previous+1, store.callCount(name),
			"%s should be called ONCE, independent of the number of products", name)
	}
}

// TestListProductsWithoutRelationsSkipsChildQueries verifies that when the
// relations are not requested the child queries are not made at all.
func TestListProductsWithoutRelationsSkipsChildQueries(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	ctx := context.Background()

	_, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Title:    "Shirt",
		Variants: []service.CreateVariantInput{{Title: "Single"}},
	})
	require.NoError(t, err)

	before := store.callCount("ListVariantsByProductIDs")
	_, err = svc.ListProducts(ctx, service.ListProductsOptions{})
	require.NoError(t, err)
	assert.Equal(t, before, store.callCount("ListVariantsByProductIDs"),
		"if no relation was requested the variants should not be read")
}

// TestGetProductNotFound verifies that an unknown id returns NotFound.
func TestGetProductNotFound(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	_, err := svc.GetProduct(context.Background(), "prod_missing")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a not found was expected: %v", err)
}

// TestCreateTagRejectsDuplicate verifies that the same tag cannot be added a
// second time.
func TestCreateTagRejectsDuplicate(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()

	tag, err := svc.CreateTag(ctx, " summer ")
	require.NoError(t, err)
	assert.Equal(t, "summer", tag.Value, "the value should be trimmed")
	assert.True(t, strings.HasPrefix(tag.ID, "ptag_"))

	_, err = svc.CreateTag(ctx, "summer")
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "a conflict was expected: %v", err)
}

// TestCreateCategoryRequiresExistingParent verifies that a parent category that
// does not exist is rejected.
func TestCreateCategoryRequiresExistingParent(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	_, err := svc.CreateCategory(context.Background(), service.CreateCategoryInput{
		Name:     "Child",
		ParentID: ptr("pcat_missing"),
	})
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a not found was expected: %v", err)
}

// TestTheStorefrontDoesNotSeeAHiddenCategory is the point of the whole
// PublicOnly flag.
//
// is_active and is_internal have been columns since the first migration and
// nothing read them until the storefront needed a vocabulary: the merchant
// could switch a category off and it stayed exactly as visible as before. This
// is the test that says the switch does something.
func TestTheStorefrontDoesNotSeeAHiddenCategory(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()

	visible, err := svc.CreateCategory(ctx, service.CreateCategoryInput{Name: "Shirts"})
	require.NoError(t, err)
	_, err = svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Not ready", IsActive: ptr(false)})
	require.NoError(t, err)
	_, err = svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Operators only", IsInternal: true})
	require.NoError(t, err)

	shop, err := svc.ListCategories(ctx, service.ListCategoriesOptions{PublicOnly: true})
	require.NoError(t, err)

	require.Len(t, shop.Items, 1, "a switched-off or internal category reached the storefront")
	assert.Equal(t, visible.ID, shop.Items[0].ID)
	require.NotNil(t, shop.Count)
	assert.Equal(t, 1, *shop.Count,
		"the count was taken over a wider set than the page; a storefront would ask for "+
			"pages that never fill")
}

// TestTheMerchantStillSeesAHiddenCategory is the other half, and it is the one
// that keeps the flag usable.
//
// A merchant who cannot see a category they switched off has no way to switch
// it back on. That is why PublicOnly defaults to FALSE and the storefront is
// the caller that has to ask for the narrower view.
func TestTheMerchantStillSeesAHiddenCategory(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()

	_, err := svc.CreateCategory(ctx, service.CreateCategoryInput{Name: "Shirts"})
	require.NoError(t, err)
	_, err = svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Not ready", IsActive: ptr(false)})
	require.NoError(t, err)

	admin, err := svc.ListCategories(ctx, service.ListCategoriesOptions{})
	require.NoError(t, err)

	assert.Len(t, admin.Items, 2, "the admin surface lost sight of a switched-off category")
}
