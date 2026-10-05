package adminui

import (
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// The order list's filter for the orders awaiting their payment (ADR 0294):
// the box's parameter, and the order module's filter, spelled again for
// [EntityOrder]'s reason and pinned against the module's in internal/arch.
const (
	paramAwaiting         = "awaiting"
	FilterAwaitingPayment = "awaiting_payment"
)

// The order list's filter for the orders an operator placed and the order
// page's field naming them (ADR 0298), the order module's names, pinned as the
// awaiting filter is.
const (
	paramPlaced            = "placed"
	FilterPlacedByOperator = "placed_by_operator"
	FieldPlacedBy          = "placed_by"
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

	// The fields ADR 0196 reads: the order an addition adds to, the two
	// current addresses, and the moment of the latest address correction.
	fieldAddsToOrderID              = "adds_to_order_id"
	fieldShippingAddress            = "shipping_address"
	fieldBillingAddress             = "billing_address"
	fieldShippingAddressCorrectedAt = "shipping_address_corrected_at"
)

// additionsPerOrder is how many additions the order page lists. An order with
// more has been added to more often than a screen should have to show.
const additionsPerOrder = 25

// The order line fields the order page reads beside the ones the sales report
// already names. They are the order module's line entity names, repeated for
// the reason [EntityOrder] is.
const (
	fieldIsGiftcard       = "is_giftcard"
	fieldProperties       = "properties"
	fieldParentLineItemID = "parent_line_item_id"
	fieldAskedBack        = "asked_back_quantity"
	fieldCanceledQuantity = "canceled_quantity"
	// fieldProductTitle is the product a line sold, beside its variant (ADR
	// 0365); empty on a line written before.
	fieldProductTitle = "product_title"
)

// The links the order page expands to reach the order's payment and its
// parcels, and the fields it reads from each. They are the payment and
// fulfillment modules' names, repeated for the reason [EntityOrder] is.
const (
	linkOrderPayment     = "order_payment"
	linkOrderFulfillment = "order_fulfillment"

	fieldAuthorizedAmount = "authorized_amount"
	fieldCapturedAmount   = "captured_amount"
	fieldRefundedAmount   = "refunded_amount"
	fieldFirstCapturedAt  = "first_captured_at"
	fieldLastRefundedAt   = "last_refunded_at"

	fieldTrackingNumber = "tracking_number"
	fieldTrackingURL    = "tracking_url"
	fieldShippedAt      = "shipped_at"
	fieldDeliveredAt    = "delivered_at"
	fieldCanceledAt     = "canceled_at"
	fieldReturnedAt     = "returned_at"
	fieldItems          = "items"

	// The keys of one of a parcel's items.
	itemLineItemID = "line_item_id"
	itemQuantity   = "quantity"
)

// linesPerOrder is how many lines the order page reads. It is the line
// entity's own ceiling — the provider clamps a larger limit to it — and the
// most distinct lines a cart may carry, so only an order grown by exchanges
// reaches it, and the page says so.
const linesPerOrder = 100

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

	// ShipTo and BillTo are the current addresses as printed lines; nil when
	// the order recorded none (ADR 0196).
	ShipTo []string
	BillTo []string
	// CorrectedAt is the latest correction of the shipping address; the zero
	// time when it was never corrected (ADR 0195).
	CorrectedAt time.Time
	// ShipToID is the current shipping address row, which the correction
	// form carries as read; ShipToForm the form's fields, ShipToCountry the
	// country a correction keeps, and AddressRefused that the form is drawn
	// with what a refused correction typed (ADR 0388).
	ShipToID       string
	ShipToForm     map[string]string
	ShipToCountry  string
	AddressRefused bool
	// PlacedBy is the operator who placed the order; empty on a shopper's
	// (ADR 0298).
	PlacedBy string
	// Parent is the order this one adds to; nil when it adds to nothing, and
	// its ID alone when the parent could not be read.
	Parent *orderRow
	// Additions are the orders that add to this one (ADR 0192).
	Additions []orderRow
	// AdditionsUnread says the additions could not be read. The page still
	// shows the order: a secondary read failing is not the order failing.
	AdditionsUnread bool

	// Credits are the order's credits, oldest first (ADR 0388); CreditedTotal
	// is their sum as printed and CreditedRead the same sum in minor units,
	// which the credit form carries. CreditsShown says the order module's
	// surface lists them, and CreditsUnread that the read failed.
	Credits       []orderCredit
	CreditedTotal string
	CreditedRead  int64
	CreditsShown  bool
	CreditsUnread bool

	// Lines are the order's lines in the order they were written, each add-on
	// under the line it belongs to.
	Lines []orderLine
	// LinesUnread says the lines could not be read, for the reason
	// AdditionsUnread gives; LinesMore that the order has more lines than the
	// page reads.
	LinesUnread bool
	LinesMore   bool

	// Payment is the order's payment collection; nil when none is linked or
	// it was not read. Parcels are its parcels, oldest first.
	Payment *orderPayment
	Parcels []orderParcel
	// PaymentHidden and ParcelsHidden say the operator lacks the privilege to
	// read them (ADR 0251); PaymentUnread and ParcelsUnread that the read
	// failed.
	PaymentHidden bool
	PaymentUnread bool
	ParcelsHidden bool
	ParcelsUnread bool

	// AfterSales are the order's returns, claims, exchanges and replacements,
	// newest first (ADR 0270); AfterSalesMore says a kind has more than the
	// page reads and AfterSalesUnread that a read failed.
	AfterSales       []orderAfterSale
	AfterSalesMore   bool
	AfterSalesUnread bool

	// Notifications are what the notification module sent for the order,
	// newest first (ADR 0318); NotificationsHidden says the operator lacks
	// the privilege to read them, NotificationsUnread that the read failed,
	// and NotificationsMore that the order has more than the page reads.
	Notifications       []deliveryRow
	NotificationsHidden bool
	NotificationsUnread bool
	NotificationsMore   bool
}

