package adminui

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// wiringCatalog satisfies the panel's read surface.
type wiringCatalog struct{}

func (wiringCatalog) Graph(context.Context, query.GraphSpec) ([]query.Record, error) {
	return nil, nil
}

// wiringSession satisfies the panel's identity surface.
type wiringSession struct{}

func (wiringSession) Login(context.Context, string, string) (string, time.Time, error) {
	return "", time.Time{}, nil
}

func (wiringSession) Logout(context.Context, string, string) (time.Time, error) {
	return time.Time{}, nil
}

// wiringContainer holds everything the panel needs except the write surface.
func wiringContainer(t *testing.T) *container.Container {
	t.Helper()

	c := container.New(nil)
	require.NoError(t, c.Provide(ServiceQuery, wiringCatalog{}))
	require.NoError(t, c.Provide(ServiceAuth, wiringSession{}))
	require.NoError(t, c.Provide(InteropAuth, fakeAuthenticator{}))
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	return c
}

// TestPanelOpensWithoutTheWriteSurface proves an installation with no product
// module still gets a panel.
//
// The write surface is resolved OPTIONALLY on purpose. Treating it like the
// others would turn a removable module into a requirement of the panel itself,
// which is the coupling the fourth tree exists to avoid (ADR 0013).
func TestPanelOpensWithoutTheWriteSurface(t *testing.T) {
	t.Parallel()

	panel, err := FromContainer(wiringContainer(t), false)

	require.NoError(t, err, "the panel must open without the product module")
	require.NotNil(t, panel)
	assert.Nil(t, panel.products, "an absent write surface must stay absent, not be faked")
}

// TestAMismatchedWriteSurfaceStopsStartup proves the OTHER half of the optional
// resolution.
//
// A name that is REGISTERED but whose surface does not match is a wiring
// mistake, not a missing module. Degrading it silently would leave the panel
// answering "editing unavailable" while the module sat right there — and the
// operator would look for the module rather than for the signature.
func TestAMismatchedWriteSurfaceStopsStartup(t *testing.T) {
	t.Parallel()

	c := wiringContainer(t)
	// Registered under the right name, but it is not a ProductWriter.
	require.NoError(t, c.Provide(ServiceProductAdmin, wiringCatalog{}))

	_, err := FromContainer(c, false)

	require.Error(t, err, "a registered name with the wrong surface must stop startup")
	assert.Equal(t, CodeDependencyMissing, errors.CodeOf(err))
	assert.Contains(t, err.Error(), ServiceProductAdmin, "the error must name the surface")
}

// TestPanelTakesTheWriteSurfaceWhenItIsThere proves the happy path wires it.
func TestPanelTakesTheWriteSurfaceWhenItIsThere(t *testing.T) {
	t.Parallel()

	c := wiringContainer(t)
	require.NoError(t, c.Provide(ServiceProductAdmin, &fakeProductWriter{}))

	panel, err := FromContainer(c, false)

	require.NoError(t, err)
	assert.NotNil(t, panel.products, "a registered write surface must be wired")
}
