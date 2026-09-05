package adminui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// fakeCatalog is the read layer's in-memory stand-in.
//
// It answers PER ENTITY, because the catalog screens make more than one call
// and a fake with a single canned answer would let a screen read the wrong
// entity without the test noticing.
type fakeCatalog struct {
	byEntity map[string][]query.Record
	err      error
	// errByEntity fails ONE entity's read while the others still answer.
	//
	// It exists because the catalog screens make more than one call and the
	// interesting failures are the PARTIAL ones: the product list read
	// succeeding while the category vocabulary behind its filter does not is a
	// state the panel has to survive, and a fake that could only fail
	// everything at once could not produce it.
	errByEntity map[string]error

	specs []query.GraphSpec
}

func (f *fakeCatalog) Graph(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
	// The spec is recorded BEFORE the failure is applied, so a test can prove
	// what a failing read was asked for. Recording it afterwards would make the
	// filter carried by a doomed call invisible.
	f.specs = append(f.specs, spec)
	if f.err != nil {
		return nil, f.err
	}
	if err := f.errByEntity[spec.Entity]; err != nil {
		return nil, err
	}

	return f.byEntity[spec.Entity], nil
}

// specFor returns the recorded spec for an entity, or false.
func (f *fakeCatalog) specFor(entity string) (query.GraphSpec, bool) {
	for _, spec := range f.specs {
		if spec.Entity == entity {
			return spec, true
		}
	}

	return query.GraphSpec{}, false
}

// newCatalogPanel builds a panel wired to the given read layer.
func newCatalogPanel(t *testing.T, catalog Catalog) *UI {
	t.Helper()

	templates, err := loadTemplates()
	require.NoError(t, err)

	return &UI{catalog: catalog, templates: templates}
}

// catalogRouter mounts the catalog routes so chi fills the URL parameters.
//
// The handler reads the product id with chi.URLParam, which is empty unless the
// request went through a router. Calling the handler directly would exercise a
// path no real request takes.
func catalogRouter(panel *UI) chi.Router {
	r := chi.NewRouter()
	r.Get(ProductsPath, panel.listProducts)
	r.Get(ProductPath, panel.showProduct)

	return r
}

// getPage sends a GET and returns the recorder.
func getPage(panel *UI, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	catalogRouter(panel).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))

	return rec
}

// TestProductListRendersRows proves the list reads through the read layer and
// prints what it got.
func TestProductListRendersRows(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityProduct: {
			{"id": "prod_1", "title": "Coffee", "handle": "coffee", "status": "published",
				"updated_at": time.Date(2026, 9, 3, 10, 30, 0, 0, time.UTC)},
		},
	}}

	rec := getPage(newCatalogPanel(t, catalog), ProductsPath)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "Coffee")
	assert.Contains(t, body, "coffee")
	assert.Contains(t, body, "published")
	assert.Contains(t, body, "2026-09-03 10:30")
	assert.Contains(t, body, ProductsPath+"/prod_1", "the title must link to the product page")
}

// TestProductListEscapesOperatorText proves a product title is escaped.
//
// The panel runs inside an ADMINISTRATOR's session and a product title is text
// somebody typed. An XSS here hands the attacker admin privileges, so the
// assertion has the same two halves as the login page's: no raw tag, and no
// ZgotmplZ either — the engine prints that marker instead of failing when it
// cannot resolve a context, which makes escaping LOOK like it worked while the
// data silently disappears.
func TestProductListEscapesOperatorText(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityProduct: {{"id": "p1", "title": `<script>alert('admin')</script>`}},
	}}

	rec := getPage(newCatalogPanel(t, catalog), ProductsPath)
	body := rec.Body.String()

	assert.NotContains(t, body, "<script>", "a title must not be printed as a raw tag")
	assert.Contains(t, body, "&lt;script&gt;", "the title must be escaped, not dropped")
	assert.NotContains(t, body, "ZgotmplZ",
		"the engine failed to resolve a context: escaping LOOKS like it worked but the "+
			"data is silently removed")
}

// TestProductListPagesWithoutCounting proves the "next page" answer comes from
// one extra record rather than from a count.
//
// The screen asks for one record more than it shows. That extra record is the
// whole paging mechanism: without it the answer would need a COUNT over the
// catalog, which is the one table that always grows.
func TestProductListPagesWithoutCounting(t *testing.T) {
	t.Parallel()

	full := make([]query.Record, 0, productsPerPage+1)
	for i := range productsPerPage + 1 {
		full = append(full, query.Record{"id": "p" + string(rune('a'+i%26)), "title": "T"})
	}

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{EntityProduct: full}}
	rec := getPage(newCatalogPanel(t, catalog), ProductsPath+"?page=2")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "page=3", "the next link must be offered")
	assert.Contains(t, rec.Body.String(), "page=1", "the previous link must be offered")

	spec, ok := catalog.specFor(EntityProduct)
	require.True(t, ok)
	assert.Equal(t, productsPerPage+1, spec.Limit,
		"one record more than the page holds must be requested; that extra record IS the "+
			"answer to \"is there a next page\"")
	assert.Equal(t, productsPerPage, spec.Offset, "page 2 must skip one page")
}

// TestProductListRejectsNothingForABadPage proves an unreadable page number
// falls back to page one instead of erroring.
//
// The address bar is edited by hand; answering "?page=abc" with an error page
// would be louder than the mistake, and the fallback is obvious on screen.
func TestProductListRejectsNothingForABadPage(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{EntityProduct: {}}}
	rec := getPage(newCatalogPanel(t, catalog), ProductsPath+"?page=abc")

	require.Equal(t, http.StatusOK, rec.Code)

	spec, ok := catalog.specFor(EntityProduct)
	require.True(t, ok)
	assert.Equal(t, 0, spec.Offset, "an unreadable page must land on the first page")
}

// categoryVocabulary is the small taxonomy the filter tests choose from.
func categoryVocabulary() []query.Record {
	return []query.Record{
		{"id": "pcat_beans", "name": "Beans"},
		{"id": "pcat_mugs", "name": "Mugs"},
	}
}

// categoryCatalog is a read layer holding one product and a category
// vocabulary.
func categoryCatalog(categories []query.Record) *fakeCatalog {
	return &fakeCatalog{byEntity: map[string][]query.Record{
		EntityProduct: {
			{"id": "prod_1", "title": "Coffee", "handle": "coffee", "status": "published"},
		},
		EntityCategory: categories,
	}}
}

