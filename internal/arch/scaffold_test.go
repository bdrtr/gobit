package arch_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/scaffold"
)

// The generated project, compiled and RUN (ADR 0154).
//
// # Why this lives here and not beside the generator
//
// Because what it proves is a property of the PUBLISHED SURFACE, which is what
// this package audits: a project written by `gobit new` reaches gobit the way an
// outside author does — through the module path, the facade and nothing else. The
// generator's own decisions (which files, which version string, which refusals)
// are tested in internal/scaffold, where they need no toolchain.
//
// # What it does NOT prove, and this matters
//
// The generated go.mod is rewritten here to point at THIS CHECKOUT with a
// `replace`, exactly as the out-of-tree examples do. So the lane compiles the
// template against HEAD and cannot tell "the template works at the version it
// pins" from "the template works at the tip of this tree" — and on 2026-09-12
// those differ absolutely: the newest tag does not contain the facade at all.
// Closing that needs a release, not a test, and it is written down as a known
// limit rather than implied away.

// generatedModule is the module path the generated fixture uses.
//
// It is under example.com because that domain is reserved for documentation, so
// the fixture can never resolve to a real module by accident.
const generatedModule = "example.com/gobit-generated-fixture"

// TestAGeneratedProjectCompilesAndRuns is the slice's end-to-end proof.
//
// A generator can be green on every unit test and still write a project that
// does not build: a template referencing a field that no longer exists, a go
// directive older than the library's, an import path that moved. The only thing
// that answers it is building what it wrote.
//
// The program is then RUN, for the reason TestTheOutOfTreeStarterRuns is: a
// program can compile against a facade that dispatches to nothing. The operator
// surface needs no database, no Redis and no port, and it comes from the
// composition root rather than the facade — so seeing it means the generated
// main really reached the lifecycle.
func TestAGeneratedProjectCompilesAndRuns(t *testing.T) {
	t.Parallel()

	goTool := goToolPath(t)
	root, err := filepath.Abs(repoRoot)
	require.NoError(t, err)

	dir := filepath.Join(t.TempDir(), "shop")
	require.NoError(t, scaffold.Write(scaffold.Input{
		Dir:       dir,
		Module:    generatedModule,
		Replace:   root,
		GoVersion: goDirectiveOfThisModule(t, root),
	}), "the generator must be able to write a project against this checkout")

	for _, name := range scaffold.Written() {
		_, statErr := os.Stat(filepath.Join(dir, name))
		require.NoError(t, statErr, "%s was not written", name)
	}

	// go.sum is copied rather than produced: `go mod tidy` would need the
	// network, and this lane runs in CI's Test job beside the offline ones. The
	// replace makes every gobit dependency resolvable from this checkout's own
	// sums.
	sum, err := os.ReadFile(filepath.Join(root, "go.sum"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.sum"), sum, 0o600))

	// `go mod tidy` is run because that is what a user does: the generated go.mod
	// names the library and nothing else, so the indirect requirements are
	// resolved once, on the user's machine. It runs OFFLINE here — the replace
	// above makes every gobit dependency resolvable from this checkout's module
	// cache — so this lane stays beside the other offline ones.
	tidy := exec.CommandContext(t.Context(), goTool, "mod", "tidy")
	tidy.Dir = dir
	tidy.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off")
	tidyOut, tidyErr := tidy.CombinedOutput()
	require.NoError(t, tidyErr,
		"the go.mod `gobit new` writes cannot be tidied:\n%s\n"+
			"The generated project's first step is this command, so a failure here is the "+
			"first thing a new user would see.", tidyOut)

	build := exec.CommandContext(t.Context(), goTool, "build", "-o", "app", ".")
	build.Dir = dir
	build.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off")
	output, buildErr := build.CombinedOutput()
	require.NoError(t, buildErr,
		"the project `gobit new` writes does not compile against this tree:\n%s\n"+
			"Fix the template or the surface it uses. This is the failure the command "+
			"exists to prevent — a generator whose output does not build hands a new user "+
			"a broken project as their first impression.", output)

	run := exec.CommandContext(t.Context(), filepath.Join(dir, "app"), "help")
	run.Dir = dir
	helpOut, runErr := run.CombinedOutput()
	require.NoError(t, runErr, "the generated binary could not be run:\n%s", helpOut)

	for _, verb := range []string{"migrate status", "stuck", "recover", "jobs", "new"} {
		assert.Contains(t, string(helpOut), verb,
			"the generated program ran but its operator surface does not mention %q.\n"+
				"That surface comes from the composition root, so its absence means the "+
				"generated main links and does nothing.", verb)
	}
}

// TestTheGeneratedEnvNamesOnlySettingsGobitReads keeps the template honest.
//
// A setting written into a generated .env that gobit does not read is worse than
// a missing one: the user fills it in, believes it took effect, and nothing says
// otherwise. The population is derived — every KEY=... line the template writes
// is looked up in .env.example, which is this repository's own written record of
// what settings exist.
func TestTheGeneratedEnvNamesOnlySettingsGobitReads(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(repoRoot)
	require.NoError(t, err)

	dir := filepath.Join(t.TempDir(), "shop")
	require.NoError(t, scaffold.Write(scaffold.Input{
		Dir: dir, Module: generatedModule, Replace: root, GoVersion: "1.26.6",
	}))

	generated, err := os.ReadFile(filepath.Join(dir, ".env"))
	require.NoError(t, err)
	reference, err := os.ReadFile(filepath.Join(root, ".env.example"))
	require.NoError(t, err)

	keyLine := regexp.MustCompile(`(?m)^([A-Z][A-Z0-9_]*)=`)
	matches := keyLine.FindAllStringSubmatch(string(generated), -1)
	require.NotEmpty(t, matches,
		"no setting was found in the generated .env, so this check would be BLIND")

	for _, match := range matches {
		key := match[1]
		assert.True(t,
			strings.Contains(string(reference), "\n"+key+"=") ||
				strings.Contains(string(reference), "\n# "+key+"="),
			"the generated .env sets %q and %s does not mention it.\n"+
				"Either gobit does not read that setting — in which case the generated "+
				"project promises something that does nothing — or the record of what "+
				"settings exist has fallen behind.", key, ".env.example")
	}
}

// TestTheGeneratedGoDirectiveMatchesThisModule keeps the versions in step.
//
// A project declaring an older language version than the library it compiles
// against fails in a way that reads as the library's fault.
//
// # The subject is the COMMAND's constant, not the template
//
// The first version of this check generated a project passing the root go.mod's
// own directive in, and then asserted the generated go.mod carried it — which
// proves the template writes what it is GIVEN and says nothing about what the
// command gives it. Measured: with `goDirective` set to "1.21" that check stayed
// green. So the subject here is the constant in the composition root, read from
// the source, compared with the module's own `go` line.
func TestTheGeneratedGoDirectiveMatchesThisModule(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs(repoRoot)
	require.NoError(t, err)

	assert.Equal(t, goDirectiveOfThisModule(t, root), commandGoDirective(t, root),
		"the `go` directive `gobit new` writes has fallen behind this module's own.\n"+
			"A generated project declaring an older language version than the library it "+
			"compiles against fails in a way that reads as the library's fault.")
}

// commandGoDirective reads the composition root's goDirective constant.
//
// It is read from the SOURCE because the constant is unexported and this package
// may not import internal/app's internals as values — and because what is being
// audited is the decision as written, not a copy of it.
func commandGoDirective(t *testing.T, root string) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(root, "internal", "app", "new.go"))
	require.NoError(t, err)

	match := regexp.MustCompile(`(?m)^const goDirective = "([0-9.]+)"$`).
		FindStringSubmatch(string(raw))
	require.Len(t, match, 2,
		"the composition root's goDirective constant could not be read; this check would "+
			"be BLIND, and a generated project's language version would be audited by "+
			"nothing")

	return match[1]
}

// goDirectiveOfThisModule reads the `go` line out of the repository's go.mod.
func goDirectiveOfThisModule(t *testing.T, root string) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(root, goModFileName))
	require.NoError(t, err)

	directive := regexp.MustCompile(`(?m)^go ([0-9.]+)$`).FindStringSubmatch(string(raw))
	require.Len(t, directive, 2,
		"this module's go.mod has no `go` directive this check can read")

	return directive[1]
}
