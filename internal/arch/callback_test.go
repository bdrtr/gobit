package arch_test

import (
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/adminui"
)

// guardedPrefixes are the path prefixes the guard stack covers.
//
// They are repeated here rather than imported because the composition root
// keeps them unexported, and [TestTheGuardedPrefixesStillExist] checks the
// repetition against that file — a prefix renamed there without being renamed
// here would otherwise leave this audit approving everything under the old one.
var guardedPrefixes = append(append([]string{}, copiedPrefixes...), adminui.URLPrefix)

// copiedPrefixes are the ones the composition root keeps UNEXPORTED, so they
// have to be spelled a second time here. Those are the two this audit can be
// wrong about, and [TestTheGuardedPrefixesStillExist] is what keeps the copy
// honest. The panel prefix needs no such check: it is imported from the package
// that declares it, so a rename does not compile.
var copiedPrefixes = []string{"/admin/v1", "/store/v1"}

// callbackRegistryFile is the one place allowed to bind a path outside them.
const callbackRegistryFile = "core/http/callback.go"

// stateChangingMethods are the chi registrations this audit looks at.
//
// GET and HEAD are left out: a read outside the guard stack is a decision this
// repository has already made in several places (/health, /ready,
// /openapi.json, /files), and it is not what an unguarded endpoint costs. What
// costs is a WRITE nothing authenticates — the class the one measured example
// belonged to, where an unauthenticated POST moved a payment to paid.
var stateChangingMethods = map[string]bool{
	"Post": true, "Put": true, "Patch": true, "Delete": true, "Method": true,
}

// routeSite is one route registration found in the source.
type routeSite struct {
	file   *sourceFile
	method string
	// paths are the possible full paths, prefix included.
	paths []string
	// pos names the line, for the error message.
	pos string
}

// TestEveryStateChangingRouteIsGuarded closes the door the callback registry
// was built to close.
//
// # What went wrong before
//
// The one inbound callback in this repository bound itself on the ROOT router,
// outside every guarded prefix. It therefore had no authentication, no quota,
// no idempotency, no audit and no body limit — its only protection was an HMAC
// inside the handler — and nothing in the repository said so. It was found by
// measurement, not by a test, and the reason no test could find it is that a
// route bound on the root router looks exactly like a route bound under a
// prefix.
//
// # Why this test is the enforcement and the registry is not
//
// [github.com/bdrtr/gobit/core/http.CallbackRegistry] makes the guarded thing
// easy: a plugin registers a route and gets the quota, the body limit, the
// replay window and an enforced signature check. It cannot make the UNGUARDED
// thing impossible — a plugin can still call r.Post on the router it is handed,
// and that is the whole failure mode. Only a test reading the source can say
// "there is no such call", which is why the registry ships with this test
// rather than on its own.
func TestEveryStateChangingRouteIsGuarded(t *testing.T) {
	t.Parallel()

	tree := scanProductionSource(t)

	seen := 0
	for _, site := range routeRegistrations(tree) {
		if site.file.path == callbackRegistryFile {
			// The registry binds the callback paths itself. That IS the guarded
			// door; asserting a prefix on it would be asserting it against
			// itself.
			continue
		}
		seen++

		assert.NotEmpty(t, site.paths,
			"%s: the path of the %s route could not be resolved.\n"+
				"An unresolvable path hides from this audit which prefix the route lands "+
				"under, and an unguarded route is exactly what hiding looks like. Write the "+
				"path as a literal or as a constant this scan can follow.", site.pos, site.method)

		for _, path := range site.paths {
			assert.True(t, isGuarded(path),
				"%s: the %s route %q is bound outside every guarded prefix (%s).\n"+
					"Nothing authenticates it, nothing bounds its body, nothing limits its "+
					"rate and nothing records it. If it is a provider callback, register it "+
					"through core/http.CallbackRegistry — the registry binds the path itself "+
					"and refuses a route with no signature check. If it is anything else, it "+
					"belongs under one of the guarded prefixes.",
				site.pos, site.method, path, strings.Join(guardedPrefixes, ", "))
		}
	}

	require.Positive(t, seen,
		"not a SINGLE state-changing route was found in the production source; the scan "+
			"has gone BLIND.\nEvery module registers writes, so an empty result means the "+
			"registration form changed (a helper, a different router type) and this audit "+
			"is approving whatever replaced it.")
}

