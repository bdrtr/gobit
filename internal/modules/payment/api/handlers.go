package api

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// listProviders returns the IDs of the registered payment providers.
//
// It is bound to both the admin and the store surface: the storefront's payment
// step has to know which ways are open, and that information is not secret.
func (h *Handler) listProviders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	writeList(ctx, w, h.svc.ProviderIDs(ctx))
}

// createCollectionRequest is the body of POST /admin/v1/payment-collections.
type createCollectionRequest struct {
	Reference string `json:"reference"`
	// Amount is a minor unit INTEGER; being a pointer keeps the distinction
	// between "not sent" and "zero sent", and both are rejected, but with
	// different messages.
	Amount       *int64         `json:"amount"`
	CurrencyCode string         `json:"currency_code"`
	Metadata     map[string]any `json:"metadata"`
}

// createCollection creates a new payment collection.
func (h *Handler) createCollection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createCollectionRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if body.Amount == nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest, "amount is required"))
		return
	}

	col, err := h.svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference:    body.Reference,
		Amount:       *body.Amount,
		CurrencyCode: body.CurrencyCode,
		Metadata:     body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toCollectionDTO(col)})
}

// listCollections returns the collections, paged.
func (h *Handler) listCollections(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	in := service.ListCollectionsInput{Page: page}
	if raw := r.URL.Query().Get("reference"); raw != "" {
		in.Reference = &raw
	}
	if raw := r.URL.Query().Get("status"); raw != "" {
		in.Status = &raw
	}

	collections, count, err := h.svc.ListPaymentCollections(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]collectionDTO, 0, len(collections))
	for i := range collections {
		data = append(data, toCollectionDTO(collections[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   data,
		Count:  count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// getCollection returns the collection by its ID.
func (h *Handler) getCollection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	col, err := h.svc.GetPaymentCollection(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toCollectionDTO(col)})
}

// createSessionRequest is the ADMIN surface's body for opening a payment
// session.
type createSessionRequest struct {
	ProviderID string `json:"provider_id"`
	// If Amount is not given, the session is opened for the whole of the
	// collection's REMAINING amount. Splitting the payment across more than one
	// session is admin work; this field DOES NOT EXIST on the store surface
	// (see [createStoreSessionRequest]).
	Amount int64 `json:"amount"`
	// IdempotencyKey is required: a second request with the same key does NOT
	// open a NEW session.
	IdempotencyKey string `json:"idempotency_key"`
	// Data is free-form data passed on to the provider.
	Data json.RawMessage `json:"data"`
}

// createStoreSessionRequest is the STORE surface's body for opening a payment
// session.
//
// The amount field is absent ON PURPOSE: a session opened from the store
// endpoint always covers the whole of the collection's remaining amount. A
// customer being able to set the session's amount from their own browser would
// have meant opening a session of 1 unit for an order of 50,000 and getting
// past the payment. If the field is sent in the body, the request is rejected
// (see [decodeBody] — unknown fields are an error).
type createStoreSessionRequest struct {
	ProviderID string `json:"provider_id"`
	// IdempotencyKey is required: a second request with the same key does NOT
	// open a NEW session.
	IdempotencyKey string `json:"idempotency_key"`
	// Data is free-form data passed on to the provider; keys that steer the
	// provider's BEHAVIOR are not accepted here.
	Data json.RawMessage `json:"data"`
}

// storeBlockedDataKeys are the data keys the store surface does NOT PASS ON to
// the provider.
//
// The manual provider's test hooks (decline injection, partial authorization)
// are read from the session's free-form data and stored with the session. If
// they were passed through the endpoint open to the customer, the customer
// could write the outcome of their own payment: on a collection of 50,000 they
// could have 1 unit held and show the order as paid. The hooks stay on the
// admin surface; whoever calls there can already trigger the capture too.
//
// The list is not silently FILTERED, the request is REJECTED: a swallowed field
// is a setting the client believes it sent but that is never applied (for the
// same reasoning see [decodeBody]).
var storeBlockedDataKeys = []string{
	manual.DataKeyOutcome,
	manual.DataKeyDeclineReason,
	manual.DataKeyAuthorizedAmount,
}

// createSession opens a payment session for the collection from the ADMIN
// surface.
func (h *Handler) createSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createSessionRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data, err := decodeSessionData(body.Data)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	ses, err := h.svc.CreateSession(ctx, chi.URLParam(r, "id"), body.ProviderID, service.CreateSessionInput{
		Amount:         body.Amount,
		IdempotencyKey: body.IdempotencyKey,
		Data:           data,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toSessionDTO(ses)})
}

// createStoreSession opens a payment session for the collection from the STORE
// surface.
//
// It differs from the admin endpoint in two ways, and both prevent the customer
// from steering the payment flow in their own favor: the amount is NOT taken
// FROM THE CLIENT (it is always the whole of the remainder) and the provider's
// behavior keys are rejected.
func (h *Handler) createStoreSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createStoreSessionRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data, err := decodeSessionData(body.Data)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	for _, key := range storeBlockedDataKeys {
		if _, ok := data[key]; ok {
			corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
				"a %s key the store surface does not accept: %q", "data", key))
			return
		}
	}

	ses, err := h.svc.CreateSession(ctx, chi.URLParam(r, "id"), body.ProviderID, service.CreateSessionInput{
		// No amount is given: the session is opened for the whole of the
		// collection's REMAINING amount.
		IdempotencyKey: body.IdempotencyKey,
		Data:           data,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toSessionDTO(ses)})
}

