package service

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// This file is the CROSS-MODULE surface of the payment module (ADR 0001,
// ADR 0006).
//
// Phase 6's complete_cart saga (internal/workflows) CANNOT import this module.
// The solution is the same as the interop.go in the region/cart modules:
// publishing a surface that uses only PRIMITIVE and stdlib types. The consumer
// defines its own narrow interface, this type satisfies it STRUCTURALLY, and
// it is resolved from the container under the name "payment.interop".
//
// The reason is Go's structural conformance rule: since the consumer cannot
// import payment, it cannot name a type such as models.PaymentSession in its
// signature; the moment it names one, that becomes ANOTHER type defined in its
// own package and the concrete service does not satisfy the consumer's
// interface.
//
// The surface is DELIBERATELY narrow and was picked according to the saga's
// need: open a collection, open a session, authorize, capture, cancel
// (compensate), refund and read the status. Every method added here raises the
// cost of pulling payment out into a separate service.
//
// # The surface carries the AMOUNT
//
// The read methods return not only the status string but the AMOUNT as well
// ([Interop.Collection], [Interop.Authorize]). It is essential that the saga
// verify for itself that the payment is complete: the status string is a
// derived summary and it can change in a way that shows a short payment as a
// complete one. Returning a number makes the verification independent of this
// module's status derivation.

// Interop turns the payment service into the cross-module PRIMITIVE surface.
//
// It makes no decisions: it only translates the signature. All of the business
// rules stay on [Service]; adding a rule here would mean the same rule
// drifting apart in two places.
type Interop struct {
	svc *Service
}

// NewInterop sets up the cross-module surface for the given service.
func NewInterop(svc *Service) *Interop { return &Interop{svc: svc} }

// CreateCollection opens a payment collection for a reference and returns its
// identifier.
//
// reference is the identifier of the cart or of the order; this module does
// not validate it (Principle 2.2 — the link is established through Module
// Links).
func (i *Interop) CreateCollection(
	ctx context.Context,
	reference, currencyCode string,
	amount int64,
) (string, error) {
	col, err := i.svc.CreatePaymentCollection(ctx, CreateCollectionInput{
		Reference:    reference,
		CurrencyCode: currencyCode,
		Amount:       amount,
	})
	if err != nil {
		return "", err
	}
	return col.ID, nil
}

// OpenSession opens a payment session at a provider for the collection and
// returns the session's identifier.
//
// No amount is given: the session is opened for the whole of the collection's
// NOT YET HELD amount. That is what the saga needs, and partial payment flows
// (a capture split across more than one session) are the business of the admin
// API, not of this surface.
//
// A second call with the same idempotencyKey DOES NOT open a NEW session, it
// returns the existing session's identifier; that is what makes sure a second
// capture is not attempted on the customer when the saga retries a step.
func (i *Interop) OpenSession(
	ctx context.Context,
	collectionID, providerID, idempotencyKey string,
) (string, error) {
	return i.OpenSessionWithData(ctx, collectionID, providerID, idempotencyKey, nil)
}

// OpenSessionWithData opens the session with free-form data to be passed on to
// the provider.
//
// data is a raw JSON object (e.g. a card token) and it is handed to the
// provider as is. If it is given as empty or as JSON null, the data is
// ignored.
//
// Numbers are decoded as json.Number; this is how an integer passing through
// the map is kept from turning into a float64 and drifting into exponential
// notation ("1e+15") while being re-encoded, or from losing precision at large
// values. The data passed on to the provider CAN CONTAIN AN AMOUNT (see the
// behavior keys in the manual package) and money must not go through floating
// point at any stage (plan Section 8).
func (i *Interop) OpenSessionWithData(
	ctx context.Context,
	collectionID, providerID, idempotencyKey string,
	data json.RawMessage,
) (string, error) {
	decoded, err := decodeInteropData(data)
	if err != nil {
		return "", err
	}

	ses, err := i.svc.CreateSession(ctx, collectionID, providerID, CreateSessionInput{
		IdempotencyKey: idempotencyKey,
		Data:           decoded,
	})
	if err != nil {
		return "", err
	}
	return ses.ID, nil
}

// Authorize authorizes the session; it returns the session's NEW status and
// the amount that was actually PUT ON HOLD.
//
// If the provider declines, an error is returned (errors.Conflict, code
// [CodeAuthorizationDeclined]) and the session is durably written as "failed".
// The saga step blows up with that error and the compensation chain kicks in;
// had the decline been swallowed silently, an unpaid order would have been
// confirmed.
//
// Returning the amount put on hold is mandatory: the provider can authorize
// PARTIALLY, and in that case the status still becomes "authorized". A saga
// that looked only at the status would take a payment put on hold below what
// was asked for as a complete one.
func (i *Interop) Authorize(ctx context.Context, sessionID string) (status string, authorized int64, err error) {
	ses, err := i.svc.AuthorizePayment(ctx, sessionID)
	if err != nil {
		return "", 0, err
	}
	return ses.Status.String(), ses.AuthorizedAmount, nil
}

