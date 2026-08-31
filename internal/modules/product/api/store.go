package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
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
func (h *Handler) storeListProducts(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := paging(r)
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
// Dönüş değerinin nil olup olmaması ANLAMLIDIR
// (bkz. service.StoreListOptions.SalesChannelIDs):
//
//   - Kimlik YOKSA nil dönülür. Bu, mağaza kimlik doğrulamasının bu kurulumda
//     hiç bağlanmamış olduğu durumdur (product tek başına dağıtılabilir) ve
//     süzgeç uygulanmaz; aksi hâlde auth'suz bir kurulumda vitrin sessizce
//     boşalırdı.
//   - Kimlik VARSA nil ASLA dönülmez: kanalsız bir kimlik boş küme demektir,
//     "süzme yok" demek değildir. Bu iki durumu bir tutmak, kanalsız bir
//     kimliğe tüm kanalların katalogunu açardı.
func salesChannelIDs(r *http.Request) []string {
	principal, ok := corehttp.PrincipalFromContext(r.Context())
	if !ok {
		return nil
	}
	if principal.SalesChannelIDs == nil {
		return []string{}
	}
	return principal.SalesChannelIDs
}
