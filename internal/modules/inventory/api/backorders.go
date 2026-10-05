package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// paramStatus narrows the backorder queue to one status.
const paramStatus = "status"

// backorderDTO is one claim as it goes over the wire (ADR 0392).
type backorderDTO struct {
	// ID is the claim's identifier.
	ID string `json:"id"`
	// InventoryItemID is the item the order line is owed.
	InventoryItemID string `json:"inventory_item_id"`
	// OrderID and OrderLineItemID are the order's line the claim was recorded
	// for.
	OrderID         string `json:"order_id"`
	OrderLineItemID string `json:"order_line_item_id"`
	// Quantity is the units the line was let through without.
	Quantity int64 `json:"quantity"`
	// WithdrawnQuantity is how many of them a write-off took back before they
	// arrived.
	WithdrawnQuantity int64 `json:"withdrawn_quantity"`
	// LocationIDs are the warehouses the claim may be filled at, in the order
	// they were ranked when the order was placed; empty when none could be.
	LocationIDs []string `json:"location_ids"`
	// Status is waiting, filled or withdrawn.
	Status string `json:"status"`
	// ReservationID is the reservation the fill confirmed; present once the
	// claim is filled.
	ReservationID string `json:"reservation_id,omitempty"`
	// FilledLocationID is the warehouse the fill deducted at.
	FilledLocationID string `json:"filled_location_id,omitempty"`
	// Seq is the claim's place in the queue.
	Seq       int64     `json:"seq"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// toBackorderDTO converts the model to its external representation.
func toBackorderDTO(b models.Backorder) backorderDTO {
	locations := b.LocationIDs
	if locations == nil {
		locations = []string{}
	}

	return backorderDTO{
		ID:                b.ID,
		InventoryItemID:   b.InventoryItemID,
		OrderID:           b.OrderID,
		OrderLineItemID:   b.OrderLineItemID,
		Quantity:          b.Quantity,
		WithdrawnQuantity: b.WithdrawnQuantity,
		LocationIDs:       locations,
		Status:            b.Status.String(),
		ReservationID:     b.ReservationID,
		FilledLocationID:  b.FilledLocationID,
		Seq:               b.Seq,
		CreatedAt:         b.CreatedAt,
		UpdatedAt:         b.UpdatedAt,
	}
}

// listBackorders returns a page of the orders waiting for the item's units.
func (h *Handler) listBackorders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	claims, count, err := h.svc.ListBackorders(ctx, service.ListBackordersInput{
		InventoryItemID: chi.URLParam(r, "id"),
		Status:          r.URL.Query().Get(paramStatus),
		Page:            page,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]backorderDTO, 0, len(claims))
	for i := range claims {
		data = append(data, toBackorderDTO(claims[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   data,
		Count:  count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}
