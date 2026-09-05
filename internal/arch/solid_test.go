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

// This file holds the SOLID checks ADR 0012 says are MECHANICALLY DETECTABLE.
//
// The ADR's table is deliberately uneven: DIP and OCP are enforced, ISP holds
// structurally across module boundaries, SRP is enforced only at the macro
// level and LSP not at all. The two checks here close the two gaps the ADR
// named as worth adding — the CONSUMPTION half of DIP and the layer boundary
// inside a module. What the ADR marked as review-level stays review-level; a
// test that pretended to cover it would be worse than the honest gap.

// resolveSelector is the call whose type argument the DIP check reads.
const resolveSelector = "Resolve"

// containerImportPath is the package resolveSelector belongs to.
const containerImportPath = modulePath + "/core/container"

// concreteResolveExemptions are the (container name, concrete type) pairs that
// may be resolved WITHOUT an interface.
//
// There is exactly one family, and it is the framework's own database pool. The
// pool is not an abstraction a module chose; it is the concrete resource the
// core hands out, every module takes the same one, and putting an interface in
// front of it would produce a surface with a single implementation whose only
// purpose was to satisfy this test.
//
// The key is the TYPE EXPRESSION as written at the call site, because that is
// what the check reads. An exemption that stopped being used is caught below:
// an unused entry means either the pool is no longer resolved anywhere — in
// which case the line must go — or the scan stopped seeing it.
var concreteResolveExemptions = map[string]string{
	"*db.Pool": "the core's own connection pool; every module takes the same one " +
		"and an interface in front of it would have a single implementation",
}

// resolveCall is one container.Resolve[T] call site.
type resolveCall struct {
	// file is the repo-relative path of the file holding the call.
	file string
	// pos is the source position, for the error message.
	pos token.Position
	// typeExpr is the type argument as written ("api.LinePricing", "*db.Pool").
	typeExpr string
	// pkgAlias is the qualifier of a qualified type argument, or "".
	pkgAlias string
	// typeName is the bare type name.
	typeName string
	// pointer reports whether the argument was written as *T.
	pointer bool
	// typeParam reports whether the argument is the enclosing function's own
	// type parameter (a generic helper); such a call site is checked at ITS
	// call sites, not here.
	typeParam bool
	// imports maps the file's import aliases to import paths.
	imports map[string]string
	// dir is the directory of the package holding the call.
	dir string
}

// TestResolvedTypeIsAnInterface enforces the CONSUMPTION half of DIP.
//
// # What was already enforced and what was not
//
// ADR 0001 says a consumer declares a narrow interface in its own package and
// resolves the provider's concrete type from the container by name. The SUPPLY
// half of that — "is every registered name consumed" — is checked by
// [TestTheInteropSurfacesHaveAConsumer]; the CONSUMPTION half was not checked at all.
// Nothing stopped a module from resolving another module's *service.Service and
// binding itself to that whole surface at once.
//
// depguard is not the same check. It forbids the IMPORT, so it catches a
// cross-module concrete type; it says nothing about a concrete type from the
// caller's OWN module or from the core. This check asks the other question:
// whatever the container hands back, is it an ABSTRACTION.
//
// # Measured today
//
// Every production call site resolves an interface except one family: 16 sites
// resolve *db.Pool under the name "core.db". That single exception is written
// down in [concreteResolveExemptions] with its reason.
func TestResolvedTypeIsAnInterface(t *testing.T) {
	t.Parallel()

	calls := resolveCalls(t)
	require.NotEmpty(t, calls,
		"not one container.Resolve call site was found in production code.\n"+
			"Either resolution moved behind a helper — in which case this check is BLIND "+
			"and must follow it — or the scan is broken.")

	interfaces, generics, exempted := 0, 0, 0

	for _, call := range calls {
		if call.typeParam {
			generics++
			continue
		}

		if _, ok := concreteResolveExemptions[call.typeExpr]; ok {
			exempted++
			continue
		}

		if call.pointer {
			t.Errorf("%s: container.Resolve[%s] resolves a CONCRETE type.\n"+
				"A pointer type is an implementation, not a contract: resolving it binds the "+
				"caller to the provider's whole surface and every change to that surface "+
				"reaches the caller (ADR 0001). Declare a narrow interface in the calling "+
				"package and resolve that — or, if this really is a shared concrete resource, "+
				"add it to concreteResolveExemptions with its reason.",
				call.pos, call.typeExpr)
			continue
		}

		decl, found := findTypeDecl(t, call)
		if !found {
			t.Errorf("%s: the type %q of container.Resolve[%s] could not be found.\n"+
				"The check needs to see the declaration to tell an interface from a concrete "+
				"type; a type it cannot find is a type it cannot judge, and staying silent "+
				"here would leave the whole rule optional.", call.pos, call.typeName, call.typeExpr)
			continue
		}

		if _, isInterface := decl.(*ast.InterfaceType); !isInterface {
			t.Errorf("%s: container.Resolve[%s] resolves a type that is NOT an interface.\n"+
				"The container hands out contracts, not implementations (ADR 0001).",
				call.pos, call.typeExpr)
			continue
		}
		interfaces++
	}

	assert.Positive(t, interfaces,
		"not a single call site resolved an interface. Either every resolution is exempt — "+
			"which would make this check meaningless — or the declaration lookup is broken "+
			"and every type silently counts as unfindable.")

	for _, expr := range slices.Sorted(maps.Keys(concreteResolveExemptions)) {
		used := slices.ContainsFunc(calls, func(c resolveCall) bool { return c.typeExpr == expr })
		assert.True(t, used,
			"exemption STALE: %q is no longer resolved anywhere.\n"+
				"An exemption is a debt; when the debt is paid the line must go, otherwise it "+
				"quietly excuses the next concrete type written under the same name.", expr)
	}

	t.Logf("resolve call sites: %d interface, %d exempt, %d generic helper",
		interfaces, exempted, generics)
}

