package identitysession

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sessionPath is the route ADR 0366 adds.
const sessionPath = "/store/v1/auth/session"

// get sends a GET carrying the cookie, when there is one.
func get(t *testing.T, r chi.Router, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
	if cookie != nil {
		req.Header.Set("Cookie", cookie.Name+"="+cookie.Value)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// signedInModule is a module whose clock stands at the given moment, its
// routes, and the cookie a sign-in at that moment issued.
func signedInModule(t *testing.T, at time.Time) (*Module, chi.Router, *http.Cookie) {
	t.Helper()

	hash, err := HashPassword("the real password")
	require.NoError(t, err)
	m := newModuleWith(t, fakeCredentials{customerID: testCustomerID, hash: hash})
	m.sessions.now = fixedClock(at)
	r := chi.NewRouter()
	m.Routes(r)

	rec := post(t, r, "/store/v1/auth/sign-in", `{"email":"known@example.test","password":"the real password"}`)
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

	return m, r, sessionCookie(t, rec)
}

// TestTheSessionSaysWhomItProves is ADR 0366: the cookie a sign-in issued is
// answered with the customer it proves and the moment it ends, and no cache
// keeps the answer.
func TestTheSessionSaysWhomItProves(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.October, 2, 12, 0, 0, 0, time.UTC)
	_, r, cookie := signedInModule(t, at)

	rec := get(t, r, sessionPath, cookie)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))

	var body struct {
		Data struct {
			CustomerID string    `json:"customer_id"`
			ExpiresAt  time.Time `json:"expires_at"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, testCustomerID, body.Data.CustomerID)
	assert.True(t, at.Add(DefaultTTL).Equal(body.Data.ExpiresAt),
		"the session ends one TTL after the sign-in: %s", body.Data.ExpiresAt)
}

// TestASessionReadThatProvesNobodyAnswersAlike is ADR 0366's refusal: no
// cookie, an edited one, one another key signed and an expired one get one
// status, one code and one sentence, uncached.
func TestASessionReadThatProvesNobodyAnswersAlike(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, time.October, 2, 12, 0, 0, 0, time.UTC)
	_, r, cookie := signedInModule(t, at)

	stranger := &Sessions{
		secret: []byte("another-signing-secret-of-thirty-two-bytes"), ttl: DefaultTTL,
		cookieName: DefaultCookieName, secure: true, now: fixedClock(at),
	}
	foreign := httptest.NewRecorder()
	stranger.Issue(foreign, testCustomerID)

	// The expired cookie is read by a module of its own, whose clock has moved
	// a second past the session's end.
	later, laterRoutes, expiring := signedInModule(t, at)
	later.sessions.now = fixedClock(at.Add(DefaultTTL + time.Second))

	answers := map[string]*httptest.ResponseRecorder{
		"no cookie": get(t, r, sessionPath, nil),
		"an edited cookie": get(t, r, sessionPath, &http.Cookie{
			Name: DefaultCookieName, Value: strings.Replace(cookie.Value, testCustomerID, "cust_SOMEBODY_ELSE", 1),
		}),
		"another key's cookie": get(t, r, sessionPath, sessionCookie(t, foreign)),
		"an expired cookie":    get(t, laterRoutes, sessionPath, expiring),
	}

	sentences := map[string]bool{}
	for name, rec := range answers {
		assert.Equal(t, http.StatusUnauthorized, rec.Code, name)
		assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"), name)
		assert.Contains(t, rec.Body.String(), CodeNoSession, name)
		assert.NotContains(t, rec.Body.String(), testCustomerID, name)
		sentences[messageOf(t, rec)] = true
	}
	assert.Len(t, sentences, 1, "every refusal says the same: %v", sentences)
}

// messageOf is the sentence an error answer carries.
func messageOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())

	return body.Error.Message
}
