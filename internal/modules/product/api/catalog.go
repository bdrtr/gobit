package api

import (
	"context"
	"io"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// Catalog is the surface the api layer needs from the service.
//
// The reason an interface is used instead of the concrete service is testing:
// verifying the envelope shape, the parameter parsing and the error mapping of
// the handlers must not require a database. The interface stands next to its
// consumer (api); ADR 0001's pattern works INSIDE a module for the same reason.
type Catalog interface {
	CreateProduct(ctx context.Context, in service.CreateProductInput) (models.Product, error)
	GetProduct(ctx context.Context, id string) (models.Product, error)
	ListProducts(ctx context.Context, opts service.ListProductsOptions) (service.ListResult[models.Product], error)
	UpdateProduct(ctx context.Context, id string, in service.UpdateProductInput) (models.Product, error)
	DeleteProduct(ctx context.Context, id string) error
	// CreateImport and GetImport are a catalog import (ADR 0205).
	CreateImport(ctx context.Context, file []byte) (models.Import, error)
	GetImport(ctx context.Context, id string) (models.Import, error)
	// ExportProducts writes the catalog as CSV (ADR 0204).
	ExportProducts(ctx context.Context, out io.Writer, opts service.ExportOptions, afterPage func() error) error
	// ProductRelations, SetProductRelations and StoreRelatedProducts are a
	// product's relations (ADR 0180).
	ProductRelations(ctx context.Context, id string) (map[models.RelationType][]string, error)
	SetProductRelations(
		ctx context.Context, id string, kind models.RelationType, relatedIDs []string,
	) (map[models.RelationType][]string, error)
	StoreRelatedProducts(
		ctx context.Context, idOrHandle string, kind models.RelationType, salesChannelIDs []string,
	) ([]service.StoreProduct, error)
	// ProductAddOns, SetProductAddOns and StoreProductAddOns are the variants
	// a product's lines may carry as add-ons (ADR 0228).
	ProductAddOns(ctx context.Context, id string) ([]string, error)
	SetProductAddOns(ctx context.Context, id string, variantIDs []string) ([]string, error)
	StoreProductAddOns(ctx context.Context, idOrHandle string, salesChannelIDs []string) ([]service.StoreAddOn, error)
	// VariantBundle and SetVariantBundle are what a bundle variant is made of
	// (ADR 0234).
	VariantBundle(ctx context.Context, variantID string) ([]models.BundleComponent, error)
	SetVariantBundle(
		ctx context.Context, variantID string, components []models.BundleComponent,
	) ([]models.BundleComponent, error)
	// VariantCosts and SetVariantCosts are what one unit of a variant costs
	// the shop, per currency (ADR 0401).
	VariantCosts(ctx context.Context, variantID string) ([]models.VariantCost, error)
	SetVariantCosts(ctx context.Context, variantID string, costs []models.VariantCost) ([]models.VariantCost, error)
	// SetSchedule and ClearSchedule replace and take off a product's schedule
	// (ADR 0177, ADR 0179).
	SetSchedule(ctx context.Context, id string, schedule service.Schedule) (models.Product, error)
	ClearSchedule(ctx context.Context, id string) (models.Product, error)
	// ListRevisions, GetRevision and RestoreRevision are a product's revisions
	// (ADR 0221).
	ListRevisions(ctx context.Context, productID string, limit, offset int) (service.ListResult[models.Revision], error)
	GetRevision(ctx context.Context, productID string, version int64) (models.Revision, error)
	RestoreRevision(ctx context.Context, productID string, version int64) (service.RestoreResult, error)
	// The image writes: one image at a time, each addressed by BOTH the
	// product's id and the image's (ADR 0108).
	AddProductImage(ctx context.Context, productID string, in service.CreateImageInput) (models.Image, error)
	UpdateProductImage(ctx context.Context, productID, imageID string, in service.UpdateImageInput) (models.Image, error)
	RemoveProductImage(ctx context.Context, productID, imageID string) error

	CreateVariant(ctx context.Context, productID string, in service.CreateVariantInput) (models.Variant, error)
	GetVariant(ctx context.Context, id string) (models.Variant, error)
	ListVariants(ctx context.Context, opts service.ListVariantsOptions) (service.ListResult[models.Variant], error)
	UpdateVariant(ctx context.Context, id string, in service.UpdateVariantInput) (models.Variant, error)
	DeleteVariant(ctx context.Context, id string) error

	CreateOption(ctx context.Context, productID string, in service.CreateOptionInput) (models.Option, error)
	ListOptions(ctx context.Context, productID string) ([]models.Option, error)
	AddOptionValue(ctx context.Context, optionID, value string) (models.OptionValue, error)
	DeleteOption(ctx context.Context, id string) error
	DeleteOptionValue(ctx context.Context, id string) error

	SetVariantPriceSet(ctx context.Context, variantID, priceSetID string) error
	ClearVariantPriceSet(ctx context.Context, variantID string) error
	SetVariantInventoryItem(ctx context.Context, variantID, itemID string) error
	ClearVariantInventoryItem(ctx context.Context, variantID string) error
	VariantLinkIDs(ctx context.Context, variantID string) (service.VariantLinks, error)

	AddProductSalesChannel(ctx context.Context, productID, salesChannelID string) error
	RemoveProductSalesChannel(ctx context.Context, productID, salesChannelID string) error
	ProductSalesChannelIDs(ctx context.Context, productID string) ([]string, error)

	// ImagesOfUpload is the REVERSE read of the image/upload binding; there is
	// no write counterpart, because an image is bound when it is created.
	ImagesOfUpload(ctx context.Context, uploadID string) ([]models.Image, error)

	CreateCollection(ctx context.Context, in service.CreateCollectionInput) (models.Collection, error)
	GetCollection(ctx context.Context, id string) (models.Collection, error)
	ListCollections(ctx context.Context, limit, offset int) (service.ListResult[models.Collection], error)
	DeleteCollection(ctx context.Context, id string) error

	CreateProductType(ctx context.Context, in service.CreateProductTypeInput) (models.ProductType, error)
	GetProductType(ctx context.Context, id string) (models.ProductType, error)
	ListProductTypes(ctx context.Context, limit, offset int) (service.ListResult[models.ProductType], error)
	DeleteProductType(ctx context.Context, id string) error

	CreateCategory(ctx context.Context, in service.CreateCategoryInput) (models.Category, error)
	UpdateCategory(ctx context.Context, id string, in service.UpdateCategoryInput) (models.Category, error)
	GetCategory(ctx context.Context, id string) (models.Category, error)
	ListCategories(ctx context.Context, opts service.ListCategoriesOptions) (service.ListResult[models.Category], error)
	DeleteCategory(ctx context.Context, id string) error

	CreateTag(ctx context.Context, value string) (models.Tag, error)
	ListTags(ctx context.Context, limit, offset int) (service.ListResult[models.Tag], error)

	// The typed attributes and their facets (ADR 0219).
	CreateAttribute(ctx context.Context, in service.AttributeInput) (models.Attribute, error)
	ListAttributes(ctx context.Context) ([]models.Attribute, error)
	UpdateAttribute(ctx context.Context, id string, patch service.AttributePatch) (models.Attribute, error)
	DeleteAttribute(ctx context.Context, id string) error
	AddAttributeOption(ctx context.Context, attributeID string, in service.AttributeOptionInput) (models.AttributeOption, error)
	DeleteAttributeOption(ctx context.Context, id string) error
	SetProductAttributes(
		ctx context.Context, productID string, values []service.ProductAttributeInput,
	) ([]models.ProductAttributeValue, error)
	StoreFacets(ctx context.Context, opts service.StoreListOptions) ([]service.Facet, error)
	// ListOptionValues is the fourth vocabulary read and the only one that
	// returns TEXT rather than ids; the reason is in
	// [service.Service.ListOptionValues].
	ListOptionValues(
		ctx context.Context,
		opts service.ListOptionValuesOptions,
	) (service.ListResult[models.OptionValuePair], error)
	DeleteTag(ctx context.Context, id string) error

	ListStoreProducts(ctx context.Context, opts service.StoreListOptions) (service.ListResult[service.StoreProduct], error)
	GetStoreProduct(ctx context.Context, idOrHandle string, salesChannelIDs []string) (service.StoreProduct, error)
}
