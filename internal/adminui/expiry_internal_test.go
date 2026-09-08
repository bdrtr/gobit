package adminui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// markerCookie returns the marker cookie on the response, or nil.
func markerCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == MarkerName {
			return c
		}
	}
	return nil
}

// guardOnce runs the ring over one unidentified GET and returns the recorder.
func guardOnce(t *testing.T, panel *UI, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, URLPrefix+"/orders?page=2", http.NoBody)

	for _, c := range cookies {
		req.AddCookie(c)
	}

	panel.Protect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the protected handler must NOT run for an unidentified request")
	})).ServeHTTP(rec, req)

	return rec
}

// TestAnExpiredSessionSaysSoAndAFirstVisitDoesNot is the defect ADR 0031 named
// and accepted, now closed.
//
// # What was wrong
//
// The session cookie's lifetime is tied to the token's expiry, so a session that
// runs out NATURALLY sends no cookie at all. The guard then took the same branch
// a first-time visitor takes and printed a login form with an EMPTY message —
// at the exact moment an operator most needs an explanation. The helpful
// sentence lived on the branch reached when a cookie exists but its token does
// not, which the cookie design makes unreachable in the ordinary case.
//
// # Why both halves are asserted in one test
//
// Because the fix is a DISTINCTION, not a message. Printing the sentence
// unconditionally would also "fix" the expired case, and would then greet
// somebody opening the panel for the first time with the claim that their
// session expired. Only the pair proves the distinction exists.
func TestAnExpiredSessionSaysSoAndAFirstVisitDoesNot(t *testing.T) {
	t.Parallel()

	panel := newTestPanel(t, &fakeSession{}, fakeAuthenticator{accepts: "good"}, true)

	first := guardOnce(t, panel)
	assert.NotContains(t, first.Body.String(), sessionExpiredMessage,
		"a browser that has never signed in must not be told its session expired")

	lapsed := guardOnce(t, panel, &http.Cookie{Name: MarkerName, Value: markerValue})
	assert.Contains(t, lapsed.Body.String(), sessionExpiredMessage,
		"a browser whose session existed and whose cookie is gone must be told why")
}

// TestTheExpirySentenceIsPrintedOnceProves the message is about a transition.
//
// The marker is dropped as the sentence is written, so a reload shows the form
// rather than repeating an expiry that has already been explained. Without this
// the panel would keep claiming a session expired for as long as the marker
// lived — a week, under [markerGrace].
func TestTheExpirySentenceIsPrintedOnce(t *testing.T) {
	t.Parallel()

	panel := newTestPanel(t, &fakeSession{}, fakeAuthenticator{accepts: "good"}, true)

	rec := guardOnce(t, panel, &http.Cookie{Name: MarkerName, Value: markerValue})

	cookie := markerCookie(rec)
	require.NotNil(t, cookie, "the marker must be dropped once its sentence is printed")
	assert.Negative(t, cookie.MaxAge, "the marker must be deleted, not rewritten")
}

// TestAnEmptyMarkerIsNotASession proves the value is compared, not the presence.
//
// A browser sends a name with an empty value for a cookie somebody deleted
// badly. Treating that as "a session existed" would print the expiry sentence at
// a moment nothing expired, which is the same lie in the other direction.
func TestAnEmptyMarkerIsNotASession(t *testing.T) {
	t.Parallel()

	panel := newTestPanel(t, &fakeSession{}, fakeAuthenticator{accepts: "good"}, true)

	rec := guardOnce(t, panel, &http.Cookie{Name: MarkerName, Value: ""})
	assert.NotContains(t, rec.Body.String(), sessionExpiredMessage)
}

// TestSigningOutIsNotAnExpiry proves the two are told apart.
//
// A deliberate sign-out drops the marker, so the next visit to the login page
// shows a plain form. Leaving the marker behind would have the panel tell an
// operator who just pressed "sign out" that their session expired.
func TestSigningOutIsNotAnExpiry(t *testing.T) {
	t.Parallel()

	panel := newTestPanel(t, &fakeSession{}, fakeAuthenticator{}, true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, LogoutPath, http.NoBody)
	panel.submitLogout(rec, req)

	cookie := markerCookie(rec)
	require.NotNil(t, cookie, "signing out must drop the marker")
	assert.Negative(t, cookie.MaxAge)
}

