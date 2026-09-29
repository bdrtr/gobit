package adminui

// The bundle screen (ADR 0236): what a variant is made of, on the variant page,
// and the form that edits it. The parts are read and named as the add-ons are,
// through [UI.namedVariants].

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// VariantBundlePath is the form that edits what one variant is made of.
const VariantBundlePath = VariantPath + "/bundle"

// The read layer's field carrying a variant's composition (ADR 0235), and the
// keys of one part in it. They are exported for the reason the add-on field is:
// internal/arch binds them to the product module's spelling.
const (
	FieldBundleComponents         = "bundle_components"
	FieldBundleComponentVariantID = "variant_id"
	FieldBundleComponentQuantity  = "quantity"
)

// BundleLimit and BundleQuantityLimit are the most parts a bundle holds and the
// most units of one (ADR 0234). The panel prints them and does not enforce them
// -- the module refuses the whole save -- and internal/arch binds them to the
// module's.
const (
	BundleLimit         = 20
	BundleQuantityLimit = 100
)

// bundlePart is one part of a bundle as the panel shows it: the variant, its
// product, and how many of it one bundle holds.
type bundlePart struct {
	namedVariant
	Quantity int64
}

// composition reads a variant record's parts: their ids and units, in the
// operator's order. An entry that does not read is left out; the page shows
// what the module published, and the module wrote nothing it would refuse.
func composition(record query.Record) (ids []string, units map[string]int64) {
	var entries []map[string]any
	switch list := record[FieldBundleComponents].(type) {
	case []query.Record:
		for _, entry := range list {
			entries = append(entries, entry)
		}
	case []map[string]any:
		entries = list
	case []any:
		for _, raw := range list {
			if entry, ok := raw.(map[string]any); ok {
				entries = append(entries, entry)
			}
		}
	}

	units = make(map[string]int64, len(entries))
	for _, entry := range entries {
		id, _ := entry[FieldBundleComponentVariantID].(string)
		quantity := recordInt(query.Record(entry), FieldBundleComponentQuantity)
		if id == "" || quantity < 1 {
			continue
		}
		ids = append(ids, id)
		units[id] = quantity
	}
	return ids, units
}

// loadBundle reads the parts a variant record names, in its order.
func (u *UI) loadBundle(r *http.Request, variant query.Record) ([]bundlePart, error) {
	ids, units := composition(variant)
	named, err := u.namedVariants(r, ids)
	if err != nil {
		return nil, err
	}
	parts := make([]bundlePart, 0, len(named))
	for _, v := range named {
		parts = append(parts, bundlePart{namedVariant: v, Quantity: units[v.VariantID]})
	}
	return parts, nil
}

// bundleText is the form's value: one part per line, its reference and its
// units, in the bundle's order.
func bundleText(parts []bundlePart) string {
	lines := make([]string, 0, len(parts))
	for _, p := range parts {
		lines = append(lines, p.Ref()+" "+strconv.FormatInt(p.Quantity, 10))
	}
	return strings.Join(lines, "\n")
}

// readBundleLines reads the typed parts: each line a variant's SKU or id and,
// after a space, how many one bundle holds -- one when it says nothing. A line
// that does not read comes back as a sentence naming it; the panel checks
// nothing else, and the module decides the rest.
func readBundleLines(typed string) (refs []string, quantities []int64, problem string) {
	for number, line := range strings.Split(typed, "\n") {
		fields := strings.Fields(line)
		switch len(fields) {
		case 0:
			continue
		case 1:
			refs, quantities = append(refs, fields[0]), append(quantities, 1)
		case 2:
			quantity, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return nil, nil, fmt.Sprintf("Line %d: %q is not a whole number of units.", number+1, fields[1])
			}
			refs, quantities = append(refs, fields[0]), append(quantities, quantity)
		default:
			return nil, nil, fmt.Sprintf(
				"Line %d: write a variant's SKU or id, then how many one bundle holds.", number+1)
		}
	}
	return refs, quantities, ""
}

// editBundle renders the form.
func (u *UI) editBundle(w http.ResponseWriter, r *http.Request) {
	form, ok := u.bundleForm(w, r)
	if !ok {
		return
	}
	u.renderBundleForm(w, r, http.StatusOK, form, bundleText(form.parts), "")
}