// orderPayment is the order's payment collection as the page prints it.
type orderPayment struct {
	Status string
	// The amounts, each formatted the way the order's are, in the
	// collection's own currency.
	Amount     string
	Authorized string
	Captured   string
	Refunded   string
	Currency   string
	// FirstCapturedAt and LastRefundedAt are when the money moved; nil when
	// it has not.
	FirstCapturedAt *time.Time
	LastRefundedAt  *time.Time
	// Awaiting are the sessions whose money the shop records when it arrives
	// (ADR 0287).
	Awaiting []orderAwaiting
}

// orderParcel is one parcel of the order.
type orderParcel struct {
	ID             string
	Status         string
	TrackingNumber string
	TrackingURL    string
	CreatedAt      time.Time
	// The moments of the parcel's life; nil for one that has not happened.
	ShippedAt   *time.Time
	DeliveredAt *time.Time
	CanceledAt  *time.Time
	ReturnedAt  *time.Time
	// Holds is what the parcel carries, one "title × quantity" per line
	// (ADR 0252).
	Holds []string
}

// orderLine is one line of the order page.
type orderLine struct {
	ID    string
	Title string
	// ProductTitle is the product the line sold (ADR 0365), or empty on a
	// line written before the order kept it.
	ProductTitle string
	VariantID    string
	Quantity     int64
	// The amounts, each formatted the way the order's are.
	UnitPrice string
	Subtotal  string
	Discount  string
	Tax       string
	Total     string
	// GiftCard says the line sold gift cards (ADR 0211).
	GiftCard bool
	// Properties are what the shopper wrote on the line (ADR 0223), as
	// "name: text" in name order.
	Properties []string
	// AddOn says the line is printed under the line it is an add-on of
	// (ADR 0229).
	AddOn bool
	// AskedBack is how many units a live return asks back and Canceled how
	// many were written off (ADR 0252).
	AskedBack int64
	Canceled  int64
}

