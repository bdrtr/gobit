package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// benchbudgetPath is the package a budget table is written against.
const benchbudgetPath = "internal/benchbudget"

// benchmarkFloor is the smallest population that could be the real one.
//
// Five benchmarks ship today. The floor is under it for the reason every floor
// in this package is under its subject: a number that tracks the surface has to
// be edited on every ordinary change, and a number edited that often stops being
// read. What it catches is the WALK having stopped working.
const benchmarkFloor = 3

// benchmarksWithoutABudget are the benchmarks that deliberately carry no ceiling.
//
// It is empty, and it is kept empty rather than deleted. A benchmark whose
// allocation count is a property of its input rather than of the code — one
// that measures a fixture, or one written to answer a single question and kept
// for the record — has a case for being here, and the case has to be WRITTEN.
// A map that did not exist would make the only way out of this gate "edit the
// gate", and an exemption nobody can see is the same as no gate at all.
var benchmarksWithoutABudget = map[string]string{}

// TestEveryBenchmarkCarriesAnAllocationBudget derives its population from the
// tree and not from a list.
//
// # What the gate is for
//
// A benchmark prints a number. Nothing in this repository read those numbers:
// `make bench` was a lane a person ran by hand, and between two runs of it a
// change could double the allocations of the cart arithmetic without a single
// test going red. `internal/benchbudget` is the half that fails; this is the
// half that makes sure the failing half covers everything.
//
// # Why the population is walked and not listed
//
// The three files that hold benchmarks today are named `*_bench_test.go`, and
// selecting the population by that suffix is the obvious and wrong way to do it:
// a benchmark written beside the tests it belongs to — the ordinary place to put
// one — would be outside the population, and the gate would report a clean tree
// it had never looked at. The unit here is the DECLARATION, so a benchmark is in
// the population wherever it is written.
func TestEveryBenchmarkCarriesAnAllocationBudget(t *testing.T) {
	benchmarks, budgets, runners := benchmarkPopulation(t)

	require.GreaterOrEqual(t, len(benchmarks), benchmarkFloor,
		"only %d benchmarks were found in the tree; the walk has gone blind, and an empty "+
			"population agrees with an empty budget table", len(benchmarks))

	for _, pkg := range slices.Sorted(maps.Keys(benchmarks)) {
		names := benchmarks[pkg]

		var unpriced []string
		for _, name := range names {
			if _, exempt := benchmarksWithoutABudget[pkg+"."+name]; exempt {
				continue
			}
			if !slices.Contains(budgets[pkg], name) {
				unpriced = append(unpriced, name)
			}
		}

		assert.Emptyf(t, unpriced,
			"%s declares benchmarks with no allocation budget: %s.\n"+
				"Add a benchbudget.Budget for each, or write down in benchmarksWithoutABudget why it has none.",
			pkg, strings.Join(unpriced, ", "))

		// A table nothing calls is decoration, and it would satisfy the check
		// above while measuring nothing. The gate therefore asks for the CALL as
		// well as the entries.
		assert.Containsf(t, runners, pkg,
			"%s declares an allocation budget but never calls benchbudget.Check, "+
				"so no test in that package runs it", pkg)
	}

	for name := range benchmarksWithoutABudget {
		pkg, bench, ok := strings.Cut(name, ".")
		require.Truef(t, ok, "%q is not a package-qualified benchmark name", name)
		assert.Containsf(t, benchmarks[pkg], bench,
			"benchmarksWithoutABudget names %s, which no longer exists; an exemption for a "+
				"benchmark that is gone hides the next one that takes its name", name)
	}
}

