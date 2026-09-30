package adminui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// bundleCatalog is a read layer holding a gift box made of a towel without a
// SKU and then two soaps. The parts are answered in the other order, so the
// page's order has to come from the composition.
func bundleCatalog() *fakeCatalog {
	return &fakeCatalog{
		byEntity: map[string][]query.Record{
			EntityProduct: {
				{"id": "prod_box", "title": "Gift box", "handle": "gift-box", "status": "published", "version": int64(7)},
				{"id": "prod_towel", "title": "Towel", "handle": "towel", "status": "published"},
				{"id": "prod_soap", "title": "Soap", "handle": "soap", "status": "published"},
			},
		},
		answer: func(spec query.GraphSpec) ([]query.Record, error, bool) {
			if spec.Entity != EntityVariant {
				return nil, nil, false
			}
			ids, _ := spec.Filters[filterID].([]string)
			if len(ids) == 1 && ids[0] == "variant_box" {
				box := query.Record{"id": "variant_box", "title": "Large", "sku": "BOX-L", "product_id": "prod_box"}
				// The composition is answered only when asked for, as the read
				// layer does: a page that stopped asking would otherwise still
				// get it.
				if slices.Contains(spec.Fields, FieldBundleComponents) {
					box[FieldBundleComponents] = []query.Record{
						{FieldBundleComponentVariantID: "variant_towel", FieldBundleComponentQuantity: int64(1)},
						{FieldBundleComponentVariantID: "variant_soap", FieldBundleComponentQuantity: int64(2)},
					}
				}
				return []query.Record{box}, nil, true
			}
			return []query.Record{
				{"id": "variant_soap", "title": "Lavender", "sku": "SOAP-1", "product_id": "prod_soap"},
				{"id": "variant_towel", "title": "White", "product_id": "prod_towel"},
			}, nil, true
		},
	}
}

// boxVariantPath is the gift box's variant page.
const boxVariantPath = ProductsPath + "/prod_box/variants/variant_box"

// bundleRouter mounts the variant page and the bundle form.
func bundleRouter(panel *UI) chi.Router {
	r := chi.NewRouter()
	r.Get(VariantPath, panel.showVariant)
	r.Get(VariantBundlePath, panel.editBundle)
	r.Post(VariantBundlePath, panel.submitBundle)
	return r
}

// bundleRequest sends a request to the bundle routes.
func bundleRequest(panel *UI, method, path, typed string) *httptest.ResponseRecorder {
	body := http.NoBody
	var req *http.Request
	if method == http.MethodPost {
		req = httptest.NewRequest(method, path,
			strings.NewReader(url.Values{"parts": {typed}, "version": {"7"}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, path, body)
	}
	rec := httptest.NewRecorder()
	bundleRouter(panel).ServeHTTP(rec, asCatalogReader(req))
	return rec
}

// TestTheVariantPageListsItsParts is ADR 0236 on the variant page: the parts in
// the composition's order with their units, each linked to its own page, and
// the stock table replaced by the sentence that a bundle counts none.
func TestTheVariantPageListsItsParts(t *testing.T) {
	t.Parallel()

	rec := bundleRequest(newEditPanel(t, bundleCatalog(), &fakeProductWriter{}), http.MethodGet, boxVariantPath, "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	towel := strings.Index(body, "1 × <a href=\""+ProductsPath+"/prod_towel/variants/variant_towel\">Towel — White</a>")
	soap := strings.Index(body, "2 × <a href=\""+ProductsPath+"/prod_soap/variants/variant_soap\">Soap — Lavender</a> (SOAP-1)")
	require.Positive(t, towel, body)
	require.Positive(t, soap, body)
	assert.Less(t, towel, soap, "the parts are in the operator's order")
	assert.Contains(t, body, "A bundle counts no stock of its own")
	assert.Contains(t, body, `href="`+boxVariantPath+`/bundle"`)
}

// TestTheBundleFormShowsThePartsInOrder holds the form's value: each part by
// its SKU, or its id when it carries none, then its units, and the product's
// version the save will be asked on.
func TestTheBundleFormShowsThePartsInOrder(t *testing.T) {
	t.Parallel()

	rec := bundleRequest(newEditPanel(t, bundleCatalog(), &fakeProductWriter{}),
		http.MethodGet, boxVariantPath+"/bundle", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, `<textarea name="parts" rows="8">variant_towel 1`+"\n"+`SOAP-1 2</textarea>`)
	assert.Contains(t, body, `<input type="hidden" name="version" value="7">`)
	assert.Contains(t, body, "from 1 to 100; at most\n  20 lines")
}

// TestSavingTheBundleSendsEveryPart holds what a save asks the module for:
// one part per line, trimmed, a line with no number holding one, blank lines
// dropped, on the version the form was read at.
func TestSavingTheBundleSendsEveryPart(t *testing.T) {
	t.Parallel()

	writer := &fakeProductWriter{}
	rec := bundleRequest(newEditPanel(t, bundleCatalog(), writer),
		http.MethodPost, boxVariantPath+"/bundle", "SOAP-1 2\r\n\r\n  variant_towel \r\n")

	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, boxVariantPath, rec.Header().Get("Location"))
	assert.Equal(t, []bundleCall{{
		variantID: "variant_box", refs: []string{"SOAP-1", "variant_towel"}, quantities: []int64{2, 1}, version: 7,
	}}, writer.bundles)
}

// TestABundleLineThatDoesNotReadIsRefusedBeforeTheModule keeps a typo from
// becoming a save: the form comes back naming the line, and the module is not
// asked.
func TestABundleLineThatDoesNotReadIsRefusedBeforeTheModule(t *testing.T) {
	t.Parallel()

	for typed, sentence := range map[string]string{
		"SOAP-1 two":           `Line 1: &#34;two&#34; is not a whole number of units.`,
		"SOAP-1 2\nTOWEL 1 2":  "Line 2: write a variant&#39;s SKU or id, then how many one bundle holds.",
		"SOAP-1 2\n\nTOWEL 1x": `Line 3: &#34;1x&#34; is not a whole number of units.`,
	} {
		writer := &fakeProductWriter{}
		rec := bundleRequest(newEditPanel(t, bundleCatalog(), writer), http.MethodPost, boxVariantPath+"/bundle", typed)

		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, typed)
		assert.Contains(t, rec.Body.String(), sentence, typed)
		assert.Empty(t, writer.bundles, typed)
	}
}

