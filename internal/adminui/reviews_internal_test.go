package adminui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// renderReviews serves the review shell and returns the page.
func renderReviews(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()

	templates, err := loadTemplates()
	require.NoError(t, err)

	panel := &UI{templates: templates}
	rec := httptest.NewRecorder()
	panel.showReviews(rec, httptest.NewRequest(http.MethodGet, ReviewsPath, http.NoBody))

	return rec
}

// TestTheReviewShellCarriesTheTwoPathsItsClientNeeds is what makes the screen a
// client rather than a blank page.
//
// The shell renders no review. What it must carry is the script that fetches
// them and the prefix that script calls — and if either is missing the operator
// gets an empty box with no error, which is the failure mode this whole screen
// has to avoid: an empty moderation queue is the one answer it must never give
// wrongly.
func TestTheReviewShellCarriesTheTwoPathsItsClientNeeds(t *testing.T) {
	t.Parallel()

	rec := renderReviews(t)
	require.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, ReviewsScriptPath,
		"the shell does not load its client, so the screen stays empty forever")
	assert.Contains(t, body, corehttp.DefaultAdminPrefix,
		"the shell names no API prefix, so the client would call a path of its own guessing")
	assert.Contains(t, body, "<noscript",
		"a page whose whole content arrives by script must say so when the script does not "+
			"run; an empty box reads as an empty queue")
}

// TestTheReviewScreenIsInTheMenu keeps the section reachable.
//
// A route with no menu entry is a screen only somebody who knows the URL can
// open, which for a moderation queue means nobody moderates.
func TestTheReviewScreenIsInTheMenu(t *testing.T) {
	t.Parallel()

	var found bool
	for _, item := range sections() {
		if item.Path == ReviewsPath {
			found = true

			assert.Equal(t, reviewsLabel, item.Label)
		}
	}

	assert.True(t, found, "the review section is served by a route and named in no menu")
}

// TestTheReviewScriptIsServedAsExecutableJavaScript is a content-type test, and
// it is not pedantry.
//
// [corehttp.WriteAsset] sends X-Content-Type-Options: nosniff, so a browser
// REFUSES to execute a script served under the wrong type rather than guessing.
// The screen would then render its shell, run nothing, and show an empty
// moderation queue — with a 200 and no error anywhere.
func TestTheReviewScriptIsServedAsExecutableJavaScript(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	(&UI{}).serveReviewsScript(rec,
		httptest.NewRequest(http.MethodGet, ReviewsScriptPath, http.NoBody))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript"),
		"the script is served as %q; with nosniff the browser will not run it",
		rec.Header().Get("Content-Type"))
	assert.NotEmpty(t, rec.Body.String())
}

// TestTheReviewClientSendsNoCredentialOfItsOwn reads the shipped script.
//
// The session is an HttpOnly cookie the browser attaches by itself (ADR 0011,
// unchanged by ADR 0030). A client that built an Authorization header would
// need the token in JavaScript, which is the one thing that decision refuses —
// and the refusal is worth checking on the FILE, because it is the file that
// ships.
func TestTheReviewClientSendsNoCredentialOfItsOwn(t *testing.T) {
	t.Parallel()

	script := string(reviewsScript)

	// Each check names the SPELLING a real use would take, not the bare word:
	// this file's own comments explain why it does none of these things, and a
	// test matching the word would make the code unable to say so. The failures
	// print the offending token rather than the file, because a 250-line body
	// in a message is a message nobody reads to the end.
	for _, forbidden := range []struct{ token, why string }{
		{`"Authorization"`, "the client sets an Authorization header, so the token would " +
			"have to be in JavaScript — the one thing ADR 0011 refuses and ADR 0030 left " +
			"standing"},
		{"localStorage", "the client keeps something in localStorage; ADR 0011 refuses it"},
		{"sessionStorage", "the client keeps something in sessionStorage; same refusal"},
		{"innerHTML =", "the client writes markup from API values"},
		{"innerHTML=", "the client writes markup from API values"},
		{"insertAdjacentHTML", "the client writes markup from API values"},
		{"document.write", "the client writes markup from API values"},
	} {
		if strings.Contains(script, forbidden.token) {
			t.Errorf("assets/reviews.js contains %q: %s", forbidden.token, forbidden.why)
		}
	}

	// The counterpart, and it is what makes the list above mean something: a
	// file that did none of those things because it made no requests at all
	// would pass every check and show an empty screen.
	for _, required := range []struct{ token, why string }{
		{"same-origin", "the client does not send the session cookie, so every call is " +
			"unauthenticated and the screen is empty"},
		{"createTextNode", "the client builds no text nodes, so it is putting API values " +
			"into the page some other way"},
		{"/reviews", "the client calls no review endpoint at all"},
	} {
		if !strings.Contains(script, required.token) {
			t.Errorf("assets/reviews.js does not contain %q: %s", required.token, required.why)
		}
	}
}
