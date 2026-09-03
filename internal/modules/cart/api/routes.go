package api

import (
	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// StoreCartsPath is the storefront's cart-creation endpoint.
//
// It is a constant because the composition root has to name it: this path is
// EXEMPT from the idempotency ring, and the reason is written where the
// exemption is declared (cmd/server/setup.go). Spelling it there by hand would
// let the two drift, and the drift would restore a cross-shopper leak.
const StoreCartsPath = "/store/v1/carts"

// Yetki sözlüğü: cart'ın yönetim uçlarının istediği yetkiler.
//
// Adlar TÜM modüllerde aynı kalıptadır ("<modül>:read" / "<modül>:write").
// Her modülün kendi sözcüğünü uydurması, yetki dağıtan kişinin modül başına
// ayrı bir sözlük ezberlemesi demek olurdu; ezberlenmeyen sözlükte yapılan
// hata da her zaman aynı yöne düşer — fazla yetki verilir.
const (
	// ScopeRead cart yönetim yüzeyindeki OKUMA uçlarının istediği yetkidir.
	//
	// Sepetleri listelemeye ve tek tek okumaya yeter. Tam yetkili kimliklere
	// ayrıca verilmesi gerekmez: corehttp.ScopeAdmin taşıyan bir çağıran bunu
	// da karşılar (bkz. corehttp.Principal.HasScope).
	ScopeRead = "cart:read"

	// ScopeWrite cart yönetim yüzeyindeki YAZMA uçlarının istediği yetkidir.
	//
	// Bugün HİÇBİR route'u açmaz, çünkü cart'ın /admin/v1 yüzeyi yalnızca
	// okumadır (bkz. [Handler.Routes]). Yine de yayımlanır: sözlük modüller
	// arasında aynı olduğu için, yönetime bir gün yazma ucu eklendiğinde
	// yetkinin adı O GÜN uydurulmaz. Adın o gün seçilmesi, çoktan dağıtılmış
	// yetki listelerinin sessizce eksik kalması demek olurdu.
	ScopeWrite = "cart:write"
)

// Routes modülün store ve admin uçlarını router'a bağlar.
//
// Uçlar TAM YOLLA kaydedilir; "/store/v1" ya da "/admin/v1" için alt router
// (chi.Route/Mount) AÇILMAZ. Sebep somut: registry tüm modüllerin Routes'unu
// AYNI router üzerinde çağırır ve chi, aynı desene ikinci kez mount edilmeyi
// panikle reddeder. İlk modül "/store/v1"i mount etseydi, ikinci modül
// sunucuyu açılışta düşürürdü.
//
// # KORUMA
//
// Yönetim uçlarının iki katmanı vardır ve ikisi de gereklidir:
//
//  1. KİMLİK — corehttp.RequireAdmin. Bu modülde DEĞİL, router'ı kuran
//     tarafta takılır (bkz. corehttp.APIGuards).
//  2. YETKİ — BURADA, uç uç corehttp.RequireScope ile. Okuma uçları
//     [ScopeRead] ister.
//
// İkinci katman olmasaydı kimlik doğrulama yetkilendirmenin yerine geçerdi:
// yetkileri bilinçli olarak boşaltılmış bir yönetim kullanıcısı da geçerli bir
// kimliktir ve GET /admin/v1/carts ile tüm müşterilerin sepetlerini, e-posta
// adresleri dâhil okuyabilirdi.
//
// Store uçlarına yetki EKLENMEZ: mağaza yüzeyinin kimliği publishable
// anahtardır ve o anahtar tanımı gereği yetki TAŞIMAZ.
func (h *Handler) Routes(r chi.Router) {
	okuma := r.With(corehttp.RequireScope(ScopeRead))

	// --- Store API (müşteri) ---
	r.Post(StoreCartsPath, h.storeCreateCart)
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

	// Sepeti siparişe çeviren uç. Sepetin uçlarının sahibi bu modüldür,
	// dolayısıyla sepeti KAPATAN uç da buradadır; bileşim kökü yalnızca akışı
	// kurar ve container'a bırakır (bkz. [Handler.storeCompleteCart]).
	r.Post("/store/v1/carts/{id}/complete", h.storeCompleteCart)

	// --- Admin API (yönetim, YALNIZCA OKUMA) ---
	okuma.Get("/admin/v1/carts", h.adminListCarts)
	okuma.Get("/admin/v1/carts/{id}", h.adminGetCart)
}
