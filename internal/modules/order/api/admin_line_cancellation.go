package api

import (
	"net/http"
	"time"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// cancelOrderLineRequest is the body of a partial cancellation.
type cancelOrderLineRequest struct {
	// OrderLineItemID is the line whose units will not be delivered.
	OrderLineItemID string `json:"order_line_item_id"`
	// Quantity is how many units are written off; it has to be positive.
	Quantity int64 `json:"quantity"`
	// Reason is the merchant's short word for why; it is REQUIRED.
	Reason string `json:"reason"`
	// Note is free-form detail; it may be left out.
	Note string `json:"note"`
}

// lineCancellationDTO is the external representation of a line cancellation.
type lineCancellationDTO struct {
	ID              string    `json:"id"`
	OrderLineItemID string    `json:"order_line_item_id"`
	Quantity        int64     `json:"quantity"`
	Reason          string    `json:"reason"`
	Note            string    `json:"note,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// toLineCancellationDTO converts the model to the external representation.
func toLineCancellationDTO(cancellation models.OrderLineCancellation) lineCancellationDTO {
	return lineCancellationDTO{
		ID:              cancellation.ID,
		OrderLineItemID: cancellation.OrderLineItemID,
		Quantity:        cancellation.Quantity,
		Reason:          cancellation.Reason,
		Note:            cancellation.Note,
		CreatedAt:       cancellation.CreatedAt,
		UpdatedAt:       cancellation.UpdatedAt,
	}
}

// adminCancelOrderLine writes off units of one line of a live order.
//
// The order's total does not move and neither does its status: what a customer
// is owed for a unit they paid for and will not receive is a refund or a credit,
// and closing an order whose lines are all written off is a separate act.
func (h *Handler) adminCancelOrderLine(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body cancelOrderLineRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	cancellation, err := h.svc.CancelOrderLine(ctx, orderID(r), service.CancelOrderLineInput{
		OrderLineItemID: body.OrderLineItemID,
		Quantity:        body.Quantity,
		Reason:          body.Reason,
		Note:            body.Note,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated,
		singleEnvelope{Data: toLineCancellationDTO(cancellation)})
}

// adminListLineCancellations returns the order's line cancellations, oldest
// first.
//
// The single envelope with an ARRAY in it, not the paged one: the cancellations
// belong to one order and there is no page to ask for — the same shape the
// timeline and the credit lines take for the same reason (D44).
func (h *Handler) adminListLineCancellations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	cancellations, err := h.svc.ListLineCancellations(ctx, orderID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	out := make([]lineCancellationDTO, 0, len(cancellations))
	for i := range cancellations {
		out = append(out, toLineCancellationDTO(cancellations[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: out})
}
