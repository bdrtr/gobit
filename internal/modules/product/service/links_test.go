package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestDefinitionsAreTheCrossModuleContract verifies that the link definitions
// match the contract exactly.
//
// This is a "do not change" test and it is deliberate: the names, the ends and
// the cardinality are the only contract the pricing and inventory modules rely
// on. That the module field of an end is "variant" is part of the contract too —
// when Query resolves a link it reads that field as the ENTITY NAME and looks
// the provider up with "<name>.query"; had "product" been written, variant ids
// would be asked of the product provider and the expansion would silently come
// back empty.
func TestDefinitionsAreTheCrossModuleContract(t *testing.T) {
	t.Parallel()

	defs := service.Definitions()
	require.Len(t, defs, 3)

	byName := map[string]link.LinkDefinition{}
	for _, def := range defs {
		byName[def.Name] = def
		require.NoError(t, def.Validate(), "the %q definition should pass the core's validation", def.Name)
	}

	priceSet, ok := byName["product_variant_price_set"]
	require.True(t, ok, "the price link should be declared under this name")
	assert.Equal(t, link.LinkSide{Module: "product", Entity: "variant", Field: "variant_id"}, priceSet.From)
	assert.Equal(t, link.LinkSide{Module: "pricing", Entity: "price_set", Field: "price_set_id"}, priceSet.To)
	assert.Equal(t, link.OneToOne, priceSet.Cardinality)

	inventory, ok := byName["product_variant_inventory"]
	require.True(t, ok, "the stock link should be declared under this name")
	assert.Equal(t, link.LinkSide{Module: "product", Entity: "variant", Field: "variant_id"}, inventory.From)
	assert.Equal(t, link.LinkSide{Module: "inventory", Entity: "inventory_item", Field: "inventory_item_id"}, inventory.To)
	assert.Equal(t, link.OneToOne, inventory.Cardinality)

	salesChannel, ok := byName["product_sales_channel"]
	require.True(t, ok, "the sales channel link should be declared under this name")
	assert.Equal(t, link.LinkSide{Module: "product", Entity: "product", Field: "product_id"}, salesChannel.From,
		"the link is at the PRODUCT level, not the VARIANT level")
	assert.Equal(t, link.LinkSide{Module: "sales_channel", Entity: "sales_channel", Field: "sales_channel_id"},
		salesChannel.To,
		"the To end should carry auth's ENTITY name, not its MODULE name; the provider is registered under that name")
	assert.Equal(t, link.ManyToMany, salesChannel.Cardinality,
		"a product can be in many channels and a channel can hold many products")
}

// TestSetVariantPriceSetReplacesExisting verifies that the old link is removed
// while the price set is being changed.
//
// Had it not been removed, the link service would return a conflict because of
// the OneToOne cardinality and a "change the price" request would look like an
// error.
func TestSetVariantPriceSetReplacesExisting(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	svc := newService(t, newMemStore(), links, nil)
	ctx := context.Background()

	product := seedProduct(t, svc, "shirt", "Shirt")
	variantID := product.Variants[0].ID

	require.NoError(t, svc.SetVariantPriceSet(ctx, variantID, "pset_1"))
	require.NoError(t, svc.SetVariantPriceSet(ctx, variantID, "pset_2"))

	assert.Equal(t, []string{"pset_2"}, links.linked(service.LinkVariantPriceSet, variantID),
		"the variant should stay linked to a single price set")
	assert.Contains(t, links.deletes, service.LinkVariantPriceSet+"|"+variantID+"|pset_1",
		"the old link should be removed explicitly")
}

// TestSetVariantPriceSetIsIdempotent verifies that creating the same link a
// second time produces no unnecessary delete.
func TestSetVariantPriceSetIsIdempotent(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	svc := newService(t, newMemStore(), links, nil)
	ctx := context.Background()

	product := seedProduct(t, svc, "shirt", "Shirt")
	variantID := product.Variants[0].ID

	require.NoError(t, svc.SetVariantPriceSet(ctx, variantID, "pset_1"))
	require.NoError(t, svc.SetVariantPriceSet(ctx, variantID, "pset_1"))

	assert.Equal(t, []string{"pset_1"}, links.linked(service.LinkVariantPriceSet, variantID))
	assert.Empty(t, links.deletes, "relinking to the same target should not remove the old link")
}

// TestSetVariantPriceSetKeepsExistingLinkWhenTargetIsTaken verifies that a
// relink that falls over with a conflict does not break the variant's EXISTING
// link.
//
// The TO end of a OneToOne is unique too: when a price set that is linked to
// another variant is requested, Create returns a conflict. If the old link has
// already been deleted at that point, the request returns 409 (read as "nothing
// changed") but the variant is left without a price and the storefront publishes
// it that way — silent data loss. That is why a failing request has to restore
// the old link.
func TestSetVariantPriceSetKeepsExistingLinkWhenTargetIsTaken(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	svc := newService(t, newMemStore(), links, nil)
	ctx := context.Background()

	product := seedProduct(t, svc, "shirt", "Shirt")
	first := product.Variants[0].ID
	second, err := svc.CreateVariant(ctx, product.ID, service.CreateVariantInput{Title: "Second"})
	require.NoError(t, err)

	require.NoError(t, svc.SetVariantPriceSet(ctx, first, "pset_1"))
	require.NoError(t, svc.SetVariantPriceSet(ctx, second.ID, "pset_2"))

	// pset_1 is already linked to the "first" variant; "second" cannot claim it.
	err = svc.SetVariantPriceSet(ctx, second.ID, "pset_1")
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "a conflict was expected: %v", err)

	assert.Equal(t, []string{"pset_2"}, links.linked(service.LinkVariantPriceSet, second.ID),
		"a failing request should not break the variant's existing price link")
	assert.Equal(t, []string{"pset_1"}, links.linked(service.LinkVariantPriceSet, first),
		"the actual owner of the target should not be affected either")

	// The link has to stay readable: the storefront should not publish this
	// variant without a price.
	linkIDs, err := svc.VariantLinkIDs(ctx, second.ID)
	require.NoError(t, err)
	require.NotNil(t, linkIDs.PriceSetID)
	assert.Equal(t, "pset_2", *linkIDs.PriceSetID)
}

