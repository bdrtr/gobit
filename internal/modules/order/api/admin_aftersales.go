package api

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// receiveReturnRequest is the body of the return receipt.
//
// It names WHERE the goods arrived and nothing else. The quantities are already
// on the return record, and letting a request restate them would let the
// warehouse be told something different from what was agreed.
type receiveReturnRequest struct {
	// LocationID is the stock location the goods arrived at; it is REQUIRED.
	LocationID string `json:"location_id"`
}

// receiveReturnResponse reports what the receipt did.
type receiveReturnResponse struct {
	RestockedLines int      `json:"restocked_lines"`
	RestockedUnits int64    `json:"restocked_units"`
	Warnings       []string `json:"warnings,omitempty"`
}

// adminReceiveReturn records that the returned goods arrived and puts their
// stock back.
//
// It goes through the FLOW rather than the service: the record half is this
// module's, the stock half reaches inventory, and an endpoint bound to the
// service method would stamp the first and silently skip the second.
//
// A 200 with warnings is a real outcome and not a contradiction: the goods
// arrived, the record says so, and something about the stock needs a human. The
// alternative — refusing the receipt — would deny a physical fact and leave the
// operator with no record to work from.
func (h *Handler) adminReceiveReturn(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	flow, err := h.returnReceiving()
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	var body receiveReturnRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	lines, units, warnings, err := flow.ReceiveReturn(ctx, chi.URLParam(r, paramReturnID), body.LocationID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: receiveReturnResponse{
		RestockedLines: lines,
		RestockedUnits: units,
		Warnings:       warnings,
	}})
}

// refundReturnRequest is the body of the return refund.
type refundReturnRequest struct {
	// Amount is how much to send back (minor unit). ZERO means everything the
	// collection has left, which is what "give the customer their money back"
	// means when nobody named a figure.
	Amount int64 `json:"amount"`
	// Reason is free text kept on the refund record; it is optional.
	Reason string `json:"reason"`
}

// refundReturnResponse reports what the refund did.
type refundReturnResponse struct {
	RefundedAmount  int64    `json:"refunded_amount"`
	SummaryRecorded bool     `json:"summary_recorded"`
	Warnings        []string `json:"warnings,omitempty"`
}

// adminRefundReturn sends money back for a received return.
//
// It is a SEPARATE endpoint from receiving on purpose. Receiving is a physical
// fact — the goods are in the building — while refunding is a decision the shop
// makes after looking at what arrived, and a single endpoint doing both would
// take that decision away.
func (h *Handler) adminRefundReturn(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	flow, err := h.returnReceiving()
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	var body refundReturnRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	refunded, recorded, warnings, err := flow.RefundReturn(
		ctx, chi.URLParam(r, paramReturnID), body.Amount, body.Reason)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: refundReturnResponse{
		RefundedAmount:  refunded,
		SummaryRecorded: recorded,
		Warnings:        warnings,
	}})
}

// adminSettleClaim settles a damage or shortage claim by refunding it.
//
// A claim to be settled with a REPLACEMENT comes back as a conflict, and the
// message says why: shipping goods against an existing order is not something
// this framework can do. Stamping it complete would record a settlement that
// never reached the customer.
func (h *Handler) adminSettleClaim(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	flow, err := h.returnReceiving()
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	var body refundReturnRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	refunded, recorded, warnings, err := flow.SettleClaim(
		ctx, chi.URLParam(r, paramClaimID), body.Amount, body.Reason)
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: refundReturnResponse{
		RefundedAmount:  refunded,
		SummaryRecorded: recorded,
		Warnings:        warnings,
	}})
}

// createReturnRequest is the body of POST /admin/v1/orders/{id}/returns.
type createReturnRequest struct {
	RefundAmount int64          `json:"refund_amount"`
	Reason       string         `json:"reason"`
	Note         string         `json:"note"`
	Metadata     map[string]any `json:"metadata"`
}

// adminCreateReturn opens a return record on the order.
func (h *Handler) adminCreateReturn(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createReturnRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	ret, err := h.svc.CreateReturn(ctx, service.CreateReturnInput{
		OrderID:      orderID(r),
		RefundAmount: body.RefundAmount,
		Reason:       body.Reason,
		Note:         body.Note,
		Metadata:     body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toReturnDTO(ret)})
}

