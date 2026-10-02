package adminui

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// A line written off on the order's page (ADR 0341): the order module
// writes off units of one line of a pending order, as the API's line
// cancellation does, from the count of the line's units spoken for when the
// page was drawn, so a form sent twice writes off once.

// OrderLineCancellationsPath takes the form that writes off units of a line.
const OrderLineCancellationsPath = OrderPath + "/line-cancellations"

// The write-off form's fields: the line, how many of its units were asked
// back or written off when the page was drawn, and the write-off itself.
const (
	formWriteOffLine     = "line"
	formReadSpokenFor    = "read_spoken_for"
	formWriteOffQuantity = "quantity"
	formWriteOffReason   = "reason"
	formWriteOffNote     = "note"
)

// OrderLineCanceler is the narrow surface units of a line are written off
// through: the order module's.
type OrderLineCanceler interface {
	// CancelOrderLine writes off units of the line, refused when the line's
	// units spoken for are no longer the count read.
	CancelOrderLine(
		ctx context.Context, orderID, lineID string, readSpokenFor, quantity int64, reason, note string,
	) error
}

// canWriteOff reports whether the operator may write off units of an order
// in the status here: a pending one, the lines of a closed order being past
// changing.
func (u *UI) canWriteOff(r *http.Request, status string) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.afterSales.(OrderLineCanceler)

	return ok && status == orderPending && principal.HasScope(scopeOrderWrite)
}

// cancelOrderLine writes off the units the form names and draws the order
// again saying so; the module's refusal, a line that moved since the page was
// drawn among them, is drawn on the order (ADR 0341).
func (u *UI) cancelOrderLine(w http.ResponseWriter, r *http.Request) {
	canceler, ok := u.afterSales.(OrderLineCanceler)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Orders unavailable",
			"The order module's panel surface cannot write off a line in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	orderID := chi.URLParam(r, "id")
	readSpokenFor, readErr := strconv.ParseInt(r.PostFormValue(formReadSpokenFor), 10, 64)
	quantity, quantityErr := strconv.ParseInt(strings.TrimSpace(r.PostFormValue(formWriteOffQuantity)), 10, 64)
	var err error
	switch {
	case readErr != nil:
		err = errors.Invalid("admin_ui_read_line", "The line the page was drawn with could not be read; draw the page again.")
	case quantityErr != nil:
		err = errors.Invalid(CodeAmountInvalid, "The units written off are a whole number.")
	default:
		err = canceler.CancelOrderLine(r.Context(), orderID, r.PostFormValue(formWriteOffLine), readSpokenFor, quantity,
			strings.TrimSpace(r.PostFormValue(formWriteOffReason)), strings.TrimSpace(r.PostFormValue(formWriteOffNote)))
	}
	switch {
	case err == nil:
		u.renderOrder(w, r, http.StatusOK, orderID, &afterSaleOutcome{Done: fmt.Sprintf(
			"%d units were written off; what no parcel holds comes back to the shelf.", quantity)})
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderOrder(w, r, http.StatusUnprocessableEntity, orderID, &afterSaleOutcome{Refused: refusalOf(err)})
	default:
		u.unexpectedFailure(w, r, err, "The line could not be written off")
	}
}

// SpokenFor is how many of the line's units are asked back or written off,
// the count the order's ceiling reads under its lock.
func (l orderLine) SpokenFor() int64 { return l.AskedBack + l.Canceled }

// Left is how many of the line's units are neither asked back nor written
// off.
func (l orderLine) Left() int64 { return l.Quantity - l.SpokenFor() }
