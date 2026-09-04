package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// TestProviderRegistrationDoesNotOverwrite proves that the same identifier
// cannot be registered a second time.
//
// Overwriting silently would leave it to the load order which provider runs in a
// setup where two plugins use the same identifier; in shipping the price of that
// is the parcel being handed to an unexpected carrier.
func TestProviderRegistrationDoesNotOverwrite(t *testing.T) {
	t.Parallel()

	registry := service.NewProviderRegistry()
	first := newFakeProvider("manual")
	require.NoError(t, registry.Register(first))

	second := newFakeProvider("manual")
	err := registry.Register(second)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "the error has to be errors.Conflict: %v", err)
	assert.Equal(t, service.CodeProviderExists, errors.CodeOf(err))

	resolved, err := registry.Get("manual")
	require.NoError(t, err)
	assert.Same(t, first, resolved, "the existing provider has to be kept")
}

// TestProviderRegistrationRejectsAnEmptyID proves that a provider without an
// identifier is not registered.
func TestProviderRegistrationRejectsAnEmptyID(t *testing.T) {
	t.Parallel()

	registry := service.NewProviderRegistry()

	require.Error(t, registry.Register(nil))
	err := registry.Register(newFakeProvider("   "))
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the error has to be errors.Invalid: %v", err)
}

// TestProviderNotFoundListsTheRegisteredIDs proves that the error message is
// diagnosable (ADR 0002).
func TestProviderNotFoundListsTheRegisteredIDs(t *testing.T) {
	t.Parallel()

	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(newFakeProvider("manual")))
	require.NoError(t, registry.Register(newFakeProvider("aras")))

	_, err := registry.Get("yurtici")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "the error has to be errors.NotFound: %v", err)
	assert.Contains(t, err.Error(), "yurtici", "the identifier looked for has to be written")
	assert.Contains(t, err.Error(), "manual", "the registered identifiers have to be written")
	assert.Contains(t, err.Error(), "aras")
}

// TestProviderIDsAreSorted proves that the order is fixed.
//
// Had it been produced by ranging over the map, it would come out in a different
// order on every call, making diagnosis and testing harder.
func TestProviderIDsAreSorted(t *testing.T) {
	t.Parallel()

	registry := service.NewProviderRegistry()
	for _, id := range []string{"ptt", "aras", "manual"} {
		require.NoError(t, registry.Register(newFakeProvider(id)))
	}
	assert.Equal(t, []string{"aras", "manual", "ptt"}, registry.IDs())
}

// TestProviderRegistryHasReportsRegistration proves that Has returns the
// registration state correctly.
func TestProviderRegistryHasReportsRegistration(t *testing.T) {
	t.Parallel()

	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(newFakeProvider("manual")))

	assert.True(t, registry.Has("manual"))
	assert.True(t, registry.Has(" manual "), "whitespace has to be trimmed")
	assert.False(t, registry.Has("aras"))
	assert.False(t, registry.Has(""))
}

// TestServiceCannotBeBuiltWithAMissingDependency proves that the construction
// error is returned explicitly.
//
// A service built with a nil store would panic on the first request and the
// error would surface long after construction.
func TestServiceCannotBeBuiltWithAMissingDependency(t *testing.T) {
	t.Parallel()

	_, err := service.New(service.Options{Providers: service.NewProviderRegistry()})
	require.Error(t, err)
	assert.Equal(t, service.CodeNotReady, errors.CodeOf(err))

	_, err = service.New(service.Options{Store: newFakeStore()})
	require.Error(t, err)
	assert.Equal(t, service.CodeNotReady, errors.CodeOf(err))
}

// TestProviderIDsAreReadFromTheService proves that the service reflects the
// registered providers.
func TestProviderIDsAreReadFromTheService(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	assert.Equal(t, []string{"fake"}, setup.svc.ProviderIDs(t.Context()))
}
