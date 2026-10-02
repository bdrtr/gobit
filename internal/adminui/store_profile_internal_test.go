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

// fakeSettings holds the profile as scripted and records each write.
type fakeSettings struct {
	profile string
	written []string
	err     error
}

func (f *fakeSettings) StoreProfileJSON(context.Context) (json.RawMessage, error) {
	return json.RawMessage(f.profile), nil
}

func (f *fakeSettings) ReviseStoreProfile(_ context.Context, readUpdatedAt string, profile json.RawMessage) error {
	f.written = append(f.written, readUpdatedAt+"|"+string(profile))
	return f.err
}

// gobitProfile is a profile written at a moment with a fraction of a second.
const gobitProfile = `{"legal_name":"Gobit Ltd","tax_number":"1234567890","tax_office":"Kadikoy",
	"email":"shop@example.com","address":"12 Main St","country_code":"TR","updated_at":"2026-10-02T09:30:15.123456Z"}`

// storeProfilePanel is a panel over the settings surface.
func storeProfilePanel(t *testing.T, settings *fakeSettings) *UI {
	t.Helper()

	panel := newCatalogPanel(t, &fakeCatalog{})
	if settings != nil {
		panel.settings = settings
	}
	panel.scopes = builtInScopes()

	return panel
}

// TestTheStoreProfileScreenShowsTheShop is ADR 0336: a reader sees who the
// shop is, or that it has not said; a writer is offered the form drawn from
// the profile with the moment it was written, to the fraction of a second,
// and the form to write the first profile open when there is none.
func TestTheStoreProfileScreenShowsTheShop(t *testing.T) {
	t.Parallel()

	panel := storeProfilePanel(t, &fakeSettings{profile: gobitProfile})
	rec := campaignsRequest(panel, http.MethodGet, StoreProfilePath, nil, scopeSettingsRead)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	for _, want := range []string{
		"<td>Gobit Ltd</td>", "<td>1234567890</td>", "<td>Kadikoy</td>", "<td>shop@example.com</td>",
		"<td>12 Main St</td>", "<td>TR</td>",
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, `<form method="post" action="`+StoreProfilePath+`"`, "a reader writes nothing")

	body = campaignsRequest(panel, http.MethodGet, StoreProfilePath, nil, scopeSettingsRead, scopeSettingsWrite).Body.String()
	for _, want := range []string{
		`name="read_updated_at" value="2026-10-02T09:30:15.123456Z"`, `name="legal_name" value="Gobit Ltd"`,
		`name="country_code" value="TR"`, "Revise the store profile", `rows="3" placeholder="address, as printed">12 Main St</textarea>`,
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, "<details open>", "a written profile's form is closed")

	none := storeProfilePanel(t, &fakeSettings{profile: `null`})
	body = campaignsRequest(none, http.MethodGet, StoreProfilePath, nil, scopeSettingsRead, scopeSettingsWrite).Body.String()
	assert.Contains(t, body, "The shop has not said who it is; no invoice can be issued until it does.")
	assert.Contains(t, body, `name="read_updated_at" value=""`, "read as never written")
	assert.Contains(t, body, "<details open>", "the first profile's form is open")
	assert.Contains(t, body, "Write the store profile</summary>")

	rec = campaignsRequest(storeProfilePanel(t, nil), http.MethodGet, StoreProfilePath, nil, scopeSettingsRead)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "no surface, no screen")
	rec = campaignsRequest(storeProfilePanel(t, nil), http.MethodPost, StoreProfilePath, url.Values{}, scopeSettingsWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestTheStoreProfileFormWritesFromWhatWasRead: the profile is written
// trimmed, its country in capitals and its address's line breaks as drawn,
// from the moment the page carried, and the page says so; a refusal, a
// profile written since included, comes back with what was typed and the
// moment as it is now, to a writer who cannot read as the reason alone.
func TestTheStoreProfileFormWritesFromWhatWasRead(t *testing.T) {
	t.Parallel()

	settings := &fakeSettings{profile: gobitProfile}
	panel := storeProfilePanel(t, settings)
	both := []string{scopeSettingsRead, scopeSettingsWrite}

	rec := campaignsRequest(panel, http.MethodPost, StoreProfilePath, url.Values{
		formReadUpdatedAt: {"2026-10-02T09:30:15.123456Z"}, formProfileName: {" Gobit A.S. "},
		formProfileTaxNo: {" 1234567890 "}, formProfileOffice: {" Kadikoy "}, formProfileEmail: {" shop@example.com "},
		formProfileAddress: {" 12 Main St\r\nIstanbul "}, formProfileCountry: {" tr "},
	}, both...)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, StoreProfilePath+"?written=1", rec.Header().Get("Location"))
	assert.Equal(t, []string{`2026-10-02T09:30:15.123456Z|{"legal_name":"Gobit A.S.","tax_number":"1234567890",` +
		`"tax_office":"Kadikoy","email":"shop@example.com","address":"12 Main St\nIstanbul","country_code":"TR"}`},
		settings.written)
	landed := campaignsRequest(panel, http.MethodGet, rec.Header().Get("Location"), nil, scopeSettingsRead)
	assert.Contains(t, landed.Body.String(), "The store profile was written.")

	settings.err = errors.Conflict("settings_store_profile_revised",
		"the store profile was written since it was read; draw the page again")
	rec = campaignsRequest(panel, http.MethodPost, StoreProfilePath, url.Values{
		formReadUpdatedAt: {"2026-10-01T00:00:00Z"}, formProfileName: {"Typed Ltd"}, formProfileCountry: {"DE"},
	}, both...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "draw the page again")
	assert.Contains(t, body, `name="legal_name" value="Typed Ltd"`, "what was typed comes back")
	assert.Contains(t, body, `name="read_updated_at" value="2026-10-02T09:30:15.123456Z"`, "with the moment as it is now")
	assert.Contains(t, body, "<details open>")
	rec = campaignsRequest(panel, http.MethodPost, StoreProfilePath, url.Values{formProfileName: {"X"}}, scopeSettingsWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.NotContains(t, rec.Body.String(), "Gobit Ltd", "a writer who cannot read is shown none of the profile")

	settings.err = errors.Invalid("settings_invalid_input", "the country has to be an ISO 3166-1 alpha-2 code")
	rec = campaignsRequest(panel, http.MethodPost, StoreProfilePath, url.Values{formProfileName: {"X"}}, both...)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "alpha-2 code")
	settings.err = errors.Unavailable("db_down", "no answer")
	rec = campaignsRequest(panel, http.MethodPost, StoreProfilePath, url.Values{formProfileName: {"X"}}, both...)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
