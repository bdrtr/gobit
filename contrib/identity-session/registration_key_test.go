package identitysession_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	identitysession "github.com/bdrtr/gobit/contrib/identity-session"
	"github.com/bdrtr/gobit/core/container"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// shopperHeader names the client in these tests, as a trusted proxy's
// forwarded address would.
const shopperHeader = "X-Shopper"

// byShopper is a client key reading shopperHeader.
func byShopper(r *http.Request) string { return r.Header.Get(shopperHeader) }

// limitedRouter is a module allowing one registration per client per hour,
// registered in the container given, its options adjusted by tune.
func limitedRouter(t *testing.T, c *container.Container, tune func(*identitysession.Options)) chi.Router {
	t.Helper()

	opts := identitysession.Options{
		Secret:       []byte("a registration key test signing secret!!"),
		Insecure:     true,
		Credentials:  newMemoryRegistrations(),
		Accounts:     &fakeAccounts{byEmail: map[string]string{}},
		Verification: &fakeSender{},
		Limiter:      corehttp.NewMemoryLimiter(1, time.Hour),
	}
	if tune != nil {
		tune(&opts)
	}
	m := identitysession.New(opts)
	require.NoError(t, m.Register(t.Context(), c))
	r := chi.NewRouter()
	m.Routes(r)

	return r
}

// registerAs sends one registration as the given shopper and answers its status.
func registerAs(t *testing.T, r chi.Router, shopper string, n int) int {
	t.Helper()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/store/v1/auth/register",
		strings.NewReader(fmt.Sprintf(`{"email":"shopper%d@example.test","password":"a fine password"}`, n)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(shopperHeader, shopper)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec.Code
}

// TestRegistrationsAreLimitedPerClientAsTheInstallationKeysThem is ADR 0368:
// the registration limit keys a client with the installation's own client
// key, so two shoppers behind one proxy each get a quota; without one in the
// container every request is the connection's, and an option set by the
// embedder wins over the container's.
func TestRegistrationsAreLimitedPerClientAsTheInstallationKeysThem(t *testing.T) {
	t.Parallel()

	keyed := container.New(nil)
	require.NoError(t, keyed.Provide(corehttp.ClientKeyName, corehttp.KeyFunc(byShopper)))
	r := limitedRouter(t, keyed, nil)
	assert.Equal(t, http.StatusAccepted, registerAs(t, r, "ada", 1))
	assert.Equal(t, http.StatusAccepted, registerAs(t, r, "can", 2), "another shopper has a quota of their own")
	assert.Equal(t, http.StatusTooManyRequests, registerAs(t, r, "ada", 3), "the same shopper is limited")

	unkeyed := limitedRouter(t, container.New(nil), nil)
	assert.Equal(t, http.StatusAccepted, registerAs(t, unkeyed, "ada", 1))
	assert.Equal(t, http.StatusTooManyRequests, registerAs(t, unkeyed, "can", 2),
		"with no key in the container every request is the connection's")

	chosen := container.New(nil)
	require.NoError(t, chosen.Provide(corehttp.ClientKeyName, corehttp.KeyFunc(byShopper)))
	overridden := limitedRouter(t, chosen, func(o *identitysession.Options) {
		o.LimitKey = func(*http.Request) string { return "everybody" }
	})
	assert.Equal(t, http.StatusAccepted, registerAs(t, overridden, "ada", 1))
	assert.Equal(t, http.StatusTooManyRequests, registerAs(t, overridden, "can", 2), "the embedder's key wins")
}
