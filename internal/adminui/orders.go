package adminui

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/internal/core/query"
)

// EntityOrder is the order module's entity name in the read layer.
//
// It is a STRING and not an import: the panel knows no module (ADR 0011), the
// same way a module knows no other module. The value is the order module's
// service.EntityName and the price of the repetition is the price of isolation.
const EntityOrder = "order"

// The order fields the panel reads.
//
// They are the read layer's names, repeated here for the same reason the entity
// name is. Asking for a field the provider does not offer is refused by the
// provider itself, so a typo fails loudly on the first request rather than
// showing an empty column.
const (
	fieldDisplayID = "display_id"
	fieldEmail     = "email"
	fieldSubtotal  = "subtotal"
	fieldDiscount  = "discount_total"
	fieldTax       = "tax_total"
	fieldShipping  = "shipping_total"
	fieldTotal     = "total"
	fieldPlacedAt  = "placed_at"
)

// ordersLabel is what the section is called on screen.
//
// It is a constant because the menu, the page title and the list heading all
// print it: three copies of a word are three places to rename it in, and the
// one that is missed is the one an operator sees.
const ordersLabel = "Orders"

// ordersPerPage is the page size of the order list.
//
// It matches the catalog's, and matching is the point: two screens in one panel
// that paged differently would make an operator learn two habits for no reason.
const ordersPerPage = 25

// orderRow is one line of the order table.
type orderRow struct {
	ID        string
	DisplayID string
	Status    string
	Email     string
	// Total is the amount as it is printed, and Minor says whether the scale
	// was known — see [formatAmount] for why an unknown scale is never guessed.
	Total    string
	Minor    bool
	Currency string
	PlacedAt time.Time
}

// orderDetail is the order page's view of one order.
type orderDetail struct {
	orderRow
	// The remaining money fields, each formatted the same way Total is.
	Subtotal string
	Discount string
	Tax      string
	Shipping string
}

// listOrders renders the order list.
//
// It reads through the cross-module read layer, exactly as the catalog does and
// for the same reason (ADR 0011): the panel knows no module, so the screen
// cannot drift from what the framework actually serves.
//
// # Why there is no total count
//
// One extra record is fetched to answer "is there a next page". A count over a
// growing order table is the expensive half of pagination — measured at 3.03 ms
// against 0.47 ms for the page itself on a 52,000-row fixture — and an operator
// paging through orders does not need to be told there are 41,207 of them.
func (u *UI) listOrders(w http.ResponseWriter, r *http.Request) {
	page := pageNumber(r.URL.Query().Get("page"))

	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity: EntityOrder,
		Fields: []string{
			fieldID, fieldDisplayID, fieldStatus, fieldEmail,
			fieldTotal, fieldCurrencyCod, fieldPlacedAt,
		},
		Limit:  ordersPerPage + 1,
		Offset: (page - 1) * ordersPerPage,
	})
	if err != nil {
		u.catalogFailure(w, r, err, "The order list could not be read.")

		return
	}

	hasNext := len(records) > ordersPerPage
	if hasNext {
		records = records[:ordersPerPage]
	}

	scales := u.currencyScales(r.Context())

	rows := make([]orderRow, 0, len(records))
	for _, rec := range records {
		rows = append(rows, orderRowOf(rec, scales))
	}

	data := map[string]any{
		titleKey: ordersLabel,
		"Orders": rows,
	}
	addPaging(data, page, hasNext, OrdersPath)

	u.templates.render(w, r, http.StatusOK, "orders.gohtml", data)
}

// showOrder renders one order.
//
// The LINES are deliberately absent. They are the order module's own records
// and the read layer joins across LINKS, not within a module — so showing them
// would need the panel to hold the order module's service, which is the
// coupling the fourth tree exists to avoid. What this screen answers is the
// question an operator opens an order for: who, when, how much, and where it
// stands.
func (u *UI) showOrder(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if strings.TrimSpace(id) == "" {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "No order was named.")

		return
	}

	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity: EntityOrder,
		Fields: []string{
			fieldID, fieldDisplayID, fieldStatus, fieldEmail, fieldCurrencyCod,
			fieldSubtotal, fieldDiscount, fieldTax, fieldShipping, fieldTotal,
			fieldPlacedAt,
		},
		Filters: map[string]any{filterID: []string{id}},
		Limit:   1,
	})
	if err != nil {
		u.catalogFailure(w, r, err, "The order could not be read.")

		return
	}

	if len(records) == 0 {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "There is no such order.")

		return
	}

	scales := u.currencyScales(r.Context())
	record := records[0]

	detail := orderDetail{orderRow: orderRowOf(record, scales)}
	detail.Subtotal, _ = amountField(record, fieldSubtotal, detail.Currency, scales)
	detail.Discount, _ = amountField(record, fieldDiscount, detail.Currency, scales)
	detail.Tax, _ = amountField(record, fieldTax, detail.Currency, scales)
	detail.Shipping, _ = amountField(record, fieldShipping, detail.Currency, scales)

	u.templates.render(w, r, http.StatusOK, "order.gohtml", map[string]any{
		titleKey:     "Order " + detail.DisplayID,
		"Order":      detail,
		"OrdersPath": OrdersPath,
	})
}

// orderRowOf turns an order record into a row.
//
// The list and the detail page share it so the two screens cannot start reading
// the same order differently — a field renamed in one and not the other would
// show a total on one screen and a blank on the other.
func orderRowOf(rec query.Record, scales map[string]int) orderRow {
	currency := recordString(rec, fieldCurrencyCod)
	total, known := amountField(rec, fieldTotal, currency, scales)

	return orderRow{
		ID:        recordString(rec, fieldID),
		DisplayID: recordNumber(rec, fieldDisplayID),
		Status:    recordString(rec, fieldStatus),
		Email:     recordString(rec, fieldEmail),
		Total:     total,
		Minor:     !known,
		Currency:  currency,
		PlacedAt:  recordTime(rec, fieldPlacedAt),
	}
}

// recordInt reads an integer field, or zero when it is absent or not one.
//
// It refuses a FLOAT deliberately, the same way [intValue] does: a money amount
// that arrived as a float has already lost precision, and printing it would show
// a wrong figure confidently (plan Section 8 — money is never a float).
func recordInt(rec query.Record, field string) int64 {
	value, ok := intValue(rec[field])
	if !ok {
		return 0
	}

	return int64(value)
}

// recordNumber reads a numeric field as text for display.
//
// The display id is a NUMBER in the record and a label on the screen; turning it
// into text here keeps the template from having to know that.
func recordNumber(rec query.Record, field string) string {
	value, ok := intValue(rec[field])
	if !ok {
		return ""
	}

	return strconv.Itoa(value)
}

// amountField formats one money field of a record.
//
// The second result says whether the currency's SCALE was known; when it was
// not the caller shows the figure as minor units rather than guessing two
// digits, which would print the wrong amount for every 0-digit and 3-digit
// currency and print it confidently.
func amountField(
	rec query.Record, field, currency string, scales map[string]int,
) (string, bool) {
	return formatAmount(recordInt(rec, field), currency, scales)
}
