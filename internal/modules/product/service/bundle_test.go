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

// bundleFixture is a gift box to make a bundle of and two products to fill it.
type bundleFixture struct {
	channelFixture
	box, soap, towel models.Product
}

func newBundleFixture(t *testing.T) bundleFixture {
	t.Helper()
	fx := newChannelFixture(t)
	return bundleFixture{
		channelFixture: fx,
		box:            seedProduct(t, fx.svc, "gift-box", "Gift box"),
		soap:           seedProduct(t, fx.svc, "soap", "Soap"),
		towel:          seedProduct(t, fx.svc, "towel", "Towel"),
	}
}

// components is the box's usual composition: two soaps and a towel.
func (fx bundleFixture) components() []models.BundleComponent {
	return []models.BundleComponent{
		{VariantID: fx.towel.Variants[0].ID, Quantity: 1},
		{VariantID: fx.soap.Variants[0].ID, Quantity: 2},
	}
}

// TestABundleKeepsTheOperatorsOrder is ADR 0234: the composition is read back
// in the order written, on its own and on the variant and product reads; a
// second write replaces it and an empty one makes the variant plain again.
func TestABundleKeepsTheOperatorsOrder(t *testing.T) {
	fx := newBundleFixture(t)
	ctx := context.Background()
	box := fx.box.Variants[0].ID

	none, err := fx.svc.VariantBundle(ctx, box)
	require.NoError(t, err)
	assert.NotNil(t, none)
	assert.Empty(t, none)

	written, err := fx.svc.SetVariantBundle(ctx, box, fx.components())
	require.NoError(t, err)
	assert.Equal(t, fx.components(), written, "the order is the operator's")

	variant, err := fx.svc.GetVariant(ctx, box)
	require.NoError(t, err)
	assert.Equal(t, fx.components(), variant.BundleComponents, "the variant read carries the composition")
	product, err := fx.svc.GetProduct(ctx, fx.box.ID)
	require.NoError(t, err)
	assert.Equal(t, fx.components(), product.Variants[0].BundleComponents,
		"the product read carries it on the variant")
	soap, err := fx.svc.GetVariant(ctx, fx.soap.Variants[0].ID)
	require.NoError(t, err)
	assert.Empty(t, soap.BundleComponents, "a component is no bundle")

	written, err = fx.svc.SetVariantBundle(ctx, box, fx.components()[1:])
	require.NoError(t, err)
	assert.Equal(t, fx.components()[1:], written, "a write replaces the composition")

	written, err = fx.svc.SetVariantBundle(ctx, box, nil)
	require.NoError(t, err)
	assert.Empty(t, written, "an empty composition makes the variant plain")
}

// TestABundleWriteIsARevision holds that the composition is a revision of the
// bundle's product: its version moves, and the snapshot carries it.
func TestABundleWriteIsARevision(t *testing.T) {
	fx := newBundleFixture(t)
	ctx := context.Background()
	before, err := fx.svc.GetProduct(ctx, fx.box.ID)
	require.NoError(t, err)

	_, err = fx.svc.SetVariantBundle(ctx, fx.box.Variants[0].ID, fx.components())
	require.NoError(t, err)

	after, err := fx.svc.GetProduct(ctx, fx.box.ID)
	require.NoError(t, err)
	assert.Equal(t, before.Version+1, after.Version)
	rev, err := fx.svc.GetRevision(ctx, fx.box.ID, after.Version)
	require.NoError(t, err)
	assert.Contains(t, string(rev.Snapshot), `"bundle_components"`)
}

