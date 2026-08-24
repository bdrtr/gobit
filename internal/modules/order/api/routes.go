package api

import "github.com/go-chi/chi/v5"

// Routes modülün store ve admin uçlarını router'a bağlar.
//
// Uçlar TAM YOLLA kaydedilir; "/store/v1" ya da "/admin/v1" için alt router
// (chi.Route/Mount) AÇILMAZ. Sebep somut: registry tüm modüllerin Routes'unu
// AYNI router üzerinde çağırır ve chi, aynı desene ikinci kez mount edilmeyi
// panikle reddeder. İlk modül "/store/v1"i mount etseydi, ikinci modül
// sunucuyu açılışta düşürürdü.
//
// Sipariş OLUŞTURMA ucu bilinçli olarak yoktur; gerekçe için bkz. paket
// belgesi.
func (h *Handler) Routes(r chi.Router) {
	// --- Store API (müşteri, YALNIZCA OKUMA) ---
	//
	// Siparişin MÜŞTERİYE AİT olduğunun doğrulanması Faz 8'in (auth) işidir;
	// o gelene kadar uç kimliği bilen herkese açıktır ve bu bilinçli bir
	// eksikliktir, gizlenmiş bir varsayım değil.
	r.Get("/store/v1/orders/{id}", h.storeGetOrder)

	// --- Admin API (yönetim) ---
	r.Get("/admin/v1/orders", h.adminListOrders)
	r.Get("/admin/v1/orders/{id}", h.adminGetOrder)

	// Durum geçişleri POST'tur ve gövdeleri yoktur (iptal hariç): geçiş bir
	// alan güncellemesi değil, bir EYLEMDİR ve kaynağın kendisine yapılan bir
	// PATCH gibi görünmemelidir.
	r.Post("/admin/v1/orders/{id}/cancel", h.adminCancelOrder)
	r.Post("/admin/v1/orders/{id}/complete", h.adminCompleteOrder)
	r.Post("/admin/v1/orders/{id}/archive", h.adminArchiveOrder)

	// Satış sonrası kayıtları: Faz 6'da yalnızca oluştur + oku + listele.
	// Durum geçişleri (iade alındı, değişim tamamlandı …) sonraki fazın işidir.
	r.Get("/admin/v1/orders/{id}/returns", h.adminListReturns)
	r.Post("/admin/v1/orders/{id}/returns", h.adminCreateReturn)
	r.Get("/admin/v1/orders/{id}/returns/{returnId}", h.adminGetReturn)
	r.Get("/admin/v1/orders/{id}/exchanges", h.adminListExchanges)
	r.Post("/admin/v1/orders/{id}/exchanges", h.adminCreateExchange)
	r.Get("/admin/v1/orders/{id}/exchanges/{exchangeId}", h.adminGetExchange)
	r.Get("/admin/v1/orders/{id}/claims", h.adminListClaims)
	r.Post("/admin/v1/orders/{id}/claims", h.adminCreateClaim)
	r.Get("/admin/v1/orders/{id}/claims/{claimId}", h.adminGetClaim)
}
