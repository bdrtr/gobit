package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// The customer side ONLY READS.
//
// The only parties that change an order are administration and the workflows;
// the operations a customer can perform on an order (cancellation request,
// return request) arrive with their own workflows in later phases. Opening a
// status transition to a client today would mean that an order whose payment
// had already been captured could be closed by the customer.

// storeGetOrder returns the customer's order with its line items and summary.
//
// # Authorization
//
// Verifying that the order belongs to the REQUESTING customer IS NOT DONE
// HERE; the auth module and the real middleware are Phase 8's work (plan
// Phase 8). Until that arrives the endpoint is open to anyone who knows the
// order id.
//
// The gap is not being hidden, it is being DECLARED: because the id itself is
// unguessable (a 26-character random body) this is not a "public listing", but
// unguessability is no substitute for authorization. This is why there is no
// LIST endpoint on the customer side — a list endpoint would turn knowing a
// single id into reading every order.
func (h *Handler) storeGetOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	detail, err := h.svc.GetOrder(ctx, orderID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toOrderDetailDTO(detail)})
}
