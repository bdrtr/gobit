package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheDescribeLoopsAgree keeps the e2e harness's schema wiring from drifting
// away from the composition root's.
//
// # The defect it was written for, and it had already happened
//
// The root walks the module list, asks each module that implements
// [github.com/bdrtr/gobit/core/openapi.Describer] to describe itself, and wraps
// each call in ForModule so the module's name becomes its components' namespace
// (ADR 0036). internal/e2e has its OWN copy of that loop, because the harness
// deliberately mirrors the root rather than importing it — the same decision
// TestEveryRegisteredModuleIsSetUpInTheE2EHarness audits for the module set.
//
// On 2026-09-07 the namespace was added to the root and not to the copy. Nothing
// failed to compile. What happened instead is that the harness's document
// stopped building AT ALL: without the namespace, customer/api and cart/api both
// wanted the component name "Address", and a name clash makes the whole document
// unbuildable rather than one endpoint poorer. The failure was loud, but it was
// loud in a test whose message was "the schema endpoint must return 200", four
// steps from the cause.
//
// # Why a text scan of two files
//
// What is being checked is that two loops make the same CALLS, and the calls are
// what a reader compares when they change one of them. An AST walk is used for
// the file structure rather than the call shape because the fragile part is
// finding the loop at all, not matching an expression tree; the blindness guard
// below is what stops this test passing over a file it no longer understands.
//
// # What it does NOT check
//
// That the two loops are IDENTICAL. They are not and should not be: the root
// has a document title from configuration and the harness has a literal. What
// has to agree is the pair of calls that decide what enters the document — the
// Describer assertion and the namespace — because those are what a change to one
// side silently leaves out of the other.
func TestTheDescribeLoopsAgree(t *testing.T) {
	t.Parallel()

	required := []string{"openapi.Describer", "ForModule", "Describe(doc)"}

	for _, tree := range []string{compositionRoot, e2eHarness} {
		body, found := describeLoopSource(t, tree)
		require.True(t, found,
			"no describe loop was found under %s/.\n"+
				"The audit has gone BLIND: it looks for a function whose body asserts "+
				"openapi.Describer over a module list, and that shape has changed. Fix the "+
				"finder rather than deleting the test — a blind audit over two copies of "+
				"one loop is worse than none.", tree)

		for _, call := range required {
			assert.Contains(t, body, call,
				"the describe loop in %s/ does not call %q.\n"+
					"This loop exists twice — once in %s and once in %s — because the e2e "+
					"harness mirrors the composition root rather than importing it. A call "+
					"added to one copy and not the other does not fail to compile; it made "+
					"the harness's whole OpenAPI document unbuildable the last time it "+
					"happened, with an error four steps from the cause.",
				tree, call, compositionRoot, e2eHarness)
		}
	}
}

// describeLoopSource returns the source of the function that walks the modules
// asking each to describe itself.
//
// It looks for the Describer type assertion rather than for a function name: the
// two copies are called describeAPI and describeDocument, and pinning either
// name would make this audit fail on a rename instead of on a drift.
func describeLoopSource(t *testing.T, tree string) (body string, found bool) {
	t.Helper()

	dir := filepath.Join(repoRoot, tree)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "%s could not be read", tree)

	fset := token.NewFileSet()

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}

		path := filepath.Join(dir, entry.Name())

		source, err := os.ReadFile(path)
		require.NoError(t, err, "%s could not be read", path)

		file, err := parser.ParseFile(fset, path, source, parser.SkipObjectResolution)
		if err != nil {
			// A file that does not parse under the default build tags is not this
			// audit's business; the e2e harness is behind a build tag and still
			// parses, which is what matters here.
			continue
		}

		for _, decl := range file.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || fn.Body == nil {
				continue
			}

			text := string(source[fset.Position(fn.Body.Pos()).Offset:fset.Position(fn.Body.End()).Offset])
			if strings.Contains(text, "openapi.Describer") && strings.Contains(text, "range modules") {
				return text, true
			}
		}
	}

	return "", false
}
