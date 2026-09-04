package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
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
func (h *Handler) Routes(r chi.Router) {
	read := r.With(corehttp.RequireScope(ScopeRead))
	write := r.With(corehttp.RequireScope(ScopeWrite))

	// --- Store API (customer) ---
	r.Get("/store/v1/products", h.storeListProducts)
	r.Get("/store/v1/products/{id}", h.storeGetProduct)

	// The GraphQL storefront read surface. ONLY POST is registered; for why GET
	// is not opened see [graph.NewHandler]. The path sitting under /store/v1
	// brings the guard stack (publishable key + rate limit) along automatically
	// and fills the sales channel ids into the Principal.
	r.Method(http.MethodPost, graph.Path, h.graphql)

	// --- Admin API: products ---
	write.Post("/admin/v1/products", h.adminCreateProduct)
	read.Get("/admin/v1/products", h.adminListProducts)
	read.Get("/admin/v1/products/{id}", h.adminGetProduct)
	write.Patch("/admin/v1/products/{id}", h.adminUpdateProduct)
	write.Delete("/admin/v1/products/{id}", h.adminDeleteProduct)

	// --- Admin API: variants ---
	write.Post("/admin/v1/products/{id}/variants", h.adminCreateVariant)
	read.Get("/admin/v1/products/{id}/variants", h.adminListVariants)
	read.Get("/admin/v1/variants/{id}", h.adminGetVariant)
	write.Patch("/admin/v1/variants/{id}", h.adminUpdateVariant)
	write.Delete("/admin/v1/variants/{id}", h.adminDeleteVariant)

	// --- Admin API: options ---
	write.Post("/admin/v1/products/{id}/options", h.adminCreateOption)
	read.Get("/admin/v1/products/{id}/options", h.adminListOptions)
	write.Post("/admin/v1/product-options/{id}/values", h.adminAddOptionValue)
	write.Delete("/admin/v1/product-options/{id}", h.adminDeleteOption)

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
	write.Post("/admin/v1/products/{id}/sales-channels", h.adminAddSalesChannel)
	write.Delete("/admin/v1/products/{id}/sales-channels/{sales_channel_id}", h.adminRemoveSalesChannel)
	read.Get("/admin/v1/products/{id}/sales-channels", h.adminListSalesChannels)

	// --- Admin API: taxonomy (a plain surface: list + create) ---
	write.Post("/admin/v1/product-collections", h.adminCreateCollection)
	read.Get("/admin/v1/product-collections", h.adminListCollections)
	write.Post("/admin/v1/product-categories", h.adminCreateCategory)
	read.Get("/admin/v1/product-categories", h.adminListCategories)
	write.Post("/admin/v1/product-tags", h.adminCreateTag)
	read.Get("/admin/v1/product-tags", h.adminListTags)
}
