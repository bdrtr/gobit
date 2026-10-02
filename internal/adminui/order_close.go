package adminui

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// An order's completion and archiving on its page (ADR 0340): the order
// module marks a pending order completed and takes a completed one out of
// the daily lists, as the API's transitions do.

const (
	// OrderCompletePath marks the order completed.
	OrderCompletePath = OrderPath + "/complete"
	// OrderArchivePath takes the order into the archive.
	OrderArchivePath = OrderPath + "/archive"
)

// orderCompleted is the status an order is archived from.
const orderCompleted = "completed"

// OrderCloser is the narrow surface an order is completed and archived
// through: the order module's.
type OrderCloser interface {
	// CompleteOrder marks a pending order completed.
	CompleteOrder(ctx context.Context, orderID string) error
	// ArchiveOrder takes a completed order into the archive.
	ArchiveOrder(ctx context.Context, orderID string) error
}

// orderCloseMove is the one move the page offers an order in a status: a
// pending order is completed, a completed one archived; empty for none.
func (u *UI) orderCloseMove(r *http.Request, status string) string {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if _, ok := u.afterSales.(OrderCloser); !ok || !principal.HasScope(scopeOrderWrite) {
		return ""
	}
	switch status {
	case orderPending:
		return "complete"
	case orderCompleted:
		return "archive"
	}

	return ""
}

// completeOrder marks the order in the path completed and draws it again
// saying so (ADR 0340).
func (u *UI) completeOrder(w http.ResponseWriter, r *http.Request) {
	u.closeOrder(w, r, "The order was marked completed.", OrderCloser.CompleteOrder)
}

// archiveOrder takes the order in the path into the archive and draws it
// again saying so (ADR 0340).
func (u *UI) archiveOrder(w http.ResponseWriter, r *http.Request) {
	u.closeOrder(w, r, "The order was archived; it leaves the daily lists.", OrderCloser.ArchiveOrder)
}

// closeOrder makes one of the two moves and draws the order again; the
// module's refusal, an order in another status among them, is drawn on it.
func (u *UI) closeOrder(
	w http.ResponseWriter, r *http.Request, done string, move func(OrderCloser, context.Context, string) error,
) {
	closer, ok := u.afterSales.(OrderCloser)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Orders unavailable",
			"The order module's panel surface cannot complete or archive an order in this installation.")
		return
	}

	orderID := chi.URLParam(r, "id")
	switch err := move(closer, r.Context(), orderID); {
	case err == nil:
		u.renderOrder(w, r, http.StatusOK, orderID, &afterSaleOutcome{Done: done})
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderOrder(w, r, http.StatusUnprocessableEntity, orderID, &afterSaleOutcome{Refused: refusalOf(err)})
	default:
		u.unexpectedFailure(w, r, err, "The order could not be moved")
	}
}
