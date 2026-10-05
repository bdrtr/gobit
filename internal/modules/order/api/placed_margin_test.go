package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// planted is a cost no other figure in the fixtures equals, so finding it in a
// body can only mean the cost leaked.
const planted int64 = 987_654_321

// costedDetail is the sample order whose line kept the planted cost.
func costedDetail() models.OrderDetail {
	detail := sampleDetail()
	cost := planted
	detail.Items[0].UnitCost = &cost
	return detail
}

// costedMargins is the sample order's placed margin with its cost.
func costedMargins() map[string]models.PlacedMargin {
	cost, margin := 3*planted, 3000-3*planted
	return map[string]models.PlacedMargin{"order_1": {
		OrderID: "order_1", Sales: 3000, Cost: &cost, Margin: &margin,
	}}
}

// TestTheAdminOrderCarriesItsCostAndMargin is ADR 0401's admin read: each line
// carries what a unit cost the shop and the record its placed margin, in the
// order's currency, read for the one order asked.
func TestTheAdminOrderCarriesItsCostAndMargin(t *testing.T) {
	svc := &fakeOrders{detail: costedDetail(), margins: costedMargins()}
	rec := doRequest(t, newRouter(svc), http.MethodGet, "/admin/v1/orders/order_1", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	data, ok := decodeResponse(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	items, ok := data["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)
	line, ok := items[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(planted), line["unit_cost"])
	assert.Equal(t, "Red T-Shirt", line["title"], "the admin line is the storefront's line and its cost")

	assert.Equal(t, map[string]any{
		"currency_code": "TRY", "sales": float64(3000), "cost": float64(3 * planted),
		"margin": float64(3000 - 3*planted), "lines_without_cost": float64(0),
	}, data["placed_margin"])
	assert.Equal(t, [][]string{{"order_1"}}, svc.marginIDs)
}

// TestAnOrderWithoutACostSaysSo: a line that kept no cost writes no unit_cost,
// a margin without a cost writes neither cost nor margin, and an order the
// margin read has no entry for writes no placed_margin.
func TestAnOrderWithoutACostSaysSo(t *testing.T) {
	svc := &fakeOrders{detail: sampleDetail(), margins: map[string]models.PlacedMargin{
		"order_1": {OrderID: "order_1", Sales: 3000, LinesWithoutCost: 1},
	}}
	rec := doRequest(t, newRouter(svc), http.MethodGet, "/admin/v1/orders/order_1", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, ok := decodeResponse(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	line, ok := data["items"].([]any)[0].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, line, "unit_cost")
	assert.Equal(t, map[string]any{
		"currency_code": "TRY", "sales": float64(3000), "lines_without_cost": float64(1),
	}, data["placed_margin"])

	svc = &fakeOrders{detail: sampleDetail()}
	rec = doRequest(t, newRouter(svc), http.MethodGet, "/admin/v1/orders/order_1", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, ok = decodeResponse(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, data, "placed_margin")
}

// TestEachAdminOrderRowCarriesItsMargin: the admin list reads the margins of
// its page in one call and puts each on its own row.
func TestEachAdminOrderRowCarriesItsMargin(t *testing.T) {
	second := sampleOrder()
	second.ID = "order_2"
	margins := costedMargins()
	margins["order_2"] = models.PlacedMargin{OrderID: "order_2", Sales: 500, LinesWithoutCost: 2}
	svc := &fakeOrders{orders: []models.Order{sampleOrder(), second}, count: 2, margins: margins}

	rec := doRequest(t, newRouter(svc), http.MethodGet, "/admin/v1/orders", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rows, ok := decodeResponse(t, rec)["data"].([]any)
	require.True(t, ok)
	require.Len(t, rows, 2)
	byID := map[any]any{}
	for _, raw := range rows {
		row, isMap := raw.(map[string]any)
		require.True(t, isMap)
		byID[row["id"]] = row["placed_margin"]
	}
	assert.Equal(t, map[string]any{
		"currency_code": "TRY", "sales": float64(3000), "cost": float64(3 * planted),
		"margin": float64(3000 - 3*planted), "lines_without_cost": float64(0),
	}, byID["order_1"])
	assert.Equal(t, map[string]any{
		"currency_code": "TRY", "sales": float64(500), "lines_without_cost": float64(2),
	}, byID["order_2"])
	assert.Equal(t, [][]string{{"order_1", "order_2"}}, svc.marginIDs, "one read for the page")
}

// TestTheStorefrontOrderCarriesNoCost: the storefront's order by id and its
// list of a customer's orders name no cost and not the planted amount, though
// the line kept it and the service would answer a margin for it.
func TestTheStorefrontOrderCarriesNoCost(t *testing.T) {
	svc := &fakeOrders{
		detail: costedDetail(), orders: []models.Order{sampleOrder()}, count: 1, margins: costedMargins(),
	}
	for name, rec := range map[string]func() string{
		"the order by id": func() string {
			rec := doRequest(t, newRouter(svc), http.MethodGet, "/store/v1/orders/order_1", "")
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			return rec.Body.String()
		},
		"the customer's orders": func() string {
			r := ownOrdersRouter(svc, provenAs{customerID: "cus_1"})
			rec := doRequest(t, r, http.MethodGet, "/store/v1/customers/cus_1/orders", "")
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			return rec.Body.String()
		},
	} {
		body := rec()
		require.Contains(t, body, "order_1", name)
		assert.NotContains(t, strings.ToLower(body), "cost", name)
		assert.NotContains(t, body, "margin", name)
		assert.NotContains(t, body, "987654321", name)
	}
	assert.Empty(t, svc.marginIDs, "the storefront never reads a margin")
}
