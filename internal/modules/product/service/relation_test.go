package service_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestRelationsKeepTheOperatorsOrder verifies what the admin surface reads back
// (ADR 0180): every kind present, the one written in the order it was given,
// and a second write replacing the list rather than adding to it.
func TestRelationsKeepTheOperatorsOrder(t *testing.T) {
	fx := newChannelFixture(t)
	ctx := context.Background()
	shirt := seedProduct(t, fx.svc, "shirt", "Shirt")
	belt := seedProduct(t, fx.svc, "belt", "Belt")
	socks := seedProduct(t, fx.svc, "socks", "Socks")
	tie := seedProduct(t, fx.svc, "tie", "Tie")

	relations, err := fx.svc.SetProductRelations(ctx, shirt.ID, models.RelationCrossSell,
		[]string{tie.ID, belt.ID, socks.ID})
	require.NoError(t, err)
	assert.Equal(t, []string{tie.ID, belt.ID, socks.ID}, relations[models.RelationCrossSell],
		"the order is the operator's, not the ids' or the products' age")
	for _, kind := range []models.RelationType{models.RelationUpSell, models.RelationSubstitute} {
		assert.NotNil(t, relations[kind], "every kind is present: %s", kind)
		assert.Empty(t, relations[kind])
	}

	relations, err = fx.svc.SetProductRelations(ctx, shirt.ID, models.RelationCrossSell, []string{socks.ID})
	require.NoError(t, err)
	assert.Equal(t, []string{socks.ID}, relations[models.RelationCrossSell], "a write replaces the kind's list")

	_, err = fx.svc.SetProductRelations(ctx, shirt.ID, models.RelationUpSell, []string{tie.ID})
	require.NoError(t, err)
	relations, err = fx.svc.SetProductRelations(ctx, shirt.ID, models.RelationCrossSell, []string{})
	require.NoError(t, err)
	assert.Empty(t, relations[models.RelationCrossSell], "an empty list takes the kind off")
	assert.Equal(t, []string{tie.ID}, relations[models.RelationUpSell], "and leaves the other kinds alone")

	read, err := fx.svc.ProductRelations(ctx, shirt.ID)
	require.NoError(t, err)
	assert.Equal(t, relations, read)
}

// TestARelationListIsRefusedRatherThanShortened verifies the refusals, each
// made before anything is written.
func TestARelationListIsRefusedRatherThanShortened(t *testing.T) {
	fx := newChannelFixture(t)
	ctx := context.Background()
	shirt := seedProduct(t, fx.svc, "shirt", "Shirt")
	belt := seedProduct(t, fx.svc, "belt", "Belt")
	gone := seedProduct(t, fx.svc, "gone", "Gone")
	require.NoError(t, fx.svc.DeleteProduct(ctx, gone.ID))

	// Every id in the long list names a product that exists, so the limit is the
	// only rule it breaks; a list of made-up ids would be refused as missing
	// with the limit gone.
	tooMany := make([]string, service.MaxRelations+1)
	for i := range tooMany {
		tooMany[i] = seedProduct(t, fx.svc, fmt.Sprintf("many-%02d", i), "Many").ID
	}

	for name, tc := range map[string]struct {
		kind  models.RelationType
		ids   []string
		code  string
		names string
	}{
		"an unknown kind": {"accessory", []string{belt.ID}, service.CodeRelationTypeUnknown, "accessory"},
		"a product twice": {models.RelationCrossSell, []string{belt.ID, belt.ID}, "", "more than once"},
		"the product itself": {models.RelationCrossSell, []string{belt.ID, shirt.ID}, "",
			"itself: " + shirt.ID},
		"a product that does not exist": {models.RelationUpSell, []string{belt.ID, "prod_nope"}, "",
			"no such product: prod_nope"},
		"a deleted product":  {models.RelationUpSell, []string{gone.ID}, "", "no such product: " + gone.ID},
		"an empty id":        {models.RelationSubstitute, []string{""}, "", "product_ids is required"},
		"more than the most": {models.RelationSubstitute, tooMany, "", "at most"},
	} {
		t.Run(name, func(t *testing.T) {
			before := fx.store.callCount("ReplaceProductRelations")

			_, err := fx.svc.SetProductRelations(ctx, shirt.ID, tc.kind, tc.ids)

			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "error: %v", err)
			if tc.code != "" {
				assert.Equal(t, tc.code, errors.CodeOf(err))
			}
			assert.Contains(t, err.Error(), tc.names, "the refusal is this rule's and names what it refused")
			assert.Equal(t, before, fx.store.callCount("ReplaceProductRelations"), "nothing was written")
		})
	}

	_, err := fx.svc.SetProductRelations(ctx, "prod_nope", models.RelationCrossSell, []string{belt.ID})
	assert.True(t, errors.IsNotFound(err), "a product that does not exist has no relations to set: %v", err)
	_, err = fx.svc.ProductRelations(ctx, "prod_nope")
	assert.True(t, errors.IsNotFound(err), "nor to read: %v", err)
}