// TestABundleIsRefusedRatherThanShortened holds the refusals of what was given,
// each made before anything is written.
func TestABundleIsRefusedRatherThanShortened(t *testing.T) {
	fx := newBundleFixture(t)
	ctx := context.Background()
	box := fx.box.Variants[0].ID
	_, err := fx.svc.SetVariantBundle(ctx, box, fx.components())
	require.NoError(t, err)

	pair := seedProductInput(t, fx.svc, service.CreateProductInput{
		Handle: "pair", Title: "Pair", Status: models.StatusPublished,
		Variants: []service.CreateVariantInput{{Title: "Left"}, {Title: "Right"}},
	})
	card := seedProductInput(t, fx.svc, service.CreateProductInput{
		Handle: "card", Title: "Card", Status: models.StatusPublished, IsGiftcard: true,
	})
	many := make([]models.BundleComponent, 0, service.MaxBundleComponents+1)
	for i := range service.MaxBundleComponents + 1 {
		extra := seedProduct(t, fx.svc, fmt.Sprintf("extra-%d", i), "Extra").Variants[0].ID
		many = append(many, models.BundleComponent{VariantID: extra, Quantity: 1})
	}
	soap := fx.soap.Variants[0].ID
	one := func(id string, quantity int32) []models.BundleComponent {
		return []models.BundleComponent{{VariantID: id, Quantity: quantity}}
	}

	for name, c := range map[string]struct {
		bundle     string
		components []models.BundleComponent
	}{
		"named twice":           {box, append(one(soap, 1), one(soap, 1)...)},
		"no such variant":       {box, one("variant_missing", 1)},
		"an empty variant":      {box, one("", 1)},
		"itself":                {box, one(box, 1)},
		"no units":              {box, one(soap, 0)},
		"past the unit ceiling": {box, one(soap, service.MaxBundleComponentQuantity+1)},
		"past the ceiling":      {box, many},
		"its product's own":     {pair.Variants[0].ID, one(pair.Variants[1].ID, 1)},
		"a gift card component": {box, one(card.Variants[0].ID, 1)},
		"a gift card as bundle": {card.Variants[0].ID, one(soap, 1)},
	} {
		_, err := fx.svc.SetVariantBundle(ctx, c.bundle, c.components)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalid(err), "%s: %v", name, err)
	}
	read, err := fx.svc.VariantBundle(ctx, box)
	require.NoError(t, err)
	assert.Equal(t, fx.components(), read, "a refused composition writes nothing")

	_, err = fx.svc.SetVariantBundle(ctx, "variant_missing", one(soap, 1))
	assert.True(t, errors.IsNotFound(err), "a variant that does not exist has no composition: %v", err)
}

// TestABundleIsRefusedByTheCatalogsState holds the refusals another record
// has to change first: no bundle inside a bundle, either way round, and a
// bundle is counted, not sold past zero, and has no stock item of its own.
func TestABundleIsRefusedByTheCatalogsState(t *testing.T) {
	fx := newBundleFixture(t)
	ctx := context.Background()
	box := fx.box.Variants[0].ID
	soap := fx.soap.Variants[0].ID
	_, err := fx.svc.SetVariantBundle(ctx, box, fx.components())
	require.NoError(t, err)
	crate := seedProduct(t, fx.svc, "crate", "Crate").Variants[0].ID
	loose := seedProductInput(t, fx.svc, service.CreateProductInput{
		Handle: "loose", Title: "Loose", Status: models.StatusPublished,
		Variants: []service.CreateVariantInput{{Title: "Loose", ManageInventory: ptr(false)}},
	}).Variants[0].ID
	backordered := seedProductInput(t, fx.svc, service.CreateProductInput{
		Handle: "backordered", Title: "Backordered", Status: models.StatusPublished,
		Variants: []service.CreateVariantInput{{Title: "Backordered", AllowBackorder: ptr(true)}},
	}).Variants[0].ID
	stocked := seedProduct(t, fx.svc, "stocked", "Stocked").Variants[0].ID
	require.NoError(t, fx.svc.SetVariantInventoryItem(ctx, stocked, "iitem_1"))

	for name, c := range map[string]struct {
		bundle    string
		component string
	}{
		"a bundle as component":  {crate, box},
		"a component as bundle":  {soap, crate},
		"a bundle not counted":   {loose, crate},
		"a bundle sold past 0":   {backordered, crate},
		"a bundle with its item": {stocked, crate},
	} {
		_, err := fx.svc.SetVariantBundle(ctx, c.bundle,
			[]models.BundleComponent{{VariantID: c.component, Quantity: 1}})
		require.Error(t, err, name)
		assert.True(t, errors.IsConflict(err), "%s: %v", name, err)
		assert.Equal(t, service.CodeBundleShape, errors.CodeOf(err), name)
		read, err := fx.svc.VariantBundle(ctx, c.bundle)
		require.NoError(t, err)
		assert.Empty(t, read, "%s: nothing is written", name)
	}

	// Clearing a composition is always allowed: it is how an operator gets out.
	_, err = fx.svc.SetVariantBundle(ctx, stocked, nil)
	require.NoError(t, err)
}

