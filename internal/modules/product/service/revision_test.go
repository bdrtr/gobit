package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// revisionsNewestFirst reads every revision of a product.
func revisionsNewestFirst(t *testing.T, svc *service.Service, productID string) []models.Revision {
	t.Helper()
	page, err := svc.ListRevisions(context.Background(), productID, service.MaxLimit, 0)
	require.NoError(t, err)
	return page.Items
}

// snapshotOf decodes one revision's snapshot.
func snapshotOf(t *testing.T, svc *service.Service, productID string, version int64) map[string]any {
	t.Helper()
	rev, err := svc.GetRevision(context.Background(), productID, version)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(rev.Snapshot, &out))
	return out
}

// TestAWriteRecordsARevisionOfTheView is ADR 0221: a product's creation is its
// first revision, each write that changes its admin view the next, naming the
// fields it changed, and a write that changes nothing records nothing.
func TestAWriteRecordsARevisionOfTheView(t *testing.T) {
	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), &fakeGraph{})
	ctx := context.Background()
	product := seedProduct(t, svc, "linen-shirt", "Linen shirt")

	stored, err := svc.GetProduct(ctx, product.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), stored.Version)

	_, err = svc.UpdateProduct(ctx, product.ID, service.UpdateProductInput{Title: ptr("Linen shirt, white")})
	require.NoError(t, err)
	_, err = svc.UpdateProduct(ctx, product.ID, service.UpdateProductInput{Title: ptr("Linen shirt, white")})
	require.NoError(t, err)

	revisions := revisionsNewestFirst(t, svc, product.ID)
	require.Len(t, revisions, 2, "the second write changed nothing")
	assert.Equal(t, int64(2), revisions[0].Version)
	assert.Equal(t, []string{"title"}, revisions[0].Changed, "the timestamps a write stamps are not a change")
	assert.Equal(t, []string{}, revisions[1].Changed)
	assert.Nil(t, revisions[0].Snapshot, "a listing leaves the snapshot out")
	assert.Equal(t, "Linen shirt", snapshotOf(t, svc, product.ID, 1)["title"])
	assert.NotContains(t, snapshotOf(t, svc, product.ID, 2), "updated_at")

	stored, err = svc.GetProduct(ctx, product.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), stored.Version)
}

// TestAProductWrittenBeforeRevisionsBeginsWithWhatItWas: a product with no
// revision gets the view before its first write as its first revision.
func TestAProductWrittenBeforeRevisionsBeginsWithWhatItWas(t *testing.T) {
	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), &fakeGraph{})
	ctx := context.Background()
	product := seedProduct(t, svc, "old-mug", "Old mug")
	store.revisions = nil
	stored := store.products[product.ID]
	stored.Version = 0
	store.products[product.ID] = stored

	_, err := svc.UpdateProduct(ctx, product.ID, service.UpdateProductInput{Title: ptr("Mug")})
	require.NoError(t, err)

	revisions := revisionsNewestFirst(t, svc, product.ID)
	require.Len(t, revisions, 2)
	assert.Equal(t, "Old mug", snapshotOf(t, svc, product.ID, 1)["title"], "the history begins with what it was")
	assert.Equal(t, "Mug", snapshotOf(t, svc, product.ID, 2)["title"])
	assert.Equal(t, []string{"title"}, revisions[0].Changed)
}

