package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// fakePlacer reads a promotion's page, lists the campaigns and records each
// campaign it is asked to set.
type fakePlacer struct {
	*fakePromotionReader
	campaigns string
	total     int64
	listErr   error
	listed    [][2]int32
	set       []string
	setErr    error
}

func (f *fakePlacer) CampaignsJSON(_ context.Context, limit, offset int32) (json.RawMessage, int64, error) {
	f.listed = append(f.listed, [2]int32{limit, offset})
	return json.RawMessage(f.campaigns), f.total, f.listErr
}

func (f *fakePlacer) SetPromotionCampaign(_ context.Context, id, from, to string) error {
	f.set = append(f.set, id+"|"+from+"|"+to)
	return f.setErr
}

// readingOnly reads the page and lists the campaigns, and cannot set one.
type readingOnly struct {
	*fakePromotionReader
	placer *fakePlacer
}

func (r readingOnly) CampaignsJSON(ctx context.Context, limit, offset int32) (json.RawMessage, int64, error) {
	return r.placer.CampaignsJSON(ctx, limit, offset)
}

// promotionInSpring is a coupon in the spring campaign.
const promotionInSpring = `{
	"id":"promo_1","code":"SPRING","is_automatic":false,"type":"standard","status":"active",
	"usage_count":0,"usage_limit":null,"created_at":"2026-09-01T10:00:00Z","campaign_id":"camp_spring",
	"campaign":{"id":"camp_spring","name":"Spring sale","campaign_identifier":"SPRING-2026",
		"starts_at":null,"ends_at":null,"budget_type":"none","budget_limit":null,"budget_used":0,
		"budget_currency_code":""},
	"application_method":null,"rules":[],"latest_uses":[]}`

// promotionInADeletedCampaign names a campaign that was deleted, which reads
// as null.
const promotionInADeletedCampaign = `{
	"id":"promo_1","code":"SPRING","is_automatic":false,"type":"standard","status":"active",
	"usage_count":0,"usage_limit":null,"created_at":"2026-09-01T10:00:00Z","campaign_id":"camp_gone",
	"campaign":null,"application_method":null,"rules":[],"latest_uses":[]}`

// twoSeasons is the campaigns the page offers.
const twoSeasons = `[
	{"id":"camp_spring","name":"Spring sale","campaign_identifier":"SPRING-2026","budget_type":"none"},
	{"id":"camp_summer","name":"Summer sale","campaign_identifier":"SUMMER-2026","budget_type":"none"}]`

// TestAPromotionsPageOffersTheCampaigns is ADR 0320: a writer is offered the
// campaigns with the promotion's own chosen, the campaign the page was drawn
// with carried in the form, and none; a reader, a surface that cannot set one
// and a failed listing offer nothing.
func TestAPromotionsPageOffersTheCampaigns(t *testing.T) {
	t.Parallel()

	placer := &fakePlacer{fakePromotionReader: &fakePromotionReader{page: promotionInSpring},
		campaigns: twoSeasons, total: 2}
	panel := campaignsPanel(t, placer)
	page := PromotionsPath + "/promo_1"

	rec := campaignsRequest(panel, http.MethodGet, page, nil, scopePromotionRead, scopePromotionWrite)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, `action="`+page+`/campaign"`)
	assert.Contains(t, body, `<input type="hidden" name="from" value="camp_spring">`)
	assert.Contains(t, body, `<option value="camp_spring" selected>Spring sale (SPRING-2026)</option>`)
	assert.Contains(t, body, `<option value="camp_summer">Summer sale (SUMMER-2026)</option>`)
	assert.Contains(t, body, `<option value="">no campaign</option>`)
	assert.Equal(t, [][2]int32{{campaignChoices, 0}}, placer.listed)
	assert.NotContains(t, body, "Only the first", "every campaign is offered")

	placer.total = campaignChoices + 1
	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopePromotionRead, scopePromotionWrite)
	assert.Contains(t, rec.Body.String(), "Only the first 100 campaigns are offered.")

	rec = campaignsRequest(panel, http.MethodGet, page, nil, scopePromotionRead)
	assert.NotContains(t, rec.Body.String(), "/campaign\"", "a reader sets nothing")
	assert.Len(t, placer.listed, 2, "the campaigns are not read for a reader")

	reading := campaignsPanel(t, readingOnly{fakePromotionReader: placer.fakePromotionReader, placer: placer})
	rec = campaignsRequest(reading, http.MethodGet, page, nil, scopePromotionRead, scopePromotionWrite)
	assert.NotContains(t, rec.Body.String(), "/campaign\"", "a surface that cannot set one offers no form")
	rec = campaignsRequest(reading, http.MethodPost, page+"/campaign", url.Values{}, scopePromotionWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	failing := &fakePlacer{fakePromotionReader: &fakePromotionReader{page: promotionInSpring},
		listErr: errors.Unavailable("db_down", "no answer")}
	rec = campaignsRequest(campaignsPanel(t, failing), http.MethodGet, page, nil, scopePromotionRead, scopePromotionWrite)
	require.Equal(t, http.StatusOK, rec.Code, "the page stands when the campaigns cannot be read")
	assert.Contains(t, rec.Body.String(), "The campaigns could not be read, so none is offered.")
	assert.NotContains(t, rec.Body.String(), "/campaign\"")
}

