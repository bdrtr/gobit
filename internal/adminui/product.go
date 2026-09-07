package adminui

// The product-detail screen: one product, its variants, their prices and their
// stock. It sits beside the LIST screen in catalog.go rather than inside it
// because the panel is otherwise strictly one screen per file, and the two
// screens read different entities through different specs. The pieces the two
// share — the row type, the record helpers, the money helpers — live in
// catalog.go, record.go and money.go, so neither screen owns the other's tools.

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/query"
)

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
