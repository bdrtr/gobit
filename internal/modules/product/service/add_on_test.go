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

// TestAnAddOnListKeepsTheOperatorsOrder is ADR 0228: the list is read back in
// the order written, a second write replaces it, an empty one takes every
// add-on off, and a product with none answers an empty list.
func TestAnAddOnListKeepsTheOperatorsOrder(t *testing.T) {
	fx := newChannelFixture(t)
	ctx := context.Background()
	ring := seedProduct(t, fx.svc, "ring", "Ring")
	engraving := seedProduct(t, fx.svc, "engraving", "Engraving").Variants[0].ID
	wrap := seedProduct(t, fx.svc, "wrap", "Wrap").Variants[0].ID

	none, err := fx.svc.ProductAddOns(ctx, ring.ID)
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none)

	written, err := fx.svc.SetProductAddOns(ctx, ring.ID, []string{wrap, engraving})
	require.NoError(t, err)
	assert.Equal(t, []string{wrap, engraving}, written, "the order is the operator's")
	read, err := fx.svc.ProductAddOns(ctx, ring.ID)
	require.NoError(t, err)
	assert.Equal(t, written, read)

	written, err = fx.svc.SetProductAddOns(ctx, ring.ID, []string{engraving})
	require.NoError(t, err)
	assert.Equal(t, []string{engraving}, written, "a write replaces the list")

	written, err = fx.svc.SetProductAddOns(ctx, ring.ID, []string{})
	require.NoError(t, err)
	assert.Empty(t, written, "an empty list takes every add-on off")
}

// TestAnAddOnListIsRefusedRatherThanShortened holds the refusals, each made
// before anything is written: a variant named twice, a variant that does not
// exist, the product's own variant, and a list past the ceiling.
func TestAnAddOnListIsRefusedRatherThanShortened(t *testing.T) {
	fx := newChannelFixture(t)
	ctx := context.Background()
	ring := seedProduct(t, fx.svc, "ring", "Ring")
	engraving := seedProduct(t, fx.svc, "engraving", "Engraving").Variants[0].ID
	_, err := fx.svc.SetProductAddOns(ctx, ring.ID, []string{engraving})
	require.NoError(t, err)

	many := make([]string, 0, service.MaxAddOns+1)
	for i := range service.MaxAddOns + 1 {
		many = append(many, seedProduct(t, fx.svc, fmt.Sprintf("extra-%d", i), "Extra").Variants[0].ID)
	}
	for name, ids := range map[string][]string{
		"named twice":      {engraving, engraving},
		"no such variant":  {"variant_missing"},
		"its own variant":  {ring.Variants[0].ID},
		"past the ceiling": many,
		"an empty variant": {""},
	} {
		_, err := fx.svc.SetProductAddOns(ctx, ring.ID, ids)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalid(err), "%s: %v", name, err)
	}
	read, err := fx.svc.ProductAddOns(ctx, ring.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{engraving}, read, "a refused list writes nothing")

	_, err = fx.svc.SetProductAddOns(ctx, "prod_missing", []string{engraving})
	assert.True(t, errors.IsNotFound(err), "a product that does not exist has no list: %v", err)
}

// TestADeletedAddOnLeavesTheList holds the two deletion hooks: a deleted
// variant leaves every list naming it, and a deleted product takes the entries
// naming its variants.
func TestADeletedAddOnLeavesTheList(t *testing.T) {
	fx := newChannelFixture(t)
	ctx := context.Background()
	ring := seedProduct(t, fx.svc, "ring", "Ring")
	engraving := seedProduct(t, fx.svc, "engraving", "Engraving").Variants[0].ID
	wrapProduct := seedProduct(t, fx.svc, "wrap", "Wrap")
	_, err := fx.svc.SetProductAddOns(ctx, ring.ID, []string{engraving, wrapProduct.Variants[0].ID})
	require.NoError(t, err)

	require.NoError(t, fx.svc.DeleteVariant(ctx, engraving))
	read, err := fx.svc.ProductAddOns(ctx, ring.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{wrapProduct.Variants[0].ID}, read)

	require.NoError(t, fx.svc.DeleteProduct(ctx, wrapProduct.ID))
	read, err = fx.svc.ProductAddOns(ctx, ring.ID)
	require.NoError(t, err)
	assert.Empty(t, read)
}

// TestTheStorefrontShowsTheAddOnsItMayShow is the storefront read (ADR 0228):
// the add-ons whose product it may show, in the operator's order, each with its
// variant and product; a draft's and another channel's are skipped until they
// become visible, and a hidden product's list is not read.
func TestTheStorefrontShowsTheAddOnsItMayShow(t *testing.T) {
	fx := newChannelFixture(t)
	ctx := context.Background()
	ring := seedProduct(t, fx.svc, "ring", "Ring")
	wrap := seedProduct(t, fx.svc, "wrap", "Wrap")
	engraving := seedProduct(t, fx.svc, "engraving", "Engraving")
	elsewhere := seedProduct(t, fx.svc, "elsewhere", "Elsewhere")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, elsewhere.ID, "sc_b"))
	upcoming := seedProductInput(t, fx.svc, service.CreateProductInput{
		Handle: "upcoming", Title: "Upcoming", Status: models.StatusDraft,
		Variants: []service.CreateVariantInput{{Title: "Upcoming"}},
	})
	_, err := fx.svc.SetProductAddOns(ctx, ring.ID, []string{
		wrap.Variants[0].ID, upcoming.Variants[0].ID, elsewhere.Variants[0].ID, engraving.Variants[0].ID,
	})
	require.NoError(t, err)

	shown, err := fx.svc.StoreProductAddOns(ctx, "ring", []string{"sc_a"})
	require.NoError(t, err)
	require.Len(t, shown, 2, "the draft and the other channel's add-on are skipped")
	assert.Equal(t, wrap.Variants[0].ID, shown[0].VariantID)
	assert.Equal(t, "wrap", shown[0].Product.Handle)
	assert.Equal(t, engraving.Variants[0].ID, shown[1].VariantID, "the order stays the operator's")
	assert.Equal(t, "engraving", shown[1].Product.Handle)

	published := models.StatusPublished
	_, err = fx.svc.UpdateProduct(ctx, upcoming.ID, service.UpdateProductInput{Status: &published})
	require.NoError(t, err)
	shown, err = fx.svc.StoreProductAddOns(ctx, ring.ID, []string{"sc_b"})
	require.NoError(t, err)
	var order []string
	for _, addOn := range shown {
		order = append(order, addOn.Product.Handle)
	}
	assert.Equal(t, []string{"wrap", "upcoming", "elsewhere", "engraving"}, order,
		"the launch shows it where the operator put it")

	hidden := seedProductInput(t, fx.svc, service.CreateProductInput{
		Handle: "hidden", Title: "Hidden", Status: models.StatusDraft,
	})
	_, err = fx.svc.SetProductAddOns(ctx, hidden.ID, []string{wrap.Variants[0].ID})
	require.NoError(t, err)
	_, err = fx.svc.StoreProductAddOns(ctx, hidden.ID, []string{"sc_a"})
	assert.True(t, errors.IsNotFound(err), "a draft's list is not the storefront's to read: %v", err)
}