// TestAPromotionWhoseCampaignWasDeletedSaysSo is D205: a promotion naming a
// deleted campaign applies nothing, and its page says so rather than that it
// belongs to none, and draws the form with the campaign it names, so that the
// move out of it is not refused as a promotion moved since.
func TestAPromotionWhoseCampaignWasDeletedSaysSo(t *testing.T) {
	t.Parallel()

	placer := &fakePlacer{fakePromotionReader: &fakePromotionReader{page: promotionInADeletedCampaign},
		campaigns: twoSeasons, total: 2}
	rec := campaignsRequest(campaignsPanel(t, placer), http.MethodGet, PromotionsPath+"/promo_1", nil,
		scopePromotionRead, scopePromotionWrite)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "The promotion's campaign camp_gone was deleted, so the promotion applies nothing")
	assert.NotContains(t, body, "belongs to no campaign")
	assert.Contains(t, body, `<input type="hidden" name="from" value="camp_gone">`)
}

// TestAPromotionIsPutIntoTheChosenCampaign: the surface is asked to move the
// promotion in the path from the campaign the page was drawn with to the one
// chosen, and the page comes back; a refusal is drawn on the page, to a
// writer who cannot read as the reason alone, and a failure is not a refusal.
func TestAPromotionIsPutIntoTheChosenCampaign(t *testing.T) {
	t.Parallel()

	placer := &fakePlacer{fakePromotionReader: &fakePromotionReader{page: promotionInSpring},
		campaigns: twoSeasons, total: 2}
	panel := campaignsPanel(t, placer)
	path := PromotionsPath + "/promo_1/campaign"
	writer := []string{scopePromotionRead, scopePromotionWrite}

	rec := campaignsRequest(panel, http.MethodPost, path,
		url.Values{formCampaignFrom: {" camp_spring "}, formCampaignTo: {" camp_summer "}}, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, PromotionsPath+"/promo_1", rec.Header().Get("Location"))
	assert.Equal(t, []string{"promo_1|camp_spring|camp_summer"}, placer.set)

	campaignsRequest(panel, http.MethodPost, path, url.Values{formCampaignFrom: {"camp_summer"}}, writer...)
	assert.Equal(t, "promo_1|camp_summer|", placer.set[1], "out of any campaign")

	placer.setErr = errors.Conflict("promotion_campaign_moved",
		"promotion promo_1 is in campaign camp_summer now, not campaign camp_spring; draw the page again")
	rec = campaignsRequest(panel, http.MethodPost, path,
		url.Values{formCampaignFrom: {"camp_spring"}, formCampaignTo: {"camp_summer"}}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "draw the page again")
	assert.Contains(t, rec.Body.String(), "<h1>SPRING</h1>", "the refusal is drawn on the page")

	placer.setErr = errors.NotFound("promotion_campaign_gone", "there is no live campaign camp_summer")
	rec = campaignsRequest(panel, http.MethodPost, path, url.Values{formCampaignTo: {"camp_summer"}}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "there is no live campaign camp_summer")
	rec = campaignsRequest(panel, http.MethodPost, path, url.Values{formCampaignTo: {"camp_summer"}}, scopePromotionWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.NotContains(t, rec.Body.String(), "<h1>SPRING</h1>", "a writer who cannot read is shown none of the page")

	placer.setErr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, path, url.Values{formCampaignTo: {"camp_summer"}}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