// TestTheChosenCategoryReachesTheProductSpec proves the dropdown's value ends
// up in the read layer's filter, under the name the provider answers to.
//
// The value is a bare STRING and not a one-element slice, which is the shape
// the provider's scalar filters take; the list-shaped filters are the identity
// ones. Passing the wrong shape is refused rather than ignored, but the refusal
// arrives on the first request, which is the whole reason this assertion is
// here and not left to a running server.
func TestTheChosenCategoryReachesTheProductSpec(t *testing.T) {
	t.Parallel()

	catalog := categoryCatalog(categoryVocabulary())

	rec := getPage(newCatalogPanel(t, catalog), ProductsPath+"?category=pcat_beans")

	require.Equal(t, http.StatusOK, rec.Code)

	spec, ok := catalog.specFor(EntityProduct)
	require.True(t, ok, "the product entity must be read")
	assert.Equal(t, map[string]any{filterCategoryID: "pcat_beans"}, spec.Filters,
		"the chosen category must reach the read layer under the provider's own filter name")
	assert.Equal(t, "category_id", filterCategoryID,
		"the panel must spell the filter the way the storefront does; one vocabulary "+
			"across the two surfaces is what keeps the two narrowings comparable")
}

// TestAnEmptyCategoryIsNotAFilter proves an absent or blank choice leaves the
// spec unfiltered.
//
// This is the class the product module named on its own list road
// (TestEmptyTextArgumentBuildsNoFilter in internal/modules/product/graph), and
// the cost is SILENT: an empty identity is a perfectly valid filter value that
// nothing matches, so the screen would answer with an empty table that reads as
// an empty catalog. It is not a hypothetical shape here — the dropdown's "All
// categories" entry submits exactly this, so the unfiltered path of this screen
// runs through the empty string on every use.
func TestAnEmptyCategoryIsNotAFilter(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"no parameter at all": ProductsPath,
		"present and empty":   ProductsPath + "?category=",
		"only whitespace":     ProductsPath + "?category=%20%20%20",
	}

	for name, path := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			catalog := categoryCatalog(categoryVocabulary())
			// No products, so the empty-list sentence is on the page and can be
			// read: the point is that an unfiltered empty list must not be
			// explained by a category nobody chose.
			catalog.byEntity[EntityProduct] = nil

			rec := getPage(newCatalogPanel(t, catalog), path)

			require.Equal(t, http.StatusOK, rec.Code)

			spec, ok := catalog.specFor(EntityProduct)
			require.True(t, ok)
			assert.Empty(t, spec.Filters,
				"an empty choice must leave the spec UNFILTERED; passing \"\" through "+
					"filters by an identity nothing has and empties the catalog silently")

			body := rec.Body.String()
			assert.Contains(t, body, "No products on this page.",
				"an unfiltered empty page must not blame a category")
			assert.NotContains(t, body, "show all products",
				"there is nothing to clear when no category is applied")
		})
	}
}

// TestTheCategoryVocabularyIsReadUnderItsOwnName pins the second call.
//
// The dropdown is filled by a SECOND Graph call rather than by a field on the
// product record, and that is the same two-call shape the product page uses:
// the read layer joins across LINKS, and a category is not linked to the panel's
// product listing. What is pinned here is the entity name, the two fields, and
// the cap — one record more than the dropdown offers, because that extra record
// is the only thing that tells the screen whether the vocabulary it holds is
// the whole vocabulary.
func TestTheCategoryVocabularyIsReadUnderItsOwnName(t *testing.T) {
	t.Parallel()

	catalog := categoryCatalog(categoryVocabulary())

	getPage(newCatalogPanel(t, catalog), ProductsPath)

	spec, ok := catalog.specFor(EntityCategory)
	require.True(t, ok, "the category entity must be read to fill the filter")
	assert.Equal(t, EntityCategory, spec.Entity)
	assert.ElementsMatch(t, []string{fieldID, fieldName}, spec.Fields,
		"only the identity and the name are needed; a wider read would cost more for "+
			"nothing the control shows")
	assert.Equal(t, categoriesInFilter+1, spec.Limit,
		"one entry more than the dropdown offers must be requested; that extra entry IS "+
			"the answer to \"is this the whole vocabulary\"")
	assert.Empty(t, spec.Filters,
		"the panel is an ADMIN surface: hiding inactive or internal categories here "+
			"would hide from the operator exactly the ones they maintain")
}

// TestTheCategoryDropdownRendersTheVocabularyAndMarksTheChoice proves the
// control shows what was read and agrees with the list under it.
func TestTheCategoryDropdownRendersTheVocabularyAndMarksTheChoice(t *testing.T) {
	t.Parallel()

	panel := newCatalogPanel(t, categoryCatalog(categoryVocabulary()))

	body := getPage(panel, ProductsPath+"?category=pcat_mugs").Body.String()

	assert.Contains(t, body, `<option value="">All categories</option>`,
		"the way back to the whole catalog must be in the control itself")
	assert.Contains(t, body, `<option value="pcat_beans">Beans</option>`)
	assert.Contains(t, body, `<option value="pcat_mugs" selected>Mugs</option>`,
		"the applied category must be the SELECTED option; a control showing "+
			"\"All categories\" over a filtered list would lie about what is on screen")
	assert.Contains(t, body, "Narrowed to <strong>Mugs</strong>",
		"the applied category must be named from the vocabulary already in hand, "+
			"rather than by asking the product provider for a category field")
	assert.NotContains(t, body, "pcat_mugs</strong>",
		"the operator is shown the NAME; the identifier is what travels in the address")
}

// TestThePagingLinksCarryTheCategory proves "next" stays inside the filter.
//
// A paging link that dropped the category would walk the operator out of the
// narrowed view into the whole catalog while the heading still said the same
// thing — the failure the sales report avoids by carrying its period.
func TestThePagingLinksCarryTheCategory(t *testing.T) {
	t.Parallel()

	full := make([]query.Record, 0, productsPerPage+1)
	for i := range productsPerPage + 1 {
		full = append(full, query.Record{"id": "p" + strconv.Itoa(i), "title": "T"})
	}

	catalog := categoryCatalog(categoryVocabulary())
	catalog.byEntity[EntityProduct] = full

	body := getPage(newCatalogPanel(t, catalog), ProductsPath+"?page=2&category=pcat_beans").Body.String()

	assert.Contains(t, body, "page=3&amp;category=pcat_beans",
		"the next link must keep the filter")
	assert.Contains(t, body, "page=1&amp;category=pcat_beans",
		"and so must the previous one")
}

// TestUnfilteredPagingLinksCarryNoCategory proves the parameter is appended
// only when there is one.
//
// The sales report always covers some window and so always appends its two
// dates; this list is unfiltered most of the time, and a trailing "&category="
// on every link would be noise in an address bar operators read and paste.
func TestUnfilteredPagingLinksCarryNoCategory(t *testing.T) {
	t.Parallel()

	full := make([]query.Record, 0, productsPerPage+1)
	for i := range productsPerPage + 1 {
		full = append(full, query.Record{"id": "p" + strconv.Itoa(i), "title": "T"})
	}

	catalog := categoryCatalog(categoryVocabulary())
	catalog.byEntity[EntityProduct] = full

	body := getPage(newCatalogPanel(t, catalog), ProductsPath).Body.String()

	assert.Contains(t, body, "page=2", "the next link must still be offered")
	assert.NotContains(t, body, "category=\"",
		"an unfiltered list must not append an empty category to its links")
	assert.NotContains(t, body, "&amp;category=")
}

