package adminui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// fakeChannelReviser lists the users as fakeUsers does and corrects channels,
// recording each correction.
type fakeChannelReviser struct {
	fakeUsers
	corrected []string
	err       error
}

func (f *fakeChannelReviser) ReviseSalesChannel(_ context.Context, id string, read, next json.RawMessage) error {
	f.corrected = append(f.corrected, id+"|"+string(read)+"|"+string(next))
	return f.err
}

// channelsPanel is a panel over the channels given, whose auth surface is
// the one given.
func channelsPanel(t *testing.T, users UserLister, channels ...query.Record) *UI {
	t.Helper()

	panel := newCatalogPanel(t, &fakeCatalog{byEntity: map[string][]query.Record{EntitySalesChannel: channels}})
	panel.users = users
	panel.scopes = builtInScopes()

	return panel
}

// channelRowOf is the screen's row of the channel.
func channelRowOf(t *testing.T, body, id string) string {
	t.Helper()

	_, row, found := strings.Cut(body, `<span class="muted">`+id+`</span>`)
	require.True(t, found, "the screen lists %s", id)
	row, _, _ = strings.Cut(row, "</tr>")

	return row
}

// TestTheSalesChannelsAreCorrectedOnTheirScreen is ADR 0352: a reader is
// shown each channel's name, description, whether it is in use and when it
// was made; an operator holding admin is offered each row's form drawn from
// its terms; the surface is asked to correct the channel from those, the
// typed ones trimmed, and the page says so; a refusal comes back with what
// was typed in that row alone.
func TestTheSalesChannelsAreCorrectedOnTheirScreen(t *testing.T) {
	t.Parallel()

	made := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	reviser := &fakeChannelReviser{}
	panel := channelsPanel(t, reviser,
		query.Record{fieldID: "sc_1", fieldChannelName: "Web", fieldChannelDescription: "the shop",
			fieldChannelDisabled: false, fieldCreatedAt: made},
		query.Record{fieldID: "sc_2", fieldChannelName: "Phone", fieldChannelDescription: "",
			fieldChannelDisabled: true, fieldCreatedAt: made},
	)

	rec := campaignsRequest(panel, http.MethodGet, SalesChannelsPath, nil, scopeAuthRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	web, phone := channelRowOf(t, rec.Body.String(), "sc_1"), channelRowOf(t, rec.Body.String(), "sc_2")
	assert.Contains(t, web, "<td>the shop</td>")
	assert.Contains(t, web, "<td>yes</td>")
	assert.Contains(t, web, "<td>2026-09-01</td>")
	assert.Contains(t, phone, `<span class="pill">disabled</span>`)
	assert.NotContains(t, rec.Body.String(), `action="`+SalesChannelsPath+`/`, "a reader corrects nothing")
	assert.Contains(t, rec.Body.String(), `href="`+SalesChannelsPath+`"`, "the screen is in the menu")

	rec = campaignsRequest(panel, http.MethodGet, SalesChannelsPath, nil, scopeAdmin)
	web, phone = channelRowOf(t, rec.Body.String(), "sc_1"), channelRowOf(t, rec.Body.String(), "sc_2")
	for _, want := range []string{
		`action="` + SalesChannelsPath + `/sc_1?page=1"`, `name="read_name" value="Web"`,
		`name="read_description" value="the shop"`, `name="read_disabled" value="false"`,
		`name="name" value="Web"`, `name="description" value="the shop"`, `value="1"> disabled`,
	} {
		assert.Contains(t, web, want)
	}
	assert.Contains(t, phone, `name="read_disabled" value="true"`)
	assert.Contains(t, phone, `value="1" checked> disabled`)

	rec = campaignsRequest(panel, http.MethodPost, SalesChannelsPath+"/sc_1", url.Values{
		formChannelReadName: {"Web"}, formChannelReadDescription: {"the shop"}, formChannelReadDisabled: {"false"},
		formChannelName: {" Web shop "}, formChannelDescription: {" the online shop "}, formChannelDisabled: {"1"},
	}, scopeAdmin)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, SalesChannelsPath+"?written=Web+shop", rec.Header().Get("Location"))
	assert.Equal(t, []string{`sc_1|{"name":"Web","description":"the shop","is_disabled":false}|` +
		`{"name":"Web shop","description":"the online shop","is_disabled":true}`}, reviser.corrected)
	landed := campaignsRequest(panel, http.MethodGet, rec.Header().Get("Location"), nil, scopeAuthRead)
	assert.Contains(t, landed.Body.String(), "Channel Web shop was written.")
	rec = campaignsRequest(panel, http.MethodPost, SalesChannelsPath+"/sc_2?page=3", url.Values{
		formChannelReadName: {"Phone"}, formChannelReadDisabled: {"true"}, formChannelName: {"Phone"},
	}, scopeAdmin)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, SalesChannelsPath+"?page=3&written=Phone", rec.Header().Get("Location"), "back to the page")
	assert.Equal(t, `sc_2|{"name":"Phone","description":"","is_disabled":true}|`+
		`{"name":"Phone","description":"","is_disabled":false}`, reviser.corrected[1], "an unticked box enables it")

	reviser.err = errors.Conflict("auth_sales_channel_revised", "sales channel sc_1 was revised since it was read; draw the list again")
	rec = campaignsRequest(panel, http.MethodPost, SalesChannelsPath+"/sc_1", url.Values{
		formChannelReadName: {"Old"}, formChannelReadDisabled: {"false"}, formChannelName: {"Typed"},
		formChannelDisabled: {"1"},
	}, scopeAdmin)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "draw the list again")
	web, phone = channelRowOf(t, body, "sc_1"), channelRowOf(t, body, "sc_2")
	assert.Contains(t, web, "<details open>", "the refused row is open")
	assert.Contains(t, web, `name="name" value="Typed"`, "with what was typed")
	assert.Contains(t, web, `value="1" checked> disabled`)
	assert.Contains(t, web, `name="read_name" value="Web"`, "and its terms as they are now")
	assert.NotContains(t, phone, "<details open>", "the other rows are not")
	assert.Equal(t, 1, strings.Count(body, "<details open>"))

	calls := len(reviser.corrected)
	rec = campaignsRequest(panel, http.MethodPost, SalesChannelsPath+"/sc_1", url.Values{
		formChannelReadName: {"Web"}, formChannelReadDisabled: {"maybe"}, formChannelName: {"Web"},
	}, scopeAdmin)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "could not be read; draw the list again")
	assert.Len(t, reviser.corrected, calls, "an unreadable row sends nothing")
	reviser.err = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, SalesChannelsPath+"/sc_1", url.Values{
		formChannelReadDisabled: {"false"}, formChannelName: {"X"},
	}, scopeAdmin)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	rec = campaignsRequest(panel, http.MethodPost, SalesChannelsPath+"/sc_1", url.Values{formChannelName: {"X"}}, scopeAuthRead)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	plain := channelsPanel(t, &fakeUsers{}, query.Record{fieldID: "sc_1", fieldChannelName: "Web", fieldCreatedAt: made})
	assert.NotContains(t, campaignsRequest(plain, http.MethodGet, SalesChannelsPath, nil, scopeAdmin).Body.String(),
		`action="`+SalesChannelsPath+`/`)
	assert.Equal(t, http.StatusServiceUnavailable,
		campaignsRequest(plain, http.MethodPost, SalesChannelsPath+"/sc_1", url.Values{}, scopeAdmin).Code)

	many := make([]query.Record, 0, salesChannelsPerPage+1)
	for i := range salesChannelsPerPage + 1 {
		many = append(many, query.Record{fieldID: fmt.Sprintf("sc_%02d", i), fieldChannelName: "C", fieldCreatedAt: made})
	}
	rec = campaignsRequest(channelsPanel(t, reviser, many...), http.MethodGet, SalesChannelsPath, nil, scopeAuthRead)
	assert.Contains(t, rec.Body.String(), `href="`+SalesChannelsPath+`?page=2">Next</a>`)
	assert.NotContains(t, rec.Body.String(), fmt.Sprintf("sc_%02d", salesChannelsPerPage), "a page holds its size")
}
