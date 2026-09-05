// Package api is the HTTP surface of the order module.
//
// There are two surfaces: the admin side (/admin/v1/orders …) reads the order
// and applies status transitions, the customer side (/store/v1/orders/{id})
// ONLY READS.
//
// # Scopes
//
// The admin endpoints ASK FOR a scope and the scope is enforced endpoint by
// endpoint (see [Handler.Routes]):
//
//   - [ScopeRead] ("order:read") — opens the GET endpoints under /admin/v1:
//     the order list and a single order, return/exchange/claim records.
//   - [ScopeWrite] ("order:write") — opens the POST endpoints under /admin/v1:
//     cancel, complete, archive and creating after-sales records.
//
// corehttp.ScopeAdmin ("admin") is the SUPER SCOPE; on its own it satisfies
// both of them (see corehttp.Principal.HasScope).
//
// No scope IS ADDED to the storefront endpoint: the identity of /store/v1 is
// the publishable key and that key by definition carries no scope.
//
// # Surfaces not opened to HTTP
//
// [service.Service.CreateOrder] DELIBERATELY gets no route. An order is a
// record whose amounts are supplied from outside: had it been opened to HTTP, a
// client could have written an order with a total it determined itself — with a
// total of zero, for example. The validation layers only ensure that the input
// is consistent WITHIN ITSELF, not that the amounts correspond to the REAL
// prices; the only thing that provides that guarantee is the complete_cart
// workflow, which builds the snapshot from the cart and from pricing (ADR
// 0006). This is why an order is opened only through "order.interop".
//
// [service.Service.SetOrderSummaryTotals] gets no route for the same reason:
// the side that knows the amount paid is the payment flow, not the client.
//
// Creating an order by hand from the admin side (draft order) is the work of
// later phases and when it arrives it has to arrive with its own validation
// chain.
//
// Handlers DO NOT CHOOSE the status code: the service returns its core/errors
// typed error and corehttp.WriteError writes the code matching its kind (plan
// Section 8).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// maxBodyBytes is the upper limit for the request body. Without a limit a
// single request could exhaust the server's memory.
const maxBodyBytes int64 = 1 << 20 // 1 MiB

// codeInvalidRequest is the error code returned when the body or a parameter
// cannot be parsed.
const codeInvalidRequest = "order_invalid_request"

// codeFlowUnavailable reports that a flow the endpoint needs is not bound.
const codeFlowUnavailable = "order_workflow_unavailable"

// codeOrderPaymentUnbound reports that no payment collection is bound to the
// order.
//
// It is SEPARATE from the order's own not-found code on purpose: a client that
// cannot tell "there is no such order" from "this order has no payment" would
// treat a checkout that died mid-flight as a missing order.
const codeOrderPaymentUnbound = "order_payment_unbound"

// URL parameter names.
const (
	// paramOrderID is the URL parameter name of the order id.
	paramOrderID = "id"
	// paramReturnID is the URL parameter name of the return record id.
	paramReturnID = "returnId"
	// paramExchangeID is the URL parameter name of the exchange record id.
	paramExchangeID = "exchangeId"
	// paramClaimID is the URL parameter name of the claim record id.
	paramClaimID = "claimId"
)

// Orders is the surface the handlers need from the service.
//
// Keeping it narrow simplifies the tests: HTTP behavior can be verified with a
// fake of a few hundred lines, without a real database. CreateOrder and
// SetOrderSummaryTotals are NOT on the surface; both of them are the workflow
// surface that is not opened to HTTP (see the package documentation).
type Orders interface {
	// GetOrder returns the order with its line items and summary.
	GetOrder(ctx context.Context, orderID string) (models.OrderDetail, error)
	// ListOrders pages the orders.
	ListOrders(ctx context.Context, in service.ListOrdersInput) (service.OrderPage, error)
	// CancelOrder cancels the order; it is idempotent.
	CancelOrder(ctx context.Context, orderID, reason string) error
	// CompleteOrder completes the order.
	CompleteOrder(ctx context.Context, orderID string) (models.Order, error)
	// PaymentOf returns the LIVE payment collection bound to the order; the
	// second value reports whether one is bound at all.
	PaymentOf(ctx context.Context, orderID string) (service.OrderPayment, bool, error)
	// ArchiveOrder archives a completed order.
	ArchiveOrder(ctx context.Context, orderID string) (models.Order, error)

	// CreateReturn opens a return record on the order.
	CreateReturn(ctx context.Context, in service.CreateReturnInput) (models.Return, error)
	// GetReturn returns the return record by its id.
	GetReturn(ctx context.Context, returnID string) (models.Return, error)
	// ListReturns pages the order's return records.
	ListReturns(ctx context.Context, orderID string, page service.Page) ([]models.Return, int64, error)

	// CreateExchange opens an exchange record on the order.
	CreateExchange(ctx context.Context, in service.CreateExchangeInput) (models.Exchange, error)
	// GetExchange returns the exchange record by its id.
	GetExchange(ctx context.Context, exchangeID string) (models.Exchange, error)
	// ListExchanges pages the order's exchange records.
	ListExchanges(ctx context.Context, orderID string, page service.Page) ([]models.Exchange, int64, error)

	// CreateClaim opens a claim record on the order.
	CreateClaim(ctx context.Context, in service.CreateClaimInput) (models.Claim, error)
	// GetClaim returns the claim record by its id.
	GetClaim(ctx context.Context, claimID string) (models.Claim, error)
	// ListClaims pages the order's claim records.
	ListClaims(ctx context.Context, orderID string, page service.Page) ([]models.Claim, int64, error)
}

