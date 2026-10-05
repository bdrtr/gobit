package arch_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file holds the end-to-end ground to the production assembly.
//
// The ground used to build its own container, registry, guard stack and router,
// and the gates here compared that copy with the composition root, flow by flow
// and module by module. The copy drifted where no gate looked — its guard stack
// — and the gate holding production to the ground's flows was green while
// production could drop the gift card sale flow's subscription (D254, D255).
//
// Since ADR 0398 the ground opens the installation through app.Open, the
// function the server's assembly shares, so there is nothing left to compare.
// What is left to hold is that it STAYS the server's: that it adds only what an
// embedder adds, and that it builds no flow that subscribes when it is built.
//
// That is narrower than "it builds no flow the root wires". The ground still
// builds the cart, checkout, fulfilling, stock alert and segment flows on the
// installation's container and drives them itself; those subscribe to nothing,
// so building one starts nothing, but a scenario that runs the ground's stock
// alert or segment pass does not show that production schedules it.
const (
	// e2eGroundDir is the end-to-end ground.
	e2eGroundDir = "internal/e2e"

	// appImportPath is the composition root's import path.
	appImportPath = modulePath + "/internal/app"

	// moduleSiblingPrefix is the module path every module of this organization
	// starts with: a nested module under it may import internal/.
	moduleSiblingPrefix = modulePath + "/"
)

// TestTheEndToEndGroundIsTheProductionAssembly keeps the ground from growing a
// composition of its own.
//
// It reads every Go file of internal/e2e, test files included, and refuses:
//
//   - any number of app.Open calls but one;
//   - a Provide outside the Register method of a module named in that call's
//     Modules field, which is where an embedder provides a name;
//   - a Subscribe outside the Setup method of a plugin named in its Plugins
//     field, which is where an embedder subscribes;
//   - a Bootstrap, or a router or guard stack built through the HTTP core;
//   - any reference to a function of a flow package that subscribes to the
//     bus anywhere outside its tests. Such a flow is wired by being built, so
//     a ground that built one would run it in every scenario whether
//     production wired it or not. Its constants and types stay legal:
//     resolving its surface by name is reading what production provided.
func TestTheEndToEndGroundIsTheProductionAssembly(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	files := parseDir(t, fset, filepath.Join(repoRoot, e2eGroundDir), true)
	require.NotEmpty(t, files, "there is no Go file to parse in %s", e2eGroundDir)

	subscribing := subscribingFlowFunctions(t)
	for _, flow := range knownSubscribingFlows {
		require.Contains(t, subscribing, flow,
			"%s subscribes when it is built, and the scan of %s no longer sees it. Either "+
				"the flow stopped listening, and this list should lose it, or the scan went "+
				"blind to how it subscribes now, and the ground could build it again", flow, workflowsDirName)
	}

	open := theOneOpenCall(t, fset, files)
	modules := literalTypeNames(open, "Modules")
	plugins := literalTypeNames(open, "Plugins")
	require.NotEmpty(t, modules, "the app.Open call names no module; the identity module is where the ground provides")
	require.NotEmpty(t, plugins, "the app.Open call names no plugin; the observers plugin is where the ground subscribes")

	var violations []string
	var provides, subscribes int
	report := func(node ast.Node, format string, args ...any) {
		violations = append(violations, fset.Position(node.Pos()).String()+": "+fmt.Sprintf(format, args...))
	}

	for _, file := range files {
		for _, decl := range file.tree.Decls {
			allowProvide, allowSubscribe := false, false
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv != nil && len(fn.Recv.List) == 1 {
				receiver := receiverTypeName(fn.Recv.List[0].Type)
				allowProvide = fn.Name.Name == "Register" && modules[receiver]
				allowSubscribe = fn.Name.Name == "Setup" && plugins[receiver]
			}

			ast.Inspect(decl, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if pkg, isIdent := selector.X.(*ast.Ident); isIdent {
					path := file.imports[pkg.Name]
					if subscribing[path][selector.Sel.Name] {
						report(selector, "%s.%s is a function of %s, a flow that subscribes when it is built; "+
							"resolve its surface from the container instead", pkg.Name, selector.Sel.Name, path)
					}
					if path == coreHTTPImportPath && (selector.Sel.Name == "NewRouter" || selector.Sel.Name == "APIGuards") {
						report(selector, "%s.%s builds a router or a guard stack of the ground's own; "+
							"the ground's are the server's", pkg.Name, selector.Sel.Name)
					}
				}

				return true
			})

			ast.Inspect(decl, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch selector.Sel.Name {
				case "Provide":
					if !allowProvide {
						report(call, "a name is provided outside a Register method of a module the "+
							"app.Open call names (%v)", sortedNames(modules))
					}
					provides++
				case "Subscribe":
					if !allowSubscribe {
						report(call, "a subscription is made outside a Setup method of a plugin the "+
							"app.Open call names (%v)", sortedNames(plugins))
					}
					subscribes++
				case "Bootstrap":
					report(call, "the ground brings modules up itself; app.Open is the only bootstrap")
				}

				return true
			})
		}
	}

	require.Positive(t, provides,
		"no Provide call was found in %s at all; the identity module provides one, so the scan has gone blind",
		e2eGroundDir)
	require.Positive(t, subscribes,
		"no Subscribe call was found in %s at all; the observers plugin makes two, so the scan has gone blind",
		e2eGroundDir)

	assert.Empty(t, violations,
		"%s composes something of its own. The ground is the server's installation (ADR 0398): "+
			"it adds only what an embedder adds, a module's Register and a plugin's Setup, and "+
			"it builds no flow that subscribes when it is built. A ground that wired its own "+
			"copy would prove a build nobody ships.", e2eGroundDir)
}

