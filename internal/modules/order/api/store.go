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
//
// # Who may ask (ADR 0439)
//
// The three routes under /store/v1/orders/{id} answer a request that presents
// one of the order's access tokens in [corehttp.OrderTokenHeader], or that the
// installation's bound identity proves to be the order's customer. The id
// alone opens nothing: gobit hands it to every webhook receiver of an order
// topic, so it names the order without being its key. Every refusal is the
// 404 an unknown id gets, so a caller holding an id learns neither that the
// order exists nor whether it has a customer; an identity that could not reach
// a verdict answers its own class, because a proof that could not be read is
// not a proof of nobody (ADR 0371).

// storeOrderAccess decides whether the request may reach the order its path
// names, and returns the error that ends it when it may not.
//
// The token is asked first: it is what a guest holds, and it needs no
// identity. Then the identity, and only an order that has a customer opens to
// it — a guest's order has none to prove, and an identity proving nobody must
// not match the empty customer.
//
// The identity is asked BEFORE the order's existence is told, for every
// request the token did not open, so an identity that cannot reach a verdict
// answers its own class for an unknown id as for a real one. Asking it only
// for a real order told the two apart during an outage: 503 for an order, 404
// for no order. A guest's order costs an identity call it cannot use; that is
// the price of the answer saying nothing about the id.
func (h *Handler) storeOrderAccess(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := orderID(r)
	privateToTheCaller(w)

	access, err := h.svc.StorefrontAccess(ctx, id, r.Header.Get(corehttp.OrderTokenHeader))
	unknown := coreerrors.IsNotFound(err)
	if err != nil && !unknown {
		return err
	}
	if !unknown && access.TokenMatches {
		return nil
	}

	proven, err := corehttp.ProvenCustomerIfAny(h.identity, r)
	if err != nil {
		return err
	}
	if !unknown && access.CustomerID != "" && proven == access.CustomerID {
		return nil
	}

	return storeOrderNotFound(id)
}

// privateToTheCaller marks an answer as the caller's alone (ADR 0439).
//
// What it holds was opened by a header or a session, so a shared cache keyed on
// the URL — one placed in front of the storefront API, as the catalog's own
// caching invites — must neither keep it nor hand it to the next caller asking
// the same URL. A refusal is marked too: a cached 404 would close the order to
// its owner. Vary names the credentials a cache would have to key on if it
// ignored no-store: the token's header, and the cookie and Authorization header
// an identity commonly reads.
func privateToTheCaller(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Add("Vary", corehttp.OrderTokenHeader+", Cookie, Authorization")
}

// storeOrderNotFound is the refusal of a request that may not reach the order:
// word for word what an unknown id is answered.
func storeOrderNotFound(id string) error {
	return coreerrors.NotFound(service.CodeOrderNotFound, "order not found: %s", id)
}

// storeGetOrder returns the customer's order with its line items and summary,
// as [storeOrderDetailDTO]: no address, no cart id and no customer id.
func (h *Handler) storeGetOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.storeOrderAccess(w, r); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	detail, err := h.svc.GetOrder(ctx, orderID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toStoreOrderDetailDTO(detail)})
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
// [Handler.storeOrderAccess], before the body is read. A request moves no
// stock and no money, but it holds the units it names: the quantity rule
// counts them against what was bought, so a stranger's request refused the
// owner's own until an operator withdrew it (ADR 0439).
//
// # The customer names lines, not amounts
//
// The refund figure is left at zero for the shop to fill in. A body that could
// name what the return is worth would let a customer decide their own refund,
// which is the shipping-price defect in another place.
func (h *Handler) storeRequestReturn(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.storeOrderAccess(w, r); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

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

	created, err := h.svc.CreateReturnRecord(ctx, service.CreateReturnInput{
		OrderID: orderID(r),
		Reason:  body.Reason,
		Lines:   lines,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toReturnDTO(&created)})
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
	// Quantity is the units a goods entry moved; set on a line cancellation.
	// It is a count of goods, not a figure of money.
	Quantity int64 `json:"quantity,omitempty"`
}

// storeGetOrderTimeline returns what happened to the order, as the customer may
// see it.
//
// # Authorization
//
// [Handler.storeOrderAccess]. The timeline carries no address, no e-mail, no
// line prices and no money moment, and it does carry each parcel's tracking
// number, which the parcel's webhook leaves out (ADR 0437): a lookup key to
// the recipient, for the order's shopper alone.
func (h *Handler) storeGetOrderTimeline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.storeOrderAccess(w, r); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	entries, err := h.svc.StorefrontTimeline(ctx, orderID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	out := make([]storeTimelineEntryDTO, 0, len(entries))
	for _, entry := range entries {
		out = append(out, storeTimelineEntryDTO{
			At:       entry.At,
			Kind:     entry.Kind,
			RefID:    entry.RefID,
			Clock:    entry.Clock,
			Detail:   entry.Detail,
			Quantity: entry.Quantity,
		})
	}

	// The single envelope, for the reason the admin timeline gives: a timeline
	// is bounded by its order and there is no page to ask for.
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: out})
}
