package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
)

// The scope dictionary: the scopes product's admin endpoints ask for.
//
// The dictionary has the SAME shape in every module and DELIBERATELY consists
// of two entries: read and write. Defining a separate scope per resource
// ("variants:write", "collections:read" …) grows the list but makes no new
// decision possible today — the only place that hands out scopes is the auth
// module, and a scope name that is never handed out is a name nobody knows the
// purpose of on the day it is first granted. The distinction gets added when it
// is really needed.
const (
	// ScopeRead is the scope the READ endpoints of product's admin surface ask
	// for.
	//
	// It is enough to read the catalog (products, variants, options, taxonomy
	// and the cross-module links); it opens no write endpoint. It does not have
	// to be granted separately to fully privileged identities: a caller
	// carrying corehttp.ScopeAdmin satisfies this too (see
	// corehttp.Principal.HasScope).
	ScopeRead = "product:read"

	// ScopeWrite is the scope the WRITE endpoints of product's admin surface
	// ask for.
	//
	// It is kept apart from read so that an integration that only REPORTS the
	// catalog (price comparison, export, a search index) does not have to run
	// with an identity that can delete products.
	ScopeWrite = "product:write"
)

// Routes binds the module's store and admin endpoints to the router.
//
// The endpoints are registered with their FULL PATH; NO sub-router
// (chi.Route/Mount) is OPENED for "/admin/v1" or "/store/v1". The reason is
// concrete: the registry calls the Routes of every module on the SAME router
// and chi refuses a second mount of the same pattern with a panic. Had the
// first module mounted "/admin/v1", the second module (pricing) would take the
// server down at startup.
//
// # THE GUARD
//
// There are two layers and both are necessary:
//
//  1. IDENTITY — the /admin/v1 endpoints are guarded with corehttp.RequireAdmin.
//     That middleware is attached not in this module but on the side that
//     builds the router (see corehttp.APIGuards).
//  2. SCOPE — the endpoints are marked HERE, endpoint by endpoint, with
//     corehttp.RequireScope: GET endpoints ask for [ScopeRead],
//     POST/PUT/PATCH/DELETE endpoints for [ScopeWrite].
//
// Without the second layer authentication would stand in for authorization. Its
// concrete cost is this: an admin user whose scopes were emptied out
// (auth service.CreateUserInput.Scopes = []string{}) could log in and call
// DELETE /admin/v1/products/{id}, that is, delete the catalog.
//
// NO scope is ADDED to the store endpoints: the identity of /store/v1 is the
// publishable key and that key by definition CARRIES NO scope. Putting a scope
// there would be putting a condition no store client could ever satisfy.
//
// That does not mean the storefront makes no authorization decision. Since
// ADR 0044 the channel-scoped catalog reads take a sales channel from the PATH
// and refuse a channel the request's key does not hold, so those reads can
// answer 403. The check is in the handler rather than in a middleware because
// the answer depends on the key's channel set and not on the route, and because
// a middleware would have to know which routes carry the segment — a second
// list beside this one.
func (h *Handler) Routes(r chi.Router) {
	read := r.With(corehttp.RequireScope(ScopeRead))
	write := r.With(corehttp.RequireScope(ScopeWrite))
	// A write that revises a product takes the version it was asked on and
	// answers the version after it (ADR 0222).
	revising := write.With(productPreconditions)

	// --- Store API (customer) ---
	//
	// The three CHANNEL-SCOPED reads carry the sales channel as a path segment
	// (ADR 0044). The channel used to arrive in a request header, which left a
	// shared cache with no key it could see: one URL, one answer per key. Now
	// the body is a function of the channel alone and the URL is the cache key.
	// The paths are constants because each is written here and in [Describe]
	// and the two must not drift; see [pathStoreProducts].
	r.Get(pathStoreProducts, h.storeListProducts)
	r.Get(pathStoreProduct, h.storeGetProduct)
	r.Get(pathStoreOptionValues, h.storeListOptionValues)
	// A product's neighbors, under the same channel segment (ADR 0180).
	r.Get(pathStoreRelated, h.storeRelatedProducts)
	// The add-ons a product's lines may carry, under it too (ADR 0228).
	r.Get(pathStoreAddOns, h.storeAddOns)

	// The vocabulary a storefront needs to use the catalog filters: it has the
	// word a shopper clicked, and the listing takes an id.
	//
	// These three stay at their unscoped URLs, and the storefront's addresses
	// are not uniform as a result. The reason is written where a reader meets
	// it (see the vocabulary block in store.go): they are not channel-scoped
	// today, so a channel segment on them would name a distinction their bodies
	// do not have.
	r.Get("/store/v1/collections", h.storeListCollections)
	r.Get("/store/v1/categories", h.storeListCategories)
	r.Get("/store/v1/tags", h.storeListTags)
	// The attribute filter's vocabulary and its counts (ADR 0219). The
	// vocabulary is unscoped like the tags; the counts are over the channel's
	// catalog, so they carry the channel segment like the listing.
	r.Get("/store/v1/product-attributes", h.storeListAttributes)
	r.Get(pathStoreFacets, h.storeFacets)

	// The GraphQL storefront read surface. ONLY POST is registered; for why GET
	// is not opened see [graph.NewHandler]. The path sitting under /store/v1
	// brings the guard stack (publishable key + rate limit) along automatically
	// and fills the sales channel ids into the Principal.
	r.Method(http.MethodPost, graph.Path, h.graphql)

	// --- Admin API: products ---
	write.Post("/admin/v1/products", h.adminCreateProduct)
	// A catalog import, applied by a job (ADR 0205).
	write.Post(pathAdminProductImports, h.adminCreateImport)
	read.Get(pathAdminProductImport, h.adminGetImport)
	read.Get("/admin/v1/products", h.adminListProducts)
	// The catalog as CSV (ADR 0204). It carries prices, so it takes pricing's
	// read as well as this module's.
	read.With(corehttp.RequireScope(scopePricingRead)).Get(pathAdminProductExport, h.adminExportProducts)
	read.Get("/admin/v1/products/{id}", h.adminGetProduct)
	revising.Patch("/admin/v1/products/{id}", h.adminUpdateProduct)
	write.Delete("/admin/v1/products/{id}", h.adminDeleteProduct)
	// The moment a draft is published (ADR 0177); a sub-resource because the
	// PATCH above cannot set a field back to empty.
	write.Put(pathProductSchedule, h.adminScheduleProduct)
	write.Delete(pathProductSchedule, h.adminCancelSchedule)
	// A product's revisions (ADR 0221): read them, and write one back.
	read.Get(pathProductRevisions, h.adminListRevisions)
	read.Get(pathProductRevision, h.adminGetRevision)
	revising.Post(pathProductRevisionRestore, h.adminRestoreRevision)
	// A product's relations (ADR 0180): read them all, replace one kind.
	read.Get(pathProductRelations, h.adminListRelations)
	write.Put(pathProductRelationsOfType, h.adminSetRelations)
	// A product's add-ons (ADR 0228): read and replace the list. They are not
	// in the revision view, as relations are not (ADR 0221).
	read.Get(pathProductAddOns, h.adminListAddOns)
	write.Put(pathProductAddOns, h.adminSetAddOns)

	// --- Admin API: variants ---
	revising.Post("/admin/v1/products/{id}/variants", h.adminCreateVariant)
	read.Get("/admin/v1/products/{id}/variants", h.adminListVariants)
	read.Get("/admin/v1/variants/{id}", h.adminGetVariant)
	revising.Patch("/admin/v1/variants/{id}", h.adminUpdateVariant)
	revising.Delete("/admin/v1/variants/{id}", h.adminDeleteVariant)
	// What a bundle variant is made of (ADR 0234): a revision of its product,
	// like the variant's own update.
	read.Get(pathVariantBundle, h.adminGetBundle)
	revising.Put(pathVariantBundle, h.adminSetBundle)

	// --- Admin API: options ---
	revising.Post("/admin/v1/products/{id}/options", h.adminCreateOption)
	read.Get("/admin/v1/products/{id}/options", h.adminListOptions)
	revising.Post("/admin/v1/product-options/{id}/values", h.adminAddOptionValue)
	revising.Delete("/admin/v1/product-options/{id}", h.adminDeleteOption)
	// The value's own delete takes the VALUE's id, not the option's; see
	// [Handler.adminDeleteOptionValue] for why it is not nested.
	revising.Delete("/admin/v1/product-option-values/{id}", h.adminDeleteOptionValue)

	// --- Admin API: cross-module links ---
	// The price and stock records are produced by pricing/inventory; the link
	// is established by the catalog. Establishing a link CHANGES catalog data
	// (it decides which price set and which inventory item the variant will
	// show), which is why it asks for [ScopeWrite]; the endpoint that only
	// reads the link makes do with [ScopeRead].
	write.Put("/admin/v1/variants/{id}/price-set", h.adminSetPriceSet)
	write.Delete("/admin/v1/variants/{id}/price-set", h.adminDeletePriceSet)
	write.Put("/admin/v1/variants/{id}/inventory-item", h.adminSetInventoryItem)
	write.Delete("/admin/v1/variants/{id}/inventory-item", h.adminDeleteInventoryItem)
	read.Get("/admin/v1/variants/{id}/links", h.adminGetVariantLinks)

	// The sales channel link is at the PRODUCT level and is many-to-many; that
	// is why the path follows the collection pattern rather than the singular
	// pattern of the variant links (POST adds, DELETE with the id in the path
	// removes). Establishing a link decides WHICH STOREFRONTS the product WILL
	// APPEAR IN, that is, it changes catalog data: the write endpoints ask for
	// [ScopeWrite], the read endpoint for [ScopeRead].
	// The image/upload binding is READ ONLY here and there is no write
	// endpoint: the binding is made when the image itself is created (the
	// image carries the upload id in the create body) and it dies with it.
	// Opening a "bind this image to that upload" endpoint would make it
	// possible for the image's own column and the binding to disagree.
	read.Get("/admin/v1/product-images/by-upload/{upload_id}", h.adminListImagesOfUpload)

	// An image is added, corrected and removed ONE AT A TIME, and every one of
	// the three carries BOTH identifiers: an image addressed by its own id
	// alone would let a caller name a product of their own and reach somebody
	// else's picture (ADR 0108). The patch reaches the alt text, the rank and
	// the metadata; it does NOT reach the address, for the reason the paragraph
	// above gives about the binding.
	revising.Post("/admin/v1/products/{id}/images", h.adminAddProductImage)
	revising.Patch("/admin/v1/products/{id}/images/{imageId}", h.adminUpdateProductImage)
	revising.Delete("/admin/v1/products/{id}/images/{imageId}", h.adminRemoveProductImage)

	write.Post("/admin/v1/products/{id}/sales-channels", h.adminAddSalesChannel)
	write.Delete("/admin/v1/products/{id}/sales-channels/{sales_channel_id}", h.adminRemoveSalesChannel)
	read.Get("/admin/v1/products/{id}/sales-channels", h.adminListSalesChannels)

	// --- Admin API: taxonomy (list, create, delete) ---
	//
	// The surface said "list + create" until 2026-09-06 and the delete was the
	// missing third: all three tables have carried a deleted_at and a partial
	// unique index built to free the handle on delete since the first
	// migration, and nothing ever set the column, so a collection, category or
	// tag created by mistake stayed on the storefront permanently
	// (docs/gaps.md D18).
	write.Post("/admin/v1/product-collections", h.adminCreateCollection)
	read.Get("/admin/v1/product-collections", h.adminListCollections)
	write.Delete("/admin/v1/product-collections/{id}", h.adminDeleteCollection)
	write.Post("/admin/v1/product-categories", h.adminCreateCategory)
	read.Get("/admin/v1/product-categories", h.adminListCategories)
	write.Patch("/admin/v1/product-categories/{id}", h.adminUpdateCategory)
	write.Delete("/admin/v1/product-categories/{id}", h.adminDeleteCategory)
	write.Post("/admin/v1/product-types", h.adminCreateProductType)
	read.Get("/admin/v1/product-types", h.adminListProductTypes)
	write.Delete("/admin/v1/product-types/{id}", h.adminDeleteProductType)
	write.Post("/admin/v1/product-attributes", h.adminCreateAttribute)
	read.Get("/admin/v1/product-attributes", h.adminListAttributes)
	write.Patch("/admin/v1/product-attributes/{id}", h.adminUpdateAttribute)
	write.Delete("/admin/v1/product-attributes/{id}", h.adminDeleteAttribute)
	write.Post("/admin/v1/product-attributes/{id}/options", h.adminAddAttributeOption)
	write.Delete("/admin/v1/product-attribute-options/{id}", h.adminDeleteAttributeOption)
	revising.Put("/admin/v1/products/{id}/attributes", h.adminSetProductAttributes)
	write.Post("/admin/v1/product-tags", h.adminCreateTag)
	read.Get("/admin/v1/product-tags", h.adminListTags)
	write.Delete("/admin/v1/product-tags/{id}", h.adminDeleteTag)
}
