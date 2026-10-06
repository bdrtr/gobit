package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// The container names of the write surfaces the variant page uses (ADR 0013).
//
// Both are spelled by hand and both are pinned against the owning modules'
// constants at compile time in internal/arch.
const (
	// ServicePricingAdmin is the pricing module's admin write surface.
	ServicePricingAdmin = "pricing.admin"
	// ServiceInventoryAdmin is the inventory module's admin surface.
	ServiceInventoryAdmin = "inventory.admin"
)

// The variant page and its two forms.
const (
	// VariantPath is one variant's page under its product.
	VariantPath = ProductPath + "/variants/{variantID}"
	// VariantPricePath takes the price form.
	VariantPricePath = VariantPath + "/price"
	// VariantStockPath takes the stock form.
	VariantStockPath = VariantPath + "/stock"
	// VariantPricesPath takes the form that adds a base price in a currency
	// the variant has none in, its price set created and linked first when it
	// has none (ADR 0309).
	VariantPricesPath = VariantPath + "/prices"
	// VariantStockItemPath gives a variant without one its inventory item
	// (ADR 0310).
	VariantStockItemPath = VariantPath + "/stock-item"
	// VariantCostPath takes the form that writes the variant's unit cost in
	// one currency (ADR 0412).
	VariantCostPath = VariantPath + "/cost"
)

// PriceWriter is the narrow price surface the panel needs (ADR 0001).
type PriceWriter interface {
	// SetBasePriceAmount sets one base price's amount and leaves every other
	// price on the set untouched; read is the amount the form was drawn with,
	// and a price that moved since is refused (ADR 0280).
	SetBasePriceAmount(ctx context.Context, priceSetID, currencyCode string, read, amount int64) error
}

// The hidden fields of the price and stock forms that carry what the form was
// drawn with, so a write over a value that moved since is refused (ADR 0280).
const (
	formReadAmount   = "read_amount"
	formReadQuantity = "read_quantity"
)

// StockAdmin is the narrow stock surface the panel needs.
//
// It carries a READ because the cross-module read layer does not expose the
// per-location breakdown, and a total cannot be edited: the operator has to
// know which location holds what.
type StockAdmin interface {
	// StockLevelsJSON returns one line per stock location for the item.
	StockLevelsJSON(ctx context.Context, itemID string) (json.RawMessage, error)
	// SetStockLevel sets the physical quantity at one location; read is the
	// count the form was drawn with, and a level that moved since is refused
	// (ADR 0280). It answers the count after the write, lower than quantity
	// when units went to orders waiting for them (ADR 0392).
	SetStockLevel(ctx context.Context, itemID, locationID string, read, quantity int64) (int64, error)
}

// paramStockFilled carries, on the redirect after a stock save, how many of the
// counted units went to orders that were waiting for them (ADR 0392).
const paramStockFilled = "stock_filled"

// stockLevelRow is one location's line on the variant page.
//
// The json tags are the contract with the inventory module's admin surface;
// the panel cannot import that module, so the schema is what binds them. A
// renamed field does not fail silently: the number simply stops arriving, which
// is why the schema is exercised end to end rather than only in a unit test.
type stockLevelRow struct {
	LocationID        string `json:"location_id"`
	LocationName      string `json:"location_name"`
	StockedQuantity   int64  `json:"stocked_quantity"`
	ReservedQuantity  int64  `json:"reserved_quantity"`
	AvailableQuantity int64  `json:"available_quantity"`
}

// priceRow is one editable price on the variant page.
type priceRow struct {
	Currency string
	// Amount is what the input box is filled with: a scaled decimal when the
	// currency's scale is known, the raw minor-unit integer when it is not.
	Amount string
	// Minor reports that Amount is a raw minor-unit integer, so the form can
	// say so rather than let the operator type a decimal that would be read as
	// cents.
	Minor bool
}

// showVariant renders one variant with its editable prices and stock.
func (u *UI) showVariant(w http.ResponseWriter, r *http.Request) {
	productID := chi.URLParam(r, "id")
	variantID := chi.URLParam(r, "variantID")

	u.renderVariant(w, r, http.StatusOK, productID, variantID, "")
}

