package adminui

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// A customer's orders (ADR 0358): their page lists the newest of them for an
// operator who may read the orders too, and the order list lists every one
// of them when it is asked for the customer.

// paramOrderCustomer names the customer the order list lists the orders of.
const paramOrderCustomer = "customer"

// FilterOrderCustomer is the order entity's filter by the customer an order
// was placed by, the order module's name, pinned against it in
// internal/arch.
const FilterOrderCustomer = "customer_id"

// FilterOrderStatus is the order entity's filter by status, the order
// module's name, pinned against it in internal/arch (ADR 0361).
const FilterOrderStatus = "status"

// The order statuses beside [orderPending] and [orderCompleted].
const (
	orderArchived = "archived"
	orderCanceled = "canceled"
)

// orderStatuses are the order module's statuses the order list narrows to,
// in the order an order moves through them (ADR 0361).
var orderStatuses = []string{orderPending, orderCompleted, orderArchived, orderCanceled}

// ordersPerCustomer is how many of a customer's orders their page lists.
const ordersPerCustomer = 5

// customerOrders is what a customer's page says of their orders.
type customerOrders struct {
	// Unread says they could not be read, which leaves the customer on
	// screen.
	Unread bool
	Orders []orderRow
	// More says the customer has more orders than the page lists.
	More bool
}

// ordersOfCustomer reads the customer's newest orders for their page, nil
// for an operator who may not read the orders (ADR 0251).
func (u *UI) ordersOfCustomer(r *http.Request, customerID string) *customerOrders {
	ctx := r.Context()
	principal, _ := corehttp.PrincipalFromContext(ctx)
	if !principal.HasScope(scopeOrderRead) {
		return nil
	}

	records, err := u.catalog.Graph(ctx, query.GraphSpec{
		Entity:  EntityOrder,
		Fields:  []string{fieldID, fieldDisplayID, fieldStatus, fieldTotal, fieldCurrencyCod, fieldPlacedAt},
		Filters: map[string]any{FilterOrderCustomer: customerID},
		Limit:   ordersPerCustomer + 1,
	})
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx,
			"the panel could not read the customer's orders", "error", err, "customer_id", customerID)
		return &customerOrders{Unread: true}
	}

	out := &customerOrders{More: len(records) > ordersPerCustomer}
	if out.More {
		records = records[:ordersPerCustomer]
	}
	scales := u.currencyScales(ctx)
	for _, record := range records {
		out.Orders = append(out.Orders, orderRowOf(record, scales))
	}

	return out
}
