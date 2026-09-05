package adminui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/query"
)

// orderRouter mounts the order routes so chi fills the URL parameters.
func orderRouter(panel *UI) chi.Router {
	r := chi.NewRouter()
	r.Get(OrdersPath, panel.listOrders)
	r.Get(OrderPath, panel.showOrder)
	r.Get(StylesheetPath, panel.serveStylesheet)

	return r
}

// getOrderPage sends a GET as a SIGNED-IN operator and returns the recorder.
//
// The identity matters to the frame rather than to the handler: the menu and
// the sign-out control are drawn only for a request that carries a principal,
// so a test that skipped it would exercise the logged-out frame.
func getOrderPage(panel *UI, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	request = request.WithContext(corehttp.WithPrincipal(request.Context(),
		corehttp.Principal{ID: "user_1", Kind: "user"}))

	rec := httptest.NewRecorder()
	orderRouter(panel).ServeHTTP(rec, request)

	return rec
}

// orderRecord is one order as the read layer hands it over.
func orderRecord() query.Record {
	return query.Record{
		"id": "order_1", "display_id": int64(1042), "status": "pending",
		"email": "buyer@example.test", "currency_code": "TRY",
		"subtotal": int64(90_000), "discount_total": int64(0),
		"tax_total": int64(18_000), "shipping_total": int64(0),
		"total":     int64(108_000),
		"placed_at": time.Date(2026, 9, 4, 9, 15, 0, 0, time.UTC),
	}
}

// currencyRecord is the region record the scales are read out of.
//
// The expansion is a plain map[string]any and NOT a query.Record: that is the
// shape the read layer hands an expansion back in, and the panel's reader
// asserts on it. A fixture using the named type would type-assert to nothing
// and the screen would silently fall back to minor units — which is exactly
// what the first version of this fixture did.
func currencyRecord(code string, digits int) query.Record {
	return query.Record{
		"id": "reg_1",
		"currency": map[string]any{
			"code": code, "decimal_digits": digits,
		},
	}
}

// TestOrderListRendersRows proves the list reads through the read layer and
// prints what it got.
func TestOrderListRendersRows(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityOrder:  {orderRecord()},
		EntityRegion: {currencyRecord("TRY", 2)},
	}}

	rec := getOrderPage(newCatalogPanel(t, catalog), OrdersPath)

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "#1042")
	assert.Contains(t, body, "buyer@example.test")
	assert.Contains(t, body, "pending")
	assert.Contains(t, body, "1080.00 TRY", "the total has to be scaled by the currency's digits")

	spec, ok := catalog.specFor(EntityOrder)
	require.True(t, ok, "the list has to read the ORDER entity")
	assert.Equal(t, ordersPerPage+1, spec.Limit,
		"one record more than the page is read, so 'is there a next page' needs no count")
}

// TestAnOrderAmountWithAnUnknownScaleIsNotGuessed is the money rule this screen
// shares with the catalog.
//
// A currency whose scale could not be read is printed as MINOR UNITS and said
// to be. Dividing by a guessed hundred would show 108000 JPY as "1080.00" when
// the right answer is "108000", and it would show it confidently.
func TestAnOrderAmountWithAnUnknownScaleIsNotGuessed(t *testing.T) {
	t.Parallel()

	record := orderRecord()
	record["currency_code"] = "JPY"

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityOrder: {record},
		// The region module answers with a currency the order does not use, so
		// the order's own scale is genuinely unknown.
		EntityRegion: {currencyRecord("TRY", 2)},
	}}

	rec := getOrderPage(newCatalogPanel(t, catalog), OrdersPath)

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "108000 JPY", "an unknown scale prints the raw minor-unit figure")
	assert.Contains(t, body, "(minor)", "and the screen has to SAY it is minor units")
	assert.NotContains(t, body, "1080.00", "a scale must never be guessed")
}

// TestTheOrderPageShowsTheAmountsItWasGiven covers the detail screen.
func TestTheOrderPageShowsTheAmountsItWasGiven(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityOrder:  {orderRecord()},
		EntityRegion: {currencyRecord("TRY", 2)},
	}}

	rec := getOrderPage(newCatalogPanel(t, catalog), OrdersPath+"/order_1")

	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "Order #1042")
	assert.Contains(t, body, "900.00 TRY", "the subtotal")
	assert.Contains(t, body, "180.00 TRY", "the tax")
	assert.Contains(t, body, "1080.00 TRY", "the total")
}

// TestAMissingOrderIsNotFound covers the empty read.
func TestAMissingOrderIsNotFound(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{}}

	rec := getOrderPage(newCatalogPanel(t, catalog), OrdersPath+"/order_missing")

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestThePanelDrawsItsMenu proves the frame the layout renders around a page.
//
// The menu is built from a LIST the Go side supplies, so a section added to the
// panel enters the menu by being added there. The current section is marked
// with aria-current, which is what the stylesheet keys on AND what a screen
// reader announces — one fact instead of two that can drift.
func TestThePanelDrawsItsMenu(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityOrder:  {orderRecord()},
		EntityRegion: {currencyRecord("TRY", 2)},
	}}

	body := getOrderPage(newCatalogPanel(t, catalog), OrdersPath).Body.String()

	assert.Contains(t, body, `href="`+ProductsPath+`"`, "the catalog has to be in the menu")
	assert.Contains(t, body, `href="`+OrdersPath+`" aria-current="page"`,
		"the section the request is in has to be marked")
	assert.Contains(t, body, `href="`+StylesheetPath+`"`, "the frame has to link the stylesheet")
	assert.Contains(t, body, "Sign out", "a signed-in operator has to be able to leave")
}

// TestADetailPageKeepsItsSectionMarked keeps the menu from going blank on every
// detail screen.
func TestADetailPageKeepsItsSectionMarked(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityOrder:  {orderRecord()},
		EntityRegion: {currencyRecord("TRY", 2)},
	}}

	body := getOrderPage(newCatalogPanel(t, catalog), OrdersPath+"/order_1").Body.String()

	assert.Contains(t, body, `href="`+OrdersPath+`" aria-current="page"`,
		"an order's own page is still inside the Orders section")
}

// TestTheStylesheetIsServedWithItsStamp is the first consumer of
// corehttp.WriteAsset.
//
// The capability was built for the panel in ADR 0011 and had never been called.
// What the stamp buys is that an operator's browser refetches the file exactly
// when it changed and not otherwise, so the header and the body have to agree.
func TestTheStylesheetIsServedWithItsStamp(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	orderRouter(newCatalogPanel(t, &fakeCatalog{})).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, StylesheetPath, http.NoBody))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, stylesheetType, rec.Header().Get("Content-Type"))
	assert.Equal(t, stylesheetETag, rec.Header().Get("ETag"))
	assert.Contains(t, rec.Header().Get("Cache-Control"), "immutable")
	assert.NotEmpty(t, rec.Body.Bytes())
	assert.Contains(t, rec.Body.String(), ".masthead", "the body has to be the stylesheet")
}

// TestTheStylesheetOpensWithoutAnIdentity is why it is on the exempt list.
//
// The login page needs it, and a login screen rendering unstyled because its
// stylesheet sat behind the login is a poor first impression of a framework.
// The file carries no data — it is bytes compiled into the binary, identical
// for every installation.
func TestTheStylesheetOpensWithoutAnIdentity(t *testing.T) {
	t.Parallel()

	assert.Contains(t, ExemptPaths(), StylesheetPath)
	assert.Contains(t, ExemptPaths(), LoginPath)
	assert.Len(t, ExemptPaths(), 2, "nothing else in the panel opens without an identity")
}
