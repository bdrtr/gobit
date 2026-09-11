package api

import (
	"net/http"
	"strconv"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// This file is the admin side's READS. Its writes are in admin_write.go, and
// there are two of them (ADR 0146).
//
// What is not there is the whole rule: a cart the shopper is holding cannot be
// CHANGED from the panel, because that would alter the amount they are looking
// at behind their back. Opening a cart and adding a priced line cannot produce
// that outcome — an opened cart is nobody's yet, and the money is taken on the
// storefront with the totals in front of the shopper. Order corrections are the
// job of the order module (Return/Exchange/Claim).

// adminListCarts returns the carts in pages.
//
// Supported filters: customer_id, region_id and completed. The rows are NOT
// LOADED; fetching the children of dozens of carts per page would open the list
// up to N+1. The detail of a single cart is taken with /admin/v1/carts/{id}.
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
				"completed has to be a boolean value: %q", raw))
			return
		}
		in.Completed = &flag
	}

	result, err := h.svc.ListCarts(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]cartDTO, 0, len(result.Items))
	// The loop is walked by index: the cart struct is large and copying it by
	// value would carry a few hundred bytes needlessly on every turn.
	for i := range result.Items {
		data = append(data, toCartDTO(result.Items[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:       data,
		Count:      result.Count,
		NextCursor: result.NextCursor,
		Offset:     page.Offset,
		Limit:      page.Limit,
	})
}

// adminGetCart returns the cart with its children.
func (h *Handler) adminGetCart(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	detail, err := h.svc.GetCart(ctx, cartID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toCartDetailDTO(detail)})
}
