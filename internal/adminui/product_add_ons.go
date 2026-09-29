package adminui

// The add-ons screen (ADR 0232): the list on the product page, and the form that
// edits it. It is a file of its own for the reason every screen is; the product
// page borrows [UI.loadAddOns] from here rather than owning a second reading of
// the same list.

import (
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// ProductAddOnsPath is the form that edits one product's add-ons.
const ProductAddOnsPath = ProductPath + "/add-ons"

// FieldAddOnVariantIDs is the read layer's field carrying a product's add-on
// variants (ADR 0228). It is exported for the reason the relation fields are:
// internal/arch binds it to the product module's spelling.
const FieldAddOnVariantIDs = "add_on_variant_ids"

// AddOnLimit is the longest list the product module keeps. The panel prints it
// and does not enforce it — the module refuses the whole save — and internal/arch
// binds it to the module's.
const AddOnLimit = 20

// fieldVariantProductID is a variant record's product.
const fieldVariantProductID = "product_id"

// addOn is one entry of a product's add-on list as the panel shows it: the
// variant, and the product it belongs to.
type addOn struct {
	VariantID    string
	VariantTitle string
	SKU          string
	ProductID    string
	ProductTitle string
	Status       string
}

// Hidden reports that the storefront leaves the add-on out: it shows one only
// once its product is published.
func (a addOn) Hidden() bool { return a.Status != statusPublished }

// Ref is how the form names the add-on: its SKU, which is what an operator
// knows, or its id when it carries none.
func (a addOn) Ref() string {
	if a.SKU != "" {
		return a.SKU
	}
	return a.VariantID
}

// loadAddOns reads the add-ons a product record names: the variants in ONE
// read, then their products in ONE read, in the list's order. An id either read
// does not return is left out; the module takes a deleted variant off every
// list in the deletion's own transaction, so this is the gap between two reads.
func (u *UI) loadAddOns(r *http.Request, product query.Record) ([]addOn, error) {
	ids := recordStrings(product, FieldAddOnVariantIDs)
	if len(ids) == 0 {
		return []addOn{}, nil
	}
	variants, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity:  EntityVariant,
		Fields:  []string{fieldID, fieldTitle, fieldSKU, fieldVariantProductID},
		Filters: map[string]any{filterID: ids},
		Limit:   len(ids),
	})
	if err != nil {
		return nil, err
	}
	byVariant := make(map[string]addOn, len(variants))
	var productIDs []string
	for _, rec := range variants {
		a := addOn{
			VariantID:    recordString(rec, fieldID),
			VariantTitle: recordString(rec, fieldTitle),
			SKU:          recordString(rec, fieldSKU),
			ProductID:    recordString(rec, fieldVariantProductID),
		}
		byVariant[a.VariantID] = a
		if !slices.Contains(productIDs, a.ProductID) {
			productIDs = append(productIDs, a.ProductID)
		}
	}
	products, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity:  EntityProduct,
		Fields:  []string{fieldID, fieldTitle, fieldStatus},
		Filters: map[string]any{filterID: productIDs},
		Limit:   len(productIDs),
	})
	if err != nil {
		return nil, err
	}
	byProduct := make(map[string]query.Record, len(products))
	for _, rec := range products {
		byProduct[recordString(rec, fieldID)] = rec
	}

	out := make([]addOn, 0, len(ids))
	for _, id := range ids {
		a, found := byVariant[id]
		if !found {
			continue
		}
		if rec, known := byProduct[a.ProductID]; known {
			a.ProductTitle = recordString(rec, fieldTitle)
			a.Status = recordString(rec, fieldStatus)
		}
		out = append(out, a)
	}
	return out, nil
}

// editAddOns renders the form.
func (u *UI) editAddOns(w http.ResponseWriter, r *http.Request) {
	product, addOns, ok := u.productWithAddOns(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	u.renderAddOnsForm(w, r, http.StatusOK, product, addOnsText(addOns), "")
}

// submitAddOns saves the list and returns to the product page.
//
// The module replaces the list whole and refuses it whole, naming the line to
// fix; the form comes back with what was typed. The panel checks nothing itself.
func (u *UI) submitAddOns(w http.ResponseWriter, r *http.Request) {
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
	typed := r.PostFormValue("add_ons")

	err := u.products.SetProductAddOns(r.Context(), id, formLines(typed))
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w, ProductsPath+"/"+id)
		return
	case errors.IsNotFound(err):
		u.errorPage(w, r, http.StatusNotFound, "Not found", "There is no product with that id.")
		return
	case !errors.IsInvalid(err) && !errors.IsConflict(err):
		u.unexpectedFailure(w, r, err, "The add-ons could not be saved")
		return
	}

	product, _, ok := u.productWithAddOns(w, r, id)
	if !ok {
		return
	}
	u.renderAddOnsForm(w, r, http.StatusUnprocessableEntity, product, typed, messageFor(err))
}

// productWithAddOns reads a product and its add-ons for the form.
func (u *UI) productWithAddOns(
	w http.ResponseWriter, r *http.Request, id string,
) (productRow, []addOn, bool) {
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
	addOns, err := u.loadAddOns(r, products[0])
	if err != nil {
		u.catalogFailure(w, r, err, "The add-ons could not be read.")
		return productRow{}, nil, false
	}
	return productRowOf(products[0]), addOns, true
}

// addOnsText is the form's value: one reference per line, in the list's order.
func addOnsText(addOns []addOn) string {
	refs := make([]string, 0, len(addOns))
	for _, a := range addOns {
		refs = append(refs, a.Ref())
	}
	return strings.Join(refs, "\n")
}

// renderAddOnsForm writes the form with the given status code and message.
func (u *UI) renderAddOnsForm(
	w http.ResponseWriter, r *http.Request, status int, product productRow, text, message string,
) {
	u.templates.render(w, r, status, "product_add_ons.gohtml", map[string]any{
		titleKey:      "Add-ons of " + product.Title,
		productKey:    product,
		"Text":        text,
		"Limit":       AddOnLimit,
		errorKey:      message,
		actionPathKey: ProductsPath + "/" + product.ID + "/add-ons",
		cancelPathKey: ProductsPath + "/" + product.ID,
	})
}