// addressLines lays an address record out the way a label reads: the name, the
// company, the street lines, then postal code, city and province on one line,
// the country and the phone. Empty parts are left out.
func addressLines(value any) []string {
	address, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	text := func(name string) string { return strings.TrimSpace(stringValue(address[name])) }
	joined := func(parts ...string) string {
		kept := make([]string, 0, len(parts))
		for _, part := range parts {
			if part != "" {
				kept = append(kept, part)
			}
		}

		return strings.Join(kept, " ")
	}

	var lines []string
	for _, line := range []string{
		joined(text("first_name"), text("last_name")),
		text("company"),
		text("address_1"),
		text("address_2"),
		joined(text("postal_code"), text("city"), text("province")),
		text("country_code"),
		text("phone"),
	} {
		if line != "" {
			lines = append(lines, line)
		}
	}

	return lines
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
	// The orders awaiting their payment, an offline method's say (ADR 0294),
	// and the ones an operator placed (ADR 0298); the two boxes narrow
	// together.
	awaiting := r.URL.Query().Get(paramAwaiting) == "1"
	placed := r.URL.Query().Get(paramPlaced) == "1"
	// And one customer's, which their page links to (ADR 0358), and the ones
	// in one status (ADR 0361).
	customer := strings.TrimSpace(r.URL.Query().Get(paramOrderCustomer))
	status := r.URL.Query().Get(paramDeliveryStatus)
	if !slices.Contains(orderStatuses, status) {
		status = ""
	}
	var filters map[string]any
	if awaiting || placed || customer != "" || status != "" {
		filters = map[string]any{}
	}
	if status != "" {
		filters[FilterOrderStatus] = status
	}
	if awaiting {
		filters[FilterAwaitingPayment] = true
	}
	if placed {
		filters[FilterPlacedByOperator] = true
	}
	if customer != "" {
		filters[FilterOrderCustomer] = customer
	}

	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity: EntityOrder,
		Fields: []string{
			fieldID, fieldDisplayID, fieldStatus, fieldEmail,
			fieldTotal, fieldCurrencyCod, fieldPlacedAt,
		},
		Filters: filters,
		Limit:   ordersPerPage + 1,
		Offset:  (page - 1) * ordersPerPage,
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
		titleKey:    ordersLabel,
		"Orders":    rows,
		"Awaiting":  awaiting,
		"Placed":    placed,
		"Customer":  customer,
		statusKey:   status,
		statusesKey: orderStatuses,
	}
	addPaging(data, page, hasNext, OrdersPath)

	u.templates.render(w, r, http.StatusOK, "orders.gohtml", data)
}

// showOrder renders one order.
//
// The lines are read through the order module's line entity, the surface the
// sales report in this package reads (D96), filtered to this order. They used
// to be absent, and the reason this comment gave for it was false.
func (u *UI) showOrder(w http.ResponseWriter, r *http.Request) {
	u.renderOrder(w, r, http.StatusOK, chi.URLParam(r, "id"), nil)
}

