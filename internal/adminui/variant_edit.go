package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/query"
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
)

// PriceWriter is the narrow price surface the panel needs (ADR 0001).
type PriceWriter interface {
	// SetBasePriceAmount sets one base price's amount and leaves every other
	// price on the set untouched.
	SetBasePriceAmount(ctx context.Context, priceSetID, currencyCode string, amount int64) error
}

// StockAdmin is the narrow stock surface the panel needs.
//
// It carries a READ because the cross-module read layer does not expose the
// per-location breakdown, and a total cannot be edited: the operator has to
// know which location holds what.
type StockAdmin interface {
	// StockLevelsJSON returns one line per stock location for the item.
	StockLevelsJSON(ctx context.Context, itemID string) (json.RawMessage, error)
	// SetStockLevel sets the physical quantity at one location.
	SetStockLevel(ctx context.Context, itemID, locationID string, quantity int64) error
}

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

	priceSetID := r.PostFormValue("price_set_id")
	currency := strings.ToUpper(strings.TrimSpace(r.PostFormValue("currency")))

	amount, err := parseAmount(r.PostFormValue("amount"), u.currencyScales(r.Context())[currency],
		r.PostFormValue("minor") == "1")
	if err != nil {
		u.renderVariant(w, r, http.StatusUnprocessableEntity, productID, variantID, err.Error())
		return
	}

	if err := u.prices.SetBasePriceAmount(r.Context(), priceSetID, currency, amount); err != nil {
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

	if err := u.stock.SetStockLevel(r.Context(), itemID, locationID, quantity); err != nil {
		u.afterWrite(w, r, err, productID, variantID, "The stock could not be saved")
		return
	}

	corehttp.WriteRedirect(r.Context(), w, variantURL(productID, variantID))
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

	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity:  EntityVariant,
		Fields:  []string{fieldID, fieldTitle, fieldSKU},
		Filters: map[string]any{filterID: []string{variantID}},
		Expand: []query.Expansion{
			{Link: LinkVariantPriceSet, As: keyPriceSet, Fields: []string{fieldID, fieldPrices}},
			{Link: LinkVariantInventory, As: keyInventory, Fields: []string{fieldID, fieldAvailable}},
		},
	})
	if err != nil {
		u.catalogFailure(w, r, err, "The variant could not be read.")
		return
	}
	if len(records) == 0 {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "There is no variant with that id.")
		return
	}

	record := records[0]
	priceSetID, _ := recordChildID(record, keyPriceSet)
	itemID, _ := recordChildID(record, keyInventory)

	u.templates.render(w, r, status, "variant.gohtml", map[string]any{
		titleKey:      recordString(record, fieldTitle),
		"Variant":     variantRow{ID: recordString(record, fieldID), Title: recordString(record, fieldTitle), SKU: recordString(record, fieldSKU)},
		"Prices":      u.priceRows(r.Context(), record),
		"PriceSetID":  priceSetID,
		"ItemID":      itemID,
		"Levels":      u.stockRows(r.Context(), itemID),
		errorKey:      message,
		"PricePath":   variantURL(productID, variantID) + "/price",
		"StockPath":   variantURL(productID, variantID) + "/stock",
		"ProductPath": ProductsPath + "/" + productID,
		"CanEdit":     u.prices != nil,
		"CanStock":    u.stock != nil,
	})
}

// priceRows turns the price-set expansion into editable rows.
func (u *UI) priceRows(ctx context.Context, record query.Record) []priceRow {
	scales := u.currencyScales(ctx)
	views := pricesOf(record, scales)

	rows := make([]priceRow, 0, len(views))
	for _, view := range views {
		rows = append(rows, priceRow{Currency: view.Currency, Amount: view.Amount, Minor: view.Minor})
	}

	return rows
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
