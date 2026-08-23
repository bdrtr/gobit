package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// storeGetPriceSet bir price set'i fiyatlarıyla döner
// (GET /store/v1/price-sets/{id}).
//
// Store tarafındaki TEK pricing uç noktasıdır. Müşteriye giden fiyat normalde
// product'ın store listelemesinden, Query katmanı üzerinden gelir (ADR 0004);
// bu uç nokta, kimliği zaten bilinen bir kabın fiyatlarını doğrudan okumak
// isteyen istemciler içindir.
//
// Yazma yüzeyi store tarafında YOKTUR: fiyat değiştirmek yönetim işidir.
//
// Gövde YALNIZCA gösterilebilir fiyatları taşır ve kural koşullarını İÇERMEZ;
// yönetim yüzeyindeki karşılığı (GET /admin/v1/price-sets/{id}) ikisini de gösterir.
func (a *API) storeGetPriceSet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := pathID(r, "id")

	set, err := a.svc.GetPriceSet(ctx, id)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	// Müşteri yüzeyi SÜZGEÇLİ yolu kullanır: taslak/süresi geçmiş kampanya
	// fiyatları ve kurala bağlı fiyatlar dışarı çıkmaz (bkz. ListStorePrices).
	prices, err := a.svc.ListStorePrices(ctx, id)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toPriceSetDTO(set, prices))
}
