package adminui

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

func (f *fakeCampaigns) ReviseCampaign(
	_ context.Context, id, readName, readDescription string, readStartsAt, readEndsAt *time.Time,
	readBudgetLimit *int64, name, description string, startsAt, endsAt *time.Time, budgetLimit *int64,
) error {
	f.revised = append(f.revised, strings.Join([]string{
		id, readName, readDescription, momentText(readStartsAt), momentText(readEndsAt), limitWritten(readBudgetLimit),
		name, description, momentText(startsAt), momentText(endsAt), limitWritten(budgetLimit),
	}, "|"))
	return f.writeErr
}

// limitWritten prints a limit the fake was sent, "none" for none.
func limitWritten(limit *int64) string {
	if limit == nil {
		return "none"
	}

	return strconv.FormatInt(*limit, 10)
}

// threeCampaigns is a spend budget whose start has a fraction of a second, a
// use budget, and a campaign without a budget.
const threeCampaigns = `[
	{"id":"camp_1","name":"Black Friday","campaign_identifier":"BF-2026","description":"the November sale",
	 "starts_at":"2026-11-27T00:00:00.123456Z","ends_at":null,"budget_type":"spend",
	 "budget_limit":500000,"budget_used":1250,"budget_currency_code":"TRY"},
	{"id":"camp_2","name":"Loyalty","campaign_identifier":"LOYAL","description":"",
	 "starts_at":null,"ends_at":"2026-12-31T23:59:00Z","budget_type":"usage","budget_limit":100,"budget_used":3,
	 "budget_currency_code":""},
	{"id":"camp_3","name":"Plain","campaign_identifier":"PLAIN","description":"",
	 "starts_at":null,"ends_at":null,"budget_type":"none","budget_limit":null,"budget_used":0,
	 "budget_currency_code":""}]`

// campaignRowForm is the revise form of the campaign's row on a page.
func campaignRowForm(t *testing.T, body, id string) string {
	t.Helper()

	_, form, found := strings.Cut(body, `action="`+CampaignsPath+"/"+id+"?page=")
	require.True(t, found, "%s offers its form", id)
	form, _, _ = strings.Cut(form, "</form>")

	return form
}

