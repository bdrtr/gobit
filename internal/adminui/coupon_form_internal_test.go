package adminui

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// fakeCouponCreator lists as scripted and records each coupon it is asked
// to write.
type fakeCouponCreator struct {
	fakePromotions
	written []string
	err     error
}

func (f *fakeCouponCreator) CreateCoupon(
	_ context.Context, code, measure, target, allocation string, value int64, currency string, usageLimit *int64,
) (string, error) {
	limit := "none"
	if usageLimit != nil {
		limit = fmt.Sprint(*usageLimit)
	}
	f.written = append(f.written, fmt.Sprintf("%s|%s|%s|%s|%d|%s|%s", code, measure, target, allocation, value, currency, limit))
	return "promo_new", f.err
}

// couponPanel is a panel whose region read knows TRY's two digits.
func couponPanel(t *testing.T, creator PromotionLister) *UI {
	t.Helper()

	panel := newCatalogPanel(t, &fakeCatalog{byEntity: map[string][]query.Record{
		EntityRegion: {currencyRecord("TRY", 2)},
	}})
	panel.promotions = creator

	return panel
}

// TestTheCouponFormIsAWritersOnly is ADR 0314: the list offers the form to
// an operator who may write promotions through a surface that can.
func TestTheCouponFormIsAWritersOnly(t *testing.T) {
	t.Parallel()

	panel := couponPanel(t, &fakeCouponCreator{fakePromotions: fakePromotions{body: "[]"}})
	rec := promotionStatusRequest(panel, http.MethodGet, PromotionsPath, nil, scopePromotionRead, scopePromotionWrite)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `<form method="post" action="`+PromotionsPath+`">`)

	rec = promotionStatusRequest(panel, http.MethodGet, PromotionsPath, nil, scopePromotionRead)
	assert.NotContains(t, rec.Body.String(), "New coupon", "a reader is offered no form")

	lister := couponPanel(t, &fakePromotions{body: "[]"})
	rec = promotionStatusRequest(lister, http.MethodGet, PromotionsPath, nil, scopePromotionRead, scopePromotionWrite)
	assert.NotContains(t, rec.Body.String(), "New coupon", "a surface that cannot write offers no form")
	rec = promotionStatusRequest(lister, http.MethodPost, PromotionsPath, url.Values{formCouponCode: {"X"}}, scopePromotionWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestTheCouponFormWritesTheSurfacesTerms: a percentage is sent in basis
// points, a fixed amount in its currency's minor units, a currency it cannot
// scale as minor units already, and the operator goes to the coupon's page.
func TestTheCouponFormWritesTheSurfacesTerms(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		form url.Values
		want string
	}{
		{"a percentage", url.Values{
			formCouponCode: {"spring"}, formCouponMeasure: {"percentage"}, formCouponAmount: {" 12.5 "},
			formCouponTarget: {"order"}, formCouponAllocation: {"across"},
		}, "spring|percentage|order|across|1250||none"},
		{"an amount in a known currency", url.Values{
			formCouponCode: {"FIFTY"}, formCouponMeasure: {"fixed"}, formCouponAmount: {"50.00"}, formCouponCurrency: {" try "},
			formCouponTarget: {"items"}, formCouponAllocation: {"each"}, formCouponLimit: {" 40 "},
		}, "FIFTY|fixed|items|each|5000|TRY|40"},
		{"an amount in a currency the panel cannot scale", url.Values{
			formCouponCode: {"YEN"}, formCouponMeasure: {"fixed"}, formCouponAmount: {"500"}, formCouponCurrency: {"JPY"},
			formCouponTarget: {"order"}, formCouponAllocation: {"across"},
		}, "YEN|fixed|order|across|500|JPY|none"},
	}
	for _, tc := range cases {
		creator := &fakeCouponCreator{fakePromotions: fakePromotions{body: "[]"}}
		panel := couponPanel(t, creator)

		rec := promotionStatusRequest(panel, http.MethodPost, PromotionsPath, tc.form, scopePromotionRead, scopePromotionWrite)

		require.Equal(t, http.StatusSeeOther, rec.Code, "%s: %s", tc.name, rec.Body.String())
		assert.Equal(t, PromotionsPath+"/promo_new", rec.Header().Get("Location"), tc.name)
		assert.Equal(t, []string{tc.want}, creator.written, tc.name)
	}
}

// TestARefusedCouponComesBackAsTyped: a figure the panel cannot read is
// refused before the surface is asked, the module's refusal is drawn on the
// drafts' list, and either way the form keeps what was typed; a failure is
// not drawn as a refusal.
func TestARefusedCouponComesBackAsTyped(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		form url.Values
		says string
	}{
		"a percentage with three decimals": {url.Values{
			formCouponCode: {"ODD"}, formCouponMeasure: {"percentage"}, formCouponAmount: {"12.555"},
		}, "at most two decimal places"},
		"an amount finer than its currency": {url.Values{
			formCouponCode: {"ODD"}, formCouponMeasure: {"fixed"}, formCouponAmount: {"50.001"}, formCouponCurrency: {"TRY"},
		}, "decimal digits"},
		"a limit that is not a number": {url.Values{
			formCouponCode: {"ODD"}, formCouponMeasure: {"percentage"}, formCouponAmount: {"10"}, formCouponLimit: {"many"},
		}, "The usage limit is a whole number."},
	} {
		creator := &fakeCouponCreator{fakePromotions: fakePromotions{body: "[]"}}
		panel := couponPanel(t, creator)

		rec := promotionStatusRequest(panel, http.MethodPost, PromotionsPath, tc.form, scopePromotionRead, scopePromotionWrite)

		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, name)
		assert.Contains(t, rec.Body.String(), tc.says, name)
		assert.Contains(t, rec.Body.String(), `name="code" value="ODD"`, "%s: what was typed is kept", name)
		assert.Empty(t, creator.written, "%s: the surface is not asked", name)
	}

	creator := &fakeCouponCreator{fakePromotions: fakePromotions{body: "[]"}, err: errors.Conflict("promotion_duplicate", "a promotion with the code TAKEN exists")}
	panel := couponPanel(t, creator)
	rec := promotionStatusRequest(panel, http.MethodPost, PromotionsPath, url.Values{
		formCouponCode: {"TAKEN"}, formCouponMeasure: {"percentage"}, formCouponAmount: {"10"}, formCouponTarget: {"items"},
	}, scopePromotionRead, scopePromotionWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "a promotion with the code TAKEN exists")
	assert.Contains(t, body, `<option value="items" selected>`, "the choice is kept")
	assert.Equal(t, []string{"draft"}, creator.asked, "a refused coupon is drawn on the drafts' list")

	creator.err = errors.Unavailable("db_down", "no answer")
	rec = promotionStatusRequest(panel, http.MethodPost, PromotionsPath, url.Values{
		formCouponCode: {"TAKEN"}, formCouponMeasure: {"percentage"}, formCouponAmount: {"10"},
	}, scopePromotionRead, scopePromotionWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
