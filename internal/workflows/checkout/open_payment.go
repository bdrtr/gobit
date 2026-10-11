package checkout

// This file holds the payment a storefront opens BEFORE it completes the cart
// (ADR 0438), and the rules a completion that names it is held to.
//
// A provider whose customer pays on the provider's own page — PayTR's iframe —
// cannot be driven: the session is a page the customer has to be shown, and
// the money moves when the customer pays there, at a moment of their choosing,
// which the provider reports by posting back. Opened inside the completion, as
// the saga opens every other provider's, the page's token never left the
// server, the authorization asked a provider that had been told nothing, and
// no order could ever be placed. So the storefront opens the payment first and
// shows the page, and the completion authorizes what was paid.

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
)

// The payment module's session statuses this package reads. They are spelled
// here for [GiftCardProviderID]'s reason: this package cannot import the module
// (ADR 0006), and internal/arch holds them to the module's own and to the core
// provider contract's, which a provider reports its own ledger in.
const (
	SessionPending    = "pending"
	SessionAuthorized = "authorized"
	SessionCaptured   = "captured"
	SessionCanceled   = "canceled"
	SessionFailed     = "failed"
)

// paymentSessionKeyPrefix is the part of an opened session's idempotency key
// before its collection's id.
//
// Every opening makes a collection of its own, so the key is never reused; it
// exists because the payment module requires one, and the prefix says in a
// session row which path opened it.
const paymentSessionKeyPrefix = "cart_payment:"

// OpenPaymentInput is a storefront's request to open its cart's payment.
type OpenPaymentInput struct {
	// CartID is the cart whose payment is opened; it is REQUIRED.
	CartID string
	// SalesChannelIDs are the channels the request holds, for
	// [CompleteCartInput.SalesChannelIDs]'s reason: the cart is checked as its
	// completion will check it.
	SalesChannelIDs []string
	// PaymentProviderID is the provider the session is opened at; it is
	// REQUIRED and there is no default, for [CompleteCartInput]'s reason.
	PaymentProviderID string
	// PaymentData is handed to the provider as it is, and is the provider's
	// to read: PayTR's customer e-mail and the customer's own address.
	PaymentData json.RawMessage
	// ExpectedTotal is the total the customer confirmed (minor unit); zero is
	// "do not compare", as on the completion.
	ExpectedTotal int64
}

// OpenPaymentResult is the session that was opened.
type OpenPaymentResult struct {
	// PaymentSessionID is what the completion names
	// ([CompleteCartInput.PaymentSessionID]).
	PaymentSessionID string
	// PaymentCollectionID is the collection the session was opened on.
	PaymentCollectionID string
	// ProviderID is the provider the session was opened at.
	ProviderID string
	// Status is the session's status: "pending" until a completion authorizes
	// it.
	Status string
	// Amount and CurrencyCode are what the session collects: the cart's total
	// as this call recomputed it.
	Amount       int64
	CurrencyCode string
	// Data is the provider's own data for the storefront: PayTR's iframe
	// token. It is handed back as the provider returned it.
	Data json.RawMessage
	// CompletionSecret is the only copy of the secret that answers the
	// storefront's completion of this payment once the provider's report has
	// completed the cart (ADR 0438, ADR 0439); the payment module keeps its
	// digest.
	CompletionSecret string
}

