package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// fakePriceWriter records what reached the pricing module's admin surface.
type fakePriceWriter struct {
	calls    int
	setID    string
	currency string
	amount   int64
	err      error
}

func (f *fakePriceWriter) SetBasePriceAmount(_ context.Context, setID, currency string, amount int64) error {
	f.calls++
	f.setID, f.currency, f.amount = setID, currency, amount

	return f.err
}

// fakeStockAdmin stands in for the inventory module's admin surface.
type fakeStockAdmin struct {
	levels json.RawMessage
	readIn error

	calls      int
	itemID     string
	locationID string
	quantity   int64
	writeErr   error
}

func (f *fakeStockAdmin) StockLevelsJSON(_ context.Context, itemID string) (json.RawMessage, error) {
	if f.readIn != nil {
		return nil, f.readIn
	}

	return f.levels, nil
}

func (f *fakeStockAdmin) SetStockLevel(_ context.Context, itemID, locationID string, quantity int64) error {
	f.calls++
	f.itemID, f.locationID, f.quantity = itemID, locationID, quantity

	return f.writeErr
}

// newVariantPanel builds a panel wired to the two write surfaces.
//
// Either may be nil: the panel resolves both OPTIONALLY (ADR 0013, decision 4)
// and the tests below exercise the absent case as well as the present one.
func newVariantPanel(t *testing.T, catalog Catalog, prices PriceWriter, stock StockAdmin) *UI {
	t.Helper()

	templates, err := loadTemplates()
	require.NoError(t, err)

	return &UI{catalog: catalog, templates: templates, prices: prices, stock: stock}
}

// variantRouter mounts the variant routes so chi fills the URL parameters.
func variantRouter(panel *UI) chi.Router {
	r := chi.NewRouter()
	r.Get(VariantPath, panel.showVariant)
	r.Post(VariantPricePath, panel.submitVariantPrice)
	r.Post(VariantStockPath, panel.submitVariantStock)

	return r
}

// variantCatalog is a read layer holding one variant with a price and an item.
func variantCatalog(digits any) *fakeCatalog {
	regions := []query.Record{}
	if digits != nil {
		regions = append(regions, query.Record{"id": "reg_1", "currency_code": "TRY",
			"currency": map[string]any{"code": "TRY", "decimal_digits": digits}})
	}

	return &fakeCatalog{byEntity: map[string][]query.Record{
		EntityVariant: {{
			"id": "var_1", "title": "250g", "sku": "COF-250",
			keyPriceSet: query.Record{"id": "pset_1", "prices": []map[string]any{
				{"id": "price_1", "currency_code": "TRY", "amount": int64(19990)},
			}},
			keyInventory: query.Record{"id": "inv_1", FieldAvailableQuantity: int64(42)},
		}},
		EntityRegion: regions,
	}}
}

// stockJSON is what the inventory module's admin surface returns.
const stockJSON = `[{"location_id":"sloc_1","location_name":"Main warehouse",` +
	`"stocked_quantity":10,"reserved_quantity":4,"available_quantity":6}]`

// variantURLFor is the path the tests request.
func variantURLFor() string { return ProductsPath + "/prod_1/variants/var_1" }

// getVariant loads the variant page.
func getVariant(panel *UI) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	variantRouter(panel).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, variantURLFor(), http.NoBody))

	return rec
}

// postForm submits one of the variant's two forms.
func postForm(panel *UI, suffix string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, variantURLFor()+suffix,
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	variantRouter(panel).ServeHTTP(rec, req)

	return rec
}

// TestVariantPageOffersThePriceAndTheStockForEditing proves the page carries
// both forms filled with what is stored.
func TestVariantPageOffersThePriceAndTheStockForEditing(t *testing.T) {
	t.Parallel()

	panel := newVariantPanel(t, variantCatalog(2), &fakePriceWriter{},
		&fakeStockAdmin{levels: json.RawMessage(stockJSON)})

	rec := getVariant(panel)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "250g")
	assert.Contains(t, body, "COF-250")
	assert.Contains(t, body, `value="199.90"`, "the price box opens with the scaled amount")
	assert.Contains(t, body, `value="pset_1"`, "the write needs the price set's identity")
	assert.Contains(t, body, "Main warehouse")
	assert.Contains(t, body, `value="10"`, "the stock box opens with the PHYSICAL count")
	assert.Contains(t, body, ">4<", "the reserved quantity explains a later refusal")
	assert.NotContains(t, body, "minor units", "a known scale must not be labeled as raw")
}

