package api

import (
	"net/http"
	"time"

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
//
// # Why the identity contract does not reach here
//
// ADR 0057 requires the embedder's proof wherever a storefront request NAMES a
// customer. This route names an ORDER. The claim it would have to compare does
// not exist in the request — the order's own customer is a fact of the record
// rather than something the caller asserts — so the check would have to become
// "the reader must be the order's customer", which is a different decision: it
// would close guest order lookup, and a guest order has no customer to prove.
// The route is outside the gate's population for that reason, not by omission.
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

// storeTimelineEntryDTO is one thing that happened, as a CUSTOMER may see it.
//
// # Why it is a second type and not the admin one with fields blanked
//
// It carries no Amount and no Currency. A type that cannot hold a figure cannot
// leak one: a later edit that starts copying the money fields across would not
// compile, where a blanking step would just stop being called. The money
// entries do not reach here anyway ([service.Service.StorefrontTimeline] filters them),
// and this makes the two halves of that decision fail together rather than
// separately.
type storeTimelineEntryDTO struct {
	// At is when it happened. It is NULL when the fact is real and its moment
	// was never recorded; those entries come LAST.
	At *time.Time `json:"at"`
	// Kind is what happened, as "<source>.<what>".
	Kind string `json:"kind"`
	// RefID is the record the moment belongs to: the order, the shipment, the
	// return, the claim or the exchange.
	RefID string `json:"ref_id"`
	// Clock says which clock stamped At; it is empty when At is null.
	//
	// It is published to the customer for the same reason it is published to the
	// support desk: the moments do NOT share one axis, and two entries a second
	// apart may be ordered by different clocks. Hiding that does not make the
	// order true, it makes it unexplainable.
	Clock string `json:"clock"`
	// Detail is a short extra: a status, a tracking number.
	Detail string `json:"detail,omitempty"`
}

// storeGetOrderTimeline returns what happened to the order, as the customer may
// see it.
//
// # Authorization
//
// The same boundary [Handler.storeGetOrder] declares, and for the same reason:
// knowing the order id is the capability, and verifying that the order belongs
// to the requesting customer is the EMBEDDING APPLICATION's job (ADR 0008).
// This route names an ORDER rather than a customer, so it is outside ADR 0057's
// population for the reason that endpoint's godoc sets out.
//
// What that boundary costs is smaller here than on the order read: the timeline
// carries no address, no e-mail and no line prices, and since it carries no
// money moments at all it says less about the order than the order does.
func (h *Handler) storeGetOrderTimeline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	entries, err := h.svc.StorefrontTimeline(ctx, orderID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	out := make([]storeTimelineEntryDTO, 0, len(entries))
	for _, entry := range entries {
		out = append(out, storeTimelineEntryDTO{
			At:     entry.At,
			Kind:   entry.Kind,
			RefID:  entry.RefID,
			Clock:  entry.Clock,
			Detail: entry.Detail,
		})
	}

	// The single envelope, for the reason the admin timeline gives: a timeline
	// is bounded by its order and there is no page to ask for.
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: out})
}
