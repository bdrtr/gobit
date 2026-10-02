package adminui

import (
	"context"
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

// fakeChannelMaker corrects channels as fakeChannelReviser does and makes
// them, recording each one made.
type fakeChannelMaker struct {
	fakeChannelReviser
	made    []string
	makeErr error
}

func (f *fakeChannelMaker) MakeSalesChannel(_ context.Context, name, description string, disabled bool) (string, error) {
	f.made = append(f.made, fmt.Sprintf("%s|%s|%t", name, description, disabled))
	return "sc_9", f.makeErr
}

// makeForm is the screen's form that makes a channel.
func makeForm(t *testing.T, body string) string {
	t.Helper()

	_, form, found := strings.Cut(body, "<summary>Make a channel</summary>")
	require.True(t, found, "the screen offers the form")
	form, _, _ = strings.Cut(form, "</details>")

	return form
}

// TestASalesChannelIsMadeOnTheirScreen is ADR 0353: an operator holding
// admin is offered the form, a reader is not; the surface is asked to make
// the channel typed, trimmed, and the screen says so; a refusal comes back
// with what was typed in the form, which a refused row correction leaves
// empty and closed.
func TestASalesChannelIsMadeOnTheirScreen(t *testing.T) {
	t.Parallel()

	maker := &fakeChannelMaker{}
	panel := channelsPanel(t, maker, query.Record{
		fieldID: "sc_1", fieldChannelName: "Web", fieldCreatedAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	})

	rec := campaignsRequest(panel, http.MethodGet, SalesChannelsPath, nil, scopeAdmin)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	form := makeForm(t, rec.Body.String())
	assert.Contains(t, form, `action="`+SalesChannelsPath+`"`)
	assert.Contains(t, form, `name="name" value=""`)
	assert.NotContains(t, form, " checked")
	assert.NotContains(t, campaignsRequest(panel, http.MethodGet, SalesChannelsPath, nil, scopeAuthRead).Body.String(),
		"Make a channel", "a reader makes nothing")

	rec = campaignsRequest(panel, http.MethodPost, SalesChannelsPath, url.Values{
		formChannelName: {" Phone "}, formChannelDescription: {" telephone orders "}, formChannelDisabled: {"1"},
	}, scopeAdmin)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, SalesChannelsPath+"?written=Phone", rec.Header().Get("Location"))
	rec = campaignsRequest(panel, http.MethodPost, SalesChannelsPath, url.Values{formChannelName: {"Shop"}}, scopeAdmin)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, []string{"Phone|telephone orders|true", "Shop||false"}, maker.made)

	maker.makeErr = errors.Conflict("auth_sales_channel_name_taken", "the channel name is in use")
	rec = campaignsRequest(panel, http.MethodPost, SalesChannelsPath, url.Values{
		formChannelName: {"Web"}, formChannelDescription: {"again"}, formChannelDisabled: {"1"},
	}, scopeAdmin)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "the channel name is in use")
	form = makeForm(t, body)
	assert.Contains(t, body, "<details open>\n  <summary>Make a channel</summary>", "the refused form is open")
	assert.Contains(t, form, `name="name" value="Web"`)
	assert.Contains(t, form, `name="description" value="again"`)
	assert.Contains(t, form, `value="1" checked> disabled`)
	assert.Equal(t, 1, strings.Count(body, "<details open>"), "and no row is")

	maker.err = errors.Conflict("auth_sales_channel_revised", "sales channel sc_1 was revised since it was read")
	rec = campaignsRequest(panel, http.MethodPost, SalesChannelsPath+"/sc_1", url.Values{
		formChannelReadName: {"Web"}, formChannelReadDisabled: {"false"}, formChannelName: {"Typed"},
	}, scopeAdmin)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	form = makeForm(t, rec.Body.String())
	assert.Contains(t, form, `name="name" value=""`, "a row's refusal leaves the form empty")
	assert.NotContains(t, rec.Body.String(), "<details open>\n  <summary>Make a channel</summary>")

	maker.makeErr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, SalesChannelsPath, url.Values{formChannelName: {"X"}}, scopeAdmin)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	rec = campaignsRequest(panel, http.MethodPost, SalesChannelsPath, url.Values{formChannelName: {"X"}}, scopeAuthRead)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	plain := channelsPanel(t, &fakeChannelReviser{})
	assert.NotContains(t, campaignsRequest(plain, http.MethodGet, SalesChannelsPath, nil, scopeAdmin).Body.String(),
		"Make a channel")
	assert.Equal(t, http.StatusServiceUnavailable,
		campaignsRequest(plain, http.MethodPost, SalesChannelsPath, url.Values{formChannelName: {"X"}}, scopeAdmin).Code)
}
