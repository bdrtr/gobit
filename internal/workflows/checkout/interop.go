package checkout

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// InteropName is the name of the order completion flow in the container (ADR
// 0001/0006).
//
// This package declares the name, the COMPOSITION ROOT performs the registration
// (cmd/server): the flow resolves seven module surfaces from the container and
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
	// Email is the order's contact address; it is optional.
	Email string `json:"email,omitempty"`
	// ExpectedTotal is the total the caller had the customer CONFIRM (minor
	// unit).
	//
	// If it does not match the computed amount the flow returns errors.Conflict
	// and NO side effect is applied: the comparison happens before the saga's
	// first step, in the preparation where the totals are recomputed. Zero means
	// "do not compare" (see [CompleteCartInput.ExpectedTotal]) and that CANNOT
	// LOWER the amount — a caller skipping the comparison still pays the amount
	// the server computed, it only loses the chance to warn the customer if the
	// price has changed.
	ExpectedTotal int64 `json:"expected_total"`

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
	// CurrencyCode is the currency that was captured (ISO 4217).
	CurrencyCode string `json:"currency_code"`
	// Amount is the captured amount (minor unit).
	Amount int64 `json:"amount"`
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
		Email:             req.Email,
		ExpectedTotal:     req.ExpectedTotal,
	})
	if err != nil {
		return nil, err
	}

	return json.Marshal(completeCartResponse{
		OrderID:      result.OrderID,
		CartID:       result.CartID,
		CurrencyCode: result.CurrencyCode,
		Amount:       result.Amount,
	})
}