// TestTheGuardedPrefixesStillExist keeps [guardedPrefixes] honest.
//
// The list is a copy of what the composition root installs. A prefix renamed
// there and not here would not fail anything: this audit would go on approving
// routes under a prefix nothing guards any more, and refusing routes under the
// one that is now guarded — a failure in both directions, neither of them
// obviously about a rename.
func TestTheGuardedPrefixesStillExist(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile(filepath.Join(repoRoot, "internal", "app", "setup.go"))
	require.NoError(t, err, "the composition root could not be read")

	for _, prefix := range copiedPrefixes {
		require.Contains(t, string(source), `"`+prefix+`"`,
			"the composition root no longer mentions the %q prefix.\n"+
				"guardedPrefixes is a copy of what it installs; if the prefix moved, this "+
				"audit is checking routes against a guard that is not there.", prefix)
	}
}

// routeRegistrations finds every state-changing chi registration, carrying the
// prefix of the Route blocks it sits inside.
func routeRegistrations(tree *sourceTree) []routeSite {
	var sites []routeSite

	for _, file := range tree.files {
		// A route can only be registered in a file that has the router type in
		// hand. Without this the scan matches any method called Post or Delete —
		// a repository's Delete, a store's Post — and reports them as routes
		// whose path it cannot resolve, which is a false alarm that would teach
		// people to ignore this audit.
		if !importsChi(file) {
			continue
		}
		for _, decl := range file.tree.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || fn.Body == nil {
				continue
			}
			sites = append(sites, routesIn(tree, file, fn, fn.Body, "")...)
		}
	}

	return sites
}

// importsChi reports whether the file imports the router package.
func importsChi(file *sourceFile) bool {
	for _, path := range file.imports {
		if path == chiImportPath {
			return true
		}
	}

	return false
}

// chiImportPath is the router this repository registers its routes with.
const chiImportPath = "github.com/go-chi/chi/v5"

// routesIn walks one body, descending into Route blocks with their prefix.
func routesIn(
	tree *sourceTree, file *sourceFile, fn *ast.FuncDecl, body ast.Node, prefix string,
) []routeSite {
	var sites []routeSite

	ast.Inspect(body, func(node ast.Node) bool {
		call, isCall := node.(*ast.CallExpr)
		if !isCall {
			return true
		}
		selector, isSelector := call.Fun.(*ast.SelectorExpr)
		if !isSelector {
			return true
		}

		if selector.Sel.Name == "Route" && len(call.Args) == 2 {
			// chi's Route mounts a subtree; everything inside it is relative to
			// the prefix, so the walk continues with the prefix carried along
			// and this branch is NOT descended into a second time.
			for _, nested := range tree.stringValues(file, fn, call.Args[0], 0) {
				sites = append(sites, routesIn(tree, file, fn, call.Args[1], prefix+nested)...)
			}

			return false
		}

		if !stateChangingMethods[selector.Sel.Name] {
			return true
		}

		index := 0
		if selector.Sel.Name == "Method" {
			// Method(verb, pattern, handler): the pattern is the second argument.
			index = 1
		}
		if len(call.Args) <= index {
			return true
		}

		paths := tree.stringValues(file, fn, call.Args[index], 0)
		for i, path := range paths {
			paths[i] = prefix + path
		}
		sites = append(sites, routeSite{
			file:   file,
			method: selector.Sel.Name,
			paths:  paths,
			pos:    tree.location(file, call.Lparen),
		})

		return true
	})

	return sites
}

// isGuarded reports whether a path falls under a guarded prefix.
func isGuarded(path string) bool {
	for _, prefix := range guardedPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}

	return false
}