// TestSetVariantInventoryItemKeepsExistingLinkWhenTargetIsTaken verifies that
// the same compensation works on the stock link as well; the two endpoints share
// the same helper but the contract binds both of them.
func TestSetVariantInventoryItemKeepsExistingLinkWhenTargetIsTaken(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	svc := newService(t, newMemStore(), links, nil)
	ctx := context.Background()

	product := seedProduct(t, svc, "shirt", "Shirt")
	first := product.Variants[0].ID
	second, err := svc.CreateVariant(ctx, product.ID, service.CreateVariantInput{Title: "Second"})
	require.NoError(t, err)

	require.NoError(t, svc.SetVariantInventoryItem(ctx, first, "invitem_1"))
	require.NoError(t, svc.SetVariantInventoryItem(ctx, second.ID, "invitem_2"))

	err = svc.SetVariantInventoryItem(ctx, second.ID, "invitem_1")
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "a conflict was expected: %v", err)

	assert.Equal(t, []string{"invitem_2"}, links.linked(service.LinkVariantInventory, second.ID),
		"a failing request should not break the variant's existing stock link")
}

// TestSetVariantLinkRequiresExistingVariant verifies that no link can be created
// to a variant that does not exist.
//
// The link service sees the ids as free-form strings and knows no module's
// schema; had there been no validation, an id carrying a typo would be linked
// silently and the link could never be resolved.
func TestSetVariantLinkRequiresExistingVariant(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	svc := newService(t, newMemStore(), links, nil)

	err := svc.SetVariantPriceSet(context.Background(), "variant_missing", "pset_1")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a not found was expected: %v", err)
	assert.Empty(t, links.linked(service.LinkVariantPriceSet, "variant_missing"),
		"no link should be created while the validation fails")
}

// TestSetVariantLinkValidatesTargetID verifies that the target id cannot be
// empty or carry whitespace.
func TestSetVariantLinkValidatesTargetID(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()
	product := seedProduct(t, svc, "shirt", "Shirt")

	err := svc.SetVariantPriceSet(ctx, product.Variants[0].ID, "")
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "an empty id should be rejected: %v", err)

	err = svc.SetVariantInventoryItem(ctx, product.Variants[0].ID, "invitem_1\n")
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "an id carrying whitespace should be rejected: %v", err)
}

// TestVariantLinkIDsReportsBothSides verifies that both links of the variant can
// be read.
func TestVariantLinkIDsReportsBothSides(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()
	product := seedProduct(t, svc, "shirt", "Shirt")
	variantID := product.Variants[0].ID

	linkIDs, err := svc.VariantLinkIDs(ctx, variantID)
	require.NoError(t, err)
	assert.Nil(t, linkIDs.PriceSetID)
	assert.Nil(t, linkIDs.InventoryItemID)

	require.NoError(t, svc.SetVariantPriceSet(ctx, variantID, "pset_1"))
	require.NoError(t, svc.SetVariantInventoryItem(ctx, variantID, "invitem_1"))

	linkIDs, err = svc.VariantLinkIDs(ctx, variantID)
	require.NoError(t, err)
	require.NotNil(t, linkIDs.PriceSetID)
	require.NotNil(t, linkIDs.InventoryItemID)
	assert.Equal(t, "pset_1", *linkIDs.PriceSetID)
	assert.Equal(t, "invitem_1", *linkIDs.InventoryItemID)
}

// TestClearVariantLinks verifies that a link can be removed.
func TestClearVariantLinks(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	svc := newService(t, newMemStore(), links, nil)
	ctx := context.Background()
	product := seedProduct(t, svc, "shirt", "Shirt")
	variantID := product.Variants[0].ID

	require.NoError(t, svc.SetVariantInventoryItem(ctx, variantID, "invitem_1"))
	require.NoError(t, svc.ClearVariantInventoryItem(ctx, variantID))

	assert.Empty(t, links.linked(service.LinkVariantInventory, variantID))
}

// TestLinkOperationsWithoutLinker verifies that on a service built without a
// link service the link endpoints return a typed "not ready" error.
func TestLinkOperationsWithoutLinker(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), nil, nil)
	ctx := context.Background()

	err := svc.SetVariantPriceSet(ctx, "variant_1", "pset_1")
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "an unavailable was expected: %v", err)

	_, err = svc.VariantLinkIDs(ctx, "variant_1")
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "an unavailable was expected: %v", err)
}

// TestSetVariantLinkPreservesConflictKind verifies that the kind of a
// cardinality conflict coming from the link service is preserved.
//
// Had the kind not been preserved, the client would see a fixable conflict as a
// 500.
func TestSetVariantLinkPreservesConflictKind(t *testing.T) {
	t.Parallel()

	links := newFakeLinker()
	links.createErr = errors.Conflict("link_cardinality_violation", "the end is already linked")
	svc := newService(t, newMemStore(), links, nil)
	ctx := context.Background()
	product := seedProduct(t, svc, "shirt", "Shirt")

	err := svc.SetVariantPriceSet(ctx, product.Variants[0].ID, "pset_1")
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "the conflict kind should be preserved: %v", err)
	assert.Equal(t, "product_link_failed", errors.CodeOf(err))
}
