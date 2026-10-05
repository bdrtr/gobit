package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// fakePromotionReader answers one promotion's page as scripted.
type fakePromotionReader struct {
	fakePromotions
	page  string
	err   error
	asked []string
}

func (f *fakePromotionReader) PromotionJSON(_ context.Context, id string) (json.RawMessage, error) {
	f.asked = append(f.asked, id)
	return json.RawMessage(f.page), f.err
}

// promotionPageRequest opens a promotion's page as a reader.
func promotionPageRequest(panel *UI, path string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Get(PromotionPath, panel.showPromotion)

	request := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	request = request.WithContext(corehttp.WithPrincipal(request.Context(),
		corehttp.Principal{ID: "user_1", Kind: "user", Scopes: []string{scopePromotionRead}}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, request)

	return rec
}

// fullPromotion is a coupon with every section filled.
const fullPromotion = `{
	"id":"promo_1","code":"SPRING","is_automatic":false,"type":"standard","status":"active",
	"usage_count":2,"usage_limit":100,"created_at":"2026-09-01T10:00:00Z",
	"campaign":{"name":"Spring sale","starts_at":"2026-09-01T00:00:00Z","ends_at":null,
		"budget_type":"spend","budget_limit":500000,"budget_used":12550,"budget_currency_code":"TRY"},
	"application_method":{"type":"percentage","target_type":"items","allocation":"each","value":1250,
		"max_quantity":3,"buy_quantity":null,"apply_to_quantity":null,"currency_code":""},
	"rules":[{"type":"context","attribute":"currency_code","operator":"in","values":["TRY","EUR"]}],
	"latest_uses":[
		{"reference":"order_new","amount":1050,"currency_code":"TRY","created_at":"2026-09-03T12:00:00Z","released_at":null},
		{"reference":"order_old","amount":2000,"currency_code":"TRY","created_at":"2026-09-02T12:00:00Z","released_at":"2026-09-02T13:30:00Z"}
	]}`

// TestAPromotionsPageShowsWhatItGives is ADR 0313: the discount in the shop's
// terms, the rules, the campaign with its budget, and the latest uses with
// their amounts in the currency's scale, a given-back one marked.
func TestAPromotionsPageShowsWhatItGives(t *testing.T) {
	t.Parallel()

	reader := &fakePromotionReader{page: fullPromotion}
	panel := newCatalogPanel(t, &fakeCatalog{byEntity: map[string][]query.Record{
		EntityRegion: {currencyRecord("TRY", 2)},
	}})
	panel.promotions = reader

	rec := promotionPageRequest(panel, PromotionsPath+"/promo_1")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Equal(t, []string{"promo_1"}, reader.asked)
	assert.Contains(t, body, "<h1>SPRING</h1>")
	assert.Contains(t, body, "2 of 100")
	assert.Contains(t, body, "<td>12.5% off</td>", "basis points read as a percentage")
	assert.Contains(t, body, "<td>the items, on each</td>")
	assert.Contains(t, body, "3 units of a line")
	assert.Contains(t, body, "<td>currency_code</td><td>in</td><td>TRY, EUR</td>")
	assert.Contains(t, body, "Spring sale")
	assert.Contains(t, body, "125.50 TRY of 5000.00 TRY", "a spend budget in the currency's scale")
	assert.Contains(t, body, "order_new")
	assert.Contains(t, body, "10.50 TRY")
	assert.Contains(t, body, "2026-09-02 13:30", "a use given back is marked when")
	assert.Less(t, strings.Index(body, "order_new"), strings.Index(body, "order_old"), "in the order the surface sends, newest first")
	assert.Contains(t, body, `href="`+PromotionsPath+`?status=active"`, "back to the promotion's own list")
}

// TestARuleOnTheCartsBagSaysItHoldsOnNoCart is ADR 0407: a rule on an
// attribute a cart's metadata filled before is marked on the promotion's page
// as holding on no cart, with the widening its removal brings, and a rule on
// any other attribute is not.
func TestARuleOnTheCartsBagSaysItHoldsOnNoCart(t *testing.T) {
	t.Parallel()

	reader := &fakePromotionReader{page: `{
		"id":"promo_1","code":"BRAND","is_automatic":true,"type":"standard","status":"active",
		"created_at":"2026-09-01T10:00:00Z",
		"rules":[
			{"type":"context","attribute":"cart.brand","operator":"eq","values":["A"]},
			{"type":"context","attribute":"currency_code","operator":"in","values":["TRY"]}
		]}`}
	panel := newCatalogPanel(t, &fakeCatalog{})
	panel.promotions = reader

	rec := promotionPageRequest(panel, PromotionsPath+"/promo_1")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "<td>cart.brand (holds on no cart, ADR 0407; removing it opens the promotion "+
		"wherever its other rules hold)</td>")
	assert.Contains(t, body, "<td>currency_code</td>", "a rule on another attribute is not marked")
	assert.Equal(t, 1, strings.Count(body, "holds on no cart"), "the mark is on the bag's rule alone")
}

