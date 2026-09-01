package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
)

// Yetki sözlüğü: product'ın yönetim uçlarının istediği yetkiler.
//
// Sözlük tüm modüllerde AYNI biçimdedir ve BİLİNÇLİ olarak iki girdiden
// ibarettir: okuma ve yazma. Kaynak başına ayrı yetki ("variants:write",
// "collections:read" …) tanımlamak listeyi büyütür ama bugün verilebilecek
// hiçbir yeni kararı mümkün kılmaz — yetkiyi dağıtan tek yer auth modülüdür ve
// dağıtılmayan bir yetki adı, ilk kez verildiği gün ne işe yaradığı kimsenin
// bilmediği bir addır. Ayrım gerçekten gerektiğinde eklenir.
const (
	// ScopeRead product yönetim yüzeyindeki OKUMA uçlarının istediği yetkidir.
	//
	// Kataloğu (ürün, varyant, seçenek, taksonomi ve modüller arası bağlar)
	// okumaya yeter; hiçbir yazma ucunu açmaz. Tam yetkili kimliklere ayrıca
	// verilmesi gerekmez: corehttp.ScopeAdmin taşıyan bir çağıran bunu da
	// karşılar (bkz. corehttp.Principal.HasScope).
	ScopeRead = "product:read"

	// ScopeWrite product yönetim yüzeyindeki YAZMA uçlarının istediği
	// yetkidir.
	//
	// Okumadan ayrılması, kataloğu yalnızca RAPORLAYAN bir entegrasyonun
	// (fiyat karşılaştırma, dışa aktarma, arama indeksi) ürün silebilen bir
	// kimlikle çalışmak zorunda kalmaması içindir.
	ScopeWrite = "product:write"
)

