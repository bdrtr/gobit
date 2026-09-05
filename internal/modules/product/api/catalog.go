package api

import (
	"context"

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

	CreateVariant(ctx context.Context, productID string, in service.CreateVariantInput) (models.Variant, error)
	GetVariant(ctx context.Context, id string) (models.Variant, error)
	ListVariants(ctx context.Context, opts service.ListVariantsOptions) (service.ListResult[models.Variant], error)
	UpdateVariant(ctx context.Context, id string, in service.UpdateVariantInput) (models.Variant, error)
	DeleteVariant(ctx context.Context, id string) error

	CreateOption(ctx context.Context, productID string, in service.CreateOptionInput) (models.Option, error)
	ListOptions(ctx context.Context, productID string) ([]models.Option, error)
	AddOptionValue(ctx context.Context, optionID, value string) (models.OptionValue, error)
	DeleteOption(ctx context.Context, id string) error

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

	CreateCategory(ctx context.Context, in service.CreateCategoryInput) (models.Category, error)
	GetCategory(ctx context.Context, id string) (models.Category, error)
	ListCategories(ctx context.Context, opts service.ListCategoriesOptions) (service.ListResult[models.Category], error)

	CreateTag(ctx context.Context, value string) (models.Tag, error)
	ListTags(ctx context.Context, limit, offset int) (service.ListResult[models.Tag], error)

	ListStoreProducts(ctx context.Context, opts service.StoreListOptions) (service.ListResult[service.StoreProduct], error)
	GetStoreProduct(ctx context.Context, idOrHandle string, salesChannelIDs []string) (service.StoreProduct, error)
}
