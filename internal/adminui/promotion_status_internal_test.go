package adminui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// fakeSwitcher lists as scripted and records each switch it is asked for.
type fakeSwitcher struct {
	fakePromotions
	switched  []string
	switchErr error
}

func (f *fakeSwitcher) SwitchPromotionStatus(_ context.Context, id, from, to string) error {
	f.switched = append(f.switched, id+"|"+from+"|"+to)
	return f.switchErr
}

// promotionStatusRequest sends one request through the screen's two routes.
func promotionStatusRequest(panel *UI, method, path string, form url.Values, scopes ...string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Get(PromotionsPath, panel.listPromotions)
	r.Post(PromotionsPath, panel.createCoupon)
	r.Post(PromotionStatusPath, panel.switchPromotion)

	request := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = request.WithContext(corehttp.WithPrincipal(request.Context(),
		corehttp.Principal{ID: "user_1", Kind: "user", Scopes: scopes}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, request)

	return rec
}

// draftRow is one draft as the surface sends it.
const draftRow = `[{"id":"promo_1","code":"SPRING","type":"standard","status":"draft",
	"usage_count":0,"usage_limit":null,"created_at":"2026-09-01T10:00:00Z"}]`

// TestEachStatusOffersItsMove is ADR 0312: a draft is published, an active
// promotion paused and an inactive one resumed, each form carrying the status
// its row was drawn in.
func TestEachStatusOffersItsMove(t *testing.T) {
	t.Parallel()

	for status, want := range map[string][2]string{
		"draft": {"active", "Publish"}, "active": {"inactive", "Pause"}, "inactive": {"active", "Resume"},
	} {
		panel := newCatalogPanel(t, &fakeCatalog{})
		panel.promotions = &fakeSwitcher{fakePromotions: fakePromotions{total: 1,
			body: strings.ReplaceAll(draftRow, `"status":"draft"`, `"status":"`+status+`"`)}}

		rec := promotionStatusRequest(panel, http.MethodGet, PromotionsPath+"?status="+status, nil,
			scopePromotionRead, scopePromotionWrite)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		body := rec.Body.String()
		assert.Contains(t, body, `action="`+PromotionsPath+`/promo_1/status"`, status)
		assert.Contains(t, body, `name="from" value="`+status+`"`, status)
		assert.Contains(t, body, `name="to" value="`+want[0]+`"`, status)
		assert.Contains(t, body, ">"+want[1]+"</button>", status)
	}
}

// TestTheMoveIsOfferedToAWriterOnly: an operator who may only read the
// promotions, or a surface that cannot switch, shows no form.
func TestTheMoveIsOfferedToAWriterOnly(t *testing.T) {
	t.Parallel()

	panel := newCatalogPanel(t, &fakeCatalog{})
	panel.promotions = &fakeSwitcher{fakePromotions: fakePromotions{total: 1, body: draftRow}}
	rec := promotionStatusRequest(panel, http.MethodGet, PromotionsPath+"?status=draft", nil, scopePromotionRead)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "SPRING")
	assert.NotContains(t, rec.Body.String(), "/status\"", "a reader is offered no move")

	listing := newCatalogPanel(t, &fakeCatalog{})
	listing.promotions = &fakePromotions{total: 1, body: draftRow}
	rec = promotionStatusRequest(listing, http.MethodGet, PromotionsPath+"?status=draft", nil,
		scopePromotionRead, scopePromotionWrite)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "/status\"", "a surface that cannot switch offers no move")

	rec = promotionStatusRequest(listing, http.MethodPost, PromotionsPath+"/promo_1/status",
		url.Values{formStatusFrom: {"draft"}, formStatusTo: {"active"}}, scopePromotionWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestTheSwitchReturnsToTheListItLeft: the surface is asked to move the
// promotion in the path from the status the form read, and the operator goes
// back to that status's list, where the row is no longer listed.
func TestTheSwitchReturnsToTheListItLeft(t *testing.T) {
	t.Parallel()

	switcher := &fakeSwitcher{}
	panel := newCatalogPanel(t, &fakeCatalog{})
	panel.promotions = switcher

	rec := promotionStatusRequest(panel, http.MethodPost, PromotionsPath+"/promo_1/status",
		url.Values{formStatusFrom: {"draft"}, formStatusTo: {"active"}}, scopePromotionWrite)

	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, PromotionsPath+"?status=draft", rec.Header().Get("Location"))
	assert.Equal(t, []string{"promo_1|draft|active"}, switcher.switched)
}

// TestARefusedSwitchIsDrawnOnTheList: a promotion another operator moved
// first is refused on the list it was drawn in, with the module's reason; an
// operator who cannot read the list is told the reason alone, and a failure
// is not mistaken for a refusal.
func TestARefusedSwitchIsDrawnOnTheList(t *testing.T) {
	t.Parallel()

	switcher := &fakeSwitcher{
		fakePromotions: fakePromotions{total: 1, body: draftRow},
		switchErr:      errors.Conflict("promotion_status_moved", "promotion promo_1 is active now, not draft"),
	}
	panel := newCatalogPanel(t, &fakeCatalog{})
	panel.promotions = switcher
	form := url.Values{formStatusFrom: {"draft"}, formStatusTo: {"active"}}

	rec := promotionStatusRequest(panel, http.MethodPost, PromotionsPath+"/promo_1/status", form,
		scopePromotionRead, scopePromotionWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "is active now, not draft")
	assert.Contains(t, rec.Body.String(), "SPRING", "the list is drawn around the refusal")
	assert.Equal(t, []string{"draft"}, switcher.asked, "the list is the status the form read")

	rec = promotionStatusRequest(panel, http.MethodPost, PromotionsPath+"/promo_1/status", form, scopePromotionWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "is active now, not draft")
	assert.NotContains(t, rec.Body.String(), "SPRING", "a writer who cannot read is shown none of the list")
	assert.Len(t, switcher.asked, 1, "nothing was read for them")

	switcher.switchErr = errors.Unavailable("db_down", "no answer")
	rec = promotionStatusRequest(panel, http.MethodPost, PromotionsPath+"/promo_1/status", form,
		scopePromotionRead, scopePromotionWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "a failure is not drawn as a refusal")
}
