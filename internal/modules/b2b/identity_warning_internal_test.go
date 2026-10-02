package b2b

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// TestTheWarningSaysWhatAnAbsentIdentityCosts is D219: with no identity
// bound, the handler is handed no identity and the operator is warned of what
// the installation does with a request naming a customer — refuses it by
// default (ADR 0125), takes it at its word only when
// STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM says so.
func TestTheWarningSaysWhatAnAbsentIdentityCosts(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		trust     bool
		says, not string
	}{
		"refused by default": {false, "the b2b storefront routes refuse every request", "the b2b storefront routes take the customer in the path at its word"},
		"trusted by setting": {true, "the b2b storefront routes take the customer in the path at its word", "the b2b storefront routes refuse every request"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var logged bytes.Buffer
			held, err := storefrontIdentity(container.New(nil), slog.New(slog.NewTextHandler(&logged, nil)),
				tc.trust).Identity(t.Context())

			require.NoError(t, err, "the handler decides what an absent identity means")
			assert.Nil(t, held)
			assert.Contains(t, logged.String(), "level=WARN")
			assert.Contains(t, logged.String(), tc.says)
			assert.NotContains(t, logged.String(), tc.not)
		})
	}
}

// TestAWrongIdentityIsThisModulesSetupFault: an identity registered under the
// wrong type is a wiring fault, and it names this module.
func TestAWrongIdentityIsThisModulesSetupFault(t *testing.T) {
	t.Parallel()

	c := container.New(nil)
	require.NoError(t, c.Provide(corehttp.IdentityName, "not an identity"))
	_, err := storefrontIdentity(c, nil, false).Identity(t.Context())
	require.Error(t, err)
	assert.Equal(t, codeSetupFailed, errors.CodeOf(err))
}