// TestTheCatalogSurvivesAnUnreadableCategoryList is the partial failure.
//
// The products were read and are correct; only the control that narrows them
// could not be built. Answering the whole request with "Catalog unavailable"
// would take the catalog away from the operator because a dropdown could not be
// filled — the page is worth more than the control.
//
// The second half is the one that matters more. A control that vanished
// SILENTLY while a category was still applied would leave a short list under a
// plain "Products" heading with nothing on the page saying it had been
// narrowed, and the operator would read those rows as the whole catalog. So the
// missing control is stated, the applied identifier is named, and a way back to
// the full list is offered — without the dropdown there is otherwise no way to
// clear the filter but editing the address.
func TestTheCatalogSurvivesAnUnreadableCategoryList(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		path     string
		contains []string
	}{
		"nothing is applied": {
			path: ProductsPath,
			contains: []string{
				"The category list could not be read",
			},
		},
		"a category is applied": {
			path: ProductsPath + "?category=pcat_beans",
			contains: []string{
				"The category list could not be read",
				"pcat_beans",
				"show all products",
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			catalog := categoryCatalog(categoryVocabulary())
			catalog.errByEntity = map[string]error{
				EntityCategory: errors.NotFound("query_provider_missing", "category.query was not found"),
			}

			rec := getPage(newCatalogPanel(t, catalog), tt.path)

			require.Equal(t, http.StatusOK, rec.Code,
				"a page must not die because a dropdown could not be filled")
			body := rec.Body.String()
			assert.Contains(t, body, "Coffee", "the products that WERE read must still be shown")
			assert.NotContains(t, body, "<select",
				"a dropdown with no vocabulary behind it must be absent, not empty")
			assert.NotContains(t, body, "category.query was not found",
				"the underlying error must not reach the page")
			for _, want := range tt.contains {
				assert.Contains(t, body, want)
			}
		})
	}

	t.Run("the filter is still applied", func(t *testing.T) {
		t.Parallel()

		catalog := categoryCatalog(categoryVocabulary())
		catalog.errByEntity = map[string]error{
			EntityCategory: errors.NotFound("query_provider_missing", "category.query was not found"),
		}

		getPage(newCatalogPanel(t, catalog), ProductsPath+"?category=pcat_beans")

		spec, ok := catalog.specFor(EntityProduct)
		require.True(t, ok)
		assert.Equal(t, map[string]any{filterCategoryID: "pcat_beans"}, spec.Filters,
			"an unreadable vocabulary must not quietly widen the listing; the rows on "+
				"screen have to be the ones the address asked for")
	})
}

// TestAStaleCategoryDoesNotLookLikeAnEmptyCatalog is the bookmark whose
// category was deleted.
//
// The filter is applied and matches nothing. Without a word on the page that is
// an empty table under a heading that says "Products", which an operator reads
// as "this shop has no products" — a confidently wrong answer, and the one
// thing they would not then check is the control that emptied it.
func TestAStaleCategoryDoesNotLookLikeAnEmptyCatalog(t *testing.T) {
	t.Parallel()

	catalog := categoryCatalog(categoryVocabulary())
	catalog.byEntity[EntityProduct] = nil

	rec := getPage(newCatalogPanel(t, catalog), ProductsPath+"?category=pcat_gone")

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "No category is known by the identifier",
		"the screen must say that the identifier is unknown")
	assert.Contains(t, body, "pcat_gone")
	assert.Contains(t, body, "show all products", "a way out of the dead filter must be offered")
	assert.NotContains(t, body, "No products on this page.",
		"the empty table must not be explained as an empty catalog")
	assert.Contains(t, body, `<option value="pcat_gone" selected>`,
		"the unknown identifier must still be the SELECTED option; a <select> whose "+
			"value matches no option renders its first one, which here says "+
			"\"All categories\" over a filtered list")
}

// TestAnUncheckableCategoryIsNotCalledUnknown is the same screen with a
// vocabulary too large to hold.
//
// The dropdown is capped, so an identifier missing from it may simply sit past
// the cap. Saying "no such category" there would be a confident false statement
// about a category that exists — the same defect class the stale case exists to
// avoid, made by the fix for it.
func TestAnUncheckableCategoryIsNotCalledUnknown(t *testing.T) {
	t.Parallel()

	many := make([]query.Record, 0, categoriesInFilter+1)
	for i := range categoriesInFilter + 1 {
		many = append(many, query.Record{
			"id": "pcat_" + strconv.Itoa(i), "name": "Category " + strconv.Itoa(i),
		})
	}

	catalog := categoryCatalog(many)
	catalog.byEntity[EntityProduct] = nil

	rec := getPage(newCatalogPanel(t, catalog), ProductsPath+"?category=pcat_past_the_cap")

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "could not be checked against it",
		"a truncated vocabulary can only report that it does not know")
	assert.NotContains(t, body, "No category is known by the identifier",
		"an identifier past the cap must not be declared nonexistent")
	assert.NotContains(t, body, `value="pcat_`+strconv.Itoa(categoriesInFilter)+`"`,
		"the extra entry read to detect truncation must not be offered in the control")
}

