package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// storeGetPromotion bir kupon kodunu doğrular
// (GET /store/v1/promotions/{code}).
//
// Store tarafındaki TEK promotion uç noktasıdır ve YALNIZCA okur; kupon
// kullanımı (sayaç artırma) yönetim ve sipariş akışının işidir.
//
// # Ne DÖNER
//
// Kuponun kodu ve indirimin türü/hedefi/değeri. Bu kadarı zorunludur: müşteri,
// yazdığı kodun ne yaptığını görmeden sepetine uygulayamaz.
//
// # Ne DÖNMEZ
//
// Promosyonun DURUMU, kullanım sayacı, kampanya kimliği ve bütçesi, üstverisi
// ve KURAL KOŞULLARI. Bir kuralın sağ tarafı (örn. bir müşteri grubunun
// kimliği ya da bir segment listesi) iş bilgisidir.
//
// # Neden sebep söylenmez
//
// Kod yoksa, promosyon taslak/pasif ise, kampanyasının penceresi kapalıysa,
// bütçesi tükenmişse ya da kullanım hakkı bittiyse AYNI 404 döner. Ayrım
// yapılsaydı, kod tahmin eden biri "bu kod var ama kampanyası henüz
// başlamadı" cevabından yayınlanmamış bir kampanya takvimi çıkarabilirdi.
//
// Kuponun bu SEPETTE gerçekten indirim üretip üretmediği ayrı bir sorudur ve
// cevabı sepet toplamının kendisidir: kural koşulları ancak sepet bağlamıyla
// değerlendirilebilir ve burada değerlendirilmeleri, koşulun VARLIĞINI ele
// verirdi.
func (a *API) storeGetPromotion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	coupon, err := a.svc.LookupStoreCoupon(ctx, pathID(r, "code"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toStoreCouponDTO(coupon))
}