// submitVariantPrice applies a price edit.
func (u *UI) submitVariantPrice(w http.ResponseWriter, r *http.Request) {
	productID := chi.URLParam(r, "id")
	variantID := chi.URLParam(r, "variantID")

	if u.prices == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Editing unavailable",
			"The pricing module's admin surface is not registered in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	priceSetID := r.PostFormValue(formPriceSetID)
	currency := strings.ToUpper(strings.TrimSpace(r.PostFormValue("currency")))

	scale, minor := u.currencyScales(r.Context())[currency], r.PostFormValue("minor") == "1"
	amount, err := parseAmount(r.PostFormValue("amount"), scale, minor)
	if err != nil {
		u.renderVariant(w, r, http.StatusUnprocessableEntity, productID, variantID, err.Error())
		return
	}
	// The amount the form was drawn with, written in the same notation, so a
	// price that moved since is refused rather than overwritten (ADR 0280).
	read, err := parseAmount(r.PostFormValue(formReadAmount), scale, minor)
	if err != nil {
		u.renderVariant(w, r, http.StatusUnprocessableEntity, productID, variantID,
			"The form does not say which price it was drawn with; open the page again.")
		return
	}

	if err := u.prices.SetBasePriceAmount(r.Context(), priceSetID, currency, read, amount); err != nil {
		u.afterWrite(w, r, err, productID, variantID, "The price could not be saved")
		return
	}

	corehttp.WriteRedirect(r.Context(), w, variantURL(productID, variantID))
}

// submitVariantStock applies a stock edit.
func (u *UI) submitVariantStock(w http.ResponseWriter, r *http.Request) {
	productID := chi.URLParam(r, "id")
	variantID := chi.URLParam(r, "variantID")

	if u.stock == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Editing unavailable",
			"The inventory module's admin surface is not registered in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	itemID := r.PostFormValue("inventory_item_id")
	locationID := r.PostFormValue("location_id")

	// The physical count is a whole number of things. It is parsed as an
	// integer and never as an amount: there is no scale to apply and a
	// decimal here would mean the operator misread the box.
	quantity, convErr := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("quantity")), 10, 64)
	if convErr != nil {
		u.renderVariant(w, r, http.StatusUnprocessableEntity, productID, variantID,
			"The quantity must be a whole number.")
		return
	}
	// The count the form was drawn with: a level that moved since is refused
	// rather than overwritten (ADR 0280).
	read, convErr := strconv.ParseInt(strings.TrimSpace(r.PostFormValue(formReadQuantity)), 10, 64)
	if convErr != nil {
		u.renderVariant(w, r, http.StatusUnprocessableEntity, productID, variantID,
			"The form does not say which count it was drawn with; open the page again.")
		return
	}

	stocked, err := u.stock.SetStockLevel(r.Context(), itemID, locationID, read, quantity)
	if err != nil {
		u.afterWrite(w, r, err, productID, variantID, "The stock could not be saved")
		return
	}

	// The count was saved and some of it went straight to orders waiting for
	// it; the operator sees a lower number than they typed and is told why.
	target := variantURL(productID, variantID)
	if stocked < quantity {
		target += "?" + url.Values{paramStockFilled: {strconv.FormatInt(quantity-stocked, 10)}}.Encode()
	}
	corehttp.WriteRedirect(r.Context(), w, target)
}

// afterWrite decides what a failed write shows.
//
// A rejection the operator can act on goes back onto the page next to the
// values they typed; anything else becomes the panel's error page and the real
// cause goes to the log. The split is [UI.submitProductEdit]'s and the reason
// is written there.
func (u *UI) afterWrite(
	w http.ResponseWriter, r *http.Request, err error, productID, variantID, title string,
) {
	if errors.IsInvalid(err) || errors.IsConflict(err) {
		u.renderVariant(w, r, http.StatusUnprocessableEntity, productID, variantID, messageFor(err))
		return
	}

	u.unexpectedFailure(w, r, err, title)
}