// TestCategoryFilterOfKeepsItsThreeFailuresApart is the control's state table.
//
// The three are different facts and each earns a different sentence, so the
// screen must never be able to show two at once. Making them exclusive in the
// pure function rather than in template branches is what makes that impossible
// instead of merely unlikely.
func TestCategoryFilterOfKeepsItsThreeFailuresApart(t *testing.T) {
	t.Parallel()

	known := []categoryOption{{ID: "pcat_beans", Name: "Beans"}}

	tests := map[string]struct {
		chosen string
		list   categoryList
		want   categoryFilter
	}{
		"nothing chosen": {
			list: categoryList{Options: known},
			want: categoryFilter{Options: known},
		},
		"a known category": {
			chosen: "pcat_beans",
			list:   categoryList{Options: known},
			want: categoryFilter{
				ID:      "pcat_beans",
				Name:    "Beans",
				Options: []categoryOption{{ID: "pcat_beans", Name: "Beans", Selected: true}},
			},
		},
		"an unknown category in a complete vocabulary": {
			chosen: "pcat_gone",
			list:   categoryList{Options: known},
			want: categoryFilter{
				ID: "pcat_gone",
				Options: []categoryOption{
					{ID: "pcat_beans", Name: "Beans"},
					{ID: "pcat_gone", Name: "pcat_gone", Selected: true},
				},
				Unknown: true,
			},
		},
		"an unlisted category in a truncated vocabulary": {
			chosen: "pcat_gone",
			list:   categoryList{Options: known, Truncated: true},
			want: categoryFilter{
				ID: "pcat_gone",
				Options: []categoryOption{
					{ID: "pcat_beans", Name: "Beans"},
					{ID: "pcat_gone", Name: "pcat_gone", Selected: true},
				},
				Unverified: true,
			},
		},
		"no vocabulary at all": {
			chosen: "pcat_gone",
			list:   categoryList{Unavailable: true},
			want:   categoryFilter{ID: "pcat_gone", Unavailable: true},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The options are cloned per case because the function marks the
			// chosen one IN PLACE; a shared slice would let one case's
			// selection leak into the next and pass for the wrong reason.
			list := tt.list
			list.Options = slices.Clone(list.Options)

			got := categoryFilterOf(tt.chosen, list)

			assert.Equal(t, tt.want, got)
			exclusive := 0
			for _, flag := range []bool{got.Unavailable, got.Unknown, got.Unverified} {
				if flag {
					exclusive++
				}
			}
			assert.LessOrEqual(t, exclusive, 1,
				"at most ONE of the three failing states may be set; two would put two "+
					"contradictory sentences on the same screen")
		})
	}
}

// TestTheCategoryNameIsEscaped proves a category name cannot carry markup.
//
// A category name is text an operator typed, it is printed inside an <option>
// and inside a <strong>, and this panel runs inside an administrator's session.
// The assertion has the same two halves as the product title's: no raw tag, and
// no ZgotmplZ either — the engine prints that marker instead of failing when it
// cannot resolve a context, which makes escaping LOOK like it worked while the
// data silently disappears.
func TestTheCategoryNameIsEscaped(t *testing.T) {
	t.Parallel()

	catalog := categoryCatalog([]query.Record{
		{"id": "pcat_x", "name": `<script>alert('admin')</script>`},
	})

	body := getPage(newCatalogPanel(t, catalog), ProductsPath+"?category=pcat_x").Body.String()

	assert.NotContains(t, body, "<script>", "a category name must not be printed as a raw tag")
	assert.Contains(t, body, "&lt;script&gt;", "the name must be escaped, not dropped")
	assert.NotContains(t, body, "ZgotmplZ",
		"the engine failed to resolve a context: escaping LOOKS like it worked but the "+
			"data is silently removed")
}

// TestTheTypedSearchReachesTheProductSpec proves the box's text ends up in the
// read layer's filter, under the name the provider answers to.
//
// The name is the storefront's "q" and not the panel's own "search", and the
// assertion pins the string itself: the two surfaces of one installation ask
// one question, and the day they stop spelling it the same way the panel is
// the side that has to move. The value is a bare STRING, the shape the
// provider's scalar filters take.
func TestTheTypedSearchReachesTheProductSpec(t *testing.T) {
	t.Parallel()

	catalog := categoryCatalog(categoryVocabulary())

	rec := getPage(newCatalogPanel(t, catalog), ProductsPath+"?search=coffee")

	require.Equal(t, http.StatusOK, rec.Code)

	spec, ok := catalog.specFor(EntityProduct)
	require.True(t, ok, "the product entity must be read")
	assert.Equal(t, map[string]any{filterSearch: "coffee"}, spec.Filters,
		"the typed text must reach the read layer under the provider's own filter name")
	assert.Equal(t, "q", filterSearch,
		"the panel must spell the search filter the way the storefront does; one "+
			"vocabulary across the two surfaces is what keeps the two searches comparable")
}

// TestTheSearchTermIsTrimmedBeforeItIsUsed proves the applied term and the term
// on screen are the same string.
//
// Trimming only the filter would leave the box holding text that no longer
// describes what was applied; trimming only the box would filter by a term with
// spaces around it, which the provider matches against titles that have none.
func TestTheSearchTermIsTrimmedBeforeItIsUsed(t *testing.T) {
	t.Parallel()

	catalog := categoryCatalog(categoryVocabulary())

	body := getPage(newCatalogPanel(t, catalog), ProductsPath+"?search=%20coffee%20").Body.String()

	spec, ok := catalog.specFor(EntityProduct)
	require.True(t, ok)
	assert.Equal(t, map[string]any{filterSearch: "coffee"}, spec.Filters,
		"the surrounding spaces must not travel into the filter")
	assert.Contains(t, body, `value="coffee"`,
		"the box must be refilled with the term that was actually applied")
}

// TestAnEmptySearchIsNotAFilter proves an absent or blank box leaves the spec
// unfiltered.
//
// It is the sibling of [TestAnEmptyCategoryIsNotAFilter] and the class is the
// same one the product module named on its own list road
// (TestEmptyTextArgumentBuildsNoFilter in internal/modules/product/graph), but
// the failure it prevents is the OPPOSITE shape. An empty category matches
// nothing; an empty term narrows nothing at all — the provider normalizes a
// blank one away and the "title ILIKE '%…%'" underneath it matches every title
// regardless. So the rows would be the whole catalog while the screen said it
// was searching: every paging link would carry a filter nobody asked for, and
// an empty page would be blamed on a search that was never typed. It is not a
// hypothetical shape either — an untouched box submits exactly this on every
// single use of the form.
//
// What is asserted is the SPEC this screen built and the sentence it printed,
// never the rows that came back. The read layer refusing to act on a blank term
// is its own rule and a good one, and it is exactly what would hide this bug
// from the rows: the list would look right while the screen around it claimed a
// narrowing that never left this package.
func TestAnEmptySearchIsNotAFilter(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"no parameter at all": ProductsPath,
		"present and empty":   ProductsPath + "?search=",
		"only whitespace":     ProductsPath + "?search=%20%20%20",
		"a tab and a newline": ProductsPath + "?search=%09%0A",
	}

	for name, path := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			catalog := categoryCatalog(categoryVocabulary())
			// No products, so the empty-list sentence is on the page and can be
			// read: the point is that an unsearched empty list must not be
			// explained by a search nobody made.
			catalog.byEntity[EntityProduct] = nil

			rec := getPage(newCatalogPanel(t, catalog), path)

			require.Equal(t, http.StatusOK, rec.Code)

			spec, ok := catalog.specFor(EntityProduct)
			require.True(t, ok)
			assert.Empty(t, spec.Filters,
				"a blank box must leave the spec UNFILTERED; passing \"\" through matches "+
					"every title and narrows nothing while the screen claims it does")

			body := rec.Body.String()
			assert.Contains(t, body, "No products on this page.",
				"an unsearched empty page must not blame a search")
			assert.NotContains(t, body, "clear the search",
				"there is nothing to clear when no search is applied")
		})
	}
}