// ReturnReceiving is the surface used by this package of the flow that RECEIVES
// a return (ADR 0001/0006).
//
// # Why the endpoint does not call the service directly
//
// Receiving a return has two halves: the record says the goods arrived, and the
// stock goes back. The second reaches the inventory module, which this one does
// not know, so it belongs to a flow. Had the endpoint been bound to the service
// method it would have stamped the record and SILENTLY skipped the restock —
// the same shape of defect the cart module names about its line price.
type ReturnReceiving interface {
	// ReceiveReturn records that the goods arrived at the location and puts
	// their stock back.
	//
	// warnings is non-empty when the record is right and the warehouse count is
	// not; every entry needs a human.
	ReceiveReturn(ctx context.Context, returnID, locationID string) (
		restockedLines int, restockedUnits int64, warnings []string, err error,
	)

	// RefundReturn sends money back for a received return and records it on the
	// order.
	//
	// summaryRecorded being false does not mean the money stayed: it means the
	// ORDER does not say it left, and an operator has to be shown that.
	RefundReturn(ctx context.Context, returnID string, amount int64, reason string) (
		refunded int64, summaryRecorded bool, warnings []string, err error,
	)

	// SettleClaim settles a damage or shortage claim by refunding it.
	//
	// A claim settled with a REPLACEMENT is refused: shipping goods against an
	// existing order is not a capability this framework has, and stamping such
	// a claim complete would say something was sent when nothing was.
	SettleClaim(ctx context.Context, claimID string, amount int64, reason string) (
		refunded int64, summaryRecorded bool, warnings []string, err error,
	)
}

// Handler is the HTTP handler set of the order module.
type Handler struct {
	svc        Orders
	receiving  ReturnReceiving
	invoicing  Invoicing
	fulfilling Fulfilling
}

// New produces the handler set that runs over the given service and flow.
//
// receiving and invoicing may be nil; the endpoints that need them then FAIL
// CLOSED rather than falling back to something else. For receiving the fallback
// would stamp a return as received and put no stock back; for invoicing there
// is nothing to fall back TO — a document cannot be produced without the flow
// that assembles it, and pretending otherwise would answer a caller who is
// waiting for a legal document.
func New(
	svc Orders, receiving ReturnReceiving, invoicing Invoicing, fulfilling Fulfilling,
) *Handler {
	return &Handler{svc: svc, receiving: receiving, invoicing: invoicing, fulfilling: fulfilling}
}

// returnReceiving returns the flow; if it is not bound it returns an ERROR.
//
// # Why it fails CLOSED
//
// The same reasoning the cart module gives about its line price. If the flow is
// missing, the correct answer is NOT "record the receipt and skip the stock":
// the goods would be in the warehouse and the count would say they are not,
// with a record claiming the receipt succeeded. The only correct outcome of a
// missing flow is the return NOT BEING RECEIVED AT ALL.
func (h *Handler) returnReceiving() (ReturnReceiving, error) {
	if h.receiving == nil {
		return nil, coreerrors.Internal(codeFlowUnavailable,
			"the return receiving flow is not bound; a return cannot be received without the "+
				"stock going back")
	}

	return h.receiving, nil
}

