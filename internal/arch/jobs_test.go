package arch_test

import (
	"go/ast"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go/token"

	"github.com/stretchr/testify/require"
)

// jobsDirName is the tree where the scheduled jobs live (ADR 0019).
//
// It shares the gap the workflow and panel trees had before their audits: it is
// neither core nor a module, so the depguard rules do not bind it and the
// module registration audit — which walks below internal/modules — cannot see
// it. The tree was opened by ADR 0019 and this closes the gap while it is still
// two packages wide.
const jobsDirName = "internal/jobs"

// jobDefinitionName is the conventional name of a job's constructor.
//
// A job package's only exported entry point is a function that RETURNS a
// [github.com/bdrtr/gobit/internal/core/job.Definition]; the composition root
// calls it and hands the result to the registry. That single convention is the
// audit's whole foothold in both directions.
const jobDefinitionName = "Definition"

// TestEveryJobIsRegisteredInTheCompositionRoot audits the jobs tree in both
// directions.
//
// A job package that nothing registers is the most expensive shape of defect
// this repository has a name for. It compiles, its unit tests pass — they build
// the definition themselves — and it runs in NO deployment. Worse than dead
// code: `gobit jobs` prints a listing with nothing missing from it, so an
// operator reads the ABSENCE of a reconciliation line as "there was nothing to
// reconcile" rather than "that job was never wired up".
func TestEveryJobIsRegisteredInTheCompositionRoot(t *testing.T) {
	t.Parallel()

	packages := jobPackages(t)
	require.NotEmpty(t, packages,
		"no package exporting %q was found below %s; the audit has gone BLIND (has the "+
			"constructor convention changed?)", jobDefinitionName, jobsDirName)

	registered := jobPackagesRegistered(t)
	require.NotEmpty(t, registered,
		"NO job constructor call was found in %s/; either the composition root stopped "+
			"registering jobs, or the call shape the audit reads has drifted", compositionRoot)

	for _, path := range slices.Sorted(maps.Keys(packages)) {
		if registered[path] {
			continue
		}
		t.Errorf("package %s exports %s() but is NOT REGISTERED in %s/.\n"+
			"An unregistered job runs nowhere, and its absence from `gobit jobs` reads as "+
			"\"nothing to report\" rather than \"never wired up\".",
			path, jobDefinitionName, compositionRoot)
	}

	for _, path := range slices.Sorted(maps.Keys(registered)) {
		if _, seen := packages[path]; !seen {
			t.Errorf("%s/ calls %s.%s() but the audit does NOT SEE that package below %s; "+
				"the shape reading has drifted from reality",
				compositionRoot, path, jobDefinitionName, jobsDirName)
		}
	}
}

// TestNoJobWritesThroughAModuleService holds ADR 0019's line, which ADR 0020
// restates for money: nothing scheduled acts.
//
// The mechanism is the same one the panel uses — a job names the surface it
// needs as a narrow interface in its OWN package — and the property that
// matters falls out of it: an interface with one read method cannot be used to
// write. Importing a module for its TYPES is fine and both jobs do it; taking a
// module's whole service is what would quietly hand a scheduled process the
// ability to change the world unwatched.
func TestNoJobWritesThroughAModuleService(t *testing.T) {
	t.Parallel()

	root := filepath.Join(repoRoot, jobsDirName)
	require.DirExists(t, root, "the %s tree does NOT EXIST; the rule has nothing left to "+
		"walk and stays green in a vacuum", jobsDirName)

	files := goFiles(t, root)
	require.NotEmpty(t, files, "there is NO Go file below %s; an empty file set shows not "+
		"that the rule has been lifted but that it has gone BLIND", jobsDirName)

	fset := token.NewFileSet()
	for _, dir := range jobDirs(t) {
		for _, file := range parseDir(t, fset, dir, false) {
			assertJobDeclaresItsOwnSurface(t, file)
		}
	}
}

// assertJobDeclaresItsOwnSurface checks one job file for a stored service.
func assertJobDeclaresItsOwnSurface(t *testing.T, file parsedFile) {
	t.Helper()

	ast.Inspect(file.tree, func(n ast.Node) bool {
		field, ok := n.(*ast.Field)
		if !ok {
			return true
		}

		star, ok := field.Type.(*ast.StarExpr)
		if !ok {
			return true
		}
		sel, ok := star.X.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}

		path := file.imports[pkg.Name]
		if !strings.HasPrefix(path, modulePath+"/internal/modules/") {
			return true
		}
		if sel.Sel.Name != "Service" {
			return true
		}

		t.Errorf("%s: a job holds *%s.%s directly.\n"+
			"A job names the surface it needs as a NARROW INTERFACE in its own package. "+
			"Holding the whole service hands a scheduled, unwatched process every write the "+
			"module can do — which is the one thing ADR 0017 and ADR 0020 both refuse.",
			file.path, pkg.Name, sel.Sel.Name)

		return true
	})
}

// jobDirs lists the package directories below the jobs tree.
func jobDirs(t *testing.T) []string {
	t.Helper()

	root := filepath.Join(repoRoot, jobsDirName)
	entries, err := os.ReadDir(root)
	require.NoError(t, err, "%s could not be read", root)

	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, filepath.Join(root, entry.Name()))
		}
	}

	return dirs
}

// jobPackages maps the import path of every job package to nothing.
//
// A job package is one that declares an exported function named
// [jobDefinitionName]. The shape is read rather than the name of the directory,
// so a package that stops being a job stops being audited as one.
func jobPackages(t *testing.T) map[string]struct{} {
	t.Helper()

	found := map[string]struct{}{}
	fset := token.NewFileSet()

	for _, dir := range jobDirs(t) {
		for _, file := range parseDir(t, fset, dir, false) {
			for _, decl := range file.tree.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || fn.Name.Name != jobDefinitionName {
					continue
				}
				found[packageImportPath(t, dir)] = struct{}{}
			}
		}
	}

	return found
}

// jobPackagesRegistered maps the import path of every job package whose
// constructor the composition root CALLS.
func jobPackagesRegistered(t *testing.T) map[string]bool {
	t.Helper()

	dir := filepath.Join(repoRoot, compositionRoot)
	fset := token.NewFileSet()

	registered := map[string]bool{}
	for _, file := range parseDir(t, fset, dir, false) {
		ast.Inspect(file.tree, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != jobDefinitionName {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}

			if path := file.imports[pkg.Name]; strings.HasPrefix(path, modulePath+"/"+jobsDirName+"/") {
				registered[path] = true
			}

			return true
		})
	}

	return registered
}
