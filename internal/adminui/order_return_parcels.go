package adminui

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// A return's parcels on the order page (ADR 0413): each return lists the
// parcels bringing it back, found by the return they name rather than by the
// order id they also carry, and a requested return offers a form that opens one
// on a return option for what it still awaits. The fulfillment module opens it
// under fulfillment:write, the privilege of POST /admin/v1/fulfillments, and
// the page moves it as it moves the order's own parcels. A label is bought by
// nobody: that waits for a carrier plugin that opens one.

// OrderReturnParcelsPath opens a parcel bringing one of the order's returns
// back.
const OrderReturnParcelsPath = OrderPath + "/returns/{return}/parcels"

// FieldParcelReturnID is the shipment's return, the fulfillment module's
// name, pinned against its constant in internal/arch.
const FieldParcelReturnID = "return_id"

// formReturnOption is the return option the open form names.
const formReturnOption = "option"

// parcelsPerReturn is how many parcels a return's row reads, oldest first; a
// return brought back in more has a story the page does not need to tell.
const parcelsPerReturn = 25

// ReturnParcelOpener is the narrow surface a return's parcel is opened
// through: the fulfillment module's panel surface.
type ReturnParcelOpener interface {
	// OpenReturnParcel opens a parcel on a return option bringing the return
	// back, holding quantities[i] units of the order line lineIDs[i], and
	// reports whether the key had already opened it.
	OpenReturnParcel(
		ctx context.Context, orderID, returnID, optionID, idempotencyKey string,
		lineIDs []string, quantities []int64,
	) (fulfillmentID string, alreadyOpen bool, err error)
	// ReturnOptionsJSON lists the return options a region offers in a
	// currency, admin-only ones included: [{id, name}].
	ReturnOptionsJSON(ctx context.Context, regionID, currencyCode string) (json.RawMessage, error)
}

// returnOptionChoice is one return option as the surface sends it; the json
// tags are the contract with that surface, exercised end to end.
type returnOptionChoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// returnParcelLine is one line of a return's open form: the order line, what
// the page calls it, and how many of its units the return still awaits, which
// bounds the field and prefills it.
type returnParcelLine struct {
	ID      string
	Title   string
	Awaited int64
}

// returnParcelForm is what a requested return's open form draws: the key it
// carries, the return options to choose from, and the lines still awaited.
// NoOption says the order's region offers no return option the page can list;
// OptionsUnread that they could not be read.
type returnParcelForm struct {
	Key           string
	Options       []returnOptionChoice
	Lines         []returnParcelLine
	NoOption      bool
	OptionsUnread bool
}

// liveParcel reports whether a parcel holds its units: one pending, on its way
// or delivered (ADR 0384). A canceled parcel, or one back undelivered, frees
// them.
func liveParcel(status string) bool {
	return status == parcelPending || status == parcelShipped || status == parcelDelivered
}

// canOpenReturnParcels reports whether the operator may open a return's
// parcel here.
func (u *UI) canOpenReturnParcels(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.parcels.(ReturnParcelOpener)

	return ok && principal.HasScope(scopeFulfillmentWrite)
}

// withReturnParcels gives each return that is not canceled its parcels and,
// while it is requested, the form that opens one for what it still awaits.
// Nothing is read for an operator who may not read the parcels; a return whose
// parcels could not be read says so and is drawn no form.
func (u *UI) withReturnParcels(r *http.Request, detail *orderDetail) {
	if detail.ParcelsHidden {
		return
	}
	titles := make(map[string]string, len(detail.Lines))
	for i := range detail.Lines {
		titles[detail.Lines[i].ID] = detail.Lines[i].Title
	}
	options := u.returnOptionsFor(r, detail)
	for i := range detail.AfterSales {
		sale := &detail.AfterSales[i]
		if sale.Kind != kindReturn || sale.Status == parcelCanceled {
			continue
		}
		records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
			Entity:  EntityFulfillment,
			Fields:  parcelFields,
			Filters: map[string]any{FieldParcelReturnID: sale.ID},
			Limit:   parcelsPerReturn,
		})
		if err != nil {
			sale.ParcelsUnread = true
			continue
		}
		sale.Parcels = parcelsFrom(records, titles)
		if sale.Status == recordRequested && options != nil {
			sale.ReturnParcel = returnParcelFormOf(*options, sale, records, titles)
		}
	}
}

// returnOptionsFor reads the return options the order's region offers, once
// for the page, for an operator who may open a return's parcel; nil for one
// who may not, or for an order with no requested return.
func (u *UI) returnOptionsFor(r *http.Request, detail *orderDetail) *returnParcelForm {
	if !u.canOpenReturnParcels(r) || !slices.ContainsFunc(detail.AfterSales, func(sale orderAfterSale) bool {
		return sale.Kind == kindReturn && sale.Status == recordRequested
	}) {
		return nil
	}
	opener, _ := u.parcels.(ReturnParcelOpener)
	ctx := r.Context()
	options := &returnParcelForm{}
	region, err := u.orderRegion(r, detail.ID)
	var raw json.RawMessage
	if err == nil {
		raw, err = opener.ReturnOptionsJSON(ctx, region, detail.Currency)
	}
	if err == nil {
		err = json.Unmarshal(raw, &options.Options)
	}
	switch {
	case err != nil:
		corehttp.LoggerFromContext(ctx).WarnContext(ctx,
			"the panel could not read the order's return options", "error", err, "order_id", detail.ID)
		options.OptionsUnread = true
	case len(options.Options) == 0:
		options.NoOption = true
	}

	return options
}