// Routes modülün store ve admin uçlarını router'a bağlar.
//
// Uçlar TAM YOLLA kaydedilir; "/admin/v1" ya da "/store/v1" için alt router
// (chi.Route/Mount) AÇILMAZ. Sebep somut: registry tüm modüllerin Routes'unu
// AYNI router üzerinde çağırır ve chi, aynı desene ikinci kez mount edilmeyi
// panikle reddeder. İlk modül "/admin/v1"i mount etseydi, ikinci modül
// (pricing) sunucuyu açılışta düşürürdü.
//
// # KORUMA
//
// İki katman vardır ve ikisi de gereklidir:
//
//  1. KİMLİK — /admin/v1 uçları corehttp.RequireAdmin ile korunur. O
//     middleware bu modülde değil, router'ı kuran tarafta takılır (bkz.
//     corehttp.APIGuards).
//  2. YETKİ — uçlar BURADA, uç uç corehttp.RequireScope ile işaretlenir:
//     GET uçları [ScopeRead], POST/PUT/PATCH/DELETE uçları [ScopeWrite] ister.
//
// İkinci katman olmasaydı kimlik doğrulama yetkilendirmenin yerine geçerdi.
// Somut bedeli şudur: yetkileri boşaltılmış bir yönetim kullanıcısı
// (auth service.CreateUserInput.Scopes = []string{}) giriş yapıp
// DELETE /admin/v1/products/{id} çağırabilir, yani kataloğu silebilirdi.
//
// Mağaza uçlarına yetki EKLENMEZ: /store/v1'in kimliği publishable anahtardır
// ve o anahtar tanımı gereği yetki TAŞIMAZ. Oraya bir scope koymak, hiçbir
// mağaza istemcisinin sağlayamayacağı bir koşul koymak olurdu.
func (h *Handler) Routes(r chi.Router) {
	okuma := r.With(corehttp.RequireScope(ScopeRead))
	yazma := r.With(corehttp.RequireScope(ScopeWrite))

	// --- Store API (müşteri) ---
	r.Get("/store/v1/products", h.storeListProducts)
	r.Get("/store/v1/products/{id}", h.storeGetProduct)

	// GraphQL vitrin okuma yüzeyi. YALNIZCA POST kaydedilir; GET'in neden
	// açılmadığı için bkz. [graph.NewHandler]. Yolun /store/v1 altında olması
	// koruma yığınını (publishable anahtar + hız sınırı) otomatik getirir ve
	// satış kanalı kimliklerini Principal'a doldurur.
	r.Method(http.MethodPost, graph.Path, h.graphql)

	// --- Admin API: ürünler ---
	yazma.Post("/admin/v1/products", h.adminCreateProduct)
	okuma.Get("/admin/v1/products", h.adminListProducts)
	okuma.Get("/admin/v1/products/{id}", h.adminGetProduct)
	yazma.Patch("/admin/v1/products/{id}", h.adminUpdateProduct)
	yazma.Delete("/admin/v1/products/{id}", h.adminDeleteProduct)

	// --- Admin API: varyantlar ---
	yazma.Post("/admin/v1/products/{id}/variants", h.adminCreateVariant)
	okuma.Get("/admin/v1/products/{id}/variants", h.adminListVariants)
	okuma.Get("/admin/v1/variants/{id}", h.adminGetVariant)
	yazma.Patch("/admin/v1/variants/{id}", h.adminUpdateVariant)
	yazma.Delete("/admin/v1/variants/{id}", h.adminDeleteVariant)

	// --- Admin API: seçenekler ---
	yazma.Post("/admin/v1/products/{id}/options", h.adminCreateOption)
	okuma.Get("/admin/v1/products/{id}/options", h.adminListOptions)
	yazma.Post("/admin/v1/product-options/{id}/values", h.adminAddOptionValue)
	yazma.Delete("/admin/v1/product-options/{id}", h.adminDeleteOption)

	// --- Admin API: modüller arası bağlar ---
	// Fiyat ve stok kayıtlarını pricing/inventory üretir; bağı katalog kurar.
	// Bağ kurmak katalog verisini DEĞİŞTİRİR (varyantın hangi fiyat setini ve
	// hangi stok kalemini göstereceğini belirler), bu yüzden [ScopeWrite]
	// ister; yalnızca bağı okuyan uç [ScopeRead] ile yetinir.
	yazma.Put("/admin/v1/variants/{id}/price-set", h.adminSetPriceSet)
	yazma.Delete("/admin/v1/variants/{id}/price-set", h.adminDeletePriceSet)
	yazma.Put("/admin/v1/variants/{id}/inventory-item", h.adminSetInventoryItem)
	yazma.Delete("/admin/v1/variants/{id}/inventory-item", h.adminDeleteInventoryItem)
	okuma.Get("/admin/v1/variants/{id}/links", h.adminGetVariantLinks)

	// Satış kanalı bağı ÜRÜN düzeyindedir ve çoktan çoğadır; bu yüzden yol
	// varyant bağlarının tekil kalıbını değil koleksiyon kalıbını izler
	// (POST ekler, yoldaki kimlikle DELETE çıkarır). Bağ kurmak ürünün hangi
	// vitrinlerde GÖRÜNECEĞİNİ belirler, yani katalog verisini değiştirir:
	// yazma uçları [ScopeWrite], okuma ucu [ScopeRead] ister.
	yazma.Post("/admin/v1/products/{id}/sales-channels", h.adminAddSalesChannel)
	yazma.Delete("/admin/v1/products/{id}/sales-channels/{sales_channel_id}", h.adminRemoveSalesChannel)
	okuma.Get("/admin/v1/products/{id}/sales-channels", h.adminListSalesChannels)

	// --- Admin API: taksonomi (sade yüzey: liste + oluştur) ---
	yazma.Post("/admin/v1/product-collections", h.adminCreateCollection)
	okuma.Get("/admin/v1/product-collections", h.adminListCollections)
	yazma.Post("/admin/v1/product-categories", h.adminCreateCategory)
	okuma.Get("/admin/v1/product-categories", h.adminListCategories)
	yazma.Post("/admin/v1/product-tags", h.adminCreateTag)
	okuma.Get("/admin/v1/product-tags", h.adminListTags)
}
