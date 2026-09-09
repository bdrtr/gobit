package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// paramReplacementID is the replacement id in the path.
const paramReplacementID = "replacementId"

// replacementLineRequest is one line of a replacement request.
type replacementLineRequest struct {
	// OrderLineItemID is the order line being replaced.
	OrderLineItemID string `json:"order_line_item_id"`
	// Quantity is how many units of it are being sent.
	Quantity int64 `json:"quantity"`
}

// createReplacementRequest is the body that says what a claim will send.
type createReplacementRequest struct {
	// ShippingOptionID is HOW it will be sent. It is required.
	ShippingOptionID string `json:"shipping_option_id"`
	// LocationID is the stock location it will be sent FROM. It is required.
	LocationID string `json:"location_id"`
	// Note is a free-form note.
	Note string `json:"note"`
	// Lines are the order lines being replaced. At least one is required: a
	// replacement that does not say what to send is the state this record was
	// added to remove.
	Lines []replacementLineRequest `json:"lines"`
}

// replacementItemDTO is one line of a replacement in a response.
type replacementItemDTO struct {
	ID              string    `json:"id"`
	OrderLineItemID string    `json:"order_line_item_id"`
	Quantity        int64     `json:"quantity"`
	CreatedAt       time.Time `json:"created_at"`
}

// replacementDTO is a replacement in a response.
type replacementDTO struct {
	ID               string               `json:"id"`
	ClaimID          string               `json:"claim_id"`
	Status           string               `json:"status"`
	ShippingOptionID string               `json:"shipping_option_id"`
	LocationID       string               `json:"location_id"`
	Note             string               `json:"note,omitempty"`
	Items            []replacementItemDTO `json:"items,omitempty"`
	CanceledAt       *time.Time           `json:"canceled_at,omitempty"`
	CreatedAt        time.Time            `json:"created_at"`
	UpdatedAt        time.Time            `json:"updated_at"`
}

// adminCreateReplacement records what a claim will send.
//
// It sends nothing. The record is the half that was missing: until it existed
// nothing in the schema could say WHAT a claim of type "replace" was going to
// send, which is the first of the two reasons the settle endpoint refuses one.
func (h *Handler) adminCreateReplacement(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createReplacementRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	lines := make([]service.ReplacementLineInput, 0, len(body.Lines))
	for i := range body.Lines {
		lines = append(lines, service.ReplacementLineInput{
			OrderLineItemID: body.Lines[i].OrderLineItemID,
			Quantity:        body.Lines[i].Quantity,
		})
	}

	record, err := h.svc.CreateReplacement(ctx, service.CreateReplacementInput{
		ClaimID:          chi.URLParam(r, paramClaimID),
		ShippingOptionID: body.ShippingOptionID,
		LocationID:       body.LocationID,
		Note:             body.Note,
		Lines:            lines,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated,
		singleEnvelope{Data: toReplacementDTO(record)})
}

// adminGetReplacement returns one replacement with its lines.
func (h *Handler) adminGetReplacement(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	record, err := h.svc.GetReplacement(ctx, chi.URLParam(r, paramReplacementID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toReplacementDTO(record)})
}

// adminListReplacements returns a claim's replacements, newest first.
//
// The list is NOT paged and the reason is the shape of the data rather than a
// shortcut: a replacement belongs to one claim, a claim is settled once, and
// the count is bounded by the lines of a single order. A page over a handful of
// rows would add a cursor nobody advances.
func (h *Handler) adminListReplacements(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	records, err := h.svc.ListReplacementsOfClaim(ctx, chi.URLParam(r, paramClaimID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	out := make([]replacementDTO, 0, len(records))
	for i := range records {
		out = append(out, toReplacementSummaryDTO(records[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: out})
}

// adminCancelReplacement withdraws a request that has not been acted on.
func (h *Handler) adminCancelReplacement(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	record, err := h.svc.CancelReplacement(ctx, chi.URLParam(r, paramReplacementID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK,
		singleEnvelope{Data: toReplacementSummaryDTO(record)})
}

// toReplacementSummaryDTO converts a replacement without its lines.
func toReplacementSummaryDTO(record models.Replacement) replacementDTO {
	return replacementDTO{
		ID:               record.ID,
		ClaimID:          record.ClaimID,
		Status:           record.Status.String(),
		ShippingOptionID: record.ShippingOptionID,
		LocationID:       record.LocationID,
		Note:             record.Note,
		CanceledAt:       record.CanceledAt,
		CreatedAt:        record.CreatedAt,
		UpdatedAt:        record.UpdatedAt,
	}
}

// toReplacementDTO converts a replacement together with its lines.
func toReplacementDTO(record service.ReplacementRecord) replacementDTO {
	out := toReplacementSummaryDTO(record.Replacement)
	out.Items = make([]replacementItemDTO, 0, len(record.Items))
	for i := range record.Items {
		out.Items = append(out.Items, replacementItemDTO{
			ID:              record.Items[i].ID,
			OrderLineItemID: record.Items[i].OrderLineItemID,
			Quantity:        record.Items[i].Quantity,
			CreatedAt:       record.Items[i].CreatedAt,
		})
	}

	return out
}
