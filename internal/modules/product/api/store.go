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
func (h *Handler) storeListProducts(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := paging(r)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	result, err := h.svc.ListStoreProducts(r.Context(), service.StoreListOptions{
		CollectionID: stringParam(r, "collection_id"),
		Search:       stringParam(r, "q"),
		Limit:        limit,
		Offset:       offset,
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
func (h *Handler) storeGetProduct(w http.ResponseWriter, r *http.Request) {
	id, err := pathParam(r, "id")
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}

	product, err := h.svc.GetStoreProduct(r.Context(), id)
	if err != nil {
		corehttp.WriteError(r.Context(), w, err)
		return
	}
	writeItem(w, r, http.StatusOK, product)
}
