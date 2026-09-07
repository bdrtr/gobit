package api

import (
	"net/http"
	"time"

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
