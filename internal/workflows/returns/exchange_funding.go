package returns

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
)

// CodeExchangeUnreadable reports that the exchange could not be read.
const CodeExchangeUnreadable = "returns_workflow_exchange_unreadable"

// CodeDifferenceNotHeld reports that the named collection does not hold the
// exchange's difference.
const CodeDifferenceNotHeld = "returns_workflow_difference_not_held"

// exchangeFunding is the order module's answer, decoded here.
//
// The two ends cannot import each other (ADR 0006), so this shape is written by
// hand on both sides and the compiler sees neither. The integration lane is the
// proof; a field renamed on one side and not the other reads back as a zero.
type exchangeFunding struct {
	ExchangeID          string `json:"exchange_id"`
	OrderID             string `json:"order_id"`
	Status              string `json:"status"`
	DifferenceDue       int64  `json:"difference_due"`
	CurrencyCode        string `json:"currency_code"`
	PaymentCollectionID string `json:"payment_collection_id"`
}

// FundExchangeDifference records that a payment collection answers an
// exchange's difference.
//
// # What the operator did first
//
// The money is collected through the payment module's own published endpoints:
// a collection opened for the difference, a session on it, an authorization, a
// capture. This flow does not move money and opens nothing — it is the side
// that can ASK both modules, and asking is the whole of its job.
//
// # Why the asking cannot be replaced by a stored figure
//
// The order module may not keep the amount (ADR 0119): a figure the payment
// module owns can be changed by a route the order never hears about, so a copy
// of it goes stale in silence. What the order records is the collection's
// IDENTIFIER and the moment — neither can rot — and every question about the
// money is asked here, live, at the moment it decides something.
//
// # The three things checked, and why each one
//
// The collection must be opened for EXACTLY the difference. A larger one would
// let a later capture take more than the exchange ever owed, and the ceiling
// has to be structural rather than arithmetic: the payment module already
// refuses a capture above a collection's amount, and that amount is written
// once and never updated.
//
// Its currency must be the ORDER's. The exchange's row has no currency of its
// own, the order's is the one the difference is denominated in, and a
// collection in another currency would satisfy every number here while holding
// the wrong money.
//
// What it holds must EQUAL the difference — captured less refunded, not
// captured alone. A collection captured in full and refunded in full holds
// nothing, and a rule reading the capture alone would call that funded. The
// comparison is an equality rather than a floor for the reason the record
// states: a floor is true for every negative difference against zero collected.
func (w *Workflows) FundExchangeDifference(ctx context.Context, exchangeID, collectionID string) error {
	if exchangeID == "" {
		return errors.Invalid(CodeInvalidInput, "the exchange id is required")
	}
	if collectionID == "" {
		return errors.Invalid(CodeInvalidInput, "the payment collection id is required")
	}

	detail, err := w.exchangeFunding(ctx, exchangeID)
	if err != nil {
		return err
	}

	if detail.DifferenceDue <= 0 {
		return errors.Conflict(CodeInvalidInput,
			"exchange %s owes nothing to collect (difference %d); money owed TO the customer "+
				"leaves by a refund, which is a different act",
			exchangeID, detail.DifferenceDue)
	}

	_, amount, _, captured, refunded, err := w.payments.Collection(ctx, collectionID)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), CodeNoPayment,
			"the collection named for exchange %s could not be read: %s", exchangeID, collectionID)
	}

	currency, err := w.payments.CollectionCurrency(ctx, collectionID)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), CodeNoPayment,
			"the currency of collection %s could not be read", collectionID)
	}

	if amount != detail.DifferenceDue {
		return errors.Conflict(CodeDifferenceNotHeld,
			"collection %s was opened for %d and exchange %s owes %d; the collection has to be "+
				"opened for exactly the difference, because its amount is what caps every capture on it",
			collectionID, amount, exchangeID, detail.DifferenceDue)
	}

	if currency != detail.CurrencyCode {
		return errors.Conflict(CodeDifferenceNotHeld,
			"collection %s is in %s and order %s is in %s",
			collectionID, currency, detail.OrderID, detail.CurrencyCode)
	}

	if held := captured - refunded; held != detail.DifferenceDue {
		return errors.Conflict(CodeDifferenceNotHeld,
			"collection %s holds %d of the %d exchange %s owes (captured %d, refunded %d); "+
				"the difference is funded when what is held EQUALS it",
			collectionID, held, detail.DifferenceDue, exchangeID, captured, refunded)
	}

	if err := w.orders.FundExchange(ctx, exchangeID, collectionID); err != nil {
		return err
	}

	w.log.InfoContext(ctx, "the exchange's difference was funded",
		"exchange_id", exchangeID, "payment_collection_id", collectionID,
		"difference_due", detail.DifferenceDue)

	return nil
}