// fulfillingFlow returns the flow; if it is not bound it returns an ERROR.
//
// It fails CLOSED and the reason is specific to this one: a shipment opened
// without the flow would be a real parcel bound to nothing, and nothing could
// afterwards say which order it belonged to. That is worse than not shipping.
func (h *Handler) fulfillingFlow() (Fulfilling, error) {
	if h.fulfilling == nil {
		return nil, coreerrors.Internal(codeFlowUnavailable,
			"the fulfilling flow is not bound; a shipment cannot be opened for an order "+
				"without it, and one opened another way would be bound to nothing")
	}

	return h.fulfilling, nil
}

// invoicingFlow returns the flow; if it is not bound it returns an ERROR.
//
// It fails CLOSED for a plainer reason than the receiving flow does: there is
// no second path to a document. What the endpoint must not do is answer 200
// with nothing, which is what a nil check placed further in would produce.
func (h *Handler) invoicingFlow() (Invoicing, error) {
	if h.invoicing == nil {
		return nil, coreerrors.Internal(codeFlowUnavailable,
			"the invoicing flow is not bound; an order cannot be invoiced without it")
	}

	return h.invoicing, nil
}

// --- envelopes and DTOs ------------------------------------------------------

// singleEnvelope is the envelope of single-record responses (plan Section 8).
type singleEnvelope struct {
	// Data is the body of the response.
	Data any `json:"data"`
}

// listEnvelope is the envelope of list responses (plan Section 8).
type listEnvelope struct {
	// Data holds the records on the page.
	Data any `json:"data"`
	// Count is the number of ALL records matching the filter; not the number of
	// rows on the page.
	Count int64 `json:"count"`
	// Offset is the number of skipped records.
	Offset int64 `json:"offset"`
	// Limit is the requested page size.
	Limit int64 `json:"limit"`
	// NextCursor is the opaque position to send back as "after" for the next
	// page; it is ABSENT when this page is the last one.
	//
	// Its absence is the end-of-listing signal, which is what a client walking
	// forward needs and what offset alone cannot give without a count.
	NextCursor string `json:"next_cursor,omitempty"`
}

