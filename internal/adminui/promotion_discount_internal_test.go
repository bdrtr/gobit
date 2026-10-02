package adminui

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// fakeDiscountReviser reads a promotion as fakePromotionReader does and
// records each change of its discount.
type fakeDiscountReviser struct {
	fakePromotionReader
	revised []string
	err     error
}

func (f *fakeDiscountReviser) ReviseDiscountValue(_ context.Context, id, kind string, readValue, value int64) error {
	f.revised = append(f.revised, fmt.Sprintf("%s|%s|%d|%d", id, kind, readValue, value))
	return f.err
}

// fixedPromotion is an automatic promotion giving a fixed amount off an order.
const fixedPromotion = `{"id":"promo_2","code":"","is_automatic":true,"type":"standard","status":"draft",
	"usage_count":0,"usage_limit":null,"created_at":"2026-09-01T10:00:00Z","campaign":null,
	"application_method":{"type":"fixed","target_type":"order","allocation":"across","value":5000,
		"max_quantity":null,"buy_quantity":null,"apply_to_quantity":null,"currency_code":"TRY"},
	"rules":[],"latest_uses":[]}`

// discountPanel is a panel over one promotion in a shop selling in TRY.
func discountPanel(t *testing.T, promotions PromotionLister) *UI {
	t.Helper()

	panel := newCatalogPanel(t, &fakeCatalog{byEntity: map[string][]query.Record{
		EntityRegion: {currencyRecord("TRY", 2)},
	}})
	panel.promotions = promotions
	panel.scopes = builtInScopes()

	return panel
}

// discountFormOf is the promotion page's discount form.
func discountFormOf(t *testing.T, body, id string) string {
	t.Helper()

	_, form, found := strings.Cut(body, `action="`+PromotionsPath+"/"+id+`/discount"`)
	require.True(t, found, "the page offers the discount form")
	form, _, _ = strings.Cut(form, "</form>")

	return form
}

// TestADiscountsValueIsChangedOnItsPage is ADR 0338: a writer is offered the
// form drawn from the type and the value the page was read with, a
// percentage as a percent and a fixed amount in its currency's decimals; the
// surface is asked to change the value from those, a percent read into basis
// points and an amount into minor units, and the page says so; a value the
// panel cannot read is not sent, and a refusal comes back with what was
// typed; a reader is offered nothing.
func TestADiscountsValueIsChangedOnItsPage(t *testing.T) {
	t.Parallel()

	percent := &fakeDiscountReviser{fakePromotionReader: fakePromotionReader{page: fullPromotion}}
	panel := discountPanel(t, percent)
	writer := []string{scopePromotionRead, scopePromotionWrite}

	rec := campaignsRequest(panel, http.MethodGet, PromotionsPath+"/promo_1", nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	form := discountFormOf(t, rec.Body.String(), "promo_1")
	for _, want := range []string{
		`name="read_type" value="percentage"`, `name="read_value" value="1250"`, `name="value" value="12.5"`, "%",
	} {
		assert.Contains(t, form, want)
	}
	rec = campaignsRequest(panel, http.MethodGet, PromotionsPath+"/promo_1", nil, scopePromotionRead)
	assert.NotContains(t, rec.Body.String(), "/discount", "a reader changes nothing")

	rec = campaignsRequest(panel, http.MethodPost, PromotionsPath+"/promo_1/discount", url.Values{
		formReadMethodType: {"percentage"}, formReadMethodValue: {"1250"}, formDiscountValue: {" 15 "},
	}, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, PromotionsPath+"/promo_1?written=1", rec.Header().Get("Location"))
	assert.Equal(t, []string{"promo_1|percentage|1250|1500"}, percent.revised, "a percent into basis points")
	landed := campaignsRequest(panel, http.MethodGet, rec.Header().Get("Location"), nil, scopePromotionRead)
	assert.Contains(t, landed.Body.String(), "The discount was written.")

	fixed := &fakeDiscountReviser{fakePromotionReader: fakePromotionReader{page: fixedPromotion}}
	fixedPanel := discountPanel(t, fixed)
	form = discountFormOf(t, campaignsRequest(fixedPanel, http.MethodGet, PromotionsPath+"/promo_2", nil, writer...).Body.String(), "promo_2")
	assert.Contains(t, form, `name="value" value="50.00"`, "an amount in its currency's decimals")
	assert.Contains(t, form, `name="read_type" value="fixed"`, "the type it was drawn with")
	assert.Contains(t, form, `name="read_value" value="5000"`)
	assert.Contains(t, form, `name="currency" value="TRY"`)
	campaignsRequest(fixedPanel, http.MethodPost, PromotionsPath+"/promo_2/discount", url.Values{
		formReadMethodType: {"fixed"}, formReadMethodValue: {"5000"}, formDiscountValue: {"75.50"}, formDiscountCurrency: {"TRY"},
	}, writer...)
	assert.Equal(t, []string{"promo_2|fixed|5000|7550"}, fixed.revised, "an amount into minor units")

	for reason, typed := range map[string]url.Values{
		"The discount the page was drawn with could not be read": {formReadMethodType: {"percentage"}, formReadMethodValue: {"12.5"}, formDiscountValue: {"15"}},
		"has 2 decimal digits": {formReadMethodType: {"percentage"}, formReadMethodValue: {"1250"}, formDiscountValue: {"15.555"}},
	} {
		rec = campaignsRequest(panel, http.MethodPost, PromotionsPath+"/promo_1/discount", typed, writer...)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, reason)
		assert.Contains(t, rec.Body.String(), reason)
	}
	assert.Len(t, percent.revised, 1, "what the panel cannot read is not sent")

	percent.err = errors.Conflict("promotion_discount_revised",
		"promotion promo_1's discount was changed since it was read: it is percentage 1250 now; draw the page again")
	rec = campaignsRequest(panel, http.MethodPost, PromotionsPath+"/promo_1/discount", url.Values{
		formReadMethodType: {"percentage"}, formReadMethodValue: {"1000"}, formDiscountValue: {"20"},
	}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "draw the page again")
	form = discountFormOf(t, body, "promo_1")
	assert.Contains(t, form, `name="read_value" value="1250"`, "drawn from the discount as it is now")
	assert.Contains(t, form, `name="value" value="20"`, "with what was typed")
	assert.Contains(t, body, "<details open>")
	percent.err = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, PromotionsPath+"/promo_1/discount", url.Values{
		formReadMethodType: {"percentage"}, formReadMethodValue: {"1250"}, formDiscountValue: {"20"},
	}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	rec = campaignsRequest(discountPanel(t, &fakePromotionReader{page: fullPromotion}), http.MethodGet,
		PromotionsPath+"/promo_1", nil, writer...)
	assert.NotContains(t, rec.Body.String(), "/discount", "a surface that cannot change a discount offers no form")
	rec = campaignsRequest(discountPanel(t, &fakePromotionReader{page: fullPromotion}), http.MethodPost,
		PromotionsPath+"/promo_1/discount", url.Values{}, scopePromotionWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
