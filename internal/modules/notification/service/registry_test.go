package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// TestRegistrySecondRegistrationWithTheSameIDConflictsAndKeepsTheExisting
// verifies that overwriting silently is refused.
//
// In a setup where two plugins use the same identifier, overwriting would leave
// which provider runs up to the LOAD ORDER; in notification the price of that
// is order confirmations going out from the wrong account or not going out at
// all.
func TestRegistrySecondRegistrationWithTheSameIDConflictsAndKeepsTheExisting(t *testing.T) {
	registry := service.NewProviderRegistry()
	first := newFakeProvider("log")
	second := newFakeProvider("log")

	require.NoError(t, registry.Register(first))
	err := registry.Register(second)

	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "error: %v", err)
	assert.Equal(t, service.CodeProviderExists, errors.CodeOf(err))

	resolved, getErr := registry.Get("log")
	require.NoError(t, getErr)
	assert.Same(t, first, resolved, "the existing provider HAS TO BE KEPT")
}

// TestRegistryUnknownIDGivesADiagnosableError verifies that a provider being
// forgotten during registration (or the name being misspelled) gives a readable
// error (ADR 0002).
func TestRegistryUnknownIDGivesADiagnosableError(t *testing.T) {
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(newFakeProvider("log")))
	require.NoError(t, registry.Register(newFakeProvider("sendgrid")))

	_, err := registry.Get("mailgun")

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "error: %v", err)
	assert.Contains(t, err.Error(), "mailgun", "the identifier looked for has to be written")
	assert.Contains(t, err.Error(), "log", "the registered identifiers have to be written")
	assert.Contains(t, err.Error(), "sendgrid")
}

// TestRegistryInvalidRegistrationsAreRefused verifies that nil providers and
// providers without an identifier cannot be registered.
//
// Had a provider without an identifier been registrable, no configuration could
// ever select it but the registry would look "full".
func TestRegistryInvalidRegistrationsAreRefused(t *testing.T) {
	registry := service.NewProviderRegistry()

	require.Error(t, registry.Register(nil))

	err := registry.Register(newFakeProvider("   "))
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "error: %v", err)
	assert.Empty(t, registry.IDs())
}

// TestRegistryIDsAreReturnedSorted verifies that the identifiers come back in a
// stable order.
//
// Had the order come from the map, the error messages and the startup log would
// come out in a different order on every run and that would make diagnosis
// harder.
func TestRegistryIDsAreReturnedSorted(t *testing.T) {
	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(newFakeProvider("sendgrid")))
	require.NoError(t, registry.Register(newFakeProvider("log")))
	require.NoError(t, registry.Register(newFakeProvider("mailgun")))

	assert.Equal(t, []string{"log", "mailgun", "sendgrid"}, registry.IDs())
}