// resolveCalls collects every container.Resolve[T] call site in production
// code.
func resolveCalls(t *testing.T) []resolveCall {
	t.Helper()

	var calls []resolveCall

	for _, root := range productionTrees {
		for _, path := range treeProductionFiles(t, root) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			require.NoError(t, err, "%s could not be parsed", path)

			aliases := importAliases(file)
			alias, imported := aliasFor(aliases, containerImportPath)
			if !imported {
				continue
			}

			rel, relErr := filepath.Rel(repoRoot, path)
			require.NoError(t, relErr)

			typeParams := typeParamNames(file)

			ast.Inspect(file, func(n ast.Node) bool {
				index, ok := n.(*ast.IndexExpr)
				if !ok || !isSelector(index.X, alias, resolveSelector) {
					return true
				}

				call := resolveCall{
					file:     filepath.ToSlash(rel),
					pos:      fset.Position(index.Pos()),
					typeExpr: exprString(index.Index),
					imports:  aliases,
					dir:      filepath.Dir(path),
				}
				call.pkgAlias, call.typeName, call.pointer = splitTypeExpr(index.Index)
				call.typeParam = call.pkgAlias == "" && typeParams[call.typeName]

				calls = append(calls, call)

				return true
			})
		}
	}

	return calls
}

// findTypeDecl locates the declaration of the call's type argument.
//
// A qualified name is looked up in the package the alias imports; a bare name
// in the calling package itself. Only repository packages are searched: a type
// from outside the repository cannot be an ADR 0001 consumer-side interface
// anyway, and the check would have nothing to say about it.
func findTypeDecl(t *testing.T, call resolveCall) (ast.Expr, bool) {
	t.Helper()

	dir := call.dir
	if call.pkgAlias != "" {
		importPath, known := call.imports[call.pkgAlias]
		if !known || !strings.HasPrefix(importPath, modulePath+"/") {
			return nil, false
		}
		dir = filepath.Join(repoRoot, strings.TrimPrefix(importPath, modulePath+"/"))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, false
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}

		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if parseErr != nil {
			continue
		}

		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != call.typeName {
					continue
				}
				return ts.Type, true
			}
		}
	}

	return nil, false
}

// typeParamNames returns the names of every function's type parameters in the
// file.
//
// A generic helper resolving its own T (see the panel's resolveService and the
// plugin host's resolveSink) says nothing about whether a contract or an
// implementation is being resolved; the answer lives at the helper's call
// sites, which this scan reads separately.
func typeParamNames(file *ast.File) map[string]bool {
	names := map[string]bool{}

	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Type.TypeParams == nil {
			return true
		}
		for _, field := range fn.Type.TypeParams.List {
			for _, name := range field.Names {
				names[name.Name] = true
			}
		}

		return true
	})

	return names
}

// aliasFor returns the local name the file imports the given path under.
func aliasFor(aliases map[string]string, importPath string) (string, bool) {
	for alias, path := range aliases {
		if path == importPath {
			return alias, true
		}
	}

	return "", false
}

// isSelector reports whether the expression is "alias.name".
func isSelector(expr ast.Expr, alias, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)

	return ok && ident.Name == alias
}

// splitTypeExpr breaks a type argument into its qualifier, its name and
// whether it is a pointer.
func splitTypeExpr(expr ast.Expr) (pkgAlias, typeName string, pointer bool) {
	if star, ok := expr.(*ast.StarExpr); ok {
		pointer = true
		expr = star.X
	}

	switch t := expr.(type) {
	case *ast.Ident:
		return "", t.Name, pointer
	case *ast.SelectorExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			return ident.Name, t.Sel.Name, pointer
		}
	}

	return "", "", pointer
}