// TestEveryWriteToAProductsContentIsARevision: its variants, options, images
// and attribute values are its view, and each write to them is revised.
func TestEveryWriteToAProductsContentIsARevision(t *testing.T) {
	fx := newAttributeFixture(t)
	svc, ctx := fx.svc, context.Background()
	id := fx.basket.ID
	version := func() int64 {
		t.Helper()
		p, err := svc.GetProduct(ctx, id)
		require.NoError(t, err)
		return p.Version
	}
	last := func() models.Revision {
		t.Helper()
		return revisionsNewestFirst(t, svc, id)[0]
	}

	option, err := svc.CreateOption(ctx, id, service.CreateOptionInput{Title: "Size", Values: []string{"S"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"options"}, last().Changed, "an option")
	value, err := svc.AddOptionValue(ctx, option.ID, "M")
	require.NoError(t, err)
	assert.Equal(t, []string{"options"}, last().Changed, "an option value")
	variant, err := svc.CreateVariant(ctx, id, service.CreateVariantInput{Title: "Large"})
	require.NoError(t, err)
	assert.Equal(t, []string{"variants"}, last().Changed, "a variant")
	_, err = svc.UpdateVariant(ctx, variant.ID, service.UpdateVariantInput{Title: ptr("Larger")})
	require.NoError(t, err)
	require.NoError(t, svc.SetVariantOptionValues(ctx, variant.ID, []string{value.ID}))
	require.NoError(t, svc.DeleteVariant(ctx, variant.ID))
	require.NoError(t, svc.DeleteOptionValue(ctx, value.ID))
	require.NoError(t, svc.DeleteOption(ctx, option.ID))
	image, err := svc.AddProductImage(ctx, id, service.CreateImageInput{URL: "https://cdn.example.com/basket.jpg"})
	require.NoError(t, err)
	assert.Equal(t, []string{"images"}, last().Changed, "an image")
	_, err = svc.UpdateProductImage(ctx, id, image.ID, service.UpdateImageInput{AltText: ptr("A basket")})
	require.NoError(t, err)
	require.NoError(t, svc.RemoveProductImage(ctx, id, image.ID))
	_, err = svc.SetProductAttributes(ctx, id, []service.ProductAttributeInput{{Attribute: "width", Number: ptr(31.0)}})
	require.NoError(t, err)
	assert.Equal(t, []string{"attributes"}, last().Changed)

	assert.Equal(t, int64(1+1+12), version(), "creation, its attribute values, and twelve writes")
}

// TestARevisionNamesTheRequestThatMadeIt: the request id the admin audit log
// records its caller under is kept; a job's revision has none.
func TestARevisionNamesTheRequestThatMadeIt(t *testing.T) {
	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), &fakeGraph{})
	product := seedProduct(t, svc, "tote", "Tote")

	ctx := corehttp.WithRequestID(context.Background(), "req_42")
	_, err := svc.UpdateProduct(ctx, product.ID, service.UpdateProductInput{Title: ptr("Tote bag")})
	require.NoError(t, err)

	revisions := revisionsNewestFirst(t, svc, product.ID)
	require.NotNil(t, revisions[0].RequestID)
	assert.Equal(t, "req_42", *revisions[0].RequestID)
	assert.Nil(t, revisions[1].RequestID)
}

// TestARestoreWritesBackTheContentAsANewRevision: the revision's own fields,
// a field it did not have cleared, its collection, type, tags, categories and
// attribute values come back, as a revision of their own; the status and the
// variants stay as they are.
func TestARestoreWritesBackTheContentAsANewRevision(t *testing.T) {
	fx := newAttributeFixture(t)
	svc, ctx := fx.svc, context.Background()
	collection, err := svc.CreateCollection(ctx, service.CreateCollectionInput{Title: "Summer"})
	require.NoError(t, err)
	kind, err := svc.CreateProductType(ctx, service.CreateProductTypeInput{Value: "Shirts"})
	require.NoError(t, err)
	tag, err := svc.CreateTag(ctx, "linen")
	require.NoError(t, err)
	other, err := svc.CreateTag(ctx, "sale")
	require.NoError(t, err)
	category, err := svc.CreateCategory(ctx, service.CreateCategoryInput{Name: "Tops"})
	require.NoError(t, err)
	id := fx.shirt.ID
	_, err = svc.UpdateProduct(ctx, id, service.UpdateProductInput{
		Title: ptr("Shirt"), CollectionID: &collection.ID, TypeID: &kind.ID, Material: ptr("linen"),
		Metadata: map[string]any{"fit": "loose"}, TagIDs: []string{tag.ID}, CategoryIDs: []string{category.ID},
	})
	require.NoError(t, err)
	before, err := svc.GetProduct(ctx, id)
	require.NoError(t, err)

	_, err = svc.UpdateProduct(ctx, id, service.UpdateProductInput{
		Title: ptr("Shirt, on sale"), Subtitle: ptr("Half price"), Material: ptr("cotton"),
		Status: ptr(models.StatusArchived), Metadata: map[string]any{"fit": "slim"}, TagIDs: []string{other.ID},
		CategoryIDs: []string{},
	})
	require.NoError(t, err)
	_, err = svc.SetProductAttributes(ctx, id, []service.ProductAttributeInput{{Attribute: "material", Options: []string{"wool"}}})
	require.NoError(t, err)
	_, err = svc.CreateVariant(ctx, id, service.CreateVariantInput{Title: "Second"})
	require.NoError(t, err)

	result, err := svc.RestoreRevision(ctx, id, before.Version)
	require.NoError(t, err)

	restored := result.Product
	assert.Empty(t, result.Dropped)
	assert.Equal(t, "Shirt", restored.Title)
	assert.Nil(t, restored.Subtitle, "a field the revision did not have is cleared")
	assert.Equal(t, "linen", *restored.Material)
	assert.Equal(t, map[string]any{"fit": "loose"}, restored.Metadata)
	assert.Equal(t, collection.ID, *restored.CollectionID)
	assert.Equal(t, kind.ID, *restored.TypeID)
	require.Len(t, restored.Tags, 1)
	assert.Equal(t, tag.ID, restored.Tags[0].ID)
	require.Len(t, restored.Categories, 1)
	assert.Equal(t, category.ID, restored.Categories[0].ID)
	assert.Equal(t, before.Attributes, restored.Attributes)
	assert.Equal(t, models.StatusArchived, restored.Status, "a status has its own writes")
	assert.Len(t, restored.Variants, 2, "variants are held by other modules by id")
	assert.Equal(t, before.Version+4, restored.Version, "the restore is a revision of its own")
	last := revisionsNewestFirst(t, svc, id)[0]
	assert.Equal(t, []string{"attributes", "categories", "material", "metadata", "subtitle", "tags", "title"}, last.Changed)
}

