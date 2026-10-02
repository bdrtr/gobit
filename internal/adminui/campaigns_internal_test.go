package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// fakeCampaigns lists as scripted and records each listing and write.
type fakeCampaigns struct {
	body     string
	total    int64
	pages    [][2]int32
	written  []campaignWrite
	writeErr error
	revised  []string
}

// campaignWrite is one write the fake was asked for.
type campaignWrite struct {
	name, identifier, description string
	startsAt, endsAt              *time.Time
	budgetType                    string
	limit                         *int64
	currency                      string
}

func (f *fakeCampaigns) PromotionsJSON(context.Context, string, int32, int32) (json.RawMessage, int64, error) {
	return json.RawMessage(`[]`), 0, nil
}

func (f *fakeCampaigns) CampaignsJSON(_ context.Context, limit, offset int32) (json.RawMessage, int64, error) {
	f.pages = append(f.pages, [2]int32{limit, offset})
	return json.RawMessage(f.body), f.total, nil
}

func (f *fakeCampaigns) CreateCampaign(
	_ context.Context, name, identifier, description string, startsAt, endsAt *time.Time,
	budgetType string, budgetLimit *int64, currency string,
) (string, error) {
	f.written = append(f.written, campaignWrite{
		name: name, identifier: identifier, description: description, startsAt: startsAt, endsAt: endsAt,
		budgetType: budgetType, limit: budgetLimit, currency: currency,
	})
	return "camp_1", f.writeErr
}

// listingCampaigns lists and cannot write: it holds the fake rather than
// embedding it, so the write is not promoted.
type listingCampaigns struct{ campaigns *fakeCampaigns }

func (l listingCampaigns) PromotionsJSON(ctx context.Context, status string, limit, offset int32) (json.RawMessage, int64, error) {
	return l.campaigns.PromotionsJSON(ctx, status, limit, offset)
}

func (l listingCampaigns) CampaignsJSON(ctx context.Context, limit, offset int32) (json.RawMessage, int64, error) {
	return l.campaigns.CampaignsJSON(ctx, limit, offset)
}

// twoCampaigns is a spend budget with a window and a use budget without one.
const twoCampaigns = `[
	{"id":"camp_1","name":"Black Friday","campaign_identifier":"BF-2026","description":"the November sale",
	 "starts_at":"2026-11-27T00:00:00Z","ends_at":"2026-11-30T23:59:00Z","budget_type":"spend",
	 "budget_limit":500000,"budget_used":1250,"budget_currency_code":"TRY"},
	{"id":"camp_2","name":"Loyalty","campaign_identifier":"LOYAL","description":"",
	 "starts_at":null,"ends_at":null,"budget_type":"usage","budget_limit":100,"budget_used":3,
	 "budget_currency_code":""}]`

// campaignsPanel is a panel over the campaigns in a shop selling in TRY, its
// scale known.
func campaignsPanel(t *testing.T, promotions PromotionLister) *UI {
	t.Helper()

	panel := newCatalogPanel(t, &fakeCatalog{byEntity: map[string][]query.Record{EntityRegion: {{
		"id": "reg_1", "currency_code": "TRY", "currency": map[string]any{"code": "TRY", "decimal_digits": int64(2)},
	}}}})
	panel.promotions = promotions
	panel.scopes = builtInScopes()

	return panel
}