// submitBundle saves the parts and returns to the variant page.
//
// The module replaces the composition whole and refuses it whole, naming what to
// fix; the form comes back with what was typed. A save made after somebody else
// saved the product comes back with the version now stored, as the product's
// own form does (ADR 0222).
func (u *UI) submitBundle(w http.ResponseWriter, r *http.Request) {
	productID := chi.URLParam(r, "id")
	variantID := chi.URLParam(r, "variantID")

	if u.products == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Editing unavailable",
			"The product module's admin surface is not registered in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}
	version, err := strconv.ParseInt(r.PostFormValue("version"), 10, 64)
	if err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request",
			"The form does not say which version of the product it was opened at; open the variant again.")
		return
	}
	typed := r.PostFormValue("parts")

	refs, quantities, problem := readBundleLines(typed)
	if problem == "" {
		err = u.products.SetVariantBundle(r.Context(), variantID, refs, quantities, version)
		switch {
		case err == nil:
			corehttp.WriteRedirect(r.Context(), w, variantURL(productID, variantID))
			return
		case errors.IsNotFound(err):
			u.errorPage(w, r, http.StatusNotFound, "Not found", "There is no variant with that id.")
			return
		case errors.IsPreconditionFailed(err):
			problem = staleEditMessage
		case errors.IsInvalid(err) || errors.IsConflict(err):
			problem = messageFor(err)
		default:
			u.unexpectedFailure(w, r, err, "The bundle could not be saved")
			return
		}
	}

	form, ok := u.bundleForm(w, r)
	if !ok {
		return
	}
	u.renderBundleForm(w, r, http.StatusUnprocessableEntity, form, typed, problem)
}

// bundleFormData is what the form is drawn from: the variant, its product at the
// version it is read at, and its parts.
type bundleFormData struct {
	productID string
	product   productRow
	variant   variantRow
	parts     []bundlePart
}

// bundleForm reads the variant, its product and its parts for the form. A
// variant of another product than the address names is not found: the version
// the form carries is that product's.
func (u *UI) bundleForm(w http.ResponseWriter, r *http.Request) (bundleFormData, bool) {
	productID := chi.URLParam(r, "id")
	variantID := chi.URLParam(r, "variantID")
	if strings.TrimSpace(productID) == "" || strings.TrimSpace(variantID) == "" {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "No variant was named.")
		return bundleFormData{}, false
	}

	variants, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity:  EntityVariant,
		Fields:  []string{fieldID, fieldTitle, fieldSKU, fieldVariantProductID, FieldBundleComponents},
		Filters: map[string]any{filterID: []string{variantID}},
		Limit:   1,
	})
	if err != nil {
		u.catalogFailure(w, r, err, "The variant could not be read.")
		return bundleFormData{}, false
	}
	if len(variants) == 0 || recordString(variants[0], fieldVariantProductID) != productID {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "There is no variant with that id on this product.")
		return bundleFormData{}, false
	}
	products, err := u.catalog.Graph(r.Context(), productByID(productID))
	if err != nil {
		u.catalogFailure(w, r, err, "The product could not be read.")
		return bundleFormData{}, false
	}
	if len(products) == 0 {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "There is no product with that id.")
		return bundleFormData{}, false
	}
	parts, err := u.loadBundle(r, variants[0])
	if err != nil {
		u.catalogFailure(w, r, err, "The bundle's parts could not be read.")
		return bundleFormData{}, false
	}

	return bundleFormData{
		productID: productID,
		product:   productRowOf(products[0]),
		variant: variantRow{
			ID:    recordString(variants[0], fieldID),
			Title: recordString(variants[0], fieldTitle),
			SKU:   recordString(variants[0], fieldSKU),
		},
		parts: parts,
	}, true
}

// renderBundleForm writes the form with the given status code and message.
func (u *UI) renderBundleForm(
	w http.ResponseWriter, r *http.Request, status int, form bundleFormData, text, message string,
) {
	u.templates.render(w, r, status, "variant_bundle.gohtml", map[string]any{
		titleKey:        "Parts of " + form.variant.Title,
		productKey:      form.product,
		"Variant":       form.variant,
		"Text":          text,
		limitKey:        BundleLimit,
		"QuantityLimit": BundleQuantityLimit,
		errorKey:        message,
		actionPathKey:   variantURL(form.productID, form.variant.ID) + "/bundle",
		cancelPathKey:   variantURL(form.productID, form.variant.ID),
	})
}
