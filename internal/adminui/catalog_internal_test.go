package adminui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
)

// fakeCatalog is the read layer's in-memory stand-in.
//
// It answers PER ENTITY, because the catalog screens make more than one call
// and a fake with a single canned answer would let a screen read the wrong
// entity without the test noticing.
type fakeCatalog struct {
	byEntity map[string][]query.Record
	err      error

	specs []query.GraphSpec
}

func (f *fakeCatalog) Graph(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
	f.specs = append(f.specs, spec)
	if f.err != nil {
		return nil, f.err
	}

	return f.byEntity[spec.Entity], nil
}

// specFor returns the recorded spec for an entity, or false.
func (f *fakeCatalog) specFor(entity string) (query.GraphSpec, bool) {
	for _, spec := range f.specs {
		if spec.Entity == entity {
			return spec, true
		}
	}

	return query.GraphSpec{}, false
}

// newCatalogPanel builds a panel wired to the given read layer.
func newCatalogPanel(t *testing.T, catalog Catalog) *UI {
	t.Helper()

	templates, err := loadTemplates()
	require.NoError(t, err)

	return &UI{catalog: catalog, templates: templates}
}

// catalogRouter mounts the catalog routes so chi fills the URL parameters.
//
// The handler reads the product id with chi.URLParam, which is empty unless the
// request went through a router. Calling the handler directly would exercise a
// path no real request takes.
func catalogRouter(panel *UI) chi.Router {
	r := chi.NewRouter()
	r.Get(ProductsPath, panel.listProducts)
	r.Get(ProductPath, panel.showProduct)

	return r
}

// getPage sends a GET and returns the recorder.
func getPage(panel *UI, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	catalogRouter(panel).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))

	return rec
}

// TestProductListRendersRows proves the list reads through the read layer and
// prints what it got.
func TestProductListRendersRows(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityProduct: {
			{"id": "prod_1", "title": "Coffee", "handle": "coffee", "status": "published",
				"updated_at": time.Date(2026, 9, 3, 10, 30, 0, 0, time.UTC)},
		},
	}}

	rec := getPage(newCatalogPanel(t, catalog), ProductsPath)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "Coffee")
	assert.Contains(t, body, "coffee")
	assert.Contains(t, body, "published")
	assert.Contains(t, body, "2026-09-03 10:30")
	assert.Contains(t, body, ProductsPath+"/prod_1", "the title must link to the product page")
}

// TestProductListEscapesOperatorText proves a product title is escaped.
//
// The panel runs inside an ADMINISTRATOR's session and a product title is text
// somebody typed. An XSS here hands the attacker admin privileges, so the
// assertion has the same two halves as the login page's: no raw tag, and no
// ZgotmplZ either — the engine prints that marker instead of failing when it
// cannot resolve a context, which makes escaping LOOK like it worked while the
// data silently disappears.
func TestProductListEscapesOperatorText(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityProduct: {{"id": "p1", "title": `<script>alert('admin')</script>`}},
	}}

	rec := getPage(newCatalogPanel(t, catalog), ProductsPath)
	body := rec.Body.String()

	assert.NotContains(t, body, "<script>", "a title must not be printed as a raw tag")
	assert.Contains(t, body, "&lt;script&gt;", "the title must be escaped, not dropped")
	assert.NotContains(t, body, "ZgotmplZ",
		"the engine failed to resolve a context: escaping LOOKS like it worked but the "+
			"data is silently removed")
}

// TestProductListPagesWithoutCounting proves the "next page" answer comes from
// one extra record rather than from a count.
//
// The screen asks for one record more than it shows. That extra record is the
// whole paging mechanism: without it the answer would need a COUNT over the
// catalog, which is the one table that always grows.
func TestProductListPagesWithoutCounting(t *testing.T) {
	t.Parallel()

	full := make([]query.Record, 0, productsPerPage+1)
	for i := range productsPerPage + 1 {
		full = append(full, query.Record{"id": "p" + string(rune('a'+i%26)), "title": "T"})
	}

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{EntityProduct: full}}
	rec := getPage(newCatalogPanel(t, catalog), ProductsPath+"?page=2")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "page=3", "the next link must be offered")
	assert.Contains(t, rec.Body.String(), "page=1", "the previous link must be offered")

	spec, ok := catalog.specFor(EntityProduct)
	require.True(t, ok)
	assert.Equal(t, productsPerPage+1, spec.Limit,
		"one record more than the page holds must be requested; that extra record IS the "+
			"answer to \"is there a next page\"")
	assert.Equal(t, productsPerPage, spec.Offset, "page 2 must skip one page")
}