// renderOrder reads the order and writes its page, with what an act on one of
// its after-sales records reported when one was taken (ADR 0271).
func (u *UI) renderOrder(
	w http.ResponseWriter, r *http.Request, status int, id string, outcome *afterSaleOutcome,
) {
	// An act lands here under order:write, which does not open the order page
	// (ADR 0260): an operator holding only the write is told what happened and
	// reads nothing of the order.
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if outcome != nil && !principal.HasScope(scopeOrderRead) {
		title, message := "Done", outcome.Done
		if outcome.Refused != "" {
			title, message = "Not done", outcome.Refused
		}
		u.errorPage(w, r, status, title, strings.Join(append([]string{message}, outcome.Warnings...), " "))
		return
	}

	if strings.TrimSpace(id) == "" {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "No order was named.")

		return
	}

	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity: EntityOrder,
		Fields: []string{
			fieldID, fieldDisplayID, fieldStatus, fieldEmail, fieldCurrencyCod,
			fieldSubtotal, fieldDiscount, fieldTax, fieldShipping, fieldTotal,
			fieldPlacedAt, fieldAddsToOrderID, fieldShippingAddress,
			fieldBillingAddress, fieldShippingAddressCorrectedAt, FieldPlacedBy, fieldShippingAddressID,
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
	detail.ShipTo = addressLines(record[fieldShippingAddress])
	detail.BillTo = addressLines(record[fieldBillingAddress])
	detail.CorrectedAt = recordTime(record, fieldShippingAddressCorrectedAt)
	detail.ShipToID = recordString(record, fieldShippingAddressID)
	shipToForm(&detail, record[fieldShippingAddress], outcome)
	detail.PlacedBy = recordString(record, FieldPlacedBy)
	detail.Parent = u.parentOrder(r, recordString(record, fieldAddsToOrderID))
	detail.Additions, detail.AdditionsUnread = u.additionsOf(r, detail.ID, scales)
	detail.Lines, detail.LinesMore, detail.LinesUnread = u.linesOf(r, detail.ID, detail.Currency, scales)
	u.creditsOf(r, &detail, scales)

	detail.PaymentHidden = !principal.HasScope(scopePaymentRead)
	if !detail.PaymentHidden {
		detail.Payment, detail.PaymentUnread = u.paymentOf(r, detail.ID, scales)
	}
	detail.ParcelsHidden = !principal.HasScope(scopeFulfillmentRead)
	if !detail.ParcelsHidden {
		detail.Parcels, detail.ParcelsUnread = u.parcelsOf(r, detail.ID, detail.Lines)
	}
	detail.AfterSales, detail.AfterSalesMore, detail.AfterSalesUnread = u.afterSalesOf(
		r, detail.ID, detail.Currency, scales, detail.Lines)
	u.withClaimEvidence(r, detail.AfterSales)
	detail.NotificationsHidden = !principal.HasScope(scopeNotificationRead)
	if u.notifications != nil && !detail.NotificationsHidden {
		detail.Notifications, detail.NotificationsMore, detail.NotificationsUnread = u.notificationsOf(r, detail.ID)
	}

	deliveries, deliveriesRead := u.deliveriesOf(r, detail.ID)
	u.templates.render(w, r, status, "order.gohtml", map[string]any{
		titleKey:                "Order " + detail.DisplayID,
		"Outcome":               outcome,
		"CanAct":                u.afterSales != nil && principal.HasScope(scopeOrderWrite),
		"CanRecordPayment":      u.payments != nil && principal.HasScope(scopePaymentWrite),
		"Order":                 detail,
		ordersPathKey:           OrdersPath,
		"LinesPerOrder":         linesPerOrder,
		"PaymentPrivilege":      scopePaymentRead,
		"FulfillmentPrivilege":  scopeFulfillmentRead,
		"ParcelMoves":           parcelMoves,
		"CanMoveParcels":        u.canMoveParcels(r),
		"ParcelOpening":         u.parcelOpeningFor(r, deliveries, deliveriesRead),
		"Deliveries":            u.deliveriesView(r, &detail, deliveries, deliveriesRead, scales),
		"Invoice":               u.invoiceOf(r, detail.ID, detail.Currency, scales),
		"CanCancel":             u.canCancelOrder(r, detail.Status),
		"CloseMove":             u.orderCloseMove(r, detail.Status),
		"CanWriteOff":           u.canWriteOff(r, detail.Status),
		"CanCredit":             u.canCredit(r, &detail),
		"CanCorrectAddress":     u.canCorrectAddress(r, &detail),
		"CanAttachEvidence":     u.canAttachEvidence(r),
		"CanDetachEvidence":     u.canDetachEvidence(r),
		"NotificationsShown":    u.notifications != nil,
		"NotificationPrivilege": scopeNotificationRead,
		"NotificationsPath":     NotificationsPath,
		"NotificationsPerOrder": notificationsPerOrder,
		"AfterSalesPerKind":     afterSalesPerKind,
		"ReplacementSources":    replacementSources(detail.AfterSales),
	})
}