// renderVariant reads the variant and writes the page.
func (u *UI) renderVariant(
	w http.ResponseWriter, r *http.Request, status int, productID, variantID, message string,
) {
	if strings.TrimSpace(variantID) == "" {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "No variant was named.")
		return
	}

	// A refused price or stock form lands here under pricing:write or
	// inventory:write, and neither opens the variant page (ADR 0260). An
	// operator holding only the write is told why the write was refused and
	// reads nothing of the product module's.
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if !principal.HasScope(scopeProductRead) {
		u.errorPage(w, r, status, "Not saved", message)
		return
	}

	access := variantAccessOf(r)
	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity:  EntityVariant,
		Fields:  []string{fieldID, fieldTitle, fieldSKU, FieldBundleComponents, FieldVariantUnitCosts},
		Filters: map[string]any{filterID: []string{variantID}},
		Expand:  access.expansions(),
	})
	if err != nil {
		u.catalogFailure(w, r, err, "The variant could not be read.")
		return
	}
	if len(records) == 0 {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "There is no variant with that id.")
		return
	}

	// A hidden section is not drawn from the record either, so a read layer
	// that answered with more than it was asked for still prints nothing the
	// operator may not read.
	record := records[0]
	var priceSetID, itemID string
	var editable []priceRow
	var others []otherPriceRow
	// The scales serve only the prices, so they are read only where the
	// prices are shown.
	var scales map[string]int
	if !access.PricesHidden {
		scales = u.currencyScales(r.Context())
		priceSetID, _ = recordChildID(record, keyPriceSet)
		editable, others = variantPrices(record, scales)
	}
	if !access.StockHidden {
		itemID, _ = recordChildID(record, keyInventory)
	}
	// A variant's list prices, with the groups each is for, come from the
	// pricing module's surface, which reads the ruled ones the read layer
	// leaves out; the expansion's list prices are then not printed twice
	// (ADR 0327).
	var listPrices []listPriceView
	var listPricesUnread bool
	var listPriceForm *listPriceForm
	if admin, ok := u.prices.(ListPriceAdmin); ok && !access.PricesHidden && priceSetID != "" {
		listPrices, listPricesUnread = u.listPricesOf(r, admin, priceSetID, scales)
		if !listPricesUnread {
			others = slices.DeleteFunc(others, func(row otherPriceRow) bool { return row.OnList })
		}
		if u.canWriteListPrices(r) {
			listPriceForm = u.listPriceFormOf(r, scales)
		}
	}
	parts, err := u.loadBundle(r, record)
	if err != nil {
		u.catalogFailure(w, r, err, "The variant's bundle could not be read.")
		return
	}
	// The unit costs are the product module's, read with the variant under
	// product:read (ADR 0401). The scales print them and list the currencies
	// a cost may be added in, so they are read here, when the prices did not
	// read them, only where there is a cost to print or a form to offer
	// (ADR 0412).
	entries, canCost := costEntries(record), u.canCostVariant(r)
	if scales == nil && (len(entries) > 0 || canCost) {
		scales = u.currencyScales(r.Context())
	}
	costs := variantCosts(entries, scales)

	u.templates.render(w, r, status, "variant.gohtml", map[string]any{
		titleKey:        recordString(record, fieldTitle),
		"Variant":       variantRow{ID: recordString(record, fieldID), Title: recordString(record, fieldTitle), SKU: recordString(record, fieldSKU)},
		"Prices":        editable,
		"OtherPrices":   others,
		"PriceSetID":    priceSetID,
		"ItemID":        itemID,
		"Levels":        u.stockRows(r.Context(), itemID),
		"StockFilled":   stockFilledOf(r),
		"Access":        access,
		errorKey:        message,
		"PricePath":     variantURL(productID, variantID) + "/price",
		"StockPath":     variantURL(productID, variantID) + "/stock",
		"ProductPath":   ProductsPath + "/" + productID,
		"CanEdit":       u.prices != nil,
		"CanStock":      u.stock != nil,
		"Parts":         parts,
		productsPathKey: ProductsPath,
		"BundlePath":    variantURL(productID, variantID) + "/bundle",
		"CanBundle":     u.products != nil,
		// The form that adds a base price, in the currencies the variant has
		// none in (ADR 0309).
		"CanAddPrice":   !access.PricesHidden && u.canPriceVariant(r),
		"NewCurrencies": unpricedCurrencies(scales, editable),
		"PricesPath":    variantURL(productID, variantID) + "/prices",
		// The button that gives a variant its inventory item (ADR 0310); the
		// template draws it only where the page says the variant has none.
		"CanKeepStock":  u.canStockVariant(r),
		"StockItemPath": variantURL(productID, variantID) + "/stock-item",
		// The list prices and the form that adds one (ADR 0327).
		"ListPrices":       listPrices,
		"ListPricesUnread": listPricesUnread,
		"ListPriceForm":    listPriceForm,
		"ListPricesPath":   variantURL(productID, variantID) + "/list-prices",
		"CanRemoveList":    u.canWriteListPrices(r),
		// The unit costs and the form that writes one (ADR 0412).
		"Costs":             costs,
		"CanCost":           canCost,
		"NewCostCurrencies": uncostedCurrencies(scales, costs),
		"CostPath":          variantURL(productID, variantID) + "/cost",
	})
}

