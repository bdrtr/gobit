package adminui

// The related-products screen (ADR 0181): the lists on the product page, and the
// form that edits them. It is a file of its own for the reason every screen is;
// the product page borrows [UI.loadRelations] from here rather than owning a
// second reading of the same lists.

import (
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// ProductRelationsPath is the form that edits one product's related products.
const ProductRelationsPath = ProductPath + "/relations"

// The read layer's fields carrying a product's relations, one per kind.
//
// They are exported for the reason the entity names are: a rename in the
// product module would leave this package compiling and every product page
// showing no related products, and internal/arch binds the two spellings at
// compile time.
const (
	FieldCrossSellIDs  = "cross_sell_ids"
	FieldUpSellIDs     = "up_sell_ids"
	FieldSubstituteIDs = "substitute_ids"
)

// RelationLimit is the longest list the product module keeps for one kind.
//
// The panel does not enforce it — the module does, and refuses the whole save —
// it only prints it on the form, and a number printed there that the module no
// longer keeps would be a false instruction. internal/arch binds it to the
// module's.
const RelationLimit = 50

// relationKind is one kind of relation as the panel shows it.
type relationKind struct {
	// kind is the product module's word for it, which is also the form field's
	// name.
	kind string
	// label is what the operator reads.
	label string
	// field is the read layer's field holding its ids.
	field string
}

// relationKinds are the kinds in the order the panel shows them.
//
// The words are the module's and the panel repeats them because the module's
// type cannot be imported; [RelationKinds] lets internal/arch compare the two.
// A kind the module no longer accepts is refused loudly on save; a kind it
// ADDED would silently never appear here, which is what the comparison catches.
var relationKinds = []relationKind{
	{kind: "cross_sell", label: "Goes with it", field: FieldCrossSellIDs},
	{kind: "up_sell", label: "The better one", field: FieldUpSellIDs},
	{kind: "substitute", label: "Instead of it", field: FieldSubstituteIDs},
}

// RelationKinds returns the kinds the panel shows, in its order.
func RelationKinds() []string {
	out := make([]string, 0, len(relationKinds))
	for _, k := range relationKinds {
		out = append(out, k.kind)
	}

	return out
}

// relatedProduct is one entry of a list.
type relatedProduct struct {
	ID     string
	Title  string
	Handle string
	Status string
}

// Hidden reports that the storefront leaves the product out of the list: it
// shows a related product only once it is published.
func (p relatedProduct) Hidden() bool { return p.Status != statusPublished }

// relatedList is one kind's list, and what the form shows for it.
type relatedList struct {
	Kind     string
	Label    string
	Products []relatedProduct
	// Typed is what the operator typed, when the form comes back refused; it
	// wins over the stored list so the mistake shows.
	Typed *string
}

// Text is the form's value: one handle per line, in the stored order.
func (l relatedList) Text() string {
	if l.Typed != nil {
		return *l.Typed
	}
	handles := make([]string, 0, len(l.Products))
	for _, p := range l.Products {
		handles = append(handles, p.Handle)
	}

	return strings.Join(handles, "\n")
}

// loadRelations reads a product and its related products: the product record,
// which carries the three lists of ids, then the related products themselves in
// ONE read for all three lists.
//
// A related id the second read does not return is left out. The module takes a
// deleted product off every list in the deletion's own transaction, so this is
// the gap between two reads, not a state the lists are kept in.
func (u *UI) loadRelations(
	w http.ResponseWriter, r *http.Request, id string,
) (product productRow, lists []relatedList, ok bool) {
	if strings.TrimSpace(id) == "" {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "No product was named.")
		return productRow{}, nil, false
	}
	products, err := u.catalog.Graph(r.Context(), productByID(id))
	if err != nil {
		u.catalogFailure(w, r, err, "The product could not be read.")
		return productRow{}, nil, false
	}
	if len(products) == 0 {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "There is no product with that id.")
		return productRow{}, nil, false
	}

	var every []string
	for _, k := range relationKinds {
		every = append(every, recordStrings(products[0], k.field)...)
	}
	byID := map[string]relatedProduct{}
	if len(every) > 0 {
		related, err := u.catalog.Graph(r.Context(), query.GraphSpec{
			Entity:  EntityProduct,
			Fields:  []string{fieldID, fieldTitle, fieldHandle, fieldStatus},
			Filters: map[string]any{filterID: every},
			Limit:   len(every),
		})
		if err != nil {
			u.catalogFailure(w, r, err, "The related products could not be read.")
			return productRow{}, nil, false
		}
		for _, rec := range related {
			p := relatedProduct{
				ID:     recordString(rec, fieldID),
				Title:  recordString(rec, fieldTitle),
				Handle: recordString(rec, fieldHandle),
				Status: recordString(rec, fieldStatus),
			}
			byID[p.ID] = p
		}
	}

	lists = make([]relatedList, 0, len(relationKinds))
	for _, k := range relationKinds {
		list := relatedList{Kind: k.kind, Label: k.label, Products: []relatedProduct{}}
		for _, relatedID := range recordStrings(products[0], k.field) {
			if p, found := byID[relatedID]; found {
				list.Products = append(list.Products, p)
			}
		}
		lists = append(lists, list)
	}

	return productRowOf(products[0]), lists, true
}

