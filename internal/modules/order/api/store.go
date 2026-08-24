package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// Müşteri tarafı YALNIZCA OKUR.
//
// Siparişi değiştiren tek taraf yönetim ve workflow'lardır; müşterinin sipariş
// üzerinde yapabileceği işlemler (iptal talebi, iade talebi) kendi iş
// akışlarıyla sonraki fazlarda gelir. Bugün bir istemciye durum geçişi açmak,
// ödemesi alınmış bir siparişin müşteri tarafından kapatılabilmesi demek olurdu.

// storeGetOrder müşterinin siparişini satırları ve özetiyle döner.
//
// # Yetkilendirme
//
// Siparişin İSTEĞİ YAPAN müşteriye ait olduğunun doğrulanması BURADA YAPILMAZ;
// auth modülü ve gerçek middleware Faz 8'in işidir (plan Faz 8). O gelene kadar
// uç, sipariş kimliğini bilen herkese açıktır.
//
// Eksiklik gizlenmiyor, BEYAN EDİLİYOR: kimliğin kendisi tahmin edilemez
// (26 karakterlik rastgele gövde) olduğu için bu bir "herkese açık liste"
// değildir, ama tahmin edilemezlik yetkilendirme yerine geçmez. Bu yüzden
// müşteri tarafında LİSTE ucu yoktur — bir liste ucu, tek bir kimliği bilmeyi
// tüm siparişleri okumaya çevirirdi.
func (h *Handler) storeGetOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	detail, err := h.svc.GetOrder(ctx, orderID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toOrderDetailDTO(detail)})
}
