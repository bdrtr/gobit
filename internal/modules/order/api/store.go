package api

import (
	"net/http"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// The customer side reads, and ASKS.
//
// It reads its order and it can open a RETURN REQUEST — a record that moves no
// stock and no money until an operator receives it. Everything that acts is
// admin-only and scoped.
//
// A cancellation request is deliberately still absent: canceling reaches money
// and stock, and a paid order cannot be canceled at all (the return path is the
// way back). Opening a status transition to a client would mean that an order
// whose payment
// had already been captured could be closed by the customer.

// storeGetOrder returns the customer's order with its line items and summary.
//
// # Authorization
//
// Verifying that the order belongs to the REQUESTING customer IS NOT DONE
// HERE, and no phase of this framework is going to do it: ADR 0008 settles the
// responsibility on the EMBEDDING APPLICATION. The auth module that arrived in
// Phase 8 is ADMIN identity — its own package doc says the user there is not
// the person shopping. The endpoint is open to anyone who knows the order id,
// and it stays that way until whoever embeds gobit puts a session in front of
// it.
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

// storeReturnRequest is the body of a customer's return request.
//
// It names the LINES and nothing about money: what a return is worth is the
// shop's to decide after seeing what comes back, and a customer-supplied refund
// amount would be exactly the defect the cart's shipping price had — a number
// from the request reaching a total.
type storeReturnRequest struct {
	// Lines are the order lines the customer wants to send back.
	Lines []storeReturnLine `json:"lines"`
	// Reason is why; it is optional and free text.
	Reason string `json:"reason"`
}

// storeReturnLine is one line the customer wants to send back.
type storeReturnLine struct {
	OrderLineItemID string `json:"order_line_item_id"`
	Quantity        int64  `json:"quantity"`
}

// storeRequestReturn opens a return request for the customer's order.
//
// # Authorization
//
// The same boundary [Handler.storeGetOrder] declares, and for the same reason:
// verifying that the order belongs to the requesting customer is the EMBEDDING
// APPLICATION's job (ADR 0008). Anyone who knows the order id can open a return
// request against it.
//
// What that costs here is bounded and worth stating: a request is a REQUEST. It
// moves no stock and no money, an operator has to receive it before anything
// happens, and the quantity rule already refuses more than was bought. The
// endpoint that acts — receiving, refunding — is admin-only and scoped.
//
// # The customer names lines, not amounts
//
// The refund figure is left at zero for the shop to fill in. A body that could
// name what the return is worth would let a customer decide their own refund,
// which is the shipping-price defect in another place.
func (h *Handler) storeRequestReturn(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body storeReturnRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	if len(body.Lines) == 0 {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
			"a return request has to name at least one line"))

		return
	}

	lines := make([]service.ReturnLineInput, 0, len(body.Lines))
	for i := range body.Lines {
		lines = append(lines, service.ReturnLineInput{
			OrderLineItemID: body.Lines[i].OrderLineItemID,
			Quantity:        body.Lines[i].Quantity,
		})
	}

	created, err := h.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID: orderID(r),
		Reason:  body.Reason,
		Lines:   lines,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toReturnDTO(created)})
}