// TestVariantPageSaysWhenAnAmountIsInMinorUnits proves the operator is told
// which box they are typing into.
//
// Without the label an operator who types "199.90" into a raw box stores a
// hundredth of what they meant, and nothing about the page would have warned
// them.
func TestVariantPageSaysWhenAnAmountIsInMinorUnits(t *testing.T) {
	t.Parallel()

	panel := newVariantPanel(t, variantCatalog(nil), &fakePriceWriter{}, nil)

	rec := getVariant(panel)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "minor units")
	assert.Contains(t, body, `value="19990"`, "the box holds the raw integer")
	assert.Contains(t, body, `name="minor" value="1"`,
		"the form must tell the handler which mode it was filled in")
}

// TestSavingAPriceScalesWithTheCurrencyNotWithAGuess proves what the operator
// typed reaches the module as minor units.
func TestSavingAPriceScalesWithTheCurrencyNotWithAGuess(t *testing.T) {
	t.Parallel()

	writer := &fakePriceWriter{}
	panel := newVariantPanel(t, variantCatalog(2), writer, nil)

	rec := postForm(panel, "/price", url.Values{
		"price_set_id": {"pset_1"}, "currency": {"try"}, "minor": {"0"}, "amount": {"249.50"},
	})

	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, variantURLFor(), rec.Header().Get("Location"),
		"a refresh must not repeat the write")
	require.Equal(t, 1, writer.calls)
	assert.Equal(t, "pset_1", writer.setID)
	assert.Equal(t, "TRY", writer.currency, "the code is normalized before it leaves the panel")
	assert.Equal(t, int64(24950), writer.amount)
}

// TestSavingAPriceInMinorUnitsPassesTheIntegerThrough proves the raw box is not
// scaled a second time.
func TestSavingAPriceInMinorUnitsPassesTheIntegerThrough(t *testing.T) {
	t.Parallel()

	writer := &fakePriceWriter{}
	panel := newVariantPanel(t, variantCatalog(nil), writer, nil)

	rec := postForm(panel, "/price", url.Values{
		"price_set_id": {"pset_1"}, "currency": {"TRY"}, "minor": {"1"}, "amount": {"24950"},
	})

	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, int64(24950), writer.amount)
}

// TestAnUnreadableAmountComesBackOnThePage proves a bad amount is refused
// rather than rounded, and that the page says so.
func TestAnUnreadableAmountComesBackOnThePage(t *testing.T) {
	t.Parallel()

	writer := &fakePriceWriter{}
	panel := newVariantPanel(t, variantCatalog(2), writer, nil)

	rec := postForm(panel, "/price", url.Values{
		"price_set_id": {"pset_1"}, "currency": {"TRY"}, "minor": {"0"}, "amount": {"12.345"},
	})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Zero(t, writer.calls, "an amount that could not be read must not be written")
	assert.Contains(t, rec.Body.String(), "decimal digits")
}

// TestARejectedPriceComesBackWithTheModulesMessage proves a refusal the
// operator can act on is shown to them.
func TestARejectedPriceComesBackWithTheModulesMessage(t *testing.T) {
	t.Parallel()

	writer := &fakePriceWriter{
		err: errors.Invalid("pricing_price_not_found", "that price set has no base price in TRY"),
	}
	panel := newVariantPanel(t, variantCatalog(2), writer, nil)

	rec := postForm(panel, "/price", url.Values{
		"price_set_id": {"pset_1"}, "currency": {"TRY"}, "minor": {"0"}, "amount": {"1.00"},
	})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "has no base price in TRY")
}

