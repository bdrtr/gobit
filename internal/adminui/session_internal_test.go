package adminui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// fakeSession is the identity service's in-memory stand-in.
type fakeSession struct {
	token     string
	expiresAt time.Time
	err       error

	logoutCalled      bool
	logoutPrincipalID string
}

func (f *fakeSession) Login(_ context.Context, _, _ string) (string, time.Time, error) {
	if f.err != nil {
		return "", time.Time{}, f.err
	}
	return f.token, f.expiresAt, nil
}

func (f *fakeSession) Logout(_ context.Context, principalID, _ string) (time.Time, error) {
	f.logoutCalled = true
	f.logoutPrincipalID = principalID
	return time.Time{}, nil
}

// fakeAuthenticator is the authenticator's in-memory stand-in.
type fakeAuthenticator struct {
	accepts   string
	principal corehttp.Principal
}

func (f fakeAuthenticator) AuthenticateAdmin(
	_ context.Context, scheme, credential string,
) (corehttp.Principal, error) {
	if !strings.EqualFold(scheme, "Bearer") || credential != f.accepts {
		return corehttp.Principal{}, errors.Unauthorized("test_invalid", "invalid credentials")
	}
	return f.principal, nil
}

func (f fakeAuthenticator) AuthenticateStore(_ context.Context, _ string) (corehttp.Principal, error) {
	return corehttp.Principal{}, errors.Unauthorized("test_invalid", "store surface is not under test")
}

// newTestPanel builds a testable panel.
func newTestPanel(t *testing.T, session Session, auth corehttp.Authenticator, secure bool) *UI {
	t.Helper()

	templates, err := loadTemplates()
	require.NoError(t, err)

	return &UI{session: session, authenticator: auth, templates: templates, secureCookie: secure}
}

// sessionCookie returns the panel cookie on the response, or nil.
func sessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == CookieName {
			return c
		}
	}
	return nil
}

