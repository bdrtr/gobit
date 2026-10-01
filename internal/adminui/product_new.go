package adminui

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// The panel creates a draft product and adds variants to a product (ADR
// 0307), through the product module's admin surface.
const (
	// ProductNewPath draws and takes the form that creates a product. Its
	// static segment wins over [ProductPath]'s id.
	ProductNewPath = ProductsPath + "/new"
	// ProductVariantsPath takes the form that adds a variant to a product.
	ProductVariantsPath = ProductPath + "/variants"
)

// The new product's and the new variant's form fields.
const (
	formProductTitle  = "title"
	formProductHandle = "handle"
	formVariantTitle  = "variant_title"
	formVariantSKU    = "variant_sku"
)

// ProductCreator is the narrow surface the panel creates through (ADR 0001):
// the product module's own acts, so the panel refuses what the module refuses.
type ProductCreator interface {
	// CreateProduct creates a draft product with the title and the handle,
	// the title's slug when it is empty, and returns its id.
	CreateProduct(ctx context.Context, title, handle string) (string, error)
	// AddVariant adds a variant with the title and the SKU, none when empty,
	// to the product and returns its id.
	AddVariant(ctx context.Context, productID, title, sku string) (string, error)
}

// canCreate reports whether the operator may create products and the panel can.
func (u *UI) canCreate(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.products.(ProductCreator)

	return ok && principal.HasScope(scopeProductWrite)
}

// newProduct draws the form that creates a product.
func (u *UI) newProduct(w http.ResponseWriter, r *http.Request) {
	u.renderNewProduct(w, r, http.StatusOK, "", url.Values{})
}

// createProduct creates a draft product and goes to its page; a refusal comes
// back on the form with what was typed.
func (u *UI) createProduct(w http.ResponseWriter, r *http.Request) {
	creator, ok := u.products.(ProductCreator)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Products cannot be created",
			"The product module's admin surface is not registered in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	id, err := creator.CreateProduct(r.Context(), r.PostFormValue(formProductTitle), r.PostFormValue(formProductHandle))
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w, ProductsPath+"/"+id)
	case errors.IsInvalid(err) || errors.IsConflict(err):
		u.renderNewProduct(w, r, http.StatusUnprocessableEntity, messageFor(err), r.PostForm)
	default:
		u.unexpectedFailure(w, r, err, "The product could not be created")
	}
}

// renderNewProduct writes the form with a refusal and what was typed.
func (u *UI) renderNewProduct(w http.ResponseWriter, r *http.Request, status int, refused string, typed url.Values) {
	u.templates.render(w, r, status, "product_new.gohtml", map[string]any{
		titleKey:        "New product",
		productsPathKey: ProductsPath,
		"NewPath":       ProductNewPath,
		refusedKey:      refused,
		typedKey:        typed,
	})
}

// addVariant adds a variant to the product in the path and goes to the
// variant's page, where its prices and stock are kept; a refusal is printed on
// the product's page.
func (u *UI) addVariant(w http.ResponseWriter, r *http.Request) {
	productID := chi.URLParam(r, "id")
	creator, ok := u.products.(ProductCreator)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Variants cannot be added",
			"The product module's admin surface is not registered in this installation.")
		return
	}
	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	id, err := creator.AddVariant(r.Context(), productID,
		r.PostFormValue(formVariantTitle), strings.TrimSpace(r.PostFormValue(formVariantSKU)))
	switch {
	case err == nil:
		corehttp.WriteRedirect(r.Context(), w, ProductsPath+"/"+productID+"/variants/"+id)
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.errorPage(w, r, http.StatusUnprocessableEntity, "The variant was not added", messageFor(err))
	default:
		u.unexpectedFailure(w, r, err, "The variant could not be added")
	}
}