// exchangeFunding reads what the decision needs from the order module.
func (w *Workflows) exchangeFunding(ctx context.Context, exchangeID string) (exchangeFunding, error) {
	raw, err := w.orders.ExchangeFundingJSON(ctx, exchangeID)
	if err != nil {
		return exchangeFunding{}, errors.Wrap(err, errors.KindOf(err), CodeExchangeUnreadable,
			"the exchange could not be read: %s", exchangeID)
	}

	var detail exchangeFunding
	if err := json.Unmarshal(raw, &detail); err != nil {
		return exchangeFunding{}, errors.Internal(CodeExchangeUnreadable,
			"the exchange %s could not be decoded: %v", exchangeID, err)
	}

	return detail, nil
}

// RefundExchangeDifference sends a funded exchange's money back and takes the
// request back with it.
//
// # Why the two halves are one act
//
// A funded exchange refuses the ordinary withdrawal, because taking a request
// back while holding the customer's money would leave money nothing answers
// for. That refusal needs an exit or it is a trap: an exchange whose goods
// turn out to be unsendable would sit funded for ever, and the order it hangs
// from could never be forgotten.
//
// The exit is this, and it is one call rather than two so the two halves cannot
// be done in the wrong order or half done on purpose. It refunds EVERYTHING the
// bound collection still holds and only then withdraws.
//
// # What it does not promise
//
// The refund happens before the record follows it, and there is no transaction
// across the two modules. A crash in between leaves the money back with the
// customer and the exchange still funded — which is the honest direction for
// the failure, and the same exposure the return's refund already accepts. A
// repeat is safe: the collection has nothing left to refund and the withdrawal
// is idempotent on an already withdrawn record.
func (w *Workflows) RefundExchangeDifference(ctx context.Context, exchangeID, reason string) error {
	if exchangeID == "" {
		return errors.Invalid(CodeInvalidInput, "the exchange id is required")
	}

	detail, err := w.exchangeFunding(ctx, exchangeID)
	if err != nil {
		return err
	}

	if detail.PaymentCollectionID == "" {
		return errors.Conflict(CodeInvalidInput,
			"exchange %s names no payment collection; there is nothing to send back",
			exchangeID)
	}

	_, _, _, captured, refunded, err := w.payments.Collection(ctx, detail.PaymentCollectionID)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), CodeNoPayment,
			"the collection of exchange %s could not be read: %s",
			exchangeID, detail.PaymentCollectionID)
	}

	if held := captured - refunded; held > 0 {
		if _, err := w.payments.RefundCollection(ctx, detail.PaymentCollectionID, held, reason); err != nil {
			return errors.Wrap(err, errors.KindOf(err), CodeRefundFailed,
				"the difference of exchange %s could not be sent back", exchangeID)
		}
	}

	if err := w.orders.WithdrawFundedExchange(ctx, exchangeID); err != nil {
		return err
	}

	w.log.InfoContext(ctx, "the exchange's difference went back and the request was withdrawn",
		"exchange_id", exchangeID, "payment_collection_id", detail.PaymentCollectionID)

	return nil
}