// listSessions returns the collection's sessions.
func (h *Handler) listSessions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	sessions, err := h.svc.ListPaymentSessions(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]sessionDTO, 0, len(sessions))
	for i := range sessions {
		data = append(data, toSessionDTO(sessions[i]))
	}
	writeList(ctx, w, data)
}

// getSession returns the session by its ID.
func (h *Handler) getSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ses, err := h.svc.GetPaymentSession(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toSessionDTO(ses)})
}

// authorizeSession authorizes the session.
//
// If the provider declines, the service returns errors.Conflict and the
// response is 409: a decline is not a server error, but the requested
// transition did not happen either.
func (h *Handler) authorizeSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ses, err := h.svc.AuthorizePayment(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toSessionDTO(ses)})
}

// amountRequest is the shape of bodies that carry only an amount.
type amountRequest struct {
	// Amount, when zero or not given at all, means "the whole".
	Amount int64 `json:"amount"`
}

// captureSession captures the held amount.
func (h *Handler) captureSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	body, err := decodeOptionalAmount(w, r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	payment, err := h.svc.CapturePayment(ctx, chi.URLParam(r, "id"), body.Amount)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toPaymentDTO(payment)})
}

// cancelSession cancels the session (saga compensation).
//
// It is IDEMPOTENT: it returns 204 for a session that is already canceled too.
func (h *Handler) cancelSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.CancelPayment(ctx, chi.URLParam(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// cancelStoreSession is the customer releasing a payment session they opened
// themselves.
//
// It makes the SAME service call as [Handler.cancelSession] on the admin
// surface; the reason it is a separate endpoint is authorization (the admin
// route needs an admin identity and [ScopeWrite], the store route only the
// publishable key), not a difference in behavior.
//
// Its reason to exist is the reservation: an open session covers the
// collection's remaining amount, and that is what prevents a double capture.
// Without a way to release it the customer could not change the payment METHOD
// and would stay locked until an administrator stepped in.
//
// It is IDEMPOTENT: it returns 204 for a session that is already canceled too.
func (h *Handler) cancelStoreSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.CancelPayment(ctx, chi.URLParam(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// listPayments returns the collection's captures.
func (h *Handler) listPayments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	payments, err := h.svc.ListPayments(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]paymentDTO, 0, len(payments))
	for i := range payments {
		data = append(data, toPaymentDTO(payments[i]))
	}
	writeList(ctx, w, data)
}

// getPayment returns the capture by its ID.
func (h *Handler) getPayment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	payment, err := h.svc.GetPayment(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toPaymentDTO(payment)})
}

// refundRequest is the body of POST /admin/v1/payments/{id}/refunds.
type refundRequest struct {
	// If Amount is zero or not given at all, the whole of the remaining amount
	// is refunded.
	Amount int64  `json:"amount"`
	Reason string `json:"reason"`
}

// refundPayment refunds the capture.
func (h *Handler) refundPayment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body refundRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	refund, err := h.svc.RefundPayment(ctx, chi.URLParam(r, "id"), body.Amount, body.Reason)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toRefundDTO(refund)})
}

// listRefunds returns the capture's refunds.
func (h *Handler) listRefunds(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	refunds, err := h.svc.ListRefunds(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]refundDTO, 0, len(refunds))
	for i := range refunds {
		data = append(data, toRefundDTO(refunds[i]))
	}
	writeList(ctx, w, data)
}

// decodeOptionalAmount decodes amount requests whose body is OPTIONAL.
//
// On the capture endpoint, its only caller, sending no body is valid and means
// "the whole"; counting an empty body as an error would force the most common
// call to write a needless JSON object. The cancel endpoints read no body.
func decodeOptionalAmount(w http.ResponseWriter, r *http.Request) (amountRequest, error) {
	var body amountRequest
	if r.ContentLength == 0 {
		return body, nil
	}
	if err := decodeBody(w, r, &body); err != nil {
		return amountRequest{}, err
	}
	return body, nil
}

// decodeSessionData converts the raw data to be passed on to the provider into
// a map.
//
// Numbers are decoded as json.Number: an integer that passes through the map
// and turns into a float64 can slip into exponent notation when it is
// re-encoded, and money must not touch floating point at any stage (plan
// Section 8).
func decodeSessionData(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"the data field has to be a JSON object")
	}
	return out, nil
}