// TestLoginWritesCookieFlags proves a successful sign-in writes the cookie with
// the right flags.
//
// Each of the four flags closes a different attack and any one missing must fail
// this test: HttpOnly guards the token from XSS, SameSite from cross-site
// requests, Path from the cookie reaching the admin API, and Secure from plain
// connections. Path is the MOST IMPORTANT: the admin API's CSRF immunity rests
// on the token not being sent automatically, and a cookie that also went there
// would destroy it.
func TestLoginWritesCookieFlags(t *testing.T) {
	t.Parallel()

	expiresAt := time.Now().Add(time.Hour).UTC()
	panel := newTestPanel(t, &fakeSession{token: "jwt-value", expiresAt: expiresAt}, fakeAuthenticator{}, true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, LoginPath,
		strings.NewReader("email=a@b.c&password=secret"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	panel.submitLogin(rec, req)

	require.Equal(t, http.StatusSeeOther, rec.Code, "a successful sign-in must return 303")
	assert.Equal(t, URLPrefix, rec.Header().Get("Location"))

	cookie := sessionCookie(rec)
	require.NotNil(t, cookie, "the session cookie must be written")
	assert.Equal(t, "jwt-value", cookie.Value)
	assert.Equal(t, URLPrefix, cookie.Path,
		"the cookie must be valid ONLY inside the panel tree; reaching the admin API "+
			"would destroy that surface's CSRF immunity")
	assert.True(t, cookie.HttpOnly, "the token must not touch JavaScript")
	assert.True(t, cookie.Secure, "in a shared environment the cookie must be Secure")
	assert.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
}

// TestLoginSkipsSecureLocally proves the cookie stays sendable in local
// development.
//
// A Secure cookie is NEVER sent over plain HTTP; without the distinction the
// panel could not be opened locally at all. The distinction is not invented —
// it comes from the framework's written decision about "the only environment
// where TLS requirements are relaxed".
func TestLoginSkipsSecureLocally(t *testing.T) {
	t.Parallel()

	panel := newTestPanel(t, &fakeSession{token: "j", expiresAt: time.Now().Add(time.Hour)},
		fakeAuthenticator{}, false)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, LoginPath, strings.NewReader("email=a@b.c&password=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	panel.submitLogin(rec, req)

	cookie := sessionCookie(rec)
	require.NotNil(t, cookie)
	assert.False(t, cookie.Secure, "Secure must be off in local development")
	assert.True(t, cookie.HttpOnly, "HttpOnly must stay on even when Secure is off")
}

// TestFailedLoginWritesNoCookie proves a rejected sign-in opens no session.
//
// The message PRESERVES the distinction the service makes: it does not reveal
// which accounts exist.
func TestFailedLoginWritesNoCookie(t *testing.T) {
	t.Parallel()

	panel := newTestPanel(t,
		&fakeSession{err: errors.Unauthorized("auth_invalid", "email or password is incorrect")},
		fakeAuthenticator{}, true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, LoginPath, strings.NewReader("email=a@b.c&password=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	panel.submitLogin(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Nil(t, sessionCookie(rec), "a failed sign-in must write NO cookie")
	assert.Contains(t, rec.Body.String(), "Email or password is incorrect")
}

// TestGuardReturnsLoginPageWithoutCookie pins what an unidentified request gets.
//
// There is NO redirect: the page comes back with a 401. This is possible
// because the HTML writer is not bound by the 2xx rule, and it is more honest
// than a redirect, which would erase the failure from the status code.
func TestGuardReturnsLoginPageWithoutCookie(t *testing.T) {
	t.Parallel()

	panel := newTestPanel(t, &fakeSession{}, fakeAuthenticator{accepts: "good"}, true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, URLPrefix, http.NoBody)
	panel.Protect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the protected handler must NOT run for an unidentified request")
	})).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html",
		"a browser must get a page, not JSON")
	assert.Contains(t, rec.Body.String(), "Sign in")
}

// TestGuardClearsInvalidCookie proves a dead token is cleaned up.
//
// Without it the browser keeps sending the same dead token on every request and
// the user is stuck on the login page forever.
func TestGuardClearsInvalidCookie(t *testing.T) {
	t.Parallel()

	panel := newTestPanel(t, &fakeSession{}, fakeAuthenticator{accepts: "good"}, true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, URLPrefix, http.NoBody)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: "dead-token"})
	panel.Protect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the protected handler must NOT run with an invalid token")
	})).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	cookie := sessionCookie(rec)
	require.NotNil(t, cookie, "the dead cookie must be dropped")
	assert.Negative(t, cookie.MaxAge, "the cookie must be deleted")
}

// TestGuardPutsPrincipalInContext proves the happy path.
//
// Identity RESOLUTION is not the panel's own work: the same authenticator is
// asked and the result goes into the framework's context key. That is what lets
// the framework's scope check run inside the panel too, and it is why the panel
// writes no token verification of its own.
func TestGuardPutsPrincipalInContext(t *testing.T) {
	t.Parallel()

	expected := corehttp.Principal{ID: "usr_1", Kind: "user", Scopes: []string{"admin"}}
	panel := newTestPanel(t, &fakeSession{}, fakeAuthenticator{accepts: "good", principal: expected}, true)

	var seen corehttp.Principal
	var called bool

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, URLPrefix, http.NoBody)
	req.AddCookie(&http.Cookie{Name: CookieName, Value: "good"})
	panel.Protect(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		called = true
		seen, _ = corehttp.PrincipalFromContext(r.Context())
	})).ServeHTTP(rec, req)

	require.True(t, called, "a valid token must reach the protected handler")
	assert.Equal(t, expected, seen, "the principal must land in the context AS IS")
}

// TestCheckOriginRejectsForeignOrigin pins CSRF's second layer.
func TestCheckOriginRejectsForeignOrigin(t *testing.T) {
	t.Parallel()

	panel := newTestPanel(t, &fakeSession{}, fakeAuthenticator{}, true)

	for _, tc := range []struct {
		name   string
		origin string
	}{
		{"foreign origin", "https://attacker.example"},
		{"no header", ""},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, LoginPath, http.NoBody)
		req.Host = "panel.example"
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}

		panel.CheckOrigin(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Errorf("%s: the request must not pass", tc.name)
		})).ServeHTTP(rec, req)

		assert.Equal(t, http.StatusForbidden, rec.Code, "case: %s", tc.name)
	}
}

