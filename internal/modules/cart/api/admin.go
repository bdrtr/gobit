package api

import (
	"net/http"
	"strconv"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// The admin side is READ ONLY.
//
// The only party that changes the cart is the customer; a correction made from
// the admin panel would mean changing the amount the customer saw behind their
// back. Order corrections are the job of the order module (Return/Exchange/
// Claim) in Phase 6.

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

	carts, count, err := h.svc.ListCarts(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]cartDTO, 0, len(carts))
	// The loop is walked by index: the cart struct is large and copying it by
	// value would carry a few hundred bytes needlessly on every turn.
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