// TestAFixedDiscountIsPrintedInItsCurrency: a fixed amount reads in its
// currency's scale, and without a known scale it says it is in minor units
// rather than guessing; an empty promotion says what is missing.
func TestAFixedDiscountIsPrintedInItsCurrency(t *testing.T) {
	t.Parallel()

	fixed := `{"id":"promo_2","code":"","is_automatic":true,"type":"standard","status":"draft",
		"usage_count":0,"usage_limit":null,"created_at":"2026-09-01T10:00:00Z",
		"campaign":{"name":"Launch","budget_type":"usage","budget_limit":100,"budget_used":7},
		"application_method":{"type":"fixed","target_type":"order","allocation":"across","value":5000,
			"max_quantity":null,"buy_quantity":null,"apply_to_quantity":null,"currency_code":"TRY"},
		"rules":[],"latest_uses":[]}`
	panel := newCatalogPanel(t, &fakeCatalog{byEntity: map[string][]query.Record{
		EntityRegion: {currencyRecord("TRY", 2)},
	}})
	panel.promotions = &fakePromotionReader{page: fixed}

	rec := promotionPageRequest(panel, PromotionsPath+"/promo_2")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "<h1>Automatic promotion</h1>")
	assert.Contains(t, body, "<td>50.00 TRY off</td>")
	assert.Contains(t, body, "<td>the order, spread across them</td>")
	assert.Contains(t, body, "No rule is set.")
	assert.Contains(t, body, "<td>7 of 100 uses</td>", "a usage budget counts uses")
	assert.Contains(t, body, "Not used yet.")

	unscaled := newCatalogPanel(t, &fakeCatalog{})
	unscaled.promotions = &fakePromotionReader{page: fixed}
	rec = promotionPageRequest(unscaled, PromotionsPath+"/promo_2")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "5000 TRY (minor units) off")

	bare := newCatalogPanel(t, &fakeCatalog{})
	bare.promotions = &fakePromotionReader{page: `{"id":"promo_3","code":"BARE","type":"standard",
		"status":"draft","campaign":null,"application_method":null,"rules":[],"latest_uses":[]}`}
	rec = promotionPageRequest(bare, PromotionsPath+"/promo_3")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "No discount is defined; the promotion applies nothing.")
	assert.Contains(t, rec.Body.String(), "belongs to no campaign")
}

// TestAPromotionThatCannotBeReadIsSaidSo: an unknown or malformed id is not
// found, a failure is not mistaken for one, and a surface that cannot read a
// promotion says the screen is unavailable.
func TestAPromotionThatCannotBeReadIsSaidSo(t *testing.T) {
	t.Parallel()

	for name, err := range map[string]error{
		"unknown":   errors.NotFound("promotion_not_found", "no such promotion"),
		"malformed": errors.Invalid("promotion_invalid_input", "promotion id has the wrong prefix"),
	} {
		panel := newCatalogPanel(t, &fakeCatalog{})
		panel.promotions = &fakePromotionReader{err: err}
		rec := promotionPageRequest(panel, PromotionsPath+"/promo_x")
		assert.Equal(t, http.StatusNotFound, rec.Code, name)
		assert.Contains(t, rec.Body.String(), "There is no such promotion.", name)
	}

	failing := newCatalogPanel(t, &fakeCatalog{})
	failing.promotions = &fakePromotionReader{err: errors.Unavailable("db_down", "no answer")}
	assert.Equal(t, http.StatusServiceUnavailable, promotionPageRequest(failing, PromotionsPath+"/promo_x").Code)

	listing := newCatalogPanel(t, &fakeCatalog{})
	listing.promotions = &fakePromotions{}
	assert.Equal(t, http.StatusServiceUnavailable, promotionPageRequest(listing, PromotionsPath+"/promo_x").Code)
}

// TestPercentText prints basis points without trailing zeros.
func TestPercentText(t *testing.T) {
	t.Parallel()

	for bp, want := range map[int64]string{1000: "10", 1250: "12.5", 10000: "100", 50: "0.5", 5: "0.05", 0: "0"} {
		assert.Equal(t, want, percentText(bp), "%d basis points", bp)
	}
}
