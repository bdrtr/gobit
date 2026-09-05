package adminui

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/query"
)

// The entity and link names the catalog screens spell BY HAND.
//
// The panel imports no module (ADR 0011), so it reaches the read layer the same
// way every other consumer does: by name (ADR 0004). A hand-repeated name is a
// silent-divergence risk of the worst kind — rename a link and the screen still
// compiles, still answers 200 and simply shows no price — so these are
// EXPORTED and pinned against the modules' own constants at COMPILE time by
// [TestThePanelCatalogNamesAgree] in internal/arch, the only package allowed to
// import both sides.
//
// # What is NOT pinned
//
// The filter and field names below are unexported in the modules that own them,
// so no compile-time comparison is possible for them. They are covered only by
// the read layer's own answer: an unknown filter or field is rejected with
// errors.Invalid (ADR 0004), which [UI.catalogFailure] turns into a 500 saying
// the screen asked for something the provider does not offer. That is a
// LOUD failure rather than a silent one, but it arrives on the first request
// instead of at compile time.
const (
	// EntityProduct is the product provider's entity name.
	EntityProduct = "product"
	// EntityVariant is the variant provider's entity name.
	EntityVariant = "variant"
	// EntityRegion is the region provider's entity name; the catalog reads it
	// only to learn each currency's decimal digits.
	EntityRegion = "region"

	// LinkVariantPriceSet joins a variant to its price set.
	LinkVariantPriceSet = "product_variant_price_set"
	// LinkVariantInventory joins a variant to its inventory item.
	LinkVariantInventory = "product_variant_inventory"

	// FieldAvailableQuantity is the inventory item's sellable total.
	FieldAvailableQuantity = "available_quantity"
)

// The filter names the catalog passes to the providers; see the note above.
const (
	// filterID selects records by identity.
	filterID = "id"
	// filterProductID selects variants belonging to a product.
	filterProductID = "product_id"
)

// The record fields the catalog reads.
const (
	fieldID          = "id"
	fieldTitle       = "title"
	fieldHandle      = "handle"
	fieldStatus      = "status"
	fieldThumbnail   = "thumbnail"
	fieldUpdatedAt   = "updated_at"
	fieldSKU         = "sku"
	fieldPrices      = "prices"
	fieldAmount      = "amount"
	fieldCurrency    = "currency"
	fieldCurrencyCod = "currency_code"
	fieldDecimals    = "decimal_digits"
	fieldAvailable   = FieldAvailableQuantity
)

// The keys the expansions are written under. They are the panel's own choice
// and are never seen outside this package.
const (
	keyPriceSet  = "price_set"
	keyInventory = "inventory"
)

// catalogLabel is what the section is called on screen.
//
// It is a constant for the reason [ordersLabel] is: the menu and the screens
// print the same word, and three copies are three places to rename it in.
const catalogLabel = "Catalog"

// productsPerPage is the product list's page size.
//
// The list is paged rather than unbounded because the read layer's limit is a
// number the CALLER writes: asking for everything makes the panel's first page
// cost grow with the catalog, and a catalog is the one table that always grows.
const productsPerPage = 25

// Error codes the catalog can produce.
const (
	// CodeCatalogUnavailable reports that the read layer could not answer.
	CodeCatalogUnavailable = "adminui_catalog_unavailable"
	// CodeProductNotFound reports that the requested product does not exist.
	CodeProductNotFound = "adminui_product_not_found"
)

// productRow is one line of the product list.
type productRow struct {
	ID        string
	Title     string
	Handle    string
	Status    string
	Thumbnail string
	UpdatedAt time.Time
}

// priceView is one price of a variant, ready to print.
type priceView struct {
	// Amount is the formatted amount, or the raw minor-unit integer when the
	// currency's scale is unknown.
	Amount string
	// Currency is the ISO 4217 code.
	Currency string
	// Minor reports that Amount is a RAW minor-unit integer rather than a
	// scaled amount. The template says so next to the number; see
	// [formatAmount] for why an unknown scale is never guessed.
	Minor bool
}

