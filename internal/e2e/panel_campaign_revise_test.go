//go:build integration

package e2e

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	promotionsvc "github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// TestAnOperatorRevisesACampaignInThePanel is ADR 0331 on the production
// wiring: a campaign's row carries its terms as drawn, its start to the
// microsecond the database holds and its money limit in minor units; the
// row's form renames the campaign and raises its budget through the
// registered `promotion.admin` surface, the start typed as it was shown
// staying the moment it was; and the same form sent again, now stale, is
// refused on the list with the campaign as it is now.
func TestAnOperatorRevisesACampaignInThePanel(t *testing.T) {
	ctx := t.Context()
	n := fixtureCounter.Add(1)
	starts := time.Date(2027, 2, 1, 9, 30, 15, 123456000, time.UTC)
	limit := int64(500000)
	created, err := promotionSvc.CreateCampaign(ctx, promotionsvc.CampaignInput{
		Name: fmt.Sprintf("E2E Season %d", n), CampaignIdentifier: fmt.Sprintf("E2E-SEASON-%d", n),
		Description: "before", StartsAt: &starts, BudgetType: models.BudgetSpend, BudgetLimit: &limit,
		BudgetCurrencyCode: "TRY",
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
	marker := `action="` + adminui.CampaignsPath + "/" + created.ID + "?page="
	// drawn is the form the campaign's row carries: its action and the
	// fields it was drawn with.
	drawn := func(page string) (string, url.Values) {
		t.Helper()

		_, form, found := strings.Cut(page, marker)
		require.True(t, found, "the campaign's row offers its form")
		query, form, _ := strings.Cut(form, `"`)
		form, _, _ = strings.Cut(form, "</form>")
		values := url.Values{}
		for _, field := range []string{
			"read_name", "read_description", "read_starts_at", "read_ends_at", "read_budget_limit",
			"budget_type", "budget_currency", "name", "starts_at", "ends_at", "budget_limit",
		} {
			_, value, ok := strings.Cut(form, `name="`+field+`" value="`)
			require.True(t, ok, field)
			value, _, _ = strings.Cut(value, `"`)
			values.Set(field, html.UnescapeString(value))
		}

		return adminui.CampaignsPath + "/" + created.ID + "?page=" + html.UnescapeString(query), values
	}

	// The campaigns come in the order they were written; the campaign is on
	// whichever page holds it.
	var page string
	for number := 1; number <= 100 && !strings.Contains(page, marker); number++ {
		page = send(http.MethodGet, adminui.CampaignsPath+"?page="+strconv.Itoa(number), nil).Body.String()
	}
	action, form := drawn(page)
	assert.Equal(t, "2027-02-01T09:30:15.123456Z", form.Get("read_starts_at"), "the start to the microsecond")
	assert.Equal(t, "500000", form.Get("read_budget_limit"), "the limit in minor units")

	renamed := fmt.Sprintf("E2E Season Renamed %d", n)
	form.Set("name", renamed)
	form.Set("description", "after")
	form.Set("budget_limit", "7500.50")
	revised := send(http.MethodPost, action, form)
	require.Equal(t, http.StatusSeeOther, revised.Code, revised.Body.String())
	assert.Contains(t, send(http.MethodGet, revised.Header().Get("Location"), nil).Body.String(),
		"Campaign "+renamed+" was written.")
	campaign, err := promotionSvc.GetCampaign(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, renamed+"|after", campaign.Name+"|"+campaign.Description)
	require.NotNil(t, campaign.StartsAt)
	assert.True(t, starts.Equal(*campaign.StartsAt), "the start typed as shown stays the moment it was: %s", campaign.StartsAt)
	require.NotNil(t, campaign.BudgetLimit)
	assert.Equal(t, int64(750050), *campaign.BudgetLimit, "the limit read in the currency's decimals")

	stale := send(http.MethodPost, action, form)
	require.Equal(t, http.StatusUnprocessableEntity, stale.Code, stale.Body.String())
	assert.Contains(t, stale.Body.String(), "draw the list again")
	_, now := drawn(stale.Body.String())
	assert.Equal(t, renamed+"|750050", now.Get("read_name")+"|"+now.Get("read_budget_limit"),
		"the row is drawn from the campaign as it is now")
}
