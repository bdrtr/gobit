package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// fakeMargins acts on after-sales records as the shared fake does, answers
// the scripted margins and line costs, and records what it was asked.
type fakeMargins struct {
	fakeAfterSales
	margins  string
	lines    string
	readErr  error
	marginsQ [][]string
	linesQ   []string
}

func (f *fakeMargins) PlacedMarginsJSON(_ context.Context, ids []string) (json.RawMessage, error) {
	f.marginsQ = append(f.marginsQ, ids)
	return json.RawMessage(f.margins), f.readErr
}

func (f *fakeMargins) LineCostsJSON(_ context.Context, orderID string) (json.RawMessage, error) {
	f.linesQ = append(f.linesQ, orderID)
	return json.RawMessage(f.lines), f.readErr
}

// marginCell is the placed margin cell of the list row linking to the order.
func marginCell(t *testing.T, body, orderID string) string {
	t.Helper()

	row := regexp.MustCompile(`(?s)<tr>\s*<td><a href="[^"]*/` + regexp.QuoteMeta(orderID) + `">.*?</tr>`).FindString(body)
	require.NotEmpty(t, row, "the list has a row for %s", orderID)
	cells := regexp.MustCompile(`(?s)<td class="num">(.*?)</td>`).FindAllStringSubmatch(row, -1)
	require.Len(t, cells, 2, "the row has its total and its margin: %s", row)

	return strings.TrimSpace(cells[1][1])
}

// TestTheOrderListPrintsEachOrdersPlacedMargin is ADR 0412 on the order list:
// one read for the page names every order on it; a costed order prints its
// margin in its currency, an order with a line that kept no cost and one whose
// lines are all gift cards print no margin and say why, and a zero margin is a
// margin.
func TestTheOrderListPrintsEachOrdersPlacedMargin(t *testing.T) {
	t.Parallel()

	orders := []query.Record{
		{fieldID: "order_costed", fieldStatus: "pending", fieldCurrencyCod: "TRY", fieldTotal: int64(2_400)},
		{fieldID: "order_uncosted", fieldStatus: "pending", fieldCurrencyCod: "TRY", fieldTotal: int64(1_200)},
		{fieldID: "order_cards", fieldStatus: "pending", fieldCurrencyCod: "TRY", fieldTotal: int64(5_000)},
		{fieldID: "order_even", fieldStatus: "pending", fieldCurrencyCod: "TRY", fieldTotal: int64(900)},
	}
	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityOrder: orders, EntityRegion: {currencyRecord("TRY", 2)},
	}}
	margins := &fakeMargins{margins: `[
		{"order_id":"order_even","sales":900,"cost":900,"margin":0,"lines_without_cost":0},
		{"order_id":"order_uncosted","sales":1200,"lines_without_cost":2},
		{"order_id":"order_costed","sales":2400,"cost":600,"margin":1800,"lines_without_cost":0}]`}
	panel := newCatalogPanel(t, catalog)
	panel.afterSales = margins

	rec := listOrdersAs(panel, OrdersPath, scopeOrderRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()

	assert.Equal(t, [][]string{{"order_costed", "order_uncosted", "order_cards", "order_even"}}, margins.marginsQ,
		"one read names every order on the page")
	assert.Contains(t, body, "<th class=\"num\">Placed margin</th>")
	assert.Equal(t, "18.00 TRY", marginCell(t, body, "order_costed"))
	assert.Equal(t, `<span class="muted" title="2 lines kept no cost">—</span>`, marginCell(t, body, "order_uncosted"))
	assert.Equal(t, `<span class="muted" title="no line that is not a gift card">—</span>`,
		marginCell(t, body, "order_cards"))
	assert.Equal(t, "0.00 TRY", marginCell(t, body, "order_even"), "a zero margin is printed")
}