// OpenPayment opens the payment of a cart before the cart is completed: it
// recomputes the cart, opens a collection for its total and a session at the
// named provider, and returns the session with the provider's data (ADR 0438).
//
// # It is checked as the completion is
//
// The preparation the completion runs runs here unchanged: the totals are
// recomputed, the cart's shape is compared, the tender is checked, the lines
// and their catalog facts are resolved, and the confirmed total is compared. A
// cart the preparation refuses — empty, completed, changed under the read, a
// variant that can never be reserved, a total the customer did not confirm — is
// refused before the customer is shown a page to pay on. What only the saga
// can tell — the stock at the moment of reserving, a coupon's last use — is
// still the completion's to refuse, after the customer paid.
//
// # It is NOT a saga
//
// Both writes are the payment module's, and neither moves money: a collection
// is a ledger line and a session is a page the customer has not paid on yet.
// A session that cannot be opened leaves an empty collection behind, the price
// [authorizePaymentStep.Invoke] already accepts. Two calls for one cart open two
// payments, and a storefront repeating one call sends the same Idempotency-Key,
// which the HTTP layer answers from its record.
//
// # What happens to the session afterwards
//
// The completion that names it authorizes it. A cart changed after it was
// opened refuses that completion, and the session stays as it is: a storefront
// cancels it — at PayTR a paid session's cancel is a refund — and opens a new
// one. A session nobody completes stays pending; at a provider whose customer
// pays on its own page reconciliation asks about it for a day, so one that was
// paid with no order behind it is reported.
func (w *Workflows) OpenPayment(ctx context.Context, in OpenPaymentInput) (OpenPaymentResult, error) {
	completion := CompleteCartInput{
		CartID:            in.CartID,
		SalesChannelIDs:   in.SalesChannelIDs,
		PaymentProviderID: in.PaymentProviderID,
		PaymentData:       in.PaymentData,
		ExpectedTotal:     in.ExpectedTotal,
	}
	if err := completion.normalize(); err != nil {
		return OpenPaymentResult{}, err
	}
	if err := refuseBalanceOpenedFirst(completion.PaymentProviderID); err != nil {
		return OpenPaymentResult{}, err
	}

	plan, err := w.prepare(ctx, completion)
	if err != nil {
		return OpenPaymentResult{}, err
	}

	collectionID, err := w.payments.CreateCollection(ctx,
		plan.CartID, plan.CustomerID, plan.CurrencyCode, plan.Amount)
	if err != nil {
		return OpenPaymentResult{}, err
	}
	if collectionID == "" {
		return OpenPaymentResult{}, errors.Internal(CodeEmptyIdentifier,
			"the payment module returned an EMPTY collection identifier: %s", plan.CartID)
	}

	sessionID, err := w.payments.OpenSessionWithData(ctx,
		collectionID, plan.PaymentProviderID, paymentSessionKeyPrefix+collectionID, plan.PaymentData)
	if err != nil {
		return OpenPaymentResult{}, err
	}
	if sessionID == "" {
		return OpenPaymentResult{}, errors.Internal(CodeEmptyIdentifier,
			"the payment module returned an EMPTY session identifier: %s (collection %s)",
			plan.CartID, collectionID)
	}

	facts, err := w.paymentSession(ctx, sessionID)
	if err != nil {
		return OpenPaymentResult{}, err
	}
	secret, err := w.payments.OpenSessionAnswer(ctx, sessionID)
	if err != nil {
		return OpenPaymentResult{}, err
	}

	w.log.InfoContext(ctx, "cart payment opened",
		"cart_id", plan.CartID, "collection_id", collectionID,
		"provider_id", plan.PaymentProviderID, "amount", plan.Amount)

	return OpenPaymentResult{
		PaymentSessionID:    facts.ID,
		PaymentCollectionID: facts.Collection.ID,
		ProviderID:          facts.ProviderID,
		Status:              facts.Status,
		Amount:              facts.Amount,
		CurrencyCode:        facts.CurrencyCode,
		Data:                facts.Data,
		CompletionSecret:    secret,
	}, nil
}

