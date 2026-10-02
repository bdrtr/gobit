package adminui

import (
	"net/http"
	"slices"
	"time"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// The Payments screen (ADR 0357): the payment module's collections across
// every order through its collection entity, one status at a time, the ones
// authorized first, for an operator who may read the payments; the order
// each was taken for is named through the order_payment link for one who may
// read the orders too.

// PaymentsPath lists the collections.
const PaymentsPath = URLPrefix + "/payments"

// paymentsLabel is what the section is called on screen.
const paymentsLabel = "Payments"

// EntityPaymentCollection is the payment module's collection entity in the
// read layer, pinned against the module's in internal/arch.
const EntityPaymentCollection = "payment_collection"

// paymentsPerPage is the list's page size, the other lists'.
const paymentsPerPage = 25

// The collection's fields and filter the screen reads beside the amounts
// the order page reads.
const (
	fieldCollectionStatus = "status"
	filterPaymentStatus   = "status"
)

// paymentStatuses are the screen's tabs, the payment module's collection
// statuses: the ones authorized first, whose money is held in full and is to
// be captured, or recorded on the order's page when it is paid offline; then
// the rest in the order money moves, then the ones whose payment is still
// open, never paid or canceled. The first is listed when no tab is chosen.
var paymentStatuses = []string{
	"authorized", "partially_captured", "captured", "partially_refunded", "refunded", "awaiting", "not_paid",
	parcelCanceled,
}

// paymentRow is one collection as the screen draws it, its amounts in its
// currency's decimals, or marked as minor units.
type paymentRow struct {
	ID                                     string
	Amount, Authorized, Captured, Refunded string
	CreatedAt                              time.Time
	// OrderID and OrderNumber name the order the collection was taken for,
	// empty for an operator who may not read the orders.
	OrderID, OrderNumber string
}

// listPayments renders the collections in the chosen status a page at a
// time, the newest first as the module orders them.
func (u *UI) listPayments(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get(paramDeliveryStatus)
	if !slices.Contains(paymentStatuses, status) {
		status = paymentStatuses[0]
	}
	page := pageNumber(r.URL.Query().Get("page"))
	spec := query.GraphSpec{
		Entity: EntityPaymentCollection,
		Fields: []string{
			fieldID, fieldAmount, fieldCurrencyCod, fieldAuthorizedAmount, fieldCapturedAmount, fieldRefundedAmount,
			fieldCreatedAt,
		},
		Filters: map[string]any{filterPaymentStatus: status},
		Limit:   paymentsPerPage + 1,
		Offset:  (page - 1) * paymentsPerPage,
	}
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if principal.HasScope(scopeOrderRead) {
		spec.Expand = []query.Expansion{{Link: linkOrderPayment, Fields: []string{fieldID, fieldDisplayID}}}
	}
	records, err := u.catalog.Graph(r.Context(), spec)
	if err != nil {
		u.catalogFailure(w, r, err, "The payments could not be read.")
		return
	}
	hasNext := len(records) > paymentsPerPage
	if hasNext {
		records = records[:paymentsPerPage]
	}

	scales := u.currencyScales(r.Context())
	rows := make([]paymentRow, 0, len(records))
	for _, record := range records {
		currency := recordString(record, fieldCurrencyCod)
		row := paymentRow{
			ID:         recordString(record, fieldID),
			Amount:     moneyText(record, fieldAmount, currency, scales),
			Authorized: moneyText(record, fieldAuthorizedAmount, currency, scales),
			Captured:   moneyText(record, fieldCapturedAmount, currency, scales),
			Refunded:   moneyText(record, fieldRefundedAmount, currency, scales),
			CreatedAt:  recordTime(record, fieldCreatedAt),
		}
		if order, ok := linkedRecord(record[linkOrderPayment]); ok {
			row.OrderID, row.OrderNumber = recordString(order, fieldID), recordNumber(order, fieldDisplayID)
		}
		rows = append(rows, row)
	}

	data := map[string]any{
		titleKey:      paymentsLabel,
		"Payments":    rows,
		statusKey:     status,
		statusesKey:   paymentStatuses,
		ordersPathKey: OrdersPath,
	}
	addPaging(data, page, hasNext, PaymentsPath)

	u.templates.render(w, r, http.StatusOK, "payments.gohtml", data)
}
