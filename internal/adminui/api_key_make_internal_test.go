package adminui

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// fakeKeyMaker lists and revokes keys as fakeKeys does and makes them as
// scripted, recording each one made.
type fakeKeyMaker struct {
	fakeKeys
	made    []string
	makeErr error
}

func (f *fakeKeyMaker) MakeAPIKey(
	_ context.Context, createdBy, keyType, title string, scopes, channelIDs []string,
) (id, token string, err error) {
	f.made = append(f.made, fmt.Sprintf("%s|%s|%s|%q|%q", createdBy, keyType, title, scopes, channelIDs))
	if f.makeErr != nil {
		return "", "", f.makeErr
	}
	return "apikey_9", "sk_the-only-copy", nil
}

// TestAKeyIsMadeOnTheAPIKeysScreen is ADR 0351: an operator holding admin is
// offered the form with the privileges unticked and the sales channels, a
// reader is not; the surface is asked to make the key described, the title
// trimmed, in the operator's name, and the answer shows its token once and
// is not stored; a refusal comes back with what was typed.
func TestAKeyIsMadeOnTheAPIKeysScreen(t *testing.T) {
	t.Parallel()

	maker := &fakeKeyMaker{}
	maker.keys = `[]`
	panel := newCatalogPanel(t, &fakeCatalog{byEntity: map[string][]query.Record{
		EntitySalesChannel: {{fieldID: "sc_1", fieldChannelName: "Web"}},
	}})
	panel.users = maker
	panel.scopes = builtInScopes()

	rec := campaignsRequest(panel, http.MethodGet, APIKeysPath, nil, scopeAdmin)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	_, form, found := strings.Cut(rec.Body.String(), `<form method="post" action="`+APIKeysPath+`">`)
	require.True(t, found, "an operator holding admin is offered the form")
	form, _, _ = strings.Cut(form, "</form>")
	for _, want := range []string{
		`name="title"`, `<option value="secret">secret</option><option value="publishable">publishable</option>`,
		`value="order:read"> order:read`, `name="channel" value="sc_1"> Web`,
	} {
		assert.Contains(t, form, want)
	}
	assert.NotContains(t, form, " checked", "nothing ticked to begin with")
	rec = campaignsRequest(panel, http.MethodGet, APIKeysPath, nil, scopeAuthRead)
	assert.NotContains(t, rec.Body.String(), "Make a key", "a reader makes nothing")

	rec = campaignsRequest(panel, http.MethodPost, APIKeysPath, url.Values{
		formKeyTitle: {" ERP "}, formKeyType: {"secret"}, formScope: {"order:read", "order:write"},
	}, scopeAdmin)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "<code>sk_the-only-copy</code>")
	assert.Contains(t, rec.Body.String(), "The key apikey_9 was made. Copy its token now; it is not shown again.")
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"), "the browser keeps no copy")
	rec = campaignsRequest(panel, http.MethodPost, APIKeysPath, url.Values{
		formKeyTitle: {"Shop"}, formKeyType: {"publishable"}, formKeyChannel: {"sc_1"},
	}, scopeAdmin)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{
		`user_1|secret|ERP|["order:read" "order:write"]|[]`,
		`user_1|publishable|Shop|[]|["sc_1"]`,
	}, maker.made, "in the operator's name, the title trimmed")

	maker.makeErr = errors.Invalid("auth_api_key_type_mismatch", "a publishable key cannot carry scopes")
	rec = campaignsRequest(panel, http.MethodPost, APIKeysPath, url.Values{
		formKeyTitle: {"Odd"}, formKeyType: {"publishable"}, formScope: {"invoice:read"},
	}, scopeAdmin)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "a publishable key cannot carry scopes")
	assert.Contains(t, body, "<details open>")
	assert.Contains(t, body, `name="title" value="Odd"`)
	assert.Contains(t, body, `<option value="publishable" selected>`)
	assert.Contains(t, body, `value="invoice:read" checked>`)
	assert.NotContains(t, body, "sk_the-only-copy")
	maker.makeErr = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, APIKeysPath, url.Values{formKeyTitle: {"X"}}, scopeAdmin)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	rec = campaignsRequest(panel, http.MethodPost, APIKeysPath, url.Values{formKeyTitle: {"X"}}, scopeAuthRead)
	assert.Equal(t, http.StatusForbidden, rec.Code)

	plain := usersPanel(t, &fakeKeys{fakeKeyReader: fakeKeyReader{keys: `[]`}})
	assert.NotContains(t, campaignsRequest(plain, http.MethodGet, APIKeysPath, nil, scopeAdmin).Body.String(), "Make a key")
	assert.Equal(t, http.StatusServiceUnavailable,
		campaignsRequest(plain, http.MethodPost, APIKeysPath, url.Values{formKeyTitle: {"X"}}, scopeAdmin).Code)
}