// VariantStocker is the narrow surface a variant's inventory item is made
// through: the product module's, which has inventory create it and links it
// (ADR 0310).
type VariantStocker interface {
	// StockVariant gives the variant an inventory item and returns its id.
	StockVariant(ctx context.Context, variantID string) (string, error)
}

// canStockVariant reports whether the operator may give a variant its item:
// the item is inventory's and the link the product's, so both writes are
// needed, and the product module's surface must be able to do it.
func (u *UI) canStockVariant(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.products.(VariantStocker)

	return ok && principal.HasScope(scopeInventoryWrite) && principal.HasScope(scopeProductWrite)
}

// keepVariantStock gives the variant its inventory item and returns to the
// variant's page, where its levels are set by location (ADR 0310).
func (u *UI) keepVariantStock(w http.ResponseWriter, r *http.Request) {
	productID := chi.URLParam(r, "id")
	variantID := chi.URLParam(r, "variantID")

	stocker, ok := u.products.(VariantStocker)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Stock unavailable",
			"The product module's admin surface cannot stock a variant in this installation.")
		return
	}
	// The route asks for the product's privilege, whose surface links the
	// item; the item is inventory's, so its privilege is asked here as well.
	if principal, _ := corehttp.PrincipalFromContext(r.Context()); !principal.HasScope(scopeInventoryWrite) {
		u.errorPage(w, r, http.StatusForbidden, "Not allowed",
			"Keeping a variant's stock creates its inventory item, which needs the "+scopeInventoryWrite+" privilege as well.")
		return
	}

	if _, err := stocker.StockVariant(r.Context(), variantID); err != nil {
		u.afterWrite(w, r, err, productID, variantID, "The variant's stock could not be kept")
		return
	}

	corehttp.WriteRedirect(r.Context(), w, variantURL(productID, variantID))
}

// VariantPricer is the narrow surface a variant's first price, or a price in a
// new currency, is written through: the product module's, which creates and
// links the price set the way an import does (ADR 0207, ADR 0309).
type VariantPricer interface {
	// PriceVariant sets the variant's base price at one unit in the currency,
	// creating and linking its price set first when it has none.
	PriceVariant(ctx context.Context, variantID, currencyCode string, amount int64) error
}

// canPriceVariant reports whether the operator may add a price here: the
// price is pricing's and the link to it the product's, so both writes are
// needed, and the product module's surface must be able to do it.
func (u *UI) canPriceVariant(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.products.(VariantPricer)

	return ok && principal.HasScope(scopePricingWrite) && principal.HasScope(scopeProductWrite)
}

// unpricedCurrencies are the shop's currencies, sorted, that the variant has
// no base price in.
func unpricedCurrencies(scales map[string]int, priced []priceRow) []string {
	have := map[string]bool{}
	for _, row := range priced {
		have[row.Currency] = true
	}
	var out []string
	for code := range scales {
		if !have[code] {
			out = append(out, code)
		}
	}
	slices.Sort(out)

	return out
}