// editRelations renders the form.
func (u *UI) editRelations(w http.ResponseWriter, r *http.Request) {
	product, lists, ok := u.loadRelations(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	u.renderRelationsForm(w, r, http.StatusOK, product, lists, "")
}

// submitRelations saves the three lists and returns to the product page.
//
// The form sends every list, and every list is saved: the module replaces them
// in one transaction, so a list it refuses leaves all three as they were and
// the form comes back with what was typed. The panel checks nothing itself —
// the module's refusal already writes nothing, and names the line to fix.
func (u *UI) submitRelations(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if u.products == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Editing unavailable",
			"The product module's admin surface is not registered in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	typed := make(map[string]string, len(relationKinds))
	lists := make(map[string][]string, len(relationKinds))
	for _, k := range relationKinds {
		typed[k.kind] = r.PostFormValue(k.kind)
		lists[k.kind] = formLines(typed[k.kind])
	}

	err := u.products.SetProductRelations(r.Context(), id, lists)
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w, ProductsPath+"/"+id)
		return
	case errors.IsNotFound(err):
		u.errorPage(w, r, http.StatusNotFound, "Not found", "There is no product with that id.")
		return
	case !errors.IsInvalid(err) && !errors.IsConflict(err):
		u.unexpectedFailure(w, r, err, "The related products could not be saved")
		return
	}

	product, stored, ok := u.loadRelations(w, r, id)
	if !ok {
		return
	}
	for i := range stored {
		text := typed[stored[i].Kind]
		stored[i].Typed = &text
	}
	u.renderRelationsForm(w, r, http.StatusUnprocessableEntity, product, stored, messageFor(err))
}

// renderRelationsForm writes the form with the given status code and message.
func (u *UI) renderRelationsForm(
	w http.ResponseWriter, r *http.Request, status int, product productRow, lists []relatedList, message string,
) {
	u.templates.render(w, r, status, "product_relations.gohtml", map[string]any{
		titleKey:     "Related products of " + product.Title,
		productKey:   product,
		"Lists":      lists,
		"Limit":      RelationLimit,
		errorKey:     message,
		"ActionPath": ProductsPath + "/" + product.ID + "/relations",
		"CancelPath": ProductsPath + "/" + product.ID,
	})
}

// formLines reads a textarea as one reference per line, trimmed, blank lines
// dropped. A line is otherwise sent as typed: a duplicate or a handle nobody
// carries is the module's to refuse, by name.
func formLines(text string) []string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out
}

// recordStrings reads a list-of-strings field, or nil.
//
// The read layer hands the provider's own value through, a []string; a []any is
// accepted too, as a record that went through JSON would carry it.
func recordStrings(rec query.Record, field string) []string {
	switch values := rec[field].(type) {
	case []string:
		return slices.Clone(values)
	case []any:
		out := make([]string, 0, len(values))
		for _, v := range values {
			if s := stringValue(v); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}
