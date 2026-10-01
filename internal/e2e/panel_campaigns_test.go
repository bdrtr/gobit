//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
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

// campaignsTotal reads the count the campaigns' list prints.
var campaignsTotal = regexp.MustCompile(`(\d+) campaigns\.`)

// TestAnOperatorWritesACampaignInThePanel is ADR 0319 on the production
// wiring: the Campaigns form writes a campaign with a money budget through the
// promotion module's registered surface, the list names it and prints its
// window and its budget in the currency's decimals, and the same identifier
// is refused by name.
func TestAnOperatorWritesACampaignInThePanel(t *testing.T) {
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

	identifier := fmt.Sprintf("E2E-CAMPAIGN-%d", fixtureCounter.Add(1))
	form := url.Values{
		"name": {"E2E Black Friday"}, "campaign_identifier": {identifier},
		"starts_at": {"2026-11-27T00:00"}, "ends_at": {"2026-11-30T23:59"},
		"budget_type": {"spend"}, "budget_limit": {"5000"}, "budget_currency": {taxedCurrency},
	}
	written := send(http.MethodPost, adminui.CampaignsPath, form)
	require.Equal(t, http.StatusSeeOther, written.Code, written.Body.String())
	landed := send(http.MethodGet, written.Header().Get("Location"), nil).Body.String()
	assert.Contains(t, landed, "Campaign "+identifier+" was written.")

	count := campaignsTotal.FindStringSubmatch(landed)
	require.Len(t, count, 2, "the list prints its count")
	total, err := strconv.Atoi(count[1])
	require.NoError(t, err)
	last := send(http.MethodGet, adminui.CampaignsPath+"?page="+strconv.Itoa((total+24)/25), nil).Body.String()
	_, row, found := strings.Cut(last, identifier)
	require.True(t, found, "the campaign is on the list's last page")
	assert.Contains(t, last, "E2E Black Friday")
	assert.Contains(t, row, "2026-11-27 00:00")
	assert.Contains(t, row, "2026-11-30 23:59")
	assert.Contains(t, row, "0.00 "+taxedCurrency+" of 5000.00 "+taxedCurrency, "the budget in the currency's decimals")

	refused := send(http.MethodPost, adminui.CampaignsPath, form)
	require.Equal(t, http.StatusUnprocessableEntity, refused.Code, refused.Body.String())
	assert.Contains(t, refused.Body.String(), "a campaign with the identifier "+identifier+" exists")
}

// TestAnOperatorPutsACouponIntoACampaignInThePanel is ADR 0320 on the
// production wiring: a coupon written in the panel is offered the live
// campaigns on its page, put into one from none, and its page then shows the
// campaign with its budget and carries it as the campaign it was read in.
func TestAnOperatorPutsACouponIntoACampaignInThePanel(t *testing.T) {
	ctx := t.Context()
	seq := fixtureCounter.Add(1)
	limit := int64(500000)
	campaign, err := promotionSvc.CreateCampaign(ctx, promotionsvc.CampaignInput{
		Name: "E2E Winter", CampaignIdentifier: fmt.Sprintf("E2E-WINTER-%d", seq),
		BudgetType: promotionmodels.BudgetSpend, BudgetLimit: &limit, BudgetCurrencyCode: taxedCurrency,
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

	written := send(http.MethodPost, adminui.PromotionsPath, url.Values{
		"code": {fmt.Sprintf("E2EWINTER%d", seq)}, "measure": {"percentage"}, "amount": {"10"},
		"target": {"order"}, "allocation": {"across"},
	})
	require.Equal(t, http.StatusSeeOther, written.Code, written.Body.String())
	pagePath := written.Header().Get("Location")

	offered := send(http.MethodGet, pagePath, nil).Body.String()
	assert.Contains(t, offered, `<option value="`+campaign.ID+`">E2E Winter (`+campaign.CampaignIdentifier+`)</option>`)
	assert.Contains(t, offered, `<input type="hidden" name="from" value="">`, "read in no campaign")

	placed := send(http.MethodPost, pagePath+"/campaign", url.Values{"from": {""}, "campaign_id": {campaign.ID}})
	require.Equal(t, http.StatusSeeOther, placed.Code, placed.Body.String())
	promo, err := promotionSvc.GetPromotion(ctx, strings.TrimPrefix(pagePath, adminui.PromotionsPath+"/"))
	require.NoError(t, err)
	require.NotNil(t, promo.CampaignID)
	assert.Equal(t, campaign.ID, *promo.CampaignID)

	page := send(http.MethodGet, pagePath, nil).Body.String()
	assert.Contains(t, page, "<td>E2E Winter</td>")
	assert.Contains(t, page, "0.00 "+taxedCurrency+" of 5000.00 "+taxedCurrency)
	assert.Contains(t, page, `<input type="hidden" name="from" value="`+campaign.ID+`">`, "read in the campaign now")

	stale := send(http.MethodPost, pagePath+"/campaign", url.Values{"from": {""}, "campaign_id": {campaign.ID}})
	require.Equal(t, http.StatusUnprocessableEntity, stale.Code, stale.Body.String())
	assert.Contains(t, stale.Body.String(), "draw the page again", "a form read before the move is refused")
}