// TestAnUnreadMarginLeavesTheListStanding: the margin read failing draws the
// list with every order and says once that the margins could not be read; a
// surface that reads no margin draws no column.
func TestAnUnreadMarginLeavesTheListStanding(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityOrder: {orderRecord()}, EntityRegion: {currencyRecord("TRY", 2)},
	}}
	panel := newCatalogPanel(t, catalog)
	panel.afterSales = &fakeMargins{readErr: errors.Unavailable("order_down", "no answer")}

	rec := listOrdersAs(panel, OrdersPath, scopeOrderRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "#1042", "the order is listed")
	assert.Contains(t, rec.Body.String(), "The orders' margins could not be read.")

	panel.afterSales = &fakeAfterSales{}
	rec = listOrdersAs(panel, OrdersPath, scopeOrderRead)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "Placed margin", "no surface, no column")
}

// TestTheOrderPagePrintsEachLinesCostAndTheMargin is ADR 0412 on the order
// page: the surface answers the lines in another order than the page lists
// them, and each cost lands on its own line; a line that kept none prints an
// empty cell; the Amounts table prints the margin with its sales and cost; a
// failed read leaves the page standing and says so.
func TestTheOrderPagePrintsEachLinesCostAndTheMargin(t *testing.T) {
	t.Parallel()

	catalog := linedOrderCatalog(func(query.GraphSpec) ([]query.Record, error) {
		return []query.Record{
			{"id": "oli_a", "title": "Kettle", "variant_id": "variant_a", "quantity": int64(1),
				"unit_price": int64(60_000), "subtotal": int64(60_000), "total": int64(60_000)},
			{"id": "oli_b", "title": "Mug", "variant_id": "variant_b", "quantity": int64(2),
				"unit_price": int64(15_000), "subtotal": int64(30_000), "total": int64(30_000)},
			{"id": "oli_c", "title": "Spoon", "variant_id": "variant_c", "quantity": int64(1),
				"unit_price": int64(1_000), "subtotal": int64(1_000), "total": int64(1_000)},
		}, nil
	})
	margins := &fakeMargins{
		margins: `[{"order_id":"order_1","sales":91000,"cost":45000,"margin":46000,"lines_without_cost":0}]`,
		lines:   `[{"line_item_id":"oli_c"},{"line_item_id":"oli_b","unit_cost":7500},{"line_item_id":"oli_a","unit_cost":30000}]`,
	}
	panel := cancelPanel(t, margins)
	panel.catalog = catalog

	rec := campaignsRequest(panel, http.MethodGet, OrdersPath+"/order_1", nil, scopeOrderRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Equal(t, [][]string{{"order_1"}}, margins.marginsQ)
	assert.Equal(t, []string{"order_1"}, margins.linesQ)

	assert.Contains(t, body, `<th class="num">Unit cost</th>`)
	for line, cost := range map[string]string{"Kettle": "300.00", "Mug": "75.00", "Spoon": ""} {
		row := regexp.MustCompile(`(?s)<tr>\s*<td>[^<]*` + line + `.*?</tr>`).FindString(body)
		require.NotEmpty(t, row, line)
		cells := regexp.MustCompile(`(?s)<td class="num">(.*?)</td>`).FindAllStringSubmatch(row, -1)
		require.GreaterOrEqual(t, len(cells), 3, row)
		assert.Equal(t, cost, strings.TrimSpace(cells[2][1]), "%s's unit cost sits after its unit price", line)
	}
	assert.Regexp(t, `(?s)<th>Placed margin</th>\s*<td class="num">\s*460.00 TRY <span class="muted">\(sales 910.00 TRY, cost 450.00 TRY\)</span>`, body)

	margins.readErr = errors.Unavailable("order_down", "no answer")
	rec = campaignsRequest(panel, http.MethodGet, OrdersPath+"/order_1", nil, scopeOrderRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "Kettle", "the lines are still listed")
	assert.Contains(t, rec.Body.String(), "What the goods cost could not be read.")
}