// variantRow is one line of the variant table on the product page.
type variantRow struct {
	ID     string
	Title  string
	SKU    string
	Prices []priceView
	// Stock is the sellable quantity across all locations, or an empty string
	// when the variant has no inventory item.
	Stock string
}

// listProducts renders the product list.
//
// It reads through the cross-module read layer and NOT through a module's
// service (ADR 0011): the panel knows no module, and the same Graph call is the
// one the storefront listing uses, so the screen cannot drift from what the
// framework actually serves.
func (u *UI) listProducts(w http.ResponseWriter, r *http.Request) {
	page := pageNumber(r.URL.Query().Get("page"))

	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity: EntityProduct,
		Fields: []string{fieldID, fieldTitle, fieldHandle, fieldStatus, fieldThumbnail, fieldUpdatedAt},
		Limit:  productsPerPage + 1,
		Offset: (page - 1) * productsPerPage,
	})
	if err != nil {
		u.catalogFailure(w, r, err, "The product list could not be read.")
		return
	}

	// One extra record is requested so "is there a next page" is answered
	// without a second, counting query — a count over a growing catalog is the
	// expensive half of pagination and this screen does not need the total.
	hasNext := len(records) > productsPerPage
	if hasNext {
		records = records[:productsPerPage]
	}

	rows := make([]productRow, 0, len(records))
	for _, rec := range records {
		rows = append(rows, productRowOf(rec))
	}

	u.templates.render(w, r, http.StatusOK, "products.gohtml", map[string]any{
		titleKey:   "Products",
		"Products": rows,
		"Page":     page,
		"HasNext":  hasNext,
		"HasPrev":  page > 1,
		"NextPage": page + 1,
		"PrevPage": page - 1,
		"Path":     ProductsPath,
	})
}

// showProduct renders one product together with its variants, prices and stock.
//
// Two Graph calls are made, not one, and that is a property of the data rather
// than a shortcut: variants live in the SAME module as products, so there is no
// link between them and the read layer joins only across links. Asking for
// variants is therefore a root query of its own, filtered by product_id.
//
// The prices and the stock DO come through links, in the same call: one
// expansion each, both batched by the read layer. A screen that fetched them
// per variant would issue a query per row, which is exactly what the read
// layer's no-N+1 rule exists to prevent.
func (u *UI) showProduct(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if strings.TrimSpace(id) == "" {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "No product was named.")
		return
	}

	products, err := u.catalog.Graph(r.Context(), productByID(id))
	if err != nil {
		u.catalogFailure(w, r, err, "The product could not be read.")
		return
	}
	if len(products) == 0 {
		u.errorPage(w, r, http.StatusNotFound, "Not found",
			"There is no product with that id.")
		return
	}

	variants, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity:  EntityVariant,
		Fields:  []string{fieldID, fieldTitle, fieldSKU},
		Filters: map[string]any{filterProductID: []string{id}},
		Expand: []query.Expansion{
			{Link: LinkVariantPriceSet, As: keyPriceSet, Fields: []string{fieldID, fieldPrices}},
			{Link: LinkVariantInventory, As: keyInventory, Fields: []string{fieldID, fieldAvailable}},
		},
	})
	if err != nil {
		u.catalogFailure(w, r, err, "The variants could not be read.")
		return
	}

	scales := u.currencyScales(r.Context())
	rows := make([]variantRow, 0, len(variants))
	for _, rec := range variants {
		rows = append(rows, variantRow{
			ID:     recordString(rec, fieldID),
			Title:  recordString(rec, fieldTitle),
			SKU:    recordString(rec, fieldSKU),
			Prices: pricesOf(rec, scales),
			Stock:  stockOf(rec),
		})
	}

	product := productRowOf(products[0])
	u.templates.render(w, r, http.StatusOK, "product.gohtml", map[string]any{
		titleKey:       product.Title,
		"Product":      product,
		"Variants":     rows,
		"ProductsPath": ProductsPath,
		"EditPath":     ProductsPath + "/" + product.ID + "/edit",
	})
}

