package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/auth/api"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// TestASignInHandsTheServiceItsBrowser is ADR 0276 at the door: the sign-in
// passes the browser's own description to the service, and the listing shows
// each session's.
func TestASignInHandsTheServiceItsBrowser(t *testing.T) {
	r, svc := scopedRouter(t)

	req := httptest.NewRequest(http.MethodPost, api.LoginPath,
		strings.NewReader(`{"email":"a@b.co","password":"secret"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 Firefox/131.0")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "Mozilla/5.0 Firefox/131.0", svc.lastUserAgent)

	svc.sessions = []service.SessionView{{
		ID: "sess_1", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), UserAgent: "Safari/17",
	}}
	listed := request(t, r, http.MethodGet, api.SessionsPath, "")
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	assert.Contains(t, listed.Body.String(), `"user_agent":"Safari/17"`)
}