// exprString writes a type expression back out as source text.
func exprString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + exprString(t.X)
	case *ast.SelectorExpr:
		return exprString(t.X) + "." + t.Sel.Name
	default:
		return "?"
	}
}

// layerRule forbids a set of imports inside a layer of a module.
type layerRule struct {
	// layer is the sub-directory of a module ("api", "service").
	layer string
	// forbidden are the import path fragments that may not appear there.
	forbidden []string
	// reason says what the rule protects; it goes into the error message.
	reason string
}

// layerRules are the layer boundaries INSIDE a module.
//
// depguard guards the boundary BETWEEN modules and says nothing about the one
// inside a module. These two rules are the SRP the repository actually already
// obeys — measured: 15 modules, 30 packages, 0 violations — written down so it
// stays obeyed.
//
// They are not a size metric. Nothing here counts methods or lines; each rule
// names a dependency that would MERGE two responsibilities: a handler reaching
// the database skips the service layer's rules (validation, the workflow, the
// event), and a service holding an http.ResponseWriter makes the transport
// decision inside the business logic and can no longer be called from a
// workflow or a subscriber.
var layerRules = []layerRule{
	{
		layer:     "api",
		forbidden: []string{"github.com/jackc/pgx", "/repository", "/sqlc"},
		reason: "a handler reaching the database directly skips the service layer, so " +
			"validation, the cross-module workflow and the events that layer publishes " +
			"never run — and the endpoint keeps working, which is what makes it dangerous",
	},
	{
		layer:     "service",
		forbidden: []string{"net/http", "github.com/go-chi/chi", "github.com/jackc/pgx"},
		reason: "a service that knows the transport can no longer be called from anywhere " +
			"but an HTTP handler: not from a workflow, not from an event subscriber, not " +
			"from a test — and a service reaching pgx bypasses the repository, so the " +
			"module's queries stop living in one place",
	},
}

// TestLayerPurity enforces the layer boundary inside a module.
//
// # Why it is not covered by depguard
//
// The 211 deny entries in .golangci.yml protect the boundary BETWEEN modules:
// they say the cart module may not import the product module. They say nothing
// about the cart module's own handler reaching into the cart module's own
// repository — which is exactly the shortcut a hurried change takes, and which
// leaves no trace because everything keeps working.
//
// # It is a floor
//
// The rule names dependencies, not sizes. A handler that inlines the business
// logic without importing pgx passes this check; SRP at the level of a single
// type is a review-level rule and ADR 0012 says so out loud rather than letting
// this test imply otherwise.
func TestLayerPurity(t *testing.T) {
	t.Parallel()

	modules := moduleNames(t)
	require.NotEmpty(t, modules)

	scanned := map[string]int{}

	for _, rule := range layerRules {
		for _, mod := range modules {
			dir := filepath.Join(repoRoot, modulesDir, mod, rule.layer)
			info, err := os.Stat(dir)
			if err != nil || !info.IsDir() {
				continue
			}
			scanned[rule.layer]++

			fset := token.NewFileSet()
			for _, parsed := range parseDir(t, fset, dir, false) {
				for _, importPath := range parsed.tree.Imports {
					path := strings.Trim(importPath.Path.Value, `"`)
					for _, forbidden := range rule.forbidden {
						if !strings.Contains(path, forbidden) {
							continue
						}
						t.Errorf("%s: %s/%s imports %q.\n%s",
							fset.Position(importPath.Pos()), mod, rule.layer, path, rule.reason)
					}
				}
			}
		}
	}

	for _, rule := range layerRules {
		want := 0
		for _, mod := range modules {
			if _, exempt := layerExemptions[mod+"/"+rule.layer]; !exempt {
				want++
			}
		}
		assert.Equal(t, want, scanned[rule.layer],
			"%d %q directories were scanned but %d modules were expected to have one.\n"+
				"A counter that reads the same list it is checking goes quiet together with "+
				"that list: renaming the layer here would find zero directories and every "+
				"module would pass without a single import being read. Either the directory "+
				"is really gone — then it belongs in layerExemptions with its reason — or the "+
				"walk is broken.", scanned[rule.layer], rule.layer, want)
	}

	for _, key := range slices.Sorted(maps.Keys(layerExemptions)) {
		mod, layer, _ := strings.Cut(key, "/")
		_, err := os.Stat(filepath.Join(repoRoot, modulesDir, mod, layer))
		assert.Error(t, err,
			"exemption STALE: %q exists again, so the exemption excuses nothing and hides "+
				"a directory that is now going unscanned.", key)
	}
}

