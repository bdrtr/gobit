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
	// OrderLineItemID is the order line being replaced; leave it out to send
	// something the order did not sell.
	OrderLineItemID string `json:"order_line_item_id,omitempty"`
	// VariantID is the product being sent when it is not one of the order's.
	//
	// EXACTLY ONE of the two is given. "Send the same shirt a size larger" is the
	// ordinary exchange, and until ADR 0145 the only thing a replacement could
	// carry was units of a variant already on the order.
	VariantID string `json:"variant_id,omitempty"`
	// Quantity is how many units of it are being sent.
	Quantity int64 `json:"quantity"`
	// UnitPrice is the operator's unit price for a variant an exchange that
	// names its return sends, in the order's convention; left out, the
	// variant is priced as a cart of the order's customer would be. Its tax is
	// computed either way, and it is refused on a line and on a replacement
	// that prices nothing (ADR 0432).
	UnitPrice *int64 `json:"unit_price,omitempty"`
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
	ID string `json:"id"`
	// OrderLineItemID is empty on an item that names a variant, and the reverse.
	OrderLineItemID string `json:"order_line_item_id,omitempty"`
	VariantID       string `json:"variant_id,omitempty"`
	Quantity        int64  `json:"quantity"`
	// ReservationID is the promise the units are held under; it is empty until
	// something sets them aside, and on an item with parts.
	ReservationID string `json:"reservation_id,omitempty"`
	// Parts are what one unit holds when the item replaces a line that sold a
	// bundle (ADR 0238), each held under its own promise; absent otherwise.
	Parts []replacementPartDTO `json:"parts,omitempty"`
	// Price is what the item's units are sold at, written with the item; it
	// is absent on a claim's item and on an item of an exchange that names no
	// return (ADR 0432).
	Price     *replacementPriceDTO `json:"price,omitempty"`
	CreatedAt time.Time            `json:"created_at"`
}

// replacementPriceDTO is what a replacement item's units are sold at.
type replacementPriceDTO struct {
	// UnitPrice is the unit price, in the order's convention.
	UnitPrice int64 `json:"unit_price"`
	// Total is what the buyer pays for the item's units, tax included, and
	// TaxTotal the tax in it, at TaxRateBps.
	Total      int64 `json:"total"`
	TaxTotal   int64 `json:"tax_total"`
	TaxRateBps int32 `json:"tax_rate_bps"`
	// TaxComponents is the per-rate breakdown when a stack taxed the item.
	TaxComponents []replacementTaxDTO `json:"tax_components,omitempty"`
	// PricedBy is "line" (the order line's own sold figures), "quote" or
	// "operator".
	PricedBy string `json:"priced_by"`
}

// replacementTaxDTO is one rate of a replacement item's stack.
type replacementTaxDTO struct {
	RateID        string `json:"rate_id,omitempty"`
	RateBps       int32  `json:"rate_bps"`
	Compound      bool   `json:"compound"`
	TaxableAmount int64  `json:"taxable_amount"`
	TaxAmount     int64  `json:"tax_amount"`
}

// replacementPartDTO is one part of a replacement item in a response.
type replacementPartDTO struct {
	VariantID string `json:"variant_id"`
	// Quantity is how many of it one unit of the item holds.
	Quantity int64 `json:"quantity"`
	// ReservationID is the promise this part's units are held under; it is
	// empty until something sets them aside.
	ReservationID string `json:"reservation_id,omitempty"`
}

