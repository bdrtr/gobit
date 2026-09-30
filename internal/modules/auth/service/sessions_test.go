package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// TestASessionIsClosedAlone is ADR 0267: each sign-in writes a session its
// token names, closing one refuses that token and no other, and the listing
// shows the open ones with the current one marked.
func TestASessionIsClosedAlone(t *testing.T) {
	t.Parallel()

	svc, interop, _, clock := setupSession(t)
	laptop := obtainSessionToken(t, svc, sessionPassword)
	clock.moment = clock.moment.Add(time.Minute)
	phone := obtainSessionToken(t, svc, sessionPassword)

	onPhone, err := resolveSessionPrincipal(interop, phone)
	require.NoError(t, err)
	require.NotEmpty(t, onPhone.SessionID, "a token names the session its sign-in opened")
	onLaptop, err := resolveSessionPrincipal(interop, laptop)
	require.NoError(t, err)
	require.NotEqual(t, onPhone.SessionID, onLaptop.SessionID, "each sign-in is its own session")

	listed, err := svc.ListSessions(context.Background(), onPhone)
	require.NoError(t, err)
	require.Len(t, listed, 2)
	assert.Equal(t, onPhone.SessionID, listed[0].ID, "newest first")
	assert.True(t, listed[0].Current)
	assert.False(t, listed[1].Current)

	require.NoError(t, svc.CloseSession(context.Background(), onPhone.ID, onLaptop.SessionID))
	_, err = resolveSessionPrincipal(interop, laptop)
	requireSessionRejected(t, err, "the closed session's token is refused")
	_, err = resolveSessionPrincipal(interop, phone)
	require.NoError(t, err, "the other session goes on")

	err = svc.CloseSession(context.Background(), onPhone.ID, onLaptop.SessionID)
	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "a session already closed is not open")
	assert.Equal(t, service.CodeSessionNotOpen, coreerrors.CodeOf(err))
}

// TestTheOtherSessionsCloseAndTheCurrentOneStays is the phone closing the
// laptop it left signed in.
func TestTheOtherSessionsCloseAndTheCurrentOneStays(t *testing.T) {
	t.Parallel()

	svc, interop, _, clock := setupSession(t)
	first := obtainSessionToken(t, svc, sessionPassword)
	clock.moment = clock.moment.Add(time.Minute)
	second := obtainSessionToken(t, svc, sessionPassword)
	clock.moment = clock.moment.Add(time.Minute)
	current := obtainSessionToken(t, svc, sessionPassword)

	principal, err := resolveSessionPrincipal(interop, current)
	require.NoError(t, err)
	closed, err := svc.CloseOtherSessions(context.Background(), principal)
	require.NoError(t, err)
	assert.Equal(t, int64(2), closed)

	for _, token := range []string{first, second} {
		_, err = resolveSessionPrincipal(interop, token)
		requireSessionRejected(t, err, "an other session is closed")
	}
	_, err = resolveSessionPrincipal(interop, current)
	require.NoError(t, err, "the session the request was made with stays")
}

// TestLoggingOutEverywhereEmptiesTheListing holds the listing to the
// verification's anchor: the sessions a logout dropped are not listed, though
// their rows are not closed one by one.
func TestLoggingOutEverywhereEmptiesTheListing(t *testing.T) {
	t.Parallel()

	svc, interop, _, clock := setupSession(t)
	token := obtainSessionToken(t, svc, sessionPassword)
	principal, err := resolveSessionPrincipal(interop, token)
	require.NoError(t, err)

	clock.moment = clock.moment.Add(time.Minute)
	_, err = svc.Logout(context.Background(), principal.ID, principal.Kind)
	require.NoError(t, err)

	listed, err := svc.ListSessions(context.Background(), principal)
	require.NoError(t, err)
	assert.Empty(t, listed, "a session the anchor dropped is not listed")

	clock.moment = clock.moment.Add(time.Minute)
	fresh := obtainSessionToken(t, svc, sessionPassword)
	principal, err = resolveSessionPrincipal(interop, fresh)
	require.NoError(t, err)
	listed, err = svc.ListSessions(context.Background(), principal)
	require.NoError(t, err)
	assert.Len(t, listed, 1)
}

// TestATokenWithoutASessionIsJudgedByTheAnchor keeps the tokens signed before
// sessions were recorded working until they expire, and closable by logging
// out everywhere.
func TestATokenWithoutASessionIsJudgedByTheAnchor(t *testing.T) {
	t.Parallel()

	svc, interop, repo, clock := setupSession(t)
	legacy, err := service.IssueLegacyToken(svc, repo.user.ID, repo.user.Scopes, clock.moment)
	require.NoError(t, err)

	principal, err := resolveSessionPrincipal(interop, legacy)
	require.NoError(t, err)
	assert.Empty(t, principal.SessionID)

	clock.moment = clock.moment.Add(time.Minute)
	_, err = svc.Logout(context.Background(), principal.ID, principal.Kind)
	require.NoError(t, err)
	_, err = resolveSessionPrincipal(interop, legacy)
	requireSessionRejected(t, err, "logging out everywhere still drops it")
}

// TestATokenNamingAnotherPersonsSessionIsRefused holds a token to the owner of
// the session it names, and one naming no row at all is refused too.
func TestATokenNamingAnotherPersonsSessionIsRefused(t *testing.T) {
	t.Parallel()

	svc, interop, repo, clock := setupSession(t)
	require.NoError(t, repo.InsertSession(context.Background(), models.Session{
		ID: "sess_somebody_else", UserID: "user_somebody_else",
		CreatedAt: clock.moment, ExpiresAt: clock.moment.Add(time.Hour),
	}))

	for name, sessionID := range map[string]string{
		"another person's session": "sess_somebody_else",
		"a session that never was": "sess_never",
	} {
		token, err := service.IssueTokenNaming(svc, repo.user.ID, repo.user.Scopes, clock.moment, sessionID)
		require.NoError(t, err)
		_, err = resolveSessionPrincipal(interop, token)
		requireSessionRejected(t, err, name)
	}
}

// TestASessionKeepsTheBrowserThatOpenedIt is ADR 0276: the listing shows what
// each browser said it was, on one line, and a description longer than a
// session keeps is cut at a character, never inside one.
func TestASessionKeepsTheBrowserThatOpenedIt(t *testing.T) {
	t.Parallel()

	svc, interop, _, clock := setupSession(t)
	laptop, _, err := svc.Login(context.Background(), sessionEmail, sessionPassword, "",
		" Mozilla/5.0 (X11; Linux x86_64)\r\n Firefox/131.0 ")
	require.NoError(t, err)
	clock.moment = clock.moment.Add(time.Minute)
	long := strings.Repeat("a", models.MaxUserAgent-1) + "日" + "tail"
	phone, _, err := svc.Login(context.Background(), sessionEmail, sessionPassword, "", long)
	require.NoError(t, err)

	onPhone, err := resolveSessionPrincipal(interop, phone)
	require.NoError(t, err)
	_, err = resolveSessionPrincipal(interop, laptop)
	require.NoError(t, err)

	listed, err := svc.ListSessions(context.Background(), onPhone)
	require.NoError(t, err)
	require.Len(t, listed, 2)
	assert.Equal(t, strings.Repeat("a", models.MaxUserAgent-1), listed[0].UserAgent,
		"cut before the character that would not fit, not inside it")
	assert.Equal(t, "Mozilla/5.0 (X11; Linux x86_64) Firefox/131.0", listed[1].UserAgent,
		"control characters are dropped so the label prints on one line")
}
