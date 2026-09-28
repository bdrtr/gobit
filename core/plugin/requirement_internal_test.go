package plugin

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// ranged is a plugin naming the core releases it works with, and recording
// whether its Setup ran.
type ranged struct {
	name, requires string
	setUp          *bool
}

func (p ranged) Name() string         { return p.name }
func (p ranged) RequiresCore() string { return p.requires }
func (p ranged) Setup(context.Context, *Host) error {
	if p.setUp != nil {
		*p.setUp = true
	}
	return nil
}

// plain names no range.
type plain struct{ setUp *bool }

func (plain) Name() string { return "plain" }
func (p plain) Setup(context.Context, *Host) error {
	*p.setUp = true
	return nil
}

// installAt installs the plugins as if the binary were built with release, and
// returns the log and the error.
func installAt(t *testing.T, release string, plugins ...Plugin) (logged string, err error) {
	t.Helper()
	previous := coreVersion
	coreVersion = func() string { return release }
	t.Cleanup(func() { coreVersion = previous })

	var buffer bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buffer, nil))
	c := container.New(log)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })
	registry := NewRegistry(log)
	for _, p := range plugins {
		registry.Add(p)
	}
	err = registry.Install(context.Background(), NewHost(c, nil, nil, log, nil))
	return buffer.String(), err
}

// TestAPluginIsInstalledOnlyWithTheReleasesItNames is ADR 0224: a release
// inside the range installs, one outside refuses before any Setup runs, each
// bound kind decides its edge, and a plugin naming no range installs with any.
func TestAPluginIsInstalledOnlyWithTheReleasesItNames(t *testing.T) {
	for release, admitted := range map[string]bool{
		"v0.9.0": true, "v0.10.3": true, "v0.11.0": false, "v0.8.9": false, "v0.11.0-rc.1": true,
	} {
		ran, other := false, false
		_, err := installAt(t, release,
			plain{setUp: &other},
			ranged{name: "shipping-acme", requires: ">=v0.9.0 <v0.11.0", setUp: &ran})
		if admitted {
			require.NoError(t, err, release)
			assert.True(t, ran, release)
			continue
		}
		require.Error(t, err, release)
		assert.Equal(t, codeCoreUnsupported, coreerrors.CodeOf(err), release)
		assert.Contains(t, err.Error(), release)
		assert.False(t, ran || other, "%s: nothing is set up once a plugin is refused", release)
	}

	for requires, admitted := range map[string]bool{
		">v0.9.0": false, "<=0.9.0": true, "=0.9.0": true, "=v0.9.1": false, ">=0.9.0,<0.9.1": true,
	} {
		_, err := installAt(t, "v0.9.0", ranged{name: "edge", requires: requires})
		assert.Equal(t, admitted, err == nil, "%s at v0.9.0: %v", requires, err)
	}
}

// TestARangeThatCannotBeReadIsRefusedEvenUnchecked: an empty range, a bound
// with no comparison and a version that is not semantic refuse the plugin,
// whether or not the build says its release; an unstamped build installs a
// readable range and says it did not check it.
func TestARangeThatCannotBeReadIsRefusedEvenUnchecked(t *testing.T) {
	for _, requires := range []string{"", "  ", "v0.9.0", ">=nine", "~v0.9.0"} {
		for _, release := range []string{"v0.9.0", ""} {
			_, err := installAt(t, release, ranged{name: "broken", requires: requires})
			require.Error(t, err, "%q at %q", requires, release)
			assert.Equal(t, codeCoreRangeInvalid, coreerrors.CodeOf(err), "%q at %q", requires, release)
		}
	}

	ran := false
	logged, err := installAt(t, "", ranged{name: "shipping-acme", requires: ">=v9.0.0", setUp: &ran})
	require.NoError(t, err, "an unstamped build cannot say it is outside the range")
	assert.True(t, ran)
	assert.Contains(t, logged, "not checked")
	assert.Contains(t, logged, "shipping-acme")
}