// TestAnUnexpectedPriceFailureIsNotShownToTheOperator proves an internal
// failure becomes a page, not a leak.
//
// The message of an unclassified error is written for us, not for them: it can
// carry a host, a port or a query. The operator gets a sentence; the cause goes
// to the log.
func TestAnUnexpectedPriceFailureIsNotShownToTheOperator(t *testing.T) {
	t.Parallel()

	writer := &fakePriceWriter{err: errors.New("dial tcp 10.0.0.5:5432: connect: refused")}
	panel := newVariantPanel(t, variantCatalog(2), writer, nil)

	rec := postForm(panel, "/price", url.Values{
		"price_set_id": {"pset_1"}, "currency": {"TRY"}, "minor": {"0"}, "amount": {"1.00"},
	})

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	body := rec.Body.String()
	assert.NotContains(t, body, "10.0.0.5")
	assert.NotContains(t, body, "{", "the panel answers a browser in HTML, never a JSON envelope")
}

// TestSavingStockReachesTheInventorySurface proves the stock form's values
// arrive unchanged.
func TestSavingStockReachesTheInventorySurface(t *testing.T) {
	t.Parallel()

	stock := &fakeStockAdmin{levels: json.RawMessage(stockJSON)}
	panel := newVariantPanel(t, variantCatalog(2), nil, stock)

	rec := postForm(panel, "/stock", url.Values{
		"inventory_item_id": {"inv_1"}, "location_id": {"sloc_1"}, "quantity": {"25"},
	})

	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, 1, stock.calls)
	assert.Equal(t, "inv_1", stock.itemID)
	assert.Equal(t, "sloc_1", stock.locationID)
	assert.Equal(t, int64(25), stock.quantity)
}

// TestAStockCountIsAWholeNumber proves a decimal is refused rather than
// truncated.
//
// A count of things has no fractional part; accepting "2.5" and storing 2 would
// silently disagree with the number the operator wrote down.
func TestAStockCountIsAWholeNumber(t *testing.T) {
	t.Parallel()

	stock := &fakeStockAdmin{levels: json.RawMessage(stockJSON)}
	panel := newVariantPanel(t, variantCatalog(2), nil, stock)

	rec := postForm(panel, "/stock", url.Values{
		"inventory_item_id": {"inv_1"}, "location_id": {"sloc_1"}, "quantity": {"2.5"},
	})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Zero(t, stock.calls)
	assert.Contains(t, rec.Body.String(), "whole number")
}

// TestPromisedStockRefusalReachesTheOperator proves the module's Conflict is
// what the page shows.
func TestPromisedStockRefusalReachesTheOperator(t *testing.T) {
	t.Parallel()

	stock := &fakeStockAdmin{
		levels:   json.RawMessage(stockJSON),
		writeErr: errors.Conflict("inventory_reserved", "4 units are reserved and cannot be removed"),
	}
	panel := newVariantPanel(t, variantCatalog(2), nil, stock)

	rec := postForm(panel, "/stock", url.Values{
		"inventory_item_id": {"inv_1"}, "location_id": {"sloc_1"}, "quantity": {"1"},
	})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "4 units are reserved")
}

// TestAnUnreadableStockSectionDoesNotCloseThePage proves half the page working
// beats none of it.
func TestAnUnreadableStockSectionDoesNotCloseThePage(t *testing.T) {
	t.Parallel()

	panel := newVariantPanel(t, variantCatalog(2), &fakePriceWriter{},
		&fakeStockAdmin{readIn: errors.Unavailable("inventory_down", "the database is unreachable")})

	rec := getVariant(panel)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "199.90", "the half that worked must still be shown")
	assert.Contains(t, body, "No stock locations were read")
}

// TestAChangedStockSchemaDoesNotPrintZeros proves a schema drift is caught
// rather than rendered.
//
// The panel and the inventory module are bound by JSON, not by the compiler. A
// renamed field would decode into a zero and the page would tell the operator
// the warehouse is empty — the one failure mode that looks like data.
func TestAChangedStockSchemaDoesNotPrintZeros(t *testing.T) {
	t.Parallel()

	panel := newVariantPanel(t, variantCatalog(2), nil,
		&fakeStockAdmin{levels: json.RawMessage(`{"location_id":"sloc_1"}`)})

	rec := getVariant(panel)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "No stock locations were read",
		"an undecodable answer is an absence, never a zero")
}