// TestOnlyTheFacadeAndTheGroundOpenTheCompositionRoot keeps app.Open's reach
// where ADR 0398 put it.
//
// The installation it returns carries the container, whose names are not a
// contract (ADR 0001); the facade hands an embedder the router alone (ADR
// 0150). So the importers of internal/app are the repository's root package,
// the end-to-end ground and internal/app's own tests. The walk includes the
// nested modules whose path is this organization's — contrib's identity
// modules can import internal/ — and skips only the ones outside it.
func TestOnlyTheFacadeAndTheGroundOpenTheCompositionRoot(t *testing.T) {
	t.Parallel()

	allowed := []string{repositoryRoot, e2eGroundDir, compositionRoot}

	var importers, violations []string
	nestedFiles := 0

	err := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if slices.Contains(skippedDirs, d.Name()) {
				return filepath.SkipDir
			}
			if filepath.Clean(path) != filepath.Clean(repoRoot) && isNestedModule(path) &&
				!strings.HasPrefix(nestedModulePath(t, path), moduleSiblingPrefix) {
				return filepath.SkipDir
			}

			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		if nestedUnder(path) {
			nestedFiles++
		}

		tree, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, imp := range tree.Imports {
			if strings.Trim(imp.Path.Value, `"`) != appImportPath {
				continue
			}
			importers = append(importers, filepath.ToSlash(rel))
			if !slices.Contains(allowed, filepath.ToSlash(filepath.Dir(rel))) {
				violations = append(violations, filepath.ToSlash(rel))
			}
		}

		return nil
	})
	require.NoError(t, err)

	require.Contains(t, importers, "gobit.go",
		"the facade was not seen importing %s; the walk has gone blind or the facade moved", appImportPath)
	require.Positive(t, nestedFiles,
		"no file of a nested module under %s was read; contrib is where internal/ can be "+
			"imported from outside this module, and a walk that skipped it is blind there", moduleSiblingPrefix)

	sort.Strings(violations)
	assert.Empty(t, violations,
		"these files import %s. Its Installation carries the container, whose names are not a "+
			"contract (ADR 0001), and only the facade and the end-to-end ground may open it (ADR 0398).",
		appImportPath)
}

// knownSubscribingFlows is the floor under [subscribingFlowFunctions]: the
// flows known today to subscribe when they are built. The scan derives the set
// and may find more; it may not find fewer, because a flow that drops out of
// it silently becomes one the ground may build again.
var knownSubscribingFlows = []string{
	modulePath + "/" + workflowsDirName + "/giftcardsale",
	modulePath + "/" + workflowsDirName + "/ordercancel",
	modulePath + "/" + workflowsDirName + "/returns",
}

