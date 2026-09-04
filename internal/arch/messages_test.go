package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// smokeDir is the tree holding the process-level tests.
const smokeDir = "internal/smoke"

// logAssertionHelpers are the smoke harness functions that take a log message
// as their first argument.
//
// The list is short because the harness is: everything that reads the process's
// output goes through these two. If a third appears and is not added here, the
// check below goes silent for it — which [TestSmokeLogAssertionsAreNotBlind]
// catches by requiring the assertion count to stay positive, and which the
// helper count pins from the other side.
var logAssertionHelpers = []string{"logContains", "waitForLog"}

// TestSmokeLogAssertionsMatchProduction proves every log message a smoke test
// waits for is still WRITTEN somewhere in production.
//
// # The failure this closes
//
// A smoke test asserts a production log line by literal text. There is no
// compiler link between the two: renaming the message leaves the smoke test
// compiling, vetting and linting cleanly, and `go test ./...` does not run it
// either — the smoke tests sit behind a build tag. The break surfaces in CI,
// after the push, in the slowest job.
//
// That is not hypothetical. Translating the observability package's "izleme
// kuruldu" to "telemetry is set up" broke exactly this pair, and every local
// gate stayed green.
//
// # Why a text match and not a shared constant
//
// Exporting a log message as a constant would put an operator-facing string
// into the package's API surface, where a future change to it becomes a
// breaking change for importers — a much larger cost than the one being
// avoided. The check reads the source instead: the message must appear as a
// literal in some production file.
//
// # What it does NOT prove
//
// It does not prove the message is reachable, nor that the code path
// asserted upon actually runs. It proves only that the sentence still EXISTS.
// The smoke test itself proves the rest.
func TestSmokeLogAssertionsMatchProduction(t *testing.T) {
	t.Parallel()

	assertions := smokeLogAssertions(t)
	require.NotEmpty(t, assertions,
		"not one log assertion was found in %s.\n"+
			"Either the harness helpers were renamed — in which case logAssertionHelpers "+
			"must follow them — or the scan is broken; in both cases this check is BLIND.",
		smokeDir)

	sources := productionSources(t)
	require.NotEmpty(t, sources, "no production source was read")

	for _, assertion := range slices.Sorted(maps.Keys(assertions)) {
		if strings.Contains(sources, assertion) {
			continue
		}
		t.Errorf("%s: the smoke test waits for the log message %q, but no production "+
			"file writes it.\n"+
			"There is no compiler link between the two: with the message renamed, the smoke "+
			"test still compiles and `go test ./...` does not run it, so the break only "+
			"shows up in CI. Either restore the message or update the assertion.",
			assertions[assertion], assertion)
	}
}

// TestSmokeLogAssertionsAreNotBlind pins the floor under the check above.
//
// A scan that found nothing would pass silently, and the pass would look
// exactly like a clean repository.
func TestSmokeLogAssertionsAreNotBlind(t *testing.T) {
	t.Parallel()

	assertions := smokeLogAssertions(t)
	assert.GreaterOrEqual(t, len(assertions), len(logAssertionHelpers),
		"only %d log assertion(s) were found for %d harness helpers.\n"+
			"A helper that is listed but never seen means the scan no longer recognizes the "+
			"call shape, and every message it guards is unguarded.",
		len(assertions), len(logAssertionHelpers))

	planted := `package p

func f(s any) { s.(interface{ logContains(string) bool }).logContains("a planted message") }
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "planted.go", planted, parser.SkipObjectResolution)
	require.NoError(t, err)

	found := map[string]token.Position{}
	collectLogAssertions(fset, file, found)
	assert.Contains(t, found, "a planted message",
		"the scan missed a planted assertion; the call shape it looks for no longer matches "+
			"the one the harness uses")
}

// smokeLogAssertions maps every asserted log message to where it is asserted.
func smokeLogAssertions(t *testing.T) map[string]token.Position {
	t.Helper()

	dir := filepath.Join(repoRoot, smokeDir)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "%s could not be read", smokeDir)

	found := map[string]token.Position{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, filepath.Join(dir, entry.Name()), nil,
			parser.SkipObjectResolution)
		require.NoError(t, parseErr)

		collectLogAssertions(fset, file, found)
	}

	return found
}

// collectLogAssertions records the literal first argument of every call to a
// harness log helper.
//
// A non-literal argument is skipped on purpose: the smoke suite also waits for
// error CODES held in constants, and those are already bound to production by
// the compiler.
func collectLogAssertions(fset *token.FileSet, file *ast.File, into map[string]token.Position) {
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !slices.Contains(logAssertionHelpers, sel.Sel.Name) {
			return true
		}

		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}

		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		into[value] = fset.Position(lit.Pos())

		return true
	})
}

// productionSources returns the concatenated text of every production Go file
// outside the smoke tree.
func productionSources(t *testing.T) string {
	t.Helper()

	var b strings.Builder
	for _, root := range []string{"internal", "cmd", "plugins"} {
		for _, path := range productionFiles(t, filepath.Join(repoRoot, root)) {
			rel, err := filepath.Rel(repoRoot, path)
			require.NoError(t, err)
			if strings.HasPrefix(filepath.ToSlash(rel), smokeDir+"/") {
				continue
			}
			raw, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			b.Write(raw)
			b.WriteByte('\n')
		}
	}

	return b.String()
}
