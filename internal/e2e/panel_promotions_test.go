//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
	promotionmodels "github.com/bdrtr/gobit/internal/modules/promotion/models"
	promotionsvc "github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// TestAnOperatorListsThePromotionsInThePanel is ADR 0311 on the production
// wiring: the panel built from this harness's container lists a draft coupon
// on the drafts' tab, through the promotion module's registered surface, with
// its usage limit — a draft the read provider never returns.
func TestAnOperatorListsThePromotionsInThePanel(t *testing.T) {
	ctx := t.Context()
	code := fmt.Sprintf("E2EPANEL%d", fixtureCounter.Add(1))
	limit := int64(40)
	_, err := promotionSvc.CreatePromotion(ctx, promotionsvc.PromotionInput{
		Code: code, Status: promotionmodels.PromotionDraft, UsageLimit: &limit,
	})
	require.NoError(t, err)

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)

	req := httptest.NewRequest(http.MethodGet, adminui.PromotionsPath+"?status=draft", http.NoBody)
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
		ID: "usr_marketing", Kind: "user", Scopes: []string{"promotion:read"},
	}))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), code)
	assert.Contains(t, rec.Body.String(), "0 of 40")
}

// TestAnOperatorPublishesADraftInThePanel is ADR 0312 on the production
// wiring: the drafts' tab offers the coupon's publish form, posting it
// moves the coupon to active through the promotion module's registered
// surface, and posting the same form again is refused on the list, since
// the coupon is no longer the draft the form read.
func TestAnOperatorPublishesADraftInThePanel(t *testing.T) {
	ctx := t.Context()
	code := fmt.Sprintf("E2EPUBLISH%d", fixtureCounter.Add(1))
	draft, err := promotionSvc.CreatePromotion(ctx, promotionsvc.PromotionInput{
		Code: code, Status: promotionmodels.PromotionDraft,
	})
	require.NoError(t, err)

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_marketing", Kind: "user", Scopes: []string{"promotion:read", "promotion:write"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	drafts := send(http.MethodGet, adminui.PromotionsPath+"?status=draft", nil)
	require.Equal(t, http.StatusOK, drafts.Code, drafts.Body.String())
	action := adminui.PromotionsPath + "/" + draft.ID + "/status"
	assert.Contains(t, drafts.Body.String(), `action="`+action+`"`)

	form := url.Values{"from": {"draft"}, "to": {"active"}}
	published := send(http.MethodPost, action, form)
	require.Equal(t, http.StatusSeeOther, published.Code, published.Body.String())
	promo, err := promotionSvc.GetPromotion(ctx, draft.ID)
	require.NoError(t, err)
	assert.Equal(t, promotionmodels.PromotionActive, promo.Status)

	again := send(http.MethodPost, action, form)
	require.Equal(t, http.StatusUnprocessableEntity, again.Code, again.Body.String())
	assert.Contains(t, again.Body.String(), "is active now, not draft")
}

// TestAnOperatorOpensAPromotionsPage is ADR 0313 on the production wiring:
// the list links a coupon to its page, and the page shows what the coupon
// gives, its rule and its use, read through the promotion module's
// registered surface.
func TestAnOperatorOpensAPromotionsPage(t *testing.T) {
	ctx := t.Context()
	code := fmt.Sprintf("E2EPAGE%d", fixtureCounter.Add(1))
	promo, err := promotionSvc.CreatePromotion(ctx, promotionsvc.PromotionInput{
		Code: code, Status: promotionmodels.PromotionActive,
	})
	require.NoError(t, err)
	_, err = promotionSvc.SetApplicationMethod(ctx, promo.ID, promotionsvc.ApplicationMethodInput{
		Type: promotionmodels.MethodPercentage, TargetType: promotionmodels.TargetOrder,
		Allocation: promotionmodels.AllocationAcross, Value: 1500,
	})
	require.NoError(t, err)
	_, err = promotionSvc.AddPromotionRule(ctx, promo.ID, promotionsvc.RuleInput{
		RuleType: promotionmodels.RuleContext, Attribute: "currency_code",
		Operator: promotionmodels.OpIn, Values: []string{"TRY"},
	})
	require.NoError(t, err)
	_, err = promotionSvc.RedeemPromotion(ctx, promotionsvc.RedeemInput{
		PromotionID: promo.ID, Reference: "order_e2e_page", Amount: 300, CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	// Paused after its use, so no other test's cart can meet it.
	_, err = promotionSvc.SwitchPromotionStatus(ctx, promo.ID,
		promotionmodels.PromotionActive, promotionmodels.PromotionInactive)
	require.NoError(t, err)

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_marketing", Kind: "user", Scopes: []string{"promotion:read"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	list := get(adminui.PromotionsPath + "?status=inactive")
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	pagePath := adminui.PromotionsPath + "/" + promo.ID
	assert.Contains(t, list.Body.String(), `href="`+pagePath+`"`)

	page := get(pagePath)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	body := page.Body.String()
	assert.Contains(t, body, "<h1>"+code+"</h1>")
	assert.Contains(t, body, "15% off")
	assert.Contains(t, body, "the order, spread across them")
	assert.Contains(t, body, "<td>currency_code</td><td>in</td><td>TRY</td>")
	assert.Contains(t, body, "order_e2e_page")
}