// subscribingFlowFunctions returns, per flow package that subscribes to the
// bus, the names of its package-level functions.
//
// A package counts when ANY of its non-test functions or methods calls
// Subscribe, not only its FromContainer: the subscription moving into a helper
// that FromContainer calls must not take the flow out of the set.
func subscribingFlowFunctions(t *testing.T) map[string]map[string]bool {
	t.Helper()

	out := map[string]map[string]bool{}
	root := filepath.Join(repoRoot, workflowsDirName)

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || !d.IsDir() {
			return walkErr
		}

		fset := token.NewFileSet()
		functions := map[string]bool{}
		subscribes := false
		for _, file := range parseDir(t, fset, path, false) {
			for _, decl := range file.tree.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				if callsSelector(fn.Body, "Subscribe") {
					subscribes = true
				}
				if fn.Recv == nil {
					functions[fn.Name.Name] = true
				}
			}
		}
		if !subscribes {
			return nil
		}

		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		out[modulePath+"/"+filepath.ToSlash(rel)] = functions

		return nil
	})
	require.NoError(t, err, "%s could not be walked", workflowsDirName)

	names := make([]string, 0, len(out))
	for path := range out {
		names = append(names, path)
	}
	sort.Strings(names)
	t.Logf("flows that subscribe to the bus: %v", names)

	return out
}

// theOneOpenCall returns the ground's only app.Open call.
func theOneOpenCall(t *testing.T, fset *token.FileSet, files []parsedFile) *ast.CallExpr {
	t.Helper()

	var calls []*ast.CallExpr
	var where []string
	for _, file := range files {
		local := ""
		for name, path := range file.imports {
			if path == appImportPath {
				local = name
			}
		}
		if local == "" {
			continue
		}
		ast.Inspect(file.tree, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if selector, isSelector := call.Fun.(*ast.SelectorExpr); isSelector {
				if pkg, isIdent := selector.X.(*ast.Ident); isIdent && pkg.Name == local && selector.Sel.Name == "Open" {
					calls = append(calls, call)
					where = append(where, fset.Position(call.Pos()).String())
				}
			}

			return true
		})
	}

	require.Len(t, calls, 1,
		"%s must open the installation exactly once, through app.Open; found %v. A second "+
			"installation is a second composition, and none is a ground that is not the server's",
		e2eGroundDir, where)

	return calls[0]
}

// literalTypeNames returns the type names in the slice literal under the field of
// an app.Open call's options literal.
func literalTypeNames(open *ast.CallExpr, field string) map[string]bool {
	names := map[string]bool{}
	if len(open.Args) != 2 {
		return names
	}
	options := open.Args[1]
	if unary, ok := options.(*ast.UnaryExpr); ok {
		options = unary.X
	}
	literal, ok := options.(*ast.CompositeLit)
	if !ok {
		return names
	}

	for _, element := range literal.Elts {
		pair, isPair := element.(*ast.KeyValueExpr)
		if !isPair {
			continue
		}
		if key, isIdent := pair.Key.(*ast.Ident); !isIdent || key.Name != field {
			continue
		}
		list, isList := pair.Value.(*ast.CompositeLit)
		if !isList {
			continue
		}
		for _, item := range list.Elts {
			if unary, isUnary := item.(*ast.UnaryExpr); isUnary {
				item = unary.X
			}
			if value, isValue := item.(*ast.CompositeLit); isValue {
				if name := receiverTypeName(value.Type); name != "" {
					names[name] = true
				}
			}
		}
	}

	return names
}

// callsSelector reports whether a body calls a method or function by the name.
func callsSelector(body *ast.BlockStmt, name string) bool {
	if body == nil {
		return false
	}

	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			if selector, isSelector := call.Fun.(*ast.SelectorExpr); isSelector && selector.Sel.Name == name {
				found = true
			}
		}

		return !found
	})

	return found
}

// nestedModulePath reads the module path a nested go.mod declares.
func nestedModulePath(t *testing.T, dir string) string {
	t.Helper()

	content, err := os.ReadFile(filepath.Join(dir, goModFileName))
	require.NoError(t, err)
	for _, line := range strings.Split(string(content), "\n") {
		if rest, found := strings.CutPrefix(strings.TrimSpace(line), "module "); found {
			return strings.TrimSpace(rest)
		}
	}

	return ""
}

// nestedUnder reports whether a file belongs to a nested module rather than to
// the repository's own.
func nestedUnder(path string) bool {
	for dir := filepath.Dir(path); filepath.Clean(dir) != filepath.Clean(repoRoot); dir = filepath.Dir(dir) {
		if isNestedModule(dir) {
			return true
		}
		if dir == "." || dir == string(filepath.Separator) {
			return false
		}
	}

	return false
}
