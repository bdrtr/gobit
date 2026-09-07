package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// This file holds the fulfillment endpoints under /admin/v1/fulfillments:
// opening a fulfillment at the provider and then moving it through its life
// cycle — cancel, ship, deliver, returned. The catalog files next door describe
// what CAN be shipped; a fulfillment is a parcel that actually was, and that
// difference is why it is its own file.

// createFulfillmentRequest is the body of POST /admin/v1/fulfillments.
type createFulfillmentRequest struct {
	Reference        string `json:"reference"`
	ShippingOptionID string `json:"shipping_option_id"`
	// IdempotencyKey is required: a second request with the same key does NOT
	// open a new fulfillment, it returns the existing one.
	IdempotencyKey string                 `json:"idempotency_key"`
	Items          []fulfillmentItemInput `json:"items"`
	Data           map[string]any         `json:"data"`
	Metadata       map[string]any         `json:"metadata"`
}

// fulfillmentItemInput is the body of a fulfillment item.
type fulfillmentItemInput struct {
	LineItemID string `json:"line_item_id"`
	// Quantity is a pointer: the distinction between "not sent" and "sent as
	// zero" is preserved and both are rejected, but with a different message.
	Quantity *int64 `json:"quantity"`
}

// createFulfillment opens a fulfillment at the provider.
func (h *Handler) createFulfillment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createFulfillmentRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	items := make([]service.FulfillmentItemInput, 0, len(body.Items))
	for i, item := range body.Items {
		if item.Quantity == nil {
			corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
				"the quantity field of item %d is required", i+1))
			return
		}
		items = append(items, service.FulfillmentItemInput{
			LineItemID: item.LineItemID,
			Quantity:   *item.Quantity,
		})
	}

	ful, err := h.svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference:        body.Reference,
		ShippingOptionID: body.ShippingOptionID,
		IdempotencyKey:   body.IdempotencyKey,
		Items:            items,
		Data:             body.Data,
		Metadata:         body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toFulfillmentDTO(ful)})
}

// listFulfillments returns the fulfillments page by page.
func (h *Handler) listFulfillments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	in := service.ListFulfillmentsInput{Page: page}
	if raw := r.URL.Query().Get("reference"); raw != "" {
		in.Reference = &raw
	}
	if raw := r.URL.Query().Get("status"); raw != "" {
		in.Status = &raw
	}

	list, count, err := h.svc.ListFulfillments(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]fulfillmentDTO, 0, len(list))
	for i := range list {
		data = append(data, toFulfillmentDTO(list[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   data,
		Count:  count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// getFulfillment returns the fulfillment together with its items.
func (h *Handler) getFulfillment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ful, err := h.svc.GetFulfillment(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toFulfillmentDTO(ful)})
}

// cancelFulfillment cancels the fulfillment and returns its CURRENT state.
//
// Cancellation is IDEMPOTENT: a second call returns 200 as well. The response
// having a body is deliberate — the caller has to be able to see from the
// status field that the cancellation was really written.
func (h *Handler) cancelFulfillment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")

	if err := h.svc.CancelFulfillment(ctx, id); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	h.writeFulfillment(w, r, id)
}

// shipRequest is the body of POST /admin/v1/fulfillments/{id}/ship.
type shipRequest struct {
	TrackingNumber string `json:"tracking_number"`
	TrackingURL    string `json:"tracking_url"`
}

// shipFulfillment marks the fulfillment as handed to the carrier.
//
// The body is OPTIONAL: shipping can be reported without tracking information
// as well (some carriers provide the number later).
func (h *Handler) shipFulfillment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body shipRequest
	if err := decodeOptionalBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	ful, err := h.svc.MarkShipped(ctx, chi.URLParam(r, "id"), body.TrackingNumber, body.TrackingURL)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toFulfillmentDTO(ful)})
}

// deliverFulfillment marks the fulfillment as delivered.
func (h *Handler) deliverFulfillment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ful, err := h.svc.MarkDelivered(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toFulfillmentDTO(ful)})
}

// returnFulfillment records that the parcel came back to the sender
// undelivered ("iade").
//
// There is NO request body, and the absence is the point: the route asserts one
// fact and takes no operator input to color it with. Cancellation next door
// takes none either, for the same reason.
func (h *Handler) returnFulfillment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ful, err := h.svc.MarkReturned(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toFulfillmentDTO(ful)})
}

// writeFulfillment reads the fulfillment and writes it with the single
// envelope.
//
// It exists to return the current record after an operation that has no body
// (cancellation); if the read fails, that error is written.
func (h *Handler) writeFulfillment(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()

	ful, err := h.svc.GetFulfillment(ctx, id)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toFulfillmentDTO(ful)})
}
