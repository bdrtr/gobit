package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// adminListOrders siparişleri sayfalayarak döner.
//
// Desteklenen süzgeçler: customer_id, region_id ve status. Satırlar YÜKLENMEZ;
// sayfa başına onlarca siparişin çocuklarını getirmek listeyi N+1'e açardı. Tek
// siparişin ayrıntısı /admin/v1/orders/{id} ile alınır.
func (h *Handler) adminListOrders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	in := service.ListOrdersInput{Page: page}
	if raw := r.URL.Query().Get("customer_id"); raw != "" {
		in.CustomerID = &raw
	}
	if raw := r.URL.Query().Get("region_id"); raw != "" {
		in.RegionID = &raw
	}
	if raw := r.URL.Query().Get("status"); raw != "" {
		status := models.OrderStatus(raw)
		in.Status = &status
	}

	orders, count, err := h.svc.ListOrders(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]orderDTO, 0, len(orders))
	// Döngü indeksle gezilir: sipariş yapısı büyüktür ve değerle kopyalamak her
	// tur birkaç yüz baytı boşuna taşır.
	for i := range orders {
		data = append(data, toOrderDTO(orders[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   data,
		Count:  count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// adminGetOrder siparişi satırları ve özetiyle döner.
func (h *Handler) adminGetOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	detail, err := h.svc.GetOrder(ctx, orderID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toOrderDetailDTO(detail)})
}

// cancelOrderRequest POST /admin/v1/orders/{id}/cancel gövdesidir.
type cancelOrderRequest struct {
	// Reason iptal gerekçesidir; opsiyoneldir.
	Reason string `json:"reason"`
}

// adminCancelOrder siparişi iptal eder ve güncel hâlini döner.
//
// Çağrı İDEMPOTENTTİR: zaten iptal edilmiş bir sipariş hata değil, mevcut
// (iptal edilmiş) hâliyle 200 döner. Gerekçe için bkz.
// [service.Service.CancelOrder]. Tamamlanmış siparişte 409 döner.
func (h *Handler) adminCancelOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body cancelOrderRequest
	if err := decodeOptionalBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	if err := h.svc.CancelOrder(ctx, orderID(r), body.Reason); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	h.writeCurrentOrder(w, r)
}

// adminCompleteOrder siparişi tamamlar ve güncel hâlini döner.
func (h *Handler) adminCompleteOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if _, err := h.svc.CompleteOrder(ctx, orderID(r)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	h.writeCurrentOrder(w, r)
}

// adminArchiveOrder tamamlanmış siparişi arşivler ve güncel hâlini döner.
func (h *Handler) adminArchiveOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if _, err := h.svc.ArchiveOrder(ctx, orderID(r)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	h.writeCurrentOrder(w, r)
}

// writeCurrentOrder durum geçişinden sonra siparişin GÜNCEL ayrıntısını yazar.
//
// Geçiş metotlarının döndürdüğü [models.Order] yerine yeniden okunmasının iki
// sebebi var: [service.Service.CancelOrder] idempotent olduğu için hiçbir şey
// döndürmez (ikinci çağrıda yazma yapılmaz), ve yanıt zarfı her üç uçta da
// AYNI olmalıdır — satırlar ve özet dâhil. Ek okuma yalnızca yönetim tarafının
// seyrek kullanılan uçlarındadır.
func (h *Handler) writeCurrentOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	detail, err := h.svc.GetOrder(ctx, orderID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toOrderDetailDTO(detail)})
}

// createReturnRequest POST /admin/v1/orders/{id}/returns gövdesidir.
type createReturnRequest struct {
	RefundAmount int64          `json:"refund_amount"`
	Reason       string         `json:"reason"`
	Note         string         `json:"note"`
	Metadata     map[string]any `json:"metadata"`
}

// adminCreateReturn siparişe iade kaydı açar.
func (h *Handler) adminCreateReturn(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createReturnRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	ret, err := h.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID:      orderID(r),
		RefundAmount: body.RefundAmount,
		Reason:       body.Reason,
		Note:         body.Note,
		Metadata:     body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toReturnDTO(ret)})
}