// parentOrder reads the order an addition adds to; nil when it adds to nothing.
//
// A parent that cannot be read is still named by its id: the link is the fact
// the operator needs, and its number is only the label.
func (u *UI) parentOrder(r *http.Request, parentID string) *orderRow {
	if parentID == "" {
		return nil
	}

	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity:  EntityOrder,
		Fields:  []string{fieldID, fieldDisplayID},
		Filters: map[string]any{filterID: []string{parentID}},
		Limit:   1,
	})
	if err != nil || len(records) == 0 {
		return &orderRow{ID: parentID}
	}
	parent := orderRowOf(records[0], nil)

	return &parent
}

// additionsOf reads the orders that add to the given one, newest first; the
// second value reports a read that failed.
func (u *UI) additionsOf(r *http.Request, orderID string, scales map[string]int) ([]orderRow, bool) {
	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity: EntityOrder,
		Fields: []string{
			fieldID, fieldDisplayID, fieldStatus, fieldCurrencyCod, fieldTotal, fieldPlacedAt,
		},
		Filters: map[string]any{fieldAddsToOrderID: orderID},
		Limit:   additionsPerOrder,
	})
	if err != nil {
		return nil, true
	}

	rows := make([]orderRow, 0, len(records))
	for _, record := range records {
		rows = append(rows, orderRowOf(record, scales))
	}

	return rows, false
}

// linesOf reads an order's lines; the second value reports more lines than
// were read and the third a read that failed.
//
// The line entity lists one order's lines in the order they were written
// (D174) and reads at most [linesPerOrder] at a time, so a full read is
// followed by a one-row read past it. When that second read fails the page
// says only the first lines are shown, which is true either way.
func (u *UI) linesOf(
	r *http.Request, orderID, currency string, scales map[string]int,
) ([]orderLine, bool, bool) {
	spec := query.GraphSpec{
		Entity: EntityOrderLineItem,
		Fields: []string{
			fieldID, fieldTitle, fieldVariantID, fieldQuantity, fieldUnitPrice,
			fieldSubtotal, fieldDiscount, fieldTax, fieldTotal,
			fieldIsGiftcard, fieldProperties, fieldParentLineItemID,
			fieldAskedBack, fieldCanceledQuantity, fieldProductTitle,
		},
		Filters: map[string]any{fieldOrderID: orderID},
		Limit:   linesPerOrder,
	}
	records, err := u.catalog.Graph(r.Context(), spec)
	if err != nil {
		return nil, false, true
	}

	more := false
	if len(records) >= linesPerOrder {
		records = records[:linesPerOrder]
		probe := spec
		probe.Fields = []string{fieldID}
		probe.Offset = linesPerOrder
		probe.Limit = 1
		beyond, err := u.catalog.Graph(r.Context(), probe)
		more = err != nil || len(beyond) > 0
	}

	lines := make([]orderLine, 0, len(records))
	parents := make([]string, 0, len(records))
	for _, record := range records {
		line := orderLine{
			ID:           recordString(record, fieldID),
			Title:        recordString(record, fieldTitle),
			ProductTitle: recordString(record, fieldProductTitle),
			VariantID:    recordString(record, fieldVariantID),
			Quantity:     recordInt(record, fieldQuantity),
			GiftCard:     recordBool(record, fieldIsGiftcard),
			Properties:   propertyLines(record[fieldProperties]),
			AskedBack:    recordInt(record, fieldAskedBack),
			Canceled:     recordInt(record, fieldCanceledQuantity),
		}
		line.UnitPrice, _ = amountField(record, fieldUnitPrice, currency, scales)
		line.Subtotal, _ = amountField(record, fieldSubtotal, currency, scales)
		line.Discount, _ = amountField(record, fieldDiscount, currency, scales)
		line.Tax, _ = amountField(record, fieldTax, currency, scales)
		line.Total, _ = amountField(record, fieldTotal, currency, scales)
		lines = append(lines, line)
		parents = append(parents, recordString(record, fieldParentLineItemID))
	}

	return nestAddOns(lines, parents), more, false
}

