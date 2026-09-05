package adminui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// getSalesPage sends a GET as a SIGNED-IN operator and returns the recorder.
//
// The identity matters to the frame rather than to the handler: the menu is
// drawn only for a request that carries a principal, so a test that skipped it
// would exercise the logged-out frame and could never see the section marked.
func getSalesPage(panel *UI, path string) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	router.Get(SalesPath, panel.listSales)

	request := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	request = request.WithContext(corehttp.WithPrincipal(request.Context(),
		corehttp.Principal{ID: "user_1", Kind: "user"}))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, request)

	return rec
}

// lineRecord is one sold line as the read layer hands it over.
//
// It carries NONE of the order's facts — no currency, no placed_at, no display
// number — because the line entity carries none: they are read from the "order"
// entity through order_id, which is what the second Graph call is for. A
// fixture that put them on the line would let the screen pass this test while
// never making that call.
func lineRecord() query.Record {
	return query.Record{
		"id": "line_1", "order_id": "order_1", "variant_id": "variant_1",
		"title": "Red T-Shirt (M)", "quantity": int64(2),
		"unit_price": int64(45_000), "total": int64(108_000),
	}
}

// salesCatalog seeds a read layer with one sold line, its order and the scales.
func salesCatalog() *fakeCatalog {
	return &fakeCatalog{byEntity: map[string][]query.Record{
		EntityOrderLineItem: {lineRecord()},
		EntityOrder:         {orderRecord()},
		EntityRegion:        {currencyRecord("TRY", 2)},
	}}
}

// TestSalesReportRendersSoldLines proves the two reads reach one row.
//
// The line supplies what sold and for how much; the ORDER supplies the day, the
// number and the currency. A row that printed the first three and lost the last
// three would mean the second call never happened, so all six are asserted.
func TestSalesReportRendersSoldLines(t *testing.T) {
	t.Parallel()

	catalog := salesCatalog()

	rec := getSalesPage(newCatalogPanel(t, catalog), SalesPath)

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "Red T-Shirt (M)", "the title the line was SOLD under")
	assert.Contains(t, body, "variant_1", "the variant id an operator pastes into a search")
	assert.Contains(t, body, "#1042", "the order's display number comes from the second read")
	assert.Contains(t, body, "2026-09-04", "the day is the ORDER's placed_at")
	assert.Contains(t, body, "450.00 TRY", "the unit price, scaled by the currency's digits")
	assert.Contains(t, body, "1080.00 TRY", "the line total, scaled the same way")

	spec, ok := catalog.specFor(EntityOrderLineItem)
	require.True(t, ok, "the report has to read the ORDER LINE entity")
	assert.Equal(t, salesPerPage+1, spec.Limit,
		"one record more than the page is read, so 'is there a next page' needs no count")

	orders, ok := catalog.specFor(EntityOrder)
	require.True(t, ok, "the order facts come from a SECOND read")
	assert.Equal(t, map[string]any{filterID: []string{"order_1"}}, orders.Filters,
		"the second read is keyed by the line's order_id, as one batch")
}

// TestSalesReportPagesWithoutCounting covers the paging arithmetic.
//
// The offset is what a wrong page size shows up in: a screen asking for 25 rows
// and skipping 26 loses one sale per page, quietly.
func TestSalesReportPagesWithoutCounting(t *testing.T) {
	t.Parallel()

	catalog := salesCatalog()

	rec := getSalesPage(newCatalogPanel(t, catalog), SalesPath+"?page=2")

	require.Equal(t, http.StatusOK, rec.Code)

	spec, ok := catalog.specFor(EntityOrderLineItem)
	require.True(t, ok)
	assert.Equal(t, salesPerPage+1, spec.Limit)
	assert.Equal(t, salesPerPage, spec.Offset, "the second page skips exactly one page")
}

// TestTheSalesRangeEndsAtTheStartOfTheDayAfter is the off-by-one this screen
// exists to get right.
//
// The operator types an INCLUSIVE last day and the filter is HALF-OPEN, so the
// upper bound has to be the start of the day AFTER the one typed. Passing the
// start of the 31st instead looks identical on screen and silently drops every
// sale placed on the 31st — a whole day of trade, missing from a report nobody
// can tell is short.
func TestTheSalesRangeEndsAtTheStartOfTheDayAfter(t *testing.T) {
	t.Parallel()

	catalog := salesCatalog()

	rec := getSalesPage(newCatalogPanel(t, catalog),
		SalesPath+"?from=2026-08-01&to=2026-08-31")

	require.Equal(t, http.StatusOK, rec.Code)

	spec, ok := catalog.specFor(EntityOrderLineItem)
	require.True(t, ok)

	from, ok := spec.Filters[filterPlacedFrom].(time.Time)
	require.True(t, ok, "the lower bound has to reach the read layer as an instant")
	assert.True(t, from.Equal(time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)),
		"the first day is INCLUDED from its midnight; %s given", from)

	to, ok := spec.Filters[filterPlacedTo].(time.Time)
	require.True(t, ok, "the upper bound has to reach the read layer as an instant")
	assert.True(t, to.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)),
		"the exclusive bound is the START of the day after the last one asked for; %s given", to)

	body := rec.Body.String()
	assert.Contains(t, body, `value="2026-08-01"`, "the form carries the period back")
	assert.Contains(t, body, `value="2026-08-31"`,
		"and the screen shows the INCLUSIVE last day, not the exclusive bound")
	assert.NotContains(t, body, `value="2026-09-01"`,
		"showing the exclusive bound would tell the operator the report covers a day it does not")
}

