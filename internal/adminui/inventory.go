package adminui

import (
	"net/http"

	"github.com/bdrtr/gobit/internal/core/query"
)

// EntityInventoryItem is the inventory module's entity name in the read layer.
//
// It is a STRING and not an import, for the reason [EntityOrder] is: the panel
// knows no module (ADR 0011).
const EntityInventoryItem = "inventory_item"

// fieldRequiresShipping is the read layer's name for whether the item ships.
const fieldRequiresShipping = "requires_shipping"

// inventoryLabel is what the section is called on screen.
const inventoryLabel = "Inventory"

// inventoryPerPage is the page size of the inventory list.
const inventoryPerPage = 25

// inventoryRow is one line of the inventory table.
type inventoryRow struct {
	ID    string
	SKU   string
	Title string
	// Available is the sellable total across every location.
	//
	// It is a POINTER because "no answer" and "none in stock" are different
	// facts, and printing 0 for both would send an operator looking for stock
	// that is actually on the shelf.
	//
	// The inventory provider always produces the field when it is asked for, so
	// a MISSING field is not a state the panel can reach today. What is
	// reachable is a value it cannot read as an integer: [intValue] refuses a
	// float on purpose, because a quantity that arrived as one has been through
	// a conversion that money and counts must not go through. That refusal is
	// what this pointer carries, and it is why the branch is not dead code.
	Available *int64
	// RequiresShipping separates a physical item from one that is not shipped.
	RequiresShipping bool
}

// listInventory renders the inventory list.
//
// # Why there is no detail page
//
// An inventory item's detail is its per-location levels, and the panel already
// shows those on the variant page — where an operator is when they care. A
// second screen showing the same numbers would be a second place to keep in
// step, and reaching one item by identity would need a filter the inventory
// provider does not offer; widening a module's published contract for a screen
// that duplicates another one is not a trade worth making.
func (u *UI) listInventory(w http.ResponseWriter, r *http.Request) {
	page := pageNumber(r.URL.Query().Get("page"))

	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity: EntityInventoryItem,
		Fields: []string{
			fieldID, fieldSKU, fieldTitle, fieldRequiresShipping, fieldAvailable,
		},
		Limit:  inventoryPerPage + 1,
		Offset: (page - 1) * inventoryPerPage,
	})
	if err != nil {
		u.catalogFailure(w, r, err, "The inventory list could not be read.")

		return
	}

	hasNext := len(records) > inventoryPerPage
	if hasNext {
		records = records[:inventoryPerPage]
	}

	rows := make([]inventoryRow, 0, len(records))
	for _, rec := range records {
		rows = append(rows, inventoryRowOf(rec))
	}

	data := map[string]any{
		titleKey: inventoryLabel,
		"Items":  rows,
	}
	addPaging(data, page, hasNext, InventoryPath)

	u.templates.render(w, r, http.StatusOK, "inventory.gohtml", data)
}

// inventoryRowOf turns an inventory record into a row.
func inventoryRowOf(rec query.Record) inventoryRow {
	row := inventoryRow{
		ID:               recordString(rec, fieldID),
		SKU:              recordString(rec, fieldSKU),
		Title:            recordString(rec, fieldTitle),
		RequiresShipping: recordBool(rec, fieldRequiresShipping),
	}

	if value, ok := intValue(rec[fieldAvailable]); ok {
		available := int64(value)
		row.Available = &available
	}

	return row
}
