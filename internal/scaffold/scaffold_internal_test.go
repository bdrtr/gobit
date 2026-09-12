package scaffold

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The generator's own decisions (ADR 0154).
//
// What a real project compiling against a real gobit proves is in
// internal/arch; these are the decisions that can be wrong while the generated
// project still builds — the version it requires, the names it writes, and the
// refusals.

// TestWriteProducesEveryListedFile is the shortest form of the claim.
func TestWriteProducesEveryListedFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shop")

	require.NoError(t, Write(Input{
		Dir: dir, Module: "example.com/shop",
		GobitVersion: "v0.9.0", GoVersion: "1.26.6",
	}))

	for _, name := range Written() {
		_, err := os.Stat(filepath.Join(dir, name))
		assert.NoError(t, err, "%s was listed but not written", name)
	}

	gomod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	require.NoError(t, err)
	assert.Contains(t, string(gomod), "module example.com/shop")
	assert.Contains(t, string(gomod), "require "+GobitModule+" v0.9.0",
		"the generated project must REQUIRE the version it was told")
	assert.NotContains(t, string(gomod), "replace",
		"a project generated against a version must not carry a replace line: it would "+
			"point at a path that exists on one machine")
	assert.NotContains(t, string(gomod), "<no value>",
		"a template field that lost its value must not reach a generated file")
}

// TestTheTemplatesCarryNoGoModFile is the trap that cannot be caught later.
//
// `//go:embed` silently EXCLUDES a directory containing a file named "go.mod" —
// the whole directory — and an `all:` prefix does not lift it. A template tree
// with one in it produces a binary that compiles, embeds part of the tree and
// generates an incomplete project with nothing saying so. Measured, not reasoned
// about.
func TestTheTemplatesCarryNoGoModFile(t *testing.T) {
	for name := range files {
		assert.NotEqual(t, "go.mod", name,
			"a template named go.mod would remove its whole directory from the embedded "+
				"set, silently")
		assert.NotEqual(t, ".go", filepath.Ext(name),
			"a template ending in .go is parsed by two repository gates that walk every "+
				"non-test Go file, and by `go build ./...`")
	}
}

// TestAReplaceProjectSaysSoInBothPlaces keeps the warning attached to the fault.
//
// A project carrying a replace line is not portable, and the person who will
// find that out is not the one who generated it. Both the go.mod and the README
// say it.
func TestAReplaceProjectSaysSoInBothPlaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shop")

	require.NoError(t, Write(Input{
		Dir: dir, Module: "example.com/shop",
		Replace: "/somewhere/gobit", GoVersion: "1.26.6",
	}))

	gomod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	require.NoError(t, err)
	assert.Contains(t, string(gomod), "replace "+GobitModule+" => /somewhere/gobit")

	readme, err := os.ReadFile(filepath.Join(dir, "README.md"))
	require.NoError(t, err)
	assert.Contains(t, readme2string(readme), "replace",
		"the README must warn about the replace line: the go.mod comment is read by "+
			"whoever opens the go.mod, which is not who hits the problem")
}

// readme2string is a named helper so the assertion above reads as prose.
func readme2string(raw []byte) string { return string(raw) }

// TestWriteRefusesAnExistingDirectory keeps somebody's go.mod safe.
func TestWriteRefusesAnExistingDirectory(t *testing.T) {
	dir := t.TempDir()

	err := Write(Input{
		Dir: dir, Module: "example.com/shop",
		GobitVersion: "v0.9.0", GoVersion: "1.26.6",
	})

	require.Error(t, err,
		"writing into an existing directory would overwrite files that are a project's "+
			"own identity")
}

// TestWriteRefusesWhenNothingSaysWhichLibrary is the refusal that keeps a
// broken project from being written.
func TestWriteRefusesWhenNothingSaysWhichLibrary(t *testing.T) {
	err := Write(Input{
		Dir: filepath.Join(t.TempDir(), "shop"), Module: "example.com/shop",
		GoVersion: "1.26.6",
	})

	require.Error(t, err,
		"with neither a version nor a checkout the generated go.mod would require "+
			"nothing and the project could not build")
}

// TestThePseudoVersionMatchesWhatTheProxyServes is the version decision.
//
// The two forms are not interchangeable. With a tag reachable, the proxy serves
// v<major>.<minor>.<patch+1>-0.<time>-<hash>; with none, v0.0.0-<time>-<hash>. A
// binary writing the wrong one names a version the proxy does not serve, and
// `go mod tidy` in the generated project rewrites it — silently, to something
// the generator never chose.
//
// The expected string is the one measured against the real proxy on 2026-09-12
// for this repository's own HEAD.
func TestThePseudoVersionMatchesWhatTheProxyServes(t *testing.T) {
	when := time.Date(2026, time.September, 12, 9, 45, 0, 0, time.UTC)

	assert.Equal(t, "v0.8.1-0.20260912094500-9cddc77b3bc7",
		Build{BaseTag: "v0.8.0", Commit: "9cddc77b3bc7aaaaaaaa", CommitTime: when}.Version(),
		"with a tag reachable the patch is bumped and the suffix is -0.")
	assert.Equal(t, "v0.0.0-20260912094500-9cddc77b3bc7",
		Build{Commit: "9cddc77b3bc7aaaaaaaa", CommitTime: when}.Version(),
		"with no tag reachable the base is v0.0.0 and there is no -0.")
	assert.Equal(t, "v1.2.3",
		Build{Release: "v1.2.3", BaseTag: "v1.2.2", Commit: "abc", CommitTime: when}.Version(),
		"a binary built FROM a tag requires that tag, not a pseudo-version of it")
	assert.Empty(t, Build{}.Version(),
		"a build that knows nothing must say so rather than invent a version")
	assert.Empty(t, Build{Commit: "abc"}.Version(),
		"a commit with no time cannot be turned into a pseudo-version")
}

// TestAPrereleaseBaseTagIsNotBumped keeps the generator from guessing.
//
// The pseudo-version of a commit after "v1.0.0-rc1" is not derived by bumping a
// patch, so a tag this function cannot read is treated as absent rather than
// approximated.
func TestAPrereleaseBaseTagIsNotBumped(t *testing.T) {
	when := time.Date(2026, time.September, 12, 9, 45, 0, 0, time.UTC)

	for _, tag := range []string{"v1.0.0-rc1", "1.0.0", "v1.0", "vx.y.z", ""} {
		version := Build{BaseTag: tag, Commit: "abcdefabcdef", CommitTime: when}.Version()
		assert.Equal(t, "v0.0.0-20260912094500-abcdefabcdef", version,
			"%q is not a plain vX.Y.Z and must not be bumped", tag)
	}
}
