package adminui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// An order's deliveries on its page (ADR 0388): an operator who may write
// orders sees what each costs, and on a pending order with no parcel on its
// way puts one on another option the shop quotes for the order, through the
// order module's surface and the fulfilling flow the API's change calls, at
// the price drawn with the page.

// OrderDeliveryPath takes the form that puts one of the order's deliveries on
// another option.
const OrderDeliveryPath = OrderPath + "/deliveries/{delivery}"

// The delivery change form's fields: the option with the price it was drawn
// at, as "id|amount", and the collection that took a dearer one's difference
// (ADR 0200). The currency is the order's, carried to print the outcome.
const (
	formDeliveryOption     = "option"
	formDeliveryCollection = "collection_id"
	formDeliveryCurrency   = "currency"
)

// DeliveryChanger is the narrow surface an order's delivery is quoted and
// changed through: the order module's, over the fulfilling flow.
type DeliveryChanger interface {
	// DeliveryQuoteJSON lists the options the order's deliveries can be put
	// on, each with the price a change would write.
	DeliveryQuoteJSON(ctx context.Context, orderID string) (json.RawMessage, error)
	// ChangeDelivery puts the delivery on the option at the price shown,
	// refused when the quote moved, and answers the change or JSON null.
	ChangeDelivery(
		ctx context.Context, orderID, deliveryID, optionID, collectionID string, quotedAmount int64,
	) (json.RawMessage, error)
}

// deliveryQuote is one option as the surface quotes it; the json tags are the
// contract with that surface, exercised end to end.
type deliveryQuote struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Amount int64  `json:"amount"`
}

// deliveryChange is the part of the surface's answer the page reports.
type deliveryChange struct {
	Name                string `json:"name"`
	Difference          int64  `json:"difference"`
	PaymentCollectionID string `json:"payment_collection_id"`
}

// deliveryChoice is one option a delivery's form offers: the value carries the
// price drawn, and the label says what it costs against the delivery.
type deliveryChoice struct {
	Value string
	Label string
}

// deliveryRowView is one of the order's deliveries on the page, with the
// options its change offers.
type deliveryRowView struct {
	ID      string
	Name    string
	Printed string
	Choices []deliveryChoice
}

// deliveriesView is the Deliveries section: the order's deliveries for an
// operator who may write orders, and whether their change is offered.
// Changeable says the form is drawn; QuoteUnread that the quote failed, so
// no form is.
type deliveriesView struct {
	Rows        []deliveryRowView
	Changeable  bool
	QuoteUnread bool
}

// canChangeDelivery reports whether the operator may change a delivery of the
// order drawn: a pending one with no parcel pending, shipped or delivered. A
// parcel the page could not read, or may not, is left to the flow, which
// refuses the change while one is on its way.
func (u *UI) canChangeDelivery(r *http.Request, detail *orderDetail) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if _, ok := u.afterSales.(DeliveryChanger); !ok || !principal.HasScope(scopeOrderWrite) ||
		detail.Status != orderPending {
		return false
	}

	return !parcelUnderway(detail.Parcels)
}

// parcelUnderway reports whether a parcel holds what it was opened with: one
// pending, shipped or delivered.
func parcelUnderway(parcels []orderParcel) bool {
	for i := range parcels {
		switch parcels[i].Status {
		case parcelPending, parcelShipped, parcelDelivered:
			return true
		}
	}

	return false
}

