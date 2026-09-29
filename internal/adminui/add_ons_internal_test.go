package adminui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// addOnsCatalog is a read layer holding a ring whose add-ons are a wrap without
// a SKU and then a draft engraving. The variants are answered in the other
// order, so the page's order has to come from the list.
func addOnsCatalog() *fakeCatalog {
	return &fakeCatalog{
		byEntity: map[string][]query.Record{
			EntityProduct: {
				{
					"id": "prod_1", "title": "Ring", "handle": "ring", "status": "published",
					FieldAddOnVariantIDs: []string{"variant_wrap", "variant_engraving"},
				},
				{"id": "prod_2", "title": "Gift wrap", "handle": "wrap", "status": "published"},
				{"id": "prod_3", "title": "Engraving", "handle": "engraving", "status": "draft"},
			},
		},
		answer: func(spec query.GraphSpec) ([]query.Record, error, bool) {
			if spec.Entity != EntityVariant {
				return nil, nil, false
			}
			if _, byID := spec.Filters[filterID]; !byID {
				return []query.Record{}, nil, true
			}
			return []query.Record{
				{"id": "variant_engraving", "title": "Script", "sku": "ENG-1", "product_id": "prod_3"},
				{"id": "variant_wrap", "title": "Red", "product_id": "prod_2"},
			}, nil, true
		},
	}
}

// addOnsRouter mounts the add-ons form beside the catalog routes.
func addOnsRouter(panel *UI) chi.Router {
	r := catalogRouter(panel)
	r.Get(ProductAddOnsPath, panel.editAddOns)
	r.Post(ProductAddOnsPath, panel.submitAddOns)
	return r
}

// postAddOns submits the add-ons form.
func postAddOns(panel *UI, typed string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, ProductsPath+"/prod_1/add-ons",
		strings.NewReader(url.Values{"add_ons": {typed}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	addOnsRouter(panel).ServeHTTP(rec, req)
	return rec
}

// TestTheProductPageListsItsAddOns is ADR 0232 on the product page: the add-ons
// in the operator's order, each with its product and variant, an add-on the
// storefront leaves out saying so, the variants read in ONE call and their
// products in another.
func TestTheProductPageListsItsAddOns(t *testing.T) {
	t.Parallel()

	catalog := addOnsCatalog()
	rec := getPage(newCatalogPanel(t, catalog), ProductsPath+"/prod_1")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	wrap, engraving := strings.Index(body, ">Gift wrap<"), strings.Index(body, ">Engraving<")
	require.Positive(t, wrap)
	require.Positive(t, engraving)
	assert.Less(t, wrap, engraving, "the list is in the operator's order")
	assert.Contains(t, body, "— Script (ENG-1) — draft; the storefront leaves it out")
	assert.Contains(t, body, "— Red</li>", "a published add-on without a SKU is not marked")
	assert.Contains(t, body, `href="`+ProductsPath+`/prod_1/add-ons"`)

	var variants, products []query.GraphSpec
	for _, spec := range catalog.specs {
		switch {
		case spec.Entity == EntityVariant && spec.Filters[filterID] != nil:
			variants = append(variants, spec)
		case spec.Entity == EntityProduct && spec.Fields != nil && len(spec.Fields) == 3:
			products = append(products, spec)
		}
	}
	require.Len(t, variants, 1, "one read for every add-on variant")
	assert.Equal(t, []string{"variant_wrap", "variant_engraving"}, variants[0].Filters[filterID])
	require.Len(t, products, 1, "one read for their products")
	assert.ElementsMatch(t, []string{"prod_3", "prod_2"}, products[0].Filters[filterID])
}

// TestTheAddOnsFormShowsTheReferencesInOrder holds the form's value: each
// add-on by its SKU, or its id when it carries none, in the list's order.
func TestTheAddOnsFormShowsTheReferencesInOrder(t *testing.T) {
	t.Parallel()

	panel := newEditPanel(t, addOnsCatalog(), &fakeProductWriter{})
	rec := httptest.NewRecorder()
	addOnsRouter(panel).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, ProductsPath+"/prod_1/add-ons", http.NoBody))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, `<textarea name="add_ons" rows="8">variant_wrap`+"\n"+`ENG-1</textarea>`)
	assert.Contains(t, body, "at most 20")
}

// TestSavingTheAddOnsSendsEveryLine holds what a save asks the module for:
// one reference per line, trimmed, blank lines dropped.
func TestSavingTheAddOnsSendsEveryLine(t *testing.T) {
	t.Parallel()

	writer := &fakeProductWriter{}
	rec := postAddOns(newEditPanel(t, addOnsCatalog(), writer), "ENG-1\r\n\r\n  variant_wrap \r\n")

	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, ProductsPath+"/prod_1", rec.Header().Get("Location"))
	assert.Equal(t, [][]string{{"ENG-1", "variant_wrap"}}, writer.addOns)
}

// TestARefusedAddOnSaveComesBackWithWhatWasTyped holds the refusal: the form
// with the module's sentence and the list as it was typed.
func TestARefusedAddOnSaveComesBackWithWhatWasTyped(t *testing.T) {
	t.Parallel()

	writer := &fakeProductWriter{addOnsErr: errors.Invalid("product_invalid_input", "no variant has the SKU: ENG-9")}
	rec := postAddOns(newEditPanel(t, addOnsCatalog(), writer), "ENG-9\nvariant_wrap")

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "no variant has the SKU: ENG-9")
	assert.Contains(t, body, `<textarea name="add_ons" rows="8">ENG-9`+"\n"+`variant_wrap</textarea>`)
}

// TestTheAddOnsCannotBeSavedWithoutTheModule holds the answer when the write
// surface is not registered.
func TestTheAddOnsCannotBeSavedWithoutTheModule(t *testing.T) {
	t.Parallel()

	rec := postAddOns(newCatalogPanel(t, addOnsCatalog()), "ENG-1")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