// TestEditingWithoutTheModuleSaysSo proves an unregistered surface is a 503 and
// not a panic.
func TestEditingWithoutTheModuleSaysSo(t *testing.T) {
	t.Parallel()

	panel := newVariantPanel(t, variantCatalog(2), nil, nil)

	price := postForm(panel, "/price", url.Values{
		"price_set_id": {"pset_1"}, "currency": {"TRY"}, "amount": {"1.00"},
	})
	stock := postForm(panel, "/stock", url.Values{
		"inventory_item_id": {"inv_1"}, "location_id": {"sloc_1"}, "quantity": {"1"},
	})

	assert.Equal(t, http.StatusServiceUnavailable, price.Code)
	assert.Equal(t, http.StatusServiceUnavailable, stock.Code)
}

// TestVariantPageEscapesOperatorText proves a variant title cannot carry a
// script into an administrator's session.
func TestVariantPageEscapesOperatorText(t *testing.T) {
	t.Parallel()

	catalog := variantCatalog(2)
	catalog.byEntity[EntityVariant][0]["title"] = `<script>alert(1)</script>`

	rec := getVariant(newVariantPanel(t, catalog, nil, nil))

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.NotContains(t, body, "<script>alert(1)</script>")
	assert.Contains(t, body, "&lt;script&gt;")
}

// TestAnUnknownVariantIsNotFound proves the page does not render an empty
// shell for an id that does not exist.
func TestAnUnknownVariantIsNotFound(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{}}

	rec := getVariant(newVariantPanel(t, catalog, nil, nil))

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestParseAmountIsIntegerArithmeticEndToEnd is the money table.
//
// Every row exists because getting it wrong changes a price the operator wrote
// down: padding rather than scaling ("1.5" is 150, not 15), refusing an extra
// digit rather than rounding it away, and never letting the text through a
// float on the way (plan Section 8).
func TestParseAmountIsIntegerArithmeticEndToEnd(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		text   string
		digits int
		minor  bool
		want   int64
		fails  bool
	}{
		{name: "two digits", text: "199.90", digits: 2, want: 19990},
		{name: "a short fraction is padded", text: "1.5", digits: 2, want: 150},
		{name: "no fraction at all", text: "200", digits: 2, want: 20000},
		{name: "a leading point", text: ".05", digits: 2, want: 5},
		{name: "surrounding space", text: "  12.34  ", digits: 2, want: 1234},
		{name: "a zero-digit currency", text: "1200", digits: 0, want: 1200},
		{name: "a three-digit currency", text: "1.234", digits: 3, want: 1234},
		{name: "a negative amount", text: "-5.00", digits: 2, want: -500},
		{name: "minor units pass through", text: "19990", digits: 2, minor: true, want: 19990},
		{name: "an unknown scale is raw", text: "19990", digits: 0, want: 19990},
		{
			name: "an amount larger than a float can hold exactly",
			text: "92233720368547.75", digits: 2, want: 9223372036854775,
		},
		{name: "too many digits is refused, not rounded", text: "1.005", digits: 2, fails: true},
		{name: "two decimal points", text: "1.0.0", digits: 2, fails: true},
		{name: "letters", text: "abc", digits: 2, fails: true},
		{name: "empty", text: "   ", digits: 2, fails: true},
		{name: "only a point", text: ".", digits: 2, fails: true},
		{name: "a decimal in a minor box", text: "199.90", digits: 2, minor: true, fails: true},
		{name: "past the int64 ceiling", text: "9223372036854775808", digits: 0, fails: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseAmount(tc.text, tc.digits, tc.minor)
			if tc.fails {
				require.Error(t, err)
				assert.Equal(t, errors.KindInvalid, errors.KindOf(err),
					"an unreadable amount is the operator's to fix, not a server error")

				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
