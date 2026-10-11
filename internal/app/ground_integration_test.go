//go:build integration

package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// groundSources is where the end-to-end ground keeps its plugin list.
const groundSources = "../e2e"

// groundPluginsVar is the variable the ground joins into PLUGINS.
const groundPluginsVar = "groundPlugins"

// TestEveryPluginThatBindsARouteStandsOnTheGround holds the end-to-end ground
// to the catalog (D290).
//
// The ground's audits read the installation it opens: the authorization matrix
// classifies every route its router binds, the schema gates read the document
// it serves, and the store-access gates walk its storefront. A plugin the
// ground does not name is in none of them, so its routes, its callback and its
// panel screen are audited by nothing; PayTR stood there for that reason
// (D286), and web push did not until this test named it.
//
// The population is read off installations, not off the plugins' source: each
// plugin in the catalog is opened alone, and what its router binds beyond the
// default installation's is what it brings. A callback is bound by the ring on
// the same router, and a panel screen under the panel's prefix, so the one walk
// sees all three.
func TestEveryPluginThatBindsARouteStandsOnTheGround(t *testing.T) {
	setUpGateInstallation(t)
	for name, value := range everyPluginsSettings(t) {
		t.Setenv(name, value)
	}

	ground := groundPluginNames(t)

	base := installationRoutes(t, "")
	binding := map[string][]string{}
	for _, name := range pluginNames() {
		for route := range installationRoutes(t, name) {
			if !base[route] {
				binding[name] = append(binding[name], route.Method+" "+route.Path)
			}
		}
	}
	// A walk that attributed nothing would pass every plugin; PayTR's callback
	// and pending list are bound by no installation without it.
	require.NotEmpty(t, binding, "no plugin in the catalog binds a route; the walk has gone blind")

	for _, name := range pluginNames() {
		t.Logf("%s binds %d routes; on the ground: %t", name, len(binding[name]), ground[name])
	}
	for name, routes := range binding {
		slices.Sort(routes)
		assert.True(t, ground[name],
			"the %s plugin binds %s and does not stand on the end-to-end ground, so the "+
				"authorization matrix, the schema gates and the store-access gates never see "+
				"them. Name it in groundPlugins (internal/e2e) and give groundEnvironment "+
				"every setting it reads.", name, strings.Join(routes, ", "))
	}
}

// installationRoutes opens an installation with the plugins named, walks its
// router and closes it.
//
// It closes at once rather than at the test's end: a dozen installations open
// together would hold a dozen connection pools against one database.
func installationRoutes(t *testing.T, plugins string) map[AdminRoute]bool {
	t.Helper()

	t.Setenv("PLUGINS", plugins)
	installation, closeAll, err := Open(t.Context(), Options{Version: "gate"})
	require.NoError(t, err, "an installation with PLUGINS=%q does not open", plugins)
	defer closeAll()

	return boundRoutes(t, installation.Router)
}

// groundPluginNames reads the plugins the end-to-end ground names, from its
// source.
//
// The ground is a test package and cannot be imported, so its list is parsed:
// each element is a plugin package's Name, and the package is matched to the
// catalog by the constructor's type. Any other spelling fails here rather than
// being skipped, because a list this test could not read would leave every
// plugin off the ground and say so about none.
func groundPluginNames(t *testing.T) map[string]bool {
	t.Helper()

	byPackage := map[string]string{}
	for name, constructor := range pluginCatalog {
		typ := reflect.TypeOf(constructor())
		if typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		byPackage[typ.PkgPath()] = name
	}

	files, err := filepath.Glob(filepath.Join(groundSources, "*.go"))
	require.NoError(t, err)
	require.NotEmpty(t, files, "no Go file under %s; the ground has moved", groundSources)

	ground := map[string]bool{}
	found := 0
	fset := token.NewFileSet()
	for _, path := range files {
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		require.NoError(t, err, "%s does not parse", path)

		imports := map[string]string{}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			require.NoError(t, err)
			local := filepath.Base(importPath)
			if spec.Name != nil {
				local = spec.Name.Name
			}
			imports[local] = importPath
		}

		ast.Inspect(file, func(node ast.Node) bool {
			spec, ok := node.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, ident := range spec.Names {
				if ident.Name != groundPluginsVar {
					continue
				}
				found++
				require.Len(t, spec.Values, len(spec.Names),
					"%s at %s carries no value this test can read", groundPluginsVar, fset.Position(ident.Pos()))
				list, ok := spec.Values[i].(*ast.CompositeLit)
				require.True(t, ok, "%s at %s is not a literal list", groundPluginsVar, fset.Position(ident.Pos()))
				for _, element := range list.Elts {
					ground[groundElement(t, fset, element, imports, byPackage)] = true
				}
			}

			return true
		})
	}
	require.Equal(t, 1, found, "%s is declared %d times under %s", groundPluginsVar, found, groundSources)
	require.NotEmpty(t, ground, "the ground names no plugin")

	return ground
}

// groundElement answers the catalog name one element of the ground's list
// spells: `pkg.Name`, where pkg is a plugin package in the catalog.
func groundElement(
	t *testing.T, fset *token.FileSet, element ast.Expr, imports, byPackage map[string]string,
) string {
	t.Helper()

	at := fset.Position(element.Pos())
	selector, ok := element.(*ast.SelectorExpr)
	require.True(t, ok, "the element at %s is not a plugin package's Name", at)
	pkg, ok := selector.X.(*ast.Ident)
	require.True(t, ok && selector.Sel.Name == "Name", "the element at %s is not a plugin package's Name", at)
	importPath, ok := imports[pkg.Name]
	require.True(t, ok, "the element at %s names %s, which its file does not import", at, pkg.Name)
	name, ok := byPackage[importPath]
	require.True(t, ok, "the element at %s names %s, which is not a plugin in the catalog", at, importPath)

	return name
}
