package adminui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// An order's parcels on its page (ADR 0324): the order module opens one
// through the fulfilling flow under order:write, and the fulfillment module
// moves it — shipped, delivered, back undelivered, canceled — under
// fulfillment:write.

// ServiceFulfillmentAdmin is the fulfillment module's panel surface, spelled
// by hand and pinned against the module's constant in internal/arch.
const ServiceFulfillmentAdmin = "fulfillment.admin"

// OrderParcelsPath opens a parcel for the order, and OrderParcelActPath makes
// one move on one of its parcels.
const (
	OrderParcelsPath   = OrderPath + "/parcels"
	OrderParcelActPath = OrderParcelsPath + "/{parcel}/{act}"
)

// scopeFulfillmentWrite is the fulfillment module's write privilege, as its
// admin API names it.
const scopeFulfillmentWrite = "fulfillment:write"

// The parcel forms' fields: the key that makes a second press of the open
// button open nothing, and the tracking a shipped parcel carries.
const (
	formParcelKey      = "key"
	formTrackingNumber = "tracking_number"
	formTrackingURL    = "tracking_url"
)

// ParcelOpener is the narrow surface a parcel is opened through: the order
// module's, over the fulfilling flow.
type ParcelOpener interface {
	// OpenParcel opens a parcel for the order on the delivery it was sold,
	// and reports whether the key had already opened it.
	OpenParcel(ctx context.Context, orderID, idempotencyKey string) (fulfillmentID string, alreadyOpen bool, err error)
}

// ParcelMover is the narrow surface a parcel is moved through: the
// fulfillment module's.
type ParcelMover interface {
	// ShipParcel records that the carrier took the parcel.
	ShipParcel(ctx context.Context, id, trackingNumber, trackingURL string) error
	// DeliverParcel records that the parcel reached the recipient.
	DeliverParcel(ctx context.Context, id string) error
	// ReturnParcel records that the parcel came back undelivered.
	ReturnParcel(ctx context.Context, id string) error
	// CancelParcel cancels a parcel that has not left.
	CancelParcel(ctx context.Context, id string) error
}

// parcelMove is one move a parcel's row offers.
type parcelMove struct {
	Act   string
	Label string
	// Tracking says the form asks for the tracking number and page.
	Tracking bool
}

// parcelMoves are the moves offered on a parcel in a status, the ones the
// fulfillment module's state machine takes from it; a terminal status offers
// none. The module refuses the rest anyway, so this is what the page offers,
// not what it allows.
var parcelMoves = map[string][]parcelMove{
	"pending": {
		{Act: "ship", Label: "Mark as shipped", Tracking: true},
		{Act: actCancel, Label: "Cancel the parcel"},
	},
	// A parcel on its way can still be recalled through the carrier, so the
	// module cancels it as well.
	"shipped": {
		{Act: "deliver", Label: "Mark as delivered"},
		{Act: "return", Label: "Mark as come back undelivered"},
		{Act: actCancel, Label: "Cancel the parcel"},
	},
}

// parcelActs carry out each move and say what it did.
var parcelActs = map[string]func(ctx context.Context, mover ParcelMover, r *http.Request, id string) (string, error){
	"ship": func(ctx context.Context, mover ParcelMover, r *http.Request, id string) (string, error) {
		return "Parcel " + id + " is on its way.", mover.ShipParcel(ctx, id,
			strings.TrimSpace(r.PostFormValue(formTrackingNumber)), strings.TrimSpace(r.PostFormValue(formTrackingURL)))
	},
	"deliver": func(ctx context.Context, mover ParcelMover, _ *http.Request, id string) (string, error) {
		return "Parcel " + id + " was delivered.", mover.DeliverParcel(ctx, id)
	},
	"return": func(ctx context.Context, mover ParcelMover, _ *http.Request, id string) (string, error) {
		return "Parcel " + id + " came back undelivered.", mover.ReturnParcel(ctx, id)
	},
	actCancel: func(ctx context.Context, mover ParcelMover, _ *http.Request, id string) (string, error) {
		return "Parcel " + id + " was canceled.", mover.CancelParcel(ctx, id)
	},
}

// canOpenParcels reports whether the operator may open a parcel here.
func (u *UI) canOpenParcels(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.afterSales.(ParcelOpener)

	return ok && principal.HasScope(scopeOrderWrite)
}

// canMoveParcels reports whether the operator may move the parcels here.
func (u *UI) canMoveParcels(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())

	return u.parcels != nil && principal.HasScope(scopeFulfillmentWrite)
}

// newParcelKey is the key one drawing of the open form carries: a second
// press, or a reload of the page it lands on, sends it again and opens
// nothing new.
func newParcelKey() string {
	var key [16]byte
	_, _ = rand.Read(key[:])

	return "panel-" + hex.EncodeToString(key[:])
}

// parcelKeyFor is the key the open form carries, empty for an operator who
// may not open a parcel, which draws no form.
func (u *UI) parcelKeyFor(r *http.Request) string {
	if !u.canOpenParcels(r) {
		return ""
	}

	return newParcelKey()
}

// openParcel opens a parcel for the order in the path with the key the form
// carried and draws the order again saying so (ADR 0324).
func (u *UI) openParcel(w http.ResponseWriter, r *http.Request) {
	opener, ok := u.afterSales.(ParcelOpener)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Parcels unavailable",
			"The order module's panel surface cannot open a parcel in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	orderID := chi.URLParam(r, "id")
	key := strings.TrimSpace(r.PostFormValue(formParcelKey))
	if key == "" {
		u.renderOrder(w, r, http.StatusUnprocessableEntity, orderID,
			&afterSaleOutcome{Refused: "The form carried no key, so nothing was opened; draw the page again."})
		return
	}

	parcel, already, err := opener.OpenParcel(r.Context(), orderID, key)
	switch {
	case err == nil && already:
		u.renderOrder(w, r, http.StatusOK, orderID, &afterSaleOutcome{
			Done: fmt.Sprintf("This form had already opened parcel %s; nothing new was opened.", parcel)})
	case err == nil:
		u.renderOrder(w, r, http.StatusOK, orderID, &afterSaleOutcome{
			Done: fmt.Sprintf("Parcel %s was opened.", parcel)})
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderOrder(w, r, http.StatusUnprocessableEntity, orderID, &afterSaleOutcome{Refused: refusalOf(err)})
	default:
		u.unexpectedFailure(w, r, err, "The parcel could not be opened")
	}
}

// moveParcel makes the move in the path on the parcel in the path and draws
// the order again saying what it did (ADR 0324).
func (u *UI) moveParcel(w http.ResponseWriter, r *http.Request) {
	act, ok := parcelActs[chi.URLParam(r, "act")]
	if !ok {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "A parcel takes no such move.")
		return
	}
	if u.parcels == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Parcels unavailable",
			"The fulfillment module's panel surface is not registered in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	orderID := chi.URLParam(r, "id")
	done, err := act(r.Context(), u.parcels, r, chi.URLParam(r, "parcel"))
	switch {
	case err == nil:
		u.renderOrder(w, r, http.StatusOK, orderID, &afterSaleOutcome{Done: done})
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderOrder(w, r, http.StatusUnprocessableEntity, orderID, &afterSaleOutcome{Refused: refusalOf(err)})
	default:
		u.unexpectedFailure(w, r, err, "The parcel could not be moved")
	}
}
