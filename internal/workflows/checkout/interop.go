package checkout

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bdrtr/gobit/core/errors"
)

// InteropName is the name of the order completion flow in the container (ADR
// 0001/0006).
//
// This package declares the name, the COMPOSITION ROOT performs the registration
// (internal/app): the flow resolves seven module surfaces from the container and
// can only be constructed AFTER every module has Registered. The consumer is the
// cart MODULE — whoever owns the HTTP endpoint that turns a cart into an order
// owns the cart — and it repeats the name as a STRING in its own package (see
// CartCompletionName in the cart module).
const InteropName = "workflows.checkout.interop"

// CodeInteropRequestInvalid reports that a completion request that cannot be
// decoded has arrived.
const CodeInteropRequestInvalid = "checkout_interop_request_invalid"

// Interop translates the order completion flow into a cross-module PRIMITIVE
// surface.
//
// # Why JSON
//
// [CompleteCartInput] carries six fields, [CompleteCartResult] ten, and both are
// types of this package; no module can name them (ADR 0006). That leaves two
// options: spreading the fields out into positional parameters, or passing the
// boundary through JSON. The first would produce a six-parameter signature, and
// swapping two strings of the same type (the provider id and the e-mail) would be
// invisible at compile time. JSON is the only structural format both sides can
// name, and the schema is written in ONE PLACE — [completeCartRequest] and
// [completeCartResponse]. The same pattern is used on the surfaces of the
// promotion, tax and fulfillment modules as well.
//
// # The surface is not the WHOLE flow
//
// The schema is deliberately NARROW: not every field the flow accepts is here,
// and not every field it produces passes through here. The rationales are written
// next to the fields; the common criterion is this — behind this surface there is
// a store endpoint reached with a publishable key, and every field opened up to
// it means a decision the customer gets to determine.
type Interop struct {
	w *Workflows
}

// NewInterop constructs the cross-module surface for the given flow.
func NewInterop(w *Workflows) *Interop { return &Interop{w: w} }

// completeCartRequest is the JSON schema of the completion request.
//
// The field names must be EXACTLY the same as the schema on the consumer side,
// and the match can only be proven by an integration test: because the two sides
// cannot import each other, the compiler does not check this boundary.
type completeCartRequest struct {
	// CartID is the cart to be completed; it is REQUIRED.
	CartID string `json:"cart_id"`
	// PaymentProviderID is the provider the payment will be opened at; it is
	// REQUIRED.
	//
	// It is the customer's CHOICE and that is why it is on the surface: the
	// server cannot pick which provider to be paid from on the customer's
	// behalf. It raises no authorization question — the name must resolve to a
	// provider REGISTERED on the server; an unrecognized name opens no capture,
	// it drops the flow.
	PaymentProviderID string `json:"payment_provider_id"`
	// PaymentData is the free-form JSON passed to the provider as is; it is
	// optional (card token, return address).
	PaymentData json.RawMessage `json:"payment_data,omitempty"`
	// PaymentSessionID is the payment the storefront opened for the cart
	// before completing it; it is optional, and required for a provider whose
	// customer pays on its own page (ADR 0438). It raises no authority
	// question: a session opened for another cart is refused, so naming one
	// cannot spend somebody else's payment.
	PaymentSessionID string `json:"payment_session_id,omitempty"`
	// CompletionSecret is the secret the opening of that payment handed the
	// storefront; it answers the completion of a cart the provider's report
	// completed (ADR 0438).
	CompletionSecret string `json:"completion_secret,omitempty"`
	// GiftCardCode is a gift card that pays first; it is optional (ADR 0209).
	GiftCardCode string `json:"gift_card_code,omitempty"`
	// PayFirstWith names the customer's balances that pay after the card and
	// before the provider; it is optional (ADR 0269).
	PayFirstWith []string `json:"pay_first_with,omitempty"`
	// Email is the order's contact address; it is optional.
	Email string `json:"email,omitempty"`
	// SalesChannelIDs are the channels the order is placed on; they are
	// optional and they NARROW the warehouses it can be reserved from.
	//
	// The storefront endpoint fills them from the request's own identity, so
	// they are not a client input: a caller cannot widen its own channel set by
	// sending one, because the module that sends this document reads them off
	// the publishable key rather than off the body. An administrative caller
	// sends none and nothing is narrowed — see
	// [CompleteCartInput.SalesChannelIDs].
	SalesChannelIDs []string `json:"sales_channel_ids,omitempty"`
	// ExpectedTotal is the total the caller had the customer CONFIRM (minor
	// unit).
	//
	// If it does not match the computed amount the flow returns errors.Conflict
	// and NO side effect is applied: the comparison happens before the saga's
	// first step, in the preparation where the totals are recomputed. Zero means
	// "do not compare" (see [CompleteCartInput.ExpectedTotal]) and that CANNOT
	// LOWER the amount — a caller skipping the comparison still pays the amount
	// the server computed, it only loses the chance to warn the customer if the
	// price has changed. The operator's offline completion compares zero too
	// (D199).
	ExpectedTotal int64 `json:"expected_total"`
	// OfflineOnly refuses a provider the checkout would capture; the operator's
	// completion sets it (ADR 0286), and a storefront's never does.
	OfflineOnly bool `json:"offline_only,omitempty"`
	// PlacedBy is the operator placing the order; the operator's completion
	// sets it from the caller's identity, and a storefront's never does (ADR
	// 0298).
	PlacedBy string `json:"placed_by,omitempty"`
	// IssueOrderToken asks for the order's access token in the answer; a
	// storefront's completion sets it and the operator's never does
	// (ADR 0439). It decides who receives a credential, which is why the
	// module that sends this document sets it from the route rather than from
	// the body.
	IssueOrderToken bool `json:"issue_order_token,omitempty"`

	// THE LOCATION IS NOT HERE and will not be. [CompleteCartInput.LocationID]
	// pins which WAREHOUSE the goods leave from; that is a shipping decision and
	// the flow makes it by asking the inventory + fulfillment modules per line.
	// Opening the field on this surface would mean letting the store client pick
	// the warehouse: it both tells the outside world about the stock topology and
	// would let a customer have their order shipped from whichever warehouse they
	// like. An ADMINISTRATIVE order that must leave a particular warehouse is not
	// this endpoint's subject and will get its own surface when it arrives.
}