// campaignsRequest sends one request through the panel's routes.
func campaignsRequest(panel *UI, method, path string, form url.Values, scopes ...string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	panel.Routes(r)

	request := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = request.WithContext(corehttp.WithPrincipal(request.Context(),
		corehttp.Principal{ID: "user_1", Kind: "user", Scopes: scopes}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, request)

	return rec
}

// TestTheCampaignsScreenListsTheWindowsAndBudgets is ADR 0319: each campaign
// with its identifier, its window in UTC or open, and how much of its budget
// is used, in uses or in its currency's decimals; the form is offered to a
// writer whose surface can write.
func TestTheCampaignsScreenListsTheWindowsAndBudgets(t *testing.T) {
	t.Parallel()

	campaigns := &fakeCampaigns{body: twoCampaigns, total: 26}
	panel := campaignsPanel(t, campaigns)

	rec := campaignsRequest(panel, http.MethodGet, CampaignsPath, nil, scopePromotionRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	for _, want := range []string{
		"Black Friday", "BF-2026", "the November sale", "2026-11-27 00:00", "2026-11-30 23:59",
		"12.50 TRY of 5000.00 TRY", "3 of 100 uses", "26 campaigns.", `href="` + CampaignsPath + `?page=2"`,
	} {
		assert.Contains(t, body, want)
	}
	assert.Contains(t, body, `<span class="muted">always</span>`, "an open start")
	assert.Contains(t, body, `<span class="muted">open</span>`, "an open end")
	assert.Equal(t, [][2]int32{{campaignsPerPage, 0}}, campaigns.pages)
	assert.NotContains(t, body, "New campaign", "a reader writes nothing")

	campaignsRequest(panel, http.MethodGet, CampaignsPath+"?page=2", nil, scopePromotionRead)
	assert.Equal(t, [2]int32{campaignsPerPage, campaignsPerPage}, campaigns.pages[1])
	onePage := campaignsPanel(t, &fakeCampaigns{body: twoCampaigns, total: campaignsPerPage})
	rec = campaignsRequest(onePage, http.MethodGet, CampaignsPath, nil, scopePromotionRead)
	assert.NotContains(t, rec.Body.String(), "page=2", "one page has no next")

	rec = campaignsRequest(panel, http.MethodGet, CampaignsPath, nil, scopePromotionRead, scopePromotionWrite)
	assert.Contains(t, rec.Body.String(), "New campaign")
	listing := campaignsPanel(t, listingCampaigns{&fakeCampaigns{body: twoCampaigns}})
	rec = campaignsRequest(listing, http.MethodGet, CampaignsPath, nil, scopePromotionRead, scopePromotionWrite)
	assert.NotContains(t, rec.Body.String(), "New campaign", "a surface that cannot write offers no form")
	rec = campaignsRequest(listing, http.MethodPost, CampaignsPath, url.Values{}, scopePromotionWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	rec = campaignsRequest(campaignsPanel(t, nil), http.MethodGet, CampaignsPath, nil, scopePromotionRead)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "no surface, no list")
}

// TestTheCampaignFormWritesWhatWasTyped: the moments are read in UTC, a money
// budget in its currency's decimals and a use budget as a count, the text
// trimmed, and the list it lands on names the campaign.
func TestTheCampaignFormWritesWhatWasTyped(t *testing.T) {
	t.Parallel()

	campaigns := &fakeCampaigns{body: twoCampaigns, total: 2}
	panel := campaignsPanel(t, campaigns)
	writer := []string{scopePromotionRead, scopePromotionWrite}

	rec := campaignsRequest(panel, http.MethodPost, CampaignsPath, url.Values{
		formCampaignName: {" Black Friday "}, formCampaignIdentifier: {" BF-2026 "}, formCampaignDesc: {" sale "},
		formCampaignStarts: {"2026-11-27T00:00"}, formCampaignEnds: {"2026-11-30T23:59"},
		formBudgetType: {budgetSpend}, formBudgetLimit: {"5000.50"}, formBudgetCurrency: {" try "},
	}, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, CampaignsPath+"?created=BF-2026", rec.Header().Get("Location"))
	require.Len(t, campaigns.written, 1)
	written := campaigns.written[0]
	assert.Equal(t, "Black Friday|BF-2026|sale", written.name+"|"+written.identifier+"|"+written.description)
	require.NotNil(t, written.startsAt)
	require.NotNil(t, written.endsAt)
	assert.Equal(t, time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC), *written.startsAt)
	assert.Equal(t, time.Date(2026, 11, 30, 23, 59, 0, 0, time.UTC), *written.endsAt)
	require.NotNil(t, written.limit)
	assert.Equal(t, int64(500050), *written.limit, "the money budget in minor units")
	assert.Equal(t, "spend|TRY", written.budgetType+"|"+written.currency)

	landed := campaignsRequest(panel, http.MethodGet, rec.Header().Get("Location"), nil, scopePromotionRead)
	assert.Contains(t, landed.Body.String(), "Campaign BF-2026 was written.")

	campaignsRequest(panel, http.MethodPost, CampaignsPath, url.Values{
		formCampaignName: {"Loyalty"}, formCampaignIdentifier: {"LOYAL"},
		formBudgetType: {budgetUsage}, formBudgetLimit: {"100"},
	}, writer...)
	require.Len(t, campaigns.written, 2)
	uses := campaigns.written[1]
	assert.Nil(t, uses.startsAt, "an empty start leaves the window open")
	assert.Nil(t, uses.endsAt)
	require.NotNil(t, uses.limit)
	assert.Equal(t, int64(100), *uses.limit, "a use budget is a count")

	campaignsRequest(panel, http.MethodPost, CampaignsPath, url.Values{
		formCampaignName: {"Open"}, formCampaignIdentifier: {"OPEN"}, formBudgetType: {"none"},
	}, writer...)
	require.Len(t, campaigns.written, 3)
	assert.Nil(t, campaigns.written[2].limit, "no budget, no limit")
}

// TestARefusedCampaignComesBackWithWhatWasTyped: the panel's own refusals and
// the module's are drawn on the list with the form open and filled, to a
// writer who cannot read as the reason alone, and a failure is not a refusal.
func TestARefusedCampaignComesBackWithWhatWasTyped(t *testing.T) {
	t.Parallel()

	campaigns := &fakeCampaigns{body: twoCampaigns, total: 2}
	panel := campaignsPanel(t, campaigns)
	writer := []string{scopePromotionRead, scopePromotionWrite}
	typed := url.Values{
		formCampaignName: {"Summer"}, formCampaignIdentifier: {"SUMMER"},
		formBudgetType: {budgetUsage}, formBudgetLimit: {"ten"},
	}

	rec := campaignsRequest(panel, http.MethodPost, CampaignsPath, typed, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "A budget in uses is a whole number.")
	assert.Contains(t, rec.Body.String(), `value="SUMMER"`, "what was typed comes back")
	assert.Contains(t, rec.Body.String(), "<details open>")
	assert.Empty(t, campaigns.written, "a form the panel cannot read is not sent")

	typed.Set(formBudgetLimit, "10")
	typed.Set(formCampaignStarts, "next week")
	rec = campaignsRequest(panel, http.MethodPost, CampaignsPath, typed, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "The campaign&#39;s start could not be read")
	typed.Set(formCampaignStarts, "")
	typed.Set(formCampaignEnds, "never")
	rec = campaignsRequest(panel, http.MethodPost, CampaignsPath, typed, writer...)
	assert.Contains(t, rec.Body.String(), "The campaign&#39;s end could not be read")
	assert.Empty(t, campaigns.written)

	typed.Set(formCampaignEnds, "")
	campaigns.writeErr = errors.Conflict("promotion_duplicate", "a campaign with the identifier SUMMER exists")
	rec = campaignsRequest(panel, http.MethodPost, CampaignsPath, typed, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "a campaign with the identifier SUMMER exists")
	rec = campaignsRequest(panel, http.MethodPost, CampaignsPath, typed, scopePromotionWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.NotContains(t, rec.Body.String(), "Black Friday", "a writer who cannot read is shown none of the list")

	campaigns.writeErr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, CampaignsPath, typed, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
