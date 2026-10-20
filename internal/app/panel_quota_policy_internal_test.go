package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
)

// TestASpentSignInQuotaOnThePanelCarriesItsPolicy is ADR 0452 beside ADR
// 0155: the 429 the sign-in quota answers the panel's form is written at the
// panel's address, so it carries the panel's content policy like every other
// refusal there.
func TestASpentSignInQuotaOnThePanelCarriesItsPolicy(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.RateLimitPerMinute = 0
	cfg.AdminSignInLimit, cfg.AdminSignInWindow = 1, time.Hour
	declared, err := routeQuotaDeclarations(cfg, nil)
	require.NoError(t, err)
	guards, _, err := guardStack(cfg, &corehttp.DeferredAuthenticator{}, &adminui.Ring{}, nil, nil, discardLogger(),
		nil, nil, declared...)
	require.NoError(t, err)
	r := corehttp.NewRouter(corehttp.RouterOptions{Version: "test", Middlewares: guards})
	r.Post(adminui.LoginPath, func(http.ResponseWriter, *http.Request) {})

	var rec *httptest.ResponseRecorder
	for range 2 {
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, adminui.LoginPath, http.NoBody))
	}
	require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
	assert.NotEmpty(t, rec.Header().Get("Content-Security-Policy"))
	assert.NotEmpty(t, rec.Header().Get("X-Frame-Options"))
}
