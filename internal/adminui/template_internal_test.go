package adminui

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// TestTemplatesParseAtStartup proves the embedded templates load and that every
// expected page has a set.
func TestTemplatesParseAtStartup(t *testing.T) {
	t.Parallel()

	set, err := loadTemplates()
	require.NoError(t, err, "embedded templates must parse")
	require.NotNil(t, set)

	for _, name := range pages {
		assert.NotNil(t, set.sets[name], "%s must have a template set", name)
	}
}

// TestMissingPageStopsStartup proves a page name with no file behind it is
// caught AT STARTUP.
//
// A page name is a STRING: a typo compiles, lints clean, and blows up only when
// that page is opened — in front of a user. The check pulls the failure back to
// startup, so it shows up in deployment instead.
func TestMissingPageStopsStartup(t *testing.T) {
	previous := pages
	t.Cleanup(func() { pages = previous })

	pages = append(slices.Clone(previous), "never-written.gohtml")

	_, err := loadTemplates()
	require.Error(t, err, "a listed page with no embedded file must fail")
	assert.Equal(t, CodeTemplateInvalid, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "never-written.gohtml",
		"the error must NAME the page that could not be resolved; without it, diagnosis "+
			"means going to the source")
}

// TestUnlistedPageStopsStartup catches the list going stale in the OTHER
// direction: a file that is embedded but referenced nowhere is dead weight.
//
// A one-way check (only "is every listed page present") would miss the change
// that forgot to delete a file, and the dead page would keep shipping in the
// binary.
func TestUnlistedPageStopsStartup(t *testing.T) {
	previous := pages
	t.Cleanup(func() { pages = previous })

	pages = slices.Clone(previous[:len(previous)-1])

	_, err := loadTemplates()
	require.Error(t, err, "an embedded page missing from the list must fail")
	assert.Equal(t, CodeTemplateInvalid, errors.CodeOf(err))
	assert.Contains(t, err.Error(), previous[len(previous)-1],
		"the error must name the page missing from the list")
}

// TestPageEscapesUserData proves the template engine's automatic escaping
// actually runs.
//
// The panel runs inside an ADMINISTRATOR's session; an XSS there hands the
// attacker admin privileges. The claim has two halves and the second is easy to
// skip: the output must contain no raw tag, but it must also contain no
// "ZgotmplZ". The engine does NOT error when it cannot resolve a context — it
// silently prints that marker, so escaping looks like it worked while the data
// disappeared, and no test fails.
func TestPageEscapesUserData(t *testing.T) {
	t.Parallel()

	set, err := loadTemplates()
	require.NoError(t, err)

	const malicious = `<script>alert('admin')</script>`

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, URLPrefix, http.NoBody)
	set.render(rec, req, http.StatusOK, "login.gohtml", map[string]any{
		"Title":     malicious,
		"LoginPath": LoginPath,
		"Error":     malicious,
		"Email":     malicious,
	})

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()

	assert.NotContains(t, body, "<script>", "user data must not be printed as a raw tag")
	assert.Contains(t, body, "&lt;script&gt;", "the data must be escaped, not dropped")
	assert.NotContains(t, body, "ZgotmplZ",
		"the engine failed to resolve a context: escaping LOOKS like it worked but the "+
			"data is silently removed")
}

// TestBrokenPageReturnsError proves no HALF page is written when rendering
// fails.
//
// Streamed straight to the writer, an error arising midway would leave a
// half-written body carrying a 200: once the header is out, neither the panic
// recoverer nor the error writer can act and the failure goes silent on the
// client. The buffer keeps the error somewhere it can still become a 500.
func TestBrokenPageReturnsError(t *testing.T) {
	t.Parallel()

	set, err := loadTemplates()
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, URLPrefix, http.NoBody)
	// A page name with no set behind it.
	set.render(rec, req, http.StatusOK, "no-such-page.gohtml", nil)

	assert.Equal(t, http.StatusInternalServerError, rec.Code,
		"the status must NOT stay 200 when rendering fails")
	assert.NotContains(t, rec.Body.String(), "<html", "no half page may be written")
	assert.True(t, strings.Contains(rec.Body.String(), `"error"`),
		"the body must be core's error envelope: %s", rec.Body.String())
}
