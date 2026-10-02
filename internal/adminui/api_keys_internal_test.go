package adminui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// fakeKeyReader lists the users as fakeUsers does and the keys as scripted,
// recording what each listing asked for.
type fakeKeyReader struct {
	fakeUsers
	keys      string
	keyTotal  int64
	keysErr   error
	keysAsked []string
}

func (f *fakeKeyReader) APIKeysJSON(_ context.Context, revoked string, limit, offset int32) (json.RawMessage, int64, error) {
	f.keysAsked = append(f.keysAsked, fmt.Sprintf("%s|%d|%d", revoked, limit, offset))
	return json.RawMessage(f.keys), f.keyTotal, f.keysErr
}

// fakeKeys lists the keys as fakeKeyReader does and revokes them, recording
// each revocation.
type fakeKeys struct {
	fakeKeyReader
	revoked   []string
	revokeErr error
}

func (f *fakeKeys) RevokeAPIKey(_ context.Context, id, revokedBy string) error {
	f.revoked = append(f.revoked, id+"|"+revokedBy)
	return f.revokeErr
}

// TestTheAPIKeysScreenListsAndRevokesTheKeys is ADR 0350: the keys still
// accepted are listed when no tab is chosen, each with its title, type,
// token as redacted, privileges, when it was last used or never, and when
// it was made, a revoked one with when and by whom; a tab lists its own a
// page at a time; an operator holding admin revokes an open key from the tab
// it was pressed on, in their name, and a reader is offered nothing.
func TestTheAPIKeysScreenListsAndRevokesTheKeys(t *testing.T) {
	t.Parallel()

	keys := &fakeKeys{}
	keys.keyTotal = 27
	keys.keys = `[{"id":"apikey_1","type":"secret","title":"ERP","redacted":"sk_…a1b2","scopes":["order:read","order:write"],
		"created_by":"user_1","last_used_at":"2026-09-30T12:00:00Z","revoked_at":null,"revoked_by":"",
		"created_at":"2026-09-01T08:00:00Z"},{"id":"apikey_2","type":"publishable","title":"Shop","redacted":"pk_…c3d4",
		"scopes":[],"created_by":"user_1","last_used_at":null,"revoked_at":"2026-10-01T09:00:00Z",
		"revoked_by":"user_3","created_at":"2026-08-01T08:00:00Z"}]`
	panel := usersPanel(t, keys)

	rec := campaignsRequest(panel, http.MethodGet, APIKeysPath, nil, scopeAuthRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	_, erp, _ := strings.Cut(body, "<td>ERP<br>")
	erp, shop, _ := strings.Cut(erp, "<td>Shop<br>")
	for _, want := range []string{"apikey_1", "<td>secret</td>", "<code>sk_…a1b2</code>", "<td>order:read, order:write</td>",
		"<td>2026-09-30 12:00</td>", "<td>2026-09-01</td>"} {
		assert.Contains(t, erp, want, "the ERP key's row")
	}
	for _, want := range []string{"<td>publishable</td>", `<span class="muted">none</span>`,
		`<span class="muted">never</span>`, "2026-10-01 09:00 by user_3"} {
		assert.Contains(t, shop, want, "the shop key's row")
	}
	assert.Contains(t, body, "27 open.")
	assert.Contains(t, body, `href="`+APIKeysPath+`?status=open" aria-current="page">open</a>`)
	assert.Contains(t, body, `href="`+APIKeysPath+`?status=all">all</a>`)
	assert.Contains(t, body, `href="`+APIKeysPath+`?status=open&amp;page=2">Next</a>`)
	assert.Contains(t, body, `href="`+APIKeysPath+`"`, "the screen is in the menu")
	assert.NotContains(t, body, "/revoke", "a reader revokes nothing")
	assert.Equal(t, []string{"open|25|0"}, keys.keysAsked, "the open keys, the first page")

	rec = campaignsRequest(panel, http.MethodGet, APIKeysPath, nil, scopeAdmin)
	_, erp, _ = strings.Cut(rec.Body.String(), "<td>ERP<br>")
	erp, shop, _ = strings.Cut(erp, "<td>Shop<br>")
	assert.Contains(t, erp, `action="`+APIKeysPath+`/apikey_1/revoke"`, "an open key is revoked")
	assert.Contains(t, erp, `name="status" value="open"`)
	assert.NotContains(t, shop, "/revoke", "a revoked one is not")

	keys.keysAsked = nil
	rec = campaignsRequest(panel, http.MethodGet, APIKeysPath+"?status=revoked&page=2", nil, scopeAuthRead)
	assert.Contains(t, rec.Body.String(), `?status=revoked&amp;page=1">Previous</a>`)
	assert.NotContains(t, rec.Body.String(), ">Next</a>", "the second page of 27 is the last")
	campaignsRequest(panel, http.MethodGet, APIKeysPath+"?status=maybe", nil, scopeAuthRead)
	campaignsRequest(panel, http.MethodGet, APIKeysPath+"?status=all", nil, scopeAuthRead)
	campaignsRequest(panel, http.MethodGet, APIKeysPath+"?status=", nil, scopeAuthRead)
	assert.Equal(t, []string{"revoked|25|25", "open|25|0", "|25|0", "open|25|0"}, keys.keysAsked,
		"an unknown tab, or none, is the open keys; every key is asked for with no tab")

	rec = campaignsRequest(panel, http.MethodPost, APIKeysPath+"/apikey_1/revoke", url.Values{paramKeyStatus: {"revoked"}},
		scopeAdmin)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, APIKeysPath+"?revoked=apikey_1&status=revoked", rec.Header().Get("Location"))
	assert.Equal(t, []string{"apikey_1|user_1"}, keys.revoked, "in the operator's name")
	landed := campaignsRequest(panel, http.MethodGet, rec.Header().Get("Location"), nil, scopeAuthRead)
	assert.Contains(t, landed.Body.String(), "Key apikey_1 was revoked.")
	assert.Contains(t, landed.Body.String(), `?status=revoked" aria-current="page">`)

	keys.revokeErr = errors.Conflict("auth_api_key_already_revoked", "the key was already revoked")
	rec = campaignsRequest(panel, http.MethodPost, APIKeysPath+"/apikey_1/revoke", url.Values{paramKeyStatus: {"open"}},
		scopeAdmin)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "the key was already revoked")
	keys.revokeErr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, APIKeysPath+"/apikey_1/revoke", nil, scopeAdmin)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	rec = campaignsRequest(panel, http.MethodPost, APIKeysPath+"/apikey_1/revoke", nil, scopeAuthRead)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	rec = campaignsRequest(panel, http.MethodGet, APIKeysPath, nil, scopeOrderRead)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	keys.keysErr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodGet, APIKeysPath, nil, scopeAuthRead)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	reader := usersPanel(t, &fakeKeyReader{keys: `[]`})
	assert.NotContains(t, campaignsRequest(reader, http.MethodGet, APIKeysPath, nil, scopeAdmin).Body.String(), "/revoke")
	assert.Equal(t, http.StatusServiceUnavailable,
		campaignsRequest(reader, http.MethodPost, APIKeysPath+"/apikey_1/revoke", nil, scopeAdmin).Code)
	assert.Equal(t, http.StatusServiceUnavailable,
		campaignsRequest(usersPanel(t, &fakeUsers{}), http.MethodGet, APIKeysPath, nil, scopeAuthRead).Code)
}
