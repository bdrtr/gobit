package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
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
	switched []string
	revised  []string
}

func (f *fakePriceLists) RevisePriceList(
	_ context.Context, id, readTitle, readDescription string, readStartsAt, readEndsAt *time.Time,
	title, description string, startsAt, endsAt *time.Time,
) error {
	f.revised = append(f.revised, strings.Join([]string{
		id, readTitle, readDescription, momentText(readStartsAt), momentText(readEndsAt),
		title, description, momentText(startsAt), momentText(endsAt),
	}, "|"))
	return f.writeErr
}

// momentText prints a moment the fake was sent, "open" for none.
func momentText(at *time.Time) string {
	if at == nil {
		return "open"
	}

	return at.UTC().Format(time.RFC3339Nano)
}

func (f *fakePriceLists) SwitchPriceListStatus(_ context.Context, id, from, to string) error {
	f.switched = append(f.switched, id+"|"+from+"|"+to)
	return f.writeErr
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

// TestAPriceListIsPublishedEndedAndReopened is ADR 0328: each row offers its
// status's one move, carrying the status it was drawn in, to a writer; the
// surface is asked to move the list from that status, and the list comes
// back; a list another operator moved first is refused on the list; a reader
// is offered no move.
func TestAPriceListIsPublishedEndedAndReopened(t *testing.T) {
	t.Parallel()

	lists := &fakePriceLists{body: `[
		{"id":"plist_1","title":"Wholesale","type":"override","status":"active","created_at":"2026-01-01T00:00:00Z"},
		{"id":"plist_2","title":"Spring","type":"sale","status":"draft","created_at":"2026-03-01T00:00:00Z"},
		{"id":"plist_3","title":"Winter","type":"sale","status":"expired","created_at":"2025-12-01T00:00:00Z"}]`, total: 3}
	panel := priceListsPanel(t, lists)
	writer := []string{scopePricingRead, scopePricingWrite}

	rec := campaignsRequest(panel, http.MethodGet, PriceListsPath, nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	for id, move := range map[string][2]string{
		"plist_1": {"End", "expired"}, "plist_2": {"Publish", "active"}, "plist_3": {"Reopen", "active"},
	} {
		_, row, found := strings.Cut(body, `action="`+PriceListsPath+"/"+id+`/status"`)
		require.True(t, found, "%s offers its move", id)
		row, _, _ = strings.Cut(row, "</form>")
		assert.Contains(t, row, ">"+move[0]+"</button>", id)
		assert.Contains(t, row, `name="to" value="`+move[1]+`"`, id)
	}
	assert.Contains(t, body, `<input type="hidden" name="from" value="draft">`)
	rec = campaignsRequest(panel, http.MethodGet, PriceListsPath, nil, scopePricingRead)
	assert.NotContains(t, rec.Body.String(), `/status"`, "a reader moves nothing")

	rec = campaignsRequest(panel, http.MethodPost, PriceListsPath+"/plist_2/status",
		url.Values{formStatusFrom: {"draft"}, formStatusTo: {"active"}}, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, PriceListsPath, rec.Header().Get("Location"))
	assert.Equal(t, []string{"plist_2|draft|active"}, lists.switched)

	lists.writeErr = errors.Conflict("pricing_price_list_moved", "price list plist_2 is active now, not draft; draw the list again")
	rec = campaignsRequest(panel, http.MethodPost, PriceListsPath+"/plist_2/status",
		url.Values{formStatusFrom: {"draft"}, formStatusTo: {"active"}}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "draw the list again")
	assert.Contains(t, rec.Body.String(), "Wholesale", "the refusal is drawn on the list")

	lists.writeErr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, PriceListsPath+"/plist_2/status",
		url.Values{formStatusFrom: {"draft"}, formStatusTo: {"active"}}, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// listRowForm is the revise form of the list's row on a page.
func listRowForm(t *testing.T, body, id string) string {
	t.Helper()

	_, form, found := strings.Cut(body, `action="`+PriceListsPath+"/"+id+"?page=")
	require.True(t, found, "%s offers its form", id)
	form, _, _ = strings.Cut(form, "</form>")

	return form
}

// TestAPriceListRowRevisesItsList is ADR 0330: each row offers a writer the
// form that revises its list, carrying the title, the description and the
// window it was drawn with, the moments to the nanosecond; the surface is
// asked to revise the list from those, an end typed as it was shown standing
// for the moment drawn, and the list's page names it; a moment the panel
// cannot read is not sent, and a refusal comes back in the row with what was
// typed, drawn from the list as it is now.
func TestAPriceListRowRevisesItsList(t *testing.T) {
	t.Parallel()

	lists := &fakePriceLists{body: `[
		{"id":"plist_1","title":"Wholesale","description":"trade","type":"override","status":"active",
		 "starts_at":"2026-01-01T00:00:00.123456Z","ends_at":null,"created_at":"2025-12-20T10:00:00Z"},
		{"id":"plist_2","title":"Spring","description":"","type":"sale","status":"draft",
		 "starts_at":null,"ends_at":"2026-05-31T23:59:00Z","created_at":"2026-03-01T10:00:00Z"}]`, total: 30}
	panel := priceListsPanel(t, lists)
	writer := []string{scopePricingRead, scopePricingWrite}

	rec := campaignsRequest(panel, http.MethodGet, PriceListsPath+"?page=2", nil, writer...)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	form := listRowForm(t, rec.Body.String(), "plist_1")
	assert.True(t, strings.HasPrefix(form, `2">`), "the form returns to the page it was drawn on")
	for _, want := range []string{
		`name="read_title" value="Wholesale"`, `name="read_description" value="trade"`,
		`name="read_starts_at" value="2026-01-01T00:00:00.123456Z"`, `name="read_ends_at" value=""`,
		`name="title" value="Wholesale"`, `rows="2">trade</textarea>`,
		`name="starts_at" value="2026-01-01T00:00"`, `name="ends_at" value=""`,
	} {
		assert.Contains(t, form, want)
	}
	assert.Contains(t, listRowForm(t, rec.Body.String(), "plist_2"), `name="read_ends_at" value="2026-05-31T23:59:00Z"`)
	assert.NotContains(t, rec.Body.String(), "<details open>", "no row is open until a refusal opens it")
	rec = campaignsRequest(panel, http.MethodGet, PriceListsPath, nil, scopePricingRead)
	assert.NotContains(t, rec.Body.String(), "Revise", "a reader revises nothing")
	rec = campaignsRequest(priceListsPanel(t, &fakePriceWriter{}), http.MethodPost, PriceListsPath+"/plist_1",
		url.Values{formPriceListTitle: {"X"}}, scopePricingWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	drawn := url.Values{
		formReadTitle: {"Wholesale"}, formReadDescription: {"trade"},
		formReadStarts: {"2026-01-01T00:00:00.123456Z"}, formReadEnds: {""},
	}
	sent := url.Values{formPriceListTitle: {" Wholesale 2027 "}, formPriceListDesc: {" trade\r\nprices "},
		formPriceListStarts: {"2026-01-01T00:00"}, formPriceListEnds: {"2027-01-31T00:00"}}
	for key, value := range drawn {
		sent[key] = value
	}
	rec = campaignsRequest(panel, http.MethodPost, PriceListsPath+"/plist_1?page=2", sent, writer...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, PriceListsPath+"?created=Wholesale+2027&page=2", rec.Header().Get("Location"))
	assert.Equal(t, []string{
		"plist_1|Wholesale|trade|2026-01-01T00:00:00.123456Z|open|Wholesale 2027|trade\nprices|2026-01-01T00:00:00.123456Z|2027-01-31T00:00:00Z",
	}, lists.revised, "the start typed as shown is the start drawn")

	rec = campaignsRequest(panel, http.MethodPost, PriceListsPath+"/plist_2?page=1", url.Values{
		formReadTitle: {"Spring"}, formReadDescription: {"a\r\nb"}, formReadEnds: {"2026-05-31T23:59:00Z"},
		formPriceListTitle: {"Spring"}, formPriceListStarts: {"2026-04-01T08:30"}, formPriceListEnds: {""},
	}, writer...)
	assert.Equal(t, PriceListsPath+"?created=Spring", rec.Header().Get("Location"), "the first page is the list")
	assert.Equal(t, "plist_2|Spring|a\nb|open|2026-05-31T23:59:00Z|Spring||2026-04-01T08:30:00Z|open", lists.revised[1],
		"a moved start is the minute typed, an emptied end is open")

	for reason, form := range map[string]url.Values{
		"The window the row was drawn with could not be read": {formReadStarts: {"2026-01-01 00:00"}, formPriceListTitle: {"X"}},
		"The price list&#39;s end could not be read":          {formPriceListTitle: {"X"}, formPriceListEnds: {"soon"}},
	} {
		rec = campaignsRequest(panel, http.MethodPost, PriceListsPath+"/plist_1", form, writer...)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, reason)
		assert.Contains(t, rec.Body.String(), reason)
	}
	assert.Len(t, lists.revised, 2, "a moment the panel cannot read is not sent")

	lists.writeErr = errors.Conflict("pricing_price_list_moved",
		`price list plist_1 was revised since it was read: it is "Wholesale" from always until open now; draw the list again`)
	rec = campaignsRequest(panel, http.MethodPost, PriceListsPath+"/plist_1", url.Values{
		formReadTitle: {"Retail"}, formPriceListTitle: {"Gold"}, formPriceListDesc: {"typed"},
		formPriceListStarts: {"2026-02-01T00:00"}, formPriceListEnds: {"2026-03-01T00:00"},
	}, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "draw the list again")
	form = listRowForm(t, body, "plist_1")
	for _, want := range []string{
		`name="read_title" value="Wholesale"`, `name="read_starts_at" value="2026-01-01T00:00:00.123456Z"`,
		`name="title" value="Gold"`, `rows="2">typed</textarea>`,
		`name="starts_at" value="2026-02-01T00:00"`, `name="ends_at" value="2026-03-01T00:00"`,
	} {
		assert.Contains(t, form, want, "the row carries the list as it is now and what was typed")
	}
	assert.Contains(t, body, "<details open>", "the refused row is open")
	assert.Contains(t, listRowForm(t, body, "plist_2"), `name="title" value="Spring"`, "another row is as drawn")
	newList, _, _ := strings.Cut(body, "<table>")
	assert.NotContains(t, newList, "Gold", "what was typed is the row's, not the new list's")

	lists.writeErr = errors.NotFound("price_list_not_found", "price list not found: plist_1")
	rec = campaignsRequest(panel, http.MethodPost, PriceListsPath+"/plist_1", sent, writer...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "price list not found: plist_1")
	lists.writeErr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, PriceListsPath+"/plist_1", sent, writer...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
