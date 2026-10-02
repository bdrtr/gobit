package cart

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
)

// TestTheWarningSaysWhatAnAbsentIdentityCosts is D219: with no identity
// bound, the operator is warned of what the installation does with a
// request naming a customer — refuses it by default (ADR 0125), takes it at
// its word only when STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM says so.
func TestTheWarningSaysWhatAnAbsentIdentityCosts(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		trust     bool
		says, not string
	}{
		"refused by default": {false, "a cart body naming a customer is refused", "a cart body naming a customer is taken at its word"},
		"trusted by setting": {true, "a cart body naming a customer is taken at its word", "a cart body naming a customer is refused"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var logged bytes.Buffer
			binding := &identityBinding{
				c: container.New(nil), log: slog.New(slog.NewTextHandler(&logged, nil)), trustUnverified: tc.trust,
			}

			identity, err := binding.identity(t.Context())

			require.NoError(t, err)
			assert.Nil(t, identity)
			assert.Contains(t, logged.String(), "level=WARN")
			assert.Contains(t, logged.String(), tc.says)
			assert.NotContains(t, logged.String(), tc.not)
		})
	}
}
