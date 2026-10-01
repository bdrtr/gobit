//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
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
