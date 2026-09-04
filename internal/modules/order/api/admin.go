package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
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

	orders, count, err := h.svc.ListOrders(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]orderDTO, 0, len(orders))
	// The loop is walked by index: the order struct is large and copying it by
	// value would carry a few hundred bytes for nothing on every turn.
	for i := range orders {
		data = append(data, toOrderDTO(orders[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   data,
		Count:  count,
		Offset: page.Offset,
		Limit:  page.Limit,
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