// TestARestoreLeavesOutWhatWasRemovedSince: a removed collection, type, tag,
// category, attribute and option are left out and named.
func TestARestoreLeavesOutWhatWasRemovedSince(t *testing.T) {
	fx := newAttributeFixture(t)
	svc, ctx := fx.svc, context.Background()
	collection, err := svc.CreateCollection(ctx, service.CreateCollectionInput{Title: "Winter"})
	require.NoError(t, err)
	kind, err := svc.CreateProductType(ctx, service.CreateProductTypeInput{Value: "Coats"})
	require.NoError(t, err)
	gone, err := svc.CreateTag(ctx, "gone")
	require.NoError(t, err)
	kept, err := svc.CreateTag(ctx, "kept")
	require.NoError(t, err)
	category, err := svc.CreateCategory(ctx, service.CreateCategoryInput{Name: "Outerwear"})
	require.NoError(t, err)
	id := fx.jacket.ID
	_, err = svc.UpdateProduct(ctx, id, service.UpdateProductInput{
		CollectionID: &collection.ID, TypeID: &kind.ID, TagIDs: []string{gone.ID, kept.ID}, CategoryIDs: []string{category.ID},
	})
	require.NoError(t, err)
	before, err := svc.GetProduct(ctx, id)
	require.NoError(t, err)
	wool := optionNamed(t, before.Attributes, "material", "wool")
	waterproof := attributeNamed(t, before.Attributes, "waterproof")

	require.NoError(t, svc.DeleteCollection(ctx, collection.ID))
	require.NoError(t, svc.DeleteProductType(ctx, kind.ID))
	require.NoError(t, svc.DeleteTag(ctx, gone.ID))
	require.NoError(t, svc.DeleteCategory(ctx, category.ID))
	require.NoError(t, svc.DeleteAttributeOption(ctx, wool))
	require.NoError(t, svc.DeleteAttribute(ctx, waterproof))
	_, err = svc.UpdateProduct(ctx, id, service.UpdateProductInput{Title: ptr("Jacket II"), TagIDs: []string{}})
	require.NoError(t, err)

	result, err := svc.RestoreRevision(ctx, id, before.Version)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"collection:" + collection.ID, "type:" + kind.ID, "tag:" + gone.ID, "category:" + category.ID,
		"option:" + wool, "attribute:" + waterproof,
	}, result.Dropped)
	assert.Nil(t, result.Product.CollectionID)
	assert.Nil(t, result.Product.TypeID)
	require.Len(t, result.Product.Tags, 1)
	assert.Equal(t, kept.ID, result.Product.Tags[0].ID)
	assert.Equal(t, "Jacket", result.Product.Title)
	var handles []string
	for _, value := range result.Product.Attributes {
		handles = append(handles, value.Handle)
		if value.Handle == "material" {
			require.Len(t, value.Options, 1)
			assert.Equal(t, "cotton", value.Options[0].Handle, "the option still standing is kept")
		}
	}
	assert.Equal(t, []string{"material", "width"}, handles)
}