// CancelPayment cancels a payment the storefront opened for an open cart, and
// leaves the cart as it was (ADR 0438).
//
// It is how a storefront drops a payment the cart no longer matches — the cart
// changed after it was paid, or the shopper chose another provider — and at
// PayTR the cancel of a paid session is the refund. The cart is the
// capability, as for every storefront write on it: the session must have been
// opened for this cart, and a completed cart's payment is the order's, not the
// storefront's. A session captured by an order is refused by the payment
// module, whose cancel is idempotent otherwise.
func (w *Workflows) CancelPayment(ctx context.Context, cartID, sessionID string) error {
	if err := requireID("cart_id", cartID, MaxCartIDLen); err != nil {
		return err
	}
	if err := requireID("payment_session_id", sessionID, maxIDLen); err != nil {
		return err
	}

	snap, err := w.snapshot(ctx, cartID)
	if err != nil {
		return err
	}
	if snap.Completed {
		return errors.Conflict(CodeCartCompleted,
			"the payment of a completed cart is its order's: %s", cartID)
	}

	facts, err := w.paymentSession(ctx, sessionID)
	if err != nil {
		return err
	}
	if facts.Collection.Reference != snap.ID {
		return errors.Conflict(CodePaymentSessionForeign,
			"payment session %s was not opened for cart %s", facts.ID, snap.ID)
	}
	// A completion of the cart running now may be placing the order with
	// this very payment — the provider's report, while the storefront was
	// told its total moved — and a cancel would give that order's money back.
	running, err := w.completionRunning(ctx, snap.ID)
	if err != nil {
		return err
	}
	if running {
		return errors.Conflict(CodePaymentPending,
			"a completion of cart %s is running; its payment is canceled once it has finished, if the order did not take it",
			snap.ID)
	}

	if err := w.payments.Cancel(ctx, facts.ID); err != nil {
		return err
	}

	w.log.InfoContext(ctx, "cart payment canceled",
		"cart_id", snap.ID, "collection_id", facts.Collection.ID, "provider_id", facts.ProviderID)

	return nil
}

// paymentSessionFacts is the JSON schema of a session and its collection as
// [Payments.CheckoutSessionJSON] answers with.
//
// The field names must be EXACTLY the payment module's; the two sides cannot
// import each other, so the fit is proven by the end-to-end tests, and a body
// missing an identifier is refused here rather than carried on.
type paymentSessionFacts struct {
	ID               string          `json:"id"`
	ProviderID       string          `json:"provider_id"`
	Status           string          `json:"status"`
	Amount           int64           `json:"amount"`
	AuthorizedAmount int64           `json:"authorized_amount"`
	CurrencyCode     string          `json:"currency_code"`
	Data             json.RawMessage `json:"data,omitempty"`
	IdempotencyKey   string          `json:"idempotency_key"`
	Collection       struct {
		ID               string `json:"id"`
		Reference        string `json:"reference"`
		CustomerID       string `json:"customer_id,omitempty"`
		Amount           int64  `json:"amount"`
		CurrencyCode     string `json:"currency_code"`
		AuthorizedAmount int64  `json:"authorized_amount"`
		CapturedAmount   int64  `json:"captured_amount"`
	} `json:"collection"`
}

// closed reports a session no completion can use any more: captured,
// canceled or declined, or on a collection that already captured.
func (f paymentSessionFacts) closed() bool {
	switch f.Status {
	case SessionCaptured, SessionCanceled, SessionFailed:
		return true
	}

	return f.Collection.CapturedAmount > 0
}

// paymentSession reads a session and its collection from the payment module.
func (w *Workflows) paymentSession(ctx context.Context, sessionID string) (paymentSessionFacts, error) {
	raw, err := w.payments.CheckoutSessionJSON(ctx, sessionID)
	if err != nil {
		return paymentSessionFacts{}, err
	}

	var facts paymentSessionFacts
	if err := json.Unmarshal(raw, &facts); err != nil {
		return paymentSessionFacts{}, errors.Wrap(err, errors.KindInternal, CodePaymentAnswerInvalid,
			"the payment module's answer about session %s could not be decoded", sessionID)
	}
	if facts.ID == "" || facts.Collection.ID == "" {
		return paymentSessionFacts{}, errors.Internal(CodeEmptyIdentifier,
			"the payment module answered about session %s with no session or collection identifier", sessionID)
	}

	return facts, nil
}

