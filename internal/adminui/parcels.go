package adminui

import (
	"net/http"
	"slices"
	"time"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// The Parcels screen (ADR 0356): the fulfillment module's parcels across
// every order through its shipment entity, one status at a time, the ones
// still to be shipped first, for an operator who may read the fulfillments;
// the order each was opened for is named through the order_fulfillment link
// for one who may read the orders too.

// ParcelsPath lists the parcels.
const ParcelsPath = URLPrefix + "/parcels"

// parcelsLabel is what the section is called on screen.
const parcelsLabel = "Parcels"

// EntityFulfillment is the fulfillment module's shipment entity in the read
// layer, pinned against the module's in internal/arch.
const EntityFulfillment = "fulfillment"

// parcelsPerPage is the list's page size, the other lists'.
const parcelsPerPage = 25

// The shipment's fields and filter the screen reads beside the ones the
// order page reads.
const (
	fieldParcelProvider = "provider_id"
	fieldParcelCreated  = "created_at"
	filterParcelStatus  = "status"
)

// The fulfillment module's parcel statuses, which the order page's moves are
// keyed by too.
const (
	parcelPending   = "pending"
	parcelShipped   = "shipped"
	parcelDelivered = "delivered"
	parcelReturned  = "returned"
	parcelCanceled  = "canceled"
)

// parcelStatuses are the screen's tabs, the fulfillment module's statuses
// in the order a parcel moves through them; the parcels still to be shipped
// come first, and are listed when no tab is chosen.
var parcelStatuses = []string{parcelPending, parcelShipped, parcelDelivered, parcelReturned, parcelCanceled}

// parcelRow is one parcel as the screen draws it.
type parcelRow struct {
	ID, Provider, TrackingNumber, TrackingURL string
	CreatedAt                                 time.Time
	// Moved is when the parcel reached the tab's status: shipped, delivered,
	// returned or canceled; zero for a pending one.
	Moved time.Time
	// OrderID and OrderNumber name the order the parcel was opened for, empty
	// for an operator who may not read the orders.
	OrderID, OrderNumber string
}

// movedField is the moment field each status records.
var movedField = map[string]string{
	parcelShipped: fieldShippedAt, parcelDelivered: fieldDeliveredAt, parcelReturned: fieldReturnedAt,
	parcelCanceled: fieldCanceledAt,
}

// listParcels renders the parcels in the chosen status a page at a time,
// the newest first as the module orders them.
func (u *UI) listParcels(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get(paramDeliveryStatus)
	if !slices.Contains(parcelStatuses, status) {
		status = parcelStatuses[0]
	}
	page := pageNumber(r.URL.Query().Get("page"))
	spec := query.GraphSpec{
		Entity: EntityFulfillment,
		Fields: []string{
			fieldID, fieldParcelProvider, fieldTrackingNumber, fieldTrackingURL, fieldParcelCreated,
			fieldShippedAt, fieldDeliveredAt, fieldReturnedAt, fieldCanceledAt,
		},
		Filters: map[string]any{filterParcelStatus: status},
		Limit:   parcelsPerPage + 1,
		Offset:  (page - 1) * parcelsPerPage,
	}
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if principal.HasScope(scopeOrderRead) {
		spec.Expand = []query.Expansion{{Link: linkOrderFulfillment, Fields: []string{fieldID, fieldDisplayID}}}
	}
	records, err := u.catalog.Graph(r.Context(), spec)
	if err != nil {
		u.catalogFailure(w, r, err, "The parcels could not be read.")
		return
	}
	hasNext := len(records) > parcelsPerPage
	if hasNext {
		records = records[:parcelsPerPage]
	}

	rows := make([]parcelRow, 0, len(records))
	for _, record := range records {
		row := parcelRow{
			ID: recordString(record, fieldID), Provider: recordString(record, fieldParcelProvider),
			TrackingNumber: recordString(record, fieldTrackingNumber), TrackingURL: recordString(record, fieldTrackingURL),
			CreatedAt: recordTime(record, fieldParcelCreated),
		}
		if field, ok := movedField[status]; ok {
			row.Moved = recordTime(record, field)
		}
		if orders := recordList(record[linkOrderFulfillment]); len(orders) > 0 {
			row.OrderID, row.OrderNumber = recordString(orders[0], fieldID), recordNumber(orders[0], fieldDisplayID)
		}
		rows = append(rows, row)
	}

	data := map[string]any{
		titleKey:      parcelsLabel,
		"Parcels":     rows,
		statusKey:     status,
		statusesKey:   parcelStatuses,
		ordersPathKey: OrdersPath,
	}
	addPaging(data, page, hasNext, ParcelsPath)

	u.templates.render(w, r, http.StatusOK, "parcels.gohtml", data)
}