// TestASearchInsideACategoryProducesBothFilters is the composition.
//
// The two narrowings are one GET form for exactly this: an operator looking for
// one product inside a category they have already chosen. Two filter keys have
// to arrive in ONE spec — a search that REPLACED the spec's filters instead of
// joining them would drop the category and answer with a wider catalog than the
// address describes, at 200, with the dropdown still showing the category.
func TestASearchInsideACategoryProducesBothFilters(t *testing.T) {
	t.Parallel()

	catalog := categoryCatalog(categoryVocabulary())

	rec := getPage(newCatalogPanel(t, catalog), ProductsPath+"?category=pcat_beans&search=coffee")

	require.Equal(t, http.StatusOK, rec.Code)

	spec, ok := catalog.specFor(EntityProduct)
	require.True(t, ok)
	assert.Equal(t, map[string]any{filterCategoryID: "pcat_beans", filterSearch: "coffee"},
		spec.Filters, "both narrowings must reach the read layer in ONE spec")

	body := rec.Body.String()
	assert.Contains(t, body, `<option value="pcat_beans" selected>Beans</option>`,
		"the category must still be the selected option while a search is applied")
	assert.Contains(t, body, `value="coffee"`,
		"and the box must still hold the term while a category is applied")
}

// TestProductFiltersLeavesOutWhatIsEmpty is the filter map's state table.
//
// The rule is one sentence — an empty value is not a filter — and it has to
// hold for each key alone and for the two together, which is four states. The
// nil result is asserted rather than merely "empty": the read layer treats a
// nil and an empty map alike, but this package's own tests read the spec back,
// and "the screen sent no filters at all" is the fact they turn on.
func TestProductFiltersLeavesOutWhatIsEmpty(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		category string
		search   string
		want     map[string]any
	}{
		"neither":            {want: nil},
		"only a category":    {category: "pcat_beans", want: map[string]any{filterCategoryID: "pcat_beans"}},
		"only a search":      {search: "coffee", want: map[string]any{filterSearch: "coffee"}},
		"an empty search":    {category: "pcat_beans", search: "", want: map[string]any{filterCategoryID: "pcat_beans"}},
		"an empty category":  {category: "", search: "coffee", want: map[string]any{filterSearch: "coffee"}},
		"both of them given": {category: "pcat_beans", search: "coffee", want: map[string]any{filterCategoryID: "pcat_beans", filterSearch: "coffee"}},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, productFilters(tt.category, tt.search))
		})
	}
}

// TestThePagingLinksCarryBothNarrowings proves "next" stays inside whatever the
// screen is showing.
//
// A link that dropped either one would walk the operator into a wider catalog
// while the heading, the box and the dropdown all still said the same thing.
// The term is one with a SPACE in it on purpose: it has to arrive in the link
// percent-encoded, or the address breaks at the first blank and the next page
// searches for half of what was typed.
func TestThePagingLinksCarryBothNarrowings(t *testing.T) {
	t.Parallel()

	full := make([]query.Record, 0, productsPerPage+1)
	for i := range productsPerPage + 1 {
		full = append(full, query.Record{"id": "p" + strconv.Itoa(i), "title": "T"})
	}

	catalog := categoryCatalog(categoryVocabulary())
	catalog.byEntity[EntityProduct] = full

	body := getPage(newCatalogPanel(t, catalog),
		ProductsPath+"?page=2&category=pcat_beans&search=cold%20brew").Body.String()

	assert.Contains(t, body, "page=3&amp;category=pcat_beans&amp;search=cold%20brew",
		"the next link must keep BOTH narrowings, and the term must be escaped")
	assert.Contains(t, body, "page=1&amp;category=pcat_beans&amp;search=cold%20brew",
		"and so must the previous one")
}

// TestPagingLinksCarryOnlyWhatIsApplied proves a searched but uncategorized
// list appends the search alone.
//
// The mirror of [TestUnfilteredPagingLinksCarryNoCategory]: the address bar is
// read and pasted by operators, and a trailing "&category=" on every link of a
// list nobody categorized is noise that also looks like a filter.
func TestPagingLinksCarryOnlyWhatIsApplied(t *testing.T) {
	t.Parallel()

	full := make([]query.Record, 0, productsPerPage+1)
	for i := range productsPerPage + 1 {
		full = append(full, query.Record{"id": "p" + strconv.Itoa(i), "title": "T"})
	}

	catalog := categoryCatalog(categoryVocabulary())
	catalog.byEntity[EntityProduct] = full

	body := getPage(newCatalogPanel(t, catalog), ProductsPath+"?search=coffee").Body.String()

	assert.Contains(t, body, "page=2&amp;search=coffee", "the search must be carried")
	assert.NotContains(t, body, "&amp;category=",
		"a list narrowed by nothing but a search must not append an empty category")
}

// TestTheEmptyListNamesWhatEmptiedIt is the four-sentence table.
//
// Two independent narrowings make four states and each of them earns its own
// sentence. Getting this wrong is how an operator concludes their catalog is
// broken: "No products on this page" under a search is true and useless — it is
// the sentence read as "this shop is empty", and the control that emptied it is
// the one thing they would not then check. The reverse mistake costs as much;
// blaming the category when the search is what matched nothing sends somebody
// to repair a taxonomy that is fine.
func TestTheEmptyListNamesWhatEmptiedIt(t *testing.T) {
	t.Parallel()

	const (
		plain         = "No products on this page."
		inCategory    = "No products in this category on this page."
		searched      = "Nothing matched that search."
		searchedInCat = "Nothing matched that search in this category."
	)

	// The four are asserted against each OTHER, not only for their own case:
	// three of these sentences are near-prefixes of another, so a template that
	// fell through two branches would still contain the right one.
	sentences := []string{plain, inCategory, searched, searchedInCat}

	tests := map[string]struct {
		path string
		want string
	}{
		"nothing is applied":     {path: ProductsPath, want: plain},
		"a category alone":       {path: ProductsPath + "?category=pcat_beans", want: inCategory},
		"a search alone":         {path: ProductsPath + "?search=coffee", want: searched},
		"a search in a category": {path: ProductsPath + "?category=pcat_beans&search=coffee", want: searchedInCat},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			catalog := categoryCatalog(categoryVocabulary())
			catalog.byEntity[EntityProduct] = nil

			rec := getPage(newCatalogPanel(t, catalog), tt.path)

			require.Equal(t, http.StatusOK, rec.Code)
			body := rec.Body.String()
			assert.Contains(t, body, tt.want)
			for _, other := range sentences {
				if other == tt.want {
					continue
				}
				assert.NotContains(t, body, other,
					"exactly ONE of the four sentences may be on the page; a second one "+
						"blames a control that emptied nothing")
			}
		})
	}
}

