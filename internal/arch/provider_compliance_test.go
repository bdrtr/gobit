package arch_test

import (
	"go/ast"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// providerComplianceFloor is the smallest population that could be the real one.
//
// Twelve providers ship in this tree today and the floor is well under it, for
// the reason every floor in this package is: a number that tracks the surface
// has to be edited on every ordinary change, and a floor edited that often
// stops being read. What it catches is the reader having stopped working.
const providerComplianceFloor = 8

// providersOutsideTheSuite are the types that satisfy provider.Provider and
// do NOT run the compliance suite.
//
// It is empty, and it is kept empty rather than deleted. `provider.Provider` has
// exactly one method, so Go's structural typing makes ANY type with an
// `ID() string` method a provider — including one that was never meant to be.
// The day such a type appears, the choice is between running the suite on it
// and writing down here why it is not a provider; a map that did not exist
// would make the second option "edit the gate".
var providersOutsideTheSuite = map[string]string{}

// TestEveryProviderRunsTheComplianceSuite derives the population from the tree.
//
// # Why a gate and not a convention
//
// `core/providertest` is published so that somebody writing a provider outside
// this repository can check it (ADR 0025). A suite the in-tree providers do not
// themselves run is a suite nobody maintains: it goes stale, and the first
// person to find out is the embedder who trusted it. This is what keeps the
// twelve providers that ship here on the same suite an embedder gets.
//
// # The population is walked, not listed
//
// Every method named ID returning a string, in a production file under
// `plugins/` or the module tree. That is the interface's whole shape, so the
// criterion cannot be escaped by the thing it looks for — the defect D10
// records, where deriving a scanned set from the property being audited let a
// component leave the audit by having the property.
//
// # Coverage is per PACKAGE, and that is exact TODAY
//
// The check is "this package's tests call providertest". Twelve packages hold
// one provider each, so package granularity and type granularity are the same
// set — and the second assertion below is what keeps them the same: a package
// growing a second provider fails, because one call would then cover two types
// and the gate would be claiming more than it checks.
func TestEveryProviderRunsTheComplianceSuite(t *testing.T) {
	t.Parallel()

	byPackage := map[string][]string{}
	for _, found := range providerImplementations(t) {
		byPackage[found.pkg] = append(byPackage[found.pkg], found.receiver)
	}

	require.GreaterOrEqual(t, len(byPackage), providerComplianceFloor,
		"only %d packages were found implementing a provider; the walk has gone blind "+
			"and an empty population agrees with an empty compliance list", len(byPackage))

	for _, pkg := range slices.Sorted(maps.Keys(byPackage)) {
		types := byPackage[pkg]

		if why, exempt := providersOutsideTheSuite[pkg]; exempt {
			assert.NotEmpty(t, why, "%s is exempt with no reason written down", pkg)

			continue
		}

		assert.Len(t, types, 1,
			"%s holds %d types satisfying provider.Provider (%v), and this gate checks "+
				"coverage PER PACKAGE — one compliance call would then stand for two "+
				"providers and the gate would claim more than it checks.\n"+
				"Split them into packages of their own, or teach this test to match a "+
				"call to its receiver.", pkg, len(types), types)

		assert.True(t, packageRunsTheSuite(t, pkg),
			"%s implements provider.Provider (%v) and its tests never call "+
				"core/providertest.\n"+
				"The suite checks the identity every registry keys on: the string an "+
				"operator types into configuration and a durable row records. A provider "+
				"outside it can register under one identity and answer with another, and "+
				"nothing in this tree would say so.", pkg, types)
	}
}

// providerImplementation is one type with an ID method.
type providerImplementation struct {
	pkg      string
	receiver string
}

// providerImplementations walks the two trees a provider can live in.
func providerImplementations(t *testing.T) []providerImplementation {
	t.Helper()

	var found []providerImplementation

	for _, tree := range []string{"plugins", modulesDir} {
		root := filepath.Join(repoRoot, tree)

		err := filepath.WalkDir(root, func(current string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if slices.Contains(skippedDirs, entry.Name()) {
					return filepath.SkipDir
				}

				return nil
			}
			if !strings.HasSuffix(current, ".go") || strings.HasSuffix(current, "_test.go") {
				return nil
			}

			relative, relErr := filepath.Rel(repoRoot, filepath.Dir(current))
			if relErr != nil {
				return relErr
			}

			fset := token.NewFileSet()
			for _, file := range parseDir(t, fset, filepath.Dir(current), false) {
				for _, decl := range file.tree.Decls {
					fn, ok := decl.(*ast.FuncDecl)
					if !ok || fn.Recv == nil || fn.Name.Name != "ID" {
						continue
					}
					if !returnsOneString(fn) {
						continue
					}

					entryFound := providerImplementation{
						pkg:      filepath.ToSlash(relative),
						receiver: publishedReceiver(fn),
					}
					if !slices.Contains(found, entryFound) {
						found = append(found, entryFound)
					}
				}
			}

			return nil
		})
		require.NoError(t, err, "%s could not be walked", tree)
	}

	return found
}

// returnsOneString reports whether the method's result is a single string.
func returnsOneString(fn *ast.FuncDecl) bool {
	results := fn.Type.Results
	if results == nil || len(results.List) != 1 || len(results.List[0].Names) > 1 {
		return false
	}

	ident, ok := results.List[0].Type.(*ast.Ident)

	return ok && ident.Name == "string"
}

// packageRunsTheSuite reports whether the package's tests call providertest.
//
// It matches the IMPORT and a call, not a particular function name: the suite
// has one entry point per contract and will grow more, and a gate listing them
// would refuse a provider for running the newest one.
func packageRunsTheSuite(t *testing.T, pkg string) bool {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(repoRoot, pkg))
	require.NoError(t, err, "%s could not be read", pkg)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}

		body, readErr := os.ReadFile(filepath.Join(repoRoot, pkg, entry.Name()))
		require.NoError(t, readErr)

		if strings.Contains(string(body), "providertest.") {
			return true
		}
	}

	return false
}