// TestACampaignRowRevisesItsCampaign is ADR 0331: each row offers a writer
// the form that revises its campaign, carrying the name, the description, the
// window to the nanosecond and the limit in its unit as drawn, and a limit
// field only on a campaign with a budget, shown in the budget's unit; the
// surface is asked to revise the campaign from those, a limit read in that
// unit and an end typed as shown standing for the moment drawn, and the
// list's page names it; what the panel cannot read is not sent, and a refusal
// comes back in the row with what was typed, drawn from the campaign as it is
// now.
func TestACampaignRowRevisesItsCampaign(t *testing.T) {
	t.Parallel()

	campaigns := &fakeCampaigns{body: threeCampaigns, total: 30}
	panel := campaignsPanel(t, campaigns)
	writer := []string{scopePromotionRead, scopePromotionWrite}

	rec := campaignsRequest(panel, http.MethodGet, CampaignsPath+"?page=2", nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	form := campaignRowForm(t, body, "camp_1")
	assert.True(t, strings.HasPrefix(form, `2">`), "the form returns to the page it was drawn on")
	for _, want := range []string{
		`name="read_name" value="Black Friday"`, `name="read_description" value="the November sale"`,
		`name="read_starts_at" value="2026-11-27T00:00:00.123456Z"`, `name="read_ends_at" value=""`,
		`name="read_budget_limit" value="500000"`, `name="budget_type" value="spend"`,
		`name="budget_currency" value="TRY"`, `name="name" value="Black Friday"`,
		`rows="2">the November sale</textarea>`, `name="starts_at" value="2026-11-27T00:00"`,
		`name="budget_limit" value="5000.00"`,
	} {
		assert.Contains(t, form, want)
	}
	loyalty := campaignRowForm(t, body, "camp_2")
	assert.Contains(t, loyalty, `name="budget_limit" value="100"`, "a use budget in uses")
	assert.Contains(t, loyalty, `name="read_ends_at" value="2026-12-31T23:59:00Z"`)
	assert.NotContains(t, campaignRowForm(t, body, "camp_3"), `name="budget_limit"`, "no budget, no limit")
	assert.NotContains(t, body, "<details open>", "no row is open until a refusal opens it")
	rec = campaignsRequest(panel, http.MethodGet, CampaignsPath, nil, scopePromotionRead)
	assert.NotContains(t, rec.Body.String(), "Revise", "a reader revises nothing")
	listing := campaignsPanel(t, listingCampaigns{&fakeCampaigns{body: threeCampaigns}})
	rec = campaignsRequest(listing, http.MethodGet, CampaignsPath, nil, writer...)
	assert.NotContains(t, rec.Body.String(), "Revise", "a surface that cannot revise offers no form")
	rec = campaignsRequest(listing, http.MethodPost, CampaignsPath+"/camp_1", url.Values{formCampaignName: {"X"}}, scopePromotionWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	sent := url.Values{
		formReadName: {"Black Friday"}, formReadDescription: {"the November sale"},
		formReadStarts: {"2026-11-27T00:00:00.123456Z"}, formReadBudgetLimit: {"500000"},
		formBudgetType: {"spend"}, formBudgetCurrency: {"TRY"},
		formCampaignName: {" Black Friday 2026 "}, formCampaignDesc: {" the\r\nsale "},
		formCampaignStarts: {"2026-11-27T00:00"}, formCampaignEnds: {"2026-12-01T00:00"}, formBudgetLimit: {" 7500.50 "},
	}
	rec = campaignsRequest(panel, http.MethodPost, CampaignsPath+"/camp_1?page=2", sent, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, CampaignsPath+"?created=Black+Friday+2026&page=2", rec.Header().Get("Location"))
	assert.Equal(t, []string{
		"camp_1|Black Friday|the November sale|2026-11-27T00:00:00.123456Z|open|500000|" +
			"Black Friday 2026|the\nsale|2026-11-27T00:00:00.123456Z|2026-12-01T00:00:00Z|750050",
	}, campaigns.revised, "a money limit in its decimals, the start typed as shown the start drawn")

	rec = campaignsRequest(panel, http.MethodPost, CampaignsPath+"/camp_2?page=1", url.Values{
		formReadName: {"Loyalty"}, formReadEnds: {"2026-12-31T23:59:00Z"}, formReadBudgetLimit: {"100"},
		formBudgetType: {"usage"}, formCampaignName: {"Loyalty"}, formBudgetLimit: {"250"},
	}, writer...)
	assert.Equal(t, CampaignsPath+"?created=Loyalty", rec.Header().Get("Location"), "the first page is the list")
	assert.Equal(t, "camp_2|Loyalty||open|2026-12-31T23:59:00Z|100|Loyalty||open|open|250", campaigns.revised[1],
		"a use limit as a count, an emptied end open")
	campaignsRequest(panel, http.MethodPost, CampaignsPath+"/camp_3", url.Values{
		formReadName: {"Plain"}, formBudgetType: {"none"}, formCampaignName: {"Plainer"},
	}, writer...)
	assert.Equal(t, "camp_3|Plain||open|open|none|Plainer||open|open|none", campaigns.revised[2], "no budget, no limit")

	for reason, form := range map[string]url.Values{
		"The budget the row was drawn with could not be read":           {formReadBudgetLimit: {"5000.00"}, formCampaignName: {"X"}},
		"The window the row was drawn with could not be read":           {formReadEnds: {"tomorrow"}, formCampaignName: {"X"}},
		"The campaign&#39;s start could not be read":                    {formCampaignName: {"X"}, formCampaignStarts: {"soon"}},
		"A budget in uses is a whole number.":                           {formBudgetType: {"usage"}, formBudgetLimit: {"2.5"}},
		"This currency has 2 decimal digits; &#34;1.234&#34; has more.": {formBudgetType: {"spend"}, formBudgetCurrency: {"TRY"}, formBudgetLimit: {"1.234"}},
	} {
		rec = campaignsRequest(panel, http.MethodPost, CampaignsPath+"/camp_1", form, writer...)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, reason)
		assert.Contains(t, rec.Body.String(), reason)
	}
	assert.Len(t, campaigns.revised, 3, "what the panel cannot read is not sent")

	campaigns.writeErr = errors.Conflict("promotion_campaign_revised",
		`campaign camp_1 was revised since it was read: it is "Black Friday" now; draw the list again`)
	rec = campaignsRequest(panel, http.MethodPost, CampaignsPath+"/camp_1", url.Values{
		formReadName: {"Cyber Monday"}, formBudgetType: {"spend"}, formBudgetCurrency: {"TRY"},
		formCampaignName: {"Gold"}, formCampaignDesc: {"typed"}, formCampaignStarts: {"2026-02-01T00:00"},
		formCampaignEnds: {"2026-03-01T00:00"}, formBudgetLimit: {"10"},
	}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body = rec.Body.String()
	assert.Contains(t, body, "draw the list again")
	form = campaignRowForm(t, body, "camp_1")
	for _, want := range []string{
		`name="read_name" value="Black Friday"`, `name="read_budget_limit" value="500000"`,
		`name="name" value="Gold"`, `rows="2">typed</textarea>`, `name="starts_at" value="2026-02-01T00:00"`,
		`name="ends_at" value="2026-03-01T00:00"`, `name="budget_limit" value="10"`,
	} {
		assert.Contains(t, form, want, "the row carries the campaign as it is now and what was typed")
	}
	assert.Contains(t, body, "<details open>", "the refused row is open")
	assert.Contains(t, campaignRowForm(t, body, "camp_2"), `name="name" value="Loyalty"`, "another row is as drawn")
	newCampaign, _, _ := strings.Cut(body, "<table>")
	assert.NotContains(t, newCampaign, "Gold", "what was typed is the row's, not the new campaign's")

	campaigns.writeErr = errors.NotFound("promotion_campaign_not_found", "campaign not found: camp_1")
	rec = campaignsRequest(panel, http.MethodPost, CampaignsPath+"/camp_1", sent, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "campaign not found: camp_1")
	campaigns.writeErr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, CampaignsPath+"/camp_1", sent, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