// TestABundleStaysCountedAndUnlinked holds the other side of the state
// refusals: a bundle variant is not updated into one that is not counted or is
// sold past zero, and takes no stock item, while a plain variant is free to.
func TestABundleStaysCountedAndUnlinked(t *testing.T) {
	fx := newBundleFixture(t)
	ctx := context.Background()
	box := fx.box.Variants[0].ID
	_, err := fx.svc.SetVariantBundle(ctx, box, fx.components())
	require.NoError(t, err)

	for name, in := range map[string]service.UpdateVariantInput{
		"not counted":    {ManageInventory: ptr(false)},
		"sold past zero": {AllowBackorder: ptr(true)},
	} {
		_, err := fx.svc.UpdateVariant(ctx, box, in)
		require.Error(t, err, name)
		assert.Equal(t, service.CodeBundleShape, errors.CodeOf(err), name)
		assert.True(t, errors.IsConflict(err), name)
	}
	variant, err := fx.svc.GetVariant(ctx, box)
	require.NoError(t, err)
	assert.True(t, variant.ManageInventory, "a refused update writes nothing")
	assert.False(t, variant.AllowBackorder)

	err = fx.svc.SetVariantInventoryItem(ctx, box, "iitem_1")
	require.Error(t, err)
	assert.Equal(t, service.CodeBundleShape, errors.CodeOf(err))

	towel := fx.towel.Variants[0].ID
	_, err = fx.svc.UpdateVariant(ctx, towel, service.UpdateVariantInput{AllowBackorder: ptr(true)})
	require.NoError(t, err, "a component is a plain variant")
	require.NoError(t, fx.svc.SetVariantInventoryItem(ctx, towel, "iitem_2"))
	_, err = fx.svc.UpdateVariant(ctx, box, service.UpdateVariantInput{Title: ptr("Gift box, large")})
	require.NoError(t, err, "an update that keeps the bundle counted passes")
}

// TestABundlesComponentIsNotDeleted holds ADR 0234's deletion rule: a variant
// a live bundle holds, or a product one of whose variants it holds, is refused
// its deletion; once the bundle is cleared or deleted, the deletion passes. A
// bundle's deletion takes its composition with it.
func TestABundlesComponentIsNotDeleted(t *testing.T) {
	fx := newBundleFixture(t)
	ctx := context.Background()
	box := fx.box.Variants[0].ID
	soap := fx.soap.Variants[0].ID
	_, err := fx.svc.SetVariantBundle(ctx, box, fx.components())
	require.NoError(t, err)

	err = fx.svc.DeleteVariant(ctx, soap)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "%v", err)
	assert.Contains(t, err.Error(), box, "the refusal names the bundle")
	err = fx.svc.DeleteProduct(ctx, fx.towel.ID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "%v", err)
	_, err = fx.svc.GetVariant(ctx, soap)
	require.NoError(t, err, "a refused deletion deletes nothing")
	_, err = fx.svc.GetProduct(ctx, fx.towel.ID)
	require.NoError(t, err)

	_, err = fx.svc.SetVariantBundle(ctx, box, fx.components()[:1])
	require.NoError(t, err)
	require.NoError(t, fx.svc.DeleteVariant(ctx, soap), "a variant no bundle holds is deleted")

	require.NoError(t, fx.svc.DeleteVariant(ctx, box), "a bundle is deleted with its composition")
	assert.Empty(t, fx.store.bundles, "the composition went with the bundle")
	require.NoError(t, fx.svc.DeleteProduct(ctx, fx.towel.ID), "a deleted bundle holds nothing")
}

// TestABundlesProductTakesItsCompositions holds that deleting the bundle's
// product takes its variants' compositions.
func TestABundlesProductTakesItsCompositions(t *testing.T) {
	fx := newBundleFixture(t)
	ctx := context.Background()
	_, err := fx.svc.SetVariantBundle(ctx, fx.box.Variants[0].ID, fx.components())
	require.NoError(t, err)

	require.NoError(t, fx.svc.DeleteProduct(ctx, fx.box.ID))
	assert.Empty(t, fx.store.bundles)
	require.NoError(t, fx.svc.DeleteVariant(ctx, fx.soap.Variants[0].ID))
}
