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
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	promotionsvc "github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// TestAnOperatorChangesADiscountInThePanel is ADR 0338 on the production
// wiring: a promotion's page offers its discount's value as a percent through
// the registered `promotion.admin` surface; the form writes the percent typed
// as basis points from the type and the value read, and the same form sent
// again, now stale, is refused on the page.
func TestAnOperatorChangesADiscountInThePanel(t *testing.T) {
	ctx := t.Context()
	promo, err := promotionSvc.CreateCoupon(ctx, promotionsvc.CouponInput{
		Code: fmt.Sprintf("E2EDISC%d", fixtureCounter.Add(1)),
		Method: promotionsvc.ApplicationMethodInput{
			Type: models.MethodPercentage, TargetType: models.TargetItems, Value: 1000,
		},
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

	pagePath := adminui.PromotionsPath + "/" + promo.ID
	page := send(http.MethodGet, pagePath, nil).Body.String()
	_, form, found := strings.Cut(page, `action="`+pagePath+`/discount"`)
	require.True(t, found, "the page offers the discount form")
	form, _, _ = strings.Cut(form, "</form>")
	for _, want := range []string{`name="read_type" value="percentage"`, `name="read_value" value="1000"`, `name="value" value="10"`} {
		assert.Contains(t, form, want)
	}

	sent := url.Values{"read_type": {"percentage"}, "read_value": {"1000"}, "value": {"15"}}
	written := send(http.MethodPost, pagePath+"/discount", sent)
	require.Equal(t, http.StatusSeeOther, written.Code, written.Body.String())
	assert.Contains(t, send(http.MethodGet, written.Header().Get("Location"), nil).Body.String(), "The discount was written.")
	method, err := promotionSvc.GetApplicationMethod(ctx, promo.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1500), method.Value, "the percent typed, as basis points")

	stale := send(http.MethodPost, pagePath+"/discount", sent)
	require.Equal(t, http.StatusUnprocessableEntity, stale.Code, stale.Body.String())
	assert.Contains(t, stale.Body.String(), "draw the page again")
	assert.Contains(t, stale.Body.String(), `name="read_value" value="1500"`, "drawn from the discount as it is now")
}