// TestProductListRejectsNothingForABadPage proves an unreadable page number
// falls back to page one instead of erroring.
//
// The address bar is edited by hand; answering "?page=abc" with an error page
// would be louder than the mistake, and the fallback is obvious on screen.
func TestProductListRejectsNothingForABadPage(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{EntityProduct: {}}}
	rec := getPage(newCatalogPanel(t, catalog), ProductsPath+"?page=abc")

	require.Equal(t, http.StatusOK, rec.Code)

	spec, ok := catalog.specFor(EntityProduct)
	require.True(t, ok)
	assert.Equal(t, 0, spec.Offset, "an unreadable page must land on the first page")
}

// TestProductPageJoinsPriceAndStock proves one product page shows the variant,
// its price and its stock — the three coming from three different modules.
//
// It also pins the SHAPE of the second call: the price and the stock arrive as
// EXPANSIONS in the same request. A screen that fetched them per variant would
// issue a query per row, which is exactly what the read layer's no-N+1 rule
// exists to prevent, and no assertion on the rendered output would notice.
func TestProductPageJoinsPriceAndStock(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityProduct: {{"id": "prod_1", "title": "Coffee", "handle": "coffee", "status": "published"}},
		EntityVariant: {{
			"id": "var_1", "title": "250g", "sku": "COF-250",
			keyPriceSet: query.Record{"id": "pset_1", "prices": []map[string]any{
				{"id": "price_1", "currency_code": "TRY", "amount": int64(19990)},
			}},
			keyInventory: query.Record{"id": "inv_1", FieldAvailableQuantity: int64(42)},
		}},
		EntityRegion: {{"id": "reg_1", "currency_code": "TRY",
			"currency": map[string]any{"code": "TRY", "decimal_digits": 2}}},
	}}

	rec := getPage(newCatalogPanel(t, catalog), ProductsPath+"/prod_1")

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "250g")
	assert.Contains(t, body, "COF-250")
	assert.Contains(t, body, "199.90 TRY", "the amount must be scaled by the currency's digits")
	assert.NotContains(t, body, "minor units", "a known scale must not be labeled as raw")
	assert.Contains(t, body, "42", "the sellable quantity must be shown")

	spec, ok := catalog.specFor(EntityVariant)
	require.True(t, ok)
	require.Len(t, spec.Expand, 2, "the price and the stock must be expansions of ONE call")
	links := []string{spec.Expand[0].Link, spec.Expand[1].Link}
	assert.ElementsMatch(t, []string{LinkVariantPriceSet, LinkVariantInventory}, links)
	assert.Equal(t, map[string]any{filterProductID: []string{"prod_1"}}, spec.Filters)
}

// TestProductPageShowsMinorUnitsWhenTheScaleIsUnknown proves an amount is never
// guessed.
//
// ISO 4217 has 0-, 2- and 3-digit currencies. With the scale unavailable — the
// region module unregistered, say — a screen assuming 100 would print a wrong
// price CONFIDENTLY. The raw integer with a label is uglier and honest.
func TestProductPageShowsMinorUnitsWhenTheScaleIsUnknown(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityProduct: {{"id": "prod_1", "title": "Coffee"}},
		EntityVariant: {{
			"id": "var_1", "title": "250g",
			keyPriceSet: query.Record{"id": "pset_1", "prices": []map[string]any{
				{"id": "price_1", "currency_code": "TRY", "amount": int64(19990)},
			}},
		}},
		// No region record: the scale cannot be learned.
	}}

	rec := getPage(newCatalogPanel(t, catalog), ProductsPath+"/prod_1")

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "19990 TRY")
	assert.Contains(t, body, "minor units",
		"an amount whose scale is unknown must SAY that it is raw")
	assert.NotContains(t, body, "199.90", "an unknown scale must never be guessed")
}

// TestProductPageTellsNoStockFromNoTracking proves a variant with no inventory
// item shows a dash rather than 0.
//
// Zero means "sold out". A variant nothing tracks is a different fact, and
// printing 0 would tell the operator to restock something the system does not
// count.
func TestProductPageTellsNoStockFromNoTracking(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityProduct: {{"id": "prod_1", "title": "Coffee"}},
		EntityVariant: {{"id": "var_1", "title": "250g"}},
	}}

	rec := getPage(newCatalogPanel(t, catalog), ProductsPath+"/prod_1")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "—", "an untracked variant must show a dash")
	assert.NotContains(t, rec.Body.String(), ">0<", "an untracked variant must not read as sold out")
}

// TestProductPageIsNotFoundForAnUnknownID proves a missing product answers 404.
func TestProductPageIsNotFoundForAnUnknownID(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{EntityProduct: {}}}
	rec := getPage(newCatalogPanel(t, catalog), ProductsPath+"/nope")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotContains(t, rec.Body.String(), "Variants",
		"a product that does not exist must not render a variant table")
}

