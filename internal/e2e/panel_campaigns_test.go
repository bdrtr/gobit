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