// TestCheckOriginAllowsSameOrigin also proves reads are never questioned.
//
// Requiring an origin on GET would make the panel unusable: a navigation typed
// into the address bar sends no Origin.
func TestCheckOriginAllowsSameOrigin(t *testing.T) {
	t.Parallel()

	panel := newTestPanel(t, &fakeSession{}, fakeAuthenticator{}, true)

	for _, tc := range []struct {
		name   string
		method string
		origin string
	}{
		{"same-origin POST", http.MethodPost, "https://panel.example"},
		{"originless GET", http.MethodGet, ""},
	} {
		passed := false
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, URLPrefix, http.NoBody)
		req.Host = "panel.example"
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}

		panel.CheckOrigin(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			passed = true
		})).ServeHTTP(rec, req)

		assert.True(t, passed, "case: %s — the request should have passed", tc.name)
	}
}

// TestLogoutDropsAllSessionsAndClearsCookie proves logout does both of its jobs.
//
// Clearing the cookie alone is NOT enough: the token is stateless, a deleted
// cookie does NOT invalidate it, and a copied token would keep working.
func TestLogoutDropsAllSessionsAndClearsCookie(t *testing.T) {
	t.Parallel()

	session := &fakeSession{}
	panel := newTestPanel(t, session, fakeAuthenticator{}, true)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, LogoutPath, http.NoBody)
	req = req.WithContext(corehttp.WithPrincipal(req.Context(),
		corehttp.Principal{ID: "usr_7", Kind: "user"}))

	panel.submitLogout(rec, req)

	assert.True(t, session.logoutCalled, "logout must REACH the identity service")
	assert.Equal(t, "usr_7", session.logoutPrincipalID)
	assert.Equal(t, http.StatusSeeOther, rec.Code)

	cookie := sessionCookie(rec)
	require.NotNil(t, cookie)
	assert.Negative(t, cookie.MaxAge, "the cookie must be deleted")
}

// TestUnboundRingRejects proves an unbound guard ring does not stay open.
//
// An unprotected admin surface must fail loudly closed rather than stay quietly
// open (ADR 0007's identity line).
func TestUnboundRingRejects(t *testing.T) {
	t.Parallel()

	var ring Ring

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, URLPrefix, http.NoBody)
	ring.Protect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("an unbound ring must NOT let the request through")
	})).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), CodeNotBound,
		"the body must carry the panel's own code so the operator can tell an unbound "+
			"ring from a rejected credential")
}

// TestAnUnexpectedSignInFailureIsAPageNotJSON proves the panel answers a
// browser with a page even when the failure is not a rejection.
//
// The framework's JSON envelope is right for an API endpoint and wrong here:
// this path was navigated to by a BROWSER, and JSON makes the failure
// unreadable to the one person who could act on it. The message of the
// underlying error is not shown either — the framework calls a non-Internal
// message client-safe because a service author wrote it, but that promise was
// made about API clients.
func TestAnUnexpectedSignInFailureIsAPageNotJSON(t *testing.T) {
	t.Parallel()

	panel := newTestPanel(t,
		&fakeSession{err: errors.Unavailable("db_down", "dial tcp 10.0.0.5:5432: connection refused")},
		fakeAuthenticator{}, false)

	req := httptest.NewRequest(http.MethodPost, LoginPath,
		strings.NewReader("email=a@example.com&password=secret"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	panel.submitLogin(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "<html", "a browser must get a page, not a JSON envelope")
	assert.NotContains(t, body, `"error"`, "the JSON envelope must not be written here")
	assert.NotContains(t, body, "10.0.0.5", "the underlying error must not reach the page")
	assert.Nil(t, sessionCookie(rec), "a failed sign-in writes no cookie")
}
