package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// An order's shipping address corrected on its page (ADR 0388): the order
// module corrects it through the fulfilling flow the API's correction calls,
// from the shipping address row the page was drawn with, so a form drawn
// before another correction does not write back what that one changed.

// OrderShippingAddressPath takes the form that corrects where the order ships.
const OrderShippingAddressPath = OrderPath + "/shipping-address"

// formReadAddress is the shipping address row the form was drawn from, as the
// order's read provider publishes it.
const formReadAddress = "read_address"

// fieldShippingAddressID is the order's current shipping address row, the
// one a correction names as read (ADR 0388).
const fieldShippingAddressID = "shipping_address_id"

// orderAddressKeys are the corrected address's fields: the customer's printed
// keys, which the order's address map uses too (ADR 0196), less the country,
// which a correction keeps (ADR 0195).
var orderAddressKeys = func() []string {
	keys := make([]string, 0, len(addressKeys)-1)
	for _, key := range addressKeys {
		if key != formCountryCode {
			keys = append(keys, key)
		}
	}

	return keys
}()

// ShippingAddressCorrector is the narrow surface an order's shipping address
// is corrected through: the order module's, over the fulfilling flow.
type ShippingAddressCorrector interface {
	// CorrectShippingAddress corrects where the order ships from the row
	// readAddressID, the address as the order API's JSON; the module refuses
	// when the order holds another row.
	CorrectShippingAddress(ctx context.Context, orderID string, address json.RawMessage, readAddressID string) error
}

// canCorrectAddress reports whether the operator may correct where the order
// drawn ships: a pending one with a shipping address and no parcel on the
// page on its way. Parcels the page could not read, or may not, are left to
// the flow, which refuses while one is on its way.
func (u *UI) canCorrectAddress(r *http.Request, detail *orderDetail) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.afterSales.(ShippingAddressCorrector)

	return ok && principal.HasScope(scopeOrderWrite) && detail.Status == orderPending &&
		detail.ShipToID != "" && !parcelUnderway(detail.Parcels)
}

// shipToForm fills the correction form: the current address's fields, or,
// after a refusal, what was typed (ADR 0342's shape), the row it names still
// the current one. A field the refused form did not send keeps the current
// value: drawn empty, the next press would clear it.
func shipToForm(detail *orderDetail, address any, outcome *afterSaleOutcome) {
	fields, _ := address.(map[string]any)
	detail.ShipToCountry = stringValue(fields[formCountryCode])
	detail.ShipToForm = make(map[string]string, len(orderAddressKeys))
	for _, key := range orderAddressKeys {
		detail.ShipToForm[key] = stringValue(fields[key])
	}
	if outcome == nil || outcome.Typed == nil {
		return
	}
	detail.AddressRefused = true
	for _, key := range orderAddressKeys {
		if typed, sent := outcome.Typed[key]; sent && len(typed) > 0 {
			detail.ShipToForm[key] = typed[0]
		}
	}
}

// correctShippingAddress corrects where the order in the path ships from the
// row the form was drawn from and draws the order again saying so; a refusal,
// an address corrected since among them, is drawn on the order with what was
// typed (ADR 0388). A form missing one of the fields is refused rather than
// sent: the correction writes the whole address, and a field left out would
// be cleared.
func (u *UI) correctShippingAddress(w http.ResponseWriter, r *http.Request) {
	corrector, ok := u.afterSales.(ShippingAddressCorrector)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Orders unavailable",
			"The order module's panel surface cannot correct an address in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	orderID := chi.URLParam(r, "id")
	readAddressID := strings.TrimSpace(r.PostFormValue(formReadAddress))
	address := make(map[string]string, len(orderAddressKeys))
	var err error
	for _, key := range orderAddressKeys {
		if _, sent := r.PostForm[key]; !sent {
			err = errors.Invalid("admin_ui_address_incomplete",
				"The form sent no %s, and a correction writes the whole address; draw the page again.", key)
			break
		}
		address[key] = strings.TrimSpace(r.PostFormValue(key))
	}
	if err == nil && readAddressID == "" {
		err = errors.Invalid("admin_ui_read_address",
			"The address the page was drawn with could not be read; draw the page again.")
	}
	var body []byte
	if err == nil {
		body, err = json.Marshal(address)
	}
	if err == nil {
		err = corrector.CorrectShippingAddress(r.Context(), orderID, body, readAddressID)
	}
	switch {
	case err == nil:
		u.renderOrder(w, r, http.StatusOK, orderID, &afterSaleOutcome{
			Done: "The order now ships to the address typed; a parcel opened from now on carries it."})
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderOrder(w, r, http.StatusUnprocessableEntity, orderID,
			&afterSaleOutcome{Refused: refusalOf(err), Typed: r.PostForm})
	default:
		u.unexpectedFailure(w, r, err, "The address could not be corrected")
	}
}
