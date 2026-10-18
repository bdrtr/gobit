package api

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// An order changes hands (ADR 0450).
//
// A signed-in shopper who can open the order — one of its access tokens in
// the header — asks for it at POST /store/v1/orders/{id}/transfer-requests,
// and gobit mails a token to the order's address. Whoever reads that mailbox
// answers at /store/v1/order-transfers/{transferId}/accept or /decline with
// the token in the body; the shopper withdraws the request at .../cancel. An
// operator lists an order's transfers and moves it at once under
// /admin/v1/orders/{id}/transfers.
//
// The answering routes are not under /store/v1/orders/{id}: their caller
// holds the mail's token, not the order's, and may be signed in as nobody.

// The routes of an order's transfers.
const (
	pathStoreTransferRequests = "/store/v1/orders/{id}/transfer-requests"
	pathStoreTransferAccept   = "/store/v1/order-transfers/{transferId}/accept"
	pathStoreTransferDecline  = "/store/v1/order-transfers/{transferId}/decline"
	pathStoreTransferCancel   = "/store/v1/order-transfers/{transferId}/cancel"
	pathAdminOrderTransfers   = "/admin/v1/orders/{id}/transfers"
	// paramTransferID is the transfer a storefront answer names.
	paramTransferID = "transferId"
)

// codeTransferUnproven refuses a transfer request or withdrawal the customer
// identity proves nobody for: the receiver is the proven customer.
const codeTransferUnproven = "order_transfer_unproven"

// Transfers is the surface the transfer routes need from the service.
type Transfers interface {
	RequestTransfer(ctx context.Context, orderID, receiverID string) (models.OrderTransfer, error)
	AcceptTransfer(ctx context.Context, transferID, token string) (models.OrderTransfer, error)
	DeclineTransfer(ctx context.Context, transferID, token string) (models.OrderTransfer, error)
	CancelTransfer(ctx context.Context, transferID, customerID string) (models.OrderTransfer, error)
	MoveOrder(ctx context.Context, orderID, customerID, expectedCustomerID string) (models.OrderTransfer, error)
	ListTransfers(ctx context.Context, orderID string, page service.Page) ([]models.OrderTransfer, int64, error)
	// Now is the clock a request's expiry is read with.
	Now() time.Time
}

// WithTransfers gives the handler the transfer surface; without one the
// transfer routes fail closed.
func (h *Handler) WithTransfers(transfers Transfers) *Handler {
	h.transfers = transfers
	return h
}

// transferSurface returns the surface or the error that ends the request.
func (h *Handler) transferSurface() (Transfers, error) {
	if h.transfers == nil {
		return nil, coreerrors.Internal(codeFlowUnavailable,
			"the order transfer surface is not bound; an order cannot change hands without it")
	}

	return h.transfers, nil
}

// storeTransferDTO is a transfer as the storefront sees it: no customer and
// no address, which its caller either is or was mailed at.
type storeTransferDTO struct {
	ID      string `json:"id"`
	OrderID string `json:"order_id"`
	// Status is requested, accepted, declined, canceled or expired.
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
}

// adminTransferDTO is a transfer as an operator sees it.
type adminTransferDTO struct {
	storeTransferDTO
	// RequestedBy is customer or operator.
	RequestedBy    string `json:"requested_by"`
	FromCustomerID string `json:"from_customer_id,omitempty"`
	ToCustomerID   string `json:"to_customer_id"`
	FromEmail      string `json:"from_email,omitempty"`
}

// toStoreTransferDTO converts a transfer, its status read at now.
func toStoreTransferDTO(t models.OrderTransfer, now time.Time) storeTransferDTO {
	return storeTransferDTO{
		ID:        t.ID,
		OrderID:   t.OrderID,
		Status:    string(t.StatusAt(now)),
		CreatedAt: t.CreatedAt,
		ExpiresAt: t.ExpiresAt,
		ClosedAt:  t.ClosedAt,
	}
}

// toAdminTransferDTO converts a transfer for an operator.
func toAdminTransferDTO(t models.OrderTransfer, now time.Time) adminTransferDTO {
	return adminTransferDTO{
		storeTransferDTO: toStoreTransferDTO(t, now),
		RequestedBy:      string(t.RequestedBy),
		FromCustomerID:   t.FromCustomerID,
		ToCustomerID:     t.ToCustomerID,
		FromEmail:        t.FromEmail,
	}
}