// unexpectedFailure renders the panel's own error page for a failure the
// operator cannot act on, and logs the real cause.
//
// # Why not corehttp.WriteError
//
// That writer produces the framework's JSON envelope, which is right for an API
// endpoint and wrong here: the panel's client is a BROWSER that navigated to
// this path, and answering it with JSON makes the failure unreadable to the one
// person who could do something about it. The split is the same one
// error.gohtml describes — the envelope stands beside the panel's page, it is
// not replaced by it.
//
// The underlying message is NOT shown. The framework treats a non-Internal
// message as client-safe because a service author wrote it, but that promise was
// made about API clients; a panel page is read by an operator who cannot tell a
// leaked connection string from a diagnosis. The real error goes to the log.
func (u *UI) unexpectedFailure(w http.ResponseWriter, r *http.Request, err error, title string) {
	corehttp.LoggerFromContext(r.Context()).ErrorContext(r.Context(),
		"the panel could not complete the request",
		"error", err, "path", r.URL.Path)

	u.errorPage(w, r, corehttp.StatusFor(err), title,
		"The request could not be completed. The reason is in the server log.")
}

// catalogFailure turns a read-layer failure into a page the operator can act
// on.
//
// The underlying error is NOT shown: it can carry a provider name, a query or a
// database address. What is shown is what the operator can do about it, and the
// real error goes to the log through the core's error path — the same split
// [corehttp.WriteError] makes for the API.
func (u *UI) catalogFailure(w http.ResponseWriter, r *http.Request, err error, message string) {
	corehttp.LoggerFromContext(r.Context()).ErrorContext(r.Context(),
		"the panel could not read the catalog",
		"error", err, "path", r.URL.Path)

	status := http.StatusServiceUnavailable
	hint := message + " The read layer did not answer; a module may not be registered."
	if errors.IsInvalid(err) {
		// An invalid spec is OUR bug, not an outage: the field, filter or link
		// name this package spells no longer matches the provider. Saying
		// "temporarily unavailable" would send the operator to look at the
		// database while the fix is in this file.
		status = http.StatusInternalServerError
		hint = message + " The screen asked the read layer for something it does not offer."
	}

	u.errorPage(w, r, status, "Catalog unavailable", hint)
}

// currencyScales maps a currency code to its number of decimal digits.
//
// # Why the panel reads regions to print a price
//
// An amount is an INTEGER in minor units. Turning it into something a human
// reads needs the currency's scale, and that scale is NOT two for every
// currency: ISO 4217 has 0-digit currencies (JPY, KRW), 2-digit ones (most) and
// 3-digit ones (KWD, BHD). A presentation layer that assumed 100 would show the
// wrong amount for two of the three classes — and show it confidently.
//
// The scale lives in the region module's currency table and reaches the panel
// through the same read layer as everything else. When it cannot be read the
// map comes back EMPTY rather than defaulting to two: [formatAmount] then
// prints the raw minor-unit integer and says so. A missing region module
// degrades the display, it does not corrupt it.
func (u *UI) currencyScales(ctx context.Context) map[string]int {
	scales := map[string]int{}

	regions, err := u.catalog.Graph(ctx, query.GraphSpec{
		Entity: EntityRegion,
		Fields: []string{fieldID, fieldCurrencyCod, fieldCurrency},
	})
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx,
			"the panel could not read the currency scales; amounts will be shown in minor units",
			"error", err)

		return scales
	}

	for _, rec := range regions {
		currency, ok := rec[fieldCurrency].(map[string]any)
		if !ok {
			continue
		}
		code := stringValue(currency[fieldCurrencyCod])
		if code == "" {
			code = stringValue(currency["code"])
		}
		digits, ok := intValue(currency[fieldDecimals])
		if code == "" || !ok {
			continue
		}
		scales[strings.ToUpper(code)] = digits
	}

	return scales
}

// productByID is the spec that reads one product.
//
// The list, the detail page and the edit form share it so the three screens
// cannot start reading the same product differently.
func productByID(id string) query.GraphSpec {
	return query.GraphSpec{
		Entity:  EntityProduct,
		Filters: map[string]any{filterID: []string{id}},
		Limit:   1,
	}
}