// nestAddOns prints each add-on under the line it belongs to and keeps the
// written order otherwise; parents[i] is the parent of lines[i], empty for a
// line of its own.
//
// An add-on whose line was not read stays where it was written, and no line is
// dropped: lines whose parents point at each other, which nothing places under
// a root, are appended in written order.
func nestAddOns(lines []orderLine, parents []string) []orderLine {
	read := make(map[string]bool, len(lines))
	for i := range lines {
		read[lines[i].ID] = true
	}

	children := make(map[string][]int)
	var roots []int
	for i, parent := range parents {
		if parent != "" && parent != lines[i].ID && read[parent] {
			children[parent] = append(children[parent], i)
			continue
		}
		roots = append(roots, i)
	}

	out := make([]orderLine, 0, len(lines))
	placed := make([]bool, len(lines))
	var place func(i int, addOn bool)
	place = func(i int, addOn bool) {
		if placed[i] {
			return
		}
		placed[i] = true
		line := lines[i]
		line.AddOn = addOn
		out = append(out, line)
		for _, child := range children[line.ID] {
			place(child, true)
		}
	}
	for _, i := range roots {
		place(i, false)
	}
	for i := range lines {
		place(i, false)
	}

	return out
}

// propertyLines reads a line's properties as "name: text" in name order.
//
// In process the line entity hands them over as map[string]string; a map of
// any is read too, since that is the shape a record takes once it has been
// through JSON.
func propertyLines(value any) []string {
	properties := map[string]string{}
	switch typed := value.(type) {
	case map[string]string:
		properties = typed
	case map[string]any:
		for name, text := range typed {
			if text, ok := text.(string); ok {
				properties[name] = text
			}
		}
	}

	out := make([]string, 0, len(properties))
	for _, name := range slices.Sorted(maps.Keys(properties)) {
		out = append(out, name+": "+properties[name])
	}

	return out
}

// linkedTo reads what one link of the order reaches.
//
// The order is read again by its id with that one expansion, so a failure costs
// the section it feeds and not the page: the main read carries no expansion
// for the same reason the additions have a read of their own.
func (u *UI) linkedTo(r *http.Request, orderID string, expansion query.Expansion) (any, error) {
	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity:  EntityOrder,
		Fields:  []string{fieldID},
		Filters: map[string]any{filterID: []string{orderID}},
		Limit:   1,
		Expand:  []query.Expansion{expansion},
	})
	if err != nil || len(records) == 0 {
		return nil, err
	}

	return records[0][expansion.Link], nil
}

// paymentOf reads the order's payment collection through the order_payment
// link; the second value reports a read that failed.
//
// The collection is linked rather than filtered: its reference is the cart the
// checkout opened it for, and the link is what binds it to the order.
func (u *UI) paymentOf(r *http.Request, orderID string, scales map[string]int) (*orderPayment, bool) {
	linked, err := u.linkedTo(r, orderID, query.Expansion{
		Link: linkOrderPayment,
		Fields: []string{
			fieldStatus, fieldAmount, fieldCurrencyCod, fieldAuthorizedAmount,
			fieldCapturedAmount, fieldRefundedAmount, fieldFirstCapturedAt, fieldLastRefundedAt,
			fieldAwaiting,
		},
	})
	if err != nil {
		return nil, true
	}
	records := linkedRecords(linked)
	if len(records) == 0 {
		return nil, false
	}

	record := records[0]
	payment := orderPayment{
		Status:          recordString(record, fieldStatus),
		Currency:        recordString(record, fieldCurrencyCod),
		FirstCapturedAt: recordAt(record, fieldFirstCapturedAt),
		LastRefundedAt:  recordAt(record, fieldLastRefundedAt),
	}
	payment.Amount, _ = amountField(record, fieldAmount, payment.Currency, scales)
	payment.Authorized, _ = amountField(record, fieldAuthorizedAmount, payment.Currency, scales)
	payment.Captured, _ = amountField(record, fieldCapturedAmount, payment.Currency, scales)
	payment.Refunded, _ = amountField(record, fieldRefundedAmount, payment.Currency, scales)
	payment.Awaiting = awaitingOf(record, payment.Currency, scales)

	return &payment, false
}

