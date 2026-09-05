package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// adminListOrders returns the orders paged.
//
// Supported filters: customer_id, region_id and status. Line items are NOT
// LOADED; fetching the children of dozens of orders per page would open the
// list up to N+1. The detail of a single order is fetched with
// /admin/v1/orders/{id}.
func (h *Handler) adminListOrders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	in := service.ListOrdersInput{Page: page}
	if raw := r.URL.Query().Get("customer_id"); raw != "" {
		in.CustomerID = &raw
	}
	if raw := r.URL.Query().Get("region_id"); raw != "" {
		in.RegionID = &raw
	}
	if raw := r.URL.Query().Get("status"); raw != "" {
		status := models.OrderStatus(raw)
		in.Status = &status
	}

	result, err := h.svc.ListOrders(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]orderDTO, 0, len(result.Items))
	// The loop is walked by index: the order struct is large and copying it by
	// value would carry a few hundred bytes for nothing on every turn.
	for i := range result.Items {
		data = append(data, toOrderDTO(result.Items[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:       data,
		Count:      result.Count,
		Offset:     page.Offset,
		Limit:      page.Limit,
		NextCursor: result.NextCursor,
	})
}

// adminGetOrder returns the order with its line items and summary.
func (h *Handler) adminGetOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	detail, err := h.svc.GetOrder(ctx, orderID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toOrderDetailDTO(detail)})
}

// orderPaymentDTO is the live payment view of an order.
type orderPaymentDTO struct {
	CollectionID     string `json:"payment_collection_id"`
	Status           string `json:"status"`
	Amount           int64  `json:"amount"`
	AuthorizedAmount int64  `json:"authorized_amount"`
	CapturedAmount   int64  `json:"captured_amount"`
	RefundedAmount   int64  `json:"refunded_amount"`
	CurrencyCode     string `json:"currency_code"`
	// FirstCapturedAt is when money first moved and LastRefundedAt when the
	// last refund went out. Both are NULL when the thing never happened; a zero
	// time would read as 1 January year one to whoever is drawing a timeline.
	FirstCapturedAt *time.Time `json:"first_captured_at"`
	LastRefundedAt  *time.Time `json:"last_refunded_at"`
}

// adminGetOrderPayment returns what the PAYMENT MODULE says about the order
// right now.
//
// # Why it is its own endpoint and not a field of the order
//
// The order's own record already carries what it BELIEVES was paid on it
// (order.summary, written by the checkout saga, ADR 0022). This reads the other
// side of the same fact, live, through the "order_payment" link — and putting
// it on the detail response would make every order read reach into another
// module, on a path that mostly does not need it.
//
// Having both is the point rather than a duplication: an operator with the two
// in front of them can tell a RECORDED payment from a real one, which is the
// same argument ADR 0020 makes about a session and its provider.
//
// # A 404 means "no payment is bound", not "no order"
//
// The two are distinguished: a missing order returns the service's own
// NotFound, while an order with no collection returns a payment-specific one.
// An order can genuinely have none — the saga binds the collection after the
// order is written — and that is a fact an operator should see, not one to hide
// the whole order behind.
func (h *Handler) adminGetOrderPayment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	payment, bound, err := h.svc.PaymentOf(ctx, orderID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	if !bound {
		corehttp.WriteError(ctx, w, coreerrors.NotFound(codeOrderPaymentUnbound,
			"no payment collection is bound to this order: %s", orderID(r)))

		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: orderPaymentDTO{
		CollectionID:     payment.CollectionID,
		Status:           payment.Status,
		Amount:           payment.Amount,
		AuthorizedAmount: payment.AuthorizedAmount,
		CapturedAmount:   payment.CapturedAmount,
		RefundedAmount:   payment.RefundedAmount,
		CurrencyCode:     payment.CurrencyCode,
		FirstCapturedAt:  payment.FirstCapturedAt,
		LastRefundedAt:   payment.LastRefundedAt,
	}})
}

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

// cancelOrderRequest is the body of POST /admin/v1/orders/{id}/cancel.
type cancelOrderRequest struct {
	// Reason is the cancellation reason; it is optional.
	Reason string `json:"reason"`
}

// adminCancelOrder cancels the order and returns its current state.
//
// The call is IDEMPOTENT: an already canceled order is not an error, it returns
// 200 with its existing (canceled) state. For the rationale see
// [service.Service.CancelOrder]. On a completed order it returns 409.
func (h *Handler) adminCancelOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body cancelOrderRequest
	if err := decodeOptionalBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	if err := h.svc.CancelOrder(ctx, orderID(r), body.Reason); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	h.writeCurrentOrder(w, r)
}

// adminCompleteOrder completes the order and returns its current state.
func (h *Handler) adminCompleteOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if _, err := h.svc.CompleteOrder(ctx, orderID(r)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	h.writeCurrentOrder(w, r)
}

// adminArchiveOrder archives a completed order and returns its current state.
func (h *Handler) adminArchiveOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if _, err := h.svc.ArchiveOrder(ctx, orderID(r)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	h.writeCurrentOrder(w, r)
}

// writeCurrentOrder writes the CURRENT detail of the order after a status
// transition.
//
// There are two reasons for reading it again instead of using the
// [models.Order] the transition methods return: [service.Service.CancelOrder]
// returns nothing because it is idempotent (the second call performs no write),
// and the response envelope has to be the SAME on all three endpoints — line
// items and summary included. The extra read happens only on the rarely used
// endpoints of the admin side.
func (h *Handler) writeCurrentOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	detail, err := h.svc.GetOrder(ctx, orderID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toOrderDetailDTO(detail)})
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
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data: data, Count: count, Offset: page.Offset, Limit: page.Limit,
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

// timelineEntryDTO is one thing that happened to an order.
type timelineEntryDTO struct {
	// At is when it happened. It is NULL when the fact is real and its moment
	// was never recorded; those entries come LAST.
	At *time.Time `json:"at"`
	// Kind is what happened, as "<source>.<what>".
	Kind string `json:"kind"`
	// RefID is the record the moment belongs to.
	RefID string `json:"ref_id"`
	// Clock says which clock stamped At — see the endpoint's description. It is
	// empty when At is null.
	Clock string `json:"clock"`
	// Detail is a short extra: a status, a tracking number.
	Detail string `json:"detail,omitempty"`
	// Amount and Currency are set on the money entries only.
	Amount   int64  `json:"amount,omitempty"`
	Currency string `json:"currency_code,omitempty"`
}

// adminGetOrderTimeline returns everything that happened to the order.
//
// # Why it is not part of the order detail
//
// It reaches two other modules through links and this module's own after-sales
// tables — a handful of round trips that the ordinary order read has no use
// for. Putting it on the detail response would make every order read pay for a
// screen that is opened when something has gone wrong.
func (h *Handler) adminGetOrderTimeline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	entries, err := h.svc.Timeline(ctx, orderID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	out := make([]timelineEntryDTO, 0, len(entries))
	for _, entry := range entries {
		out = append(out, timelineEntryDTO{
			At:       entry.At,
			Kind:     entry.Kind,
			RefID:    entry.RefID,
			Clock:    entry.Clock,
			Detail:   entry.Detail,
			Amount:   entry.Amount,
			Currency: entry.Currency,
		})
	}

	// The single envelope, not the paged one: a timeline is bounded by its order
	// and there is no page to ask for. Filling a paging envelope with zeros
	// would announce a count, an offset and a limit that mean nothing.
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: out})
}