// productRowOf turns a product record into a row.
//
// The list and the detail page share it so the two screens cannot start reading
// the same product differently — a field renamed in one and not the other would
// show a title on one screen and a blank on the other.
func productRowOf(rec query.Record) productRow {
	return productRow{
		ID:        recordString(rec, fieldID),
		Title:     recordString(rec, fieldTitle),
		Handle:    recordString(rec, fieldHandle),
		Status:    recordString(rec, fieldStatus),
		Thumbnail: recordString(rec, fieldThumbnail),
		UpdatedAt: recordTime(rec, fieldUpdatedAt),
	}
}

// pricesOf reads a variant's prices out of the price-set expansion.
func pricesOf(rec query.Record, scales map[string]int) []priceView {
	set, ok := rec[keyPriceSet].(query.Record)
	if !ok {
		return nil
	}

	raw, ok := set[fieldPrices].([]map[string]any)
	if !ok {
		return nil
	}

	out := make([]priceView, 0, len(raw))
	for _, price := range raw {
		amount, ok := intValue(price[fieldAmount])
		if !ok {
			continue
		}
		code := strings.ToUpper(stringValue(price[fieldCurrencyCod]))
		text, exact := formatAmount(int64(amount), code, scales)
		out = append(out, priceView{Amount: text, Currency: code, Minor: !exact})
	}

	return out
}

// stockOf reads the sellable quantity out of the inventory expansion.
//
// An empty string means "this variant has no inventory item", which is NOT the
// same as zero stock: printing 0 would tell the operator the product is sold
// out when nothing tracks it at all.
func stockOf(rec query.Record) string {
	item, ok := rec[keyInventory].(query.Record)
	if !ok {
		return ""
	}
	quantity, ok := intValue(item[fieldAvailable])
	if !ok {
		return ""
	}

	return strconv.FormatInt(int64(quantity), 10)
}

// formatAmount turns a minor-unit integer into a readable amount.
//
// The second result reports whether the currency's scale was KNOWN. When it was
// not, the raw integer is returned unchanged and the caller marks it as minor
// units; guessing two digits would print 1000 JPY as "10.00" and 1000 KWD as
// "10.00" while the right answers are "1000" and "1.000".
//
// The arithmetic stays in integers throughout. Dividing by a power of ten in
// floating point is exactly the operation plan Section 8 forbids for money: at
// large amounts the result is no longer the number that was stored.
func formatAmount(minor int64, code string, scales map[string]int) (string, bool) {
	digits, ok := scales[code]
	if !ok || digits < 0 {
		return strconv.FormatInt(minor, 10), false
	}
	if digits == 0 {
		return strconv.FormatInt(minor, 10), true
	}

	negative := minor < 0
	if negative {
		minor = -minor
	}

	text := strconv.FormatInt(minor, 10)
	if len(text) <= digits {
		text = strings.Repeat("0", digits-len(text)+1) + text
	}

	split := len(text) - digits
	out := text[:split] + "." + text[split:]
	if negative {
		out = "-" + out
	}

	return out, true
}

// pageNumber reads the page parameter; anything unreadable is page one.
//
// A bad page number is not an error the operator needs to see: the address bar
// is edited by hand and "?page=abc" answering with an error page would be
// louder than the mistake. Falling back to the first page is both harmless and
// obvious on screen.
func pageNumber(raw string) int {
	page, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || page < 1 {
		return 1
	}

	return page
}

// recordString reads a string field, or "" when it is absent or not a string.
func recordString(rec query.Record, field string) string {
	return stringValue(rec[field])
}

// recordTime reads a time field, or the zero time.
func recordTime(rec query.Record, field string) time.Time {
	t, _ := rec[field].(time.Time)

	return t
}

// stringValue narrows any to a string without panicking.
func stringValue(v any) string {
	s, _ := v.(string)

	return s
}

// intValue narrows any to an int, accepting the integer shapes a provider may
// return.
//
// float64 is DELIBERATELY not accepted. The read layer runs in-process and
// hands the provider's own values through, so an amount arrives as an integer;
// a float64 here would mean the value had passed through a JSON round trip and
// lost precision, and rendering it would print a wrong price confidently
// (plan Section 8: money is never a float).
func intValue(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	default:
		return 0, false
	}
}