// addVariantPrice adds a base price in a currency the variant has none in and
// returns to the variant's page; a refusal is printed there.
func (u *UI) addVariantPrice(w http.ResponseWriter, r *http.Request) {
	productID := chi.URLParam(r, "id")
	variantID := chi.URLParam(r, "variantID")

	pricer, ok := u.products.(VariantPricer)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Pricing unavailable",
			"The product module's admin surface cannot price a variant in this installation.")
		return
	}
	// The route asks for the product's privilege, whose surface links the
	// price set; the price is pricing's, so its privilege is asked here as
	// well, as the import asks for both (ADR 0207).
	if principal, _ := corehttp.PrincipalFromContext(r.Context()); !principal.HasScope(scopePricingWrite) {
		u.errorPage(w, r, http.StatusForbidden, "Not allowed",
			"Adding a price writes the variant's prices, which needs the "+scopePricingWrite+" privilege as well.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	currency := strings.ToUpper(strings.TrimSpace(r.PostFormValue("currency")))
	scale, known := u.currencyScales(r.Context())[currency]
	amount, err := parseAmount(r.PostFormValue("amount"), scale, !known)
	if err != nil {
		u.renderVariant(w, r, http.StatusUnprocessableEntity, productID, variantID, err.Error())
		return
	}

	if err := pricer.PriceVariant(r.Context(), variantID, currency, amount); err != nil {
		u.afterWrite(w, r, err, productID, variantID, "The price could not be added")
		return
	}

	corehttp.WriteRedirect(r.Context(), w, variantURL(productID, variantID))
}

// The price sub-record's fields that say when a price applies. Like the amount
// and the currency, they are pricing's and pricing does not publish them.
const (
	fieldMinQuantity = "min_quantity"
	fieldMaxQuantity = "max_quantity"
	fieldPriceListID = "price_list_id"
)

// otherPriceRow is a price the variant page shows and does not edit.
type otherPriceRow struct {
	Currency string
	Amount   string
	Minor    bool
	// Applies says when the price applies: its quantities and its list.
	Applies string
	// OnList says the price is on a price list, which the page lists apart
	// when the pricing module's surface reads them (ADR 0327).
	OnList bool
}

// variantPrices splits the price-set expansion into the prices the page edits
// and the ones it only shows (ADR 0206).
//
// The form names a currency and nothing else, and the write behind it changes
// the base price at one unit in that currency. So only that price gets a form:
// a quantity tier, a price on a list, or one of two prices at one unit in the
// same currency would have been a box whose save changed another price.
func variantPrices(record query.Record, scales map[string]int) ([]priceRow, []otherPriceRow) {
	set, _ := record[keyPriceSet].(query.Record)
	raw, _ := set[fieldPrices].([]map[string]any)

	type read struct {
		view    priceView
		unit    bool
		applies string
		onList  bool
	}
	prices := make([]read, 0, len(raw))
	unitCount := map[string]int{}
	for _, price := range raw {
		amount, ok := intValue(price[fieldAmount])
		if !ok {
			continue
		}
		code := strings.ToUpper(stringValue(price[fieldCurrencyCod]))
		text, exact := formatAmount(int64(amount), code, scales)
		listID := priceListID(price[fieldPriceListID])
		from, upTo := quantityRange(price)
		unit := listID == "" && from <= 1 && (upTo == nil || *upTo >= 1)
		if unit {
			unitCount[code]++
		}
		prices = append(prices, read{
			view:    priceView{Amount: text, Currency: code, Minor: !exact},
			unit:    unit,
			applies: applies(from, upTo, listID),
			onList:  listID != "",
		})
	}

	var editable []priceRow
	var others []otherPriceRow
	for _, price := range prices {
		if price.unit && unitCount[price.view.Currency] == 1 {
			editable = append(editable, priceRow{Currency: price.view.Currency, Amount: price.view.Amount, Minor: price.view.Minor})
			continue
		}
		others = append(others, otherPriceRow{
			Currency: price.view.Currency, Amount: price.view.Amount, Minor: price.view.Minor, Applies: price.applies,
			OnList: price.onList,
		})
	}

	return editable, others
}

// priceListID reads a price's list, "" when it has none. Pricing writes a
// *string that may be a typed nil, which a nil check on the interface misses.
func priceListID(raw any) string {
	switch value := raw.(type) {
	case *string:
		if value != nil {
			return *value
		}
	case string:
		return value
	}

	return ""
}

// quantityRange reads a price's quantity range: a missing lower end is one,
// and a missing upper end is open, as pricing's schema defaults them.
func quantityRange(price map[string]any) (from int, upTo *int) {
	from = 1
	if value, ok := intValue(price[fieldMinQuantity]); ok {
		from = value
	}

	switch value := price[fieldMaxQuantity].(type) {
	case *int32:
		if value != nil {
			bound := int(*value)
			return from, &bound
		}
	default:
		if bound, ok := intValue(value); ok {
			return from, &bound
		}
	}

	return from, nil
}

// applies describes when a price applies.
func applies(from int, upTo *int, listID string) string {
	quantities := strconv.Itoa(from) + " or more"
	if upTo != nil {
		quantities = strconv.Itoa(from) + " to " + strconv.Itoa(*upTo)
	}
	if listID != "" {
		return quantities + ", on price list " + listID
	}

	return quantities
}

// stockRows reads the per-location levels through the inventory admin surface.
//
// A failure is NOT fatal to the page: the prices are still shown and the stock
// section says it could not be read. A variant page that refused to open
// because one of two panels was unavailable would hide the half that worked.
func (u *UI) stockRows(ctx context.Context, itemID string) []stockLevelRow {
	if u.stock == nil || itemID == "" {
		return nil
	}

	body, err := u.stock.StockLevelsJSON(ctx, itemID)
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx,
			"the panel could not read the stock levels", "error", err, "item_id", itemID)

		return nil
	}

	var rows []stockLevelRow
	if err := json.Unmarshal(body, &rows); err != nil {
		corehttp.LoggerFromContext(ctx).ErrorContext(ctx,
			"the stock levels did not match the expected schema", "error", err, "item_id", itemID)

		return nil
	}

	return rows
}

