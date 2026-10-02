package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/core/config"
)

// TestTheClientKeyTrustsTheInstallationsHops is ADR 0368's key: behind the
// hops the installation trusts it reads the forwarded address, and with none
// trusted it reads the connection's, as the installation's own limit does.
func TestTheClientKeyTrustsTheInstallationsHops(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/store/v1/auth/register", http.NoBody)
	req.RemoteAddr = "10.0.0.1:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")

	assert.Equal(t, "203.0.113.9", clientKey(config.Config{TrustedProxyHops: 1})(req),
		"one trusted hop: the address the proxy forwarded")
	assert.Equal(t, "10.0.0.1", clientKey(config.Config{})(req),
		"no trusted hop: the connection's address, whatever the header says")
}

// TestTheGuardStacksLimitKeysAClientByTheTrustedHops proves the installation's
// own limit keys by the same client key: behind one trusted hop two shoppers
// forwarded by the same proxy are two quotas, and one shopper is one.
func TestTheGuardStacksLimitKeysAClientByTheTrustedHops(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.RateLimitPerMinute = 1
	cfg.TrustedProxyHops = 1
	r := guardedRouter(t, cfg, &corehttp.DeferredAuthenticator{})

	from := func(shopper string) int {
		req := httptest.NewRequest(http.MethodGet, "/admin/v1/users", http.NoBody)
		req.RemoteAddr = "10.0.0.1:5555"
		req.Header.Set("X-Forwarded-For", shopper)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)

		return rec.Code
	}

	assert.NotEqual(t, http.StatusTooManyRequests, from("203.0.113.9"), "the first shopper's first request")
	assert.NotEqual(t, http.StatusTooManyRequests, from("203.0.113.10"),
		"a second shopper behind the same proxy has a quota of their own")
	assert.Equal(t, http.StatusTooManyRequests, from("203.0.113.9"),
		"the first shopper's quota of one is spent")
}
