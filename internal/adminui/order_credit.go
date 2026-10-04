package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// An order's credits on its page (ADR 0388): the order module lists them
// with their total and writes one, as the API's credit does, from the
// credited total the page was drawn with, so a form sent twice credits once.

// OrderCreditLinesPath takes the form that credits the order.
const OrderCreditLinesPath = OrderPath + "/credit-lines"

// The credit form's fields: the credited total the page was drawn with, the
// amount in the order's currency, and why. The amount is read by
// [UI.formAmount], which reads "amount", "currency" and "minor".
const (
	formReadCredited = "read_credited"
	formCreditReason = "reason"
	formCreditNote   = "note"
)

// OrderCreditor is the narrow surface an order's credits are read and
// written through: the order module's.
type OrderCreditor interface {
	// CreditLinesJSON lists the order's credits, oldest first, with their sum.
	CreditLinesJSON(ctx context.Context, orderID string) (json.RawMessage, error)
	// CreditOrder writes off part of what the order owes, refused when the
	// credited total is no longer the one read.
	CreditOrder(ctx context.Context, orderID string, readCredited, amount int64, reason, note string) error
}

// orderCredit is one of an order's credits as the surface sends it; the json
// tags are the contract with that surface, exercised end to end.
type orderCredit struct {
	ID        string    `json:"id"`
	Amount    int64     `json:"amount"`
	Reason    string    `json:"reason"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
	// Printed is the amount in the order's currency, as the page prints it.
	Printed string `json:"-"`
}

// orderCredits is the surface's answer: the credits and their sum, the
// credited total the module's ceiling reads.
type orderCredits struct {
	CreditedTotal int64         `json:"credited_total"`
	Lines         []orderCredit `json:"lines"`
}

// creditsOf reads the order's credits for anyone who opens the page, as a
// claim's evidence is read; shown says the surface lists them, and unread
// that the read failed, which leaves the order on the page.
func (u *UI) creditsOf(
	r *http.Request, detail *orderDetail, scales map[string]int,
) {
	creditor, ok := u.afterSales.(OrderCreditor)
	if !ok {
		return
	}
	detail.CreditsShown = true
	ctx := r.Context()
	var credits orderCredits
	raw, err := creditor.CreditLinesJSON(ctx, detail.ID)
	if err == nil {
		err = json.Unmarshal(raw, &credits)
	}
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx,
			"the panel could not read the order's credits", "error", err, "order_id", detail.ID)
		detail.CreditsUnread = true

		return
	}
	for i := range credits.Lines {
		text, known := formatAmount(credits.Lines[i].Amount, detail.Currency, scales)
		credits.Lines[i].Printed = withCurrency(text, detail.Currency, known)
	}
	detail.Credits = credits.Lines
	detail.CreditedRead = credits.CreditedTotal
	text, known := formatAmount(credits.CreditedTotal, detail.Currency, scales)
	detail.CreditedTotal = withCurrency(text, detail.Currency, known)
}

// canCredit reports whether the operator may credit the order drawn: one
// that is not canceled, its credits read, since the form carries their
// total. Leaving out a canceled order is the page's: it was canceled with
// nothing collected (ADR 0339), so a credit has nothing to lower.
func (u *UI) canCredit(r *http.Request, detail *orderDetail) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.afterSales.(OrderCreditor)

	return ok && principal.HasScope(scopeOrderWrite) && detail.Status != orderCanceled &&
		detail.CreditsShown && !detail.CreditsUnread
}

// creditOrder writes the credit the form names and draws the order again
// saying so; the module's refusal, a credit written since the page was drawn
// among them, is drawn on the order (ADR 0388).
func (u *UI) creditOrder(w http.ResponseWriter, r *http.Request) {
	creditor, ok := u.afterSales.(OrderCreditor)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Orders unavailable",
			"The order module's panel surface cannot credit an order in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	orderID := chi.URLParam(r, "id")
	readCredited, readErr := strconv.ParseInt(r.PostFormValue(formReadCredited), 10, 64)
	amount, currency, amountErr := u.formAmount(r)
	var err error
	switch {
	case readErr != nil:
		err = errors.Invalid("admin_ui_read_credited",
			"The credits the page was drawn with could not be read; draw the page again.")
	case amountErr != nil:
		err = amountErr
	default:
		err = creditor.CreditOrder(r.Context(), orderID, readCredited, amount,
			strings.TrimSpace(r.PostFormValue(formCreditReason)), strings.TrimSpace(r.PostFormValue(formCreditNote)))
	}
	switch {
	case err == nil:
		text, known := formatAmount(amount, currency, u.currencyScales(r.Context()))
		u.renderOrder(w, r, http.StatusOK, orderID, &afterSaleOutcome{Done: withCurrency(text, currency, known) +
			" was credited; what the customer owes is lower by it, and no money moved."})
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderOrder(w, r, http.StatusUnprocessableEntity, orderID, &afterSaleOutcome{Refused: refusalOf(err)})
	default:
		u.unexpectedFailure(w, r, err, "The order could not be credited")
	}
}
