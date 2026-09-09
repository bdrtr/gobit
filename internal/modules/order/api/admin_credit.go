package api

import (
	"net/http"
	"time"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// createCreditLineRequest is the body of a write-off.
type createCreditLineRequest struct {
	// Amount is the credited amount (minor unit); it has to be positive.
	Amount int64 `json:"amount"`
	// Reason is the merchant's short word for why; it is REQUIRED.
	Reason string `json:"reason"`
	// Note is free-form detail; it may be left out.
	Note string `json:"note"`
}

// creditLineDTO is the external representation of a credit line.
type creditLineDTO struct {
	ID        string    `json:"id"`
	OrderID   string    `json:"order_id"`
	Amount    int64     `json:"amount"`
	Reason    string    `json:"reason"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// toCreditLineDTO converts the model to the external representation.
func toCreditLineDTO(credit models.OrderCreditLine) creditLineDTO {
	return creditLineDTO{
		ID:        credit.ID,
		OrderID:   credit.OrderID,
		Amount:    credit.Amount,
		Reason:    credit.Reason,
		Note:      credit.Note,
		CreatedAt: credit.CreatedAt,
		UpdatedAt: credit.UpdatedAt,
	}
}

// adminCreateCreditLine writes off part of what the order owes.
//
// The order's TOTAL does not move. What changes is the summary's outstanding
// amount, which is where a client should look for the effect: the response is
// the credit that was written, not a restated order.
func (h *Handler) adminCreateCreditLine(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createCreditLineRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	credit, err := h.svc.CreateCreditLine(ctx, orderID(r), service.CreateCreditLineInput{
		Amount: body.Amount,
		Reason: body.Reason,
		Note:   body.Note,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toCreditLineDTO(credit)})
}

// adminListCreditLines returns the order's credit lines, oldest first.
//
// The single envelope with an ARRAY in it, not the paged one: the credits belong
// to one order and there is no page to ask for, which is the shape the timeline
// takes for the same reason (D44).
func (h *Handler) adminListCreditLines(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	credits, err := h.svc.ListCreditLines(ctx, orderID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	out := make([]creditLineDTO, 0, len(credits))
	for i := range credits {
		out = append(out, toCreditLineDTO(credits[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: out})
}