// TestTheAppliedSearchStaysVisibleAndCanBeCleared is the visibility half.
//
// A filtered screen that looks unfiltered is the failure the category dropdown
// was built around, and a search box has more ways to fall into it: the term
// lives only in the address, so a box that came back blank would leave a short
// list under a heading that says "Products" with nothing on the page explaining
// it. The way OUT has to be on the page too — emptying the box works, but only
// if the operator already knows the box is what did this.
//
// The clear link keeps the category and drops only the search, which is what
// its words promise. The category's own "show all products" is the other half
// of the pair and clears everything, which is what ITS words promise; the two
// are asserted together here because getting either one backwards turns a
// deliberate narrowing into an accidental one.
func TestTheAppliedSearchStaysVisibleAndCanBeCleared(t *testing.T) {
	t.Parallel()

	t.Run("a search on its own", func(t *testing.T) {
		t.Parallel()

		body := getPage(newCatalogPanel(t, categoryCatalog(categoryVocabulary())),
			ProductsPath+"?search=coffee").Body.String()

		assert.Contains(t, body, `value="coffee"`, "the box must keep what was searched for")
		assert.Contains(t, body, "Matching <strong>coffee</strong>",
			"the applied search must be stated above the list, not only inside the box")
		assert.Contains(t, body, `<a href="`+ProductsPath+`">clear the search</a>`,
			"with no category applied the clear link is the bare product list")
	})

	t.Run("a search inside a category", func(t *testing.T) {
		t.Parallel()

		body := getPage(newCatalogPanel(t, categoryCatalog(categoryVocabulary())),
			ProductsPath+"?category=pcat_beans&search=coffee").Body.String()

		assert.Contains(t, body,
			`<a href="`+ProductsPath+`?category=pcat_beans">clear the search</a>`,
			"clearing the search must KEEP the category; dropping a narrowing the "+
				"operator did not ask to drop is how a filtered screen widens by accident")
		assert.Contains(t, body, `<a href="`+ProductsPath+`">show all products</a>`,
			"and the category's own link must still clear everything, which is what it says")
	})
}

// TestTheSearchBoxSurvivesAnUnreadableCategoryList proves one broken read does
// not take away the control that never depended on it.
//
// The dropdown is filled by a second Graph call and disappears when that call
// fails. The box needs no vocabulary at all, so a box nested inside the
// dropdown's own condition would vanish for a reason that has nothing to do
// with it — and an operator who cannot search a catalog of tens of thousands is
// back to paging through it.
//
// The applied category has to survive submitting that form, too. With the
// dropdown gone there is no control carrying it, so the form carries it hidden:
// without that, typing a search would silently widen the list to the whole
// catalog while the notice above it still named the category.
func TestTheSearchBoxSurvivesAnUnreadableCategoryList(t *testing.T) {
	t.Parallel()

	catalog := categoryCatalog(categoryVocabulary())
	catalog.errByEntity = map[string]error{
		EntityCategory: errors.NotFound("query_provider_missing", "category.query was not found"),
	}

	rec := getPage(newCatalogPanel(t, catalog), ProductsPath+"?category=pcat_beans&search=coffee")

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.NotContains(t, body, "<select", "the dropdown must still be absent, not empty")
	assert.Contains(t, body, `<input type="search" id="search" name="search" value="coffee">`,
		"the search box must survive a failure it has nothing to do with")
	assert.Contains(t, body, `<input type="hidden" name="category" value="pcat_beans">`,
		"the form must carry the applied category that the missing dropdown can no "+
			"longer submit")
}

// TestTheSearchTermIsEscaped proves the typed text cannot carry markup.
//
// It is printed back in three places — an attribute value, a <strong> and a URL
// in the paging links — and it is the most directly attacker-controlled string
// on the screen: unlike a title or a category name it needs no write access at
// all, only a link somebody clicks while signed in as an administrator.
func TestTheSearchTermIsEscaped(t *testing.T) {
	t.Parallel()

	full := make([]query.Record, 0, productsPerPage+1)
	for i := range productsPerPage + 1 {
		full = append(full, query.Record{"id": "p" + strconv.Itoa(i), "title": "T"})
	}

	catalog := categoryCatalog(categoryVocabulary())
	catalog.byEntity[EntityProduct] = full

	term := url.QueryEscape(`"><script>alert('admin')</script>`)
	body := getPage(newCatalogPanel(t, catalog), ProductsPath+"?search="+term).Body.String()

	assert.NotContains(t, body, "<script>", "the typed term must not be printed as a raw tag")
	assert.Contains(t, body, "&lt;script&gt;", "the term must be escaped, not dropped")
	assert.NotContains(t, body, "ZgotmplZ",
		"the engine failed to resolve a context: escaping LOOKS like it worked but the "+
			"data is silently removed")
}

// TestProductPageJoinsPriceAndStock proves one product page shows the variant,
// its price and its stock — the three coming from three different modules.
//
// It also pins the SHAPE of the second call: the price and the stock arrive as
// EXPANSIONS in the same request. A screen that fetched them per variant would
// issue a query per row, which is exactly what the read layer's no-N+1 rule
// exists to prevent, and no assertion on the rendered output would notice.
func TestProductPageJoinsPriceAndStock(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityProduct: {{"id": "prod_1", "title": "Coffee", "handle": "coffee", "status": "published"}},
		EntityVariant: {{
			"id": "var_1", "title": "250g", "sku": "COF-250",
			keyPriceSet: query.Record{"id": "pset_1", "prices": []map[string]any{
				{"id": "price_1", "currency_code": "TRY", "amount": int64(19990)},
			}},
			keyInventory: query.Record{"id": "inv_1", FieldAvailableQuantity: int64(42)},
		}},
		EntityRegion: {{"id": "reg_1", "currency_code": "TRY",
			"currency": map[string]any{"code": "TRY", "decimal_digits": 2}}},
	}}

	rec := getPage(newCatalogPanel(t, catalog), ProductsPath+"/prod_1")

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "250g")
	assert.Contains(t, body, "COF-250")
	assert.Contains(t, body, "199.90 TRY", "the amount must be scaled by the currency's digits")
	assert.NotContains(t, body, "minor units", "a known scale must not be labeled as raw")
	assert.Contains(t, body, "42", "the sellable quantity must be shown")

	spec, ok := catalog.specFor(EntityVariant)
	require.True(t, ok)
	require.Len(t, spec.Expand, 2, "the price and the stock must be expansions of ONE call")
	links := []string{spec.Expand[0].Link, spec.Expand[1].Link}
	assert.ElementsMatch(t, []string{LinkVariantPriceSet, LinkVariantInventory}, links)
	assert.Equal(t, map[string]any{filterProductID: []string{"prod_1"}}, spec.Filters)
}

