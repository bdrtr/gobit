package adminui

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
)

// ServiceOrderAdmin is the order module's panel surface (ADR 0271). It is
// spelled by hand, as every surface name the panel resolves is, and pinned
// against the module's constant in internal/arch.
const ServiceOrderAdmin = "order.admin"

// OrderAfterSaleOpenPath opens an after-sales record of one kind on the order
// (ADR 0272).
const OrderAfterSaleOpenPath = OrderPath + "/after-sales/{kind}"

// OrderAfterSalePath takes one act on one after-sales record of the order: the
// kind is "return", "claim", "exchange" or "replacement", and the acts each
// kind takes are [afterSaleActs].
const OrderAfterSalePath = OrderPath + "/after-sales/{kind}/{record}/{act}"

// AfterSalesAdmin is the narrow surface the panel acts on an order's
// after-sales records through (ADR 0001). Each method is the order API's own,
// so the panel acts under the API's conditions.
type AfterSalesAdmin interface {
	// ReceiveReturn records the goods' arrival at a location and puts their
	// stock back; warnings need a human.
	ReceiveReturn(ctx context.Context, returnID, locationID string) (
		restockedLines int, restockedUnits int64, warnings []string, err error)
	// RefundReturn sends money back for a received return; zero is everything
	// the collection has left.
	RefundReturn(ctx context.Context, returnID string, amount int64, reason string) (
		refunded int64, summaryRecorded bool, warnings []string, err error)
	// CancelReturn withdraws a return that was not received.
	CancelReturn(ctx context.Context, returnID string) error
	// SettleClaim refunds a claim of the refund kind; zero is the claim's own
	// amount.
	SettleClaim(ctx context.Context, claimID string, amount int64, reason string) (
		refunded int64, summaryRecorded bool, warnings []string, err error)
	// CancelClaim withdraws a claim that was not settled.
	CancelClaim(ctx context.Context, claimID string) error
	// FundExchange names the collection that answers an exchange's difference.
	FundExchange(ctx context.Context, exchangeID, collectionID string) error
	// RefundExchange sends a funded exchange's money back and withdraws it.
	RefundExchange(ctx context.Context, exchangeID, reason string) error
	// CancelExchange withdraws an exchange that was not funded.
	CancelExchange(ctx context.Context, exchangeID string) error
	// DispatchReplacement sends what a replacement promised.
	DispatchReplacement(ctx context.Context, replacementID string) (
		fulfillmentID string, sentUnits int64, alreadySent bool, err error)
	// WithdrawReplacement takes back a replacement that has not left.
	WithdrawReplacement(ctx context.Context, replacementID string) error

	// OpenReturn opens a return naming its lines, a quantity for each and each
	// line's part of the refund (ADR 0279).
	OpenReturn(ctx context.Context, orderID string, lineIDs []string, quantities, lineRefunds []int64,
		refundAmount int64, reason string) (string, error)
	// OpenClaim opens a claim settled by "refund" or "replace".
	OpenClaim(ctx context.Context, orderID, claimType string, refundAmount int64, reason string) (string, error)
	// OpenExchange opens an exchange; a negative difference is paid to the
	// customer. With a return it takes that return's goods back and derives
	// its difference, which is then left zero (ADR 0432).
	OpenExchange(ctx context.Context, orderID, returnID string, differenceDue int64, note string) (string, error)
	// OpenReplacement records what a claim or an exchange will send: more of
	// the order's lines, a quantity for each, and variants the order never
	// sold, a quantity for each (ADR 0145, ADR 0279).
	OpenReplacement(ctx context.Context, claimID, exchangeID string, lineIDs []string, quantities []int64,
		variantIDs []string, variantQuantities []int64, shippingOptionID, locationID string) (string, error)
}

// The repeated fields of a form that names order lines: each line's id and the
// quantity typed beside it, in the same order, and beside those a return
// line's part of the refund (ADR 0279). A replacement names a variant the order
// never sold in a pair of its own, not beside a line (ADR 0145).
const (
	formLineID          = "line_id"
	formQuantity        = "quantity"
	formLineRefund      = "line_refund"
	formVariantID       = "variant_id"
	formVariantQuantity = "variant_quantity"
)