// adminGetReturn iade kaydını kimliğiyle döner.
//
// Uç siparişin ALTINDA durur ({id}/returns/{returnId}) çünkü kaynak siparişe
// aittir; kaydın kimliği zaten benzersizdir ve okuma yalnızca onunla yapılır.
// Yoldaki sipariş kimliği kaydın gerçekten o siparişe ait olduğunun kontrolü
// DEĞİLDİR — böyle bir kontrol Faz 8'in (auth) yetki denetimiyle birlikte
// anlam kazanır ve bugün eklenmesi, yetki varmış izlenimi verirdi.
func (h *Handler) adminGetReturn(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ret, err := h.svc.GetReturn(ctx, chi.URLParam(r, paramReturnID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toReturnDTO(ret)})
}

// adminListReturns siparişin iade kayıtlarını sayfalayarak döner.
func (h *Handler) adminListReturns(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	items, count, err := h.svc.ListReturns(ctx, orderID(r), page)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]returnDTO, 0, len(items))
	for i := range items {
		data = append(data, toReturnDTO(items[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data: data, Count: count, Offset: page.Offset, Limit: page.Limit,
	})
}

// createExchangeRequest POST /admin/v1/orders/{id}/exchanges gövdesidir.
type createExchangeRequest struct {
	// DifferenceDue pozitifse fark müşteriden tahsil edilir, negatifse
	// müşteriye ödenir.
	DifferenceDue int64          `json:"difference_due"`
	Note          string         `json:"note"`
	Metadata      map[string]any `json:"metadata"`
}

// adminCreateExchange siparişe değişim kaydı açar.
func (h *Handler) adminCreateExchange(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createExchangeRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	exchange, err := h.svc.CreateExchange(ctx, service.CreateExchangeInput{
		OrderID:       orderID(r),
		DifferenceDue: body.DifferenceDue,
		Note:          body.Note,
		Metadata:      body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toExchangeDTO(exchange)})
}

// adminGetExchange değişim kaydını kimliğiyle döner.
//
// Yol ve kimlik sözleşmesi için bkz. [Handler.adminGetReturn].
func (h *Handler) adminGetExchange(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	exchange, err := h.svc.GetExchange(ctx, chi.URLParam(r, paramExchangeID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toExchangeDTO(exchange)})
}

// adminListExchanges siparişin değişim kayıtlarını sayfalayarak döner.
func (h *Handler) adminListExchanges(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	items, count, err := h.svc.ListExchanges(ctx, orderID(r), page)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]exchangeDTO, 0, len(items))
	for i := range items {
		data = append(data, toExchangeDTO(items[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data: data, Count: count, Offset: page.Offset, Limit: page.Limit,
	})
}

// createClaimRequest POST /admin/v1/orders/{id}/claims gövdesidir.
type createClaimRequest struct {
	// Type "refund" ya da "replace" olmalıdır.
	Type         string         `json:"type"`
	RefundAmount int64          `json:"refund_amount"`
	Reason       string         `json:"reason"`
	Note         string         `json:"note"`
	Metadata     map[string]any `json:"metadata"`
}

// adminCreateClaim siparişe hasar/eksik kaydı açar.
//
// Tür boş bırakılamaz: varsayılan bir tür seçmek (örn. "refund"), talebin nasıl
// karşılanacağına istemci adına karar vermek olurdu.
func (h *Handler) adminCreateClaim(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createClaimRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if body.Type == "" {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
			"type boş olamaz: %q ya da %q olmalı", models.ClaimRefund, models.ClaimReplace))
		return
	}

	claim, err := h.svc.CreateClaim(ctx, service.CreateClaimInput{
		OrderID:      orderID(r),
		Type:         models.ClaimType(body.Type),
		RefundAmount: body.RefundAmount,
		Reason:       body.Reason,
		Note:         body.Note,
		Metadata:     body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toClaimDTO(claim)})
}

// adminGetClaim hasar kaydını kimliğiyle döner.
//
// Yol ve kimlik sözleşmesi için bkz. [Handler.adminGetReturn].
func (h *Handler) adminGetClaim(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claim, err := h.svc.GetClaim(ctx, chi.URLParam(r, paramClaimID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toClaimDTO(claim)})
}

// adminListClaims siparişin hasar kayıtlarını sayfalayarak döner.
func (h *Handler) adminListClaims(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	items, count, err := h.svc.ListClaims(ctx, orderID(r), page)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]claimDTO, 0, len(items))
	for i := range items {
		data = append(data, toClaimDTO(items[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data: data, Count: count, Offset: page.Offset, Limit: page.Limit,
	})
}