// orderDTO is the external representation of the order.
type orderDTO struct {
	ID            string         `json:"id"`
	DisplayID     int64          `json:"display_id"`
	Status        string         `json:"status"`
	RegionID      string         `json:"region_id"`
	CustomerID    string         `json:"customer_id,omitempty"`
	Email         string         `json:"email,omitempty"`
	CurrencyCode  string         `json:"currency_code"`
	CartID        string         `json:"cart_id,omitempty"`
	Subtotal      int64          `json:"subtotal"`
	DiscountTotal int64          `json:"discount_total"`
	TaxTotal      int64          `json:"tax_total"`
	ShippingTotal int64          `json:"shipping_total"`
	Total         int64          `json:"total"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	PlacedAt      time.Time      `json:"placed_at"`
	CompletedAt   *time.Time     `json:"completed_at,omitempty"`
	CanceledAt    *time.Time     `json:"canceled_at,omitempty"`
	CancelReason  string         `json:"cancel_reason,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// orderDetailDTO is the external representation of the order with its line
// items and summary.
type orderDetailDTO struct {
	orderDTO
	Items   []lineItemDTO `json:"items"`
	Summary summaryDTO    `json:"summary"`
}

// lineItemDTO is the external representation of an order line item.
type lineItemDTO struct {
	ID            string `json:"id"`
	OrderID       string `json:"order_id"`
	VariantID     string `json:"variant_id"`
	Title         string `json:"title"`
	Quantity      int64  `json:"quantity"`
	UnitPrice     int64  `json:"unit_price"`
	Subtotal      int64  `json:"subtotal"`
	DiscountTotal int64  `json:"discount_total"`
	TaxTotal      int64  `json:"tax_total"`
	// TaxRateBps is the rate the line's tax was computed at, in BASIS POINTS
	// (2000 = 20%).
	//
	// It is published because it cannot be recomputed: the tax is rounded down
	// per line, so the amount alone maps back to a range of rates. Anything
	// that prints an invoice needs the rate the customer was charged under.
	TaxRateBps int32          `json:"tax_rate_bps"`
	Total      int64          `json:"total"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

// summaryDTO is the external representation of the order's payment/refund
// summary.
//
// Outstanding is a DERIVED field and it is presented TOGETHER with the amounts:
// having the client compute the outstanding amount itself meant the same
// formula being written in two places and one of them being wrong. The value
// can be NEGATIVE (overcollection).
type summaryDTO struct {
	ID            string    `json:"id"`
	OrderID       string    `json:"order_id"`
	PaidTotal     int64     `json:"paid_total"`
	RefundedTotal int64     `json:"refunded_total"`
	Outstanding   int64     `json:"outstanding"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// returnDTO is the external representation of a return record.
type returnDTO struct {
	ID           string         `json:"id"`
	OrderID      string         `json:"order_id"`
	Status       string         `json:"status"`
	RefundAmount int64          `json:"refund_amount"`
	Reason       string         `json:"reason,omitempty"`
	Note         string         `json:"note,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	ReceivedAt   *time.Time     `json:"received_at,omitempty"`
	CanceledAt   *time.Time     `json:"canceled_at,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// exchangeDTO is the external representation of an exchange record.
type exchangeDTO struct {
	ID            string         `json:"id"`
	OrderID       string         `json:"order_id"`
	Status        string         `json:"status"`
	DifferenceDue int64          `json:"difference_due"`
	Note          string         `json:"note,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	CompletedAt   *time.Time     `json:"completed_at,omitempty"`
	CanceledAt    *time.Time     `json:"canceled_at,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// claimDTO is the external representation of a claim record.
type claimDTO struct {
	ID           string         `json:"id"`
	OrderID      string         `json:"order_id"`
	Type         string         `json:"type"`
	Status       string         `json:"status"`
	RefundAmount int64          `json:"refund_amount"`
	Reason       string         `json:"reason,omitempty"`
	Note         string         `json:"note,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	CompletedAt  *time.Time     `json:"completed_at,omitempty"`
	CanceledAt   *time.Time     `json:"canceled_at,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// toOrderDTO converts the model to the external representation.
func toOrderDTO(order models.Order) orderDTO {
	return orderDTO{
		ID:            order.ID,
		DisplayID:     order.DisplayID,
		Status:        order.Status.String(),
		RegionID:      order.RegionID,
		CustomerID:    order.CustomerID,
		Email:         order.Email,
		CurrencyCode:  order.CurrencyCode,
		CartID:        order.CartID,
		Subtotal:      order.Subtotal,
		DiscountTotal: order.DiscountTotal,
		TaxTotal:      order.TaxTotal,
		ShippingTotal: order.ShippingTotal,
		Total:         order.Total,
		Metadata:      order.Metadata,
		PlacedAt:      order.PlacedAt,
		CompletedAt:   order.CompletedAt,
		CanceledAt:    order.CanceledAt,
		CancelReason:  order.CancelReason,
		CreatedAt:     order.CreatedAt,
		UpdatedAt:     order.UpdatedAt,
	}
}

// toOrderDetailDTO converts the order with its line items and summary to the
// external representation.
func toOrderDetailDTO(detail models.OrderDetail) orderDetailDTO {
	out := orderDetailDTO{
		orderDTO: toOrderDTO(detail.Order),
		Items:    make([]lineItemDTO, 0, len(detail.Items)),
		Summary:  toSummaryDTO(detail.Summary, detail.Total),
	}
	// The loop is walked by index: the line item struct is large and copying it
	// by value would carry a few hundred bytes for nothing on every turn.
	for i := range detail.Items {
		out.Items = append(out.Items, toLineItemDTO(detail.Items[i]))
	}
	return out
}

// toLineItemDTO converts the model to the external representation.
func toLineItemDTO(item models.OrderLineItem) lineItemDTO {
	return lineItemDTO{
		ID:            item.ID,
		OrderID:       item.OrderID,
		VariantID:     item.VariantID,
		Title:         item.Title,
		Quantity:      item.Quantity,
		UnitPrice:     item.UnitPrice,
		Subtotal:      item.Subtotal,
		DiscountTotal: item.DiscountTotal,
		TaxTotal:      item.TaxTotal,
		TaxRateBps:    item.TaxRateBps,
		Total:         item.Total,
		Metadata:      item.Metadata,
		CreatedAt:     item.CreatedAt,
		UpdatedAt:     item.UpdatedAt,
	}
}

// toSummaryDTO converts the summary to the external representation; the
// outstanding amount is computed from the order total.
func toSummaryDTO(summary models.OrderSummary, orderTotal int64) summaryDTO {
	return summaryDTO{
		ID:            summary.ID,
		OrderID:       summary.OrderID,
		PaidTotal:     summary.PaidTotal,
		RefundedTotal: summary.RefundedTotal,
		Outstanding:   summary.Outstanding(orderTotal),
		CreatedAt:     summary.CreatedAt,
		UpdatedAt:     summary.UpdatedAt,
	}
}

// toReturnDTO converts the model to the external representation.
func toReturnDTO(ret models.Return) returnDTO {
	return returnDTO{
		ID:           ret.ID,
		OrderID:      ret.OrderID,
		Status:       ret.Status.String(),
		RefundAmount: ret.RefundAmount,
		Reason:       ret.Reason,
		Note:         ret.Note,
		Metadata:     ret.Metadata,
		ReceivedAt:   ret.ReceivedAt,
		CanceledAt:   ret.CanceledAt,
		CreatedAt:    ret.CreatedAt,
		UpdatedAt:    ret.UpdatedAt,
	}
}

// toExchangeDTO converts the model to the external representation.
func toExchangeDTO(exchange models.Exchange) exchangeDTO {
	return exchangeDTO{
		ID:            exchange.ID,
		OrderID:       exchange.OrderID,
		Status:        exchange.Status.String(),
		DifferenceDue: exchange.DifferenceDue,
		Note:          exchange.Note,
		Metadata:      exchange.Metadata,
		CompletedAt:   exchange.CompletedAt,
		CanceledAt:    exchange.CanceledAt,
		CreatedAt:     exchange.CreatedAt,
		UpdatedAt:     exchange.UpdatedAt,
	}
}

// toClaimDTO converts the model to the external representation.
func toClaimDTO(claim models.Claim) claimDTO {
	return claimDTO{
		ID:           claim.ID,
		OrderID:      claim.OrderID,
		Type:         claim.Type.String(),
		Status:       claim.Status.String(),
		RefundAmount: claim.RefundAmount,
		Reason:       claim.Reason,
		Note:         claim.Note,
		Metadata:     claim.Metadata,
		CompletedAt:  claim.CompletedAt,
		CanceledAt:   claim.CanceledAt,
		CreatedAt:    claim.CreatedAt,
		UpdatedAt:    claim.UpdatedAt,
	}
}

// --- helpers -----------------------------------------------------------------

// decodeBody decodes the request body; the body is MANDATORY.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	return decodeJSON(w, r, dst, false)
}

// decodeOptionalBody decodes requests whose body MAY BE LEFT EMPTY.
//
// On the cancel endpoint the body only carries an optional reason; counting an
// empty body as an error would have made canceling without a reason impossible.
// If a body was sent, the whole strictness of [decodeJSON] (including the
// rejection of unknown fields) applies.
func decodeOptionalBody(w http.ResponseWriter, r *http.Request, dst any) error {
	return decodeJSON(w, r, dst, true)
}

// decodeJSON decodes the request body.
//
// The body size is limited and UNRECOGNIZED FIELDS are rejected: a silently
// swallowed field means a setting the client believes it sent but which is
// never applied.
//
// If allowEmpty is true then sending no body at all is valid and dst stays at
// its zero value. The emptiness check is done by looking NOT at Content-Length
// but at the io.EOF of the decoding: in a chunked request the length is -1 and
// a check that looked at the length would misclassify those requests.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, allowEmpty bool) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			if allowEmpty {
				return nil
			}
			return coreerrors.Invalid(codeInvalidRequest, "the request body cannot be empty")
		}
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"the request body could not be parsed")
	}
	// If more than a single JSON value was sent, that too is a client error.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return coreerrors.Invalid(codeInvalidRequest,
			"the request body has to be a single JSON object")
	}
	return nil
}

