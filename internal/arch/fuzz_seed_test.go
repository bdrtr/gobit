package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fuzzSeedFloor is the smallest seed corpus a target may ship with.
//
// Three is not a quality bar, it is a statement about which lane runs. Outside
// `make fuzz`, a fuzz target executes its SEEDS and nothing else: `go test`
// runs f.Add's inputs as ordinary sub-tests and never generates one. A target
// with no seeds therefore runs the zero value once on every build in this
// repository and is a fuzz target only on the day somebody remembers to fuzz it.
const fuzzSeedFloor = 3

// fuzzTargetFloor is the smallest population that could be the real one.
const fuzzTargetFloor = 2

// fuzzTargetsWithoutSeeds are the targets that deliberately ship no corpus.
//
// It is empty, and it is kept empty rather than deleted. A target whose input
// space is small enough to be covered from nothing has a case for being here,
// and the case has to be WRITTEN — otherwise the only way past this gate is to
// edit it.
var fuzzTargetsWithoutSeeds = map[string]string{}

// TestEveryFuzzTargetIsSeeded derives its population from the tree.
//
// The gate is the same shape as the allocation-budget one and for the same
// reason: a target that cannot run in the ordinary lane is a measurement
// waiting for somebody's attention, and this repository has already found what
// an unwatched measurement is worth.
func TestEveryFuzzTargetIsSeeded(t *testing.T) {
	seeds := fuzzTargetSeedCounts(t)

	require.GreaterOrEqual(t, len(seeds), fuzzTargetFloor,
		"only %d fuzz targets were found in the tree; the walk has gone blind, and an empty "+
			"population is seeded by definition", len(seeds))

	for _, name := range slices.Sorted(maps.Keys(seeds)) {
		if _, exempt := fuzzTargetsWithoutSeeds[name]; exempt {
			continue
		}
		assert.GreaterOrEqualf(t, seeds[name], fuzzSeedFloor,
			"%s ships %d seeds and the floor is %d; outside `make fuzz` a target runs its seeds "+
				"and nothing else, so this one barely runs at all", name, seeds[name], fuzzSeedFloor)
	}

	for name := range fuzzTargetsWithoutSeeds {
		assert.Containsf(t, seeds, name,
			"fuzzTargetsWithoutSeeds names %s, which no longer exists; an exemption for a target "+
				"that is gone hides the next one that takes its name", name)
	}
}

// fuzzTargetSeedCounts maps every fuzz target to the number of seeds it adds.
//
// A target that never calls f.Fuzz is not counted as a target with zero seeds —
// it is not a target at all, and reporting it here would send a reader looking
// for a corpus rather than for the missing call.
func fuzzTargetSeedCounts(t *testing.T) map[string]int {
	t.Helper()

	counts := map[string]int{}

	fset := token.NewFileSet()
	for _, file := range goFiles(t, repoRoot) {
		if !strings.HasSuffix(file, "_test.go") {
			continue
		}

		parsed, err := parser.ParseFile(fset, file, nil, 0)
		require.NoErrorf(t, err, "%s could not be parsed", file)

		pkg := filepath.ToSlash(filepath.Dir(strings.TrimPrefix(filepath.ToSlash(file), repoRoot+"/")))

		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Fuzz") {
				continue
			}
			receiver, ok := fuzzReceiver(fn)
			if !ok {
				continue
			}

			adds, fuzzes := 0, false
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch {
				case selectorIs(call.Fun, receiver, "Add"):
					adds++
				case selectorIs(call.Fun, receiver, "Fuzz"):
					fuzzes = true
				}
				return true
			})

			if fuzzes {
				counts[pkg+"."+fn.Name.Name] = adds
			}
		}
	}

	return counts
}

// fuzzReceiver returns the name a target calls its *testing.F by.
//
// The parameter is read rather than assumed to be `f`: a target that named it
// anything else would look seedless to a gate matching on the letter, and the
// failure would be a demand for seeds that are already there.
func fuzzReceiver(fn *ast.FuncDecl) (string, bool) {
	params := fn.Type.Params.List
	if len(params) != 1 || len(params[0].Names) != 1 {
		return "", false
	}
	star, ok := params[0].Type.(*ast.StarExpr)
	if !ok || !selectorIs(star.X, "testing", "F") {
		return "", false
	}
	return params[0].Names[0].Name, true
}