// recordChildID reads the id out of an expansion.
func recordChildID(record query.Record, key string) (string, bool) {
	child, ok := record[key].(query.Record)
	if !ok {
		return "", false
	}
	id := recordString(child, fieldID)

	return id, id != ""
}

// variantURL builds a variant's path.
func variantURL(productID, variantID string) string {
	return ProductsPath + "/" + productID + "/variants/" + variantID
}

// parseAmount turns what the operator typed into MINOR UNITS.
//
// # No float, ever
//
// The whole conversion is integer arithmetic. Parsing "199.90" as a float and
// multiplying by 100 is exactly the operation plan Section 8 forbids for money:
// at large amounts the product is no longer the number that was typed, and the
// error is invisible until an invoice disagrees with a total.
//
// # Two modes, and the operator is told which one they are in
//
// With the currency's scale KNOWN the box takes a scaled decimal ("199.90") and
// at most that many fractional digits; a third digit on a two-digit currency is
// REFUSED rather than rounded, because rounding silently changes a price the
// operator wrote down.
//
// With the scale unknown the box takes the raw minor-unit integer and the form
// says so. Guessing two digits would let "199.90" be stored as 19990 on a
// currency where the right answer is 199900.
func parseAmount(text string, digits int, minor bool) (int64, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, errors.Invalid(CodeAmountInvalid, "An amount is required.")
	}

	if minor || digits <= 0 {
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return 0, errors.Invalid(CodeAmountInvalid,
				"The amount must be a whole number of minor units.")
		}

		return value, nil
	}

	negative := strings.HasPrefix(text, "-")
	text = strings.TrimPrefix(text, "-")

	whole, fraction, hasPoint := strings.Cut(text, ".")
	if !hasPoint {
		fraction = ""
	}
	if strings.ContainsAny(fraction, ".") {
		return 0, errors.Invalid(CodeAmountInvalid, "The amount has more than one decimal point.")
	}
	if len(fraction) > digits {
		return 0, errors.Invalid(CodeAmountInvalid,
			"This currency has %d decimal digits; %q has more.", digits, text)
	}
	if whole == "" && fraction == "" {
		return 0, errors.Invalid(CodeAmountInvalid, "An amount is required.")
	}
	if whole == "" {
		whole = "0"
	}

	// The fraction is padded rather than scaled: "1.5" on a two-digit currency
	// is 150 minor units, not 15.
	fraction += strings.Repeat("0", digits-len(fraction))

	value, err := strconv.ParseInt(whole+fraction, 10, 64)
	if err != nil {
		return 0, errors.Invalid(CodeAmountInvalid, "The amount must be a number.")
	}
	if negative {
		value = -value
	}

	return value, nil
}

// stockFilledOf reads how many counted units went to waiting orders, or 0 when
// the page was not reached from a stock save that filled any.
func stockFilledOf(r *http.Request) int64 {
	filled, err := strconv.ParseInt(r.URL.Query().Get(paramStockFilled), 10, 64)
	if err != nil || filled <= 0 {
		return 0
	}

	return filled
}
