package adminui

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// An order's cancel on its page (ADR 0339): the order module cancels an order
// the checkout placed and writes off its units, so their stock comes back,
// as the API's cancel does.

// OrderCancelPath takes the form that cancels the order.
const OrderCancelPath = OrderPath + "/cancel"

// formCancelReason is why the order is canceled, kept with it.
const formCancelReason = "reason"

// orderPending is the status an order can be canceled in; a completed one
// is refused, and a canceled one is canceled already.
const orderPending = "pending"

// OrderCanceler is the narrow surface an order is canceled through: the
// order module's.
type OrderCanceler interface {
	// CancelOrder cancels the order and writes off its units; a completed
	// order and one with money collected are refused.
	CancelOrder(ctx context.Context, orderID, reason string) error
}

// canCancelOrder reports whether the operator may cancel an order in the
// status here.
func (u *UI) canCancelOrder(r *http.Request, status string) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.afterSales.(OrderCanceler)

	return ok && status == orderPending && principal.HasScope(scopeOrderWrite)
}

// cancelOrder cancels the order in the path with the reason typed and draws
// it again saying so; the module's refusal, an order with money collected
// among them, is drawn on the order (ADR 0339).
func (u *UI) cancelOrder(w http.ResponseWriter, r *http.Request) {
	canceler, ok := u.afterSales.(OrderCanceler)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Orders unavailable",
			"The order module's panel surface cannot cancel an order in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	orderID := chi.URLParam(r, "id")
	err := canceler.CancelOrder(r.Context(), orderID, strings.TrimSpace(r.PostFormValue(formCancelReason)))
	switch {
	case err == nil:
		u.renderOrder(w, r, http.StatusOK, orderID, &afterSaleOutcome{
			Done: "The order was canceled; its units were written off and their stock comes back."})
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderOrder(w, r, http.StatusUnprocessableEntity, orderID, &afterSaleOutcome{Refused: refusalOf(err)})
	default:
		u.unexpectedFailure(w, r, err, "The order could not be canceled")
	}
}