// orderRegion reads the order's region, which the return options are listed
// for.
func (u *UI) orderRegion(r *http.Request, orderID string) (string, error) {
	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity:  EntityOrder,
		Fields:  []string{fieldID, FieldOrderRegion},
		Filters: map[string]any{filterID: []string{orderID}},
		Limit:   1,
	})
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "", fmt.Errorf("order %s was not found", orderID)
	}

	return recordString(records[0], FieldOrderRegion), nil
}

// FieldOrderRegion is the order's region, the order module's name.
const FieldOrderRegion = "region_id"

// returnParcelFormOf is the open form of a requested return: each of its lines
// with the units it names less those its live parcels hold, the lines with none
// left out. A return awaiting nothing is drawn no form.
func returnParcelFormOf(
	options returnParcelForm, sale *orderAfterSale, parcels []query.Record, titles map[string]string,
) *returnParcelForm {
	held := map[string]int64{}
	for _, record := range parcels {
		if !liveParcel(recordString(record, fieldStatus)) {
			continue
		}
		items, _ := record[fieldItems].([]map[string]any)
		for _, item := range items {
			quantity, _ := intValue(item[itemQuantity])
			held[stringValue(item[itemLineItemID])] += int64(quantity)
		}
	}

	form := options
	form.Key = newParcelKey()
	form.Lines = nil
	for _, unit := range sale.units {
		awaited := unit.quantity - held[unit.lineID]
		if awaited <= 0 {
			continue
		}
		title := titles[unit.lineID]
		if title == "" {
			title = unit.lineID
		}
		form.Lines = append(form.Lines, returnParcelLine{ID: unit.lineID, Title: title, Awaited: awaited})
	}
	if len(form.Lines) == 0 {
		return nil
	}

	return &form
}

// openReturnParcel opens a parcel bringing back the return in the path, on the
// option and with the units the form names, and draws the order again saying
// so (ADR 0413). Whether the return is the order's, and what it still awaits,
// the fulfillment module asks the fulfilling flow.
func (u *UI) openReturnParcel(w http.ResponseWriter, r *http.Request) {
	opener, ok := u.parcels.(ReturnParcelOpener)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Parcels unavailable",
			"The fulfillment module's panel surface cannot open a return's parcel in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	orderID := chi.URLParam(r, "id")
	refuse := func(reason string) {
		u.renderOrder(w, r, http.StatusUnprocessableEntity, orderID,
			&afterSaleOutcome{Refused: reason + "; nothing was opened."})
	}
	key := strings.TrimSpace(r.PostFormValue(formParcelKey))
	if key == "" {
		refuse("The form carried no key; draw the page again")
		return
	}
	option := strings.TrimSpace(r.PostFormValue(formReturnOption))
	if option == "" {
		refuse("The form named no return option")
		return
	}
	units, err := parcelUnits(r)
	if err != nil {
		refuse(err.Error())
		return
	}
	lines := slices.Sorted(maps.Keys(units))
	quantities := make([]int64, 0, len(lines))
	for _, line := range lines {
		quantities = append(quantities, units[line])
	}

	parcel, already, err := opener.OpenReturnParcel(r.Context(), orderID, chi.URLParam(r, "return"),
		option, key, lines, quantities)
	switch {
	case err == nil && already:
		u.renderOrder(w, r, http.StatusOK, orderID, &afterSaleOutcome{
			Done: fmt.Sprintf("This form had already opened parcel %s; nothing new was opened.", parcel)})
	case err == nil:
		u.renderOrder(w, r, http.StatusOK, orderID, &afterSaleOutcome{
			Done: fmt.Sprintf("Parcel %s was opened to bring the return back.", parcel)})
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderOrder(w, r, http.StatusUnprocessableEntity, orderID, &afterSaleOutcome{Refused: refusalOf(err)})
	default:
		u.unexpectedFailure(w, r, err, "The return's parcel could not be opened")
	}
}

// parcelOfOrder reports whether the parcel is one the order's page shows
// (ADR 0413, gap D266). A move names a parcel by its id, and the fulfillment
// module, which does not know the order, would move any.
//
// The parcel's own record answers first, under the move's privilege: a parcel
// whose reference is the order is the order's. Every parcel the order route
// opens carries it, and so does every return's parcel, whose return the
// fulfilling flow holds to that order when it is opened (ADR 0384). A parcel
// an addition joined carries its parent's reference and is the addition's
// through the order_fulfillment link, which is the order module's to read, so
// it is asked only of an operator who may read the order (ADR 0251, ADR 0260).
func (u *UI) parcelOfOrder(r *http.Request, orderID, parcelID string) (bool, error) {
	parcels, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity:  EntityFulfillment,
		Fields:  []string{fieldID, FieldParcelReference},
		Filters: map[string]any{filterID: parcelID},
		Limit:   1,
	})
	if err != nil || len(parcels) == 0 {
		return false, err
	}
	if recordString(parcels[0], FieldParcelReference) == orderID {
		return true, nil
	}
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if !principal.HasScope(scopeOrderRead) {
		return false, nil
	}
	linked, err := u.linkedTo(r, orderID, query.Expansion{Link: linkOrderFulfillment, Fields: []string{fieldID}})
	if err != nil {
		return false, err
	}

	return slices.ContainsFunc(linkedRecords(linked), func(record query.Record) bool {
		return recordString(record, fieldID) == parcelID
	}), nil
}

// FieldParcelReference is the shipment's reference, the fulfillment module's
// name: the id of the record the parcel was opened for.
const FieldParcelReference = "reference"