// The kinds of after-sales record, as the routes and the page name them.
const (
	kindReturn      = "return"
	kindClaim       = "claim"
	kindExchange    = "exchange"
	kindReplacement = "replacement"
)

// The act names and the status the table and the forms share.
const (
	actRefund       = "refund"
	actCancel       = "cancel"
	recordRequested = "requested"
)

// afterSaleOutcome is what an act reports on the order page it returns to.
type afterSaleOutcome struct {
	// Done is the sentence of an act that went through; Refused the reason
	// one did not.
	Done    string
	Refused string
	// Warnings are what the act did right and the world did not; each needs a
	// human.
	Warnings []string
	// Typed is what a refused address correction was sent with, drawn back
	// into its form (ADR 0388); nil for every other act.
	Typed url.Values
}

// afterSaleForm is one act a record offers on the page, and what its form
// asks for.
type afterSaleForm struct {
	Act   string
	Label string
	// Location, Collection and Reason ask for the text each names; Amount asks
	// for an optional amount, and AmountHint says what an empty one means.
	Location   bool
	Collection bool
	Amount     bool
	AmountHint string
	Reason     bool
}

// afterSaleAct carries out one act on one record and says what happened.
type afterSaleAct func(ctx context.Context, u *UI, r *http.Request, recordID string) (
	done string, warnings []string, err error)

// afterSaleActs are the acts the panel takes, by kind and act. They are the
// order API's after-sales writes on a record, but for opening one and a
// claim's evidence.
var afterSaleActs = map[string]map[string]afterSaleAct{
	kindReturn: {
		"receive": func(ctx context.Context, u *UI, r *http.Request, id string) (string, []string, error) {
			lines, units, warnings, err := u.afterSales.ReceiveReturn(ctx, id,
				strings.TrimSpace(r.PostFormValue("location_id")))
			return fmt.Sprintf("The return was received: %d units of %d lines went back to stock.",
				units, lines), warnings, err
		},
		actRefund: func(ctx context.Context, u *UI, r *http.Request, id string) (string, []string, error) {
			amount, currency, err := u.formAmount(r)
			if err != nil {
				return "", nil, err
			}
			refunded, recorded, warnings, err := u.afterSales.RefundReturn(ctx, id, amount,
				strings.TrimSpace(r.PostFormValue("reason")))
			return u.refunded(ctx, refunded, currency), unrecorded(recorded, warnings), err
		},
		actCancel: func(ctx context.Context, u *UI, _ *http.Request, id string) (string, []string, error) {
			return "The return was withdrawn.", nil, u.afterSales.CancelReturn(ctx, id)
		},
	},
	kindClaim: {
		"settle": func(ctx context.Context, u *UI, r *http.Request, id string) (string, []string, error) {
			amount, currency, err := u.formAmount(r)
			if err != nil {
				return "", nil, err
			}
			refunded, recorded, warnings, err := u.afterSales.SettleClaim(ctx, id, amount,
				strings.TrimSpace(r.PostFormValue("reason")))
			return u.refunded(ctx, refunded, currency), unrecorded(recorded, warnings), err
		},
		actCancel: func(ctx context.Context, u *UI, _ *http.Request, id string) (string, []string, error) {
			return "The claim was withdrawn.", nil, u.afterSales.CancelClaim(ctx, id)
		},
	},
	kindExchange: {
		"fund": func(ctx context.Context, u *UI, r *http.Request, id string) (string, []string, error) {
			collection := strings.TrimSpace(r.PostFormValue("collection_id"))
			return "The exchange's difference is answered by " + collection + ".", nil,
				u.afterSales.FundExchange(ctx, id, collection)
		},
		actRefund: func(ctx context.Context, u *UI, r *http.Request, id string) (string, []string, error) {
			return "The exchange's money was sent back and the exchange withdrawn.", nil,
				u.afterSales.RefundExchange(ctx, id, strings.TrimSpace(r.PostFormValue("reason")))
		},
		actCancel: func(ctx context.Context, u *UI, _ *http.Request, id string) (string, []string, error) {
			return "The exchange was withdrawn.", nil, u.afterSales.CancelExchange(ctx, id)
		},
	},
	kindReplacement: {
		"dispatch": func(ctx context.Context, u *UI, _ *http.Request, id string) (string, []string, error) {
			parcel, units, already, err := u.afterSales.DispatchReplacement(ctx, id)
			if already {
				return "The replacement had already left in parcel " + parcel + "; nothing moved.", nil, err
			}
			return fmt.Sprintf("The replacement left in parcel %s with %d units.", parcel, units), nil, err
		},
		"withdraw": func(ctx context.Context, u *UI, _ *http.Request, id string) (string, []string, error) {
			return "The replacement was withdrawn and its units given back.", nil,
				u.afterSales.WithdrawReplacement(ctx, id)
		},
	},
}

