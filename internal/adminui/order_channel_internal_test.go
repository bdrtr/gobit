package adminui

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// listOrdersAs sends a GET of the order list as an operator holding scopes.
func listOrdersAs(panel *UI, path string, scopes ...string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	request = request.WithContext(corehttp.WithPrincipal(request.Context(),
		corehttp.Principal{ID: "user_1", Kind: "user", Scopes: scopes}))

	rec := httptest.NewRecorder()
	orderRouter(panel).ServeHTTP(rec, request)

	return rec
}

// TestTheOrderListFiltersTheOrdersOfOneChannel is ADR 0410 on the panel: the
// list offers the sales channels by name to an operator who may read them, asks
// the order module for the orders placed in the one chosen, keeps it chosen,
// and the paging keeps it.
func TestTheOrderListFiltersTheOrdersOfOneChannel(t *testing.T) {
	t.Parallel()

	orders := make([]query.Record, ordersPerPage+1)
	for i := range orders {
		orders[i] = query.Record{fieldID: "order_" + strconv.Itoa(i), fieldStatus: "pending"}
	}
	channels := []query.Record{
		{fieldID: "sc_web", fieldChannelName: "Web"},
		{fieldID: "sc_store", fieldChannelName: "Store"},
	}
	catalog := &fakeCatalog{byEntity: map[string][]query.Record{EntityOrder: orders, EntitySalesChannel: channels}}

	rec := listOrdersAs(newCatalogPanel(t, catalog), OrdersPath+"?channel=sc_store&placed=1",
		scopeOrderRead, scopeAuthRead)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	spec, ok := catalog.specFor(EntityOrder)
	require.True(t, ok)
	assert.Equal(t, map[string]any{FieldOrderSalesChannel: "sc_store", FilterPlacedByOperator: true}, spec.Filters)
	body := rec.Body.String()
	assert.Contains(t, body, `<option value="sc_store" selected>Store</option>`)
	assert.Contains(t, body, `<option value="sc_web">Web</option>`)
	assert.Contains(t, body, `&amp;placed=1&amp;channel=sc_store"`, "the next page keeps the channel")

	plain := &fakeCatalog{byEntity: map[string][]query.Record{EntityOrder: {}}}
	rec = listOrdersAs(newCatalogPanel(t, plain), OrdersPath, scopeOrderRead, scopeAuthRead)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, plain.specs[0].Filters, "no channel asked, none filtered")

	// An operator who may not read the sales channels is offered none, and
	// the list asks the auth module nothing; the channel in the address still
	// filters the orders and rides in the form.
	unscoped := &fakeCatalog{byEntity: map[string][]query.Record{EntityOrder: {}, EntitySalesChannel: channels}}
	rec = listOrdersAs(newCatalogPanel(t, unscoped), OrdersPath+"?channel=sc_store", scopeOrderRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	_, asked := unscoped.specFor(EntitySalesChannel)
	assert.False(t, asked, "the channels are auth:read's, and this operator holds order:read alone")
	spec, ok = unscoped.specFor(EntityOrder)
	require.True(t, ok)
	assert.Equal(t, map[string]any{FieldOrderSalesChannel: "sc_store"}, spec.Filters)
	assert.NotContains(t, rec.Body.String(), `aria-label="sales channel"`)
	assert.Contains(t, rec.Body.String(), `<input type="hidden" name="channel" value="sc_store">`)

	// So is one whose channel list cannot be read.
	unread := &fakeCatalog{
		byEntity:    map[string][]query.Record{EntityOrder: {}},
		errByEntity: map[string]error{EntitySalesChannel: errors.Unavailable("auth_down", "no answer")},
	}
	rec = listOrdersAs(newCatalogPanel(t, unread), OrdersPath+"?channel=sc_store", scopeOrderRead, scopeAuthRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), `aria-label="sales channel"`)
	assert.Contains(t, rec.Body.String(), `<input type="hidden" name="channel" value="sc_store">`)
}

// TestTheOrderPageNamesTheChannelItWasSoldIn: the page reads the channel the
// order recorded and names it; an order from a cart that named none names no
// channel (ADR 0410).
func TestTheOrderPageNamesTheChannelItWasSoldIn(t *testing.T) {
	t.Parallel()

	catalog := addressedOrderCatalog(nil)
	answer := catalog.answer
	catalog.answer = func(spec query.GraphSpec) ([]query.Record, error, bool) {
		records, err, handled := answer(spec)
		if handled && err == nil && len(records) == 1 && records[0]["id"] == "order_1" {
			records[0][FieldOrderSalesChannel] = "sc_store"
		}
		return records, err, handled
	}
	rec := getOrderPage(newCatalogPanel(t, catalog), OrdersPath+"/order_1")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "in sales channel sc_store")
	spec, ok := catalog.specFor(EntityOrder)
	require.True(t, ok)
	assert.Contains(t, spec.Fields, FieldOrderSalesChannel)

	rec = getOrderPage(newCatalogPanel(t, addressedOrderCatalog(nil)), OrdersPath+"/order_1")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "in sales channel")
}