// parcelsOf reads the order's parcels through the order_fulfillment link,
// oldest first; the second value reports a read that failed.
//
// A parcel's items name order lines, and the lines already read give their
// titles. A line the page did not read — a parent's line in a parcel an
// addition joined, or one past the page's hundred — is named by its id.
func (u *UI) parcelsOf(r *http.Request, orderID string, lines []orderLine) ([]orderParcel, bool) {
	linked, err := u.linkedTo(r, orderID, query.Expansion{
		Link: linkOrderFulfillment,
		Fields: []string{
			fieldID, fieldStatus, fieldTrackingNumber, fieldTrackingURL, fieldCreatedAt,
			fieldShippedAt, fieldDeliveredAt, fieldCanceledAt, fieldReturnedAt, fieldItems,
		},
	})
	if err != nil {
		return nil, true
	}

	titles := make(map[string]string, len(lines))
	for i := range lines {
		titles[lines[i].ID] = lines[i].Title
	}

	records := linkedRecords(linked)
	parcels := make([]orderParcel, 0, len(records))
	for _, record := range records {
		parcels = append(parcels, orderParcel{
			ID:             recordString(record, fieldID),
			Status:         recordString(record, fieldStatus),
			TrackingNumber: recordString(record, fieldTrackingNumber),
			TrackingURL:    recordString(record, fieldTrackingURL),
			CreatedAt:      recordTime(record, fieldCreatedAt),
			ShippedAt:      recordAt(record, fieldShippedAt),
			DeliveredAt:    recordAt(record, fieldDeliveredAt),
			CanceledAt:     recordAt(record, fieldCanceledAt),
			ReturnedAt:     recordAt(record, fieldReturnedAt),
			Holds:          parcelHolds(record[fieldItems], titles),
		})
	}
	// The link promises no order, so the page gives its own: oldest first, the
	// id breaking a tie between two parcels of one moment.
	slices.SortStableFunc(parcels, func(a, b orderParcel) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})

	return parcels, false
}

// parcelHolds reads a parcel's items as "title × quantity", in the order the
// parcel lists them.
func parcelHolds(value any, titles map[string]string) []string {
	items, _ := value.([]map[string]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		line := stringValue(item[itemLineItemID])
		name := titles[line]
		if name == "" {
			name = line
		}
		quantity, _ := intValue(item[itemQuantity])
		out = append(out, name+" × "+strconv.Itoa(quantity))
	}

	return out
}

// linkedRecords reads an expansion's value as records: a one-ended link
// writes a record or nil, a many-ended one a slice.
func linkedRecords(value any) []query.Record {
	switch typed := value.(type) {
	case query.Record:
		if typed == nil {
			return nil
		}
		return []query.Record{typed}
	case []query.Record:
		return typed
	default:
		return nil
	}
}

// recordAt reads a moment that may be absent, whether the provider hands it
// over as a time or as a pointer to one; nil when absent or zero.
func recordAt(rec query.Record, field string) *time.Time {
	var at time.Time
	switch typed := rec[field].(type) {
	case time.Time:
		at = typed
	case *time.Time:
		if typed != nil {
			at = *typed
		}
	}
	if at.IsZero() {
		return nil
	}

	return &at
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
