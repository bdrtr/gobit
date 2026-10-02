package payment

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// TestTheStorefrontIdentityRefusesWhenNothingIsBound is this module's absence
// (ADR 0370): with no identity bound a request naming a customer is refused and
// the operator is warned in the module's words, and an identity registered
// under the wrong type is this module's own setup fault.
func TestTheStorefrontIdentityRefusesWhenNothingIsBound(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodGet, "/store/v1", http.NoBody)
	var logged bytes.Buffer
	_, err := storefrontIdentity(container.New(nil), slog.New(slog.NewTextHandler(&logged, nil))).CustomerID(request)
	require.Error(t, err)
	assert.Equal(t, corehttp.CodeIdentityNotBound, errors.CodeOf(err))
	assert.Contains(t, logged.String(), "the storefront balance routes will refuse")

	c := container.New(nil)
	require.NoError(t, c.Provide(corehttp.IdentityName, "not an identity"))
	_, err = storefrontIdentity(c, nil).CustomerID(request)
	require.Error(t, err)
	assert.Equal(t, codeSetupFailed, errors.CodeOf(err), "the fault names this module")
}