// TestTheDefaultSalesWindowIsTheLastThirtyDays covers the absent parameters.
//
// The window is computed against a FIXED clock here rather than through the
// handler: the handler reads time.Now, and a test that recomputed the same
// expectation from its own time.Now would be a test that fails once a day, at
// midnight, for no reason anybody could reproduce.
func TestTheDefaultSalesWindowIsTheLastThirtyDays(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 14, 30, 0, 0, time.UTC)

	window := salesWindowOf("", "", now)

	assert.True(t, window.From.Equal(time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)),
		"the window starts at the midnight %d days back, today included; %s given",
		salesWindowDays, window.From)
	assert.True(t, window.To.Equal(time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)),
		"and ends at the start of TOMORROW, so a sale placed an hour ago is in it; %s given",
		window.To)
	assert.Equal(t, "2026-09-05", window.lastDay().Format(dayLayout),
		"the day printed on screen is the last one INCLUDED")
}

// TestASalesRequestWithoutAPeriodStillFiltersByDate proves the default reaches
// the read layer.
//
// Without this the default could be computed and then dropped, and the report
// would silently cover all of history — which reads as a busy month.
func TestASalesRequestWithoutAPeriodStillFiltersByDate(t *testing.T) {
	t.Parallel()

	catalog := salesCatalog()

	rec := getSalesPage(newCatalogPanel(t, catalog), SalesPath)

	require.Equal(t, http.StatusOK, rec.Code)

	spec, ok := catalog.specFor(EntityOrderLineItem)
	require.True(t, ok)

	from, ok := spec.Filters[filterPlacedFrom].(time.Time)
	require.True(t, ok, "an absent period still has to be a period")
	to, ok := spec.Filters[filterPlacedTo].(time.Time)
	require.True(t, ok)

	assert.Equal(t, "00:00:00", from.Format("15:04:05"), "the bounds are midnights")
	assert.Equal(t, "00:00:00", to.Format("15:04:05"))
	// The span is checked by CALENDAR arithmetic rather than by subtracting the
	// two instants: a window crossing a daylight-saving change is not 720 hours
	// long, and a test that said so would fail twice a year in most zones.
	assert.True(t, from.AddDate(0, 0, salesWindowDays).Equal(to),
		"the default window is %d days wide; %s to %s given", salesWindowDays, from, to)
}

// TestAReversedSalesRangeFallsBackRatherThanFailing follows pageNumber's rule.
//
// The provider REFUSES a range whose start is not before its end — deliberately,
// because an empty page would read as "nothing sold". Passed straight through,
// two dates typed the wrong way round in the address bar would therefore become
// "Catalog unavailable", which sends the operator to look at the database for a
// typo they made themselves.
func TestAReversedSalesRangeFallsBackRatherThanFailing(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 5, 14, 30, 0, 0, time.UTC)

	reversed := salesWindowOf("2026-08-31", "2026-08-01", now)

	assert.Equal(t, salesWindowOf("", "", now), reversed,
		"a reversed range falls back to the default window, whole")
	assert.True(t, reversed.From.Before(reversed.To),
		"whatever is passed on, the range that leaves here is a real one")
}

// TestASoldAmountWithAnUnknownScaleIsNotGuessed is the money rule this screen
// shares with the order list.
//
// A currency whose scale could not be read prints as MINOR UNITS and says so.
// Dividing by a guessed hundred would show 108000 JPY as "1080.00" when the
// right answer is "108000", and it would show it confidently.
func TestASoldAmountWithAnUnknownScaleIsNotGuessed(t *testing.T) {
	t.Parallel()

	order := orderRecord()
	order["currency_code"] = "JPY"

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityOrderLineItem: {lineRecord()},
		EntityOrder:         {order},
		// The region module answers with a currency the order does not use, so
		// the order's own scale is genuinely unknown.
		EntityRegion: {currencyRecord("TRY", 2)},
	}}

	rec := getSalesPage(newCatalogPanel(t, catalog), SalesPath)

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "108000 JPY", "an unknown scale prints the raw minor-unit figure")
	assert.Contains(t, body, "(minor)", "and the screen has to SAY it is minor units")
	assert.NotContains(t, body, "1080.00", "a scale must never be guessed")
	assert.NotContains(t, body, "450.00", "the unit price is not guessed either")
}

// TestALineWhoseOrderIsGoneStillAppears is the rule that keeps a report honest.
//
// The order was deleted between the two reads, or its identifier points at a
// record the order provider no longer serves. The line was still SOLD, and a
// row quietly dropped is a sale that vanished from the books — a report that is
// short by an unknown amount is worse than one carrying a row with gaps in it.
func TestALineWhoseOrderIsGoneStillAppears(t *testing.T) {
	t.Parallel()

	orphan := lineRecord()
	orphan["id"] = "line_2"
	orphan["order_id"] = "order_gone"
	orphan["title"] = "Blue Mug"
	orphan["variant_id"] = "variant_2"

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityOrderLineItem: {lineRecord(), orphan},
		// Only the first line's order comes back; the second one's is missing
		// from the answer, which is not an error.
		EntityOrder:  {orderRecord()},
		EntityRegion: {currencyRecord("TRY", 2)},
	}}

	rec := getSalesPage(newCatalogPanel(t, catalog), SalesPath)

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "Blue Mug", "the orphaned line is still a sale and still a row")
	assert.Contains(t, body, "no order", "what it lost is said rather than left blank")
	assert.Contains(t, body, "108000", "with no currency the amount is a raw minor figure")
	assert.Contains(t, body, "(minor)", "and is marked as one")
}

// TestTheSalesSectionIsInTheMenu keeps the route and the menu in step.
func TestTheSalesSectionIsInTheMenu(t *testing.T) {
	t.Parallel()

	body := getSalesPage(newCatalogPanel(t, &fakeCatalog{}), SalesPath).Body.String()

	assert.Contains(t, body, `href="`+SalesPath+`" aria-current="page"`)
}
