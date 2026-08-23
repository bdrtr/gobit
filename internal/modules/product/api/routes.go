package api

import "github.com/go-chi/chi/v5"

// Routes modülün store ve admin uçlarını router'a bağlar.
//
// Uçlar TAM YOLLA kaydedilir; "/admin/v1" ya da "/store/v1" için alt router
// (chi.Route/Mount) AÇILMAZ. Sebep somut: registry tüm modüllerin Routes'unu
// AYNI router üzerinde çağırır ve chi, aynı desene ikinci kez mount edilmeyi
// panikle reddeder. İlk modül "/admin/v1"i mount etseydi, ikinci modül
// (pricing) sunucuyu açılışta düşürürdü.
func (h *Handler) Routes(r chi.Router) {
	// --- Store API (müşteri) ---
	r.Get("/store/v1/products", h.storeListProducts)
	r.Get("/store/v1/products/{id}", h.storeGetProduct)

	// --- Admin API: ürünler ---
	r.Post("/admin/v1/products", h.adminCreateProduct)
	r.Get("/admin/v1/products", h.adminListProducts)
	r.Get("/admin/v1/products/{id}", h.adminGetProduct)
	r.Patch("/admin/v1/products/{id}", h.adminUpdateProduct)
	r.Delete("/admin/v1/products/{id}", h.adminDeleteProduct)

	// --- Admin API: varyantlar ---
	r.Post("/admin/v1/products/{id}/variants", h.adminCreateVariant)
	r.Get("/admin/v1/products/{id}/variants", h.adminListVariants)
	r.Get("/admin/v1/variants/{id}", h.adminGetVariant)
	r.Patch("/admin/v1/variants/{id}", h.adminUpdateVariant)
	r.Delete("/admin/v1/variants/{id}", h.adminDeleteVariant)

	// --- Admin API: seçenekler ---
	r.Post("/admin/v1/products/{id}/options", h.adminCreateOption)
	r.Get("/admin/v1/products/{id}/options", h.adminListOptions)
	r.Post("/admin/v1/product-options/{id}/values", h.adminAddOptionValue)
	r.Delete("/admin/v1/product-options/{id}", h.adminDeleteOption)

	// --- Admin API: modüller arası bağlar ---
	// Fiyat ve stok kayıtlarını pricing/inventory üretir; bağı katalog kurar.
	r.Put("/admin/v1/variants/{id}/price-set", h.adminSetPriceSet)
	r.Delete("/admin/v1/variants/{id}/price-set", h.adminDeletePriceSet)
	r.Put("/admin/v1/variants/{id}/inventory-item", h.adminSetInventoryItem)
	r.Delete("/admin/v1/variants/{id}/inventory-item", h.adminDeleteInventoryItem)
	r.Get("/admin/v1/variants/{id}/links", h.adminGetVariantLinks)

	// --- Admin API: taksonomi (sade yüzey: liste + oluştur) ---
	r.Post("/admin/v1/product-collections", h.adminCreateCollection)
	r.Get("/admin/v1/product-collections", h.adminListCollections)
	r.Post("/admin/v1/product-categories", h.adminCreateCategory)
	r.Get("/admin/v1/product-categories", h.adminListCategories)
	r.Post("/admin/v1/product-tags", h.adminCreateTag)
	r.Get("/admin/v1/product-tags", h.adminListTags)
}
