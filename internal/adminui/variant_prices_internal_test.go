package adminui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// fakePricer is the product writer that also prices a variant (ADR 0309).
type fakePricer struct {
	fakeProductWriter
	priced []string
	err    error
}

func (f *fakePricer) PriceVariant(_ context.Context, variantID, currencyCode string, amount int64) error {
	f.priced = append(f.priced, variantID+"|"+currencyCode+"|"+strconv.FormatInt(amount, 10))

	return f.err
}

// pricingCatalog holds the variant priced in TRY in a shop that also sells in
// EUR, both scales known.
func pricingCatalog() *fakeCatalog {
	catalog := variantCatalog(int64(2))
	catalog.byEntity[EntityRegion] = append(catalog.byEntity[EntityRegion], query.Record{
		"id": "reg_2", "currency_code": "EUR", "currency": map[string]any{"code": "EUR", "decimal_digits": int64(2)},
	})

	return catalog
}

// pricingRequest sends one request to the variant's routes as an operator
// holding the scopes.
func pricingRequest(panel *UI, method, path string, form url.Values, scopes ...string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Get(VariantPath, panel.showVariant)
	r.Post(VariantPricesPath, panel.addVariantPrice)

	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(),
		corehttp.Principal{ID: "user_1", Kind: "user", Scopes: scopes}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// TestAVariantTakesAPriceInANewCurrency is ADR 0309: an operator holding both
// writes is offered the shop's currencies the variant has no price in, the
// amount is read in the currency's scale and reaches the product module's
// surface, and the operator returns to the variant; one holding the price's
// write alone is neither offered the form nor let through it.
func TestAVariantTakesAPriceInANewCurrency(t *testing.T) {
	t.Parallel()

	pricer := &fakePricer{}
	panel := newVariantPanel(t, pricingCatalog(), &fakePriceWriter{}, nil)
	panel.products = pricer
	both := []string{scopeProductRead, scopePricingRead, scopePricingWrite, scopeProductWrite}

	page := pricingRequest(panel, http.MethodGet, variantURLFor(), nil, both...)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), `<option value="EUR">EUR</option>`)
	assert.NotContains(t, page.Body.String(), `<option value="TRY">`, "a currency already priced is edited, not added")

	rec := pricingRequest(panel, http.MethodPost, variantURLFor()+"/prices", url.Values{
		"currency": {"eur"}, "amount": {"9.50"},
	}, both...)
	assert.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, variantURLFor(), rec.Header().Get("Location"))
	assert.Equal(t, []string{"var_1|EUR|950"}, pricer.priced)

	pricingOnly := []string{scopeProductRead, scopePricingRead, scopePricingWrite}
	page = pricingRequest(panel, http.MethodGet, variantURLFor(), nil, pricingOnly...)
	assert.NotContains(t, page.Body.String(), `action="`+variantURLFor()+`/prices"`)
	productOnly := []string{scopeProductRead, scopeProductWrite}
	rec = pricingRequest(panel, http.MethodPost, variantURLFor()+"/prices", url.Values{
		"currency": {"EUR"}, "amount": {"1"},
	}, productOnly...)
	assert.Equal(t, http.StatusForbidden, rec.Code, "the price's own write is asked for too")
	assert.Len(t, pricer.priced, 1, "nothing more was written")

	bad := pricingRequest(panel, http.MethodPost, variantURLFor()+"/prices", url.Values{
		"currency": {"EUR"}, "amount": {"nine"},
	}, both...)
	assert.Equal(t, http.StatusUnprocessableEntity, bad.Code, bad.Body.String())

	refusing := newVariantPanel(t, pricingCatalog(), &fakePriceWriter{}, nil)
	refusing.products = &fakePricer{err: errors.Unavailable("product_import_prices_unavailable", "this installation writes no prices")}
	rec = pricingRequest(refusing, http.MethodPost, variantURLFor()+"/prices", url.Values{
		"currency": {"EUR"}, "amount": {"1"},
	}, both...)
	assert.NotEqual(t, http.StatusSeeOther, rec.Code, "a write the module could not make is not reported as made")
	assert.Contains(t, rec.Body.String(), "The price could not be added")
}