// TestProductPageShowsMinorUnitsWhenTheScaleIsUnknown proves an amount is never
// guessed.
//
// ISO 4217 has 0-, 2- and 3-digit currencies. With the scale unavailable — the
// region module unregistered, say — a screen assuming 100 would print a wrong
// price CONFIDENTLY. The raw integer with a label is uglier and honest.
func TestProductPageShowsMinorUnitsWhenTheScaleIsUnknown(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityProduct: {{"id": "prod_1", "title": "Coffee"}},
		EntityVariant: {{
			"id": "var_1", "title": "250g",
			keyPriceSet: query.Record{"id": "pset_1", "prices": []map[string]any{
				{"id": "price_1", "currency_code": "TRY", "amount": int64(19990)},
			}},
		}},
		// No region record: the scale cannot be learned.
	}}

	rec := getPage(newCatalogPanel(t, catalog), ProductsPath+"/prod_1")

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "19990 TRY")
	assert.Contains(t, body, "minor units",
		"an amount whose scale is unknown must SAY that it is raw")
	assert.NotContains(t, body, "199.90", "an unknown scale must never be guessed")
}

// TestProductPageTellsNoStockFromNoTracking proves a variant with no inventory
// item shows a dash rather than 0.
//
// Zero means "sold out". A variant nothing tracks is a different fact, and
// printing 0 would tell the operator to restock something the system does not
// count.
func TestProductPageTellsNoStockFromNoTracking(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityProduct: {{"id": "prod_1", "title": "Coffee"}},
		EntityVariant: {{"id": "var_1", "title": "250g"}},
	}}

	rec := getPage(newCatalogPanel(t, catalog), ProductsPath+"/prod_1")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "—", "an untracked variant must show a dash")
	assert.NotContains(t, rec.Body.String(), ">0<", "an untracked variant must not read as sold out")
}

// TestProductPageIsNotFoundForAnUnknownID proves a missing product answers 404.
func TestProductPageIsNotFoundForAnUnknownID(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{EntityProduct: {}}}
	rec := getPage(newCatalogPanel(t, catalog), ProductsPath+"/nope")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotContains(t, rec.Body.String(), "Variants",
		"a product that does not exist must not render a variant table")
}

// TestCatalogFailureSeparatesOurBugFromAnOutage proves the two failures get
// different status codes.
//
// An invalid spec is THIS package's bug: a field, filter or link name it spells
// no longer matches the provider. Reporting it as "temporarily unavailable"
// would send the operator to look at the database while the fix is in the
// panel.
func TestCatalogFailureSeparatesOurBugFromAnOutage(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		err  error
		want int
	}{
		"the read layer is down": {
			err:  errors.Unavailable("db_down", "the database is unreachable"),
			want: http.StatusServiceUnavailable,
		},
		"the spec is invalid": {
			err:  errors.Invalid("query_invalid_field", "no such field"),
			want: http.StatusInternalServerError,
		},
		"a provider is missing": {
			err:  errors.NotFound("query_provider_missing", "product.query was not found"),
			want: http.StatusServiceUnavailable,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := getPage(newCatalogPanel(t, &fakeCatalog{err: tt.err}), ProductsPath)

			assert.Equal(t, tt.want, rec.Code)
			assert.NotContains(t, rec.Body.String(), "unreachable",
				"the underlying error must not reach the page; it can carry a provider "+
					"name, a query or a database address")
		})
	}
}

// TestFormatAmountNeverGuessesTheScale is the money formatter's table.
//
// The arithmetic stays in integers throughout: dividing by a power of ten in
// floating point is exactly the operation plan Section 8 forbids for money,
// because at large amounts the result is no longer the number that was stored.
func TestFormatAmountNeverGuessesTheScale(t *testing.T) {
	t.Parallel()

	scales := map[string]int{"TRY": 2, "JPY": 0, "KWD": 3}

	tests := map[string]struct {
		minor int64
		code  string
		want  string
		exact bool
	}{
		"two digits":            {19990, "TRY", "199.90", true},
		"zero digits":           {1000, "JPY", "1000", true},
		"three digits":          {1000, "KWD", "1.000", true},
		"smaller than the unit": {5, "TRY", "0.05", true},
		"exactly one unit":      {100, "TRY", "1.00", true},
		"zero":                  {0, "TRY", "0.00", true},
		"negative":              {-19990, "TRY", "-199.90", true},
		"unknown currency":      {19990, "XXX", "19990", false},
		"large amount":          {922337203685477, "TRY", "9223372036854.77", true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, exact := formatAmount(tt.minor, tt.code, scales)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.exact, exact)
		})
	}
}

// TestIntValueRejectsFloats proves a float amount is refused rather than
// printed.
//
// The read layer runs in-process and hands the provider's own values through,
// so an amount arrives as an integer. A float64 would mean the value had passed
// through a JSON round trip and lost precision; rendering it would print a
// wrong price confidently.
func TestIntValueRejectsFloats(t *testing.T) {
	t.Parallel()

	_, ok := intValue(float64(19990))
	assert.False(t, ok, "a float amount must not be accepted as money")

	_, ok = intValue("19990")
	assert.False(t, ok, "a string amount must not be accepted as money")

	got, ok := intValue(int64(19990))
	assert.True(t, ok)
	assert.Equal(t, 19990, got)
}

// TestCurrencyScalesDegradeWithoutRegions proves an unreadable region list
// leaves the scale map empty rather than defaulting.
func TestCurrencyScalesDegradeWithoutRegions(t *testing.T) {
	t.Parallel()

	panel := newCatalogPanel(t, &fakeCatalog{err: errors.NotFound("no_region", "region.query is missing")})

	scales := panel.currencyScales(context.Background())

	assert.Empty(t, scales, "an unreadable region list must not produce a guessed scale")
}

// TestCatalogSpellsTheNamesTheProvidersUse pins the specs the screens build.
//
// The names are compared against the modules' own constants at compile time in
// internal/arch; what is pinned HERE is that the screen actually PUTS them into
// the spec. A screen that built the right names into the wrong field would pass
// the arch check and still read nothing.
func TestCatalogSpellsTheNamesTheProvidersUse(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{byEntity: map[string][]query.Record{
		EntityProduct: {{"id": "prod_1", "title": "Coffee"}},
	}}

	getPage(newCatalogPanel(t, catalog), ProductsPath+"/prod_1")

	products, ok := catalog.specFor(EntityProduct)
	require.True(t, ok, "the product entity must be read")
	assert.Equal(t, map[string]any{filterID: []string{"prod_1"}}, products.Filters)

	variants, ok := catalog.specFor(EntityVariant)
	require.True(t, ok, "the variant entity must be read")
	assert.Contains(t, variants.Fields, "sku")

	regions, ok := catalog.specFor(EntityRegion)
	require.True(t, ok, "the region entity must be read for the currency scales")
	assert.Contains(t, regions.Fields, "currency")
	assert.True(t, strings.HasPrefix(EntityRegion, "region"))
}