// afterSaleForms are the acts a record offers in the status it is in. The
// module decides whether an act is allowed; this only keeps the page from
// offering one the record's status plainly refuses.
func afterSaleForms(kind, status, claimType string) []afterSaleForm {
	cancel := afterSaleForm{Act: actCancel, Label: "Withdraw"}
	switch kind {
	case kindReturn:
		switch status {
		case recordRequested:
			return []afterSaleForm{{Act: "receive", Label: "Receive", Location: true}, cancel}
		case "received":
			return []afterSaleForm{{
				Act: actRefund, Label: "Refund", Amount: true, Reason: true,
				AmountHint: "empty: everything the collection has left",
			}}
		}
	case kindClaim:
		if status != recordRequested {
			return nil
		}
		if claimType == "refund" {
			return []afterSaleForm{{
				Act: "settle", Label: "Settle", Amount: true, Reason: true,
				AmountHint: "empty: the claim's own amount",
			}, cancel}
		}
		return []afterSaleForm{cancel}
	case kindExchange:
		switch status {
		case recordRequested:
			return []afterSaleForm{{Act: "fund", Label: "Fund", Collection: true}, cancel}
		case "funded":
			return []afterSaleForm{{Act: actRefund, Label: "Refund and withdraw", Reason: true}}
		}
	case kindReplacement:
		if status == recordRequested {
			return []afterSaleForm{{Act: "dispatch", Label: "Dispatch"}, {Act: "withdraw", Label: "Withdraw"}}
		}
	}

	return nil
}

// submitAfterSale takes one act on one after-sales record and returns to the
// order page with what happened.
//
// The page is drawn again rather than redirected to, because what an act
// reports — the units restocked, the parcel opened, a warning that needs a
// human — is said once and is not a property of the order a later read could
// show. A second submission of the same form is refused by the record's own
// status.
func (u *UI) submitAfterSale(w http.ResponseWriter, r *http.Request) {
	orderID := chi.URLParam(r, "id")
	act, ok := afterSaleActs[chi.URLParam(r, "kind")][chi.URLParam(r, "act")]
	if !ok {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "An after-sales record takes no such act.")
		return
	}
	if u.afterSales == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Acting unavailable",
			"The order module's panel surface is not registered in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	done, warnings, err := act(r.Context(), u, r, chi.URLParam(r, "record"))
	if err != nil {
		if errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err) {
			u.renderOrder(w, r, http.StatusUnprocessableEntity, orderID,
				&afterSaleOutcome{Refused: refusalOf(err)})
			return
		}
		u.unexpectedFailure(w, r, err, "The after-sales record could not be changed")
		return
	}

	u.renderOrder(w, r, http.StatusOK, orderID, &afterSaleOutcome{Done: done, Warnings: warnings})
}

// formAmount reads the form's optional amount in its currency's scale; empty
// is zero, which each act reads as the module does.
func (u *UI) formAmount(r *http.Request) (amount int64, currency string, err error) {
	currency = strings.ToUpper(strings.TrimSpace(r.PostFormValue("currency")))
	text := strings.TrimSpace(r.PostFormValue("amount"))
	if text == "" {
		return 0, currency, nil
	}
	amount, err = parseAmount(text, u.currencyScales(r.Context())[currency], r.PostFormValue("minor") == "1")

	return amount, currency, err
}