// completeCartResponse is the JSON schema of the completion result.
//
// The schema is NARROWER than the flow's result. What stays outside, and why:
//
//   - payment_collection_id, payment_session_id, payment_id, reservation_ids:
//     these are the INTERNAL ids of the payment and inventory modules. They are
//     not needed to follow the order; publishing them would mean telling the
//     store client about internal structure it cannot use from any endpoint.
//   - warnings: these are faults that do NOT DROP the order but ask for a manual
//     repair, and their audience is the operator, not the customer. They are not
//     lost: the flow already logs them at ERROR level (see clearCartStep.Invoke).
//   - cart_completed, reservations_confirmed: the flag form of the same faults.
type completeCartResponse struct {
	// OrderID is the id of the created order.
	OrderID string `json:"order_id"`
	// CartID is the cart the order was born from.
	CartID string `json:"cart_id"`
	// CurrencyCode is the order's currency (ISO 4217).
	CurrencyCode string `json:"currency_code"`
	// Amount is the order's total (minor unit); the checkout captured it less
	// Outstanding.
	Amount int64 `json:"amount"`
	// Outstanding is what the order still owes: the part of an offline
	// method, paid later (ADR 0284).
	Outstanding int64 `json:"outstanding"`
	// OrderToken is the order's access token, when the request asked for one
	// and it could be issued (ADR 0439). It is the only copy: the order keeps
	// its digest.
	OrderToken string `json:"order_token,omitempty"`
}

// CompleteCartJSON turns the cart into an order.
//
// The request is in the [completeCartRequest] schema, the response in the
// [completeCartResponse] schema. The flow itself and the saga's compensation
// rules are in the [Workflows.CompleteCart] godoc; this method only translates
// the boundary and makes NO decision.
//
// A request body that cannot be decoded is errors.Invalid: the caller cannot
// import this package, so it builds the schema by hand and a typo can only be
// seen here. Silently continuing with zero values would mean trying to complete
// the cart with no provider and no confirmation.
func (i *Interop) CompleteCartJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error) {
	if len(request) == 0 {
		return nil, errors.Invalid(CodeInteropRequestInvalid,
			"order completion request cannot be empty")
	}

	var req completeCartRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, CodeInteropRequestInvalid,
			"order completion request could not be decoded")
	}

	result, err := i.w.CompleteCart(ctx, CompleteCartInput{
		CartID:            req.CartID,
		PaymentProviderID: req.PaymentProviderID,
		PaymentData:       req.PaymentData,
		PaymentSessionID:  req.PaymentSessionID,
		CompletionSecret:  req.CompletionSecret,
		GiftCardCode:      req.GiftCardCode,
		PayFirstWith:      req.PayFirstWith,
		Email:             req.Email,
		SalesChannelIDs:   req.SalesChannelIDs,
		ExpectedTotal:     req.ExpectedTotal,
		OfflineOnly:       req.OfflineOnly,
		PlacedBy:          req.PlacedBy,
		IssueOrderToken:   req.IssueOrderToken,
	})
	if err != nil {
		return nil, err
	}

	return json.Marshal(completeCartResponse{
		OrderID:      result.OrderID,
		CartID:       result.CartID,
		CurrencyCode: result.CurrencyCode,
		Amount:       result.Amount,
		Outstanding:  result.Outstanding,
		OrderToken:   result.OrderToken,
	})
}

