package adminui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// An order's parcels on its page (ADR 0324): the order module opens one
// through the fulfilling flow under order:write, on the delivery the operator
// names when the order was sold several (ADR 0332), and the fulfillment module
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
// button open nothing, the delivery the parcel goes on, and the tracking a
// shipped parcel carries.
const (
	formParcelKey      = "key"
	formParcelDelivery = "delivery"
	formTrackingNumber = "tracking_number"
	formTrackingURL    = "tracking_url"
	// formParcelUnits prefixes the field of each line's units in the parcel,
	// "units_<line id>" (ADR 0409).
	formParcelUnits = "units_"
)

// ParcelOpener is the narrow surface a parcel is opened through: the order
// module's, over the fulfilling flow.
type ParcelOpener interface {
	// OpenParcel opens a parcel for the order on the delivery deliveryID
	// names, or on the one it was sold when that is empty, holding the units
	// items names per order line, and reports whether the key had already
	// opened it (ADR 0409).
	OpenParcel(
		ctx context.Context, orderID, deliveryID, idempotencyKey string, items map[string]int64,
	) (fulfillmentID string, alreadyOpen bool, err error)
}

// OwedReader is the narrow surface what an order still owes a parcel is read
// through, per line: the order module's, over the fulfilling flow's dispatch
// bound (ADR 0409).
type OwedReader interface {
	// OwedUnits answers, per order line, how many units a new parcel may hold.
	OwedUnits(ctx context.Context, orderID string) (map[string]int64, error)
}

// parcelLine is one line of the open form: the order line, what the page calls
// it, how many of its units are still owed, which bounds the field, and what
// the field is prefilled with.
type parcelLine struct {
	ID      string
	Title   string
	Owed    int64
	Prefill int64
}

// DeliveryLister is the narrow surface an order's deliveries are read
// through, as they stand after their changes (ADR 0332).
type DeliveryLister interface {
	// DeliveriesJSON lists the order's deliveries.
	DeliveriesJSON(ctx context.Context, orderID string) (json.RawMessage, error)
}

// parcelDelivery is one of an order's deliveries as the surface sends it; the
// json tags are the contract with that surface, exercised end to end.
type parcelDelivery struct {
	ID               string `json:"id"`
	ShippingOptionID string `json:"shipping_option_id"`
	Name             string `json:"name"`
	// Amount is what the delivery costs as it stands (ADR 0388).
	Amount int64 `json:"amount"`
}

// parcelOpening is what the open form draws: the key it carries, the
// deliveries to choose from when the order was sold several, and the lines
// still owed a parcel (ADR 0409); NoDelivery says the order was sold none, so
// there is nothing to open a parcel on, NothingOwed that every unit is in a
// parcel or written off, and OwedUnread that what the order owes could not be
// read, so no line can be named and no form is drawn.
type parcelOpening struct {
	Key         string
	Deliveries  []parcelDelivery
	NoDelivery  bool
	Lines       []parcelLine
	NothingOwed bool
	OwedUnread  bool
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
	parcelPending: {
		{Act: "ship", Label: "Mark as shipped", Tracking: true},
		{Act: actCancel, Label: "Cancel the parcel"},
	},
	// A parcel on its way can still be recalled through the carrier, so the
	// module cancels it as well.
	parcelShipped: {
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

// deliveriesOf reads the order's deliveries for an operator who may open a
// parcel, once for the page: the open form and the delivery change draw from
// the same read. read says they were read; a surface that cannot list them,
// or a read that failed, leaves it false.
func (u *UI) deliveriesOf(r *http.Request, orderID string) (deliveries []parcelDelivery, read bool) {
	if !u.canOpenParcels(r) {
		return nil, false
	}
	lister, ok := u.afterSales.(DeliveryLister)
	if !ok {
		return nil, false
	}
	ctx := r.Context()
	raw, err := lister.DeliveriesJSON(ctx, orderID)
	if err == nil {
		err = json.Unmarshal(raw, &deliveries)
	}
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx,
			"the panel could not read the order's deliveries", "error", err, "order_id", orderID)

		return nil, false
	}

	return deliveries, true
}

// parcelOpeningFor is what the open form draws, nil for an operator who may
// not open a parcel, who is drawn no form. An order sold one delivery is
// opened on it by the flow, so only an order sold several is asked which
// (ADR 0332); deliveries the surface cannot list leave the choice to the flow,
// which refuses an order it cannot default. The lines still owed a parcel are
// named on the form, each bounded by what it owes (ADR 0409). An order sold one
// delivery is offered every owed unit; one sold several is offered none, since
// one parcel would take every delivery's goods, and the operator names them.
func (u *UI) parcelOpeningFor(
	r *http.Request, orderID string, lines []orderLine, deliveries []parcelDelivery, read bool,
) *parcelOpening {
	if !u.canOpenParcels(r) {
		return nil
	}
	opening := &parcelOpening{Key: newParcelKey()}
	switch {
	case !read:
	case len(deliveries) == 0:
		opening.NoDelivery = true
	case len(deliveries) > 1:
		opening.Deliveries = deliveries
	}
	opening.Lines, opening.NothingOwed, opening.OwedUnread = u.owedLines(r, orderID, lines)
	if read && len(deliveries) == 1 {
		for i := range opening.Lines {
			opening.Lines[i].Prefill = opening.Lines[i].Owed
		}
	}

	return opening
}

// owedLines are the order's lines still owed a parcel, in the page's order,
// whether the order owes none, and whether what it owes could not be read, by a
// surface that cannot say or a read that failed.
func (u *UI) owedLines(r *http.Request, orderID string, lines []orderLine) (owedLines []parcelLine, nothing, unread bool) {
	reader, ok := u.afterSales.(OwedReader)
	if !ok {
		return nil, false, true
	}
	ctx := r.Context()
	owed, err := reader.OwedUnits(ctx, orderID)
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx,
			"the panel could not read what the order still owes a parcel", "error", err, "order_id", orderID)

		return nil, false, true
	}
	var out []parcelLine
	for i := range lines {
		line := &lines[i]
		if owed[line.ID] > 0 {
			out = append(out, parcelLine{ID: line.ID, Title: line.Title, Owed: owed[line.ID]})
		}
	}

	return out, len(out) == 0, false
}

// parcelUnits reads the units the form names per line: a blank or zero field
// leaves the line out, a value that is not a whole number is refused, and so is
// a form naming no unit at all (ADR 0409). The panel never sends a parcel no
// items: the flow would read that as every unit still owed, the opposite of a
// form left at zero.
func parcelUnits(r *http.Request) (map[string]int64, error) {
	units := map[string]int64{}
	for field, values := range r.PostForm {
		line, ok := strings.CutPrefix(field, formParcelUnits)
		if !ok || line == "" || len(values) == 0 {
			continue
		}
		text := strings.TrimSpace(values[0])
		if text == "" {
			continue
		}
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("the units of line %s are not a whole number: %q", line, text)
		}
		if n > 0 {
			units[line] = n
		}
	}
	if len(units) == 0 {
		return nil, fmt.Errorf("the form named no unit; name at least one unit the parcel holds")
	}

	return units, nil
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

	units, err := parcelUnits(r)
	if err != nil {
		u.renderOrder(w, r, http.StatusUnprocessableEntity, orderID,
			&afterSaleOutcome{Refused: err.Error() + "; nothing was opened."})
		return
	}

	parcel, already, err := opener.OpenParcel(r.Context(), orderID,
		strings.TrimSpace(r.PostFormValue(formParcelDelivery)), key, units)
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