// fakeProductWriter is the module's admin write surface in memory.
type fakeProductWriter struct {
	err error

	calls int
	id    string
	title string
	// handle and status are recorded so a test can prove the form's values
	// reach the surface unchanged.
	handle string
	status string
}

func (f *fakeProductWriter) UpdateProductBasics(_ context.Context, id, title, handle, status string) error {
	f.calls++
	f.id, f.title, f.handle, f.status = id, title, handle, status

	return f.err
}

// newEditPanel builds a panel with both a read layer and a write surface.
func newEditPanel(t *testing.T, catalog Catalog, writer ProductWriter) *UI {
	t.Helper()

	panel := newCatalogPanel(t, catalog)
	panel.products = writer

	return panel
}

// editRouter mounts the edit routes alongside the catalog ones.
func editRouter(panel *UI) chi.Router {
	r := catalogRouter(panel)
	r.Get(ProductEditPath, panel.editProduct)
	r.Post(ProductEditPath, panel.submitProductEdit)

	return r
}

// postEdit submits the edit form.
func postEdit(panel *UI, id string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, ProductsPath+"/"+id+"/edit",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	editRouter(panel).ServeHTTP(rec, req)

	return rec
}

// editCatalog is a read layer holding one product.
func editCatalog() *fakeCatalog {
	return &fakeCatalog{byEntity: map[string][]query.Record{
		EntityProduct: {{"id": "prod_1", "title": "Coffee", "handle": "coffee", "status": "draft"}},
	}}
}

// TestEditFormShowsTheStoredValues proves the form opens with what is stored.
func TestEditFormShowsTheStoredValues(t *testing.T) {
	t.Parallel()

	panel := newEditPanel(t, editCatalog(), &fakeProductWriter{})

	rec := httptest.NewRecorder()
	editRouter(panel).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, ProductsPath+"/prod_1/edit", http.NoBody))

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, `value="Coffee"`)
	assert.Contains(t, body, `value="coffee"`)
	assert.Contains(t, body, `<option value="draft" selected>`,
		"the stored status must be the selected one")
	assert.Contains(t, body, `<option value="archived">`,
		"every status the module accepts must be offered")
}

// TestSubmittingTheEditReachesTheWriteSurface proves the form's values arrive
// unchanged and the browser is redirected.
//
// The redirect matters as much as the write: rendering the result here would
// leave the POST in the history, and a refresh would apply the same edit twice.
func TestSubmittingTheEditReachesTheWriteSurface(t *testing.T) {
	t.Parallel()

	writer := &fakeProductWriter{}
	panel := newEditPanel(t, editCatalog(), writer)

	rec := postEdit(panel, "prod_1", url.Values{
		"title":  {"Filter Coffee"},
		"handle": {"filter-coffee"},
		"status": {"published"},
	})

	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, ProductsPath+"/prod_1", rec.Header().Get("Location"))

	require.Equal(t, 1, writer.calls, "the write surface must be called exactly once")
	assert.Equal(t, "prod_1", writer.id)
	assert.Equal(t, "Filter Coffee", writer.title)
	assert.Equal(t, "filter-coffee", writer.handle)
	assert.Equal(t, "published", writer.status)
}

// TestARejectedEditComesBackWithTheTypedValues proves a refusal re-renders the
// form rather than redirecting.
//
// Redirecting on failure would throw away what the operator typed and show a
// message with no field to fix. The status code is 422 and not 200: the request
// was understood and refused, and saying so in the status is what makes the
// failure visible to anything but a human.
func TestARejectedEditComesBackWithTheTypedValues(t *testing.T) {
	t.Parallel()

	writer := &fakeProductWriter{
		err: errors.Conflict("product_handle_taken", "that handle is already used by another product"),
	}
	panel := newEditPanel(t, editCatalog(), writer)

	rec := postEdit(panel, "prod_1", url.Values{
		"title":  {"Filter Coffee"},
		"handle": {"coffee"},
		"status": {"published"},
	})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "that handle is already used by another product",
		"the service's own message must be shown; it is the only thing that says what to fix")
	assert.Contains(t, body, `value="Filter Coffee"`,
		"what the operator typed must come back, not what is stored")
	assert.Contains(t, body, `<option value="published" selected>`)
}

// TestAnUnexpectedFailureIsNotShownToTheOperator proves only a rejection the
// operator can act on reaches the page.
//
// An Invalid or Conflict message is written by a service author and is
// client-safe by the framework's own rule. Anything else can carry a query, a
// provider name or a database address, so it goes through the core's error path
// and is masked there.
func TestAnUnexpectedFailureIsNotShownToTheOperator(t *testing.T) {
	t.Parallel()

	writer := &fakeProductWriter{
		err: errors.Unavailable("db_down", "dial tcp 10.0.0.5:5432: connection refused"),
	}
	panel := newEditPanel(t, editCatalog(), writer)

	rec := postEdit(panel, "prod_1", url.Values{
		"title": {"Filter Coffee"}, "handle": {"filter-coffee"}, "status": {"published"},
	})

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.NotContains(t, rec.Body.String(), "10.0.0.5",
		"the underlying error must not reach the page")
	assert.NotContains(t, rec.Body.String(), "connection refused")
}

// TestEditingIsUnavailableWithoutTheModule proves a panel with no write surface
// says so instead of panicking.
//
// The write surface is resolved OPTIONALLY: an installation without the product
// module still gets a panel. Treating it as mandatory would turn a removable
// module into a requirement of the panel itself.
func TestEditingIsUnavailableWithoutTheModule(t *testing.T) {
	t.Parallel()

	panel := newEditPanel(t, editCatalog(), nil)

	rec := postEdit(panel, "prod_1", url.Values{
		"title": {"Filter Coffee"}, "handle": {"filter-coffee"}, "status": {"published"},
	})

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "not registered")
}

// TestEditFormEscapesOperatorText proves the form's values are escaped.
//
// An edit form prints back exactly the text somebody typed, which makes it the
// most direct XSS surface in the panel — and the panel runs inside an
// administrator's session.
func TestEditFormEscapesOperatorText(t *testing.T) {
	t.Parallel()

	writer := &fakeProductWriter{err: errors.Invalid("bad", "no")}
	panel := newEditPanel(t, editCatalog(), writer)

	rec := postEdit(panel, "prod_1", url.Values{
		"title":  {`"><script>alert('admin')</script>`},
		"handle": {"coffee"},
		"status": {"draft"},
	})

	body := rec.Body.String()
	assert.NotContains(t, body, "<script>", "the typed title must not be printed as a raw tag")
	assert.NotContains(t, body, "ZgotmplZ",
		"the engine failed to resolve a context: escaping LOOKS like it worked but the "+
			"data is silently removed")
}