// TestARefusedBundleSaveComesBackWithWhatWasTyped holds the module's refusal:
// the form with its sentence and the parts as they were typed.
func TestARefusedBundleSaveComesBackWithWhatWasTyped(t *testing.T) {
	t.Parallel()

	writer := &fakeProductWriter{bundleErr: errors.Conflict("product_bundle_shape",
		"a bundle's stock is its components'; clear the variant's inventory item first (variant_box)")}
	rec := bundleRequest(newEditPanel(t, bundleCatalog(), writer), http.MethodPost, boxVariantPath+"/bundle", "SOAP-9 3")

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "clear the variant&#39;s inventory item first")
	assert.Contains(t, body, `<textarea name="parts" rows="8">SOAP-9 3</textarea>`)
}

// TestAStaleBundleSaveComesBackAtTheStoredVersion is ADR 0222's refusal on the
// bundle form: somebody saved the product after the form was opened, and the
// form comes back with what was typed, at the version now stored.
func TestAStaleBundleSaveComesBackAtTheStoredVersion(t *testing.T) {
	t.Parallel()

	catalog := bundleCatalog()
	catalog.byEntity[EntityProduct][0]["version"] = int64(8)
	writer := &fakeProductWriter{bundleErr: errors.PreconditionFailed("product_version_mismatch", "stale")}
	rec := bundleRequest(newEditPanel(t, catalog, writer), http.MethodPost, boxVariantPath+"/bundle", "SOAP-1 3")

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "Somebody saved this product after you opened it")
	assert.Contains(t, body, `<input type="hidden" name="version" value="8">`)
	assert.Contains(t, body, `<textarea name="parts" rows="8">SOAP-1 3</textarea>`)
}

// TestTheBundleCannotBeSavedWithoutTheModule holds the answer when the write
// surface is not registered, and the page then offers no form.
func TestTheBundleCannotBeSavedWithoutTheModule(t *testing.T) {
	t.Parallel()

	panel := newCatalogPanel(t, bundleCatalog())
	assert.Equal(t, http.StatusServiceUnavailable,
		bundleRequest(panel, http.MethodPost, boxVariantPath+"/bundle", "SOAP-1 1").Code)
	page := bundleRequest(panel, http.MethodGet, boxVariantPath, "")
	assert.NotContains(t, page.Body.String(), "Edit the parts")
}

// TestAVariantOfAnotherProductHasNoBundleForm keeps the version honest: the
// form carries the addressed product's version, so a variant of another
// product is not found there.
func TestAVariantOfAnotherProductHasNoBundleForm(t *testing.T) {
	t.Parallel()

	rec := bundleRequest(newEditPanel(t, bundleCatalog(), &fakeProductWriter{}),
		http.MethodGet, ProductsPath+"/prod_towel/variants/variant_box/bundle", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
