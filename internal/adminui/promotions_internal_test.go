package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// fakePromotions answers the listing as scripted and records what it was asked.
type fakePromotions struct {
	body  string
	total int64
	err   error
	asked []string
	pages [][2]int32
}

func (f *fakePromotions) PromotionsJSON(_ context.Context, status string, limit, offset int32) (json.RawMessage, int64, error) {
	f.asked = append(f.asked, status)
	f.pages = append(f.pages, [2]int32{limit, offset})

	return json.RawMessage(f.body), f.total, f.err
}

// promotionsRequest sends one request to the screen as an operator.
func promotionsRequest(t *testing.T, panel *UI, path string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	req = req.WithContext(corehttp.WithPrincipal(req.Context(),
		corehttp.Principal{ID: "user_1", Kind: "user", Scopes: []string{scopePromotionRead}}))
	rec := httptest.NewRecorder()
	panel.listPromotions(rec, req)

	return rec
}

// TestThePromotionsScreenListsAStatus is ADR 0311: the active promotions are
// listed when no status is chosen, a chosen one is asked for, each row shows
// its code or that it applies itself, its usage against its limit, and the
// paging carries the status; an unknown status lists the active ones.
func TestThePromotionsScreenListsAStatus(t *testing.T) {
	t.Parallel()

	promotions := &fakePromotions{total: 26, body: `[
		{"id":"promo_1","code":"SUMMER10","is_automatic":false,"type":"standard","status":"active",
		 "usage_count":7,"usage_limit":50,"created_at":"2026-09-01T10:00:00Z"},
		{"id":"promo_2","code":"","is_automatic":true,"type":"buyget","status":"active",
		 "usage_count":3,"usage_limit":null,"created_at":"2026-09-02T10:00:00Z"}
	]`}
	panel := newCatalogPanel(t, &fakeCatalog{})
	panel.promotions = promotions

	rec := promotionsRequest(t, panel, PromotionsPath)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "SUMMER10")
	assert.Contains(t, body, "7 of 50")
	assert.Contains(t, body, `<span class="pill">automatic</span>`)
	assert.Contains(t, body, "<td class=\"num\">3</td>", "no limit is printed as none")
	assert.Contains(t, body, `?status=active&amp;page=2"`, "the next page keeps the status")
	assert.Equal(t, []string{"active"}, promotions.asked)
	assert.Equal(t, [][2]int32{{promotionsPerPage, 0}}, promotions.pages)

	promotionsRequest(t, panel, PromotionsPath+"?status=draft&page=2")
	assert.Equal(t, "draft", promotions.asked[1])
	assert.Equal(t, [2]int32{promotionsPerPage, promotionsPerPage}, promotions.pages[1])
	promotionsRequest(t, panel, PromotionsPath+"?status=on-sale")
	assert.Equal(t, "active", promotions.asked[2], "an unknown status lists the active ones")

	failing := newCatalogPanel(t, &fakeCatalog{})
	failing.promotions = &fakePromotions{err: errors.Unavailable("db_down", "no answer")}
	rec = promotionsRequest(t, failing, PromotionsPath)
	assert.NotEqual(t, http.StatusOK, rec.Code)

	absent := newCatalogPanel(t, &fakeCatalog{})
	rec = promotionsRequest(t, absent, PromotionsPath)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