// TestTheStorefrontShowsOnlyWhatItMayShow verifies the storefront read: the
// related products it may show, in the operator's order, and nothing it may not
// — a draft or another channel's product lined up by the operator is skipped
// until it becomes visible.
func TestTheStorefrontShowsOnlyWhatItMayShow(t *testing.T) {
	fx := newChannelFixture(t)
	ctx := context.Background()
	shirt := seedProduct(t, fx.svc, "shirt", "Shirt")
	belt := seedProduct(t, fx.svc, "belt", "Belt")
	socks := seedProduct(t, fx.svc, "socks", "Socks")
	elsewhere := seedProduct(t, fx.svc, "elsewhere", "Elsewhere")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, elsewhere.ID, "sc_b"))
	upcoming := seedProductInput(t, fx.svc, service.CreateProductInput{
		Handle: "upcoming", Title: "Upcoming", Status: models.StatusDraft,
	})

	_, err := fx.svc.SetProductRelations(ctx, shirt.ID, models.RelationCrossSell,
		[]string{socks.ID, upcoming.ID, elsewhere.ID, belt.ID})
	require.NoError(t, err, "a draft and another channel's product can be lined up")

	related, err := fx.svc.StoreRelatedProducts(ctx, "shirt", models.RelationCrossSell, []string{"sc_a"})
	require.NoError(t, err)
	assert.Equal(t, []string{"socks", "belt"}, storeHandles(related),
		"the draft and the other channel's product are skipped; the order stays")

	published := models.StatusPublished
	_, err = fx.svc.UpdateProduct(ctx, upcoming.ID, service.UpdateProductInput{Status: &published})
	require.NoError(t, err)
	related, err = fx.svc.StoreRelatedProducts(ctx, shirt.ID, models.RelationCrossSell, []string{"sc_b"})
	require.NoError(t, err)
	assert.Equal(t, []string{"socks", "upcoming", "elsewhere", "belt"}, storeHandles(related),
		"the launch shows it where the operator put it")

	none, err := fx.svc.StoreRelatedProducts(ctx, shirt.ID, models.RelationUpSell, []string{"sc_a"})
	require.NoError(t, err)
	assert.NotNil(t, none, "a kind with nothing in it is an empty list")
	assert.Empty(t, none)

	_, err = fx.svc.StoreRelatedProducts(ctx, shirt.ID, "accessory", nil)
	assert.Equal(t, service.CodeRelationTypeUnknown, errors.CodeOf(err))
}

// TestARelationListCannotBeReadOffAHiddenProduct verifies that the product the
// read starts from has to be one the storefront may show: its list names
// products, and a draft's or another channel's list is not the storefront's to
// read.
func TestARelationListCannotBeReadOffAHiddenProduct(t *testing.T) {
	fx := newChannelFixture(t)
	ctx := context.Background()
	belt := seedProduct(t, fx.svc, "belt", "Belt")
	draft := seedProductInput(t, fx.svc, service.CreateProductInput{
		Handle: "draft", Title: "Draft", Status: models.StatusDraft,
	})
	elsewhere := seedProduct(t, fx.svc, "elsewhere", "Elsewhere")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, elsewhere.ID, "sc_b"))

	for _, source := range []models.Product{draft, elsewhere} {
		_, err := fx.svc.SetProductRelations(ctx, source.ID, models.RelationCrossSell, []string{belt.ID})
		require.NoError(t, err)

		_, err = fx.svc.StoreRelatedProducts(ctx, source.Handle, models.RelationCrossSell, []string{"sc_a"})
		assert.True(t, errors.IsNotFound(err), "%s: %v", source.Handle, err)
	}
}

// TestADeletedProductLeavesEveryList verifies both directions of a deletion: the
// deleted product's own lists go, and it goes from every list naming it.
func TestADeletedProductLeavesEveryList(t *testing.T) {
	fx := newChannelFixture(t)
	ctx := context.Background()
	shirt := seedProduct(t, fx.svc, "shirt", "Shirt")
	belt := seedProduct(t, fx.svc, "belt", "Belt")
	socks := seedProduct(t, fx.svc, "socks", "Socks")

	_, err := fx.svc.SetProductRelations(ctx, shirt.ID, models.RelationCrossSell, []string{belt.ID, socks.ID})
	require.NoError(t, err)
	_, err = fx.svc.SetProductRelations(ctx, belt.ID, models.RelationSubstitute, []string{shirt.ID})
	require.NoError(t, err)

	require.NoError(t, fx.svc.DeleteProduct(ctx, belt.ID))

	relations, err := fx.svc.ProductRelations(ctx, shirt.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{socks.ID}, relations[models.RelationCrossSell], "it left the list naming it")
	assert.Empty(t, fx.store.relations[belt.ID], "its own lists went with it")
}

// TestAFullListFitsTheStorefrontRead verifies that the longest list a product
// may hold is one the storefront read accepts: the read goes through
// StoreProductsByIDs, which refuses more than MaxLimit ids.
func TestAFullListFitsTheStorefrontRead(t *testing.T) {
	fx := newChannelFixture(t)
	ctx := context.Background()
	shirt := seedProduct(t, fx.svc, "shirt", "Shirt")

	ids := make([]string, service.MaxRelations)
	for i := range ids {
		ids[i] = seedProduct(t, fx.svc, fmt.Sprintf("related-%02d", i), "Related").ID
	}
	_, err := fx.svc.SetProductRelations(ctx, shirt.ID, models.RelationUpSell, ids)
	require.NoError(t, err)

	related, err := fx.svc.StoreRelatedProducts(ctx, shirt.ID, models.RelationUpSell, nil)
	require.NoError(t, err)
	assert.Len(t, related, service.MaxRelations)
}