// Capture captures the amount that was put on hold and returns the identifier
// of the capture that came about. If amount is zero, the whole of the amount
// on hold is drawn.
func (i *Interop) Capture(ctx context.Context, sessionID string, amount int64) (string, error) {
	payment, err := i.svc.CapturePayment(ctx, sessionID, amount)
	if err != nil {
		return "", err
	}
	return payment.ID, nil
}

// Cancel cancels the session; THIS IS THE SAGA COMPENSATION and IT IS
// IDEMPOTENT.
//
// If it is called twice, the second call DOES NOT return an error. If the
// session identifier is unknown, errors.NotFound is returned; the compensation
// does not silently swallow a record that does not exist.
func (i *Interop) Cancel(ctx context.Context, sessionID string) error {
	return i.svc.CancelPayment(ctx, sessionID)
}

// Refund refunds the capture and returns the identifier of the refund record
// that came about. If amount is zero, the whole of the remaining amount is
// refunded.
func (i *Interop) Refund(ctx context.Context, paymentID string, amount int64, reason string) (string, error) {
	refund, err := i.svc.RefundPayment(ctx, paymentID, amount, reason)
	if err != nil {
		return "", err
	}
	return refund.ID, nil
}

// RefundCollection refunds an amount against a collection and returns the total
// that actually went back.
//
// The caller names a COLLECTION rather than a capture because how a collected
// amount is split across captures is this module's bookkeeping; the rationale
// is on [Service.RefundCollection]. A zero amount refunds everything left.
//
// The returned total is what the caller has to record on its own side — the
// order module writes it into the order's summary — and it is returned rather
// than assumed, because a refund can legitimately be capped by what the
// collection still holds.
func (i *Interop) RefundCollection(
	ctx context.Context, collectionID string, amount int64, reason string,
) (int64, error) {
	refunds, err := i.svc.RefundCollection(ctx, collectionID, amount, reason)

	var total int64
	for idx := range refunds {
		total += refunds[idx].Amount
	}
	if err != nil {
		// The total is returned ALONGSIDE the error: a partly made refund moved
		// real money, and a caller told only "it failed" would record nothing
		// and retry the whole amount.
		return total, err
	}

	return total, nil
}

// Collection returns the collection's current status and its AMOUNTS.
//
// The saga is obliged to verify for itself that the payment is COMPLETE and
// its only window is this surface; the status string is not enough on its own.
// The status is a SUMMARY derived from the amounts (see
// [models.CollectionStatusFor]), and adding a new value for every new
// distinction would have meant the consumer having to memorize strings. When
// the amounts are returned, the saga's rule is a single line:
// captured >= amount.
//
// The returned values are, in order, the collection's status, the amount that
// must be collected, and the amounts put on hold, captured and refunded (all
// of them minor unit).
//
// The signature is long and this is deliberate: since the consumer CANNOT
// import this package it cannot name a shared struct type (ADR 0006), and the
// amounts can only be carried as separate primitive values. All of them being
// returned FROM A SINGLE read additionally keeps the saga from seeing a
// collection inconsistently when it changes between two calls.
//
//nolint:gocritic // The result count comes from ADR 0006's primitive-type constraint; the rationale is above.
func (i *Interop) Collection(ctx context.Context, collectionID string) (
	status string,
	amount, authorized, captured, refunded int64,
	err error,
) {
	col, err := i.svc.GetPaymentCollection(ctx, collectionID)
	if err != nil {
		return "", 0, 0, 0, 0, err
	}
	return col.Status.String(), col.Amount, col.AuthorizedAmount, col.CapturedAmount, col.RefundedAmount, nil
}

// SessionStatus returns the session's current status.
//
// The tests that verify the compensation really runs look at this: a canceled
// session returns "canceled" and the saga's undo chain becomes visible to the
// eye.
func (i *Interop) SessionStatus(ctx context.Context, sessionID string) (string, error) {
	ses, err := i.svc.GetPaymentSession(ctx, sessionID)
	if err != nil {
		return "", err
	}
	return ses.Status.String(), nil
}

// decodeInteropData turns the raw JSON body into the map that is given to the
// provider.
//
// Numbers are left as json.Number; for the rationale see
// [Interop.OpenSessionWithData].
func decodeInteropData(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, CodeInvalidInput,
			"the session data could not be decoded; it must be a JSON object")
	}
	return out, nil
}