// adminGetReturn returns the return record by its id.
//
// The endpoint sits UNDER the order ({id}/returns/{returnId}) because the
// resource belongs to the order; the record's id is already unique and the read
// is done with that alone. The order id in the path IS NOT a check that the
// record really belongs to that order — such a check gains meaning together
// with the scope enforcement of Phase 8 (auth), and adding it today would give
// the impression that authorization exists.
func (h *Handler) adminGetReturn(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ret, err := h.svc.GetReturn(ctx, chi.URLParam(r, paramReturnID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toReturnDTO(ret)})
}

// adminListReturns returns the order's return records paged.
func (h *Handler) adminListReturns(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	items, count, err := h.svc.ListReturns(ctx, orderID(r), page)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]returnDTO, 0, len(items))
	for i := range items {
		data = append(data, toReturnDTO(items[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data: data, Count: count, Offset: page.Offset, Limit: page.Limit,
	})
}

// adminCancelReturn withdraws the return request.
//
// # Why this endpoint exists, and why "a return is never withdrawn" was the
// wrong answer
//
// [service.Service.CancelReturn] was written in full — query, repository
// method, transition table — and until this route no HTTP caller reached it
// (D17). The alternative on the table was to delete the method instead, on the
// product argument that a return is simply never taken back. Three things
// already in this module say otherwise, and each of them is a promise the
// module could not keep while nothing called it:
//
//  1. The quantity rule has a RELEASE VALVE and it was unreachable.
//     SumReturnedQuantities (queries/order_return_items.sql) excludes canceled
//     returns on purpose — "a withdrawn request releases the units it was
//     holding" — and with no caller that clause could never fire. A request
//     for the whole line therefore consumed the line's returnable quantity
//     FOREVER, and the customer-facing [Handler.storeRequestReturn] needs
//     nothing but the order id to open one.
//  2. [models.ReturnStatus.CancelAction] is a three-row table whose two other
//     rows exist only to guard this transition: received -> conflict is the
//     goods being physically here, canceled -> noop is the second click.
//  3. The support desk's timeline already publishes a "return.canceled" entry
//     kind (service/timeline.go) that no row could ever carry.
//
// Deleting the method would have meant tearing all three out and leaving a
// return request with no way to be closed except receiving goods nobody sent.
//
// # Why it goes to the service and not through a flow
//
// The same argument [Handler.adminCancelExchange] makes, and it holds here for
// a narrower reason than it looks: RECEIVING a return reaches inventory, so
// that one goes through a flow. Withdrawing an UNRECEIVED request reaches
// nothing — no stock was ever put back, so none has to be taken away again,
// and no money has moved. The received case is not a hole in that argument,
// it is refused by the transition table.
//
// It answers with the RECORD rather than the order: the order is unchanged by
// a request that was taken back.
func (h *Handler) adminCancelReturn(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ret, err := h.svc.CancelReturn(ctx, chi.URLParam(r, paramReturnID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toReturnDTO(ret)})
}

// createExchangeRequest is the body of POST /admin/v1/orders/{id}/exchanges.
type createExchangeRequest struct {
	// DifferenceDue, when positive, is collected from the customer; when
	// negative it is paid to the customer.
	DifferenceDue int64          `json:"difference_due"`
	Note          string         `json:"note"`
	Metadata      map[string]any `json:"metadata"`
}

// adminCreateExchange opens an exchange record on the order.
func (h *Handler) adminCreateExchange(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createExchangeRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	exchange, err := h.svc.CreateExchange(ctx, service.CreateExchangeInput{
		OrderID:       orderID(r),
		DifferenceDue: body.DifferenceDue,
		Note:          body.Note,
		Metadata:      body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toExchangeDTO(exchange)})
}

// adminGetExchange returns the exchange record by its id.
//
// For the path and id contract see [Handler.adminGetReturn].
func (h *Handler) adminGetExchange(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	exchange, err := h.svc.GetExchange(ctx, chi.URLParam(r, paramExchangeID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toExchangeDTO(exchange)})
}

// adminListExchanges returns the order's exchange records paged.
func (h *Handler) adminListExchanges(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	items, count, err := h.svc.ListExchanges(ctx, orderID(r), page)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]exchangeDTO, 0, len(items))
	for i := range items {
		data = append(data, toExchangeDTO(items[i]))
	}
	clientLimit, _ := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64)
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data: data, Count: count, Offset: page.Offset, Limit: clientLimit,
	})
}