// TestARestoreRefusesWhatItCannotWrite: a handle another product took since,
// an unknown version and a version below one are refused, and nothing is
// written.
func TestARestoreRefusesWhatItCannotWrite(t *testing.T) {
	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), &fakeGraph{})
	ctx := context.Background()
	product := seedProduct(t, svc, "scarf", "Scarf")
	_, err := svc.UpdateProduct(ctx, product.ID, service.UpdateProductInput{Handle: ptr("silk-scarf")})
	require.NoError(t, err)
	seedProduct(t, svc, "scarf", "Another scarf")

	_, err = svc.RestoreRevision(ctx, product.ID, 1)
	assert.True(t, errors.IsConflict(err), "the handle is taken: %v", err)
	_, err = svc.RestoreRevision(ctx, product.ID, 9)
	assert.True(t, errors.IsNotFound(err))
	_, err = svc.RestoreRevision(ctx, product.ID, 0)
	assert.True(t, errors.IsInvalid(err))
	assert.Len(t, revisionsNewestFirst(t, svc, product.ID), 2)
}

// TestRevisionsArePagedNewestFirst: the listing's page and count.
func TestRevisionsArePagedNewestFirst(t *testing.T) {
	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), &fakeGraph{})
	ctx := context.Background()
	product := seedProduct(t, svc, "cap", "Cap")
	for _, title := range []string{"Cap 2", "Cap 3", "Cap 4"} {
		_, err := svc.UpdateProduct(ctx, product.ID, service.UpdateProductInput{Title: ptr(title)})
		require.NoError(t, err)
	}

	page, err := svc.ListRevisions(ctx, product.ID, 2, 1)

	require.NoError(t, err)
	assert.Equal(t, 4, requireCount(t, page))
	require.Len(t, page.Items, 2)
	assert.Equal(t, []int64{3, 2}, []int64{page.Items[0].Version, page.Items[1].Version})
	_, err = svc.ListRevisions(ctx, "prod_missing", 2, 0)
	assert.True(t, errors.IsNotFound(err))
}

// optionNamed returns the id of an option a product's attribute value holds.
func optionNamed(t *testing.T, values []models.ProductAttributeValue, attribute, option string) string {
	t.Helper()
	for _, value := range values {
		if value.Handle != attribute {
			continue
		}
		for _, o := range value.Options {
			if o.Handle == option {
				return o.ID
			}
		}
	}
	t.Fatalf("no option %s of %s", option, attribute)
	return ""
}

// attributeNamed returns the id of the attribute a product's value names.
func attributeNamed(t *testing.T, values []models.ProductAttributeValue, attribute string) string {
	t.Helper()
	for _, value := range values {
		if value.Handle == attribute {
			return value.AttributeID
		}
	}
	t.Fatalf("no attribute %s", attribute)
	return ""
}

// TestARestoreIsAnnouncedAsAnEdit: the search index and the webhooks learn of a
// restore as they learn of an edit.
func TestARestoreIsAnnouncedAsAnEdit(t *testing.T) {
	store, bus := newMemStore(), newFakeBus()
	svc := newServiceWithBus(t, store, newFakeLinker(), &fakeGraph{}, bus)
	ctx := context.Background()
	product := seedProduct(t, svc, "belt", "Belt")
	_, err := svc.UpdateProduct(ctx, product.ID, service.UpdateProductInput{Title: ptr("Leather belt")})
	require.NoError(t, err)
	before := len(bus.byName(service.EventProductUpdated))

	_, err = svc.RestoreRevision(ctx, product.ID, 1)

	require.NoError(t, err)
	assert.Len(t, bus.byName(service.EventProductUpdated), before+1)
}
