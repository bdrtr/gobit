package adminui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// fakeCreator is the product writer that also creates (ADR 0307).
type fakeCreator struct {
	fakeProductWriter
	created  [][2]string
	variants [][3]string
	err      error
}

func (f *fakeCreator) CreateProduct(_ context.Context, title, handle string) (string, error) {
	f.created = append(f.created, [2]string{title, handle})
	if f.err != nil {
		return "", f.err
	}

	return "prod_new", nil
}

func (f *fakeCreator) AddVariant(_ context.Context, productID, title, sku string) (string, error) {
	f.variants = append(f.variants, [3]string{productID, title, sku})
	if f.err != nil {
		return "", f.err
	}

	return "variant_new", nil
}

// creationRequest sends one request to the creation routes and the catalog's
// as an operator holding the scopes.
func creationRequest(panel *UI, method, path string, form url.Values, scopes ...string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Get(ProductsPath, panel.listProducts)
	r.Get(ProductNewPath, panel.newProduct)
	r.Post(ProductNewPath, panel.createProduct)
	r.Get(ProductPath, panel.showProduct)
	r.Post(ProductVariantsPath, panel.addVariant)

	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(),
		corehttp.Principal{ID: "user_1", Kind: "user", Scopes: scopes}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// TestThePanelCreatesADraftProduct is ADR 0307: the list links to the form for
// an operator who may write products, the form's title and handle reach the
// surface, and the operator is sent to the new product; a refusal comes back
// on the form with what was typed.
func TestThePanelCreatesADraftProduct(t *testing.T) {
	t.Parallel()

	creator := &fakeCreator{}
	panel := newEditPanel(t, editCatalog(), creator)

	list := creationRequest(panel, http.MethodGet, ProductsPath, nil, scopeProductRead, scopeProductWrite)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	assert.Contains(t, list.Body.String(), `href="`+ProductsPath+`/new"`)
	reader := creationRequest(panel, http.MethodGet, ProductsPath, nil, scopeProductRead)
	assert.NotContains(t, reader.Body.String(), `href="`+ProductsPath+`/new"`, "a reader is not offered the form")

	form := creationRequest(panel, http.MethodGet, ProductNewPath, nil, scopeProductWrite)
	require.Equal(t, http.StatusOK, form.Code, form.Body.String())
	assert.Contains(t, form.Body.String(), `action="`+ProductNewPath+`"`)

	rec := creationRequest(panel, http.MethodPost, ProductNewPath, url.Values{
		formProductTitle: {"Linen Shirt"}, formProductHandle: {""},
	}, scopeProductWrite)
	assert.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, ProductsPath+"/prod_new", rec.Header().Get("Location"))
	assert.Equal(t, [][2]string{{"Linen Shirt", ""}}, creator.created)

	refusing := newEditPanel(t, editCatalog(), &fakeCreator{err: errors.Conflict("product_handle_taken", "the handle is taken")})
	rec = creationRequest(refusing, http.MethodPost, ProductNewPath, url.Values{
		formProductTitle: {"Coffee"}, formProductHandle: {"coffee"},
	}, scopeProductWrite)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "the handle is taken")
	assert.Contains(t, rec.Body.String(), `name="handle" value="coffee"`)

	edit := newEditPanel(t, editCatalog(), &fakeProductWriter{})
	rec = creationRequest(edit, http.MethodPost, ProductNewPath, url.Values{formProductTitle: {"X"}}, scopeProductWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "a surface that cannot create says so")
}

// TestThePanelAddsAVariant is ADR 0307 on the product page: the form is drawn
// for an operator who may write products, its title and SKU reach the surface
// for the product in the path, and the operator is sent to the variant's page;
// a refusal is said.
func TestThePanelAddsAVariant(t *testing.T) {
	t.Parallel()

	creator := &fakeCreator{}
	panel := newEditPanel(t, editCatalog(), creator)

	page := creationRequest(panel, http.MethodGet, ProductsPath+"/prod_1", nil, scopeProductRead, scopeProductWrite)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), `action="`+ProductsPath+`/prod_1/variants"`)
	reader := creationRequest(panel, http.MethodGet, ProductsPath+"/prod_1", nil, scopeProductRead)
	assert.NotContains(t, reader.Body.String(), `/prod_1/variants"`)

	rec := creationRequest(panel, http.MethodPost, ProductsPath+"/prod_1/variants", url.Values{
		formVariantTitle: {"M / Red"}, formVariantSKU: {" SH-M-RED "},
	}, scopeProductWrite)
	assert.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, ProductsPath+"/prod_1/variants/variant_new", rec.Header().Get("Location"))
	assert.Equal(t, [][3]string{{"prod_1", "M / Red", "SH-M-RED"}}, creator.variants)

	refusing := newEditPanel(t, editCatalog(), &fakeCreator{err: errors.Conflict("product_sku_taken", "the SKU is taken")})
	rec = creationRequest(refusing, http.MethodPost, ProductsPath+"/prod_1/variants", url.Values{
		formVariantTitle: {"M"}, formVariantSKU: {"SH-M"},
	}, scopeProductWrite)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "the SKU is taken")
}
