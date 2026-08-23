package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// createCartRequest POST /store/v1/carts gövdesidir.
type createCartRequest struct {
	// RegionID zorunludur.
	RegionID string `json:"region_id"`
	// CustomerID boş bırakılırsa sepet misafirindir.
	CustomerID string `json:"customer_id"`
	Email      string `json:"email"`
	// CurrencyCode zorunludur; bölgenin para birimidir ve çağıran onu bölgeden
	// kopyalar (cart modülü region modülünü çağırmaz, bkz. ADR 0006).
	CurrencyCode string         `json:"currency_code"`
	Metadata     map[string]any `json:"metadata"`
}

// storeCreateCart yeni bir sepet oluşturur.
func (h *Handler) storeCreateCart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createCartRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	cart, err := h.svc.CreateCart(ctx, service.CreateCartInput{
		RegionID:     body.RegionID,
		CustomerID:   body.CustomerID,
		Email:        body.Email,
		CurrencyCode: body.CurrencyCode,
		Metadata:     body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toCartDTO(cart)})
}

// storeGetCart sepeti çocuklarıyla döner.
func (h *Handler) storeGetCart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	detail, err := h.svc.GetCart(ctx, cartID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toCartDetailDTO(detail)})
}

// updateCartRequest POST /store/v1/carts/{id} gövdesidir.
type updateCartRequest struct {
	// Email işaretçidir: gövdede HİÇ gönderilmemiş e-posta ile boşaltılmak
	// istenen e-posta ayrı niyetlerdir. İkisi tek boş dizeye indirgenseydi,
	// yalnızca müşteri devretmek isteyen her istek sepetin e-postasını
	// sessizce silerdi.
	Email *string `json:"email"`
	// CustomerID misafir sepeti devralacak müşteridir; boş bırakılırsa sepetin
	// müşterisine dokunulmaz.
	CustomerID string `json:"customer_id"`
}

// storeUpdateCart sepetin e-postasını ve/veya müşterisini günceller.
//
// Ödeme adımında e-posta toplamak ve misafir sepeti giriş yapan müşteriye
// devretmek için vardır. Uç PATCH değil POST'tur: chi yönlendirmesi zaten
// gövdeye göre dallanmaz ve müşteri tarafındaki diğer yazmalar da POST'tur.
func (h *Handler) storeUpdateCart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body updateCartRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	cart, err := h.svc.UpdateCart(ctx, cartID(r), service.UpdateCartInput{
		Email:      body.Email,
		CustomerID: body.CustomerID,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toCartDTO(cart)})
}

// storeDeleteCart sepeti yumuşak siler.
func (h *Handler) storeDeleteCart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteCart(ctx, cartID(r)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// addLineItemRequest POST /store/v1/carts/{id}/line-items gövdesidir.
type addLineItemRequest struct {
	VariantID string `json:"variant_id"`
	Title     string `json:"title"`
	// Quantity işaretçidir: gönderilmeyen adet ile sıfır adet birbirinden
	// ayrılsın diye. Sıfır adet zaten geçersizdir, ama iki durum FARKLI
	// mesajlar hak eder.
	Quantity *int64 `json:"quantity"`
	// UnitPrice opsiyoneldir; nihai fiyatı calculate_totals workflow'u yazar.
	UnitPrice int64          `json:"unit_price"`
	Metadata  map[string]any `json:"metadata"`
}

// storeAddLineItem sepete satır ekler.
func (h *Handler) storeAddLineItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body addLineItemRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if body.Quantity == nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest, "quantity zorunludur"))
		return
	}

	item, err := h.svc.AddLineItem(ctx, cartID(r), service.AddLineItemInput{
		VariantID: body.VariantID,
		Title:     body.Title,
		Quantity:  *body.Quantity,
		UnitPrice: body.UnitPrice,
		Metadata:  body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toLineItemDTO(item)})
}

// updateLineItemRequest satır güncelleme gövdesidir.
type updateLineItemRequest struct {
	// Quantity işaretçidir; bkz. [addLineItemRequest].
	Quantity *int64 `json:"quantity"`
}

// storeUpdateLineItem satırın adedini yazar.
func (h *Handler) storeUpdateLineItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body updateLineItemRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if body.Quantity == nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest, "quantity zorunludur"))
		return
	}

	item, err := h.svc.UpdateLineItemQuantity(ctx,
		cartID(r), chi.URLParam(r, paramLineItemID), *body.Quantity)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toLineItemDTO(item)})
}

// storeRemoveLineItem satırı sepetten kaldırır.
func (h *Handler) storeRemoveLineItem(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.RemoveLineItem(ctx, cartID(r), chi.URLParam(r, paramLineItemID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// storeSetShippingAddress sepetin kargo adresini yazar.
func (h *Handler) storeSetShippingAddress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body addressRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	addr, err := h.svc.SetShippingAddress(ctx, cartID(r), body.toInput())
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toAddressDTO(addr)})
}

// storeSetBillingAddress sepetin fatura adresini yazar.
func (h *Handler) storeSetBillingAddress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body addressRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	addr, err := h.svc.SetBillingAddress(ctx, cartID(r), body.toInput())
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toAddressDTO(addr)})
}

// addShippingMethodRequest kargo yöntemi ekleme gövdesidir.
type addShippingMethodRequest struct {
	Name             string         `json:"name"`
	ShippingOptionID string         `json:"shipping_option_id"`
	Amount           int64          `json:"amount"`
	Data             map[string]any `json:"data"`
}

// storeAddShippingMethod sepete kargo yöntemi ekler.
func (h *Handler) storeAddShippingMethod(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body addShippingMethodRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	method, err := h.svc.AddShippingMethod(ctx, cartID(r), service.AddShippingMethodInput{
		Name:             body.Name,
		ShippingOptionID: body.ShippingOptionID,
		Amount:           body.Amount,
		Data:             body.Data,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toShippingMethodDTO(method)})
}

// storeRemoveShippingMethod kargo yöntemini sepetten kaldırır.
func (h *Handler) storeRemoveShippingMethod(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.RemoveShippingMethod(ctx, cartID(r), chi.URLParam(r, paramMethodID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}