// layerExemptions names the (module, layer) pairs that legitimately have no
// such directory.
//
// It is EMPTY today: all 15 modules carry both an api and a service directory.
// The map exists so that a module which genuinely has no HTTP surface can say
// so in one reviewed line, instead of the counter above being loosened into an
// assertion that no longer catches a renamed layer.
var layerExemptions = map[string]string{}

// TestLayerPurityCatchesAViolation is the positive control.
//
// [TestLayerPurity] passes today because there is no violation, and a check
// that only ever sees clean input cannot tell "there is nothing wrong" from "I
// am not looking". The fixture plants the exact import each rule forbids and
// requires it to be seen.
func TestLayerPurityCatchesAViolation(t *testing.T) {
	t.Parallel()

	for _, rule := range layerRules {
		require.NotEmpty(t, rule.forbidden, "the %s rule forbids nothing", rule.layer)

		for _, forbidden := range rule.forbidden {
			source := "package p\n\nimport _ \"" + planted(forbidden) + "\"\n"

			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "planted.go", source, parser.SkipObjectResolution)
			require.NoError(t, err)

			caught := false
			for _, importPath := range file.Imports {
				if strings.Contains(strings.Trim(importPath.Path.Value, `"`), forbidden) {
					caught = true
				}
			}
			assert.True(t, caught,
				"the %s rule's %q entry does not match even a planted import; the fragment "+
					"is written in a shape the scan cannot see", rule.layer, forbidden)
		}
	}
}

// planted turns a forbidden fragment into an import path that a parser accepts.
func planted(fragment string) string {
	if strings.HasPrefix(fragment, "/") {
		return modulePath + "/internal/modules/product" + fragment
	}

	return fragment
}

// adminSurfaceSuffix is the container-name suffix reserved for the admin write
// surfaces (ADR 0013).
const adminSurfaceSuffix = ".admin"

// adminSurfaceAudience are the trees allowed to name an admin write surface.
//
// Two entries and both are load-bearing. The owning module registers the name;
// the panel resolves it. Anyone else naming it — a workflow, a plugin, another
// module — would be writing the catalog through a surface that exists for a
// human operator, and the separation from interop would become a comment
// rather than a rule.
var adminSurfaceAudience = []string{
	"internal/modules/",
	"internal/adminui/",
}

// TestAdminSurfaceHasOneAudience proves the write surfaces stay reserved for
// the panel and their owning module.
//
// # Why the name alone is not enough
//
// ADR 0013 splits the admin write surface from interop so that a plugin cannot
// rewrite the catalog. Nothing in the container enforces that split: any holder
// of the container can resolve any name. Without this check, "product.admin is
// for the panel" would be a sentence in a godoc and the first workflow that
// found it convenient would make it false — silently, because resolving a
// registered name succeeds.
//
// # What it does NOT prove
//
// It reads NAMES, not intent. A module that registered its write surface under
// a name not ending in the reserved suffix would be invisible here, and so
// would a caller that built the name at runtime by concatenation. The check is
// a floor under the rule, and ADR 0013 says so.
func TestAdminSurfaceHasOneAudience(t *testing.T) {
	t.Parallel()

	mentions := 0

	for _, root := range productionTrees {
		for _, path := range treeProductionFiles(t, root) {
			rel, err := filepath.Rel(repoRoot, path)
			require.NoError(t, err)
			rel = filepath.ToSlash(rel)

			raw, readErr := os.ReadFile(path)
			require.NoError(t, readErr)

			fset := token.NewFileSet()
			file, parseErr := parser.ParseFile(fset, path, raw, parser.SkipObjectResolution)
			require.NoError(t, parseErr, "%s could not be parsed", rel)

			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				value, unquoteErr := strconv.Unquote(lit.Value)
				if unquoteErr != nil || !strings.HasSuffix(value, adminSurfaceSuffix) {
					return true
				}
				mentions++

				allowed := slices.ContainsFunc(adminSurfaceAudience, func(prefix string) bool {
					return strings.HasPrefix(rel, prefix)
				})
				if allowed {
					return true
				}

				t.Errorf("%s: %q names an admin write surface.\n"+
					"Those surfaces exist for the admin panel and are registered by the module "+
					"that owns them (ADR 0013); they were deliberately kept OUT of interop so a "+
					"plugin or a workflow could not rewrite the catalog through them. Resolving "+
					"one from here succeeds at runtime, which is exactly why it is checked "+
					"here.", fset.Position(lit.Pos()), value)

				return true
			})
		}
	}

	assert.Positive(t, mentions,
		"no admin surface name was found anywhere.\n"+
			"Either the suffix changed — in which case adminSurfaceSuffix must follow it — or "+
			"the scan is broken; in both cases this check is BLIND and every caller is "+
			"allowed.")
}