// TestCatalogFailureSeparatesOurBugFromAnOutage proves the two failures get
// different status codes.
//
// An invalid spec is THIS package's bug: a field, filter or link name it spells
// no longer matches the provider. Reporting it as "temporarily unavailable"
// would send the operator to look at the database while the fix is in the
// panel.
func TestCatalogFailureSeparatesOurBugFromAnOutage(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  error
		want int
	}{
		"the read layer is down": {
			err:  errors.Unavailable("db_down", "the database is unreachable"),
			want: http.StatusServiceUnavailable,
		},
		"the spec is invalid": {
			err:  errors.Invalid("query_invalid_field", "no such field"),
			want: http.StatusInternalServerError,
		},
		"a provider is missing": {
			err:  errors.NotFound("query_provider_missing", "product.query was not found"),
			want: http.StatusServiceUnavailable,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := getPage(newCatalogPanel(t, &fakeCatalog{err: tt.err}), ProductsPath)

			assert.Equal(t, tt.want, rec.Code)
			assert.NotContains(t, rec.Body.String(), "unreachable",
				"the underlying error must not reach the page; it can carry a provider "+
					"name, a query or a database address")
		})
	}
}

// TestFormatAmountNeverGuessesTheScale is the money formatter's table.
//
// The arithmetic stays in integers throughout: dividing by a power of ten in
// floating point is exactly the operation plan Section 8 forbids for money,
// because at large amounts the result is no longer the number that was stored.
func TestFormatAmountNeverGuessesTheScale(t *testing.T) {
	t.Parallel()

	scales := map[string]int{"TRY": 2, "JPY": 0, "KWD": 3}

	tests := map[string]struct {
		minor int64
		code  string
		want  string
		exact bool
	}{
		"two digits":            {19990, "TRY", "199.90", true},
		"zero digits":           {1000, "JPY", "1000", true},
		"three digits":          {1000, "KWD", "1.000", true},
		"smaller than the unit": {5, "TRY", "0.05", true},
		"exactly one unit":      {100, "TRY", "1.00", true},
		"zero":                  {0, "TRY", "0.00", true},
		"negative":              {-19990, "TRY", "-199.90", true},
		"unknown currency":      {19990, "XXX", "19990", false},
		"large amount":          {922337203685477, "TRY", "9223372036854.77", true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, exact := formatAmount(tt.minor, tt.code, scales)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.exact, exact)
		})
	}
}

// TestIntValueRejectsFloats proves a float amount is refused rather than
// printed.
//
// The read layer runs in-process and hands the provider's own values through,
// so an amount arrives as an integer. A float64 would mean the value had passed
// through a JSON round trip and lost precision; rendering it would print a
// wrong price confidently.
func TestIntValueRejectsFloats(t *testing.T) {
	t.Parallel()

	_, ok := intValue(float64(19990))
	assert.False(t, ok, "a float amount must not be accepted as money")

	_, ok = intValue("19990")
	assert.False(t, ok, "a string amount must not be accepted as money")

	got, ok := intValue(int64(19990))
	assert.True(t, ok)
	assert.Equal(t, 19990, got)
}

// TestCurrencyScalesDegradeWithoutRegions proves an unreadable region list
// leaves the scale map empty rather than defaulting.
func TestCurrencyScalesDegradeWithoutRegions(t *testing.T) {
	t.Parallel()

	panel := newCatalogPanel(t, &fakeCatalog{err: errors.NotFound("no_region", "region.query is missing")})

	scales := panel.currencyScales(context.Background())

	assert.Empty(t, scales, "an unreadable region list must not produce a guessed scale")
}

// TestCatalogSpellsTheNamesTheProvidersUse pins the specs the screens build.
//
// The names are compared against the modules' own constants at compile time in
// internal/arch; what is pinned HERE is that the screen actually PUTS them into
// the spec. A screen that built the right names into the wrong field would pass
// the arch check and still read nothing.
func TestCatalogSpellsTheNamesTheProvidersUse(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityProduct: {{"id": "prod_1", "title": "Coffee"}},
	}}

	getPage(newCatalogPanel(t, catalog), ProductsPath+"/prod_1")

	products, ok := catalog.specFor(EntityProduct)
	require.True(t, ok, "the product entity must be read")
	assert.Equal(t, map[string]any{filterID: []string{"prod_1"}}, products.Filters)

	variants, ok := catalog.specFor(EntityVariant)
	require.True(t, ok, "the variant entity must be read")
	assert.Contains(t, variants.Fields, "sku")

	regions, ok := catalog.specFor(EntityRegion)
	require.True(t, ok, "the region entity must be read for the currency scales")
	assert.Contains(t, regions.Fields, "currency")
	assert.True(t, strings.HasPrefix(EntityRegion, "region"))
}
