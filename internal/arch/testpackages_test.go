package arch_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A test package must not reach the server binary.
//
// The rule existed before this gate and lived in ONE godoc: the GraphQL
// handler's `responseCapture` says httptest "belongs to the test binary and
// using it in production code would carry test helpers into the server binary".
// That sentence is the reason nine lines were written by hand instead of taken
// from the standard library — and nothing enforced it. Measured by mutation on
// 2026-09-12: an httptest import added to a file in the PUBLISHED tree passed
// lint and every arch gate.

// testOnlyImports are the standard-library packages that exist to serve a test
// binary, with what each one drags in.
//
// The list is short on purpose. What earns a place is not "used mainly in
// tests" — it is a package whose own documentation says it is for testing, so
// that importing it from production code is a mistake rather than a style.
var testOnlyImports = map[string]string{
	"net/http/httptest": "its recorder and its server are test helpers; a handler " +
		"that needs to capture a response writes the three methods, which is what " +
		"the GraphQL handler already does",
	"testing": "the test framework itself, and importing it registers flags on the " +
		"binary's command line",
	"testing/fstest": "an in-memory filesystem for tests; production code that needs " +
		"one has a real directory or an embed",
	"testing/iotest":    "readers that fail on purpose",
	"net/http/httputil": "",
}

// TestNoProductionFileImportsATestPackage is the gate.
func TestNoProductionFileImportsATestPackage(t *testing.T) {
	t.Parallel()

	// httputil is NOT a test package — it is in the map above only to be removed
	// here, which is how this test proves its own list is read rather than
	// assumed. A gate whose population came from a map nobody checks would pass
	// with the map empty.
	delete(testOnlyImports, "net/http/httputil")
	require.Len(t, testOnlyImports, 4, "the test-package list was not read")

	reachedFromProduction := importedByProduction(t)
	exempted := map[string]bool{}

	files := productionFiles(t, repoRoot)
	require.GreaterOrEqual(t, len(files), 200,
		"only %d production files were walked; the walk has gone blind and a blind "+
			"walk imports nothing", len(files))

	for _, path := range files {
		body, err := os.ReadFile(path)
		require.NoError(t, err)

		for _, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			for pkg, why := range testOnlyImports {
				if !strings.Contains(trimmed, `"`+pkg+`"`) {
					continue
				}
				// A line naming the package inside a longer path is not an import
				// of it; the quotes are what make the match an import path
				// rather than a prefix of one.
				if !strings.HasPrefix(trimmed, `"`) && !strings.HasPrefix(trimmed, "_ ") &&
					!strings.HasPrefix(trimmed, "// ") && !strings.Contains(trimmed, `"`+pkg+`"`) {
					continue
				}
				if strings.HasPrefix(trimmed, "//") {
					continue
				}

				if onlyTestsImport(t, path, reachedFromProduction) {
					exempted[path] = true

					// A package nothing in production imports is test support,
					// whatever it is called. core/identitytest is the published
					// one — a conformance suite an embedder runs against its own
					// verifier — and internal/benchbudget is the local one. Both
					// would be on a hand-written exemption list; deriving it
					// instead means the next such package needs no edit here, and
					// a package that STOPS being test-only is caught the day
					// production first imports it.
					continue
				}

				assert.Failf(t, "a production file imports a test package",
					"%s imports %q.\n%s\n\nThe package would travel into the server "+
						"binary, and with it the flags and helpers a test binary "+
						"registers. The rule used to live in one godoc; this is the gate "+
						"that makes it a rule.", path, pkg, why)
			}
		}
	}

	// The exemption is load-bearing, so its SIZE is checked in both directions.
	// Broken so that every package looked like test support, the derivation would
	// swallow a real violation and this gate would pass while auditing nothing —
	// which a mutation proved it does. Two files take it today: the published
	// conformance suite and the benchmark budget.
	assert.Len(t, exempted, 2,
		"%d files took the test-support exemption and two were expected (%v).\n"+
			"More means the derivation has widened and a real import is being "+
			"swallowed; fewer means a test-support package started being reached from "+
			"production, and what it carries now travels into the server binary.",
		len(exempted), exempted)
}

// importedByProduction is every in-repository package path a NON-test file
// imports.
//
// It is the population the exemption is derived from: a package outside this set
// is reached only by tests, and what it carries never enters the server binary.
func importedByProduction(t *testing.T) map[string]bool {
	t.Helper()

	out := map[string]bool{}
	for _, path := range productionFiles(t, repoRoot) {
		body, err := os.ReadFile(path)
		require.NoError(t, err)

		for _, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.Contains(trimmed, `"`+modulePath+`/`) || strings.HasPrefix(trimmed, "//") {
				continue
			}
			start := strings.Index(trimmed, `"`+modulePath+`/`)
			rest := trimmed[start+1:]
			if end := strings.Index(rest, `"`); end > 0 {
				out[rest[:end]] = true
			}
		}
	}

	return out
}

// onlyTestsImport reports whether the file's own package is reached from
// production.
func onlyTestsImport(t *testing.T, path string, reached map[string]bool) bool {
	t.Helper()

	dir := filepath.Dir(path)
	rel, err := filepath.Rel(repoRoot, dir)
	require.NoError(t, err)

	return !reached[modulePath+"/"+filepath.ToSlash(rel)]
}
