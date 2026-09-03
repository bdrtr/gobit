package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// storeListProducts GET /store/v1/products
//
// Faz 4'ün kalbi budur: vitrin listesi ürünleri FİYAT ve STOK bilgisiyle
// birlikte döner. İkisi de başka modüllerin verisidir ve buraya link'ler
// üzerinden Query katmanıyla gelir; product modülü onları import etmez.
//
// Liste ayrıca isteğin SATIŞ KANALINA göre süzülür; kanalların nereden
// okunduğu için bkz. [salesChannelIDs].
//
// # with_count
//
// Toplam sayaç isteğe bağlıdır ve VARSAYILANI TRUE'dur: parametre verilmeyen
// istek bugünkü yanıtın baytını bayta aynısını alır. "with_count=false"
// diyen istek sayaç sorgusunu HİÇ çalıştırmaz ve zarfında "count" alanı
// bulunmaz (bkz. [listEnvelope]).
//
// Neden bir parametre gerekiyor: sayaç, sayfa boyutundan bağımsız olarak satış
// kanalı süzgecinin uygulandığı kümenin tamamını gezer. gobit_load üzerinde
// ölçüldü (52.004 ürün, 52.000 kanal ataması, LIMIT 20, ortanca) — ölçülen şey
// SERVİS ÇAĞRISIDIR (service.ListProducts), ucun tamamı değil:
//
//	sayarak (bugünkü varsayılan)  67,00 ms
//	saymadan (with_count=false)    0,65 ms
//
// Ucun geri kalanı — varyantların fiyat ve stok zenginleştirmesi — sayaçtan
// bağımsızdır ve with_count=false onu ATLAMAZ; ölçüldüğünde iki bacak da
// indeks üzerinden 0,1-0,2 ms sürüyor, yani sayaçsız uç ~1 ms'dir, 0,65 değil.
// Oran değişmiyor: büyük katalogta isteğin SQL'inin neredeyse tamamı sayaçtır
// ve maliyeti KATALOGLA büyür, sayfa boyutuyla değil. Sayı istemcinin ilk sayfada bir kez ihtiyaç
// duyduğu bir şeydir; sonraki her sayfada aynı sayı yeniden hesaplanır.
//
// Değer OKUNMAZ da yorumlanmaz: "with_count=abc" tipli bir doğrulama hatası
// döner (bkz. [boolParam]). Sessizce varsayılana düşmek, istemcinin sayacı
// kapattığını sanıp maliyeti ödemeye devam etmesi olurdu.
func (h *Handler) storeListProducts(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := paging(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	withCount, err := boolParam(r, "with_count", true)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	result, err := h.svc.ListStoreProducts(r.Context(), service.StoreListOptions{
		CollectionID:    stringParam(r, "collection_id"),
		Search:          stringParam(r, "q"),
		SalesChannelIDs: salesChannelIDs(r),
		Limit:           limit,
		Offset:          offset,
		SkipCount:       !withCount,
	})
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeList(w, r, result)
}

// storeGetProduct GET /store/v1/products/{id}
//
// Yol parçası ürün kimliği ya da handle olabilir: vitrin adresleri handle
// taşır ("/store/v1/products/tisort"), yönetim akışları kimlik.
//
// Tekil uç da listeyle AYNI satış kanalı süzgecine tabidir; gerekçe için bkz.
// service.Service.GetStoreProduct.
func (h *Handler) storeGetProduct(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	product, err := h.svc.GetStoreProduct(r.Context(), id, salesChannelIDs(r))
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, product)
}

// salesChannelIDs isteğin bağlı olduğu satış kanallarını DOĞRULANMIŞ KİMLİKTEN
// okur.
//
// Kanal, istemcinin sorgu dizesinde bildirdiği bir değer OLMAMALIDIR ve bu
// yüzden burada r.URL.Query()'ye hiç bakılmaz: "?sales_channel_id=..." kabul
// edilseydi elindeki herhangi bir publishable anahtarla gelen bir istemci
// BAŞKA bir kanalın kataloğunu okuyabilirdi — yani süzgeç bir yetkilendirme
// olmaktan çıkıp bir görüntüleme tercihine dönüşürdü. Kimliği çekirdeğin
// corehttp.RequireStore middleware'i koyar; kanal listesi anahtarın kaydından
// gelir.
//
// Kuralın KENDİSİ burada değil [graph.SalesChannelIDsFromContext]'tedir ve
// bunun sebebi ikinci okuma yüzeyidir: GraphQL resolver'larının elinde
// *http.Request yoktur, yalnızca context vardır. Kural iki yerde yazılsaydı
// biri düzeltilip diğeri unutulduğunda yüzeylerden birinde katalog sızıntısı
// olurdu — dönüşün nil ile BOŞ dilim arasındaki farkı dâhil (anlamı için o
// belgeye bakın; ikisi farklı şey söyler).
func salesChannelIDs(r *http.Request) []string {
	return graph.SalesChannelIDsFromContext(r.Context())
}