// TestTheMarkerOutlivesTheTokenAndCarriesNothing proves both of its properties.
//
// It has to outlive the token — a marker that died with the credential would be
// gone in exactly the case it exists to explain. And it must carry no
// credential: its value is a constant, so stealing it wins an attacker the
// knowledge that somebody once signed in, which the login page already implies.
func TestTheMarkerOutlivesTheTokenAndCarriesNothing(t *testing.T) {
	t.Parallel()

	expiresAt := time.Now().Add(time.Hour).UTC()
	panel := newTestPanel(t, &fakeSession{token: "jwt-value", expiresAt: expiresAt}, fakeAuthenticator{}, true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, LoginPath, strings.NewReader("email=a@b.c&password=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	panel.submitLogin(rec, req)

	marker := markerCookie(rec)
	require.NotNil(t, marker, "signing in must record that a session existed")
	assert.NotContains(t, marker.Value, "jwt-value", "the marker must carry no credential")
	assert.Equal(t, markerValue, marker.Value)
	assert.True(t, marker.Expires.After(expiresAt),
		"a marker that died with the token would be gone in the case it explains")
	// The marker follows the session cookie's path exactly, and that is the
	// whole requirement: browsers match cookies by name AND path, so a marker
	// written under a different path is a marker the expiry page cannot find —
	// and the operator is told nothing about why they were signed out.
	assert.Equal(t, CookiePath, marker.Path,
		"the marker is not on the session cookie's path, so nothing will read it")
	assert.True(t, marker.HttpOnly)
	assert.True(t, marker.Secure)
}

// TestTheReturnToRefusesEverythingOutsideThePanel is the open-redirect guard.
//
// The value round-trips through a hidden form field, so it reaches the login
// handler as attacker-influenceable input even though the guard itself only ever
// produces panel paths. Every rejected row below is a redirect somebody has
// actually been caught by.
func TestTheReturnToRefusesEverythingOutsideThePanel(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		target string
		ok     bool
		why    string
	}{
		{"a panel page", URLPrefix + "/orders", true, ""},
		{"a panel page with a query", URLPrefix + "/orders?page=2", true, ""},
		{"the panel root", URLPrefix, true, ""},
		{"empty", "", false, "nothing was requested"},
		{"protocol relative", "//evil.example/", false,
			"it passes a naive \"starts with /\" check and lands on another origin"},
		{"absolute", "https://evil.example/", false, "another origin"},
		{"another origin wearing the panel's path", "https://evil.example" + URLPrefix + "/orders", false,
			"the path is exactly what the allow-list looks for; only the host says otherwise"},
		{"a traversal that keeps the prefix", URLPrefix + "/../../admin/v1/orders", false,
			"the raw string carries the prefix and the resolved path does not"},
		{"a traversal to the root", URLPrefix + "/../..", false, "same escape, shorter"},
		{"a backslash", "/\\evil.example", false, "some browsers fold it to a forward slash"},
		{"a backslash inside the panel", URLPrefix + "/x\\..\\..\\evil", false,
			"path.Clean does not see it, and the browser does"},
		{"the admin API", "/admin/v1/orders", false, "the JSON API is somebody else's tree"},
		{"a prefix lookalike", URLPrefix + "-evil/x", false,
			"a string prefix check without the separator accepts a neighboring path"},
		{"the login page", LoginPath, false, "signing in must not return to the form"},
		{"the login page with a query", LoginPath + "?x=1", false, "the query does not make it a page"},
		{"the stylesheet", StylesheetPath, false, "it is not a page a person is on"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.ok, safeReturnTarget(tc.target), tc.why)
		})
	}
}

