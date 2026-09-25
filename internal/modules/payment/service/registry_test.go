package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// TestASecondRegistrationUnderOneIdentityConflictsAndKeepsTheFirst verifies that
// silent overwriting is refused.
//
// In an installation where two plugins use the same identity, overwriting would
// leave which provider runs to the LOAD ORDER; in payments the price of that is
// money going to an institution nobody expected.
func TestASecondRegistrationUnderOneIdentityConflictsAndKeepsTheFirst(t *testing.T) {
	registry := service.NewProviderRegistry()
	first := newFakeProvider("manual")
	second := newFakeProvider("manual")

	require.NoError(t, registry.Register(first))
	err := registry.Register(second)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindConflict), "error: %v", err)
	assert.Equal(t, service.CodeProviderExists, errors.CodeOf(err))

	resolved, getErr := registry.Get("manual")
	require.NoError(t, getErr)
	assert.Same(t, first, resolved, "the existing provider has to be KEPT")
}

// TestAnUnknownIdentityGivesADiagnosableError verifies that a provider somebody
// forgot to register produces a readable error (ADR 0002).
func TestAnUnknownIdentityGivesADiagnosableError(t *testing.T) {
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(newFakeProvider("manual")))
	require.NoError(t, registry.Register(newFakeProvider("stripe")))

	_, err := registry.Get("adyen")

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindNotFound), "error: %v", err)
	assert.Contains(t, err.Error(), "adyen", "the identity asked for has to be written")
	assert.Contains(t, err.Error(), "manual", "the registered identities have to be written")
	assert.Contains(t, err.Error(), "stripe")
}

// TestInvalidRegistrationsAreRefused verifies that a nil provider and one with no
// identity cannot be registered.
func TestInvalidRegistrationsAreRefused(t *testing.T) {
	registry := service.NewProviderRegistry()

	require.Error(t, registry.Register(nil))

	err := registry.Register(newFakeProvider("   "))
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "error: %v", err)

	_, err = registry.Get("")
	assert.True(t, errors.HasKind(err, errors.KindInvalid), "error: %v", err)
}

// TestTheIdentitiesComeBackSorted verifies that the identity list comes back in
// a FIXED order.
//
// A list produced by ranging over a map comes out in a different order on every
// call; both the API answer and the error message would be unpredictable.
func TestTheIdentitiesComeBackSorted(t *testing.T) {
	registry := service.NewProviderRegistry()
	for _, id := range []string{"stripe", "adyen", "manual"} {
		require.NoError(t, registry.Register(newFakeProvider(id)))
	}

	assert.Equal(t, []string{"adyen", "manual", "stripe"}, registry.IDs())
}

// TestTheIdentityIsTrimmed verifies that leading and trailing spaces cause no
// trouble in the lookup.
func TestTheIdentityIsTrimmed(t *testing.T) {
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(newFakeProvider("  manual  ")))

	_, err := registry.Get("manual")
	require.NoError(t, err)
	assert.Equal(t, []string{"manual"}, registry.IDs())
}