// benchmarkPopulation reads the tree once and returns three things: the
// benchmarks each package declares, the budget entries each package writes, and
// the packages that actually call [github.com/bdrtr/gobit/internal/benchbudget.Check].
//
// The three are gathered together because they are three readings of the same
// files, and a second walk is a second chance for the two to disagree about
// which files they saw.
func benchmarkPopulation(t *testing.T) (benchmarks, budgets map[string][]string, runners []string) {
	t.Helper()

	benchmarks = map[string][]string{}
	budgets = map[string][]string{}

	fset := token.NewFileSet()
	for _, file := range goFiles(t, repoRoot) {
		if !strings.HasSuffix(file, "_test.go") {
			continue
		}

		parsed, err := parser.ParseFile(fset, file, nil, 0)
		require.NoErrorf(t, err, "%s could not be parsed", file)

		pkg := filepath.ToSlash(filepath.Dir(strings.TrimPrefix(filepath.ToSlash(file), repoRoot+"/")))
		local, imported := benchbudgetLocalName(parsed)

		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Benchmark") {
				continue
			}
			if !takesABenchmark(fn) {
				continue
			}
			benchmarks[pkg] = append(benchmarks[pkg], fn.Name.Name)
		}

		if !imported {
			continue
		}

		ast.Inspect(parsed, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.CompositeLit:
				budgets[pkg] = append(budgets[pkg], budgetEntryNames(n, local)...)
			case *ast.CallExpr:
				if selectorIs(n.Fun, local, "Check") && !slices.Contains(runners, pkg) {
					runners = append(runners, pkg)
				}
			}
			return true
		})
	}

	return benchmarks, budgets, runners
}

// takesABenchmark reports whether a function has the shape `go test` runs.
//
// The name prefix alone is not the shape: a helper called `BenchmarkInput` that
// builds a fixture is not a benchmark, and counting it would demand a budget for
// something that never runs.
func takesABenchmark(fn *ast.FuncDecl) bool {
	params := fn.Type.Params.List
	if len(params) != 1 {
		return false
	}
	star, ok := params[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	return selectorIs(star.X, "testing", "B")
}

// budgetEntryNames returns the benchmarks a composite literal prices.
//
// It matches the ENTRY rather than the variable holding the entries, so a table
// keeps working whatever it is called and wherever it is declared — including
// inline at the call, which is how every table in this tree is written today.
//
// Both shapes have to be read, and the first draft of this gate read only one:
// a `[]benchbudget.Budget{{...}}` literal names its element type ONCE, and the
// entries inside it carry no type at all. Reading only the typed form found
// nothing and reported five priced benchmarks as unpriced — the loud direction,
// which is the only reason it was noticed on the first run.
func budgetEntryNames(lit *ast.CompositeLit, local string) []string {
	if array, ok := lit.Type.(*ast.ArrayType); ok && selectorIs(array.Elt, local, "Budget") {
		var names []string
		for _, elt := range lit.Elts {
			entry, ok := elt.(*ast.CompositeLit)
			if !ok {
				continue
			}
			if name, ok := budgetName(entry); ok {
				names = append(names, name)
			}
		}
		return names
	}

	if selectorIs(lit.Type, local, "Budget") {
		if name, ok := budgetName(lit); ok {
			return []string{name}
		}
	}
	return nil
}

// budgetName reads the Name field out of one budget entry.
func budgetName(lit *ast.CompositeLit) (string, bool) {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "Name" {
			continue
		}
		value, ok := kv.Value.(*ast.BasicLit)
		if !ok || value.Kind != token.STRING {
			continue
		}
		name, err := strconv.Unquote(value.Value)
		if err != nil {
			return "", false
		}
		return name, true
	}
	return "", false
}

// selectorIs reports whether an expression is exactly `pkg.name`.
func selectorIs(expr ast.Expr, pkg, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == pkg
}

// benchbudgetLocalName returns the name a file refers to the budget package by.
//
// An import may be renamed, and a gate that assumed the default name would stop
// seeing a table the day somebody aliased it — the quiet direction, where the
// gate reports a benchmark as unpriced or, worse, a package as having no table
// to run.
func benchbudgetLocalName(file *ast.File) (local string, imported bool) {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || !strings.HasSuffix(path, "/"+benchbudgetPath) {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name, true
		}
		return "benchbudget", true
	}
	return "", false
}
