package identitysession

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rules of a signed-in password change (ADR 0375); the statement it reads
// with is in the integration lane.

const changePath = "/store/v1/auth/password"

// changeWith posts a password change carrying a cookie.
func changeWith(t *testing.T, r chi.Router, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, changePath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.Header.Set("Cookie", cookie.Name+"="+cookie.Value)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// TestAPasswordChangeReplacesItAndEndsTheOtherSessions: the owner who knows
// the password replaces it; the other browser is out, this one renewed.
func TestAPasswordChangeReplacesItAndEndsTheOtherSessions(t *testing.T) {
	t.Parallel()

	store, clock, _, r := anchoredModule(t)
	here := signIn(t, r, "the old password")
	there := signIn(t, r, "the old password")
	clock.advance(time.Millisecond)

	rec := changeWith(t, r, here, `{"current_password":"the old password","new_password":"the new password"}`)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

	require.NoError(t, VerifyPassword(store.hash, "the new password"))
	assert.Equal(t, http.StatusUnauthorized, get(t, r, sessionPath, there).Code, "the other browser is out")
	assert.Equal(t, http.StatusOK, get(t, r, sessionPath, sessionCookie(t, rec)).Code, "this one is renewed")
	signIn(t, r, "the new password")
}

// TestAWrongCurrentPasswordChangesNothing: a session alone does not replace a
// password, and a refusal ends nobody's session.
func TestAWrongCurrentPasswordChangesNothing(t *testing.T) {
	t.Parallel()

	store, clock, _, r := anchoredModule(t)
	here := signIn(t, r, "the old password")
	clock.advance(time.Millisecond)

	rec := changeWith(t, r, here, `{"current_password":"a guess","new_password":"the new password"}`)
	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), CodeCurrentPasswordWrong)
	assert.Zero(t, store.puts)
	assert.True(t, store.anchor.IsZero(), "nobody was signed out")
	assert.Equal(t, http.StatusOK, get(t, r, sessionPath, here).Code)
}

// TestARefusedNewPasswordIsCheckedFirst: an empty new password is refused
// before the current one is looked at.
func TestARefusedNewPasswordIsCheckedFirst(t *testing.T) {
	t.Parallel()

	store, _, _, r := anchoredModule(t)
	here := signIn(t, r, "the old password")

	rec := changeWith(t, r, here, `{"current_password":"a guess","new_password":"  "}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), CodePasswordChangeInvalid)
	assert.Zero(t, store.reads, "the current password was not looked at")
	assert.Zero(t, store.puts)
}

// TestAPasswordChangeAsksForAProvenCustomer: no session, no change.
func TestAPasswordChangeAsksForAProvenCustomer(t *testing.T) {
	t.Parallel()

	_, _, _, r := anchoredModule(t)
	rec := changeWith(t, r, nil, `{"current_password":"the old password","new_password":"the new password"}`)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), CodeNoSession)
}

// TestACustomerWithNoPasswordHereHasNoneToChange: signed in some other way,
// they are told so rather than refused as if they had guessed wrong.
func TestACustomerWithNoPasswordHereHasNoneToChange(t *testing.T) {
	t.Parallel()

	store, _, _, r := anchoredModule(t)
	stranger := newSessions(t, time.Now)
	stranger.anchors = store
	rec := httptest.NewRecorder()
	stranger.Issue(rec, "cust_SIGNED_IN_ELSEWHERE")

	answer := changeWith(t, r, sessionCookie(t, rec), `{"current_password":"x","new_password":"y"}`)
	assert.Equal(t, http.StatusConflict, answer.Code, answer.Body.String())
	assert.Contains(t, answer.Body.String(), CodeNoPasswordHere)
}

// TestAChangeThatCannotEndTheSessionsLeavesThePassword: the anchor moves
// before the password, as a reset's does.
func TestAChangeThatCannotEndTheSessionsLeavesThePassword(t *testing.T) {
	t.Parallel()

	store, _, _, r := anchoredModule(t)
	here := signIn(t, r, "the old password")
	store.endErr = errors.New("the database is down")

	rec := changeWith(t, r, here, `{"current_password":"the old password","new_password":"the new password"}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.Zero(t, store.puts)
	assert.NoError(t, VerifyPassword(store.hash, "the old password"))
}

// TestAStoreThatCannotReadByCustomerMountsNoChange: the route exists only
// where the credential can be found from a session.
func TestAStoreThatCannotReadByCustomerMountsNoChange(t *testing.T) {
	t.Parallel()

	hash, err := HashPassword("the real password")
	require.NoError(t, err)
	m := newModuleWith(t, fakeCredentials{customerID: testCustomerID, hash: hash})
	r := chi.NewRouter()
	m.Routes(r)

	rec := changeWith(t, r, signIn(t, r, "the real password"),
		`{"current_password":"the real password","new_password":"another"}`)
	assert.Contains(t, []int{http.StatusNotFound, http.StatusMethodNotAllowed}, rec.Code)
}