// checkPayment holds a completion to the payment the storefront opened for it,
// and leaves NO side effect (ADR 0438).
//
// It runs after the plan is built, so every comparison is with what the cart
// is NOW, and before the saga's first step, so a refusal reserves no stock,
// opens no order and publishes nothing. It refuses:
//
//   - a provider whose customer pays on its own page, named with no session:
//     its session cannot be opened inside the completion, because the customer
//     has to be shown the page first;
//   - a session opened for another cart, at another provider than the one
//     named, or for another customer than the cart's: another cart's or
//     another person's money would pay for this one;
//   - a session already captured, canceled or declined;
//   - a session for an amount or a currency the cart no longer has: a cart
//     grown after it was paid would be placed paid short;
//   - a session whose provider has not reported the customer's payment yet,
//     which is the one refusal that is not final: the session stays open and
//     the same completion may be sent again.
//
// The plan records the session it accepted, which the authorization step then
// authorizes rather than opening one.
func (w *Workflows) checkPayment(ctx context.Context, in CompleteCartInput, plan *checkoutPlan) error {
	customerAuthorizes, err := w.payments.CustomerAuthorizes(ctx, plan.PaymentProviderID)
	if err != nil {
		return err
	}
	if in.PaymentSessionID == "" {
		if customerAuthorizes {
			return errors.Conflict(CodePaymentSessionRequired,
				"%q is paid on the provider's own page, so the payment is opened before the cart is "+
					"completed: POST /store/v1/carts/%s/payment-sessions, then complete with its "+
					"payment_session_id (ADR 0438)", plan.PaymentProviderID, plan.CartID)
		}

		return nil
	}

	facts, err := w.paymentSession(ctx, in.PaymentSessionID)
	if err != nil {
		return err
	}
	// The message names neither the other cart nor the other customer: a cart
	// id is the capability to read that cart.
	if facts.Collection.Reference != plan.CartID || facts.ProviderID != plan.PaymentProviderID ||
		(facts.Collection.CustomerID != "" && facts.Collection.CustomerID != plan.CustomerID) {
		return errors.Conflict(CodePaymentSessionForeign,
			"payment session %s was not opened for cart %s at %q; open the cart's own payment",
			facts.ID, plan.CartID, plan.PaymentProviderID)
	}
	if facts.closed() {
		return errors.Conflict(CodePaymentSessionClosed,
			"payment session %s is %s (its collection captured %d); open a new payment",
			facts.ID, facts.Status, facts.Collection.CapturedAmount)
	}
	if facts.Amount != plan.Amount || facts.Collection.Amount != plan.Amount ||
		facts.CurrencyCode != plan.CurrencyCode || facts.Collection.CurrencyCode != plan.CurrencyCode {
		return errors.Conflict(CodePaymentSessionStale,
			"payment session %s collects %d %s and the cart now totals %d %s; the cart changed after "+
				"its payment was opened, so cancel that session and open a new one",
			facts.ID, facts.Amount, facts.CurrencyCode, plan.Amount, plan.CurrencyCode)
	}

	if customerAuthorizes && facts.Status == SessionPending {
		reported, err := w.payments.ReportedStatus(ctx, facts.ID)
		if err != nil {
			return err
		}
		switch reported {
		case SessionPending:
			return errors.Conflict(CodePaymentPending,
				"%q has not reported the customer's payment of session %s yet; nothing was done, and "+
					"the same completion may be sent again", plan.PaymentProviderID, facts.ID)
		case SessionFailed, SessionCanceled:
			return errors.Conflict(CodePaymentReportedDeclined,
				"%q reported the customer's payment of session %s as %s; open a new payment",
				plan.PaymentProviderID, facts.ID, reported)
		}
	}

	plan.PaymentSessionID = facts.ID
	plan.PaymentCollectionID = facts.Collection.ID
	plan.CustomerAuthorizes = customerAuthorizes

	return nil
}
