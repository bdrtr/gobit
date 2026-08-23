package api

import (
	"net/http"
	"strconv"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// Yönetim tarafı YALNIZCA OKUR.
//
// Sepeti değiştiren tek taraf müşteridir; yönetim panelinden yapılan bir
// düzeltme, müşterinin gördüğü tutarı arkasından değiştirmek olurdu. Sipariş
// düzeltmeleri Faz 6'daki order modülünün (Return/Exchange/Claim) işidir.

// adminListCarts sepetleri sayfalayarak döner.
//
// Desteklenen süzgeçler: customer_id, region_id ve completed. Satırlar
// YÜKLENMEZ; sayfa başına onlarca sepetin çocuklarını getirmek listeyi N+1'e
// açardı. Tek sepetin ayrıntısı /admin/v1/carts/{id} ile alınır.
func (h *Handler) adminListCarts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	in := service.ListCartsInput{Page: page}
	if raw := r.URL.Query().Get("customer_id"); raw != "" {
		in.CustomerID = &raw
	}
	if raw := r.URL.Query().Get("region_id"); raw != "" {
		in.RegionID = &raw
	}
	if raw := r.URL.Query().Get("completed"); raw != "" {
		flag, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
				"completed mantıksal bir değer olmalı: %q", raw))
			return
		}
		in.Completed = &flag
	}

	carts, count, err := h.svc.ListCarts(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]cartDTO, 0, len(carts))
	// Döngü indeksle gezilir: sepet yapısı büyüktür ve değerle kopyalamak her
	// tur birkaç yüz baytı boşuna taşır.
	for i := range carts {
		data = append(data, toCartDTO(carts[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   data,
		Count:  count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// adminGetCart sepeti çocuklarıyla döner.
func (h *Handler) adminGetCart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	detail, err := h.svc.GetCart(ctx, cartID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toCartDetailDTO(detail)})
}