// adminCancelExchange withdraws the exchange request.
//
// # Why this endpoint exists and no completion endpoint does
//
// Withdrawing is the only thing that can be done to an exchange, and until this
// endpoint the record could not be moved at all: it was born "requested" and
// stayed there forever, with two stamp columns nothing wrote. The reason no
// sibling "complete" route stands here is on [models.ExchangeStatus] — the two
// movements completing an exchange needs are capabilities this framework does
// not have, and a route that stamped the record anyway would report work that
// never happened.
//
// # Why it goes to the service and not through a flow
//
// [Handler.adminReceiveReturn] goes through one because receiving reaches
// inventory. Withdrawing reaches nothing: no stock moves, no money moves, no
// other module is told. The whole transition is the record's own row, so the
// service IS the right depth — routing it through a flow would add a hop that
// does nothing and imply a cross-module effect that does not exist.
//
// It answers with the record rather than the order: the caller acted on the
// exchange, and the order is unchanged by a withdrawn request.
func (h *Handler) adminCancelExchange(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	exchange, err := h.svc.CancelExchange(ctx, chi.URLParam(r, paramExchangeID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toExchangeDTO(exchange)})
}

// createClaimRequest is the body of POST /admin/v1/orders/{id}/claims.
type createClaimRequest struct {
	// Type has to be either "refund" or "replace".
	Type         string         `json:"type"`
	RefundAmount int64          `json:"refund_amount"`
	Reason       string         `json:"reason"`
	Note         string         `json:"note"`
	Metadata     map[string]any `json:"metadata"`
}

// adminCreateClaim opens a damage/shortage claim record on the order.
//
// The type cannot be left empty: picking a default type (e.g. "refund") would
// mean deciding on the client's behalf how the request is to be met.
func (h *Handler) adminCreateClaim(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createClaimRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if body.Type == "" {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
			"type cannot be empty: it has to be %q or %q", models.ClaimRefund, models.ClaimReplace))
		return
	}

	claim, err := h.svc.CreateClaim(ctx, service.CreateClaimInput{
		OrderID:      orderID(r),
		Type:         models.ClaimType(body.Type),
		RefundAmount: body.RefundAmount,
		Reason:       body.Reason,
		Note:         body.Note,
		Metadata:     body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toClaimDTO(claim)})
}

// adminGetClaim returns the claim record by its id.
//
// For the path and id contract see [Handler.adminGetReturn].
func (h *Handler) adminGetClaim(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claim, err := h.svc.GetClaim(ctx, chi.URLParam(r, paramClaimID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toClaimDTO(claim)})
}

// adminListClaims returns the order's claim records paged.
func (h *Handler) adminListClaims(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	items, count, err := h.svc.ListClaims(ctx, orderID(r), page)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]claimDTO, 0, len(items))
	for i := range items {
		data = append(data, toClaimDTO(items[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data: data, Count: count, Offset: page.Offset, Limit: page.Limit,
	})
}

// adminCancelClaim withdraws the claim.
//
// # Why an operator has to be able to close a claim without settling it
//
// A claim is opened from the ADMIN side ([Handler.adminCreateClaim]) — the
// operator records that goods arrived damaged or short. Until this route the
// record had exactly one exit, [Handler.adminSettleClaim], and that one refuses
// anything but a claim in status "requested" AND of type "refund"
// (internal/workflows/returns/claim.go). So a claim opened against the wrong
// order, opened twice, or opened for a customer who then withdrew the
// complaint had NO exit at all: it stayed "requested" forever, on the order's
// claim list and on its timeline, indistinguishable from work still owed.
//
// The alternative — deleting [service.Service.CancelClaim] on the grounds that
// a claim is only ever settled — asserts that every claim an operator opens is
// correct. It also contradicts [models.ClaimStatus.CancelAction], whose
// completed -> conflict row exists precisely to state the one case where
// withdrawal is NOT the answer: a claim already met with money is un-met by a
// new record, not by a status change.
//
// # Why it goes to the service and not through the flow
//
// Settling reaches payment, so it goes through the flow. Withdrawing settles
// nothing: no money is sent, no goods are promised, no other module is told.
// Routing it through the flow would imply a cross-module effect that does not
// exist — the same argument [Handler.adminCancelExchange] makes.
func (h *Handler) adminCancelClaim(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	claim, err := h.svc.CancelClaim(ctx, chi.URLParam(r, paramClaimID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toClaimDTO(claim)})
}