// transferAnswerRequest is the body of an accept and a decline.
type transferAnswerRequest struct {
	// Token is the transfer token the request's mail carried.
	Token string `json:"token"`
}

// moveOrderRequest is the body of an operator's move.
type moveOrderRequest struct {
	// CustomerID is the customer the order moves to.
	CustomerID string `json:"customer_id"`
	// ExpectedCustomerID is the customer the operator read the order with;
	// empty for a guest's order. It is required: a move drawn from a page
	// another move overtook is refused rather than undoing it.
	ExpectedCustomerID *string `json:"expected_customer_id"`
}

// storeRequestTransfer asks for the order to become the proven customer's.
func (h *Handler) storeRequestTransfer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.storeOrderAccess(w, r); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	transfers, err := h.transferSurface()
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	receiver, err := h.provenTransferCustomer(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	transfer, err := transfers.RequestTransfer(ctx, orderID(r), receiver)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated,
		singleEnvelope{Data: toStoreTransferDTO(transfer, transfers.Now())})
}

// storeAcceptTransfer answers a request yes with the mailed token.
func (h *Handler) storeAcceptTransfer(w http.ResponseWriter, r *http.Request) {
	h.storeAnswerTransfer(w, r, Transfers.AcceptTransfer)
}

// storeDeclineTransfer answers a request no with the mailed token.
func (h *Handler) storeDeclineTransfer(w http.ResponseWriter, r *http.Request) {
	h.storeAnswerTransfer(w, r, Transfers.DeclineTransfer)
}

// storeAnswerTransfer reads the token and hands it to answer.
func (h *Handler) storeAnswerTransfer(
	w http.ResponseWriter, r *http.Request,
	answer func(Transfers, context.Context, string, string) (models.OrderTransfer, error),
) {
	ctx := r.Context()
	w.Header().Set("Cache-Control", "no-store")

	transfers, err := h.transferSurface()
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	var body transferAnswerRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	transfer, err := answer(transfers, ctx, chi.URLParam(r, paramTransferID), body.Token)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toStoreTransferDTO(transfer, transfers.Now())})
}

// storeCancelTransfer withdraws the proven customer's own request.
func (h *Handler) storeCancelTransfer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	w.Header().Set("Cache-Control", "no-store")

	transfers, err := h.transferSurface()
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	customer, err := h.provenTransferCustomer(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	transfer, err := transfers.CancelTransfer(ctx, chi.URLParam(r, paramTransferID), customer)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toStoreTransferDTO(transfer, transfers.Now())})
}

// provenTransferCustomer is the customer the request proves, or the refusal
// of one that proves nobody. An identity that could not reach a verdict
// answers its own class (ADR 0371).
func (h *Handler) provenTransferCustomer(r *http.Request) (string, error) {
	proven, err := corehttp.ProvenCustomerIfAny(h.identity, r)
	if err != nil {
		return "", err
	}
	if proven == "" {
		return "", coreerrors.Unauthorized(codeTransferUnproven,
			"an order changes hands to a signed-in customer, and this request proves none")
	}

	return proven, nil
}

// adminListTransfers returns a page of the order's transfers, newest first.
func (h *Handler) adminListTransfers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	transfers, err := h.transferSurface()
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	items, count, err := transfers.ListTransfers(ctx, orderID(r), page)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	now := transfers.Now()
	data := make([]adminTransferDTO, 0, len(items))
	for i := range items {
		data = append(data, toAdminTransferDTO(items[i], now))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data: data, Count: count, Offset: page.Offset, Limit: page.Limit,
	})
}

// adminMoveOrder moves the order to another customer at once.
func (h *Handler) adminMoveOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	transfers, err := h.transferSurface()
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	var body moveOrderRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if body.ExpectedCustomerID == nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
			"a move names the customer the order was read with as \"expected_customer_id\", "+
				"empty for a guest's order"))
		return
	}

	transfer, err := transfers.MoveOrder(ctx, orderID(r), body.CustomerID, *body.ExpectedCustomerID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated,
		singleEnvelope{Data: toAdminTransferDTO(transfer, transfers.Now())})
}