// parsePage decodes the limit/offset query parameters.
func parsePage(r *http.Request) (service.Page, error) {
	limit, err := parseInt64Param(r, "limit")
	if err != nil {
		return service.Page{}, err
	}
	offset, err := parseInt64Param(r, "offset")
	if err != nil {
		return service.Page{}, err
	}
	// "after" and "offset" name two different positions; honoring both would
	// serve the page N rows past the cursor, which neither of them asked for.
	raw := r.URL.Query().Get("after")
	if raw != "" && offset != 0 {
		return service.Page{}, coreerrors.Invalid(codeInvalidRequest,
			`"after" and "offset" name two different positions; send one of them`)
	}

	after, err := corepage.Decode(service.OrderListing, raw)
	if err != nil {
		return service.Page{}, err
	}

	page := service.Page{Limit: limit, Offset: offset, After: after}
	if page.Limit == 0 {
		// So that the limit field in the response really shows the limit that
		// is applied, the default is made visible here as well.
		page.Limit = service.DefaultLimit
	}
	return page, nil
}

// parseInt64Param converts a query parameter to an integer; returns 0 when it
// is absent.
func parseInt64Param(r *http.Request, name string) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"%s has to be an integer: %q", name, raw)
	}
	return value, nil
}

// orderID reads the order id from the request.
func orderID(r *http.Request) string {
	return chi.URLParam(r, paramOrderID)
}
