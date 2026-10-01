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
