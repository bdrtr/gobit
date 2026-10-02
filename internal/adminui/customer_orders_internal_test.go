package adminui

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
)

// customerOrdersPanel is a panel over one customer, answering an order read
// with as many orders as given, or the error.
func customerOrdersPanel(t *testing.T, orders int, readErr error) (*UI, *fakeCatalog) {
	t.Helper()

	catalog := &fakeCatalog{
		byEntity: map[string][]query.Record{
			EntityCustomer: {{fieldID: "cus_1", "email": "ada@example.test", fieldCustomerGroupIDs: []string{}}},
			EntityRegion:   {currencyRecord("TRY", 2)},
		},
		answer: func(spec query.GraphSpec) ([]query.Record, error, bool) {
			if spec.Entity != EntityOrder {
				return nil, nil, false
			}
			if readErr != nil {
				return nil, readErr, true
			}
			out := make([]query.Record, 0, orders)
			for i := range orders {
				out = append(out, query.Record{
					fieldID: fmt.Sprintf("order_%d", i), fieldDisplayID: int64(1000 + i), fieldStatus: "pending",
					fieldTotal: int64(12345), fieldCurrencyCod: "TRY",
					fieldPlacedAt: time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC),
				})
			}
			return out, nil, true
		},
	}
	panel := newCatalogPanel(t, catalog)
	panel.scopes = builtInScopes()

	return panel, catalog
}

// TestACustomersPageListsTheirOrders is ADR 0358: an operator who may read
// the orders too is shown the customer's newest orders, each linked, with
// its status, total and when it was placed, and a link to every one of them
// when there are more; one who may not is read none; an order read that
// fails leaves the customer on screen; and the order list lists one
// customer's orders, keeping them across its pages.
func TestACustomersPageListsTheirOrders(t *testing.T) {
	t.Parallel()

	page := CustomersPath + "/cus_1"
	panel, catalog := customerOrdersPanel(t, ordersPerCustomer+1, nil)
	rec := campaignsRequest(panel, http.MethodGet, page, nil, scopeCustomerRead, scopeOrderRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	spec := lastSpec(t, catalog, EntityOrder)
	assert.Equal(t, map[string]any{FilterOrderCustomer: "cus_1"}, spec.Filters, "the customer's orders")
	assert.Equal(t, ordersPerCustomer+1, spec.Limit, "one more than the page lists, to know there are more")
	_, section, found := strings.Cut(body, "<h2>Orders</h2>")
	require.True(t, found, "the page lists the customer's orders")
	section, _, _ = strings.Cut(section, "<h2>")
	assert.Contains(t, section, `<a href="`+OrdersPath+`/order_0">#1000</a>`)
	assert.Contains(t, section, "123.45 TRY")
	assert.Contains(t, section, "2026-10-01 08:00")
	assert.Equal(t, ordersPerCustomer, strings.Count(section, `<a href="`+OrdersPath+`/order_`), "the newest five")
	assert.Contains(t, section, `<a href="`+OrdersPath+`?customer=cus_1">Every order of this customer</a>`)

	panel, catalog = customerOrdersPanel(t, 1, nil)
	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopeCustomerRead, scopeOrderRead)
	assert.NotContains(t, rec.Body.String(), "Every order of this customer", "one order is every order")
	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopeCustomerRead)
	assert.NotContains(t, rec.Body.String(), "<h2>Orders</h2>", "an operator who may not read the orders")
	for _, spec := range catalog.specs[len(catalog.specs)-2:] {
		assert.NotEqual(t, EntityOrder, spec.Entity, "is read none")
	}
	panel, _ = customerOrdersPanel(t, 0, nil)
	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopeCustomerRead, scopeOrderRead)
	assert.Contains(t, rec.Body.String(), "No order yet.")
	panel, _ = customerOrdersPanel(t, 0, errors.New("db down"))
	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopeCustomerRead, scopeOrderRead)
	require.Equal(t, http.StatusOK, rec.Code, "the customer stays on screen")
	assert.Contains(t, rec.Body.String(), "The customer's orders could not be read.")

	panel, catalog = customerOrdersPanel(t, ordersPerPage+1, nil)
	rec = campaignsRequest(panel, http.MethodGet, OrdersPath+"?customer=+cus_1+", nil, scopeOrderRead)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, map[string]any{FilterOrderCustomer: "cus_1"}, lastSpec(t, catalog, EntityOrder).Filters)
	body = rec.Body.String()
	assert.Contains(t, body, "The orders of customer cus_1.")
	assert.Contains(t, body, `<input type="hidden" name="customer" value="cus_1">`)
	assert.Contains(t, body, `?page=2&amp;customer=cus_1">Next</a>`)
	campaignsRequest(panel, http.MethodGet, OrdersPath+"?customer=cus_1&awaiting=1", nil, scopeOrderRead)
	assert.Equal(t, map[string]any{FilterOrderCustomer: "cus_1", FilterAwaitingPayment: true},
		lastSpec(t, catalog, EntityOrder).Filters, "the boxes narrow one customer's too")
	campaignsRequest(panel, http.MethodGet, OrdersPath, nil, scopeOrderRead)
	assert.Nil(t, lastSpec(t, catalog, EntityOrder).Filters, "every order")
}

// TestTheOrderListNarrowsToAStatus is ADR 0361: the order list asks for the
// orders in the status chosen, keeps it across its pages and in its form
// beside the other narrowings, and lists every status when none, or one
// there is not, is chosen.
func TestTheOrderListNarrowsToAStatus(t *testing.T) {
	t.Parallel()

	panel, catalog := customerOrdersPanel(t, ordersPerPage+1, nil)
	rec := campaignsRequest(panel, http.MethodGet, OrdersPath+"?status=canceled", nil, scopeOrderRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, map[string]any{FilterOrderStatus: "canceled"}, lastSpec(t, catalog, EntityOrder).Filters)
	body := rec.Body.String()
	assert.Contains(t, body, `<option value="canceled" selected>canceled</option>`)
	assert.Contains(t, body, `<option value="">every status</option>`)
	assert.Contains(t, body, `?page=2&amp;status=canceled">Next</a>`)

	campaignsRequest(panel, http.MethodGet, OrdersPath+"?status=pending&customer=cus_1&placed=1", nil, scopeOrderRead)
	assert.Equal(t, map[string]any{FilterOrderStatus: "pending", FilterOrderCustomer: "cus_1", FilterPlacedByOperator: true},
		lastSpec(t, catalog, EntityOrder).Filters, "a status narrows beside the rest")
	rec = campaignsRequest(panel, http.MethodGet, OrdersPath+"?status=lost", nil, scopeOrderRead)
	assert.Nil(t, lastSpec(t, catalog, EntityOrder).Filters, "a status there is not is every status")
	assert.NotContains(t, rec.Body.String(), " selected>")
}
