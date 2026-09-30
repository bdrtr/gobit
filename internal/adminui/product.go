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
	"time"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/core/http"
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

// variantAccess says which of a variant's two other modules the operator may
// read on the product and variant pages (ADR 0260).
//
// The prices are the pricing module's and the stock the inventory module's, and
// the API hands them out under pricing:read and inventory:read. The product's
// privilege opens the page; expanding them for everybody it admits would make
// the page a second door into both, which is the rule the order page follows
// for its payment and parcels (ADR 0251).
type variantAccess struct {
	// PricesHidden reports that the operator lacks pricing:read, so the page
	// neither reads nor prints a price.
	PricesHidden bool
	// StockHidden reports that the operator lacks inventory:read.
	StockHidden bool
	// PricingPrivilege and InventoryPrivilege are what the page names when it
	// hides a section, so an operator can tell a missing grant from a missing
	// price.
	PricingPrivilege   string
	InventoryPrivilege string
}

// variantAccessOf reads the operator's grants off the request.
func variantAccessOf(r *http.Request) variantAccess {
	principal, _ := corehttp.PrincipalFromContext(r.Context())

	return variantAccess{
		PricesHidden:       !principal.HasScope(scopePricingRead),
		StockHidden:        !principal.HasScope(scopeInventoryRead),
		PricingPrivilege:   scopePricingRead,
		InventoryPrivilege: scopeInventoryRead,
	}
}

// expansions is what a variant read expands for the operator: nothing they
// may not read is asked for.
func (a variantAccess) expansions() []query.Expansion {
	var out []query.Expansion
	if !a.PricesHidden {
		out = append(out, query.Expansion{Link: LinkVariantPriceSet, As: keyPriceSet, Fields: []string{fieldID, fieldPrices}})
	}
	if !a.StockHidden {
		out = append(out, query.Expansion{Link: LinkVariantInventory, As: keyInventory, Fields: []string{fieldID, fieldAvailable}})
	}

	return out
}

// showProduct renders one product together with its variants, prices and stock.
//
// Two Graph calls are made, not one, and that is a property of the data rather
// than a shortcut: variants live in the SAME module as products, so there is no
// link between them and the read layer joins only across links. Asking for
// variants is therefore a root query of its own, filtered by product_id. A
// product with related products costs a third, for the same reason: the related
// products are products of the same module, read in one call for all three
// lists (ADR 0181, [UI.loadRelations]).
//
// The prices and the stock DO come through links, in the same call: one
// expansion each, both batched by the read layer. A screen that fetched them
// per variant would issue a query per row, which is exactly what the read
// layer's no-N+1 rule exists to prevent. Each is expanded only for an operator
// who may read it ([variantAccess]).
func (u *UI) showProduct(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	product, record, relations, ok := u.loadRelations(w, r, id)
	if !ok {
		return
	}
	addOns, err := u.loadAddOns(r, record)
	if err != nil {
		u.catalogFailure(w, r, err, "The add-ons could not be read.")
		return
	}

	access := variantAccessOf(r)
	variants, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity:  EntityVariant,
		Fields:  []string{fieldID, fieldTitle, fieldSKU},
		Filters: map[string]any{filterProductID: []string{id}},
		Expand:  access.expansions(),
	})
	if err != nil {
		u.catalogFailure(w, r, err, "The variants could not be read.")
		return
	}

	var scales map[string]int
	if !access.PricesHidden {
		scales = u.currencyScales(r.Context())
	}
	rows := make([]variantRow, 0, len(variants))
	for _, rec := range variants {
		row := variantRow{
			ID:    recordString(rec, fieldID),
			Title: recordString(rec, fieldTitle),
			SKU:   recordString(rec, fieldSKU),
		}
		if !access.PricesHidden {
			row.Prices = pricesOf(rec, scales)
		}
		if !access.StockHidden {
			row.Stock = stockOf(rec)
		}
		rows = append(rows, row)
	}

	u.templates.render(w, r, http.StatusOK, "product.gohtml", map[string]any{
		titleKey:        product.Title,
		productKey:      product,
		"Variants":      rows,
		"Access":        access,
		"Relations":     relations,
		"ProductsPath":  ProductsPath,
		"EditPath":      ProductsPath + "/" + product.ID + "/edit",
		"RelationsPath": ProductsPath + "/" + product.ID + "/relations",
		"AddOns":        addOns,
		"AddOnsPath":    ProductsPath + "/" + product.ID + "/add-ons",
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
		PublishAt: recordMoment(rec, fieldPublishAt),
		ArchiveAt: recordMoment(rec, fieldArchiveAt),
		Version:   recordInt(rec, fieldVersion),
	}
}

// recordMoment reads a moment that may be absent, as nil rather than as the
// zero time, so a page never prints the year one.
func recordMoment(rec query.Record, field string) *time.Time {
	t := recordTime(rec, field)
	if t.IsZero() {
		return nil
	}

	return &t
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