// openPaymentRequest is the JSON schema of a storefront's request to open its
// cart's payment (ADR 0438).
//
// It is the completion's request less what the payment opened here cannot
// carry: no gift card and no balance pays first, because the session is opened
// for the whole total, and no operator opens one.
type openPaymentRequest struct {
	// CartID is the cart whose payment is opened; it is REQUIRED.
	CartID string `json:"cart_id"`
	// PaymentProviderID is the provider the session is opened at; it is
	// REQUIRED.
	PaymentProviderID string `json:"payment_provider_id"`
	// PaymentData is handed to the provider as it is.
	PaymentData json.RawMessage `json:"payment_data,omitempty"`
	// SalesChannelIDs are the request's channels, read from the identity, for
	// [completeCartRequest.SalesChannelIDs]'s reason.
	SalesChannelIDs []string `json:"sales_channel_ids,omitempty"`
	// ExpectedTotal is the total the customer confirmed (minor unit).
	ExpectedTotal int64 `json:"expected_total"`
}

// openPaymentResponse is the JSON schema of the opened payment.
//
// Unlike [completeCartResponse] it carries the payment's identifiers: the
// storefront holds the session, names it in the completion and cancels it when
// the cart changes, and the provider's data is what it shows the customer.
type openPaymentResponse struct {
	PaymentSessionID    string          `json:"payment_session_id"`
	PaymentCollectionID string          `json:"payment_collection_id"`
	ProviderID          string          `json:"provider_id"`
	Status              string          `json:"status"`
	Amount              int64           `json:"amount"`
	CurrencyCode        string          `json:"currency_code"`
	Data                json.RawMessage `json:"data,omitempty"`
	CompletionSecret    string          `json:"completion_secret"`
}

// OpenPaymentJSON opens a cart's payment before the cart is completed (ADR
// 0438).
//
// The request is in the [openPaymentRequest] schema and the response in the
// [openPaymentResponse] schema; the decisions are [Workflows.OpenPayment]'s and
// this method only translates the boundary, refusing a body it cannot decode
// for [Interop.CompleteCartJSON]'s reason.
func (i *Interop) OpenPaymentJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error) {
	if len(request) == 0 {
		return nil, errors.Invalid(CodeInteropRequestInvalid,
			"payment opening request cannot be empty")
	}

	var req openPaymentRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, CodeInteropRequestInvalid,
			"payment opening request could not be decoded")
	}

	result, err := i.w.OpenPayment(ctx, OpenPaymentInput{
		CartID:            req.CartID,
		SalesChannelIDs:   req.SalesChannelIDs,
		PaymentProviderID: req.PaymentProviderID,
		PaymentData:       req.PaymentData,
		ExpectedTotal:     req.ExpectedTotal,
	})
	if err != nil {
		return nil, err
	}

	return json.Marshal(openPaymentResponse(result))
}

// cancelPaymentRequest is the JSON schema of a storefront's request to cancel a
// payment it opened for its cart (ADR 0438).
type cancelPaymentRequest struct {
	// CartID is the cart the payment was opened for; it is REQUIRED.
	CartID string `json:"cart_id"`
	// PaymentSessionID is the session to cancel; it is REQUIRED.
	PaymentSessionID string `json:"payment_session_id"`
}

// CancelPaymentJSON cancels a payment the storefront opened for its cart; the
// request is in the [cancelPaymentRequest] schema and the decisions are
// [Workflows.CancelPayment]'s (ADR 0438).
func (i *Interop) CancelPaymentJSON(ctx context.Context, request json.RawMessage) error {
	if len(request) == 0 {
		return errors.Invalid(CodeInteropRequestInvalid, "payment cancel request cannot be empty")
	}

	var req cancelPaymentRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return errors.Wrap(err, errors.KindInvalid, CodeInteropRequestInvalid,
			"payment cancel request could not be decoded")
	}

	return i.w.CancelPayment(ctx, req.CartID, req.PaymentSessionID)
}

// CompleteReportedPayments sweeps the report queue once; the decisions are
// [Workflows.CompleteReportedPayments]'s (ADR 0438).
func (i *Interop) CompleteReportedPayments(ctx context.Context, now time.Time) (ReportPass, error) {
	return i.w.CompleteReportedPayments(ctx, now)
}

// PaymentReported is a provider's report that the customer paid one of its
// sessions, named by the provider's own id; it is what the composition root
// registers for a provider plugin to report through, and the decisions are
// [Workflows.PaymentReported]'s (ADR 0438).
func (i *Interop) PaymentReported(ctx context.Context, providerID, sessionID string) error {
	return i.w.PaymentReported(ctx, providerID, sessionID)
}
