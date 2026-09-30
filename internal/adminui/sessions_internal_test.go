package adminui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// fakeSessions stands in for the auth module's surface.
type fakeSessions struct {
	raw json.RawMessage
	err error

	listedFor, listedCurrent string
	closed, keptCurrent      string
}

func (f *fakeSessions) SessionsJSON(_ context.Context, userID, current string) (json.RawMessage, error) {
	f.listedFor, f.listedCurrent = userID, current
	return f.raw, nil
}

func (f *fakeSessions) CloseSession(_ context.Context, _, sessionID string) error {
	f.closed = sessionID
	return f.err
}

func (f *fakeSessions) CloseOtherSessions(_ context.Context, _, current string) (int64, error) {
	f.keptCurrent = current
	return 1, f.err
}

// sessionsRequest sends a request to the screen as a person signed in with
// the session "sess_here".
func sessionsRequest(t *testing.T, surface SessionAdmin, method, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	templates, err := loadTemplates()
	require.NoError(t, err)
	ui := &UI{templates: templates, sessions: surface}
	r := chi.NewRouter()
	r.Get(SessionsPath, ui.showSessions)
	r.Post(SessionsRevokePath, ui.submitSessionRevoke)
	r.Post(SessionsRevokeOthersPath, ui.submitSessionsRevokeOthers)

	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(),
		corehttp.Principal{ID: "usr_person", Kind: "user", SessionID: "sess_here"}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

const twoSessions = `[
 {"session_id":"sess_here","signed_in_at":"2026-09-30T10:00:00Z","ends_at":"2026-09-30T22:00:00Z","current":true},
 {"session_id":"sess_laptop","signed_in_at":"2026-09-29T08:00:00Z","ends_at":"2026-09-29T20:00:00Z","current":false}
]`

// TestTheScreenListsTheSessionsAndClosesOnlyTheOthers draws the sessions
// with the current one marked and closable only through the others.
func TestTheScreenListsTheSessionsAndClosesOnlyTheOthers(t *testing.T) {
	t.Parallel()

	surface := &fakeSessions{raw: json.RawMessage(twoSessions)}
	rec := sessionsRequest(t, surface, http.MethodGet, SessionsPath, nil)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Equal(t, "usr_person", surface.listedFor)
	assert.Equal(t, "sess_here", surface.listedCurrent, "the surface is told which session is current")
	assert.Contains(t, body, "2026-09-30 10:00 UTC")
	assert.Contains(t, body, "this session")
	assert.Contains(t, body, `value="sess_laptop"`, "another session can be closed")
	assert.NotContains(t, body, `value="sess_here"`, "the current one is closed by signing out")
	assert.Contains(t, body, `action="`+SessionsRevokeOthersPath+`"`)

	closed := sessionsRequest(t, surface, http.MethodPost, SessionsRevokePath, url.Values{"id": {"sess_laptop"}})
	assert.Equal(t, http.StatusSeeOther, closed.Code)
	assert.Equal(t, "sess_laptop", surface.closed)

	others := sessionsRequest(t, surface, http.MethodPost, SessionsRevokeOthersPath, url.Values{})
	assert.Equal(t, http.StatusSeeOther, others.Code)
	assert.Equal(t, "sess_here", surface.keptCurrent, "the others close around the current session")
}

// TestAClosedSessionIsSaidToBeClosed words a close that found nothing open.
func TestAClosedSessionIsSaidToBeClosed(t *testing.T) {
	t.Parallel()

	surface := &fakeSessions{raw: json.RawMessage(`[]`), err: errors.NotFound("auth_session_not_open", "none")}
	rec := sessionsRequest(t, surface, http.MethodPost, SessionsRevokePath, url.Values{"id": {"sess_gone"}})

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "That session is not open any more.")
	assert.Contains(t, rec.Body.String(), "No other session is open.")
}

// TestAnInstallationWithoutTheSessionSurfaceSaysSo keeps the optional
// resolution honest.
func TestAnInstallationWithoutTheSessionSurfaceSaysSo(t *testing.T) {
	t.Parallel()

	rec := sessionsRequest(t, nil, http.MethodGet, SessionsPath, nil)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "session surface is not registered")
}
