package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// The storefront's list of a signed-in customer's own orders (ADR 0367).
//
// Whose orders are listed is the path's customer, and the request has to PROVE
// it (corehttp.ProvenCustomer), as the customer's addresses and balances do:
// with no customer identity bound the route refuses, because a list of one
// person's orders has no correct anonymous reader. The records are the order
// records GET /store/v1/orders/{id} would answer each of, without their lines,
// newest first.

// pathStoreOwnOrders lists the proven customer's orders.
const pathStoreOwnOrders = "/store/v1/customers/{id}/orders"

// WithIdentity gives the handler the customer identity the storefront's own
// orders route asks; without one that route refuses.
//
// It is a separate step rather than a parameter of [New] because only that
// route asks it, and every other test of this package builds a handler that
// never will.
func (h *Handler) WithIdentity(identity corehttp.Identity) *Handler {
	h.identity = identity
	return h
}

// storeListOwnOrders returns a page of the proven customer's orders
// (GET /store/v1/customers/{id}/orders).
func (h *Handler) storeListOwnOrders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customerID, err := corehttp.ProvenCustomer(h.identity, r, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	result, err := h.svc.ListOrders(ctx, service.ListOrdersInput{CustomerID: &customerID, Page: page})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]orderDTO, 0, len(result.Items))
	for i := range result.Items {
		data = append(data, toOrderDTO(result.Items[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:       data,
		Count:      result.Count,
		Offset:     page.Offset,
		Limit:      page.Limit,
		NextCursor: result.NextCursor,
	})
}
