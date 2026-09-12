package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/scaffold"
)

// `gobit new`'s own decisions (ADR 0154).
//
// What a generated project DOES is proven in internal/arch, by building and
// running one. These are the decisions that can be wrong while the generated
// project still builds: the argument shape, the refusals, and the one thing this
// verb must not do.

// TestNewReadsNoConfiguration is the property that makes the verb usable at all.
//
// Every other subcommand calls config.Load, which is right: run inside the
// container they are already pointed at the right database. This one runs on a
// laptop where nothing exists yet, so touching the configuration would make the
// front door of the framework depend on the thing the front door sets up.
//
// The environment is emptied for the call. A DATABASE_URL is not set, so any
// config.Load on this path would fail and the write would never happen.
func TestNewReadsNoConfiguration(t *testing.T) {
	for _, name := range []string{"DATABASE_URL", "REDIS_URL", "JWT_SECRET", "APP_ENV"} {
		t.Setenv(name, "")
		require.NoError(t, os.Unsetenv(name))
	}

	dir := filepath.Join(t.TempDir(), "shop")
	var out bytes.Buffer

	err := runNew([]string{dir, "-" + flagReplace, "."}, &out, Options{})

	require.NoError(t, err,
		"the one verb that must work before anything is configured read the configuration")
	assert.Contains(t, out.String(), "wrote "+dir)
	for _, name := range scaffold.Written() {
		assert.Contains(t, out.String(), name,
			"the report must name what was written; a generator that said nothing leaves "+
				"the user to find out with ls")
	}
}

// TestNewRefusesWhenTheBuildCannotNameAVersion is the refusal that keeps a
// broken project from being written.
//
// A plain `go build ./...` injects none of the build facts, so the binary cannot
// say which library version a generated project should require. The measured
// alternatives are all worse: `@latest` and the newest tag resolve to a release
// that does not contain the published surface, so the project fails at `go mod
// tidy` — with nothing telling the user why.
func TestNewRefusesWhenTheBuildCannotNameAVersion(t *testing.T) {
	require.Empty(t, buildRelease,
		"this test runs under `go test`, which injects no build facts; if that changed "+
			"the refusal below is no longer the case being exercised")

	dir := filepath.Join(t.TempDir(), "shop")
	var out bytes.Buffer

	err := runNew([]string{dir}, &out, Options{})

	require.Error(t, err)
	assert.Equal(t, codeNewInvalidInput, coreerrors.CodeOf(err))
	assert.Contains(t, err.Error(), flagReplace,
		"the refusal must name the way out; an error that only says no leaves the user "+
			"with a command that never works")

	_, statErr := os.Stat(dir)
	assert.Error(t, statErr, "nothing may be written when the version cannot be named")
}

// TestNewNeedsTheDirectoryFirst pins the argument shape to the repository's.
//
// The flag package stops at the first non-flag argument, so a directory written
// after the flags is swallowed as a leftover — the same trap `recover` and
// `migrate down` already carry a refusal for.
func TestNewNeedsTheDirectoryFirst(t *testing.T) {
	for name, args := range map[string][]string{
		"no arguments":        {},
		"flag first":          {"-" + flagReplace, ".", "shop"},
		"argument after flag": {"shop", "-" + flagReplace, ".", "extra"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseNewFlags(args)

			require.Error(t, err)
			assert.Equal(t, codeNewInvalidInput, coreerrors.CodeOf(err))
		})
	}
}

// TestTheDefaultModulePathIsReserved keeps a generated project from colliding.
//
// example.com is reserved for documentation, so a project whose module path was
// never chosen cannot resolve to somebody's real module.
func TestTheDefaultModulePathIsReserved(t *testing.T) {
	parsed, err := parseNewFlags([]string{filepath.Join("some", "where", "my-shop")})
	require.NoError(t, err)

	assert.Equal(t, "example.com/my-shop", parsed.module,
		"the default module path is derived from the directory's LAST element and sits "+
			"under the reserved domain")

	chosen, err := parseNewFlags([]string{"shop", "-" + flagModule, "github.com/acme/shop"})
	require.NoError(t, err)
	assert.Equal(t, "github.com/acme/shop", chosen.module)
}

// TestTheReplacePathIsMadeAbsolute keeps the generated go.mod usable from
// anywhere.
//
// A relative replace is resolved against the go.mod's own directory, which is
// the GENERATED project rather than the shell the command ran in — so "." would
// point the project at itself.
func TestTheReplacePathIsMadeAbsolute(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shop")
	var out bytes.Buffer

	require.NoError(t, runNew([]string{dir, "-" + flagReplace, "."}, &out, Options{}))

	gomod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	require.NoError(t, err)

	line := ""
	for _, candidate := range strings.Split(string(gomod), "\n") {
		if strings.HasPrefix(candidate, "replace ") {
			line = candidate
		}
	}
	require.NotEmpty(t, line, "the generated go.mod carries no replace line")
	assert.True(t, strings.Contains(line, "=> "+string(filepath.Separator)),
		"the replace target must be ABSOLUTE; a relative one resolves against the "+
			"generated project's own directory and points it at itself: %s", line)
}
