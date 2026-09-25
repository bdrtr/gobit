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

// relationsCatalog is a read layer holding a coffee whose lists name a draft
// grinder before a published cup, and a published tea.
//
// The fake answers every product read with every product, so the coffee is
// first: the page takes the first record as the product it was asked for.
func relationsCatalog() *fakeCatalog {
	return &fakeCatalog{byEntity: map[string][]query.Record{
		EntityProduct: {
			{
				"id": "prod_1", "title": "Coffee", "handle": "coffee", "status": "published",
				FieldCrossSellIDs:  []string{"prod_3", "prod_2"},
				FieldUpSellIDs:     []string{},
				FieldSubstituteIDs: []string{"prod_4"},
			},
			{"id": "prod_2", "title": "Cup", "handle": "cup", "status": "published"},
			{"id": "prod_3", "title": "Grinder", "handle": "grinder", "status": "draft"},
			{"id": "prod_4", "title": "Tea", "handle": "tea", "status": "published"},
		},
	}}
}

// relationsRouter mounts the relations form beside the catalog routes.
func relationsRouter(panel *UI) chi.Router {
	r := catalogRouter(panel)
	r.Get(ProductRelationsPath, panel.editRelations)
	r.Post(ProductRelationsPath, panel.submitRelations)

	return r
}

// postRelations submits the relations form.
func postRelations(panel *UI, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, ProductsPath+"/prod_1/relations",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	relationsRouter(panel).ServeHTTP(rec, req)

	return rec
}

// TestTheProductPageListsItsRelatedProducts verifies the lists on the product
// page (ADR 0181): each in the operator's order, a product the storefront leaves
// out saying so, and the related products read in ONE call naming the fields it
// prints.
func TestTheProductPageListsItsRelatedProducts(t *testing.T) {
	t.Parallel()

	catalog := relationsCatalog()
	rec := getPage(newCatalogPanel(t, catalog), ProductsPath+"/prod_1")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	grinder, cup := strings.Index(body, ">Grinder<"), strings.Index(body, ">Cup<")
	require.Positive(t, grinder)
	require.Positive(t, cup)
	assert.Less(t, grinder, cup, "the list is in the operator's order")
	assert.Contains(t, body, "(grinder) — draft; the storefront leaves it out")
	assert.NotContains(t, body, "(cup) —", "a published product is not marked")
	assert.Contains(t, body, ">Tea<")
	assert.Contains(t, body, "<p>None.</p>", "a kind with nothing in it says so")
	assert.Contains(t, body, `href="`+ProductsPath+`/prod_1/relations"`)

	var related []query.GraphSpec
	for _, spec := range catalog.specs {
		if spec.Entity == EntityProduct && spec.Fields != nil {
			related = append(related, spec)
		}
	}
	require.Len(t, related, 1, "one read for all three lists")
	assert.Equal(t, []string{"prod_3", "prod_2", "prod_4"}, related[0].Filters[filterID])
	assert.Equal(t, []string{fieldID, fieldTitle, fieldHandle, fieldStatus}, related[0].Fields)
}

// TestAProductWithoutRelatedProductsCostsNoRead verifies the page pays for the
// related products only when there are some.
func TestAProductWithoutRelatedProductsCostsNoRead(t *testing.T) {
	t.Parallel()

	catalog := editCatalog()
	rec := getPage(newCatalogPanel(t, catalog), ProductsPath+"/prod_1")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	products := 0
	for _, spec := range catalog.specs {
		if spec.Entity == EntityProduct {
			products++
		}
	}
	assert.Equal(t, 1, products, "the product itself, and nothing for lists that are empty")
}

// TestTheRelationsFormShowsTheHandlesInOrder verifies the form opens with each
// list as one handle per line.
func TestTheRelationsFormShowsTheHandlesInOrder(t *testing.T) {
	t.Parallel()

	panel := newEditPanel(t, relationsCatalog(), &fakeProductWriter{})
	rec := httptest.NewRecorder()
	relationsRouter(panel).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, ProductsPath+"/prod_1/relations", http.NoBody))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, `<textarea name="cross_sell" rows="6">grinder`+"\n"+`cup</textarea>`)
	assert.Contains(t, body, `<textarea name="up_sell" rows="6"></textarea>`)
	assert.Contains(t, body, `<textarea name="substitute" rows="6">tea</textarea>`)
	assert.Contains(t, body, "at most 50 in each list")
}

// TestSavingTheFormSendsEveryList verifies what a save asks the module for:
// every list, one reference per line, trimmed, blank lines dropped, and an empty
// list sent as empty so the kind is taken off.
func TestSavingTheFormSendsEveryList(t *testing.T) {
	t.Parallel()

	writer := &fakeProductWriter{}
	panel := newEditPanel(t, relationsCatalog(), writer)

	rec := postRelations(panel, url.Values{
		"cross_sell": {"cup\r\n\r\n  grinder \r\n"},
		"up_sell":    {""},
		"substitute": {"prod_4"},
	})

	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, ProductsPath+"/prod_1", rec.Header().Get("Location"))
	require.Len(t, writer.related, 1)
	assert.Equal(t, map[string][]string{
		"cross_sell": {"cup", "grinder"},
		"up_sell":    {},
		"substitute": {"prod_4"},
	}, writer.related[0])
}

// TestARefusedSaveComesBackWithWhatWasTyped verifies the refusal: the form with
// the module's sentence and every list as it was typed, so the line to fix is
// on the screen.
func TestARefusedSaveComesBackWithWhatWasTyped(t *testing.T) {
	t.Parallel()

	writer := &fakeProductWriter{relatedErr: errors.Invalid("product_invalid_input", "no such product: sokcs")}
	panel := newEditPanel(t, relationsCatalog(), writer)

	rec := postRelations(panel, url.Values{
		"cross_sell": {"cup\nsokcs"},
		"up_sell":    {"tea"},
		"substitute": {""},
	})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "no such product: sokcs")
	assert.Contains(t, body, `<textarea name="cross_sell" rows="6">cup`+"\n"+`sokcs</textarea>`)
	assert.Contains(t, body, `<textarea name="up_sell" rows="6">tea</textarea>`,
		"a list that was fine comes back as typed too, not as stored")
}

// TestASaveTheModuleCannotMakeIsNotAFormError verifies the two answers that
// are not the operator's to fix on the form.
func TestASaveTheModuleCannotMakeIsNotAFormError(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		err  error
		code int
		says string
	}{
		// Deleted in another tab while this form was open: the operator's own
		// situation, said in its own words, and not logged as a server fault.
		"the product is gone": {errors.NotFound("product_not_found", "gone"), http.StatusNotFound,
			"There is no product with that id."},
		"the database is not there": {errors.Unavailable("db_down", "down"), http.StatusServiceUnavailable,
			"The reason is in the server log."},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			panel := newEditPanel(t, relationsCatalog(), &fakeProductWriter{relatedErr: tc.err})
			rec := postRelations(panel, url.Values{"cross_sell": {"cup"}})

			assert.Equal(t, tc.code, rec.Code)
			assert.Contains(t, rec.Body.String(), tc.says)
			assert.NotContains(t, rec.Body.String(), "<textarea")
		})
	}
}
