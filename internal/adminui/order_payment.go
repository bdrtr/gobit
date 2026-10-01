package adminui

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// ServicePaymentAdmin is the payment module's panel surface (ADR 0287),
// spelled by hand and pinned against the module's constant in internal/arch.
const ServicePaymentAdmin = "payment.admin"

// OrderPaymentReceivedPath records that the money one of the order's offline
// sessions awaited arrived (ADR 0287).
const OrderPaymentReceivedPath = OrderPath + "/payment/{session}/received"

// PaymentReceiver is the narrow surface the panel records an offline payment
// through (ADR 0001); the method is the payment module's own, so the panel
// acts under the module's conditions.
type PaymentReceiver interface {
	// RecordReceived captures the session's awaited money whole and returns
	// the capture, its amount and its currency; a session whose money moves at
	// the checkout is refused.
	RecordReceived(ctx context.Context, sessionID string) (
		paymentID string, amount int64, currencyCode string, err error)
}

// The payment field the order page reads for what it awaits, and the keys of
// one entry. They are the payment module's names, repeated for the reason
// [EntityOrder] is.
const (
	fieldAwaiting      = "awaiting"
	awaitingSessionID  = "session_id"
	awaitingProviderID = "provider_id"
	awaitingAmount     = "amount"
)

// orderAwaiting is one session whose money the shop records when it arrives.
type orderAwaiting struct {
	SessionID string
	// Method is the offline method the customer was to pay by.
	Method string
	// Amount is what it awaits, formatted in the collection's currency.
	Amount string
}

// awaitingOf reads the collection's awaited sessions from the payment record.
// The list arrives as the provider built it, or as records when the read went
// through a layer that converts them; anything else is nothing awaited.
func awaitingOf(record query.Record, currency string, scales map[string]int) []orderAwaiting {
	var entries []query.Record
	switch value := record[fieldAwaiting].(type) {
	case []map[string]any:
		for _, entry := range value {
			entries = append(entries, entry)
		}
	case []query.Record:
		entries = value
	}

	out := make([]orderAwaiting, 0, len(entries))
	for _, entry := range entries {
		item := orderAwaiting{
			SessionID: recordString(entry, awaitingSessionID),
			Method:    recordString(entry, awaitingProviderID),
		}
		item.Amount, _ = amountField(entry, awaitingAmount, currency, scales)
		out = append(out, item)
	}

	return out
}

// submitPaymentReceived records that the money a session awaited arrived and
// shows the order again with what happened.
func (u *UI) submitPaymentReceived(w http.ResponseWriter, r *http.Request) {
	orderID := chi.URLParam(r, "id")
	if u.payments == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Recording unavailable",
			"The payment module's panel surface is not registered in this installation.")
		return
	}

	_, amount, currency, err := u.payments.RecordReceived(r.Context(), chi.URLParam(r, "session"))
	if err != nil {
		if errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err) {
			u.renderOrder(w, r, http.StatusUnprocessableEntity, orderID,
				&afterSaleOutcome{Refused: refusalOf(err)})
			return
		}
		u.unexpectedFailure(w, r, err, "The payment could not be recorded")
		return
	}

	text, known := formatAmount(amount, currency, u.currencyScales(r.Context()))
	u.renderOrder(w, r, http.StatusOK, orderID, &afterSaleOutcome{
		Done: "The payment of " + withCurrency(text, currency, known) + " was recorded as received.",
	})
}