// refunded says how much went back, in the order's currency.
func (u *UI) refunded(ctx context.Context, amount int64, currency string) string {
	text, known := formatAmount(amount, currency, u.currencyScales(ctx))

	return withCurrency(text, currency, known) + " was refunded."
}

// unrecorded adds the warning an act gives when the money left and the order
// does not say so.
func unrecorded(recorded bool, warnings []string) []string {
	if recorded {
		return warnings
	}

	return append(warnings, "The money left, and the order does not record it: its totals "+
		"have to be put right by hand.")
}

// refusalOf is the sentence a refused act shows: the module's own message.
func refusalOf(err error) string {
	var typed *errors.Error
	if errors.As(err, &typed) && typed.Message != "" {
		return typed.Message
	}

	return "The after-sales record could not be changed."
}

// afterSaleOpener opens one kind of record on the order and returns its id.
type afterSaleOpener func(ctx context.Context, u *UI, r *http.Request, orderID string) (string, error)

// afterSaleOpeners are the records the panel opens, by kind (ADR 0272). They
// are the order API's openings, the replacement's limited to the order's own
// lines.
var afterSaleOpeners = map[string]afterSaleOpener{
	kindReturn: func(ctx context.Context, u *UI, r *http.Request, orderID string) (string, error) {
		lineIDs, quantities, rows, err := formOrderLines(r)
		if err != nil {
			return "", err
		}
		amount, currency, err := u.formAmount(r)
		if err != nil {
			return "", err
		}
		typed, err := formLineField(r, formLineRefund, rows)
		if err != nil {
			return "", err
		}
		lineRefunds := make([]int64, len(typed))
		for i, text := range typed {
			if text == "" {
				continue
			}
			if lineRefunds[i], err = parseAmount(text, u.currencyScales(ctx)[currency],
				r.PostFormValue("minor") == "1"); err != nil {
				return "", errors.Invalid(CodeAmountInvalid,
					"The refund beside line %s must be an amount: %q.", lineIDs[i], text)
			}
		}
		return u.afterSales.OpenReturn(ctx, orderID, lineIDs, quantities, lineRefunds, amount,
			strings.TrimSpace(r.PostFormValue("reason")))
	},
	kindClaim: func(ctx context.Context, u *UI, r *http.Request, orderID string) (string, error) {
		amount, _, err := u.formAmount(r)
		if err != nil {
			return "", err
		}
		return u.afterSales.OpenClaim(ctx, orderID, r.PostFormValue("type"), amount,
			strings.TrimSpace(r.PostFormValue("reason")))
	},
	kindExchange: func(ctx context.Context, u *UI, r *http.Request, orderID string) (string, error) {
		amount, _, err := u.formAmount(r)
		if err != nil {
			return "", err
		}
		return u.afterSales.OpenExchange(ctx, orderID, strings.TrimSpace(r.PostFormValue("return_id")),
			amount, strings.TrimSpace(r.PostFormValue("note")))
	},
	kindReplacement: func(ctx context.Context, u *UI, r *http.Request, _ string) (string, error) {
		lineIDs, quantities, _, err := formOrderLines(r)
		if err != nil {
			return "", err
		}
		variantIDs, variantQuantities, err := formVariants(r)
		if err != nil {
			return "", err
		}
		kind, sourceID, _ := strings.Cut(r.PostFormValue("source"), ":")
		claimID, exchangeID := "", ""
		switch kind {
		case kindClaim:
			claimID = sourceID
		case kindExchange:
			exchangeID = sourceID
		default:
			return "", errors.Invalid(CodeAmountInvalid, "Choose the claim or the exchange the replacement settles.")
		}
		return u.afterSales.OpenReplacement(ctx, claimID, exchangeID, lineIDs, quantities,
			variantIDs, variantQuantities, strings.TrimSpace(r.PostFormValue("shipping_option_id")),
			strings.TrimSpace(r.PostFormValue("location_id")))
	},
}

