package api

import (
	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// Yetki sözlüğü: order'ın yönetim uçlarının istediği yetkiler.
//
// Ayrım OKUMA/YAZMA üzerinedir, kaynak üzerine değil. "order_returns:write"
// gibi kaynak başına yetkiler listeyi büyütür ama bugün verilebilecek yeni bir
// karar üretmez: iade açabilen bir kimliğin siparişi iptal edememesi diye bir
// ihtiyaç henüz yok ve olmayan bir ihtiyaç için tanımlanan yetki adı, ilk kez
// verildiği gün ne işe yaradığı bilinmeyen bir addır.
const (
	// ScopeRead order yönetim yüzeyindeki OKUMA uçlarının istediği yetkidir.
	//
	// Siparişleri, iade/değişim/hasar kayıtlarını okumaya yeter; hiçbir durum
	// geçişini açmaz. Tam yetkili kimliklere ayrıca verilmesi gerekmez:
	// corehttp.ScopeAdmin taşıyan bir çağıran bunu da karşılar (bkz.
	// corehttp.Principal.HasScope).
	ScopeRead = "order:read"

	// ScopeWrite order yönetim yüzeyindeki YAZMA uçlarının istediği yetkidir.
	//
	// Durum geçişleri (iptal, tamamla, arşivle) ve satış sonrası kayıt açma
	// bunu ister. Geçişlerin çoğu GERİ ALINAMAZ — iptal edilmiş sipariş geri
	// açılmaz — bu yüzden okuma yetkisinden ayrılması bir biçimsellik değil,
	// hasarın sınırıdır.
	ScopeWrite = "order:write"
)

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
//
// # KORUMA
//
// Yönetim uçları iki katmanla korunur ve ikisi de gereklidir:
//
//  1. KİMLİK — corehttp.RequireAdmin, router'ı kuran tarafta takılır (bkz.
//     corehttp.APIGuards); bu modülün işi değildir.
//  2. YETKİ — uçlar BURADA, uç uç corehttp.RequireScope ile işaretlenir.
//
// İkinci katman olmadan kimlik doğrulama yetkilendirmenin yerine geçerdi:
// yetkileri BOŞ bırakılmış bir yönetim kullanıcısı giriş yapıp ödemesi alınmış
// bir siparişi iptal edebilirdi. Kimlik "kim" sorusunu yanıtlar, "ne
// yapabilir" sorusunu değil.
func (h *Handler) Routes(r chi.Router) {
	okuma := r.With(corehttp.RequireScope(ScopeRead))
	yazma := r.With(corehttp.RequireScope(ScopeWrite))

	// --- Store API (müşteri, YALNIZCA OKUMA) ---
	//
	// Mağaza yüzeyine yetki EKLENMEZ: oradaki kimlik publishable anahtardır ve
	// o anahtar tanımı gereği yetki taşımaz. Siparişin MÜŞTERİYE AİT olduğunun
	// doğrulanması ayrı bir iştir ve hâlâ yapılmıyor; bu bilinçli bir
	// eksikliktir, gizlenmiş bir varsayım değil.
	r.Get("/store/v1/orders/{id}", h.storeGetOrder)

	// --- Admin API (yönetim) ---
	okuma.Get("/admin/v1/orders", h.adminListOrders)
	okuma.Get("/admin/v1/orders/{id}", h.adminGetOrder)

	// Durum geçişleri POST'tur ve gövdeleri yoktur (iptal hariç): geçiş bir
	// alan güncellemesi değil, bir EYLEMDİR ve kaynağın kendisine yapılan bir
	// PATCH gibi görünmemelidir.
	yazma.Post("/admin/v1/orders/{id}/cancel", h.adminCancelOrder)
	yazma.Post("/admin/v1/orders/{id}/complete", h.adminCompleteOrder)
	yazma.Post("/admin/v1/orders/{id}/archive", h.adminArchiveOrder)

	// Satış sonrası kayıtları: Faz 6'da yalnızca oluştur + oku + listele.
	// Durum geçişleri (iade alındı, değişim tamamlandı …) sonraki fazın işidir.
	okuma.Get("/admin/v1/orders/{id}/returns", h.adminListReturns)
	yazma.Post("/admin/v1/orders/{id}/returns", h.adminCreateReturn)
	okuma.Get("/admin/v1/orders/{id}/returns/{returnId}", h.adminGetReturn)
	okuma.Get("/admin/v1/orders/{id}/exchanges", h.adminListExchanges)
	yazma.Post("/admin/v1/orders/{id}/exchanges", h.adminCreateExchange)
	okuma.Get("/admin/v1/orders/{id}/exchanges/{exchangeId}", h.adminGetExchange)
	okuma.Get("/admin/v1/orders/{id}/claims", h.adminListClaims)
	yazma.Post("/admin/v1/orders/{id}/claims", h.adminCreateClaim)
	okuma.Get("/admin/v1/orders/{id}/claims/{claimId}", h.adminGetClaim)
}
