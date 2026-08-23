package api

import "github.com/go-chi/chi/v5"

// Routes modülün store ve admin uçlarını router'a bağlar.
//
// Uçlar TAM YOLLA kaydedilir; "/store/v1" ya da "/admin/v1" için alt router
// (chi.Route/Mount) AÇILMAZ. Sebep somut: registry tüm modüllerin Routes'unu
// AYNI router üzerinde çağırır ve chi, aynı desene ikinci kez mount edilmeyi
// panikle reddeder. İlk modül "/store/v1"i mount etseydi, ikinci modül
// sunucuyu açılışta düşürürdü.
func (h *Handler) Routes(r chi.Router) {
	// --- Store API (müşteri) ---
	r.Post("/store/v1/carts", h.storeCreateCart)
	r.Get("/store/v1/carts/{id}", h.storeGetCart)
	r.Post("/store/v1/carts/{id}", h.storeUpdateCart)
	r.Delete("/store/v1/carts/{id}", h.storeDeleteCart)

	r.Post("/store/v1/carts/{id}/line-items", h.storeAddLineItem)
	r.Patch("/store/v1/carts/{id}/line-items/{line_item_id}", h.storeUpdateLineItem)
	r.Delete("/store/v1/carts/{id}/line-items/{line_item_id}", h.storeRemoveLineItem)

	r.Put("/store/v1/carts/{id}/shipping-address", h.storeSetShippingAddress)
	r.Put("/store/v1/carts/{id}/billing-address", h.storeSetBillingAddress)

	r.Post("/store/v1/carts/{id}/shipping-methods", h.storeAddShippingMethod)
	r.Delete("/store/v1/carts/{id}/shipping-methods/{shipping_method_id}", h.storeRemoveShippingMethod)

	// --- Admin API (yönetim, YALNIZCA OKUMA) ---
	r.Get("/admin/v1/carts", h.adminListCarts)
	r.Get("/admin/v1/carts/{id}", h.adminGetCart)
}
