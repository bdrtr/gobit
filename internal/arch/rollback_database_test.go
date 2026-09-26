package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestARollbackRunsInADatabaseOfItsOwn holds every test that rolls a migration
// back to an address the test itself made.
//
// A rollback drops the schema the rest of its package writes into. Against the
// address the package shares — a package-level variable its TestMain fills —
// it rewinds the other tests' rows and passes or fails by which of them ran
// first. D135 and D141 were two such tests, each found only when a down
// migration began to refuse on data; ten more had the same shape. The address
// a rollback may use is one declared inside the test: in practice
// internal/testdb's.
//
// The check reads the call, not the helper: MigrateDown's address argument may
// not be a package-level identifier the enclosing function does not declare
// again.
func TestARollbackRunsInADatabaseOfItsOwn(t *testing.T) {
	checked := 0
	packageVars := map[string]map[string]bool{}

	for _, path := range goFiles(t, repoRoot) {
		if !strings.HasSuffix(path, "_test.go") {
			continue
		}
		dir := filepath.Dir(path)
		if _, ok := packageVars[dir]; !ok {
			packageVars[dir] = topLevelNames(t, dir)
		}

		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, path, nil, 0)
		require.NoError(t, err, "%s could not be parsed", path)

		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			locals := declaredIn(fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || calledName(call) != "MigrateDown" || len(call.Args) < 2 {
					return true
				}
				checked++
				address, ok := call.Args[1].(*ast.Ident)
				if !ok || locals[address.Name] || !packageVars[dir][address.Name] {
					return true
				}
				t.Errorf("%s: %s rolls a migration back against %s, the address its package "+
					"shares.\nThe rollback drops the schema every other test of the package "+
					"writes into, and rewinds their rows (D141). Run it in a database of the "+
					"test's own: dsn := testdb.New(t, %s, \"<name>\").",
					fset.Position(call.Pos()), fn.Name.Name, address.Name, address.Name)

				return true
			})
		}
	}

	require.Positive(t, checked,
		"no MigrateDown call was found in any test, so this audit proved nothing.\n"+
			"The walk or the call's name may have moved; a gate that reads no rollback cannot "+
			"catch one against a shared database.")
}

// calledName is the name a call calls: MigrateDown for both db.MigrateDown and
// MigrateDown inside core/db.
func calledName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fun.Sel.Name
	case *ast.Ident:
		return fun.Name
	default:
		return ""
	}
}

// topLevelNames are the variables and constants declared at a package's top
// level, across all of its files.
func topLevelNames(t *testing.T, dir string) map[string]bool {
	t.Helper()

	names := map[string]bool{}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "%s could not be read", dir)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, entry.Name()), nil, 0)
		require.NoError(t, err, "%s could not be parsed", entry.Name())
		for _, decl := range parsed.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || (gen.Tok != token.VAR && gen.Tok != token.CONST) {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, name := range value.Names {
					names[name.Name] = true
				}
			}
		}
	}

	return names
}

// declaredIn are the names a function declares anywhere in it: its and its
// closures' parameters, := and var.
func declaredIn(fn *ast.FuncDecl) map[string]bool {
	names := map[string]bool{}
	addFields := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			for _, name := range field.Names {
				names[name.Name] = true
			}
		}
	}
	addFields(fn.Type.Params)
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncLit:
			addFields(node.Type.Params)
		case *ast.AssignStmt:
			if node.Tok == token.DEFINE {
				for _, lhs := range node.Lhs {
					if ident, ok := lhs.(*ast.Ident); ok {
						names[ident.Name] = true
					}
				}
			}
		case *ast.ValueSpec:
			for _, name := range node.Names {
				names[name.Name] = true
			}
		}

		return true
	})

	return names
}