// TestSignInReturnsToTheInterruptedPage proves the other half of ADR 0031's
// repair: a message AND a return-to.
//
// An operator interrupted on page two of the orders list lands back on page two,
// not on the panel's front page. The destination survives the round trip through
// the form, and it is validated again on the way out rather than trusted because
// the template wrote it.
func TestSignInReturnsToTheInterruptedPage(t *testing.T) {
	t.Parallel()

	panel := newTestPanel(t, &fakeSession{}, fakeAuthenticator{accepts: "good"}, true)

	interrupted := guardOnce(t, panel, &http.Cookie{Name: MarkerName, Value: markerValue})
	assert.Contains(t, interrupted.Body.String(), URLPrefix+"/orders?page=2",
		"the login form must carry the page the operator was interrupted on")

	signIn := newTestPanel(t, &fakeSession{token: "j", expiresAt: time.Now().Add(time.Hour)},
		fakeAuthenticator{}, true)

	rec := httptest.NewRecorder()
	body := url.Values{
		"email":    {"a@b.c"},
		"password": {"x"},
		nextField:  {URLPrefix + "/orders?page=2"},
	}.Encode()
	req := httptest.NewRequest(http.MethodPost, LoginPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	signIn.submitLogin(rec, req)

	assert.Equal(t, URLPrefix+"/orders?page=2", rec.Header().Get("Location"))
}

// TestAHostileReturnToLandsOnThePanelRoot proves the refusal is not a 500.
//
// A rejected destination is not an error the operator has to see: they asked to
// sign in and they are signed in. The fallback is the panel's front page, which
// is also where somebody who typed the login URL meant to go.
func TestAHostileReturnToLandsOnThePanelRoot(t *testing.T) {
	t.Parallel()

	panel := newTestPanel(t, &fakeSession{token: "j", expiresAt: time.Now().Add(time.Hour)},
		fakeAuthenticator{}, true)

	rec := httptest.NewRecorder()
	body := url.Values{"email": {"a@b.c"}, "password": {"x"}, nextField: {"//evil.example/"}}.Encode()
	req := httptest.NewRequest(http.MethodPost, LoginPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	panel.submitLogin(rec, req)

	require.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, URLPrefix, rec.Header().Get("Location"),
		"a hostile destination must be dropped, not shown as an error")
}

// TestAMistypedPasswordKeepsTheDestination proves the return-to survives a
// failed attempt.
//
// That is the moment losing it would be most annoying: the operator was
// interrupted, typed the password wrong, and would then be dropped on the front
// page after finally getting in.
func TestAMistypedPasswordKeepsTheDestination(t *testing.T) {
	t.Parallel()

	panel := newTestPanel(t, &fakeSession{err: errUnauthorizedForTest()}, fakeAuthenticator{}, true)

	rec := httptest.NewRecorder()
	body := url.Values{
		"email":    {"a@b.c"},
		"password": {"wrong"},
		nextField:  {URLPrefix + "/orders?page=2"},
	}.Encode()
	req := httptest.NewRequest(http.MethodPost, LoginPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	panel.submitLogin(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), URLPrefix+"/orders?page=2",
		"the re-rendered form must still carry the destination")
}

// TestOnlyAGetIsWorthReturningTo proves the method restriction.
//
// A return-to is a promise to replay the navigation, and the only navigation a
// Location header can replay is a GET.
func TestOnlyAGetIsWorthReturningTo(t *testing.T) {
	t.Parallel()

	get := httptest.NewRequest(http.MethodGet, URLPrefix+"/orders", http.NoBody)
	assert.Equal(t, URLPrefix+"/orders", requestedPath(get))

	post := httptest.NewRequest(http.MethodPost, URLPrefix+"/orders", http.NoBody)
	assert.Empty(t, requestedPath(post),
		"sending an operator to a POST target after signing in shows them a page they did not ask for")
}

// errUnauthorizedForTest is the rejection the identity service returns for bad
// credentials.
func errUnauthorizedForTest() error {
	return coreerrors.Unauthorized("test_invalid", "invalid credentials")
}