// deliveriesView draws the order's deliveries, nil when they were not read.
// The options are quoted only when a change is offered and there is a
// delivery to change: every calculated option asks its provider for a price.
func (u *UI) deliveriesView(
	r *http.Request, detail *orderDetail, deliveries []parcelDelivery, read bool, scales map[string]int,
) *deliveriesView {
	if !read {
		return nil
	}
	view := &deliveriesView{}
	printed := func(amount int64) string {
		text, known := formatAmount(amount, detail.Currency, scales)
		return withCurrency(text, detail.Currency, known)
	}
	for _, delivery := range deliveries {
		view.Rows = append(view.Rows, deliveryRowView{ID: delivery.ID, Name: delivery.Name, Printed: printed(delivery.Amount)})
	}
	if len(deliveries) == 0 || !u.canChangeDelivery(r, detail) {
		return view
	}
	quotes, ok := u.deliveryQuotes(r, detail.ID)
	if !ok {
		view.QuoteUnread = true
		return view
	}
	view.Changeable = true
	for i, delivery := range deliveries {
		for _, quote := range quotes {
			if quote.ID == delivery.ShippingOptionID {
				continue
			}
			label := quote.Name + ", " + printed(quote.Amount)
			switch difference := quote.Amount - delivery.Amount; {
			case difference < 0:
				label += " (credits " + printed(-difference) + ")"
			case difference > 0:
				label += " (costs " + printed(difference) + " more)"
			default:
				label += " (costs the same)"
			}
			view.Rows[i].Choices = append(view.Rows[i].Choices, deliveryChoice{
				Value: quote.ID + "|" + strconv.FormatInt(quote.Amount, 10), Label: label,
			})
		}
	}

	return view
}

// deliveryQuotes reads the options the order's deliveries can be put on; ok
// is false when the quote failed.
func (u *UI) deliveryQuotes(r *http.Request, orderID string) ([]deliveryQuote, bool) {
	changer, _ := u.afterSales.(DeliveryChanger)
	ctx := r.Context()
	var quotes []deliveryQuote
	raw, err := changer.DeliveryQuoteJSON(ctx, orderID)
	if err == nil {
		err = json.Unmarshal(raw, &quotes)
	}
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx,
			"the panel could not quote the order's delivery options", "error", err, "order_id", orderID)

		return nil, false
	}

	return quotes, true
}

// changeDelivery puts the delivery in the path on the option the form names,
// at the price it was drawn with, and draws the order again saying what it
// cost; the module's and the flow's refusals, a quote that moved among them,
// are drawn on the order (ADR 0388).
func (u *UI) changeDelivery(w http.ResponseWriter, r *http.Request) {
	changer, ok := u.afterSales.(DeliveryChanger)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Orders unavailable",
			"The order module's panel surface cannot change a delivery in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	orderID := chi.URLParam(r, "id")
	optionID, quotedText, cut := strings.Cut(r.PostFormValue(formDeliveryOption), "|")
	quoted, quotedErr := strconv.ParseInt(quotedText, 10, 64)
	var answer json.RawMessage
	var err error
	if !cut || optionID == "" || quotedErr != nil {
		err = errors.Invalid("admin_ui_read_quote",
			"The option the page was drawn with could not be read; draw the page again.")
	} else {
		answer, err = changer.ChangeDelivery(r.Context(), orderID, chi.URLParam(r, "delivery"), optionID,
			strings.TrimSpace(r.PostFormValue(formDeliveryCollection)), quoted)
	}
	switch {
	case err == nil:
		u.renderOrder(w, r, http.StatusOK, orderID, &afterSaleOutcome{Done: u.deliveryChanged(r.Context(), r, answer)})
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderOrder(w, r, http.StatusUnprocessableEntity, orderID, &afterSaleOutcome{Refused: refusalOf(err)})
	default:
		u.unexpectedFailure(w, r, err, "The delivery could not be changed")
	}
}

// deliveryChanged is the sentence a change that went through shows, by what
// it cost against the delivery as it stood.
func (u *UI) deliveryChanged(ctx context.Context, r *http.Request, answer json.RawMessage) string {
	var change *deliveryChange
	if json.Unmarshal(answer, &change) != nil || change == nil {
		return "The delivery was on that option already; nothing changed."
	}
	currency := strings.ToUpper(strings.TrimSpace(r.PostFormValue(formDeliveryCurrency)))
	printed := func(amount int64) string {
		text, known := formatAmount(amount, currency, u.currencyScales(ctx))
		return withCurrency(text, currency, known)
	}
	switch {
	case change.Difference < 0:
		return fmt.Sprintf("The delivery is on %s now; %s was credited against shipping.",
			change.Name, printed(-change.Difference))
	case change.Difference > 0:
		return fmt.Sprintf("The delivery is on %s now; collection %s paid %s more.",
			change.Name, change.PaymentCollectionID, printed(change.Difference))
	default:
		return fmt.Sprintf("The delivery is on %s now; it costs the same.", change.Name)
	}
}
