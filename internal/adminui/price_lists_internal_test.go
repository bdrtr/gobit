package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// fakePriceLists writes prices as the shared fake does, and lists and writes
// price lists, recording each listing and write.
type fakePriceLists struct {
	fakePriceWriter
	body     string
	total    int64
	pages    [][2]int32
	written  []string
	windows  [][2]*time.Time
	writeErr error
}

func (f *fakePriceLists) PriceListsJSON(_ context.Context, limit, offset int32) (json.RawMessage, int64, error) {
	f.pages = append(f.pages, [2]int32{limit, offset})
	return json.RawMessage(f.body), f.total, nil
}

func (f *fakePriceLists) CreatePriceList(
	_ context.Context, title, description, listType, status string, startsAt, endsAt *time.Time,
) (string, error) {
	f.written = append(f.written, title+"|"+description+"|"+listType+"|"+status)
	f.windows = append(f.windows, [2]*time.Time{startsAt, endsAt})
	return "plist_new", f.writeErr
}

// twoLists is a wholesale override with a window and a draft sale without.
const twoLists = `[
	{"id":"plist_1","title":"Wholesale 2026","description":"trade prices","type":"override","status":"active",
	 "starts_at":"2026-01-01T00:00:00Z","ends_at":"2026-12-31T23:59:00Z","created_at":"2025-12-20T10:00:00Z"},
	{"id":"plist_2","title":"Spring sale","description":"","type":"sale","status":"draft",
	 "starts_at":null,"ends_at":null,"created_at":"2026-03-01T10:00:00Z"}]`

// priceListsPanel is a panel over the price lists.
func priceListsPanel(t *testing.T, prices PriceWriter) *UI {
	t.Helper()

	panel := newCatalogPanel(t, &fakeCatalog{})
	panel.prices = prices
	panel.scopes = builtInScopes()

	return panel
}

// TestThePriceListsScreenListsTheLists is ADR 0326: each list with its type,
// status and window in UTC or open, a page at a time; the form is offered to
// a writer whose surface can write a list.
func TestThePriceListsScreenListsTheLists(t *testing.T) {
	t.Parallel()

	lists := &fakePriceLists{body: twoLists, total: 26}
	panel := priceListsPanel(t, lists)

	rec := campaignsRequest(panel, http.MethodGet, PriceListsPath, nil, scopePricingRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	for _, want := range []string{
		"Wholesale 2026", "plist_1", "trade prices", "<td>override</td>", `<span class="pill">active</span>`,
		"2026-01-01 00:00", "2026-12-31 23:59", "Spring sale", `<span class="pill">draft</span>`,
		"26 price lists.", `href="` + PriceListsPath + `?page=2"`,
	} {
		assert.Contains(t, body, want)
	}
	assert.Equal(t, [][2]int32{{priceListsPerPage, 0}}, lists.pages)
	assert.NotContains(t, body, "New price list", "a reader writes nothing")
	campaignsRequest(panel, http.MethodGet, PriceListsPath+"?page=2", nil, scopePricingRead)
	assert.Equal(t, [2]int32{priceListsPerPage, priceListsPerPage}, lists.pages[1])
	rec = campaignsRequest(priceListsPanel(t, &fakePriceLists{body: twoLists, total: priceListsPerPage}),
		http.MethodGet, PriceListsPath, nil, scopePricingRead)
	assert.NotContains(t, rec.Body.String(), "page=2", "one page has no next")

	rec = campaignsRequest(panel, http.MethodGet, PriceListsPath, nil, scopePricingRead, scopePricingWrite)
	assert.Contains(t, rec.Body.String(), "New price list")
	rec = campaignsRequest(priceListsPanel(t, &fakePriceWriter{}), http.MethodGet, PriceListsPath, nil, scopePricingRead)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "a surface without lists answers 503")
	rec = campaignsRequest(priceListsPanel(t, &fakePriceWriter{}), http.MethodPost, PriceListsPath,
		url.Values{formPriceListTitle: {"X"}}, scopePricingWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestThePriceListFormWritesWhatWasTyped: the text trimmed, the type and the
// status as chosen, the window read in UTC, the list it lands on naming the
// list; an unreadable moment and the module's refusal come back with what
// was typed, to a writer who cannot read as the reason alone.
func TestThePriceListFormWritesWhatWasTyped(t *testing.T) {
	t.Parallel()

	lists := &fakePriceLists{body: twoLists, total: 2}
	panel := priceListsPanel(t, lists)
	writer := []string{scopePricingRead, scopePricingWrite}

	rec := campaignsRequest(panel, http.MethodPost, PriceListsPath, url.Values{
		formPriceListTitle: {" Wholesale 2027 "}, formPriceListDesc: {" trade "},
		formPriceListType: {"override"}, formPriceListStatus: {"active"},
		formPriceListStarts: {"2027-01-01T00:00"}, formPriceListEnds: {""},
	}, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, PriceListsPath+"?created=Wholesale+2027", rec.Header().Get("Location"))
	assert.Equal(t, []string{"Wholesale 2027|trade|override|active"}, lists.written)
	require.NotNil(t, lists.windows[0][0])
	assert.Equal(t, time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), *lists.windows[0][0])
	assert.Nil(t, lists.windows[0][1], "an empty end leaves the window open")
	landed := campaignsRequest(panel, http.MethodGet, rec.Header().Get("Location"), nil, scopePricingRead)
	assert.Contains(t, landed.Body.String(), "Price list Wholesale 2027 was written.")

	rec = campaignsRequest(panel, http.MethodPost, PriceListsPath, url.Values{
		formPriceListTitle: {"Summer"}, formPriceListType: {"sale"}, formPriceListStatus: {"draft"},
		formPriceListEnds: {"soon"},
	}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "The price list&#39;s end could not be read")
	assert.Contains(t, rec.Body.String(), `value="Summer"`, "what was typed comes back")
	assert.Len(t, lists.written, 1, "a form the panel cannot read is not sent")

	lists.writeErr = errors.Invalid("pricing_invalid_input", "the price list type is undefined")
	rec = campaignsRequest(panel, http.MethodPost, PriceListsPath, url.Values{formPriceListTitle: {"Odd"}}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "the price list type is undefined")
	rec = campaignsRequest(panel, http.MethodPost, PriceListsPath, url.Values{formPriceListTitle: {"Odd"}}, scopePricingWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.NotContains(t, rec.Body.String(), "Wholesale 2026", "a writer who cannot read is shown none of the list")

	lists.writeErr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, PriceListsPath, url.Values{formPriceListTitle: {"Odd"}}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