// submitAfterSaleOpen opens one after-sales record on the order and draws the
// page again with it, as an act does (ADR 0272).
func (u *UI) submitAfterSaleOpen(w http.ResponseWriter, r *http.Request) {
	orderID := chi.URLParam(r, "id")
	kind := chi.URLParam(r, "kind")
	open, ok := afterSaleOpeners[kind]
	if !ok {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "No such after-sales record is opened.")
		return
	}
	if u.afterSales == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Acting unavailable",
			"The order module's panel surface is not registered in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	id, err := open(r.Context(), u, r, orderID)
	if err != nil {
		if errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err) {
			u.renderOrder(w, r, http.StatusUnprocessableEntity, orderID,
				&afterSaleOutcome{Refused: refusalOf(err)})
			return
		}
		u.unexpectedFailure(w, r, err, "The after-sales record could not be opened")
		return
	}

	u.renderOrder(w, r, http.StatusOK, orderID, &afterSaleOutcome{Done: "The " + kind + " " + id + " was opened."})
}

// formOrderLines reads the order lines a form names, each with the quantity typed
// beside it; a line left empty or at zero is not named. rows says which of the
// form's rows each named line came from, so a field typed beside it is read
// off the same row ([formLineField]).
func formOrderLines(r *http.Request) (lineIDs []string, quantities []int64, rows []int, err error) {
	ids, typed := r.PostForm[formLineID], r.PostForm[formQuantity]
	if len(ids) != len(typed) {
		return nil, nil, nil, errors.Invalid(CodeAmountInvalid, "Every line needs its quantity box.")
	}
	for i := range ids {
		text := strings.TrimSpace(typed[i])
		if text == "" || text == "0" {
			continue
		}
		quantity, convErr := strconv.ParseInt(text, 10, 64)
		if convErr != nil || quantity < 0 {
			return nil, nil, nil, errors.Invalid(CodeAmountInvalid, "A quantity must be a whole number: %q.", text)
		}
		lineIDs = append(lineIDs, ids[i])
		quantities = append(quantities, quantity)
		rows = append(rows, i)
	}

	return lineIDs, quantities, rows, nil
}

// formVariants reads the variants a replacement sends that the order never
// sold, each with its quantity; a pair left empty or at zero names nothing, and
// a variant with no quantity, or a quantity with no variant, is refused.
func formVariants(r *http.Request) (variantIDs []string, quantities []int64, err error) {
	ids, typed := r.PostForm[formVariantID], r.PostForm[formVariantQuantity]
	if len(ids) != len(typed) {
		return nil, nil, errors.Invalid(CodeAmountInvalid, "Every variant needs its quantity box.")
	}
	for i := range ids {
		id, text := strings.TrimSpace(ids[i]), strings.TrimSpace(typed[i])
		if id == "" && (text == "" || text == "0") {
			continue
		}
		quantity, convErr := strconv.ParseInt(text, 10, 64)
		if id == "" || convErr != nil || quantity <= 0 {
			return nil, nil, errors.Invalid(CodeAmountInvalid,
				"Another variant needs its id and a whole number of units: %q, %q.", id, text)
		}
		variantIDs = append(variantIDs, id)
		quantities = append(quantities, quantity)
	}

	return variantIDs, quantities, nil
}

// formLineField reads the field typed beside each named line, trimmed and in
// the lines' order. A form that carries no such field names none; one that
// carries it for some rows and not others cannot be paired and is refused.
func formLineField(r *http.Request, field string, rows []int) ([]string, error) {
	values := r.PostForm[field]
	out := make([]string, len(rows))
	if len(values) == 0 {
		return out, nil
	}
	if len(values) != len(r.PostForm[formLineID]) {
		return nil, errors.Invalid(CodeAmountInvalid, "Every line needs its %s box.", strings.ReplaceAll(field, "_", " "))
	}
	for i, row := range rows {
		out[i] = strings.TrimSpace(values[row])
	}

	return out, nil
}