// replacementDTO is a replacement in a response.
type replacementDTO struct {
	ID string `json:"id"`
	// SourceKind is "claim" or "exchange": which record the goods answer.
	SourceKind string `json:"source_kind"`
	// ClaimID and ExchangeID name that record; exactly ONE of them is set, and
	// the empty one is omitted rather than sent as "".
	ClaimID          string `json:"claim_id,omitempty"`
	ExchangeID       string `json:"exchange_id,omitempty"`
	Status           string `json:"status"`
	ShippingOptionID string `json:"shipping_option_id"`
	LocationID       string `json:"location_id"`
	Note             string `json:"note,omitempty"`
	// FulfillmentID is the parcel the goods left in; it is empty until they do.
	FulfillmentID string               `json:"fulfillment_id,omitempty"`
	Items         []replacementItemDTO `json:"items,omitempty"`
	CanceledAt    *time.Time           `json:"canceled_at,omitempty"`
	// DispatchedAt is the moment the goods left; it is absent until they do.
	DispatchedAt *time.Time `json:"dispatched_at,omitempty"`
	// Recalls counts the parcels canceled under this replacement, each of which
	// put its units back and sent it back to waiting (ADR 0239).
	Recalls   int       `json:"recalls"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
			VariantID:       body.Lines[i].VariantID,
			Quantity:        body.Lines[i].Quantity,
			UnitPrice:       body.Lines[i].UnitPrice,
		})
	}

	record, err := h.svc.CreateReplacement(ctx, service.CreateReplacementInput{
		ClaimID:          chi.URLParam(r, paramClaimID),
		ExchangeID:       chi.URLParam(r, paramExchangeID),
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

// adminListExchangeReplacements returns an exchange's replacements, newest first.
//
// It is the claim listing one record over, and unpaged for the same reason.
func (h *Handler) adminListExchangeReplacements(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	records, err := h.svc.ListReplacementsOfExchange(ctx, chi.URLParam(r, paramExchangeID))
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

// adminCancelReplacement withdraws a request that has not left.
//
// It goes through the returns flow because a dispatch that stopped half way has
// set units aside under the request's lines, and the record alone cannot give
// them back (ADR 0237). Without the flow it fails closed, as a dispatch does:
// withdrawing the record alone is what kept those units held (D159).
func (h *Handler) adminCancelReplacement(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, paramReplacementID)

	flow, err := h.returnReceiving()
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	if err := flow.WithdrawReplacement(ctx, id); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	record, err := h.svc.GetReplacement(ctx, id)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK,
		singleEnvelope{Data: toReplacementSummaryDTO(record.Replacement)})
}

// toReplacementSummaryDTO converts a replacement without its lines.
func toReplacementSummaryDTO(record models.Replacement) replacementDTO {
	return replacementDTO{
		ID:               record.ID,
		SourceKind:       string(record.Source()),
		ClaimID:          record.ClaimID,
		ExchangeID:       record.ExchangeID,
		Status:           record.Status.String(),
		ShippingOptionID: record.ShippingOptionID,
		LocationID:       record.LocationID,
		Note:             record.Note,
		FulfillmentID:    record.FulfillmentID,
		CanceledAt:       record.CanceledAt,
		DispatchedAt:     record.DispatchedAt,
		Recalls:          record.Recalls,
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
			VariantID:       record.Items[i].VariantID,
			Quantity:        record.Items[i].Quantity,
			ReservationID:   record.Items[i].ReservationID,
			Parts:           toReplacementPartDTOs(record.Items[i].Parts),
			Price:           toReplacementPriceDTO(record.Items[i].Price),
			CreatedAt:       record.Items[i].CreatedAt,
		})
	}

	return out
}

// toReplacementPriceDTO converts an item's price; an unpriced item has none.
func toReplacementPriceDTO(price *models.ReplacementPrice) *replacementPriceDTO {
	if price == nil {
		return nil
	}
	out := &replacementPriceDTO{
		UnitPrice: price.UnitPrice, Total: price.Total, TaxTotal: price.TaxTotal,
		TaxRateBps: price.TaxRateBps, PricedBy: string(price.PricedBy),
	}
	for _, c := range price.TaxComponents {
		out.TaxComponents = append(out.TaxComponents, replacementTaxDTO(c))
	}

	return out
}

// toReplacementPartDTOs converts an item's parts; none is nil, so the field is
// left out.
func toReplacementPartDTOs(parts []models.ReplacementItemPart) []replacementPartDTO {
	if len(parts) == 0 {
		return nil
	}
	out := make([]replacementPartDTO, 0, len(parts))
	for _, p := range parts {
		out = append(out, replacementPartDTO{VariantID: p.VariantID, Quantity: p.Quantity, ReservationID: p.ReservationID})
	}

	return out
}

// dispatchReplacementResponse is what the dispatch endpoint answers with.
type dispatchReplacementResponse struct {
	// FulfillmentID is the parcel the goods left in.
	FulfillmentID string `json:"fulfillment_id"`
	// SentUnits is how many units left the warehouse.
	SentUnits int64 `json:"sent_units"`
	// AlreadySent reports that the goods had already gone and nothing moved
	// this time.
	AlreadySent bool `json:"already_sent"`
}

// adminDispatchReplacement sends what the claim promised.
//
// It is the endpoint the record was waiting for. The record says WHAT to send
// (ADR 0089) and this makes it leave: the units are set aside, a parcel is
// opened, the units come out of the count and the claim is settled by the goods
// rather than by money.
func (h *Handler) adminDispatchReplacement(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	flow, err := h.returnReceiving()
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	fulfillmentID, sentUnits, alreadySent, err := flow.DispatchReplacement(
		ctx, chi.URLParam(r, paramReplacementID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{
		Data: dispatchReplacementResponse{
			FulfillmentID: fulfillmentID,
			SentUnits:     sentUnits,
			AlreadySent:   alreadySent,
		},
	})
}
