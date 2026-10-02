//go:build integration

package b2b_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/internal/modules/b2b"
)

// TestTheWarningSaysWhatTheRoutesDo is D219 through the module the root
// registers: with no identity bound, the storefront's company read refuses
// by default and is served when the installation trusts an unverified claim
// (ADR 0125), and the WARN the first request logs says the same thing the
// route does.
func TestTheWarningSaysWhatTheRoutesDo(t *testing.T) {
	for name, tc := range map[string]struct {
		trust   bool
		refused bool
		says    string
	}{
		"refused by default": {false, true, "the b2b storefront routes refuse every request"},
		"trusted by setting": {true, false, "take the customer in the path at its word"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			c := container.New(nil)
			t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
			require.NoError(t, c.Provide("core.db", testPool))
			require.NoError(t, c.Provide("core.link", testLinks))

			var logged bytes.Buffer
			mod := b2b.New(slog.New(slog.NewTextHandler(&logged, nil)),
				b2b.Options{TrustUnverifiedCustomerClaim: tc.trust})
			require.NoError(t, mod.Register(ctx, c))
			r := chi.NewRouter()
			mod.Routes(r)

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet,
				"/store/v1/b2b/customers/cus_nobody/company", http.NoBody))

			if tc.refused {
				assert.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
			} else {
				assert.NotEqual(t, http.StatusUnauthorized, rec.Code,
					"a trusted claim is served, and this customer simply has no company: %s", rec.Body.String())
			}
			assert.Contains(t, logged.String(), tc.says)
		})
	}
}
