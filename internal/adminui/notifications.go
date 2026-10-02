package adminui

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The notifications screen (ADR 0317): the delivery log of the notification
// module, one status at a time or one order's, and the resend of a failed
// order confirmation, through the module's panel surface.

// ServiceNotificationAdmin is the notification module's panel surface,
// spelled by hand and pinned against the module's constant in internal/arch.
const ServiceNotificationAdmin = "notification.admin"

// NotificationsPath lists the deliveries, and NotificationResendPath sends one
// again.
const (
	NotificationsPath      = URLPrefix + "/notifications"
	NotificationResendPath = NotificationsPath + "/{id}/resend"
)

// notificationsLabel is what the section is called on screen.
const notificationsLabel = "Notifications"

// scopeNotificationRead and scopeNotificationWrite are the notification
// module's privileges, as its admin API names them.
const (
	scopeNotificationRead  = "notification:read"
	scopeNotificationWrite = "notification:write"
)

// notificationStatuses are the statuses the screen offers, in the order of
// its tabs; the failed ones come first because they are what an operator
// comes to the screen for, and they are listed when none is chosen.
var notificationStatuses = []string{"failed", "pending", "sent", "skipped"}

// The screen's parameters: the status tab, an order to list the deliveries
// of, and the outcome of a resend, carried in the address it lands on.
const (
	paramDeliveryStatus = "status"
	paramReference      = "reference"
	paramResent         = "resent"
	paramOutcome        = "outcome"
)

// deliveriesPerPage is the list's page size, the other lists'.
const deliveriesPerPage = 25

// notificationsPerOrder is how many of an order's deliveries the order's page
// lists (ADR 0318); the Notifications screen lists them all.
const notificationsPerOrder = 10

// NotificationLister is the narrow surface the screen reads through.
type NotificationLister interface {
	// DeliveriesJSON lists the deliveries in the status, or of the order the
	// reference names, a page at a time, with the total.
	DeliveriesJSON(ctx context.Context, status, reference string, limit, offset int32) (json.RawMessage, int64, error)
}

// NotificationResender is the narrow surface a delivery is sent again
// through.
type NotificationResender interface {
	// ResendDelivery sends the delivery again and returns the status it was
	// left in.
	ResendDelivery(ctx context.Context, id string) (string, error)
}

// deliveryRow is one delivery as the surface sends it; the json tags are the
// contract with that surface, exercised end to end.
type deliveryRow struct {
	ID         string    `json:"id"`
	Template   string    `json:"template"`
	Channel    string    `json:"channel"`
	Reference  string    `json:"reference"`
	Status     string    `json:"status"`
	Error      string    `json:"error"`
	UpdatedAt  time.Time `json:"updated_at"`
	Resendable bool      `json:"resendable"`
}

// listNotifications renders the deliveries in the chosen status, or of the
// order searched for.
func (u *UI) listNotifications(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	u.renderNotifications(w, r, http.StatusOK, query.Get(paramDeliveryStatus), query.Get(paramReference), "")
}

// resendNotification sends the delivery in the path again and returns to the
// list it was pressed on, the tab or the order's, saying how it went; a
// delivery that may not be sent again is refused on that list (ADR 0317).
func (u *UI) resendNotification(w http.ResponseWriter, r *http.Request) {
	resender, ok := u.notifications.(NotificationResender)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Notifications unavailable",
			"The notification module's panel surface cannot send a notification again in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	id := chi.URLParam(r, "id")
	from := r.PostFormValue(paramDeliveryStatus)
	reference := strings.TrimSpace(r.PostFormValue(paramReference))
	outcome, err := resender.ResendDelivery(r.Context(), id)
	switch {
	case err == nil:
		back := url.Values{paramDeliveryStatus: {from}, paramResent: {id}, paramOutcome: {outcome}}
		if reference != "" {
			back.Set(paramReference, reference)
		}
		corehttp.WriteRedirect(r.Context(), w, NotificationsPath+"?"+back.Encode())
	case errors.IsConflict(err) || errors.IsNotFound(err) || errors.IsInvalid(err):
		u.renderNotifications(w, r, http.StatusUnprocessableEntity, from, reference, messageFor(err))
	default:
		u.unexpectedFailure(w, r, err, "The notification could not be sent again")
	}
}

// notificationsOf reads the order's deliveries for its page, newest first,
// and whether it has more than the page lists; unread says the read failed,
// which leaves the order on screen (ADR 0318).
func (u *UI) notificationsOf(r *http.Request, orderID string) (rows []deliveryRow, more, unread bool) {
	ctx := r.Context()
	raw, total, err := u.notifications.DeliveriesJSON(ctx, "", orderID, notificationsPerOrder, 0)
	if err == nil {
		err = json.Unmarshal(raw, &rows)
	}
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx,
			"the panel could not read the order's notifications", "error", err, "order_id", orderID)

		return nil, false, true
	}

	return rows, total > int64(len(rows)), false
}

// renderNotifications lists the deliveries with a refused resend's reason.
// An order's deliveries are listed in every status; otherwise the status is
// the tab's, the first when it is none of them. An operator who may resend
// and not read is told the reason alone (ADR 0260).
func (u *UI) renderNotifications(w http.ResponseWriter, r *http.Request, code int, status, reference, refused string) {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if refused != "" && !principal.HasScope(scopeNotificationRead) {
		u.errorPage(w, r, code, "Not done", refused)
		return
	}
	if u.notifications == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Notifications unavailable",
			"The notification module's panel surface is not registered in this installation.")
		return
	}

	reference = strings.TrimSpace(reference)
	if !slices.Contains(notificationStatuses, status) {
		status = notificationStatuses[0]
	}
	listed := status
	if reference != "" {
		listed = ""
	}
	page := pageNumber(r.URL.Query().Get("page"))
	offset := (page - 1) * deliveriesPerPage
	if offset > math.MaxInt32 {
		offset = math.MaxInt32
	}

	raw, total, err := u.notifications.DeliveriesJSON(r.Context(), listed, reference, deliveriesPerPage, int32(offset))
	if err != nil {
		u.unexpectedFailure(w, r, err, "The notifications could not be read")
		return
	}
	var rows []deliveryRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		u.unexpectedFailure(w, r, err, "The notifications could not be read")
		return
	}

	_, canResend := u.notifications.(NotificationResender)
	data := map[string]any{
		titleKey:     notificationsLabel,
		"Deliveries": rows,
		statusKey:    status,
		statusesKey:  notificationStatuses,
		"Reference":  reference,
		totalKey:     total,
		"CanResend":  canResend && principal.HasScope(scopeNotificationWrite),
		"Resent":     r.URL.Query().Get(paramResent),
		"Outcome":    r.URL.Query().Get(paramOutcome),
		refusedKey:   refused,
	}
	addPaging(data, page, int64(page*deliveriesPerPage) < total, NotificationsPath)

	u.templates.render(w, r, code, "notifications.gohtml", data)
}
